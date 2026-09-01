package policy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
)

// MaximumScenarios bounds one deterministic in-process scenario-suite run.
const MaximumScenarios = 256

// ScenarioExpectation describes exact owned evaluation meaning expected from
// one synthetic input. It is resolved against that input's immutable snapshot.
type ScenarioExpectation struct {
	Results   []RequirementResult
	Assurance string
}

// Scenario is one uniquely named synthetic policy example.
type Scenario struct {
	Name        string
	Input       SimulationInput
	Expectation ScenarioExpectation
}

// ScenarioResult contains one portable actual result and its expected digest.
// An expectation mismatch is data, not an execution error.
type ScenarioResult struct {
	name           string
	bundle         SimulationBundle
	report         SimulationReport
	expectedDigest string
	matches        bool
}

// ScenarioCaseReport is a safe summary that excludes facts, provenance,
// expressions, canonical policy bytes, requirement results, and reason codes.
type ScenarioCaseReport struct {
	Name                     string           `json:"name"`
	Actual                   SimulationReport `json:"actual"`
	ExpectedEvaluationDigest string           `json:"expected_evaluation_digest"`
	Matches                  bool             `json:"matches"`
}

// ScenarioSuiteReport is a canonical, input-order-independent safe summary.
type ScenarioSuiteReport struct {
	SchemaMajor uint16               `json:"schema_major"`
	SchemaMinor uint16               `json:"schema_minor"`
	Cases       []ScenarioCaseReport `json:"cases"`
	Passed      bool                 `json:"passed"`
	Digest      string               `json:"digest"`
}

// ScenarioSuite runs bounded named examples through the owned Simulator.
type ScenarioSuite struct{ simulator *Simulator }

// NewScenarioSuite constructs an internal deterministic scenario runner.
func NewScenarioSuite(simulator *Simulator) (*ScenarioSuite, error) {
	if simulator == nil || simulator.compiler == nil {
		return nil, errors.New("policy scenario suite: simulator is required")
	}
	return &ScenarioSuite{simulator: simulator}, nil
}

// Run evaluates every scenario in canonical name order. Execution failures
// fail the run; expectation mismatches remain available together in the report.
func (suite *ScenarioSuite) Run(
	ctx context.Context,
	scenarios []Scenario,
) ([]ScenarioResult, ScenarioSuiteReport, error) {
	if suite == nil || suite.simulator == nil || suite.simulator.compiler == nil ||
		len(scenarios) == 0 || len(scenarios) > MaximumScenarios {
		return nil, ScenarioSuiteReport{}, fmt.Errorf("%w: scenario suite", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return nil, ScenarioSuiteReport{}, err
	}

	ordered := slices.Clone(scenarios)
	for _, scenario := range ordered {
		if !validToken(scenario.Name, 128) {
			return nil, ScenarioSuiteReport{}, fmt.Errorf("%w: scenario name", ErrInvalid)
		}
	}
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Name < ordered[right].Name })
	for index := 1; index < len(ordered); index++ {
		if ordered[index-1].Name == ordered[index].Name {
			return nil, ScenarioSuiteReport{}, fmt.Errorf("%w: duplicate scenario name", ErrConflict)
		}
	}

	results := make([]ScenarioResult, 0, len(ordered))
	reports := make([]ScenarioCaseReport, 0, len(ordered))
	passed := true
	for _, scenario := range ordered {
		if err := ctx.Err(); err != nil {
			return nil, ScenarioSuiteReport{}, err
		}
		simulation, err := suite.simulator.Run(ctx, scenario.Input)
		if err != nil {
			return nil, ScenarioSuiteReport{}, fmt.Errorf("run scenario %q: %w", scenario.Name, err)
		}
		expected, err := Resolve(
			simulation.Snapshot(),
			cloneResults(scenario.Expectation.Results),
			scenario.Expectation.Assurance,
		)
		if err != nil {
			return nil, ScenarioSuiteReport{}, fmt.Errorf("resolve scenario %q expectation: %w", scenario.Name, err)
		}
		bundle, report, err := NewSimulationBundle(simulation)
		if err != nil {
			return nil, ScenarioSuiteReport{}, fmt.Errorf("bundle scenario %q: %w", scenario.Name, err)
		}
		matches := simulation.Evaluation().Digest() == expected.Digest()
		passed = passed && matches
		results = append(results, ScenarioResult{
			name: scenario.Name, bundle: bundle, report: report,
			expectedDigest: expected.Digest(), matches: matches,
		})
		reports = append(reports, ScenarioCaseReport{
			Name: scenario.Name, Actual: report,
			ExpectedEvaluationDigest: expected.Digest(), Matches: matches,
		})
	}
	report, err := newScenarioSuiteReport(reports, passed)
	if err != nil {
		return nil, ScenarioSuiteReport{}, err
	}
	return cloneScenarioResults(results), cloneScenarioSuiteReport(report), nil
}

// Name returns the validated unique scenario name.
func (result ScenarioResult) Name() string { return result.name }

// Bundle returns the self-checking portable actual simulation bundle.
func (result ScenarioResult) Bundle() SimulationBundle {
	return SimulationBundle{
		simulation: cloneSimulation(result.bundle.simulation),
		canonical:  slices.Clone(result.bundle.canonical), digest: result.bundle.digest,
	}
}

// Report returns the safe actual simulation summary.
func (result ScenarioResult) Report() SimulationReport { return result.report }

// ExpectedEvaluationDigest returns the exact expected evaluation meaning.
func (result ScenarioResult) ExpectedEvaluationDigest() string { return result.expectedDigest }

// Matches reports whether actual and expected canonical evaluations agree.
func (result ScenarioResult) Matches() bool { return result.matches }

func newScenarioSuiteReport(
	cases []ScenarioCaseReport,
	passed bool,
) (ScenarioSuiteReport, error) {
	payload := struct {
		SchemaMajor uint16               `json:"schema_major"`
		SchemaMinor uint16               `json:"schema_minor"`
		Cases       []ScenarioCaseReport `json:"cases"`
		Passed      bool                 `json:"passed"`
	}{
		SchemaMajor: SimulationBundleSchemaMajor,
		SchemaMinor: SimulationBundleSchemaMinor,
		Cases:       slices.Clone(cases),
		Passed:      passed,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ScenarioSuiteReport{}, fmt.Errorf("encode scenario suite report: %w", err)
	}
	sum := sha256.Sum256(encoded)
	return ScenarioSuiteReport{
		SchemaMajor: payload.SchemaMajor, SchemaMinor: payload.SchemaMinor,
		Cases: slices.Clone(payload.Cases), Passed: passed, Digest: hex.EncodeToString(sum[:]),
	}, nil
}

func cloneResults(values []RequirementResult) []RequirementResult {
	cloned := make([]RequirementResult, len(values))
	for index, value := range values {
		cloned[index] = cloneResult(value)
	}
	return cloned
}

func cloneScenarioResults(values []ScenarioResult) []ScenarioResult {
	cloned := make([]ScenarioResult, len(values))
	for index, value := range values {
		cloned[index] = value
		cloned[index].bundle = value.Bundle()
	}
	return cloned
}

func cloneScenarioSuiteReport(value ScenarioSuiteReport) ScenarioSuiteReport {
	value.Cases = slices.Clone(value.Cases)
	return value
}
