package provider_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/provider"
)

type recordingProviderMetrics struct {
	mu         sync.Mutex
	dispatches []observability.ProviderDispatch
	delays     []observability.ProviderCallbackDelay
}

func (metrics *recordingProviderMetrics) RecordProviderDispatch(dispatch observability.ProviderDispatch) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	metrics.dispatches = append(metrics.dispatches, dispatch)
}

func (metrics *recordingProviderMetrics) RecordProviderCallbackDelay(delay observability.ProviderCallbackDelay) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	metrics.delays = append(metrics.delays, delay)
}

func TestDurableExecutorRecordsOnlyBoundedProviderLabels(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	store := &receiptStore{}
	metrics := &recordingProviderMetrics{}
	remote := executorFunc(func(context.Context, providerv1.Request) (providerv1.Result, error) {
		return providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted, Signals: []providerv1.Signal{{Name: "idenqa.signal.document_quality", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: request.Deadline.Add(-time.Second)}, nil
	})
	executor, err := provider.NewDurableExecutor(store, remote, func() time.Time { return request.Deadline.Add(-time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	executor.WithMetrics(metrics)
	if _, err := executor.Execute(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if len(metrics.dispatches) != 1 {
		t.Fatalf("dispatches = %d, want 1", len(metrics.dispatches))
	}
	dispatch := metrics.dispatches[0]
	if dispatch.Provider.Safe() != request.Adapter.AdapterID {
		t.Fatalf("provider label = %q, want adapter id %q", dispatch.Provider.Safe(), request.Adapter.AdapterID)
	}
	if dispatch.Outcome != observability.DispatchCompleted || dispatch.FailureClass != observability.FailureNone {
		t.Fatalf("dispatch = %+v", dispatch)
	}
	for _, forbidden := range []string{request.TenantID, request.VerificationID, request.Evidence[0].EvidenceID, "ten_", "ver_", "evd_"} {
		if strings.Contains(dispatch.Provider.Safe(), forbidden) {
			t.Fatalf("provider label leaked %q", forbidden)
		}
	}
}

func TestDurableExecutorRecordsUncertainAndRecoveredOutcomes(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	store := &receiptStore{}
	metrics := &recordingProviderMetrics{}
	remote := executorFunc(func(context.Context, providerv1.Request) (providerv1.Result, error) {
		return providerv1.Result{}, errors.New("runner unavailable")
	})
	executor, err := provider.NewDurableExecutor(store, remote, func() time.Time { return request.Deadline.Add(-time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	executor.WithMetrics(metrics)
	if _, err := executor.Execute(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if len(metrics.dispatches) != 2 {
		t.Fatalf("dispatches = %d, want 2", len(metrics.dispatches))
	}
	if metrics.dispatches[0].Outcome != observability.DispatchUncertain || metrics.dispatches[0].FailureClass != observability.FailureUnavailable {
		t.Fatalf("first dispatch = %+v", metrics.dispatches[0])
	}
	if metrics.dispatches[1].Outcome != observability.DispatchRecovered || metrics.dispatches[1].FailureClass != observability.FailureNone {
		t.Fatalf("second dispatch = %+v", metrics.dispatches[1])
	}
}
