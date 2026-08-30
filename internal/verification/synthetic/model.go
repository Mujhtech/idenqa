package synthetic

import (
	"context"
	"errors"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

// Model is a controllable model executor with an injected clock.
type Model struct {
	Scenario Scenario
	Now      func() time.Time
}

// Execute returns only synthetic bounded signals and honours cancellation.
func (model Model) Execute(ctx context.Context, request modelv1.Request) (modelv1.Result, error) {
	if model.Now == nil {
		return modelv1.Result{}, errors.New("synthetic model clock is required")
	}
	if err := ctx.Err(); err != nil {
		return modelv1.Result{}, err
	}
	if model.Scenario == Timeout {
		<-ctx.Done()
		return modelv1.Result{}, ctx.Err()
	}
	result := modelv1.Result{Contract: request.Contract, AttemptID: request.AttemptID,
		Outcome: modelv1.ResultOutcomeCompleted, CompletedAt: model.Now().UTC()}
	switch model.Scenario {
	case Success:
		result.Signals = []modelv1.Signal{{Name: "synthetic.liveness", Outcome: modelv1.SignalOutcomeSatisfied}}
	case Rejected:
		result.Signals = []modelv1.Signal{{Name: "synthetic.liveness", Outcome: modelv1.SignalOutcomeNotSatisfied,
			ReasonCodes: []string{"synthetic_not_live"}}}
	case Inconclusive:
		result.Signals = []modelv1.Signal{{Name: "synthetic.liveness", Outcome: modelv1.SignalOutcomeInconclusive,
			ReasonCodes: []string{"synthetic_low_quality"}}}
	case Unavailable:
		result.Outcome = modelv1.ResultOutcomeFailed
		result.Failure = &modelv1.Failure{Class: modelv1.FailureUnavailable, Code: "synthetic_unavailable",
			Retry: modelv1.RetryBackoff, RetryAfter: time.Second}
	case Malformed:
		result.AttemptID = "atm_00000000000000000000000000"
	default:
		return modelv1.Result{}, errors.New("unknown synthetic model scenario")
	}
	return result, nil
}

var _ modelv1.Executor = Model{}
