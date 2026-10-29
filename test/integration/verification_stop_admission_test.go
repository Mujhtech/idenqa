//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"strings"
	"testing"
	"time"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
	"github.com/Mujhtech/idenqa/internal/admission"
	admissionpostgres "github.com/Mujhtech/idenqa/internal/admission/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestVerificationStopAdmissionCancellation(t *testing.T) {
	for _, kind := range []verification.RunnerKind{verification.RunnerProvider, verification.RunnerModel} {
		for _, scenario := range []string{"undispatched", "scheduled", "claimed", "reconciliation"} {
			t.Run(string(kind)+"/"+scenario, func(t *testing.T) {
				f := newStopAdmissionFixture(t, kind)
				started := f.now
				if scenario == "scheduled" {
					started = f.now.Add(2 * time.Minute)
				}
				check, attempt := f.reserve(t, f.verificationID, started)
				f.assertBudgets(t, 1, 1, 1)
				if scenario == "claimed" || scenario == "reconciliation" {
					var result any
					if scenario == "reconciliation" {
						result = []byte(`{"outcome":"failed","failure":{"retry":"reconcile"}}`)
					}
					_, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.`+string(kind)+`_dispatches
(tenant_id,attempt_id,request_digest,claimed_at,result_body) VALUES($1,$2,$3,$4,$5)`, f.scope.ID().String(), attempt.ID.String(), attempt.Provenance.RequestDigest, f.now, result)
					if err != nil {
						t.Fatal(err)
					}
				}
				actor, err := f.ids.NewAPIKey()
				if err != nil {
					t.Fatal(err)
				}
				at := f.now.Add(time.Minute)
				mutation := cancelMutation(t, f.scope, actor, f.verificationID, 1, "cancel-admitted", at)
				source := lifecycleClock{now: at}
				if scenario == "undispatched" {
					// Fail the parent outbox write after child closure and its trigger
					// releases. Every effect must roll back with that parent failure.
					collision, err := f.ids.NewEvent()
					if err != nil {
						t.Fatal(err)
					}
					if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.outbox_events
(id,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,payload,occurred_at,created_at)
VALUES($1,$2,'verification',$3,99,'synthetic.conflict.v1',1,'{}',$4,$4)`, collision.String(), f.scope.ID().String(), f.verificationID.String(), f.now); err != nil {
						t.Fatal(err)
					}
					failing, err := verificationpostgres.NewStopStore(f.runtime, integrationProtector{}, &stopAdmissionEvents{first: collision, rest: f.ids}, source)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := failing.Cancel(t.Context(), f.scope, mutation); err == nil {
						t.Fatal("parent persistence failure committed cancellation")
					}
					f.assertCheck(t, check.ID, verification.CheckRunning, verification.AttemptRunning, time.Time{})
					f.assertBudgets(t, 1, 1, 1)
					var parent string
					var replays int
					if err := f.admin.Native().QueryRow(t.Context(), `SELECT state,
(SELECT count(*) FROM idenqa.idempotency_records WHERE tenant_id=$1 AND idempotency_key='cancel-admitted')
FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.verificationID.String()).Scan(&parent, &replays); err != nil || parent != "collecting" || replays != 0 {
						t.Fatalf("partial cancellation survived rollback: parent=%s replays=%d: %v", parent, replays, err)
					}
				}
				stops, err := verificationpostgres.NewStopStore(f.runtime, integrationProtector{}, f.ids, source)
				if err != nil {
					t.Fatal(err)
				}
				receipt, err := stops.Cancel(t.Context(), f.scope, mutation)
				if err != nil || receipt.State != verification.SessionStateCancelled || !receipt.OccurredAt.Equal(at) {
					t.Fatalf("cancel admitted work: %+v: %v", receipt, err)
				}
				replay, err := stops.Cancel(t.Context(), f.scope, mutation)
				if err != nil || replay != receipt {
					t.Fatalf("cancel replay changed its receipt: %+v: %v", replay, err)
				}
				if scenario == "claimed" || scenario == "reconciliation" {
					f.assertCheck(t, check.ID, verification.CheckRunning, verification.AttemptRunning, time.Time{})
					f.assertBudgets(t, 1, 1, 1)
					return
				}
				finished := at
				if started.After(finished) {
					finished = started
				}
				f.assertCheck(t, check.ID, verification.CheckCancelled, verification.AttemptCancelled, finished)
				f.assertBudgets(t, 0, 1, 1)
				// A new verification can use the released physical slot. Retained
				// metadata and window usage are charged independently of closure.
				sibling := f.sibling(t)
				f.reserve(t, sibling, f.now)
				f.assertBudgets(t, 1, 2, 2)
			})
		}
	}
}

type stopAdmissionFixture struct {
	lifecycleFixture
	kind     verification.RunnerKind
	gate     *admissionpostgres.Store
	workflow string
}

type stopAdmissionEvents struct {
	first id.Event
	rest  *id.Generator
}

func (events *stopAdmissionEvents) NewEvent() (id.Event, error) {
	if !events.first.IsZero() {
		first := events.first
		events.first = id.Event{}
		return first, nil
	}
	return events.rest.NewEvent()
}

func newStopAdmissionFixture(t *testing.T, kind verification.RunnerKind) stopAdmissionFixture {
	t.Helper()
	f := stopAdmissionFixture{lifecycleFixture: newLifecycleFixture(t), kind: kind}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{31}, ed25519.SeedSize))
	policy, err := admission.NewPolicy(admission.Config{DeploymentID: "dep_stop", Region: "local", MaximumLifetime: 2 * time.Hour, Issuers: map[string]ed25519.PublicKey{"fixture": key.Public().(ed25519.PublicKey)}})
	if err != nil {
		t.Fatal(err)
	}
	f.gate, err = admissionpostgres.New(f.runtime, policy)
	if err != nil {
		t.Fatal(err)
	}
	control, err := admissionpostgres.New(f.admin, policy)
	if err != nil {
		t.Fatal(err)
	}
	f.workflow, err = admissionv1.VerificationOperation("fixture_profile", 1, "fixture_policy")
	if err != nil {
		t.Fatal(err)
	}
	resource, err := admissionv1.ResourceOperation(string(kind), "fixture", "1.0.0", "fixture_digest", "fixture.check")
	if err != nil {
		t.Fatal(err)
	}
	dependent, err := admissionv1.DependentOperation(f.workflow, resource)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := admissionv1.Snapshot{Schema: admissionv1.Schema, ID: "stop_snapshot", Scope: admissionv1.Scope{TenantID: f.scope.ID().String(), DeploymentID: "dep_stop", Region: "local"}, Revision: 1, Issuer: "fixture", IssuedAt: f.now.Add(-time.Minute), ValidFrom: f.now.Add(-time.Minute), ValidUntil: f.now.Add(time.Hour), Status: "active", Capabilities: map[string]int64{"work": 1}, Limits: []admissionv1.Limit{
		{Kind: "capacity", Key: "slot", Maximum: 1},
		{Kind: "concurrency", Key: "slot", Maximum: 1},
		{Kind: "count", Key: "metadata", Maximum: 10},
		{Kind: "usage", Key: "work", Maximum: 10, Window: 24 * time.Hour},
	}, Rules: []admissionv1.Rule{{Operation: dependent, Capabilities: map[string]int64{"work": 1}, Budgets: []admissionv1.Budget{
		{Kind: "capacity", Key: "slot", Quantity: 1, Lifetime: "terminal"},
		{Kind: "concurrency", Key: "slot", Quantity: 1, Lifetime: "terminal"},
		{Kind: "count", Key: "metadata", Quantity: 1, Lifetime: "delete"},
		{Kind: "usage", Key: "work", Quantity: 1, Lifetime: "window"},
	}}}}
	snapshot.Proof = ed25519.Sign(key, snapshot.SigningBytes())
	if err := control.Install(t.Context(), snapshot); err != nil {
		t.Fatal(err)
	}
	f.acceptWorkflow(t, f.verificationID)
	return f
}

func (f stopAdmissionFixture) acceptWorkflow(t *testing.T, verificationID id.Verification) {
	t.Helper()
	// The accepted workflow is fixture history; the dependent resource below
	// enters through the real runtime admission port and all migration triggers.
	if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.admission_operations
(tenant_id,id,rule,digest,snapshot_revision,state,accepted_at) VALUES($1,$2,$3,$4,1,'active',$5)`, f.scope.ID().String(), verificationID.String(), f.workflow, strings.Repeat("a", 64), f.now); err != nil {
		t.Fatal(err)
	}
}

func (f stopAdmissionFixture) sibling(t *testing.T) id.Verification {
	t.Helper()
	verificationID, err := f.ids.NewVerification()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.verification_sessions
(id,tenant_id,state,version,source_profile_id,source_profile_revision,source_profile_digest,requirements,created_at,updated_at,expires_at)
SELECT $1,tenant_id,'collecting',1,source_profile_id,source_profile_revision,source_profile_digest,requirements,created_at,created_at,expires_at
FROM idenqa.verification_sessions WHERE tenant_id=$2 AND id=$3`, verificationID.String(), f.scope.ID().String(), f.verificationID.String()); err != nil {
		t.Fatal(err)
	}
	f.acceptWorkflow(t, verificationID)
	return verificationID
}

func (f stopAdmissionFixture) reserve(t *testing.T, verificationID id.Verification, started time.Time) (verification.Check, verification.Attempt) {
	t.Helper()
	checks, err := verificationpostgres.NewCheckStore(f.runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	checkID, err := f.ids.NewCheck()
	if err != nil {
		t.Fatal(err)
	}
	check, err := verification.NewCheck(checkID, f.scope.ID(), verificationID, "fixture.check", f.now)
	if err != nil {
		t.Fatal(err)
	}
	eventID, err := f.ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if err := checks.CreateCheck(t.Context(), f.scope, check, eventID); err != nil {
		t.Fatal(err)
	}
	attempt := newExecutionAttempt(t, f.ids, 1, 1, started)
	attempt.RunnerKind = f.kind
	if err := check.BeginAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	eventID, err = f.ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checks.SaveCheck(t.Context(), f.scope, verification.CheckCommit{Check: check, ExpectedVersion: 1, EventID: eventID}); err != nil {
		t.Fatal(err)
	}
	body := map[string]any{"provider_id": "fixture", "adapter": map[string]string{"adapter_version": "1.0.0", "package_digest": "fixture_digest"}, "check": "fixture.check"}
	if f.kind == verification.RunnerModel {
		body = map[string]any{"model_registration_id": "fixture", "provenance": map[string]string{"model_version": "1.0.0", "model_digest": "fixture_digest"}, "evaluation": "fixture.check"}
	}
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	err = f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, f.scope.ID().String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.`+string(f.kind)+`_requests
(tenant_id,attempt_id,verification_id,check_id,request_digest,request_body) VALUES($1,$2,$3,$4,$5,$6)`, f.scope.ID().String(), attempt.ID.String(), verificationID.String(), check.ID.String(), attempt.Provenance.RequestDigest, raw); err != nil {
			return err
		}
		return f.gate.AdmitResourceWithin(ctx, tx, admission.Resource{TenantID: f.scope.ID().String(), VerificationID: verificationID.String(), Kind: string(f.kind), ID: attempt.ID.String()})
	})
	if err != nil {
		t.Fatal(err)
	}
	return check, attempt
}

func (f stopAdmissionFixture) assertCheck(t *testing.T, checkID id.Check, state verification.CheckState, attemptState verification.AttemptState, finished time.Time) {
	t.Helper()
	checks, err := verificationpostgres.NewCheckStore(f.runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	check, err := checks.FindCheck(t.Context(), f.scope, checkID)
	if err != nil {
		t.Fatal(err)
	}
	attempts := check.Attempts()
	if check.State != state || len(attempts) != 1 || attempts[0].State != attemptState || !attempts[0].FinishedAt.Equal(finished) {
		t.Fatalf("persisted check state=%s attempts=%+v, want %s/%s finished %v", check.State, attempts, state, attemptState, finished)
	}
	if !finished.IsZero() && (!check.UpdatedAt.Equal(finished) || attempts[0].ResultDigest() == "" || attempts[0].Failure == nil || attempts[0].Failure.Code != "verification_cancelled") {
		t.Fatalf("cancellation lost its immutable terminal fields: %+v / %+v", check, attempts[0])
	}
}

func (f stopAdmissionFixture) assertBudgets(t *testing.T, live, metadata, usage int64) {
	t.Helper()
	for _, expected := range []struct {
		kind string
		used int64
	}{{"capacity", live}, {"concurrency", live}, {"count", metadata}, {"usage", usage}} {
		var used int64
		var dirty bool
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT coalesce(sum(used),0)::bigint,coalesce(bool_or(dirty),false)
FROM idenqa.admission_counters WHERE tenant_id=$1 AND kind=$2`, f.scope.ID().String(), expected.kind).Scan(&used, &dirty); err != nil || used != expected.used || dirty {
			t.Fatalf("%s used=%d dirty=%t, want %d: %v", expected.kind, used, dirty, expected.used, err)
		}
	}
}
