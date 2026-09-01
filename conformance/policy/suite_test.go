package policy_test

import (
	"context"
	"errors"
	"testing"

	policytest "github.com/Mujhtech/idenqa/conformance/policy"
	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
)

type engine struct{ wrong bool }

func (value engine) Evaluate(ctx context.Context, document policyv1.Document, facts policytest.Facts) ([]policytest.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	rule := document.Rules[0]
	state := facts.Values[rule.Result.ContributingFacts[0]]
	if value.wrong {
		state = policyv1.RequirementNotSatisfied
	}
	return []policytest.Result{{Name: rule.Name, State: state, Directive: rule.Result.Directive, Priority: rule.Result.Priority, Facts: append([]string(nil), rule.Result.ContributingFacts...), Reasons: append([]string(nil), rule.Result.ReasonCodes...)}}, nil
}

func TestRunRejectsNonConformingEngine(t *testing.T) {
	document := policyv1.Document{SchemaMajor: 1, PolicyID: "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV", Revision: 1, VerifiedAssurance: "identity.basic", Rules: []policyv1.Rule{{Name: "document", When: `facts["document.match"] == "satisfied"`, Result: policyv1.Result{State: policyv1.RequirementSatisfied, Directive: policyv1.DirectiveCompleteVerified, Priority: 1, ContributingFacts: []string{"document.match"}, ReasonCodes: []string{"document.match"}}}}}
	want := []policytest.Result{{Name: "document", State: policyv1.RequirementSatisfied, Directive: policyv1.DirectiveCompleteVerified, Priority: 1, Facts: []string{"document.match"}, Reasons: []string{"document.match"}}}
	cases := []policytest.Case{{Name: "verified", Document: document, Facts: policytest.Facts{Region: "ng", Values: map[string]policyv1.RequirementState{"document.match": policyv1.RequirementSatisfied}}, Want: want}}
	if err := policytest.Run(context.Background(), engine{}, cases); err != nil {
		t.Fatal(err)
	}
	if err := policytest.Run(context.Background(), engine{wrong: true}, cases); !errors.Is(err, policytest.ErrNonConforming) {
		t.Fatalf("non-conforming engine error = %v", err)
	}
}
