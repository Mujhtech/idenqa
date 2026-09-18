package model

import (
	"context"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

// SupervisedExecutor checks current runner readiness before each new dispatch.
// Saved durable results bypass this adapter and remain replayable during an outage.
type SupervisedExecutor struct {
	executor modelv1.Executor
	health   modelv1.HealthChecker
	now      func() time.Time
}

// NewSupervisedExecutor bounds readiness probes without retaining request contexts.
func NewSupervisedExecutor(executor modelv1.Executor, health modelv1.HealthChecker, now func() time.Time) (*SupervisedExecutor, error) {
	if executor == nil || health == nil || now == nil {
		return nil, ErrRequestUnavailable
	}
	return &SupervisedExecutor{executor, health, now}, nil
}

// Execute never sends evidence grants to an unhealthy or stale runner.
func (executor *SupervisedExecutor) Execute(ctx context.Context, request modelv1.Request) (modelv1.Result, error) {
	if request.Validate() != nil {
		return modelv1.Result{}, ErrRequestUnavailable
	}
	probe, cancel := context.WithTimeout(ctx, 2*time.Second)
	health, err := executor.health.Health(probe)
	cancel()
	now := executor.now().UTC()
	if err != nil || health.State != modelv1.HealthReady || health.CheckedAt.After(now.Add(time.Second)) || health.CheckedAt.Before(now.Add(-5*time.Second)) {
		at := now
		if at.After(request.Deadline) {
			at = request.Deadline
		}
		return modelv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: modelv1.ResultOutcomeFailed, CompletedAt: at, Failure: &modelv1.Failure{Class: modelv1.FailureUnavailable, Code: "model_not_ready", Retry: modelv1.RetryBackoff}}, nil
	}
	return executor.executor.Execute(ctx, request)
}
