// Package synthetic provides deterministic V-02 runner fakes. It never reads real evidence.
package synthetic

import (
	"context"
	"errors"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

// Scenario selects one deterministic synthetic execution path.
type Scenario string

const (
	// Success begins the closed set of deterministic synthetic scenarios.
	Success Scenario = "success"
	// Rejected returns a defined not-satisfied evidence signal.
	Rejected Scenario = "rejected"
	// Inconclusive returns a defined inconclusive evidence signal.
	Inconclusive Scenario = "inconclusive"
	// Unavailable returns a retryable operational failure.
	Unavailable Scenario = "unavailable"
	// Malformed returns an invalid attempt binding.
	Malformed Scenario = "malformed"
	// Timeout waits for context cancellation or deadline.
	Timeout Scenario = "timeout"
)

// Provider is a controllable provider executor with an injected clock.
type Provider struct {
	Scenario Scenario
	Now      func() time.Time
}

// Execute returns only synthetic bounded signals and honours cancellation.
func (provider Provider) Execute(ctx context.Context, request providerv1.Request) (providerv1.Result, error) {
	if provider.Now == nil {
		return providerv1.Result{}, errors.New("synthetic provider clock is required")
	}
	if err := ctx.Err(); err != nil {
		return providerv1.Result{}, err
	}
	if provider.Scenario == Timeout {
		<-ctx.Done()
		return providerv1.Result{}, ctx.Err()
	}
	result := providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID,
		Outcome: providerv1.ResultOutcomeCompleted, CompletedAt: provider.Now().UTC()}
	switch provider.Scenario {
	case Success:
		result.Signals = []providerv1.Signal{{Name: "synthetic.document", Outcome: providerv1.SignalOutcomeSatisfied}}
	case Rejected:
		result.Signals = []providerv1.Signal{{Name: "synthetic.document", Outcome: providerv1.SignalOutcomeNotSatisfied,
			ReasonCodes: []string{"synthetic_no_match"}}}
	case Inconclusive:
		result.Signals = []providerv1.Signal{{Name: "synthetic.document", Outcome: providerv1.SignalOutcomeInconclusive,
			ReasonCodes: []string{"synthetic_insufficient_data"}}}
	case Unavailable:
		result.Outcome = providerv1.ResultOutcomeFailed
		result.Failure = &providerv1.Failure{Class: providerv1.FailureUnavailable, Code: "synthetic_unavailable",
			Retry: providerv1.RetryBackoff, RetryAfter: time.Second}
	case Malformed:
		result.AttemptID = "atm_00000000000000000000000000"
	default:
		return providerv1.Result{}, errors.New("unknown synthetic provider scenario")
	}
	return result, nil
}

var _ providerv1.Executor = Provider{}
