package headgate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	libheadgate "github.com/mujhtech/headgate/go"
)

func TestRuntimeTransactionRejectsUnusableIsolation(t *testing.T) {
	t.Parallel()
	isolationErr := errors.New("cannot change isolation")
	for _, test := range []struct {
		name string
		tx   any
		want error
	}{
		{name: "foreign transaction", tx: struct{}{}, want: task.ErrInvalid},
		{name: "isolation failure", tx: &isolationTransaction{err: isolationErr}, want: isolationErr},
	} {
		t.Run(test.name, func(t *testing.T) {
			base := &transactionalStoreStub{tx: runtimeTransactionStub{value: test.tx}}
			store := &transactionalRetryStore{transactional: base}
			transaction, err := store.BeginTx(t.Context())
			if transaction != nil || !errors.Is(err, test.want) {
				t.Fatalf("BeginTx() = %v, %v; want nil, %v", transaction, err, test.want)
			}
			if !base.rolledBack {
				t.Fatal("unusable transaction was not rolled back")
			}
		})
	}
}

func TestRetryDelayRetriesSerializationConflictsPromptly(t *testing.T) {
	t.Parallel()
	policy := task.RetryPolicy{MaxAttempts: 8, InitialBackoff: time.Second, MaximumBackoff: time.Hour, JitterPercent: 20}
	store := newRetryStore(nil)
	store.claims["tsk_conflict"] = retryClaim{policy: policy, attempt: 2, seed: "conflict-seed"}
	store.claims["tsk_unavailable"] = retryClaim{policy: policy, attempt: 2, seed: "unavailable-seed"}

	delay := store.retryDelay("tsk_conflict", libheadgate.OutcomeRetry, 0,
		"advance webhook fanout: ERROR: could not serialize access due to read/write dependencies among transactions (SQLSTATE 40001)")
	if minimum, maximum := conflictInitialBackoff.Milliseconds(), (conflictInitialBackoff + conflictJitterBackoff).Milliseconds(); delay < minimum || delay >= maximum {
		t.Fatalf("serialization conflict delay = %dms, want [%d,%d)", delay, minimum, maximum)
	}
	if again := store.retryDelay("tsk_conflict", libheadgate.OutcomeRetry, 0, "ERROR: (SQLSTATE 40001)"); again != delay {
		t.Fatalf("serialization conflict delay = %dms then %dms, want deterministic", delay, again)
	}
	unavailable := store.retryDelay("tsk_unavailable", libheadgate.OutcomeRetry, 0, "dial tcp: connection refused")
	if minimum, maximum := int64(1600), int64(2400); unavailable < minimum || unavailable > maximum {
		t.Fatalf("unavailable delay = %dms, want policy backoff in [%d,%d]", unavailable, minimum, maximum)
	}
	if supplied := store.retryDelay("tsk_unavailable", libheadgate.OutcomeRetry, 5000, "dial tcp: connection refused"); supplied != 5000 {
		t.Fatalf("supplied delay = %dms, want 5000ms", supplied)
	}
}

type transactionalStoreStub struct {
	libheadgate.TransactionalStore
	tx         libheadgate.Tx
	rolledBack bool
}

func (store *transactionalStoreStub) BeginTx(context.Context) (libheadgate.Tx, error) {
	return store.tx, nil
}

func (store *transactionalStoreStub) RollbackTx(ctx context.Context, transaction libheadgate.Tx) error {
	store.rolledBack = transaction == store.tx && ctx.Err() == nil
	return nil
}

type runtimeTransactionStub struct{ value any }

func (transaction runtimeTransactionStub) Unwrap() any { return transaction.value }

type isolationTransaction struct {
	pgx.Tx
	err error
}

func (transaction *isolationTransaction) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	return pgconn.CommandTag{}, transaction.err
}

type retryStoreStub struct {
	libheadgate.Store
	claim   libheadgate.Claim
	delayMs int64
}

func (store *retryStoreStub) Admit(
	context.Context,
	libheadgate.AdmitRequest,
) ([]libheadgate.AdmissionUnit, error) {
	return []libheadgate.AdmissionUnit{{Claims: []libheadgate.Claim{store.claim}}}, nil
}

func (store *retryStoreStub) AckAttemptWithActualWeight(
	_ context.Context,
	_ libheadgate.LeaseRef,
	_ libheadgate.Outcome,
	_ string,
	delayMs int64,
	_ []string,
	_ *uint32,
) error {
	store.delayMs = delayMs
	return nil
}

func TestRetryStoreSuppliesOwnedDeterministicDelay(t *testing.T) {
	t.Parallel()
	intent := testIntent(t)
	envelope, err := envelopeOf(intent, "idenqa-test")
	if err != nil {
		t.Fatal(err)
	}
	envelope.Attempt = 1
	base := &retryStoreStub{claim: libheadgate.Claim{
		Envelope: envelope,
		LeaseID:  "lease",
		Fence:    2,
	}}
	store := newRetryStore(base)
	units, err := store.Admit(t.Context(), libheadgate.AdmitRequest{})
	if err != nil || len(units) != 1 {
		t.Fatalf("Admit() = %v, %v", units, err)
	}
	claim := units[0].Claims[0]
	lease := libheadgate.LeaseRef{
		JobID: claim.Envelope.ID, LeaseID: claim.LeaseID, Fence: claim.Fence,
	}
	if err := store.AckAttemptWithActualWeight(
		t.Context(), lease, libheadgate.OutcomeRetry, "temporary", 0, nil, nil,
	); err != nil {
		t.Fatal(err)
	}
	want := intent.Retry().Backoff(2, intent.ID().String()).Milliseconds()
	if base.delayMs != want {
		t.Fatalf("retry delay = %dms, want %dms", base.delayMs, want)
	}
}

func TestRetryStorePreservesExplicitHeadgateDelay(t *testing.T) {
	t.Parallel()
	base := &retryStoreStub{}
	store := newRetryStore(base)
	if err := store.AckAttemptWithActualWeight(
		t.Context(), libheadgate.LeaseRef{JobID: "job"},
		libheadgate.OutcomeRetry, "temporary", 73, nil, nil,
	); err != nil {
		t.Fatal(err)
	}
	if base.delayMs != 73 {
		t.Fatalf("retry delay = %dms, want 73ms", base.delayMs)
	}
}
