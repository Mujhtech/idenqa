package headgate

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/task"
	libheadgate "github.com/mujhtech/headgate/go"
)

// retryStore supplies the task-specific delay that Headgate's PostgreSQL Ack
// boundary already supports. Headgate v0.1.2 does not call Config.RetryPolicy,
// so the claimed durable envelope is retained only for the lifetime of its
// lease and translated at Ack time.
type retryStore struct {
	libheadgate.Store
	mu     sync.Mutex
	claims map[string]retryClaim
}

type retryClaim struct {
	policy  task.RetryPolicy
	attempt uint32
	seed    string
}

func newRetryStore(store libheadgate.Store) *retryStore {
	return &retryStore{Store: store, claims: make(map[string]retryClaim)}
}

func newRuntimeStore(store libheadgate.Store) libheadgate.Store {
	retries := newRetryStore(store)
	transactional, ok := store.(libheadgate.TransactionalStore)
	if !ok {
		return retries
	}
	return &transactionalRetryStore{retryStore: retries, transactional: transactional}
}

type transactionalRetryStore struct {
	*retryStore
	transactional libheadgate.TransactionalStore
}

type retryTransaction struct {
	transaction libheadgate.Tx
	completed   []string
}

func (transaction *retryTransaction) Unwrap() any { return transaction.transaction.Unwrap() }

func (store *transactionalRetryStore) BeginTx(ctx context.Context) (libheadgate.Tx, error) {
	transaction, err := store.transactional.BeginTx(ctx)
	if err != nil {
		return nil, err
	}
	return &retryTransaction{transaction: transaction}, nil
}

func (store *transactionalRetryStore) CommitTx(ctx context.Context, transaction libheadgate.Tx) error {
	retryTx, err := ownedRetryTransaction(transaction)
	if err != nil {
		return err
	}
	if err := store.transactional.CommitTx(ctx, retryTx.transaction); err != nil {
		return err
	}
	store.releaseClaims(retryTx.completed)
	return nil
}

func (store *transactionalRetryStore) RollbackTx(ctx context.Context, transaction libheadgate.Tx) error {
	retryTx, err := ownedRetryTransaction(transaction)
	if err != nil {
		return err
	}
	return store.transactional.RollbackTx(ctx, retryTx.transaction)
}

func (store *transactionalRetryStore) EnqueueTx(
	ctx context.Context,
	transaction libheadgate.Tx,
	batch []libheadgate.Envelope,
) error {
	return store.transactional.EnqueueTx(ctx, unwrapRetryTransaction(transaction), batch)
}

func (store *transactionalRetryStore) CompleteTx(
	ctx context.Context,
	transaction libheadgate.Tx,
	lease libheadgate.LeaseRef,
) error {
	return store.CompleteTxWithActualWeight(ctx, transaction, lease, nil)
}

func (store *transactionalRetryStore) CompleteTxWithActualWeight(
	ctx context.Context,
	transaction libheadgate.Tx,
	lease libheadgate.LeaseRef,
	actualWeight *uint32,
) error {
	if err := store.transactional.CompleteTxWithActualWeight(
		ctx, unwrapRetryTransaction(transaction), lease, actualWeight,
	); err != nil {
		return err
	}
	if retryTx, ok := transaction.(*retryTransaction); ok {
		retryTx.completed = append(retryTx.completed, lease.JobID)
	}
	return nil
}

func (store *transactionalRetryStore) ClaimEffect(
	ctx context.Context,
	transaction libheadgate.Tx,
	key string,
) (bool, error) {
	return store.transactional.ClaimEffect(ctx, unwrapRetryTransaction(transaction), key)
}

func (store *transactionalRetryStore) CheckpointTx(
	ctx context.Context,
	transaction libheadgate.Tx,
	lease libheadgate.LeaseRef,
	checkpoint libheadgate.Checkpoint,
) error {
	return store.transactional.CheckpointTx(ctx, unwrapRetryTransaction(transaction), lease, checkpoint)
}

func ownedRetryTransaction(transaction libheadgate.Tx) (*retryTransaction, error) {
	retryTx, ok := transaction.(*retryTransaction)
	if !ok || retryTx.transaction == nil {
		return nil, errors.New("verification task: foreign retry transaction")
	}
	return retryTx, nil
}

func unwrapRetryTransaction(transaction libheadgate.Tx) libheadgate.Tx {
	if retryTx, ok := transaction.(*retryTransaction); ok {
		return retryTx.transaction
	}
	return transaction
}

func (store *retryStore) Admit(
	ctx context.Context,
	request libheadgate.AdmitRequest,
) ([]libheadgate.AdmissionUnit, error) {
	units, err := store.Store.Admit(ctx, request)
	if err != nil {
		return nil, err
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, unit := range units {
		for _, claim := range unit.Claims {
			intent, parseErr := intentOf(claim.Envelope)
			if parseErr != nil {
				continue
			}
			store.claims[claim.Envelope.ID] = retryClaim{
				policy: intent.Retry(), attempt: claim.Envelope.Attempt + 1,
				seed: intent.ID().String(),
			}
		}
	}
	return units, nil
}

func (store *retryStore) Ack(
	ctx context.Context,
	lease libheadgate.LeaseRef,
	outcome libheadgate.Outcome,
	errMsg string,
	delayMs int64,
) error {
	delayMs = store.retryDelay(lease.JobID, outcome, delayMs)
	err := store.Store.Ack(ctx, lease, outcome, errMsg, delayMs)
	store.releaseClaim(lease.JobID, err)
	return err
}

func (store *retryStore) AckAttempt(
	ctx context.Context,
	lease libheadgate.LeaseRef,
	outcome libheadgate.Outcome,
	errMsg string,
	delayMs int64,
	logs []string,
) error {
	delayMs = store.retryDelay(lease.JobID, outcome, delayMs)
	err := store.Store.AckAttempt(ctx, lease, outcome, errMsg, delayMs, logs)
	store.releaseClaim(lease.JobID, err)
	return err
}

func (store *retryStore) AckAttemptWithActualWeight(
	ctx context.Context,
	lease libheadgate.LeaseRef,
	outcome libheadgate.Outcome,
	errMsg string,
	delayMs int64,
	logs []string,
	actualWeight *uint32,
) error {
	delayMs = store.retryDelay(lease.JobID, outcome, delayMs)
	err := store.Store.AckAttemptWithActualWeight(
		ctx, lease, outcome, errMsg, delayMs, logs, actualWeight,
	)
	store.releaseClaim(lease.JobID, err)
	return err
}

func (store *retryStore) Renew(
	ctx context.Context,
	leases []libheadgate.LeaseRef,
	leaseDuration time.Duration,
) ([]string, error) {
	lost, err := store.Store.Renew(ctx, leases, leaseDuration)
	if err == nil {
		store.releaseClaims(lost)
	}
	return lost, err
}

func (store *retryStore) ReclaimExpired(
	ctx context.Context,
	limit int64,
) ([]libheadgate.Reclaimed, error) {
	reclaimed, err := store.Store.ReclaimExpired(ctx, limit)
	if err == nil {
		identifiers := make([]string, 0, len(reclaimed))
		for _, item := range reclaimed {
			identifiers = append(identifiers, item.JobID)
		}
		store.releaseClaims(identifiers)
	}
	return reclaimed, err
}

func (store *retryStore) retryDelay(jobID string, outcome libheadgate.Outcome, supplied int64) int64 {
	if outcome != libheadgate.OutcomeRetry || supplied != 0 {
		return supplied
	}
	store.mu.Lock()
	claim, exists := store.claims[jobID]
	store.mu.Unlock()
	if !exists {
		return supplied
	}
	delay := claim.policy.Backoff(claim.attempt, claim.seed)
	if delay < time.Millisecond {
		return supplied
	}
	return delay.Milliseconds()
}

func (store *retryStore) releaseClaim(jobID string, ackErr error) {
	if ackErr != nil {
		return
	}
	store.releaseClaims([]string{jobID})
}

func (store *retryStore) releaseClaims(jobIDs []string) {
	store.mu.Lock()
	defer store.mu.Unlock()
	for _, jobID := range jobIDs {
		delete(store.claims, jobID)
	}
}
