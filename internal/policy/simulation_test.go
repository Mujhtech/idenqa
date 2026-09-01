package policy_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
)

func TestSimulatorProducesTerminalAndNonterminalMeaning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		state      policy.RequirementState
		directive  policy.Directive
		outcome    policy.Outcome
		isTerminal bool
	}{
		{name: "verified", state: policy.RequirementSatisfied, directive: policy.DirectiveCompleteVerified, outcome: policy.OutcomeVerified, isTerminal: true},
		{name: "not verified", state: policy.RequirementNotSatisfied, directive: policy.DirectiveCompleteNotVerified, outcome: policy.OutcomeNotVerified, isTerminal: true},
		{name: "inconclusive", state: policy.RequirementInconclusive, directive: policy.DirectiveCompleteInconclusive, outcome: policy.OutcomeInconclusive, isTerminal: true},
		{name: "request input", state: policy.RequirementUnavailable, directive: policy.DirectiveRequestInput},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := simulationFixture(t, test.state, test.directive)
			simulator, err := policy.NewSimulator(policycel.Compiler{})
			if err != nil {
				t.Fatal(err)
			}
			simulation, err := simulator.Run(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			evaluation := simulation.Evaluation()
			if evaluation.Selected() != test.directive || evaluation.Outcome() != test.outcome ||
				evaluation.AuthorisesCompletion() != test.isTerminal {
				t.Fatalf("evaluation = %q %q terminal=%t", evaluation.Selected(), evaluation.Outcome(), evaluation.AuthorisesCompletion())
			}
		})
	}
}

func TestSimulatorIsDeterministicAndOwnsCallerInput(t *testing.T) {
	t.Parallel()
	simulator, err := policy.NewSimulator(mutatingCompiler{})
	if err != nil {
		t.Fatal(err)
	}
	input := simulationFixture(t, policy.RequirementSatisfied, policy.DirectiveCompleteVerified)
	first, err := simulator.Run(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(input.Facts)
	second, err := simulator.Run(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Snapshot().Digest() != second.Snapshot().Digest() ||
		first.Evaluation().Digest() != second.Evaluation().Digest() {
		t.Fatal("input order changed deterministic simulation meaning")
	}
	input.CanonicalPolicy[0] = ' '
	input.Facts[0].ReasonCodes[0] = "mutated"
	if first.CanonicalPolicy()[0] == ' ' || first.Snapshot().Facts()[0].ReasonCodes[0] == "mutated" {
		t.Fatal("simulation retained caller-owned mutable input")
	}
	policyCopy := first.CanonicalPolicy()
	policyCopy[0] = ' '
	factsCopy := first.Snapshot().Facts()
	factsCopy[0].ReasonCodes[0] = "changed"
	if first.CanonicalPolicy()[0] == ' ' || first.Snapshot().Facts()[0].ReasonCodes[0] == "changed" {
		t.Fatal("simulation accessors exposed mutable state")
	}
}

func TestSimulatorRejectsInvalidAndMismatchedMeaning(t *testing.T) {
	t.Parallel()
	base := simulationFixture(t, policy.RequirementSatisfied, policy.DirectiveCompleteVerified)
	evaluator, err := policycel.ParseCanonical(base.CanonicalPolicy)
	if err != nil {
		t.Fatal(err)
	}
	compileFailure := errors.New("compiler unavailable")
	tests := []struct {
		name     string
		input    policy.SimulationInput
		compiler policy.SimulationCompiler
		is       error
	}{
		{name: "non canonical policy", input: changedSimulationInput(base, func(input *policy.SimulationInput) {
			input.CanonicalPolicy = append([]byte{' '}, input.CanonicalPolicy...)
		}), compiler: policycel.Compiler{}, is: policy.ErrInvalid},
		{name: "zero tenant", input: changedSimulationInput(base, func(input *policy.SimulationInput) { input.TenantID = id.Tenant{} }), compiler: policycel.Compiler{}, is: policy.ErrInvalid},
		{name: "local evaluation time", input: changedSimulationInput(base, func(input *policy.SimulationInput) {
			input.EvaluatedAt = time.Date(2026, 8, 31, 13, 0, 0, 0, time.FixedZone("local", 3600))
		}), compiler: policycel.Compiler{}, is: policy.ErrInvalid},
		{name: "no facts", input: changedSimulationInput(base, func(input *policy.SimulationInput) { input.Facts = nil }), compiler: policycel.Compiler{}, is: policy.ErrInvalid},
		{name: "compiler failure", input: base, compiler: compilerStub{err: compileFailure}, is: compileFailure},
		{name: "nil program", input: base, compiler: compilerStub{}, is: policy.ErrReproduction},
		{name: "policy digest mismatch", input: base, compiler: compilerStub{program: programStub{Evaluator: evaluator, digest: strings.Repeat("f", 64)}}, is: policy.ErrReproduction},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			simulator, err := policy.NewSimulator(test.compiler)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := simulator.Run(t.Context(), test.input); !errors.Is(err, test.is) {
				t.Fatalf("Run() error = %v, want %v", err, test.is)
			}
		})
	}
}

func TestSimulatorPropagatesCancellationWithoutCompilation(t *testing.T) {
	t.Parallel()
	compiler := &countingCompiler{}
	simulator, err := policy.NewSimulator(compiler)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := simulator.Run(ctx, simulationFixture(t, policy.RequirementSatisfied, policy.DirectiveCompleteVerified)); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() error = %v", err)
	}
	if compiler.count != 0 {
		t.Fatal("cancelled simulation reached compiler")
	}
}

func TestSimulatorSupportsConcurrentDeterministicReuse(t *testing.T) {
	t.Parallel()
	simulator, err := policy.NewSimulator(policycel.Compiler{})
	if err != nil {
		t.Fatal(err)
	}
	input := simulationFixture(t, policy.RequirementSatisfied, policy.DirectiveCompleteVerified)
	const workers = 32
	digests := make(chan string, workers)
	errorsSeen := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			simulation, runErr := simulator.Run(t.Context(), input)
			if runErr != nil {
				errorsSeen <- runErr
				return
			}
			digests <- simulation.Evaluation().Digest()
		}()
	}
	wait.Wait()
	close(digests)
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	var expected string
	for digest := range digests {
		if expected == "" {
			expected = digest
		}
		if digest != expected {
			t.Fatal("concurrent simulations produced different meaning")
		}
	}
}

func TestNewSimulatorRequiresCompiler(t *testing.T) {
	t.Parallel()
	if _, err := policy.NewSimulator(nil); err == nil {
		t.Fatal("NewSimulator(nil) succeeded")
	}
	var simulator *policy.Simulator
	if _, err := simulator.Run(t.Context(), policy.SimulationInput{}); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("nil Run() error = %v", err)
	}
}

type compilerStub struct {
	program policy.SimulationProgram
	err     error
}

func (compiler compilerStub) CompileSimulation(context.Context, []byte) (policy.SimulationProgram, error) {
	return compiler.program, compiler.err
}

type countingCompiler struct{ count int }

func (compiler *countingCompiler) CompileSimulation(context.Context, []byte) (policy.SimulationProgram, error) {
	compiler.count++
	return nil, nil
}

type mutatingCompiler struct{}

func (mutatingCompiler) CompileSimulation(
	ctx context.Context,
	canonical []byte,
) (policy.SimulationProgram, error) {
	original := slices.Clone(canonical)
	canonical[0] = ' '
	return policycel.Compiler{}.CompileSimulation(ctx, original)
}

type programStub struct {
	policy.Evaluator
	digest string
}

func (program programStub) PolicyDigest() string { return program.digest }

func simulationFixture(
	t testing.TB,
	state policy.RequirementState,
	directive policy.Directive,
) policy.SimulationInput {
	t.Helper()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_01K3P4NQF00000000000000001")
	verificationID, _ := id.ParseVerification("ver_01K3P4NQF00000000000000002")
	authorityID, _ := id.ParseAuthority("aut_01K3P4NQF00000000000000003")
	acknowledgementID, _ := id.ParseAcknowledgement("ack_01K3P4NQF00000000000000004")
	documentFact, _ := policy.NewFactKey("document.authenticity")
	selfieFact, _ := policy.NewFactKey("selfie.liveness")
	document := simulationDocument(state, directive)
	canonical, err := policyv1.Canonical(document)
	if err != nil {
		t.Fatal(err)
	}
	return policy.SimulationInput{
		CanonicalPolicy: canonical, TenantID: tenantID, VerificationID: verificationID,
		AuthorityID: authorityID, AcknowledgementID: acknowledgementID,
		Region: "tenant_home", EvaluatedAt: now,
		Facts: []policy.Fact{
			{Key: documentFact, State: state, Source: policy.FactSource{Kind: policy.FactSourceProcessingAuthority, Authority: &policy.AuthoritySource{AuthorityID: authorityID}}, ObservedAt: now, ReasonCodes: []string{"synthetic_document"}},
			{Key: selfieFact, State: state, Source: policy.FactSource{Kind: policy.FactSourceSubjectResponse, SubjectResponse: &policy.SubjectResponseSource{AcknowledgementID: acknowledgementID}}, ObservedAt: now, ReasonCodes: []string{"synthetic_selfie"}},
		},
	}
}

func simulationDocument(
	state policy.RequirementState,
	directive policy.Directive,
) policyv1.Document {
	assurance := ""
	if directive == policy.DirectiveCompleteVerified {
		assurance = "global_individual_substantial.1"
	}
	return policyv1.Document{
		SchemaMajor: 1, SchemaMinor: 0,
		PolicyID: "pol_01K3P4NQF00000000000000001", Revision: 3,
		VerifiedAssurance: assurance,
		Rules: []policyv1.Rule{{
			Name: "scenario", When: `facts["document.authenticity"] == "` + string(state) + `" && facts["selfie.liveness"] == "` + string(state) + `"`,
			Result: policyv1.Result{
				State: policyv1.RequirementState(state), Directive: policyv1.Directive(directive),
				Priority: 1, ContributingFacts: []string{"document.authenticity", "selfie.liveness"},
				ReasonCodes: []string{"synthetic_scenario"},
			},
		}},
	}
}

func changedSimulationInput(
	input policy.SimulationInput,
	change func(*policy.SimulationInput),
) policy.SimulationInput {
	input.CanonicalPolicy = slices.Clone(input.CanonicalPolicy)
	input.Facts = slices.Clone(input.Facts)
	change(&input)
	return input
}
