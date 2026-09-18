package model_test

import (
	"context"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/model"
)

type healthFunc func(context.Context) (modelv1.Health, error)

func (f healthFunc) Health(ctx context.Context) (modelv1.Health, error) { return f(ctx) }
func TestSupervisionFencesUnhealthyAndStaleRunner(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	now := request.Deadline.Add(-time.Second)
	for _, test := range []struct {
		name      string
		state     modelv1.HealthState
		age       time.Duration
		wantCalls int
	}{
		{"ready", modelv1.HealthReady, 0, 1}, {"degraded", modelv1.HealthDegraded, 0, 0}, {"stale", modelv1.HealthReady, -time.Minute, 0}, {"future", modelv1.HealthReady, time.Minute, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			remote := executorFunc(func(context.Context, modelv1.Request) (modelv1.Result, error) { calls++; return modelv1.Result{}, nil })
			executor, err := model.NewSupervisedExecutor(remote, healthFunc(func(context.Context) (modelv1.Health, error) {
				return modelv1.Health{State: test.state, CheckedAt: now.Add(test.age)}, nil
			}), func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			result, err := executor.Execute(t.Context(), request)
			if err != nil || calls != test.wantCalls {
				t.Fatal("unexpected dispatch", calls, err)
			}
			if calls == 0 && (result.Failure == nil || result.Failure.Code != "model_not_ready") {
				t.Fatal("missing safe failure")
			}
		})
	}
}
