package policycel_test

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

func TestEvaluatorProducesOwnedTerminalResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		state     policy.RequirementState
		directive policy.Directive
		outcome   policy.Outcome
	}{
		{name: "verified", state: policy.RequirementSatisfied, directive: policy.DirectiveCompleteVerified, outcome: policy.OutcomeVerified},
		{name: "not verified", state: policy.RequirementNotSatisfied, directive: policy.DirectiveCompleteNotVerified, outcome: policy.OutcomeNotVerified},
		{name: "inconclusive", state: policy.RequirementInconclusive, directive: policy.DirectiveCompleteInconclusive, outcome: policy.OutcomeInconclusive},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			evaluator, snapshot := evaluatorFixture(t, test.state)
			output, err := evaluator.Evaluate(t.Context(), snapshot)
			if err != nil {
				t.Fatal(err)
			}
			evaluation, err := policy.Resolve(snapshot, output.Results, output.Assurance)
			if err != nil {
				t.Fatal(err)
			}
			if evaluation.Selected() != test.directive || evaluation.Outcome() != test.outcome ||
				!evaluation.AuthorisesCompletion() {
				t.Fatalf("evaluation = %q %q", evaluation.Selected(), evaluation.Outcome())
			}
		})
	}
}

func TestEvaluatorUsesRegionAndExactStaticFactProvenance(t *testing.T) {
	t.Parallel()
	document := baseDocument()
	document.Rules[0].When = `region == "tenant_home" && facts["document.authenticity"] == "satisfied"`
	document.Rules[0].Result.ContributingFacts = []string{"document.authenticity"}
	evaluator, err := policycel.New(document)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := snapshotFor(t, evaluator, document, policy.RequirementSatisfied)
	output, err := evaluator.Evaluate(t.Context(), snapshot)
	if err != nil || len(output.Results) != 1 ||
		!slices.Equal(output.Results[0].ContributingFacts, []policy.FactKey{mustFactKey(t, "document.authenticity")}) {
		t.Fatalf("Evaluate() = %+v, %v", output, err)
	}
}

func TestNewRejectsExpressionsOutsideClosedSubset(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		when  string
		facts []string
	}{
		{name: "macro", when: `facts.exists(key, key == "document.authenticity")`, facts: []string{"document.authenticity"}},
		{name: "receiver function", when: `region.startsWith("tenant") && facts["document.authenticity"] == "satisfied"`, facts: []string{"document.authenticity"}},
		{name: "arithmetic", when: `1 + 1 == 2 && facts["document.authenticity"] == "satisfied"`, facts: []string{"document.authenticity"}},
		{name: "dynamic index", when: `facts[region] == "satisfied"`, facts: []string{"document.authenticity"}},
		{name: "unknown identifier", when: `subject == "x" && facts["document.authenticity"] == "satisfied"`, facts: []string{"document.authenticity"}},
		{name: "non boolean", when: `facts["document.authenticity"]`, facts: []string{"document.authenticity"}},
		{name: "undeclared fact", when: `facts["selfie.liveness"] == "satisfied"`, facts: []string{"document.authenticity"}},
		{name: "extra provenance", when: `facts["document.authenticity"] == "satisfied"`, facts: []string{"document.authenticity", "selfie.liveness"}},
		{name: "conditional", when: `facts["document.authenticity"] == "satisfied" ? true : false`, facts: []string{"document.authenticity"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := baseDocument()
			document.Rules = document.Rules[:1]
			document.Rules[0].When = test.when
			document.Rules[0].Result.ContributingFacts = test.facts
			if _, err := policycel.New(document); !errors.Is(err, policycel.ErrCompile) {
				t.Fatalf("New() error = %v", err)
			}
		})
	}
}

func TestParseCanonicalRejectsNonCanonicalDocument(t *testing.T) {
	t.Parallel()
	canonical, err := policyv1.Canonical(baseDocument())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policycel.ParseCanonical(append([]byte{' '}, canonical...)); !errors.Is(err, policycel.ErrCompile) {
		t.Fatalf("ParseCanonical() error = %v", err)
	}
	evaluator, err := policycel.ParseCanonical(canonical)
	if err != nil || evaluator.PolicyDigest() == "" || evaluator.Reference().Digest == "" {
		t.Fatalf("ParseCanonical() = %+v, %v", evaluator, err)
	}
}

func TestEvaluatorOwnsCompiledMeaningAndNilFailsClosed(t *testing.T) {
	t.Parallel()
	document := baseDocument()
	evaluator, err := policycel.New(document)
	if err != nil {
		t.Fatal(err)
	}
	document.PolicyID = "pol_01K3P4NQF00000000000000009"
	document.Rules[0].When = "false"
	document.Rules[0].Result.ContributingFacts[0] = "mutated.fact"
	snapshot := snapshotFor(t, evaluator, baseDocument(), policy.RequirementSatisfied)
	if _, err := evaluator.Evaluate(t.Context(), snapshot); err != nil {
		t.Fatalf("caller mutation changed evaluator: %v", err)
	}
	var nilEvaluator *policycel.Evaluator
	if nilEvaluator.Reference() != (policy.EvaluatorReference{}) || nilEvaluator.PolicyDigest() != "" {
		t.Fatal("nil evaluator exposed non-zero identity")
	}
	if _, err := nilEvaluator.Evaluate(t.Context(), snapshot); !errors.Is(err, policycel.ErrEvaluate) {
		t.Fatalf("nil Evaluate() error = %v", err)
	}
}

func TestNewEnforcesParserNodeAndDepthLimits(t *testing.T) {
	t.Parallel()
	document := baseDocument()
	document.Rules = document.Rules[:1]
	document.Rules[0].When = strings.Repeat(
		`facts["document.authenticity"] == "satisfied" && `,
		40,
	) + `facts["document.authenticity"] == "satisfied"`
	document.Rules[0].Result.ContributingFacts = []string{"document.authenticity"}
	if _, err := policycel.New(document); !errors.Is(err, policycel.ErrCompile) {
		t.Fatalf("New(over-complex) error = %v", err)
	}
}

func TestEvaluateRejectsPolicyAndEvaluatorMismatch(t *testing.T) {
	t.Parallel()
	evaluator, snapshot := evaluatorFixture(t, policy.RequirementSatisfied)
	tests := []struct {
		name      string
		reference policy.Reference
		evaluator policy.EvaluatorReference
	}{
		{name: "policy digest", reference: changedReference(snapshot.Policy(), func(reference *policy.Reference) { reference.Digest = strings.Repeat("f", 64) }), evaluator: evaluator.Reference()},
		{name: "policy revision", reference: changedReference(snapshot.Policy(), func(reference *policy.Reference) { reference.Revision++ }), evaluator: evaluator.Reference()},
		{name: "evaluator", reference: snapshot.Policy(), evaluator: policy.EvaluatorReference{Major: 1, Minor: 0, Digest: strings.Repeat("e", 64)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			input := snapshotInput(t, test.reference, test.evaluator, policy.RequirementSatisfied)
			mismatched, err := policy.NewSnapshot(input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := evaluator.Evaluate(t.Context(), mismatched); !errors.Is(err, policycel.ErrPolicyMismatch) {
				t.Fatalf("Evaluate() error = %v", err)
			}
		})
	}
}

func TestEvaluateFailsClosedOnNoMatchMissingFactAndCancellation(t *testing.T) {
	t.Parallel()
	evaluator, snapshot := evaluatorFixture(t, policy.RequirementUnavailable)
	if _, err := evaluator.Evaluate(t.Context(), snapshot); !errors.Is(err, policycel.ErrEvaluate) {
		t.Fatalf("Evaluate(no match) error = %v", err)
	}
	document := baseDocument()
	document.Rules = document.Rules[:1]
	evaluator, err := policycel.New(document)
	if err != nil {
		t.Fatal(err)
	}
	missingInput := snapshotInput(t, policy.Reference{
		ID: parsePolicy(t, document.PolicyID), Revision: document.Revision, SchemaMajor: 1, SchemaMinor: 0,
		Digest: evaluator.PolicyDigest(),
	}, evaluator.Reference(), policy.RequirementSatisfied)
	missingInput.Facts = missingInput.Facts[1:]
	missingSnapshot, err := policy.NewSnapshot(missingInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := evaluator.Evaluate(t.Context(), missingSnapshot); !errors.Is(err, policycel.ErrEvaluate) {
		t.Fatalf("Evaluate(missing fact) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := evaluator.Evaluate(ctx, missingSnapshot); !errors.Is(err, context.Canceled) {
		t.Fatalf("Evaluate(cancelled) error = %v", err)
	}
}

func TestEvaluatorIsSafeForConcurrentDeterministicUse(t *testing.T) {
	t.Parallel()
	evaluator, snapshot := evaluatorFixture(t, policy.RequirementSatisfied)
	const workers = 64
	digests := make(chan string, workers)
	errorsSeen := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			output, err := evaluator.Evaluate(t.Context(), snapshot)
			if err != nil {
				errorsSeen <- err
				return
			}
			resolved, err := policy.Resolve(snapshot, output.Results, output.Assurance)
			if err != nil {
				errorsSeen <- err
				return
			}
			digests <- resolved.Digest()
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
			t.Fatalf("digest = %q, want %q", digest, expected)
		}
	}
}

func evaluatorFixture(t testing.TB, state policy.RequirementState) (*policycel.Evaluator, policy.Snapshot) {
	t.Helper()
	document := baseDocument()
	evaluator, err := policycel.New(document)
	if err != nil {
		t.Fatal(err)
	}
	return evaluator, snapshotFor(t, evaluator, document, state)
}

func snapshotFor(t testing.TB, evaluator *policycel.Evaluator, document policyv1.Document, state policy.RequirementState) policy.Snapshot {
	t.Helper()
	reference := policy.Reference{ID: parsePolicy(t, document.PolicyID), Revision: document.Revision,
		SchemaMajor: document.SchemaMajor, SchemaMinor: document.SchemaMinor, Digest: evaluator.PolicyDigest()}
	snapshot, err := policy.NewSnapshot(snapshotInput(t, reference, evaluator.Reference(), state))
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func snapshotInput(t testing.TB, reference policy.Reference, evaluator policy.EvaluatorReference, state policy.RequirementState) policy.SnapshotInput {
	t.Helper()
	now := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_01K3P4NQF00000000000000001")
	verificationID, _ := id.ParseVerification("ver_01K3P4NQF00000000000000002")
	authorityID, _ := id.ParseAuthority("aut_01K3P4NQF00000000000000003")
	acknowledgementID, _ := id.ParseAcknowledgement("ack_01K3P4NQF00000000000000004")
	documentFact, _ := policy.NewFactKey("document.authenticity")
	selfieFact, _ := policy.NewFactKey("selfie.liveness")
	return policy.SnapshotInput{
		TenantID: tenantID, VerificationID: verificationID, AuthorityID: authorityID,
		AcknowledgementID: acknowledgementID, Region: "tenant_home", Policy: reference,
		Evaluator: evaluator, EvaluatedAt: now,
		Facts: []policy.Fact{
			{Key: documentFact, State: state, Source: policy.FactSource{Kind: policy.FactSourceProcessingAuthority, Authority: &policy.AuthoritySource{AuthorityID: authorityID}}, ObservedAt: now},
			{Key: selfieFact, State: state, Source: policy.FactSource{Kind: policy.FactSourceSubjectResponse, SubjectResponse: &policy.SubjectResponseSource{AcknowledgementID: acknowledgementID}}, ObservedAt: now},
		},
	}
}

func changedReference(reference policy.Reference, change func(*policy.Reference)) policy.Reference {
	change(&reference)
	return reference
}

func mustFactKey(t testing.TB, encoded string) policy.FactKey {
	t.Helper()
	value, err := policy.NewFactKey(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func parsePolicy(t testing.TB, encoded string) id.Policy {
	t.Helper()
	value, err := id.ParsePolicy(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func baseDocument() policyv1.Document {
	return policyv1.Document{
		SchemaMajor: 1, SchemaMinor: 0, PolicyID: "pol_01K3P4NQF00000000000000001", Revision: 3,
		VerifiedAssurance: "global_individual_substantial.1",
		Rules: []policyv1.Rule{
			{Name: "verified", When: `facts["document.authenticity"] == "satisfied" && facts["selfie.liveness"] == "satisfied"`,
				Result: policyv1.Result{State: policyv1.RequirementSatisfied, Directive: policyv1.DirectiveCompleteVerified,
					Priority: 100, ContributingFacts: []string{"document.authenticity", "selfie.liveness"}, ReasonCodes: []string{"requirements_satisfied"}}},
			{Name: "not_verified", When: `facts["document.authenticity"] == "not_satisfied" || facts["selfie.liveness"] == "not_satisfied"`,
				Result: policyv1.Result{State: policyv1.RequirementNotSatisfied, Directive: policyv1.DirectiveCompleteNotVerified,
					Priority: 10, ContributingFacts: []string{"document.authenticity", "selfie.liveness"}, ReasonCodes: []string{"requirement_failed"}}},
			{Name: "inconclusive", When: `facts["document.authenticity"] == "inconclusive" || facts["selfie.liveness"] == "inconclusive"`,
				Result: policyv1.Result{State: policyv1.RequirementInconclusive, Directive: policyv1.DirectiveCompleteInconclusive,
					Priority: 20, ContributingFacts: []string{"document.authenticity", "selfie.liveness"}, ReasonCodes: []string{"requirement_inconclusive"}}},
		},
	}
}
