package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type reconciliationStore struct {
	check      verification.Check
	claim      verification.ReconciliationClaim
	claimError error
	claimed    int
	resolved   int
	lease      time.Duration
}

func (store *reconciliationStore) FindCheckWithin(
	context.Context,
	tenant.Scope,
	postgres.Transaction,
	id.Check,
) (verification.Check, error) {
	return store.check, nil
}

func (store *reconciliationStore) ClaimReconciliationForAttempt(
	_ context.Context,
	_ tenant.Scope,
	_ id.Check,
	_ id.Attempt,
	_ id.Task,
	_ time.Time,
	lease time.Duration,
) (verification.ReconciliationClaim, error) {
	store.claimed++
	store.lease = lease
	return store.claim, store.claimError
}

func (store *reconciliationStore) ResolveReconciliationWithin(
	_ context.Context,
	_ tenant.Scope,
	_ postgres.Transaction,
	_ verification.ReconciliationClaim,
	_ time.Time,
) error {
	store.resolved++
	return nil
}

func TestReconcileHandlerClaimsExactTargetAndResolvesTransactionally(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 4)
	claimToken, _ := id.ParseTask("tsk_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	store := &reconciliationStore{check: check, claim: verification.ReconciliationClaim{
		TenantID: check.TenantID, VerificationID: check.VerificationID, CheckID: check.ID,
		AttemptID: attempt.ID, Reason: "stale", ClaimToken: claimToken,
		ClaimedAt: now, LeaseExpiresAt: now.Add(ReconciliationItemLease),
	}}
	handler, err := NewReconcileHandler(
		store, fixedTaskIDs{claimToken}, coordinationClock{now}, ReconciliationItemLease,
	)
	if err != nil {
		t.Fatal(err)
	}
	delivery := reconcileDelivery(t, check, attempt, now)
	work, result := handler.Prepare(t.Context(), delivery)
	if result.Outcome != platformtask.OutcomeComplete || work == nil || store.claimed != 1 ||
		store.lease != ReconciliationItemLease {
		t.Fatalf("Prepare() result=%+v claimed=%d lease=%s", result, store.claimed, store.lease)
	}
	if committed := work(t.Context(), nil); committed.Outcome != platformtask.OutcomeComplete || store.resolved != 1 {
		t.Fatalf("commit=%+v resolved=%d", committed, store.resolved)
	}
}

func TestReconcileHandlerRetriesAnApplicationLeaseConflict(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 4)
	claimToken, _ := id.ParseTask("tsk_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	store := &reconciliationStore{check: check, claimError: verification.ErrStaleAttempt}
	handler, err := NewReconcileHandler(
		store, fixedTaskIDs{claimToken}, coordinationClock{now}, ReconciliationItemLease,
	)
	if err != nil {
		t.Fatal(err)
	}
	work, result := handler.Prepare(t.Context(), reconcileDelivery(t, check, attempt, now))
	if work != nil || result.Outcome != platformtask.OutcomeRetry ||
		result.Class != platformtask.RetryClassConflict || !errors.Is(result.Err, verification.ErrStaleAttempt) {
		t.Fatalf("Prepare() work=%v result=%+v", work != nil, result)
	}
}

func reconcileDelivery(
	t *testing.T,
	check verification.Check,
	attempt verification.Attempt,
	now time.Time,
) platformtask.Delivery {
	t.Helper()
	taskID, _ := id.ParseTask("tsk_" + taskTestULID)
	scope, _ := tenant.NewScope(check.TenantID)
	intent, err := NewReconcileIntent(
		fixedTaskIDs{taskID}, scope, ReconcilePayload{CheckID: check.ID, AttemptID: attempt.ID},
		IntentMetadata{ScheduledAt: now, Deadline: now.Add(MaximumReconcileDuration)},
	)
	if err != nil {
		t.Fatal(err)
	}
	return platformtask.Delivery{Intent: intent, Attempt: 1, Fence: 9}
}
