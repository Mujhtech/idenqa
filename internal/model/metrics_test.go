package model_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
)

type recordingModelMetrics struct {
	mu         sync.Mutex
	dispatches []observability.ModelDispatch
	health     []observability.ModelHealth
}

func (metrics *recordingModelMetrics) RecordModelDispatch(dispatch observability.ModelDispatch) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	metrics.dispatches = append(metrics.dispatches, dispatch)
}

func (metrics *recordingModelMetrics) RecordModelHealth(health observability.ModelHealth) {
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	metrics.health = append(metrics.health, health)
}

func TestDurableExecutorRecordsBoundedModelDispatch(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	store := &receiptStore{}
	metrics := &recordingModelMetrics{}
	remote := executorFunc(func(context.Context, modelv1.Request) (modelv1.Result, error) {
		return modelv1.Result{
			Contract: request.Contract, AttemptID: request.AttemptID, Outcome: modelv1.ResultOutcomeCompleted,
			Signals:     []modelv1.Signal{{Name: "idenqa.signal.passive_pad", Outcome: modelv1.SignalOutcomeSatisfied}},
			CompletedAt: request.Deadline,
		}, nil
	})
	executor, err := model.NewDurableExecutor(store, remote, func() time.Time { return request.Deadline.Add(-time.Second) })
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
	if dispatch.Model.Safe() != request.Provenance.ModelID {
		t.Fatalf("model label = %q, want %q", dispatch.Model.Safe(), request.Provenance.ModelID)
	}
	if dispatch.Outcome != observability.DispatchCompleted || dispatch.FailureClass != observability.FailureNone {
		t.Fatalf("dispatch = %+v", dispatch)
	}
	if dispatch.Duration != time.Second {
		t.Fatalf("duration = %v, want 1s", dispatch.Duration)
	}
	for _, forbidden := range []string{request.TenantID, request.VerificationID, request.Evidence[0].EvidenceID, "ten_", "ver_", "evd_"} {
		if strings.Contains(dispatch.Model.Safe(), forbidden) {
			t.Fatalf("model label leaked %q", forbidden)
		}
	}
}

func TestSupervisedExecutorRecordsBoundedHealth(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	metrics := &recordingModelMetrics{}
	health := healthFunc(func(context.Context) (modelv1.Health, error) {
		return modelv1.Health{State: modelv1.HealthDegraded, CheckedAt: request.Deadline.Add(-time.Second)}, nil
	})
	inner := executorFunc(func(context.Context, modelv1.Request) (modelv1.Result, error) {
		t.Fatal("degraded runner must not execute")
		return modelv1.Result{}, nil
	})
	supervised, err := model.NewSupervisedExecutor(inner, health, func() time.Time { return request.Deadline.Add(-time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	supervised.WithMetrics(metrics)
	if _, err := supervised.Execute(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	metrics.mu.Lock()
	defer metrics.mu.Unlock()
	if len(metrics.health) != 1 || metrics.health[0].State != observability.HealthDegraded {
		t.Fatalf("health = %+v", metrics.health)
	}
	if metrics.health[0].Model.Safe() != request.Provenance.ModelID {
		t.Fatalf("model label = %q", metrics.health[0].Model.Safe())
	}
}
