package headgate

import (
	"context"
	"testing"

	libheadgate "github.com/mujhtech/headgate/go"
)

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
