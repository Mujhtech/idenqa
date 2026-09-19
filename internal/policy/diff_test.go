package policy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
)

func TestDiffRevisionsIdenticalMeaningHasNoChanges(t *testing.T) {
	t.Parallel()
	policyID := diffPolicyID(t, "pol_01K3P4NQF00000000000000001")
	rule := diffRule("alpha", `facts["alpha.fact"] == "satisfied"`, []string{"alpha.fact"}, "satisfied", "route_manual_review")
	from := diffRevision(t, diffDocument(policyID, 1, "synthetic.fixture", rule))
	to := diffRevision(t, diffDocument(policyID, 2, "synthetic.fixture", rule))
	diff, err := policy.DiffRevisions(from, to)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Identical || diff.Truncated || diff.ChangeCount != 0 || len(diff.Changes) != 0 || diff.Changes == nil {
		t.Fatalf("diff = %+v", diff)
	}
	if diff.PolicyID != policyID.String() || diff.From.Revision != 1 || diff.To.Revision != 2 ||
		diff.From.Digest != from.Reference().Digest || diff.To.Digest != to.Reference().Digest ||
		diff.Digest == "" {
		t.Fatalf("identity = %+v", diff)
	}
	encoded, err := json.Marshal(diff)
	if err != nil || !bytes.Contains(encoded, []byte(`"changes":[]`)) {
		t.Fatalf("encoded identical diff = %s (%v)", encoded, err)
	}
}

func TestDiffRevisionsReportsOrderedCanonicalChanges(t *testing.T) {
	t.Parallel()
	policyID := diffPolicyID(t, "pol_01K3P4NQF00000000000000001")
	from := diffRevision(t, diffDocument(policyID, 1, "synthetic.first",
		diffRule("alpha", `facts["alpha.fact"] == "satisfied"`, []string{"alpha.fact"}, "satisfied", "route_manual_review"),
		diffRule("charlie", "true", []string{"charlie.fact"}, "unavailable", "request_input"),
		diffRule("delta", "true", []string{"delta.fact"}, "unavailable", "request_input"),
	))
	to := diffRevision(t, diffDocument(policyID, 2, "synthetic.second",
		diffRule("alpha", `facts["alpha.fact"] == "satisfied"`, []string{"alpha.fact"}, "unavailable", "request_input"),
		diffRule("bravo", "true", []string{"bravo.fact"}, "satisfied", "route_manual_review"),
	))
	diff, err := policy.DiffRevisions(from, to)
	if err != nil {
		t.Fatal(err)
	}
	if diff.Identical || diff.Truncated || diff.ChangeCount != len(diff.Changes) {
		t.Fatalf("diff = %+v", diff)
	}
	expected := []struct {
		path string
		kind policy.RevisionDiffKind
		old  string
		new  string
	}{
		{path: "/rules/0/result/directive", kind: policy.RevisionDiffChanged, old: `"route_manual_review"`, new: `"request_input"`},
		{path: "/rules/0/result/state", kind: policy.RevisionDiffChanged, old: `"satisfied"`, new: `"unavailable"`},
		{path: "/rules/1/name", kind: policy.RevisionDiffChanged, old: `"charlie"`, new: `"bravo"`},
		{path: "/rules/1/result/contributing_facts/0", kind: policy.RevisionDiffChanged, old: `"charlie.fact"`, new: `"bravo.fact"`},
		{path: "/rules/1/result/directive", kind: policy.RevisionDiffChanged, old: `"request_input"`, new: `"route_manual_review"`},
		{path: "/rules/1/result/state", kind: policy.RevisionDiffChanged, old: `"unavailable"`, new: `"satisfied"`},
		{path: "/rules/2", kind: policy.RevisionDiffRemoved},
		{path: "/verified_assurance", kind: policy.RevisionDiffChanged, old: `"synthetic.first"`, new: `"synthetic.second"`},
	}
	if len(diff.Changes) != len(expected) {
		t.Fatalf("changes = %+v", diff.Changes)
	}
	for index, want := range expected {
		change := diff.Changes[index]
		if change.Path != want.path || change.Kind != want.kind {
			t.Fatalf("change %d = %+v, want %s %s", index, change, want.path, want.kind)
		}
		if want.old != "" && string(change.Old) != want.old {
			t.Fatalf("change %d old = %s, want %s", index, change.Old, want.old)
		}
		if want.new != "" && string(change.New) != want.new {
			t.Fatalf("change %d new = %s, want %s", index, change.New, want.new)
		}
	}
	removed := diff.Changes[6]
	if len(removed.Old) == 0 || len(removed.New) != 0 {
		t.Fatalf("removed fragments = %s %s", removed.Old, removed.New)
	}
}

func TestDiffRevisionsIsDeterministicAndOwnsOutput(t *testing.T) {
	t.Parallel()
	policyID := diffPolicyID(t, "pol_01K3P4NQF00000000000000001")
	from := diffRevision(t, diffDocument(policyID, 1, "synthetic.first",
		diffRule("alpha", `facts["alpha.fact"] == "satisfied"`, []string{"alpha.fact"}, "satisfied", "route_manual_review")))
	to := diffRevision(t, diffDocument(policyID, 2, "synthetic.second",
		diffRule("alpha", `facts["alpha.fact"] == "satisfied"`, []string{"alpha.fact"}, "unavailable", "request_input")))
	first, err := policy.DiffRevisions(from, to)
	if err != nil {
		t.Fatal(err)
	}
	second, err := policy.DiffRevisions(from, to)
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, _ := json.Marshal(first)
	secondJSON, _ := json.Marshal(second)
	if !bytes.Equal(firstJSON, secondJSON) || first.Digest != second.Digest {
		t.Fatalf("nondeterministic diff = %s / %s", firstJSON, secondJSON)
	}
	first.Changes[0].Path = "/mutated"
	if second.Changes[0].Path == "/mutated" {
		t.Fatal("diff shared mutable change state")
	}
}

func TestDiffRevisionsBoundsChangeList(t *testing.T) {
	t.Parallel()
	policyID := diffPolicyID(t, "pol_01K3P4NQF00000000000000001")
	from := diffRevision(t, diffDocument(policyID, 1, "",
		diffWideRule("alpha", "from"), diffWideRule("bravo", "from")))
	to := diffRevision(t, diffDocument(policyID, 2, "synthetic.fixture",
		diffWideRule("alpha", "to"), diffWideRule("bravo", "to")))
	diff, err := policy.DiffRevisions(from, to)
	if err != nil {
		t.Fatal(err)
	}
	if !diff.Truncated || diff.ChangeCount <= policy.MaximumRevisionDiffChanges ||
		len(diff.Changes) != policy.MaximumRevisionDiffChanges {
		t.Fatalf("truncated=%t count=%d changes=%d", diff.Truncated, diff.ChangeCount, len(diff.Changes))
	}
	if diff.Changes == nil {
		t.Fatal("truncated diff exposed nil changes")
	}
	encoded, err := json.Marshal(diff)
	if err != nil || len(encoded) > policy.MaximumRevisionDiffBytes {
		t.Fatalf("encoded diff bytes = %d (%v)", len(encoded), err)
	}
}

func TestDiffRevisionsRejectsMismatchedIdentity(t *testing.T) {
	t.Parallel()
	first := diffRevision(t, diffDocument(diffPolicyID(t, "pol_01K3P4NQF00000000000000001"), 1, "",
		diffRule("alpha", `facts["alpha.fact"] == "satisfied"`, []string{"alpha.fact"}, "satisfied", "route_manual_review")))
	second := diffRevision(t, diffDocument(diffPolicyID(t, "pol_01K3P4NQF00000000000000002"), 1, "",
		diffRule("alpha", `facts["alpha.fact"] == "satisfied"`, []string{"alpha.fact"}, "satisfied", "route_manual_review")))
	if _, err := policy.DiffRevisions(first, second); !errors.Is(err, policy.ErrConflict) {
		t.Fatalf("mismatched policy error = %v", err)
	}
	if _, err := policy.DiffRevisions(policy.Revision{}, first); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("zero revision error = %v", err)
	}
}

func diffPolicyID(t testing.TB, value string) id.Policy {
	t.Helper()
	identifier, err := id.ParsePolicy(value)
	if err != nil {
		t.Fatal(err)
	}
	return identifier
}

func diffDocument(
	policyID id.Policy,
	revision uint32,
	assurance string,
	rules ...policyv1.Rule,
) []byte {
	canonical, err := policyv1.Canonical(policyv1.Document{
		SchemaMajor: 1, SchemaMinor: 0, PolicyID: policyID.String(), Revision: revision,
		VerifiedAssurance: assurance, Rules: rules,
	})
	if err != nil {
		panic(err)
	}
	return canonical
}

func diffRule(
	name, when string,
	facts []string,
	state policyv1.RequirementState,
	directive policyv1.Directive,
) policyv1.Rule {
	return policyv1.Rule{
		Name: name, When: when,
		Result: policyv1.Result{
			State: state, Directive: directive,
			Priority: 1, ContributingFacts: facts,
		},
	}
}

func diffWideRule(name, prefix string) policyv1.Rule {
	facts := make([]string, 0, policyv1.MaximumFactsPerRule)
	for index := range policyv1.MaximumFactsPerRule {
		facts = append(facts, fmt.Sprintf("%s.f%03d", prefix, index))
	}
	return policyv1.Rule{
		Name: name, When: "true",
		Result: policyv1.Result{
			State: policyv1.RequirementUnavailable, Directive: policyv1.DirectiveRequestInput,
			Priority: 1, ContributingFacts: facts,
		},
	}
}

func diffRevision(t testing.TB, canonical []byte) policy.Revision {
	t.Helper()
	revision, err := policy.NewRevisionCanonical(
		canonical,
		policy.EvaluatorReference{Major: 1, Minor: 0, Digest: strings.Repeat("a", 64)},
		time.Date(2026, 9, 19, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatal(err)
	}
	return revision
}
