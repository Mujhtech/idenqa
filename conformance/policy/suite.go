// Package policy provides the public policy-engine conformance test kit.
//
// It deliberately depends only on the public policy v1 contract. Implementors
// can therefore test an engine without importing Idenqa's internal CEL adapter.
package policy

import (
	"context"
	"errors"
	"fmt"
	"slices"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
)

// ErrNonConforming is returned when an engine changes public policy meaning.
var ErrNonConforming = errors.New("policy conformance: non-conforming engine")

// Facts is the complete reference-only input exposed to a policy engine.
type Facts struct {
	Region string
	Values map[string]policyv1.RequirementState
}

// Result is one matched public rule in canonical rule-name order.
type Result struct {
	Name      string
	State     policyv1.RequirementState
	Directive policyv1.Directive
	Priority  uint16
	Facts     []string
	Reasons   []string
}

// Engine evaluates one already validated public document. It must honor
// cancellation, must not mutate its inputs, and must be deterministic.
type Engine interface {
	Evaluate(context.Context, policyv1.Document, Facts) ([]Result, error)
}

// Case describes one portable policy scenario and its exact expected output.
type Case struct {
	Name     string
	Document policyv1.Document
	Facts    Facts
	Want     []Result
}

// Run executes the bounded public conformance suite against engine.
func Run(ctx context.Context, engine Engine, cases []Case) error {
	if ctx == nil || engine == nil || len(cases) == 0 || len(cases) > 128 {
		return ErrNonConforming
	}
	for index, test := range cases {
		if test.Name == "" || policyv1.Validate(test.Document) != nil {
			return fmt.Errorf("%w: case %d is invalid", ErrNonConforming, index)
		}
		facts := cloneFacts(test.Facts)
		first, err := engine.Evaluate(ctx, test.Document, cloneFacts(facts))
		if err != nil {
			return fmt.Errorf("%w: case %q: %w", ErrNonConforming, test.Name, err)
		}
		second, err := engine.Evaluate(ctx, test.Document, cloneFacts(facts))
		if err != nil || !equalResults(first, second) || !equalResults(first, test.Want) || !equalFacts(facts, test.Facts) {
			return fmt.Errorf("%w: case %q changed deterministic meaning", ErrNonConforming, test.Name)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := engine.Evaluate(cancelled, cases[0].Document, cloneFacts(cases[0].Facts)); !errors.Is(err, context.Canceled) {
		return fmt.Errorf("%w: engine ignored cancellation", ErrNonConforming)
	}
	return nil
}

func cloneFacts(value Facts) Facts {
	cloned := Facts{Region: value.Region, Values: make(map[string]policyv1.RequirementState, len(value.Values))}
	for key, state := range value.Values {
		cloned.Values[key] = state
	}
	return cloned
}

func equalFacts(left, right Facts) bool {
	if left.Region != right.Region || len(left.Values) != len(right.Values) {
		return false
	}
	for key, value := range left.Values {
		if right.Values[key] != value {
			return false
		}
	}
	return true
}

func equalResults(left, right []Result) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index].Name != right[index].Name || left[index].State != right[index].State ||
			left[index].Directive != right[index].Directive || left[index].Priority != right[index].Priority ||
			!slices.Equal(left[index].Facts, right[index].Facts) || !slices.Equal(left[index].Reasons, right[index].Reasons) {
			return false
		}
	}
	return true
}
