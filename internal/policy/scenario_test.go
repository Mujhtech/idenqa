package policy_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
)

func TestScenarioSuiteReportsAllMatchesAndMismatches(t *testing.T) {
	t.Parallel()
	suite := newScenarioSuite(t, policycel.Compiler{})
	scenarios := []policy.Scenario{
		scenarioFixture(t, "verified", policy.RequirementSatisfied, policy.DirectiveCompleteVerified,
			policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
		scenarioFixture(t, "mismatch_one", policy.RequirementSatisfied, policy.DirectiveCompleteVerified,
			policy.RequirementUnavailable, policy.DirectiveRequestInput),
		scenarioFixture(t, "mismatch_two", policy.RequirementUnavailable, policy.DirectiveRequestInput,
			policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
	}
	results, report, err := suite.Run(t.Context(), scenarios)
	if err != nil {
		t.Fatal(err)
	}
	if report.Passed || len(results) != 3 || len(report.Cases) != 3 || report.Digest == "" {
		t.Fatalf("report = %+v", report)
	}
	if results[0].Name() != "mismatch_one" || results[0].Matches() ||
		results[1].Name() != "mismatch_two" || results[1].Matches() ||
		results[2].Name() != "verified" || !results[2].Matches() {
		t.Fatalf("results = %+v", report.Cases)
	}
	for _, result := range results {
		if _, _, err := policy.RestoreSimulationBundle(result.Bundle().Canonical()); err != nil {
			t.Fatalf("restore %q bundle: %v", result.Name(), err)
		}
		if result.Report().EvaluationDigest == "" || result.ExpectedEvaluationDigest() == "" {
			t.Fatalf("missing digests for %q", result.Name())
		}
	}
}

func TestScenarioSuiteDigestIsIndependentOfInputOrder(t *testing.T) {
	t.Parallel()
	suite := newScenarioSuite(t, policycel.Compiler{})
	scenarios := []policy.Scenario{
		scenarioFixture(t, "zulu", policy.RequirementSatisfied, policy.DirectiveCompleteVerified,
			policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
		scenarioFixture(t, "alpha", policy.RequirementUnavailable, policy.DirectiveRequestInput,
			policy.RequirementUnavailable, policy.DirectiveRequestInput),
	}
	firstResults, first, err := suite.Run(t.Context(), scenarios)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(scenarios)
	secondResults, second, err := suite.Run(t.Context(), scenarios)
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest != second.Digest || first.Digest == "" ||
		firstResults[0].Name() != "alpha" || secondResults[0].Name() != "alpha" ||
		!first.Passed || !second.Passed {
		t.Fatalf("first=%+v second=%+v", first, second)
	}
}

func TestScenarioSuiteRejectsInvalidBoundaries(t *testing.T) {
	t.Parallel()
	suite := newScenarioSuite(t, policycel.Compiler{})
	valid := scenarioFixture(t, "valid", policy.RequirementSatisfied, policy.DirectiveCompleteVerified,
		policy.RequirementSatisfied, policy.DirectiveCompleteVerified)
	tests := []struct {
		name      string
		scenarios []policy.Scenario
		is        error
	}{
		{name: "empty", is: policy.ErrInvalid},
		{name: "invalid name", scenarios: []policy.Scenario{{Name: "Not Valid"}}, is: policy.ErrInvalid},
		{name: "duplicate name", scenarios: []policy.Scenario{valid, valid}, is: policy.ErrConflict},
		{name: "too many", scenarios: make([]policy.Scenario, policy.MaximumScenarios+1), is: policy.ErrInvalid},
		{name: "invalid expectation", scenarios: []policy.Scenario{changedScenario(valid, func(value *policy.Scenario) {
			value.Expectation.Results = nil
		})}, is: policy.ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := suite.Run(t.Context(), test.scenarios); !errors.Is(err, test.is) {
				t.Fatalf("Run() error = %v, want %v", err, test.is)
			}
		})
	}
}

func TestScenarioSuitePropagatesCancellationAndStops(t *testing.T) {
	t.Parallel()
	compiler := &cancelingSimulationCompiler{delegate: policycel.Compiler{}}
	suite := newScenarioSuite(t, compiler)
	ctx, cancel := context.WithCancel(t.Context())
	compiler.cancel = cancel
	scenarios := []policy.Scenario{
		scenarioFixture(t, "alpha", policy.RequirementSatisfied, policy.DirectiveCompleteVerified,
			policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
		scenarioFixture(t, "bravo", policy.RequirementSatisfied, policy.DirectiveCompleteVerified,
			policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
		scenarioFixture(t, "charlie", policy.RequirementSatisfied, policy.DirectiveCompleteVerified,
			policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
	}
	if _, _, err := suite.Run(ctx, scenarios); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
	if compiler.Calls() != 2 {
		t.Fatalf("compiler calls = %d, want 2", compiler.Calls())
	}
}

func TestScenarioSuiteOwnsReturnedStateAndOmitsSensitiveMeaning(t *testing.T) {
	t.Parallel()
	suite := newScenarioSuite(t, policycel.Compiler{})
	results, report, err := suite.Run(t.Context(), []policy.Scenario{
		scenarioFixture(t, "verified", policy.RequirementSatisfied, policy.DirectiveCompleteVerified,
			policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical := results[0].Bundle().Canonical()
	canonical[0] = ' '
	report.Cases[0].Name = "mutated"
	if results[0].Bundle().Canonical()[0] == ' ' || results[0].Name() != "verified" {
		t.Fatal("result exposed mutable bundle state")
	}
	_, fresh, err := suite.Run(t.Context(), []policy.Scenario{
		scenarioFixture(t, "verified", policy.RequirementSatisfied, policy.DirectiveCompleteVerified,
			policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(fresh)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		`"facts"`, `"reason_codes"`, `"source"`, `"expression"`,
		"document.authenticity", "synthetic_scenario", "processing_authority",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("suite report contains %q: %s", forbidden, encoded)
		}
	}
}

func TestScenarioSuiteSupportsConcurrentReuse(t *testing.T) {
	t.Parallel()
	suite := newScenarioSuite(t, policycel.Compiler{})
	scenarios := []policy.Scenario{
		scenarioFixture(t, "verified", policy.RequirementSatisfied, policy.DirectiveCompleteVerified,
			policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
	}
	const workers = 24
	digests := make(chan string, workers)
	errorsSeen := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, report, err := suite.Run(t.Context(), scenarios)
			if err != nil {
				errorsSeen <- err
				return
			}
			digests <- report.Digest
		}()
	}
	wait.Wait()
	close(digests)
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	expected := ""
	for digest := range digests {
		if expected == "" {
			expected = digest
		}
		if digest != expected {
			t.Fatal("concurrent suite reuse changed report meaning")
		}
	}
}

func TestNewScenarioSuiteRequiresSimulator(t *testing.T) {
	t.Parallel()
	if _, err := policy.NewScenarioSuite(nil); err == nil {
		t.Fatal("NewScenarioSuite(nil) succeeded")
	}
	var suite *policy.ScenarioSuite
	if _, _, err := suite.Run(t.Context(), []policy.Scenario{{}}); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("nil Run() error = %v", err)
	}
}

func FuzzScenarioSuiteOrder(f *testing.F) {
	f.Add(true)
	f.Add(false)
	f.Fuzz(func(t *testing.T, reverse bool) {
		suite := newScenarioSuite(t, policycel.Compiler{})
		scenarios := []policy.Scenario{
			scenarioFixture(t, "alpha", policy.RequirementSatisfied, policy.DirectiveCompleteVerified,
				policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
			scenarioFixture(t, "bravo", policy.RequirementUnavailable, policy.DirectiveRequestInput,
				policy.RequirementUnavailable, policy.DirectiveRequestInput),
		}
		_, expected, err := suite.Run(t.Context(), scenarios)
		if err != nil {
			t.Fatal(err)
		}
		if reverse {
			slices.Reverse(scenarios)
		}
		_, actual, err := suite.Run(t.Context(), scenarios)
		if err != nil {
			t.Fatal(err)
		}
		if actual.Digest != expected.Digest {
			t.Fatal("scenario order changed suite digest")
		}
	})
}

func newScenarioSuite(t testing.TB, compiler policy.SimulationCompiler) *policy.ScenarioSuite {
	t.Helper()
	simulator, err := policy.NewSimulator(compiler)
	if err != nil {
		t.Fatal(err)
	}
	suite, err := policy.NewScenarioSuite(simulator)
	if err != nil {
		t.Fatal(err)
	}
	return suite
}

func scenarioFixture(
	t testing.TB,
	name string,
	actualState policy.RequirementState,
	actualDirective policy.Directive,
	expectedState policy.RequirementState,
	expectedDirective policy.Directive,
) policy.Scenario {
	t.Helper()
	input := simulationFixture(t, actualState, actualDirective)
	documentFact, _ := policy.NewFactKey("document.authenticity")
	selfieFact, _ := policy.NewFactKey("selfie.liveness")
	assurance := ""
	if expectedDirective == policy.DirectiveCompleteVerified {
		assurance = "global_individual_substantial.1"
	}
	return policy.Scenario{
		Name:  name,
		Input: input,
		Expectation: policy.ScenarioExpectation{
			Results: []policy.RequirementResult{{
				Name: "scenario", State: expectedState,
				ContributingFacts: []policy.FactKey{documentFact, selfieFact},
				Candidate:         expectedDirective, Priority: 1,
				ReasonCodes: []string{"synthetic_scenario"},
			}},
			Assurance: assurance,
		},
	}
}

func changedScenario(value policy.Scenario, change func(*policy.Scenario)) policy.Scenario {
	value.Input.CanonicalPolicy = bytes.Clone(value.Input.CanonicalPolicy)
	value.Input.Facts = slices.Clone(value.Input.Facts)
	value.Expectation.Results = slices.Clone(value.Expectation.Results)
	change(&value)
	return value
}

type cancelingSimulationCompiler struct {
	delegate policy.SimulationCompiler
	cancel   context.CancelFunc
	mu       sync.Mutex
	calls    int
}

func (compiler *cancelingSimulationCompiler) CompileSimulation(
	ctx context.Context,
	canonical []byte,
) (policy.SimulationProgram, error) {
	compiler.mu.Lock()
	compiler.calls++
	calls := compiler.calls
	compiler.mu.Unlock()
	if calls == 2 {
		compiler.cancel()
	}
	return compiler.delegate.CompileSimulation(ctx, canonical)
}

func (compiler *cancelingSimulationCompiler) Calls() int {
	compiler.mu.Lock()
	defer compiler.mu.Unlock()
	return compiler.calls
}
