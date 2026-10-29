//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
	"github.com/Mujhtech/idenqa/internal/admission"
	admissionpg "github.com/Mujhtech/idenqa/internal/admission/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpg "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestOperationalAdmissionAtomicSessionAndLifetimeRecovery(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		ctx := t.Context()
		var now time.Time
		if err := f.admin.Native().QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
			t.Fatal(err)
		}
		now = now.UTC().Truncate(time.Microsecond)
		key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{29}, ed25519.SeedSize))
		policy, err := admission.NewPolicy(admission.Config{DeploymentID: "dep_fixture", Region: "local", MaximumLifetime: 2 * time.Hour, Issuers: map[string]ed25519.PublicKey{"fixture": key.Public().(ed25519.PublicKey)}})
		if err != nil {
			t.Fatal(err)
		}
		control, err := admissionpg.New(f.admin, policy)
		if err != nil {
			t.Fatal(err)
		}
		gate, err := admissionpg.New(f.runtime, policy)
		if err != nil {
			t.Fatal(err)
		}
		sessions, err := verificationpg.NewSessionStore(f.runtime, integrationProtector{}, f.catalog)
		if err != nil {
			t.Fatal(err)
		}
		sessions.WithAdmission(gate)
		actor, err := id.ParseAPIKey(f.creationMutation.Actor.String())
		if err != nil {
			t.Fatal(err)
		}
		mutation := func(key string) verification.SessionCreateMutation {
			return newIntegrationSessionMutation(t, f.ids, f.scope.ID(), actor, f.creation.Session.ProfileID(), f.creation.Session.PolicyID(), now, key)
		}
		first := mutation("admission-first")
		assertAbsent := func(m verification.SessionCreateMutation) {
			t.Helper()
			for _, query := range []string{
				`SELECT count(*) FROM idenqa.verification_sessions WHERE id=$1`,
				`SELECT count(*) FROM idenqa.capture_tokens WHERE verification_id=$1`,
				`SELECT count(*) FROM idenqa.outcome_tokens WHERE verification_id=$1`,
				`SELECT count(*) FROM idenqa.verification_session_audit WHERE verification_id=$1`,
				`SELECT count(*) FROM idenqa.outbox_events WHERE aggregate_id=$1`,
				`SELECT count(*) FROM idenqa.admission_operations WHERE id=$1`,
			} {
				var count int
				if err := f.admin.Native().QueryRow(ctx, query, m.SessionID.String()).Scan(&count); err != nil || count != 0 {
					t.Fatalf("rolled-back source %q count=%d: %v", query, count, err)
				}
			}
		}
		if _, err := sessions.Create(ctx, f.scope, first); !errors.Is(err, admission.ErrUnavailable) {
			t.Fatalf("missing policy: %v", err)
		}
		assertAbsent(first)
		rule, err := admissionv1.VerificationOperation(first.ProfileID.String(), f.creation.Session.ProfileRevision(), first.PolicyID.String())
		if err != nil {
			t.Fatal(err)
		}
		snapshot := admissionv1.Snapshot{Schema: admissionv1.Schema, ID: "snapshot_1", Scope: admissionv1.Scope{TenantID: f.scope.ID().String(), DeploymentID: "dep_fixture", Region: "local"}, Revision: 1, Issuer: "fixture", IssuedAt: now.Add(-time.Minute), ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour), Status: "active", Capabilities: map[string]int64{"standard": 1}, Limits: []admissionv1.Limit{
			{Kind: "capacity", Key: "work", Maximum: 10},
			{Kind: "concurrency", Key: "work", Maximum: 1},
			{Kind: "count", Key: "work", Maximum: 10},
			{Kind: "rate", Key: "work", Maximum: 10, Window: 24 * time.Hour},
			{Kind: "storage", Key: "work", Maximum: 100},
			{Kind: "usage", Key: "work", Maximum: 10, Window: 24 * time.Hour},
		}, Rules: []admissionv1.Rule{{Operation: rule, Capabilities: map[string]int64{"standard": 1}, Budgets: []admissionv1.Budget{
			{Kind: "capacity", Key: "work", Quantity: 1, Lifetime: "terminal"},
			{Kind: "concurrency", Key: "work", Quantity: 1, Lifetime: "terminal"},
			{Kind: "count", Key: "work", Quantity: 1, Lifetime: "delete"},
			{Kind: "rate", Key: "work", Quantity: 1, Lifetime: "window"},
			{Kind: "storage", Key: "work", Quantity: 1, Lifetime: "delete"},
			{Kind: "usage", Key: "work", Quantity: 1, Lifetime: "window"},
		}}}}
		sign := func(s admissionv1.Snapshot) admissionv1.Snapshot {
			s.Proof = ed25519.Sign(key, s.SigningBytes())
			return s
		}
		snapshot = sign(snapshot)
		if err := control.Install(ctx, snapshot); err != nil {
			t.Fatal(err)
		}
		if err := control.Install(ctx, snapshot); err != nil {
			t.Fatalf("lost installation acknowledgement: %v", err)
		}
		changed := snapshot
		changed.Status = "revoked"
		changed = sign(changed)
		if err := control.Install(ctx, changed); !errors.Is(err, admission.ErrConflict) {
			t.Fatalf("changed revision: %v", err)
		}
		future := snapshot
		future.ID = "snapshot_2"
		future.Revision = 2
		future.ValidFrom = now.Add(30 * time.Minute)
		future.ValidUntil = now.Add(90 * time.Minute)
		future = sign(future)
		if err := control.Install(ctx, future); err != nil {
			t.Fatal(err)
		}
		if _, err := f.admin.Native().Exec(ctx, `CREATE FUNCTION idenqa.reject_test_admission_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected downstream idempotency failure'; END; $$;
CREATE TRIGGER reject_test_admission_commit BEFORE UPDATE ON idenqa.idempotency_records FOR EACH ROW WHEN(NEW.idempotency_key='admission-downstream' AND NEW.state='completed') EXECUTE FUNCTION idenqa.reject_test_admission_commit()`); err != nil {
			t.Fatal(err)
		}
		downstream := mutation("admission-downstream")
		if _, err := sessions.Create(ctx, f.scope, downstream); err == nil {
			t.Fatal("downstream failure did not roll back admitted source")
		}
		assertAbsent(downstream)
		for _, table := range []string{"admission_counters", "admission_reservations", "admission_operations"} {
			var count int
			if err := f.admin.Native().QueryRow(ctx, `SELECT count(*) FROM idenqa.`+table).Scan(&count); err != nil || count != 0 {
				t.Fatalf("admission survived downstream failure in %s: %d: %v", table, count, err)
			}
		}
		var pending int
		if err := f.admin.Native().QueryRow(ctx, `SELECT count(*) FROM idenqa.idempotency_records WHERE idempotency_key='admission-downstream'`).Scan(&pending); err != nil || pending != 0 {
			t.Fatalf("idempotency survived rollback: %d: %v", pending, err)
		}
		if _, err := f.admin.Native().Exec(ctx, `DROP TRIGGER reject_test_admission_commit ON idenqa.idempotency_records; DROP FUNCTION idenqa.reject_test_admission_commit()`); err != nil {
			t.Fatal(err)
		}
		created, err := sessions.Create(ctx, f.scope, first)
		if err != nil {
			t.Fatalf("current snapshot shadowed by future revision: %v", err)
		}
		if _, err := sessions.Create(ctx, f.scope, first); err != nil {
			t.Fatalf("accepted create replay: %v", err)
		}
		summary, err := gate.Summary(ctx, snapshot.Scope)
		if err != nil || summary.Revision != 1 || len(summary.Limits) != 6 {
			t.Fatalf("bounded quota summary: %+v: %v", summary, err)
		}
		for _, counter := range summary.Limits {
			if counter.Used != 1 || counter.Dirty || counter.Remaining != counter.Maximum-1 {
				t.Fatalf("quota summary counter: %+v", counter)
			}
		}
		foreignSummaryScope := snapshot.Scope
		foreignSummaryScope.Region = "other"
		if _, err := gate.Summary(ctx, foreignSummaryScope); !errors.Is(err, admission.ErrInvalid) {
			t.Fatalf("foreign placement summary: %v", err)
		}
		counters := func() string {
			t.Helper()
			var result string
			if err := f.admin.Native().QueryRow(ctx, `SELECT jsonb_agg(jsonb_build_array(kind,key,window_key,used,dirty) ORDER BY kind,key,window_key)::text FROM idenqa.admission_counters WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&result); err != nil {
				t.Fatal(err)
			}
			return result
		}
		before := counters()
		second := mutation("admission-second")
		if _, err := sessions.Create(ctx, f.scope, second); !errors.Is(err, admission.ErrQuota) {
			t.Fatalf("quota denial: %v", err)
		}
		assertAbsent(second)
		if counters() != before {
			t.Fatal("partial multi-budget consumption survived rollback")
		}
		// Forced RLS is accompanied by application scope and rejects bypass roles.
		if err := f.admin.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, f.scope.ID().String()); err != nil {
				return err
			}
			return control.AdmitWithin(ctx, tx, admission.Operation{TenantID: f.scope.ID().String(), ID: first.SessionID.String(), Rule: rule})
		}); !errors.Is(err, admission.ErrInvalid) {
			t.Fatalf("bypass runtime: %v", err)
		}
		runtimeInstall := snapshot
		runtimeInstall.ID = "runtime_cannot_install"
		runtimeInstall.Revision = 3
		runtimeInstall = sign(runtimeInstall)
		if err := gate.Install(ctx, runtimeInstall); err == nil {
			t.Fatal("API runtime could install policy")
		}
		if err := f.runtime.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, f.scope.ID().String()+"other"); err != nil {
				return err
			}
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.admission_operations`).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				return fmt.Errorf("foreign operation count %d", count)
			}
			return gate.AdmitWithin(ctx, tx, admission.Operation{TenantID: f.scope.ID().String(), ID: first.SessionID.String(), Rule: rule})
		}); !errors.Is(err, admission.ErrInvalid) {
			t.Fatalf("cross-scope runtime: %v", err)
		}
		// Corrupt one projection as a privileged fault injection. Source closure
		// still succeeds, releases exactly once and marks the counter for repair.
		if _, err := f.admin.Native().Exec(ctx, `UPDATE idenqa.admission_counters SET used=0 WHERE tenant_id=$1 AND kind='concurrency'`, f.scope.ID().String()); err != nil {
			t.Fatal(err)
		}
		life, err := verificationpg.NewLifecycleStore(f.runtime, integrationProtector{}, fixedIntegrationClock{now: now.Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		event, _ := f.ids.NewEvent()
		task, _ := f.ids.NewTask()
		command := verification.LifecycleCommand{EventID: event, VerificationID: created.Session.ID(), ExpectedVersion: 1, Target: verification.SessionStateCancelled, ActorID: task.String(), OccurredAt: now.Add(time.Second)}
		if _, err := life.Apply(ctx, f.scope, command); err != nil {
			t.Fatalf("closure during projection drift: %v", err)
		}
		if _, err := life.Apply(ctx, f.scope, command); err != nil {
			t.Fatalf("closure replay: %v", err)
		}
		if _, err := sessions.Create(ctx, f.scope, second); !errors.Is(err, admission.ErrUnavailable) {
			t.Fatalf("dirty counter accepted work: %v", err)
		}
		driftSummary, err := gate.Summary(ctx, snapshot.Scope)
		if err != nil {
			t.Fatal(err)
		}
		for _, counter := range driftSummary.Limits {
			if counter.Kind == "concurrency" && (!counter.Dirty || counter.Remaining != 0) {
				t.Fatalf("dirty summary invented availability: %+v", counter)
			}
		}
		if _, err := f.admin.Native().Exec(ctx, `CREATE FUNCTION idenqa.reject_test_admission_repair() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected repair audit failure'; END; $$;
CREATE TRIGGER reject_test_admission_repair BEFORE INSERT ON idenqa.admission_events FOR EACH ROW WHEN(NEW.kind='counter_rebuilt') EXECUTE FUNCTION idenqa.reject_test_admission_repair()`); err != nil {
			t.Fatal(err)
		}
		if err := gate.RebuildCounter(ctx, f.scope.ID().String(), "concurrency", "work", 0, "operator_fixture", "change_fixture"); err == nil {
			t.Fatal("repair succeeded without durable audit")
		}
		var dirty bool
		if err := f.admin.Native().QueryRow(ctx, `SELECT dirty FROM idenqa.admission_counters WHERE tenant_id=$1 AND kind='concurrency'`, f.scope.ID().String()).Scan(&dirty); err != nil || !dirty {
			t.Fatalf("repair audit failure did not roll back projection: %v", err)
		}
		if _, err := f.admin.Native().Exec(ctx, `DROP TRIGGER reject_test_admission_repair ON idenqa.admission_events; DROP FUNCTION idenqa.reject_test_admission_repair()`); err != nil {
			t.Fatal(err)
		}
		if err := gate.RebuildCounter(ctx, f.scope.ID().String(), "concurrency", "work", 0, "operator_fixture", "change_fixture"); err != nil {
			t.Fatal(err)
		}
		var repaired bool
		if err := f.admin.Native().QueryRow(ctx, `SELECT counter_kind='concurrency' AND counter_key='work' AND counter_window=0 AND counter_before=0 AND counter_after=0 AND counter_was_dirty AND actor_id='operator_fixture' AND reason_reference='change_fixture' FROM idenqa.admission_events WHERE tenant_id=$1 AND kind='counter_rebuilt'`, f.scope.ID().String()).Scan(&repaired); err != nil || !repaired {
			t.Fatalf("repair audit cannot reconstruct target and result: %v", err)
		}
		acceptedSecond, err := sessions.Create(ctx, f.scope, second)
		if err != nil {
			t.Fatalf("recovered reservation: %v", err)
		}
		for _, kind := range []string{"count", "rate", "storage", "usage"} {
			var used int64
			if err := f.admin.Native().QueryRow(ctx, `SELECT used FROM idenqa.admission_counters WHERE tenant_id=$1 AND kind=$2`, f.scope.ID().String(), kind).Scan(&used); err != nil || used != 2 {
				t.Fatalf("%s was refunded on terminal: %d: %v", kind, used, err)
			}
		}
		revoked := snapshot
		revoked.ID = "snapshot_3"
		revoked.Revision = 3
		revoked.Status = "revoked"
		revoked = sign(revoked)
		if err := control.Install(ctx, revoked); err != nil {
			t.Fatal(err)
		}
		if err := control.Install(ctx, snapshot); err != nil {
			t.Fatalf("old acknowledgement retry: %v", err)
		}
		if _, err := sessions.Create(ctx, f.scope, first); err != nil {
			t.Fatalf("accepted terminal replay after revocation: %v", err)
		}
		third := mutation("admission-third")
		if _, err := sessions.Create(ctx, f.scope, third); !errors.Is(err, admission.ErrEntitlement) {
			t.Fatalf("new work after revocation: %v", err)
		}
		assertAbsent(third)
		event, _ = f.ids.NewEvent()
		task, _ = f.ids.NewTask()
		if _, err := life.Apply(ctx, f.scope, verification.LifecycleCommand{EventID: event, VerificationID: acceptedSecond.Session.ID(), ExpectedVersion: 1, Target: verification.SessionStateProcessing, ActorID: task.String(), OccurredAt: now.Add(time.Second)}); err != nil {
			t.Fatalf("accepted continuation after revocation: %v", err)
		}
		changedWindow := snapshot
		changedWindow.Limits = append([]admissionv1.Limit(nil), snapshot.Limits...)
		changedWindow.ID = "snapshot_4"
		changedWindow.Revision = 4
		changedWindow.Limits[3].Window = time.Hour
		changedWindow = sign(changedWindow)
		if err := control.Install(ctx, changedWindow); !errors.Is(err, admission.ErrConflict) {
			t.Fatalf("window reset: %v", err)
		}
		event, _ = f.ids.NewEvent()
		task, _ = f.ids.NewTask()
		if _, err := life.Apply(ctx, f.scope, verification.LifecycleCommand{EventID: event, VerificationID: acceptedSecond.Session.ID(), ExpectedVersion: 2, Target: verification.SessionStateCancelled, ActorID: task.String(), OccurredAt: now.Add(2 * time.Second)}); err != nil {
			t.Fatal(err)
		}
		active := snapshot
		active.ID = "snapshot_5"
		active.Revision = 5
		active = sign(active)
		if err := control.Install(ctx, active); err != nil {
			t.Fatal(err)
		}
		requests := make([]verification.SessionCreateMutation, 8)
		for i := range requests {
			requests[i] = mutation(fmt.Sprintf("admission-race-%d", i))
		}
		start := make(chan struct{})
		results := make(chan error, len(requests))
		var workers sync.WaitGroup
		for _, request := range requests {
			workers.Go(func() { <-start; _, err := sessions.Create(ctx, f.scope, request); results <- err })
		}
		close(start)
		workers.Wait()
		close(results)
		accepted := 0
		for err := range results {
			if err == nil {
				accepted++
			} else if !errors.Is(err, admission.ErrQuota) {
				t.Fatalf("concurrent admission: %v", err)
			}
		}
		if accepted != 1 {
			t.Fatalf("concurrent admits=%d; want one configured slot", accepted)
		}
		var activeID string
		if err := f.admin.Native().QueryRow(ctx, `SELECT id FROM idenqa.admission_operations WHERE tenant_id=$1 AND state='active'`, f.scope.ID().String()).Scan(&activeID); err != nil {
			t.Fatal(err)
		}
		identifier, err := id.ParseVerification(activeID)
		if err != nil {
			t.Fatal(err)
		}
		event, _ = f.ids.NewEvent()
		task, _ = f.ids.NewTask()
		closeCommand := verification.LifecycleCommand{EventID: event, VerificationID: identifier, ExpectedVersion: 1, Target: verification.SessionStateCancelled, ActorID: task.String(), OccurredAt: now.Add(2 * time.Second)}
		contender := mutation("admission-contention")
		type outcome struct {
			kind string
			err  error
		}
		contended := make(chan outcome, 3)
		begin := make(chan struct{})
		var concurrent sync.WaitGroup
		concurrent.Go(func() { <-begin; _, err := life.Apply(ctx, f.scope, closeCommand); contended <- outcome{"close", err} })
		concurrent.Go(func() {
			<-begin
			err := gate.RebuildCounter(ctx, f.scope.ID().String(), "concurrency", "work", 0, "operator_fixture", "contention_fixture")
			contended <- outcome{"repair", err}
		})
		concurrent.Go(func() {
			<-begin
			_, err := sessions.Create(ctx, f.scope, contender)
			contended <- outcome{"create", err}
		})
		close(begin)
		concurrent.Wait()
		close(contended)
		for result := range contended {
			if result.err == nil || (result.kind == "create" && errors.Is(result.err, admission.ErrQuota)) {
				continue
			}
			var serialization *pgconn.PgError
			if result.kind == "close" && errors.As(result.err, &serialization) && serialization.Code == "40001" {
				if _, err := life.Apply(ctx, f.scope, closeCommand); err != nil {
					t.Fatalf("authoritative closure retry: %v", err)
				}
				continue
			}
			t.Fatalf("%s contention: %v", result.kind, result.err)
		}
		var consistent bool
		if err := f.admin.Native().QueryRow(ctx, `SELECT used=(SELECT coalesce(sum(quantity),0) FROM idenqa.admission_reservations r WHERE r.tenant_id=c.tenant_id AND r.kind=c.kind AND r.key=c.key AND r.window_key=c.window_key AND NOT released) AND NOT dirty FROM idenqa.admission_counters c WHERE tenant_id=$1 AND kind='concurrency'`, f.scope.ID().String()).Scan(&consistent); err != nil || !consistent {
			t.Fatalf("closure/admission/repair lost its fence: %v", err)
		}
		// An expired newer revision must not fall back to an older active policy.
		expired := snapshot
		expired.ID = "snapshot_6"
		expired.Revision = 6
		expired.IssuedAt = now.Add(-2 * time.Minute)
		expired.ValidFrom = expired.IssuedAt
		expired.ValidUntil = now.Add(-time.Minute)
		expired = sign(expired)
		if err := control.Install(ctx, expired); err != nil {
			t.Fatal(err)
		}
		if _, err := sessions.Create(ctx, f.scope, mutation("admission-expired")); !errors.Is(err, admission.ErrUnavailable) {
			t.Fatalf("expired policy fell back: %v", err)
		}
		// Future scheduling remains bounded, and a full queue cannot prevent an
		// urgent effective revocation from fencing every scheduled revision.
		for revision := int64(7); revision < 71; revision++ {
			scheduled := snapshot
			scheduled.ID = fmt.Sprintf("scheduled_%d", revision)
			scheduled.Revision = revision
			scheduled.ValidFrom = now.Add(30 * time.Minute)
			scheduled = sign(scheduled)
			if err := control.Install(ctx, scheduled); err != nil {
				t.Fatalf("schedule %d: %v", revision, err)
			}
		}
		overflow := snapshot
		overflow.ID = "schedule_overflow"
		overflow.Revision = 71
		overflow.ValidFrom = now.Add(30 * time.Minute)
		overflow = sign(overflow)
		if err := control.Install(ctx, overflow); !errors.Is(err, admission.ErrInvalid) {
			t.Fatalf("unbounded future queue: %v", err)
		}
		urgent := snapshot
		urgent.ID = "urgent_revocation"
		urgent.Revision = 71
		urgent.Status = "revoked"
		urgent = sign(urgent)
		if err := control.Install(ctx, urgent); err != nil {
			t.Fatalf("full queue prevented urgent revocation: %v", err)
		}
		if err := f.admin.Native().QueryRow(ctx, `SELECT count(*) FROM idenqa.admission_pending WHERE tenant_id=$1`, snapshot.Scope.TenantID).Scan(&pending); err != nil || pending != 0 {
			t.Fatalf("revocation retained obsolete future queue %d: %v", pending, err)
		}
		if _, err := sessions.Create(ctx, f.scope, mutation("urgent-revocation")); !errors.Is(err, admission.ErrEntitlement) {
			t.Fatalf("urgent revocation: %v", err)
		}
		var databaseNow time.Time
		if err := f.admin.Native().QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&databaseNow); err != nil {
			t.Fatal(err)
		}
		activation := snapshot
		activation.ID = "future_activation"
		activation.Revision = 72
		activation.ValidFrom = databaseNow.Add(200 * time.Millisecond)
		activation = sign(activation)
		if err := control.Install(ctx, activation); err != nil {
			t.Fatal(err)
		}
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(25 * time.Millisecond)
		defer tick.Stop()
	activationWait:
		for {
			observed, err := gate.Summary(ctx, snapshot.Scope)
			if err != nil {
				t.Fatal(err)
			}
			if observed.Revision == 72 {
				break activationWait
			}
			select {
			case <-deadline.C:
				t.Fatal("future revision never became effective")
			case <-tick.C:
			}
		}
		for _, statement := range []string{`UPDATE idenqa.admission_snapshots SET digest=repeat('0',64)`, `DELETE FROM idenqa.admission_events`, `DELETE FROM idenqa.admission_operations`, `UPDATE idenqa.admission_reservations SET quantity=quantity+1`} {
			if _, err := f.admin.Native().Exec(ctx, statement); err == nil {
				t.Fatalf("mutable history: %s", statement)
			}
		}
	})
}
