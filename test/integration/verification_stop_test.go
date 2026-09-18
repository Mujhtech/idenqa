//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/jackc/pgx/v5"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func cancelMutation(t *testing.T, scope tenant.Scope, principal idempotency.PrincipalValue, identifier id.Verification, version int64, key string, now time.Time) verification.CancellationMutation {
	t.Helper()
	canonical, err := json.Marshal(struct {
		VerificationID  string `json:"verification_id"`
		ExpectedVersion int64  `json:"expected_version"`
	}{identifier.String(), version})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := idempotency.NewRequest(scope.ID(), principal, "verification.cancel", key, canonical, now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return verification.CancellationMutation{VerificationID: identifier, ExpectedVersion: version, Retry: retry}
}

func TestVerificationStopCancellationReplayAndIsolation(t *testing.T) {
	f := newLifecycleFixture(t)
	source := &captureClock{}
	source.microseconds.Store(f.now.Add(time.Minute).UnixMicro())
	store, err := verificationpostgres.NewStopStore(f.runtime, f.ids, source)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := f.ids.NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	mutation := cancelMutation(t, f.scope, actor, f.verificationID, 1, "cancel-1", source.Now())
	original, err := store.Cancel(t.Context(), f.scope, mutation)
	if err != nil || original.State != verification.SessionStateCancelled {
		t.Fatalf("cancel: %+v %v", original, err)
	}
	f.assertState(t, verification.SessionStateCancelled, 2, 1)
	source.microseconds.Store(f.now.Add(time.Hour + 30*time.Second).UnixMicro())
	mutation = cancelMutation(t, f.scope, actor, f.verificationID, 1, "cancel-1", source.Now())
	replay, err := store.Cancel(t.Context(), f.scope, mutation)
	if err != nil || replay != original {
		t.Fatalf("replay: %+v %v", replay, err)
	}
	changed := cancelMutation(t, f.scope, actor, f.verificationID, 2, "cancel-1", mutation.Retry.CreatedAt())
	if _, err := store.Cancel(t.Context(), f.scope, changed); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("changed input: %v", err)
	}
	fresh := cancelMutation(t, f.scope, actor, f.verificationID, 2, "cancel-2", source.Now())
	if _, err := store.Cancel(t.Context(), f.scope, fresh); !errors.Is(err, verification.ErrSessionConflict) {
		t.Fatalf("terminal cancellation: %v", err)
	}
	otherTenant, _ := seedExecutionVerification(t, f.admin, f.ids, f.now)
	otherScope, err := tenant.NewScope(otherTenant)
	if err != nil {
		t.Fatal(err)
	}
	cross := cancelMutation(t, otherScope, actor, f.verificationID, 1, "cancel-1", source.Now())
	if _, err := store.Cancel(t.Context(), otherScope, cross); !errors.Is(err, verification.ErrSessionNotFound) {
		t.Fatalf("cross tenant: %v", err)
	}
	f.assertState(t, verification.SessionStateCancelled, 2, 1)
}

func TestVerificationStopExpiryRollbackAndDiscovery(t *testing.T) {
	f := newLifecycleFixture(t)
	source := &captureClock{}
	source.microseconds.Store(f.now.Add(time.Hour).UnixMicro())
	store, err := verificationpostgres.NewStopStore(f.runtime, f.ids, source)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := f.ids.NewTask()
	if err != nil {
		t.Fatal(err)
	}
	apply := func(fail bool) error {
		return f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			if err := store.ExpireWithin(ctx, f.scope, tx, f.verificationID, actor); err != nil {
				return err
			}
			if fail {
				return errPlannedQueueFailure
			}
			return nil
		})
	}
	if err := apply(true); !errors.Is(err, errPlannedQueueFailure) {
		t.Fatal(err)
	}
	f.assertState(t, verification.SessionStateCollecting, 1, 0)
	otherTenant, otherID := seedExecutionVerification(t, f.admin, f.ids, f.now)
	seen := map[id.Verification]bool{}
	for offset := range 2 {
		targets, err := store.ListDueExpirations(t.Context(), source.Now().Add(time.Duration(offset)*time.Second), 1)
		if err != nil || len(targets) != 1 {
			t.Fatalf("discovery %v %v", targets, err)
		}
		seen[targets[0].VerificationID] = true
	}
	if !seen[f.verificationID] || !seen[otherID] {
		t.Fatal("expiry discovery starved due work")
	}
	if err := apply(false); err != nil {
		t.Fatal(err)
	}
	if err := apply(false); err != nil {
		t.Fatal(err)
	}
	f.assertState(t, verification.SessionStateExpired, 2, 1)
	targets, err := store.ListDueExpirations(t.Context(), source.Now(), 100)
	if err != nil || len(targets) != 1 || targets[0].TenantID != otherTenant {
		t.Fatalf("terminal rediscovery: %v %v", targets, err)
	}
}

func TestVerificationStopSubjectBindingAndPendingUploads(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		uploads, mutations := f.prepare(t)
		source := fixedIntegrationClock{now: f.now}
		store, err := verificationpostgres.NewStopStore(f.runtime, f.ids, source)
		if err != nil {
			t.Fatal(err)
		}
		mutation := cancelMutation(t, f.scope, f.creation.Credential.ID(), f.creation.Session.ID(), 1, "subject-cancel", f.now)
		if _, err := store.Cancel(t.Context(), f.scope, mutation); err != nil {
			t.Fatal(err)
		}
		if _, err := store.Cancel(t.Context(), f.scope, mutation); err != nil {
			t.Fatal(err)
		}
		// Existing prepared uploads cannot start a fresh body attempt after cancellation.
		for _, mutation := range mutations {
			if _, err := uploads.ClaimUploadAttempt(t.Context(), f.scope, f.creation.Credential.ID(), mutation.UploadID, mutation.ExpectedVersion, f.now); err == nil {
				t.Fatal("stopped session claimed upload")
			}
		}
	})
}

func TestVerificationStopCancellationRacesCompletion(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		decision := prepareCompletion(t, f)
		source := fixedIntegrationClock{now: f.now}
		stops, err := verificationpostgres.NewStopStore(f.runtime, f.ids, source)
		if err != nil {
			t.Fatal(err)
		}
		decisions, err := policypostgres.NewGuarded(f.runtime, source)
		if err != nil {
			t.Fatal(err)
		}
		completion, err := verificationpostgres.NewCompletionStore(f.runtime, f.ids, completionProbe{}, source)
		if err != nil {
			t.Fatal(err)
		}
		actor, err := f.ids.NewTask()
		if err != nil {
			t.Fatal(err)
		}
		key, err := f.ids.NewAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		mutation := cancelMutation(t, f.scope, key, f.creation.Session.ID(), 2, "race-stop", f.now)
		start := make(chan struct{})
		results := make(chan error, 2)
		go func() { <-start; _, err := stops.Cancel(t.Context(), f.scope, mutation); results <- err }()
		go func() {
			<-start
			results <- f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
				if err := decisions.AppendWithin(ctx, f.scope, tx, decision); err != nil {
					return err
				}
				return completion.CompleteWithin(ctx, f.scope, tx, decision, actor)
			})
		}()
		close(start)
		successes := 0
		for range 2 {
			if err := <-results; err == nil {
				successes++
			}
		}
		if successes != 1 {
			t.Fatalf("terminal winners=%d", successes)
		}
		var state string
		var version int64
		var decisionsCount, deliveries, receipts int
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT state,version,
 (SELECT count(*) FROM idenqa.verification_decisions WHERE tenant_id=$1),
 (SELECT count(*) FROM idenqa.webhook_deliveries WHERE tenant_id=$1),
 (SELECT count(*) FROM idenqa.verification_transitions WHERE tenant_id=$1 AND to_state IN ('completed','cancelled'))
 FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.creation.Session.ID().String()).Scan(&state, &version, &decisionsCount, &deliveries, &receipts); err != nil {
			t.Fatal(err)
		}
		if version != 3 || receipts != 1 {
			t.Fatalf("terminal state=%s version=%d receipts=%d", state, version, receipts)
		}
		if state == "cancelled" && (decisionsCount != 0 || deliveries != 0) {
			t.Fatal("completion effects escaped cancellation")
		}
		if state == "completed" && (decisionsCount != 1 || deliveries != 1) {
			t.Fatal("partial completion")
		}
	})
}

func TestVerificationStopObservesTokenExpiryAfterLock(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		source := &captureClock{}
		source.microseconds.Store(f.now.UnixMicro())
		store, err := verificationpostgres.NewStopStore(f.runtime, f.ids, source)
		if err != nil {
			t.Fatal(err)
		}
		mutation := cancelMutation(t, f.scope, f.creation.Credential.ID(), f.creation.Session.ID(), 1, "subject-wait", f.now)
		tx, err := f.admin.Native().Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := tx.Rollback(context.WithoutCancel(t.Context())); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Error(err)
			}
		}()
		var blocker int32
		if err := tx.QueryRow(t.Context(), `SELECT pg_backend_pid() FROM idenqa.capture_tokens WHERE id=$1 FOR UPDATE`, f.creation.Credential.ID().String()).Scan(&blocker); err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { _, err := store.Cancel(t.Context(), f.scope, mutation); result <- err }()
		waitForCaptureBlock(t, f.admin, blocker, result)
		source.microseconds.Store(f.creation.Credential.ExpiresAt().UnixMicro())
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := <-result; !errors.Is(err, access.ErrInvalidCaptureToken) {
			t.Fatalf("expired credential after lock: %v", err)
		}
		var count int
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.verification_transitions WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&count); err != nil || count != 0 {
			t.Fatalf("invalid cancellation wrote transition: %d %v", count, err)
		}
	})
}

type stopEventID struct{ event id.Event }

func (source stopEventID) NewEvent() (id.Event, error) { return source.event, nil }

func TestVerificationStopCancellationRollsBackReplayOnAuditFailure(t *testing.T) {
	f := newLifecycleFixture(t)
	source := lifecycleClock{now: f.now.Add(time.Minute)}
	collision, err := f.ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.admin.Native().Exec(t.Context(), `INSERT INTO idenqa.outbox_events (id,tenant_id,aggregate_type,aggregate_id,aggregate_version,event_type,schema_version,payload,occurred_at,created_at) VALUES ($1,$2,'verification',$3,99,'synthetic.conflict.v1',1,'{}',$4,$4)`, collision.String(), f.scope.ID().String(), f.verificationID.String(), f.now); err != nil {
		t.Fatal(err)
	}
	store, err := verificationpostgres.NewStopStore(f.runtime, stopEventID{collision}, source)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := f.ids.NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	mutation := cancelMutation(t, f.scope, actor, f.verificationID, 1, "rollback-cancel", source.Now())
	if _, err := store.Cancel(t.Context(), f.scope, mutation); err == nil {
		t.Fatal("outbox failure committed cancellation")
	}
	f.assertState(t, verification.SessionStateCollecting, 1, 0)
	var reservations int
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.idempotency_records WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&reservations); err != nil || reservations != 0 {
		t.Fatalf("partial replay reservation: %d %v", reservations, err)
	}
	store, err = verificationpostgres.NewStopStore(f.runtime, f.ids, source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Cancel(t.Context(), f.scope, mutation); err != nil {
		t.Fatal(err)
	}
	f.assertState(t, verification.SessionStateCancelled, 2, 1)
}

func TestVerificationStopDeadlineWinsWaitingCompletionAndCancellation(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		decision := prepareCompletion(t, f)
		source := &captureClock{}
		source.microseconds.Store(f.now.UnixMicro())
		stops, err := verificationpostgres.NewStopStore(f.runtime, f.ids, source)
		if err != nil {
			t.Fatal(err)
		}
		decisions, err := policypostgres.NewGuarded(f.runtime, source)
		if err != nil {
			t.Fatal(err)
		}
		completion, err := verificationpostgres.NewCompletionStore(f.runtime, f.ids, completionProbe{}, source)
		if err != nil {
			t.Fatal(err)
		}
		actor, err := f.ids.NewTask()
		if err != nil {
			t.Fatal(err)
		}
		key, err := f.ids.NewAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		mutation := cancelMutation(t, f.scope, key, f.creation.Session.ID(), 2, "deadline-cancel", f.now)
		tx, err := f.admin.Native().Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := tx.Rollback(context.WithoutCancel(t.Context())); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
				t.Error(err)
			}
		}()
		var blocker int32
		if err := tx.QueryRow(t.Context(), `SELECT pg_backend_pid() FROM idenqa.verification_sessions WHERE id=$1 FOR UPDATE`, f.creation.Session.ID().String()).Scan(&blocker); err != nil {
			t.Fatal(err)
		}
		completeResult := make(chan error, 1)
		go func() {
			completeResult <- f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
				if err := decisions.AppendWithin(ctx, f.scope, tx, decision); err != nil {
					return err
				}
				return completion.CompleteWithin(ctx, f.scope, tx, decision, actor)
			})
		}()
		waitForCaptureBlock(t, f.admin, blocker, completeResult)
		source.microseconds.Store(f.creation.Session.ExpiresAt().UnixMicro())
		if err := tx.Commit(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := <-completeResult; err == nil {
			t.Fatal("waiting completion crossed deadline")
		}
		if _, err := stops.Cancel(t.Context(), f.scope, mutation); !errors.Is(err, verification.ErrSessionConflict) {
			t.Fatalf("cancellation crossed deadline: %v", err)
		}
		if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			return stops.ExpireWithin(ctx, f.scope, tx, f.creation.Session.ID(), actor)
		}); err != nil {
			t.Fatal(err)
		}
		assertCompletionCounts(t, f, 0, 0, 2, 2)
		var state string
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT state FROM idenqa.verification_sessions WHERE id=$1`, f.creation.Session.ID().String()).Scan(&state); err != nil || state != "expired" {
			t.Fatalf("deadline state=%s %v", state, err)
		}
	})
}
