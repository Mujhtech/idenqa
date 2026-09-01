package policyv1_test

import (
	"bytes"
	"errors"
	"slices"
	"strings"
	"testing"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
)

func TestCanonicalNormalizesUnorderedDocumentWithoutMutatingCaller(t *testing.T) {
	t.Parallel()
	document := validDocument()
	document.Rules = []policyv1.Rule{document.Rules[1], document.Rules[0]}
	document.Rules[0].Result.ContributingFacts = []string{"selfie.liveness", "document.authenticity"}
	originalRules := slices.Clone(document.Rules)
	canonical, err := policyv1.Canonical(document)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := policyv1.ParseCanonical(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Rules[0].Name != "document_failed" ||
		!slices.Equal(parsed.Rules[1].Result.ContributingFacts, []string{"document.authenticity", "selfie.liveness"}) {
		t.Fatalf("normalized document = %+v", parsed)
	}
	if document.Rules[0].Name != originalRules[0].Name ||
		!slices.Equal(document.Rules[0].Result.ContributingFacts, originalRules[0].Result.ContributingFacts) {
		t.Fatal("Canonical mutated caller-owned slices")
	}
	first, _ := policyv1.Digest(document)
	second, _ := policyv1.Digest(parsed)
	if first != second || len(first) != 64 {
		t.Fatalf("digests = %q %q", first, second)
	}
}

func TestParseRejectsMalformedClosedAndNonCanonicalDocuments(t *testing.T) {
	t.Parallel()
	canonical, err := policyv1.Canonical(validDocument())
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		input []byte
		parse func([]byte) (policyv1.Document, error)
		want  error
	}{
		{name: "empty", input: nil, parse: policyv1.Parse, want: policyv1.ErrInvalid},
		{name: "oversized", input: bytes.Repeat([]byte{'x'}, policyv1.MaximumDocumentBytes+1), parse: policyv1.Parse, want: policyv1.ErrInvalid},
		{name: "unknown field", input: bytes.Replace(canonical, []byte(`"rules":`), []byte(`"unknown":true,"rules":`), 1), parse: policyv1.Parse, want: policyv1.ErrInvalid},
		{name: "duplicate field", input: bytes.Replace(canonical, []byte(`"schema_minor":0`), []byte(`"schema_minor":0,"schema_minor":0`), 1), parse: policyv1.Parse, want: policyv1.ErrInvalid},
		{name: "trailing", input: append(slices.Clone(canonical), []byte(` {}`)...), parse: policyv1.Parse, want: policyv1.ErrInvalid},
		{name: "non canonical", input: append([]byte{' '}, canonical...), parse: policyv1.ParseCanonical, want: policyv1.ErrNonCanonical},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, parseErr := test.parse(test.input)
			if !errors.Is(parseErr, test.want) {
				t.Fatalf("Parse() error = %v, want %v", parseErr, test.want)
			}
		})
	}
}

func TestValidateRejectsInvalidDocumentMeaning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*policyv1.Document)
		want   error
	}{
		{name: "version", change: func(document *policyv1.Document) { document.SchemaMinor++ }, want: policyv1.ErrVersion},
		{name: "policy id", change: func(document *policyv1.Document) { document.PolicyID = "policy" }, want: policyv1.ErrInvalid},
		{name: "revision", change: func(document *policyv1.Document) { document.Revision = 0 }, want: policyv1.ErrInvalid},
		{name: "no rules", change: func(document *policyv1.Document) { document.Rules = nil }, want: policyv1.ErrInvalid},
		{name: "duplicate rule", change: func(document *policyv1.Document) { document.Rules[1].Name = document.Rules[0].Name }, want: policyv1.ErrInvalid},
		{name: "expression", change: func(document *policyv1.Document) { document.Rules[0].When = "" }, want: policyv1.ErrInvalid},
		{name: "expression bytes", change: func(document *policyv1.Document) {
			document.Rules[0].When = strings.Repeat("x", policyv1.MaximumExpressionBytes+1)
		}, want: policyv1.ErrInvalid},
		{name: "state", change: func(document *policyv1.Document) { document.Rules[0].Result.State = "passed" }, want: policyv1.ErrInvalid},
		{name: "directive", change: func(document *policyv1.Document) { document.Rules[0].Result.Directive = "execute" }, want: policyv1.ErrInvalid},
		{name: "terminal meaning", change: func(document *policyv1.Document) { document.Rules[0].Result.State = policyv1.RequirementInconclusive }, want: policyv1.ErrInvalid},
		{name: "prohibited meaning", change: func(document *policyv1.Document) { document.Rules[0].Result.State = policyv1.RequirementProhibited }, want: policyv1.ErrInvalid},
		{name: "priority", change: func(document *policyv1.Document) { document.Rules[0].Result.Priority = 0 }, want: policyv1.ErrInvalid},
		{name: "facts", change: func(document *policyv1.Document) { document.Rules[0].Result.ContributingFacts = nil }, want: policyv1.ErrInvalid},
		{name: "duplicate fact", change: func(document *policyv1.Document) {
			document.Rules[0].Result.ContributingFacts = []string{"document.authenticity", "document.authenticity"}
		}, want: policyv1.ErrInvalid},
		{name: "duplicate reason", change: func(document *policyv1.Document) { document.Rules[0].Result.ReasonCodes = []string{"failed", "failed"} }, want: policyv1.ErrInvalid},
		{name: "missing assurance", change: func(document *policyv1.Document) { document.VerifiedAssurance = "" }, want: policyv1.ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := validDocument()
			test.change(&document)
			if err := policyv1.Validate(document); !errors.Is(err, test.want) {
				t.Fatalf("Validate() error = %v, want %v", err, test.want)
			}
		})
	}
}

func FuzzParseClosedDocument(f *testing.F) {
	canonical, err := policyv1.Canonical(validDocument())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(canonical)
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema_major":1,"schema_major":1}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		document, parseErr := policyv1.Parse(input)
		if parseErr != nil {
			return
		}
		canonical, canonicalErr := policyv1.Canonical(document)
		if canonicalErr != nil {
			t.Fatalf("Canonical(parsed) error = %v", canonicalErr)
		}
		if _, restoreErr := policyv1.ParseCanonical(canonical); restoreErr != nil {
			t.Fatalf("ParseCanonical(Canonical(parsed)) error = %v", restoreErr)
		}
	})
}

func validDocument() policyv1.Document {
	return policyv1.Document{
		SchemaMajor:       1,
		SchemaMinor:       0,
		PolicyID:          "pol_01K3P4NQF00000000000000001",
		Revision:          3,
		VerifiedAssurance: "global_individual_substantial.1",
		Rules: []policyv1.Rule{
			{
				Name: "document_passed",
				When: `facts["document.authenticity"] == "satisfied" && facts["selfie.liveness"] == "satisfied"`,
				Result: policyv1.Result{State: policyv1.RequirementSatisfied,
					Directive: policyv1.DirectiveCompleteVerified, Priority: 100,
					ContributingFacts: []string{"document.authenticity", "selfie.liveness"},
					ReasonCodes:       []string{"requirements_satisfied"}},
			},
			{
				Name: "document_failed",
				When: `facts["document.authenticity"] == "not_satisfied"`,
				Result: policyv1.Result{State: policyv1.RequirementNotSatisfied,
					Directive: policyv1.DirectiveCompleteNotVerified, Priority: 10,
					ContributingFacts: []string{"document.authenticity"},
					ReasonCodes:       []string{"document_failed"}},
			},
		},
	}
}
