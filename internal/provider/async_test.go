package provider_test

import (
	"context"
	"errors"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/provider"
)

type asyncMemory struct {
	claimed bool
	fence   int64
	result  *providerv1.Result
}

func (store *asyncMemory) ClaimAsync(context.Context, providerv1.Request) (provider.AsyncClaim, error) {
	initial := !store.claimed
	store.claimed = true
	store.fence++
	return provider.AsyncClaim{Initial: initial, Acquired: true, Fence: store.fence, Result: store.result}, nil
}
func (store *asyncMemory) SaveProgress(_ context.Context, _ providerv1.Request, claim provider.AsyncClaim, progress providerv1.Progress) error {
	if claim.Fence != store.fence {
		return provider.ErrDispatchPending
	}
	store.result = progress.Result
	return nil
}

type advanceFunc func(context.Context, providerv1.Request, bool) (providerv1.Progress, error)

func (function advanceFunc) Advance(ctx context.Context, request providerv1.Request, resume bool) (providerv1.Progress, error) {
	return function(ctx, request, resume)
}
func TestAsyncLostSubmissionReplyRecoversWithoutResubmission(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	store := &asyncMemory{}
	calls := 0
	remote := advanceFunc(func(_ context.Context, r providerv1.Request, resume bool) (providerv1.Progress, error) {
		calls++
		if calls == 1 {
			if resume {
				t.Fatal("initial was recovery")
			}
			return providerv1.Progress{}, errors.New("lost acknowledgement")
		}
		if !resume {
			t.Fatal("repeated initial dispatch")
		}
		return providerv1.Progress{ProviderJobID: "job-1", Result: &providerv1.Result{Contract: r.Contract, AttemptID: r.AttemptID, Outcome: providerv1.ResultOutcomeCompleted, Signals: []providerv1.Signal{{Name: "idenqa.signal.document_quality", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: r.Deadline.Add(-time.Second)}}, nil
	})
	makeExecutor := func() *provider.AsyncExecutor {
		executor, err := provider.NewAsyncExecutor(store, remote, func() time.Time { return request.Deadline.Add(-time.Second) })
		if err != nil {
			t.Fatal(err)
		}
		return executor
	}
	if _, err := makeExecutor().Execute(t.Context(), request); !errors.Is(err, provider.ErrDispatchPending) {
		t.Fatalf("ambiguous reply terminalized: %v", err)
	}
	result, err := makeExecutor().Execute(t.Context(), request)
	if err != nil || result.Outcome != providerv1.ResultOutcomeCompleted {
		t.Fatalf("restart recovery: %v %v", result, err)
	}
	if _, err := makeExecutor().Execute(t.Context(), request); err != nil || calls != 2 {
		t.Fatalf("durable final receipt not reused: calls=%d err=%v", calls, err)
	}
}
