package provider_test

import (
	"context"
	"errors"
	"strings"
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

func TestAsyncExecutorConsumesDocumentObservationBeforeSaveProgress(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	store := &asyncMemory{}
	observation := testDocumentObservation(t)
	remote := advanceFunc(func(_ context.Context, r providerv1.Request, resume bool) (providerv1.Progress, error) {
		if resume {
			t.Fatal("initial submission used a recovery resume")
		}
		result := providerv1.Result{
			Contract: r.Contract, AttemptID: r.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
			Signals:  []providerv1.Signal{{Name: "idenqa.signal.provider_job", Outcome: providerv1.SignalOutcomeSatisfied}},
			Document: &observation, CompletedAt: r.Deadline.Add(-time.Second),
		}
		return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1", Result: &result}, nil
	})
	executor, err := provider.NewAsyncExecutor(store, remote, func() time.Time { return request.Deadline.Add(-time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	result, err := executor.Execute(t.Context(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Document != nil {
		t.Fatal("returned result retained the transient document observation")
	}
	assertDerivedDocumentSignal(t, result)
	if store.result == nil || store.result.Document != nil {
		t.Fatalf("saved progress retained the transient document observation: %+v", store.result)
	}
	assertDerivedDocumentSignal(t, *store.result)
	raw := resultJSON(t, *store.result)
	if strings.Contains(raw, "X10000001") || strings.Contains(raw, "DOE") {
		t.Fatalf("saved progress retained raw document values: %s", raw)
	}

	recovered, err := executor.Execute(t.Context(), request)
	if err != nil || recovered.Document != nil || !resultJSONEqual(t, recovered, *store.result) {
		t.Fatalf("recovered result = %+v err=%v", recovered, err)
	}
}

func TestAsyncExecutorFailsClosedOnMalformedDocumentObservation(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	store := &asyncMemory{}
	remote := advanceFunc(func(_ context.Context, r providerv1.Request, _ bool) (providerv1.Progress, error) {
		result := providerv1.Result{
			Contract: r.Contract, AttemptID: r.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
			Signals:     []providerv1.Signal{{Name: "idenqa.signal.provider_job", Outcome: providerv1.SignalOutcomeSatisfied}},
			Document:    &providerv1.DocumentObservation{MRZLines: []string{strings.Repeat("A", 45)}},
			CompletedAt: r.Deadline.Add(-time.Second),
		}
		return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1", Result: &result}, nil
	})
	executor, err := provider.NewAsyncExecutor(store, remote, func() time.Time { return request.Deadline.Add(-time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(t.Context(), request); !errors.Is(err, provider.ErrDispatchPending) {
		t.Fatalf("malformed observation = %v", err)
	}
	if store.result != nil {
		t.Fatalf("malformed observation was persisted: %+v", store.result)
	}
}

func resultJSONEqual(t *testing.T, left, right providerv1.Result) bool {
	t.Helper()
	return resultJSON(t, left) == resultJSON(t, right)
}
