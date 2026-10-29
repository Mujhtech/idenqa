//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"reflect"
	"testing"
	"time"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
	"github.com/Mujhtech/idenqa/internal/admission"
	admissionpg "github.com/Mujhtech/idenqa/internal/admission/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	reviewpg "github.com/Mujhtech/idenqa/internal/review/postgres"
)

func testAdmittedLinkedRecapture(t *testing.T, f captureAcceptanceFixture, value review.Case, candidate review.Evaluation) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{53}, ed25519.SeedSize))
	trust, err := admission.NewPolicy(admission.Config{DeploymentID: "recapture_fixture", Region: value.Region, MaximumLifetime: time.Hour, Issuers: map[string]ed25519.PublicKey{"fixture": key.Public().(ed25519.PublicKey)}})
	if err != nil {
		t.Fatal(err)
	}
	control, err := admissionpg.New(f.admin, trust)
	if err != nil {
		t.Fatal(err)
	}
	gate, err := admissionpg.New(f.runtime, trust)
	if err != nil {
		t.Fatal(err)
	}
	store, err := reviewpg.NewRecaptureStore(f.runtime, integrationProtector{}, f.catalog, fixedIntegrationClock{now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	store.WithSessionAdmission(gate)
	var actorText string
	if err = f.admin.Native().QueryRow(t.Context(), `SELECT created_by FROM idenqa.processing_authorities WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.declaration.ID().String()).Scan(&actorText); err != nil {
		t.Fatal(err)
	}
	actor, err := id.ParseAPIKey(actorText)
	if err != nil {
		t.Fatal(err)
	}
	mutation := newIntegrationSessionMutation(t, f.ids, f.scope.ID(), actor, f.creation.Session.ProfileID(), candidate.Snapshot.Policy().ID, f.now, "admitted-recapture", value.Region)
	mutation.Idempotency = integrationIdempotencyRequest(t, f.scope.ID(), actor, review.OperationRecapture, "admitted-recapture", []byte(`{"case":"`+value.ID.String()+`"}`), f.now)
	counts := func() []int64 {
		t.Helper()
		out := make([]int64, 6)
		err := f.admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.verification_sessions),(SELECT count(*) FROM idenqa.capture_tokens),(SELECT count(*) FROM idenqa.outcome_tokens),(SELECT count(*) FROM idenqa.review_recaptures),(SELECT count(*) FROM idenqa.outbox_events),(SELECT count(*) FROM idenqa.idempotency_records)`).Scan(&out[0], &out[1], &out[2], &out[3], &out[4], &out[5])
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	baseline := counts()
	assertRollback := func(want error) {
		t.Helper()
		_, err := store.CreateRecapture(t.Context(), f.scope, value.ID, value.Version, mutation)
		if !errors.Is(err, want) {
			t.Fatalf("recapture admission want=%v got=%v", want, err)
		}
		if got := counts(); !reflect.DeepEqual(got, baseline) {
			t.Fatalf("denial committed child/token/lineage/outbox/idempotency: %+v != %+v", got, baseline)
		}
	}
	assertRollback(admission.ErrUnavailable)
	ordinary, _ := admissionv1.VerificationOperation(f.creation.Session.ProfileID().String(), f.creation.Session.ProfileRevision(), candidate.Snapshot.Policy().ID.String())
	recapture, _ := admissionv1.RecaptureOperation(f.creation.Session.ProfileID().String(), f.creation.Session.ProfileRevision(), candidate.Snapshot.Policy().ID.String())
	now := time.Now().UTC().Truncate(time.Microsecond)
	snapshot := admissionv1.Snapshot{Schema: admissionv1.Schema, ID: "recapture1", Scope: admissionv1.Scope{TenantID: f.scope.ID().String(), DeploymentID: "recapture_fixture", Region: value.Region}, Revision: 1, Issuer: "fixture", IssuedAt: now.Add(-time.Second), ValidFrom: now.Add(-time.Second), ValidUntil: now.Add(30 * time.Minute), Status: "active", Capabilities: map[string]int64{"recapture": 1}, Limits: []admissionv1.Limit{{Kind: "count", Key: "children", Maximum: 1}}, Rules: []admissionv1.Rule{{Operation: ordinary, Capabilities: map[string]int64{"recapture": 1}, Budgets: []admissionv1.Budget{{Kind: "count", Key: "children", Quantity: 1, Lifetime: "delete"}}}}}
	install := func() {
		t.Helper()
		snapshot.Proof = ed25519.Sign(key, snapshot.SigningBytes())
		if err = control.Install(t.Context(), snapshot); err != nil {
			t.Fatal(err)
		}
	}
	install()
	assertRollback(admission.ErrEntitlement)
	snapshot.ID = "recapture2"
	snapshot.Revision = 2
	snapshot.Rules[0].Operation = recapture
	snapshot.Limits[0].Maximum = 0
	install()
	assertRollback(admission.ErrCeiling)
	snapshot.ID = "recapture3"
	snapshot.Revision = 3
	snapshot.Limits[0].Maximum = 1
	install()
	child, err := store.CreateRecapture(t.Context(), f.scope, value.ID, value.Version, mutation)
	if err != nil {
		t.Fatal(err)
	}
	if child.Session.ID() != mutation.SessionID || child.Session.ProfileDigest() != f.creation.Session.ProfileDigest() {
		t.Fatal("admitted lineage changed source configuration")
	}
	var used int64
	if err = f.admin.Native().QueryRow(t.Context(), `SELECT used FROM idenqa.admission_counters WHERE tenant_id=$1 AND kind='count' AND key='children'`, f.scope.ID().String()).Scan(&used); err != nil || used != 1 {
		t.Fatalf("recapture not reserved exactly once %d %v", used, err)
	}
	err = f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, f.scope.ID().String()); err != nil {
			return err
		}
		return gate.AdmitWithin(ctx, tx, admission.Operation{TenantID: f.scope.ID().String(), ID: child.Session.ID().String(), Rule: ordinary})
	})
	if !errors.Is(err, admission.ErrInvalid) {
		t.Fatalf("recapture borrowed ordinary creation: %v", err)
	}
	snapshot.ID = "recapture4"
	snapshot.Revision = 4
	snapshot.Status = "revoked"
	install()
	for _, idem := range []string{"admitted-recapture", "another-recature-key"} {
		retry := newIntegrationSessionMutation(t, f.ids, f.scope.ID(), actor, mutation.ProfileID, mutation.PolicyID, f.now, idem, value.Region)
		retry.Idempotency = integrationIdempotencyRequest(t, f.scope.ID(), actor, review.OperationRecapture, idem, []byte(`{"case":"`+value.ID.String()+`"}`), f.now)
		restored, err := store.CreateRecapture(t.Context(), f.scope, value.ID, value.Version, retry)
		if err != nil || restored.Session.ID() != child.Session.ID() || restored.Credential.ID() != child.Credential.ID() {
			t.Fatalf("accepted replay lost after revocation: %v", err)
		}
	}
	if err = f.admin.Native().QueryRow(t.Context(), `SELECT used FROM idenqa.admission_counters WHERE tenant_id=$1 AND kind='count' AND key='children'`, f.scope.ID().String()).Scan(&used); err != nil || used != 1 {
		t.Fatalf("replay reserved additional child %d %v", used, err)
	}
	var stored string
	if err = f.admin.Native().QueryRow(t.Context(), `SELECT rule FROM idenqa.admission_operations WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), child.Session.ID().String()).Scan(&stored); err != nil || stored != recapture {
		t.Fatalf("wrong durable workflow %s %v", stored, err)
	}
}
