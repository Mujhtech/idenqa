//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestVerificationLifecycleReplayIsolationAndImmutability(t *testing.T) {
	f := newLifecycleFixture(t)
	first := f.command(t, verification.SessionStateProcessing, 1, f.now.Add(time.Second))
	original, err := f.store.Apply(t.Context(), f.scope, first)
	if err != nil || original.Version != 2 || original.From != verification.SessionStateCollecting {
		t.Fatalf("first transition = %+v, %v", original, err)
	}
	second := f.command(t, verification.SessionStateAwaitingExternal, 2, f.now.Add(2*time.Second))
	if _, err := f.store.Apply(t.Context(), f.scope, second); err != nil {
		t.Fatal(err)
	}
	replayed, err := f.store.Apply(t.Context(), f.scope, first)
	if err != nil || replayed != original {
		t.Fatalf("replay = %+v, %v; want original %+v", replayed, err, original)
	}
	for _, change := range []struct {
		name   string
		mutate func(*verification.LifecycleCommand)
	}{
		{"target", func(c *verification.LifecycleCommand) { c.Target = verification.SessionStateFailed }},
		{"version", func(c *verification.LifecycleCommand) { c.ExpectedVersion++ }},
		{"time", func(c *verification.LifecycleCommand) { c.OccurredAt = c.OccurredAt.Add(time.Second) }},
		{"actor", func(c *verification.LifecycleCommand) { c.ActorID = "key_01ARZ3NDEKTSV4RRFFQ69G5FAV" }},
	} {
		t.Run("changed_"+change.name, func(t *testing.T) {
			changed := first
			change.mutate(&changed)
			if _, err := f.store.Apply(t.Context(), f.scope, changed); !errors.Is(err, verification.ErrSessionConflict) {
				t.Fatalf("changed replay = %v", err)
			}
		})
	}
	otherTenant, _ := seedExecutionVerification(t, f.admin, f.ids, f.now)
	otherScope, err := tenant.NewScope(otherTenant)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Apply(t.Context(), otherScope, first); !errors.Is(err, verification.ErrSessionNotFound) {
		t.Fatalf("cross-tenant transition = %v", err)
	}
	f.assertState(t, verification.SessionStateAwaitingExternal, 3, 2)
	for _, statement := range []string{
		`UPDATE idenqa.verification_transitions SET to_state='failed' WHERE tenant_id=$1`,
		`DELETE FROM idenqa.verification_transitions WHERE tenant_id=$1`,
	} {
		err := f.runtime.WithinTransaction(t.Context(), idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
			if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, f.scope.ID().String()); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, statement, f.scope.ID().String())
			return err
		})
		if err == nil {
			t.Fatal("transition receipt was mutable")
		}
		if _, err := f.admin.Native().Exec(t.Context(), statement, f.scope.ID().String()); err == nil {
			t.Fatal("receipt immutability trigger allowed administrative mutation")
		}
	}
	var unscoped int
	if err := f.runtime.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.verification_transitions`).Scan(&unscoped); err != nil || unscoped != 0 {
		t.Fatalf("unscoped receipt count = %d, %v", unscoped, err)
	}
}

func TestVerificationLifecycleCallerEffectRollback(t *testing.T) {
	f := newLifecycleFixture(t)
	command := f.command(t, verification.SessionStateProcessing, 1, f.now.Add(time.Second))
	lostFence := errors.New("synthetic caller effect lost its execution fence")
	err := f.runtime.WithinTransaction(t.Context(), idenqapostgres.TransactionOptions{Isolation: idenqapostgres.IsolationSerializable}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if _, err := f.store.ApplyWithin(ctx, f.scope, tx, command); err != nil {
			return err
		}
		return lostFence
	})
	if !errors.Is(err, lostFence) {
		t.Fatalf("rollback = %v", err)
	}
	f.assertState(t, verification.SessionStateCollecting, 1, 0)
	if _, err := f.store.Apply(t.Context(), f.scope, command); err != nil {
		t.Fatal(err)
	}
	f.assertState(t, verification.SessionStateProcessing, 2, 1)

	// Failure late in persistence (outbox uniqueness) must also undo state and
	// receipt writes, rather than only failures returned by the caller.
	conflict := f.command(t, verification.SessionStateAwaitingExternal, 2, f.now.Add(2*time.Second))
	_, err = f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.outbox_events
(id,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,payload,occurred_at,created_at)
VALUES ($1,$2,'verification',$3,99,'synthetic.conflict.v1',1,'{}',$4,$4)`, conflict.EventID.String(), f.scope.ID().String(), f.verificationID.String(), f.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.Apply(t.Context(), f.scope, conflict); err == nil {
		t.Fatal("duplicate outbox identity succeeded")
	}
	f.assertState(t, verification.SessionStateProcessing, 2, 1)

	err = f.runtime.WithinTransaction(t.Context(), idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := f.store.ApplyWithin(ctx, f.scope, tx, conflict)
		return err
	})
	if err == nil {
		t.Fatal("non-serializable effect accepted")
	}
}

func TestVerificationLifecycleConcurrentTransitions(t *testing.T) {
	f := newLifecycleFixture(t)
	commands := []verification.LifecycleCommand{
		f.command(t, verification.SessionStateProcessing, 1, f.now.Add(time.Second)),
		f.command(t, verification.SessionStateCancelled, 1, f.now.Add(time.Second)),
	}
	start := make(chan struct{})
	results := make(chan error, len(commands))
	var workers sync.WaitGroup
	for _, command := range commands {
		workers.Go(func() {
			<-start
			_, err := f.store.Apply(t.Context(), f.scope, command)
			results <- err
		})
	}
	close(start)
	workers.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		var postgresError *pgconn.PgError
		if !errors.Is(err, verification.ErrSessionConflict) &&
			(!errors.As(err, &postgresError) || postgresError.Code != "40001") {
			t.Fatalf("unexpected concurrency failure: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful competing transitions = %d", succeeded)
	}
	var state verification.SessionState
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT state FROM idenqa.verification_sessions WHERE id=$1`, f.verificationID.String()).Scan(&state); err != nil {
		t.Fatal(err)
	}
	f.assertState(t, state, 2, 1)
	for _, command := range commands {
		_, err := f.store.Apply(t.Context(), f.scope, command)
		if err != nil && !errors.Is(err, verification.ErrSessionConflict) {
			t.Fatalf("retry = %v", err)
		}
	}
	f.assertState(t, state, 2, 1)
}

func TestVerificationLifecycleExpiryAndCompletion(t *testing.T) {
	f := newLifecycleFixture(t)
	command := f.command(t, verification.SessionStateProcessing, 1, f.now.Add(time.Second))
	if _, err := f.store.Apply(t.Context(), f.scope, command); err != nil {
		t.Fatal(err)
	}
	complete := f.command(t, verification.SessionStateCompleted, 2, f.now.Add(time.Minute))
	missingDecision, err := f.ids.NewDecision()
	if err != nil {
		t.Fatal(err)
	}
	complete.DecisionID = missingDecision
	if _, err := f.store.Apply(t.Context(), f.scope, complete); !errors.Is(err, verification.ErrSessionConflict) {
		t.Fatalf("completion without persisted decision = %v", err)
	}
	policyStore, err := policypostgres.New(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	otherTenant, otherVerification := seedExecutionVerification(t, f.admin, f.ids, f.now)
	otherScope, err := tenant.NewScope(otherTenant)
	if err != nil {
		t.Fatal(err)
	}
	otherDecision := newIntegrationDecision(t, f.ids, otherTenant, otherVerification, id.Decision{}, f.now)
	if err := policyStore.Append(t.Context(), otherScope, otherDecision); err != nil {
		t.Fatal(err)
	}
	complete.DecisionID = otherDecision.ID()
	if _, err := f.store.Apply(t.Context(), f.scope, complete); !errors.Is(err, verification.ErrSessionConflict) {
		t.Fatalf("completion with another verification's decision = %v", err)
	}
	siblingID, err := f.ids.NewVerification()
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.verification_sessions
(id,tenant_id,state,version,source_profile_id,source_profile_revision,source_profile_digest,requirements,created_at,updated_at,expires_at)
SELECT $1,tenant_id,'collecting',1,source_profile_id,source_profile_revision,source_profile_digest,requirements,created_at,created_at,expires_at
FROM idenqa.verification_sessions WHERE id=$2`, siblingID.String(), f.verificationID.String())
	if err != nil {
		t.Fatal(err)
	}
	siblingDecision := newIntegrationDecision(t, f.ids, f.scope.ID(), siblingID, id.Decision{}, f.now)
	if err := policyStore.Append(t.Context(), f.scope, siblingDecision); err != nil {
		t.Fatal(err)
	}
	complete.DecisionID = siblingDecision.ID()
	if _, err := f.store.Apply(t.Context(), f.scope, complete); !errors.Is(err, verification.ErrSessionConflict) {
		t.Fatalf("same-tenant wrong-verification decision = %v", err)
	}
	decision := newIntegrationDecision(t, f.ids, f.scope.ID(), f.verificationID, id.Decision{}, f.now.Add(30*time.Second))
	if err := policyStore.Append(t.Context(), f.scope, decision); err != nil {
		t.Fatal(err)
	}
	complete.DecisionID = decision.ID()
	premature := complete
	premature.OccurredAt = f.now.Add(20 * time.Second)
	if _, err := f.store.Apply(t.Context(), f.scope, premature); !errors.Is(err, verification.ErrSessionConflict) {
		t.Fatalf("completion before decision exists = %v", err)
	}
	if _, err := f.store.Apply(t.Context(), f.scope, complete); err != nil {
		t.Fatal(err)
	}
	f.assertState(t, verification.SessionStateCompleted, 3, 2)
	late := f.command(t, verification.SessionStateFailed, 3, f.now.Add(90*time.Second))
	if _, err := f.store.Apply(t.Context(), f.scope, late); !errors.Is(err, verification.ErrSessionConflict) {
		t.Fatalf("revive terminal session = %v", err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.verification_sessions SET state='processing',completed_decision_id=NULL,version=version+1 WHERE id=$1`, f.verificationID.String()); err == nil {
		t.Fatal("database allowed terminal resurrection")
	}

	// A pinned old command cannot bypass the current observation deadline.
	expiredStore, err := verificationpostgres.NewLifecycleStore(f.runtime, lifecycleClock{f.now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	other := f.command(t, verification.SessionStateProcessing, 1, f.now.Add(time.Second))
	other.VerificationID = otherVerification
	if _, err := expiredStore.Apply(t.Context(), otherScope, other); !errors.Is(err, verification.ErrSessionConflict) {
		t.Fatalf("backdated processing after expiry = %v", err)
	}
	other.Target, other.OccurredAt = verification.SessionStateExpired, f.now.Add(time.Hour)
	if _, err := expiredStore.Apply(t.Context(), otherScope, other); err != nil {
		t.Fatalf("expiry at equality: %v", err)
	}
	if _, err := expiredStore.Apply(t.Context(), f.scope, command); err != nil {
		t.Fatalf("committed replay after expiry: %v", err)
	}
	down, err := migrations.Files.ReadFile("000030_verification_lifecycle.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	err = f.admin.WithinTransaction(t.Context(), idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(ctx, string(down))
		return err
	})
	if err == nil {
		t.Fatal("rollback erased transitioned history")
	}
	f.assertState(t, verification.SessionStateCompleted, 3, 2)
}

type lifecycleClock struct{ now time.Time }

func (source lifecycleClock) Now() time.Time { return source.now }

type lifecycleFixture struct {
	database       *isolatedDatabase
	role           string
	admin, runtime *idenqapostgres.Pool
	store          *verificationpostgres.LifecycleStore
	scope          tenant.Scope
	verificationID id.Verification
	ids            *id.Generator
	now            time.Time
}

func newLifecycleFixture(t *testing.T) lifecycleFixture {
	t.Helper()
	database := createIsolatedDatabase(t)
	migrator, err := idenqapostgres.OpenMigrator(t.Context(), migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := idenqapostgres.Open(t.Context(), poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	ids, err := id.NewGenerator(lifecycleClock{now}, bytes.NewReader(bytes.Repeat([]byte{1}, 4096)))
	if err != nil {
		t.Fatal(err)
	}
	tenantID, verificationID := seedExecutionVerification(t, admin, ids, now)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	configuration := poolConfig(database.url)
	configuration.Role = database.createRuntimeRole(t)
	runtime, err := idenqapostgres.Open(t.Context(), configuration)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	store, err := verificationpostgres.NewLifecycleStore(runtime, lifecycleClock{now.Add(2 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	return lifecycleFixture{database: database, role: configuration.Role, admin: admin, runtime: runtime, store: store, scope: scope, verificationID: verificationID, ids: ids, now: now}
}

func (f lifecycleFixture) command(t *testing.T, target verification.SessionState, expected int64, at time.Time) verification.LifecycleCommand {
	t.Helper()
	eventID, err := f.ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	actorID, err := f.ids.NewTask()
	if err != nil {
		t.Fatal(err)
	}
	return verification.LifecycleCommand{EventID: eventID, VerificationID: f.verificationID, ExpectedVersion: expected,
		Target: target, ActorID: actorID.String(), OccurredAt: at}
}

func (f lifecycleFixture) assertState(t *testing.T, state verification.SessionState, version int64, transitions int) {
	t.Helper()
	var gotState string
	var gotVersion int64
	var receipts, audits, outbox int
	err := f.admin.Native().QueryRow(t.Context(), `SELECT state, version,
(SELECT count(*) FROM idenqa.verification_transitions WHERE tenant_id=$1),
(SELECT count(*) FROM idenqa.audit_records WHERE tenant_id=$1),
(SELECT count(*) FROM idenqa.outbox_events WHERE tenant_id=$1 AND event_type='verification.transitioned.v1')
FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.verificationID.String()).Scan(&gotState, &gotVersion, &receipts, &audits, &outbox)
	if err != nil || gotState != string(state) || gotVersion != version || receipts != transitions || audits != transitions || outbox != transitions {
		t.Fatalf("state/version=%s/%d receipt/audit/outbox=%d/%d/%d error=%v", gotState, gotVersion, receipts, audits, outbox, err)
	}
}
