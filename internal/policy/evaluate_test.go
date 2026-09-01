package policy_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
)

func TestResolveSelectsLowestPriorityIndependentlyOfInputOrder(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	snapshot, err := policy.NewSnapshot(base.input)
	if err != nil {
		t.Fatal(err)
	}
	results := verifiedResults(base)
	results[0].Priority = 20
	results[1].Priority = 10
	results[2].Priority = 30
	for _, order := range [][]policy.RequirementResult{
		results,
		{results[2], results[0], results[1]},
	} {
		evaluation, resolveErr := policy.Resolve(snapshot, order, "global_individual_substantial.1")
		if resolveErr != nil {
			t.Fatal(resolveErr)
		}
		if evaluation.Selected() != policy.DirectiveCompleteVerified || evaluation.Outcome() != policy.OutcomeVerified ||
			!evaluation.AuthorisesCompletion() {
			t.Fatalf("evaluation = %q %q", evaluation.Selected(), evaluation.Outcome())
		}
	}
	first, _ := policy.Resolve(snapshot, results, "global_individual_substantial.1")
	second, _ := policy.Resolve(snapshot, []policy.RequirementResult{results[2], results[0], results[1]}, "global_individual_substantial.1")
	if first.Digest() != second.Digest() || !slices.Equal(first.Canonical(), second.Canonical()) {
		t.Fatal("requirement input order changed evaluation meaning")
	}
}

func TestResolveReportsEqualPriorityDirectiveConflict(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	snapshot, _ := policy.NewSnapshot(base.input)
	results := verifiedResults(base)
	results[1].Candidate = policy.DirectiveRouteManualReview
	if _, err := policy.Resolve(snapshot, results, "moderate"); !errors.Is(err, policy.ErrConflict) {
		t.Fatalf("Resolve() error = %v", err)
	}
}

func TestResolveEnforcesTerminalRequirementMeaning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		state     policy.RequirementState
		directive policy.Directive
		outcome   policy.Outcome
		valid     bool
	}{
		{name: "verified", state: policy.RequirementSatisfied, directive: policy.DirectiveCompleteVerified, outcome: policy.OutcomeVerified, valid: true},
		{name: "not verified", state: policy.RequirementNotSatisfied, directive: policy.DirectiveCompleteNotVerified, outcome: policy.OutcomeNotVerified, valid: true},
		{name: "inconclusive", state: policy.RequirementInconclusive, directive: policy.DirectiveCompleteInconclusive, outcome: policy.OutcomeInconclusive, valid: true},
		{name: "unavailable is inconclusive", state: policy.RequirementUnavailable, directive: policy.DirectiveCompleteInconclusive, outcome: policy.OutcomeInconclusive, valid: true},
		{name: "prohibited fails workflow", state: policy.RequirementProhibited, directive: policy.DirectiveFailWorkflow, valid: true},
		{name: "inconclusive cannot verify", state: policy.RequirementInconclusive, directive: policy.DirectiveCompleteVerified},
		{name: "unavailable cannot verify", state: policy.RequirementUnavailable, directive: policy.DirectiveCompleteVerified},
		{name: "prohibited cannot decide", state: policy.RequirementProhibited, directive: policy.DirectiveCompleteNotVerified},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			base := policyFixture(t)
			base.input.Facts[0].State = test.state
			snapshot, err := policy.NewSnapshot(base.input)
			if err != nil {
				t.Fatal(err)
			}
			result := policy.RequirementResult{Name: "document_authenticity", State: test.state,
				ContributingFacts: []policy.FactKey{base.checkFact}, Candidate: test.directive,
				Priority: 1, ReasonCodes: []string{"evaluated"}}
			evaluation, err := policy.Resolve(snapshot, []policy.RequirementResult{result}, "moderate")
			if (err == nil) != test.valid {
				t.Fatalf("Resolve() error = %v", err)
			}
			if err == nil && evaluation.Outcome() != test.outcome {
				t.Fatalf("Outcome() = %q, want %q", evaluation.Outcome(), test.outcome)
			}
		})
	}
}

func TestResolveRejectsUnknownDuplicateAndUnboundRequirementInputs(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	snapshot, _ := policy.NewSnapshot(base.input)
	unknownFact, _ := policy.NewFactKey("check.unknown")
	tests := []struct {
		name    string
		results []policy.RequirementResult
		want    error
	}{
		{name: "unknown fact", results: []policy.RequirementResult{{Name: "document", State: policy.RequirementSatisfied,
			ContributingFacts: []policy.FactKey{unknownFact}, Candidate: policy.DirectiveCompleteVerified, Priority: 1}}, want: policy.ErrInvalid},
		{name: "duplicate fact reference", results: []policy.RequirementResult{{Name: "document", State: policy.RequirementSatisfied,
			ContributingFacts: []policy.FactKey{base.checkFact, base.checkFact}, Candidate: policy.DirectiveCompleteVerified, Priority: 1}}, want: policy.ErrInvalid},
		{name: "duplicate requirement", results: []policy.RequirementResult{
			{Name: "document", State: policy.RequirementSatisfied, ContributingFacts: []policy.FactKey{base.checkFact}, Candidate: policy.DirectiveCompleteVerified, Priority: 1},
			{Name: "document", State: policy.RequirementSatisfied, ContributingFacts: []policy.FactKey{base.authFact}, Candidate: policy.DirectiveCompleteVerified, Priority: 1},
		}, want: policy.ErrConflict},
		{name: "unknown directive", results: []policy.RequirementResult{{Name: "document", State: policy.RequirementSatisfied,
			ContributingFacts: []policy.FactKey{base.checkFact}, Candidate: "execute_remote_code", Priority: 1}}, want: policy.ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := policy.Resolve(snapshot, test.results, "moderate")
			if !errors.Is(err, test.want) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestNewDecisionReproducesExactTerminalMeaningAndSupersession(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	snapshot, _ := policy.NewSnapshot(base.input)
	evaluation, _ := policy.Resolve(snapshot, verifiedResults(base), "global_individual_substantial.1")
	previous, _ := id.ParseDecision("dec_01K3P4NQF00000000000000001")
	decision, err := policy.NewDecision(policy.DecisionInput{
		ID: base.decision, Snapshot: snapshot, Evaluation: evaluation, Actor: policy.ActorMachine,
		Supersedes: previous, DecidedAt: base.now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	reproduced, err := policy.Reproduce(decision)
	if err != nil {
		t.Fatal(err)
	}
	if reproduced.Digest() != evaluation.Digest() || decision.Supersedes().String() != previous.String() ||
		decision.Actor() != policy.ActorMachine {
		t.Fatal("reproduced decision meaning differs")
	}
	if _, err := policy.RestoreDecision(policy.DecisionInput{
		ID: base.decision, Snapshot: snapshot, Evaluation: evaluation, Actor: policy.ActorMachine,
		Supersedes: previous, DecidedAt: base.now.Add(time.Second),
	}, strings.Repeat("f", 64), evaluation.Digest()); !errors.Is(err, policy.ErrReproduction) {
		t.Fatalf("RestoreDecision(tampered) error = %v", err)
	}
}

func TestNewDecisionRejectsNonTerminalDirectiveAuthorisation(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	snapshot, _ := policy.NewSnapshot(base.input)
	results := verifiedResults(base)
	for index := range results {
		results[index].Candidate = policy.DirectiveRouteManualReview
	}
	evaluation, err := policy.Resolve(snapshot, results, "moderate")
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.AuthorisesCompletion() {
		t.Fatal("manual-review directive authorises terminal completion")
	}
	_, err = policy.NewDecision(policy.DecisionInput{
		ID: base.decision, Snapshot: snapshot, Evaluation: evaluation,
		Actor: policy.ActorMachine, DecidedAt: base.now,
	})
	if !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("NewDecision(non-terminal) error = %v", err)
	}
}

func TestEvaluationAccessorsDoNotAliasInternalState(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	snapshot, _ := policy.NewSnapshot(base.input)
	evaluation, _ := policy.Resolve(snapshot, verifiedResults(base), "moderate")
	results, reasons, canonical := evaluation.Results(), evaluation.ReasonCodes(), evaluation.Canonical()
	results[0].ReasonCodes[0], reasons[0], canonical[0] = "mutated", "mutated", 'X'
	if evaluation.Results()[0].ReasonCodes[0] == "mutated" || evaluation.ReasonCodes()[0] == "mutated" ||
		evaluation.Canonical()[0] == canonical[0] {
		t.Fatal("evaluation accessors alias internal state")
	}
}

func FuzzResolveOrderStable(f *testing.F) {
	f.Add(uint8(0))
	f.Add(uint8(1))
	f.Fuzz(func(t *testing.T, order uint8) {
		base := policyFixture(t)
		snapshot, err := policy.NewSnapshot(base.input)
		if err != nil {
			t.Fatal(err)
		}
		results := verifiedResults(base)
		if order%2 == 1 {
			slices.Reverse(results)
		}
		evaluation, err := policy.Resolve(snapshot, results, "moderate")
		if err != nil {
			t.Fatal(err)
		}
		baseline, err := policy.Resolve(snapshot, verifiedResults(base), "moderate")
		if err != nil || evaluation.Digest() != baseline.Digest() {
			t.Fatalf("order changed digest: %v", err)
		}
	})
}

func verifiedResults(base fixture) []policy.RequirementResult {
	return []policy.RequirementResult{
		{Name: "document_authenticity", State: policy.RequirementSatisfied,
			ContributingFacts: []policy.FactKey{base.checkFact}, Candidate: policy.DirectiveCompleteVerified,
			Priority: 10, ReasonCodes: []string{"document_authenticity_satisfied"}},
		{Name: "processing_authority", State: policy.RequirementSatisfied,
			ContributingFacts: []policy.FactKey{base.authFact}, Candidate: policy.DirectiveCompleteVerified,
			Priority: 10, ReasonCodes: []string{"processing_permitted"}},
		{Name: "subject_response", State: policy.RequirementSatisfied,
			ContributingFacts: []policy.FactKey{base.response}, Candidate: policy.DirectiveCompleteVerified,
			Priority: 10, ReasonCodes: []string{"subject_acknowledged"}},
	}
}
