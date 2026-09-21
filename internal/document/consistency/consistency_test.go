package consistency_test

import (
	"cmp"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/document"
	"github.com/Mujhtech/idenqa/internal/document/consistency"
)

var evaluationTime = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

func TestCompareAgeAndExpiry(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		fields     []document.Field
		wantExpiry consistency.ExpiryState
		wantAge    int
		wantKnown  bool
	}{
		{
			name: "valid document",
			fields: []document.Field{
				field(document.NameDateOfBirth, "1974-08-12"),
				field(document.NameDateOfExpiry, "2030-01-01"),
			},
			wantExpiry: consistency.ExpiryValid, wantAge: 52, wantKnown: true,
		},
		{
			name: "expiring within window",
			fields: []document.Field{
				field(document.NameDateOfBirth, "1974-08-12"),
				field(document.NameDateOfExpiry, "2026-09-15"),
			},
			wantExpiry: consistency.ExpiryExpiringSoon, wantAge: 52, wantKnown: true,
		},
		{
			name: "expired",
			fields: []document.Field{
				field(document.NameDateOfBirth, "1974-08-12"),
				field(document.NameDateOfExpiry, "2012-04-15"),
			},
			wantExpiry: consistency.ExpiryExpired, wantAge: 52, wantKnown: true,
		},
		{
			name: "unavailable",
			fields: []document.Field{
				field(document.NameDocumentNumber, "X1"),
			},
			wantExpiry: consistency.ExpiryUnavailable, wantAge: 0, wantKnown: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			front := analysis(t, document.SideFront, test.fields, nil)
			result, err := consistency.Compare(front, nil, evaluationTime)
			if err != nil {
				t.Fatal(err)
			}
			if result.Expiry != test.wantExpiry || result.Age != test.wantAge || result.AgeKnown != test.wantKnown {
				t.Fatalf("result = %+v", result)
			}
		})
	}
}

func TestCompareCrossSideAndPlan(t *testing.T) {
	t.Parallel()
	front := analysis(t, document.SideFront, []document.Field{
		field(document.NameDocumentNumber, "X100"),
		field(document.NameDateOfBirth, "1990-01-01"),
	}, []document.Side{document.SideFront, document.SideBack})
	back := analysis(t, document.SideBack, []document.Field{
		field(document.NameDocumentNumber, "X200"),
		field(document.NameDateOfBirth, "1990-01-01"),
	}, nil)
	result, err := consistency.Compare(front, back, evaluationTime)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(result.Findings, consistency.FindingSideMismatch, "document_number_mismatch") {
		t.Fatalf("findings = %v", result.Findings)
	}

	missing, err := consistency.Compare(front, nil, evaluationTime)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(missing.Findings, consistency.FindingSideMissing, "document_back_missing") {
		t.Fatalf("findings = %v", missing.Findings)
	}
}

func TestCompareWithinSourceConflict(t *testing.T) {
	t.Parallel()
	front := analysis(t, document.SideFront, []document.Field{{
		Name: document.NameDocumentNumber, Value: "X100", Source: document.SourceProvider,
		Sources: []document.Source{document.SourceMRZ, document.SourceProvider}, Agreement: document.AgreementConflicted,
	}}, nil)
	result, err := consistency.Compare(front, nil, evaluationTime)
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(result.Findings, consistency.FindingFieldConflict, "document_field_conflict") {
		t.Fatalf("findings = %v", result.Findings)
	}
}

func TestCompareZeroEvaluationTime(t *testing.T) {
	t.Parallel()
	front := analysis(t, document.SideFront, []document.Field{
		field(document.NameDateOfBirth, "1990-01-01"),
		field(document.NameDateOfExpiry, "2030-01-01"),
	}, nil)
	result, err := consistency.Compare(front, nil, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if result.AgeKnown || result.Expiry != consistency.ExpiryUnavailable {
		t.Fatalf("result = %+v", result)
	}
}

func TestCompareRejectsInvalidAnalysis(t *testing.T) {
	t.Parallel()
	front := analysis(t, document.SideFront, nil, nil)
	front.Side = "sideways"
	if _, err := consistency.Compare(front, nil, evaluationTime); !errors.Is(err, consistency.ErrInvalid) {
		t.Fatalf("error = %v", err)
	}
}

func field(name document.Name, value string) document.Field {
	return document.Field{
		Name: name, Value: value, Source: document.SourceProvider,
		Sources: []document.Source{document.SourceProvider}, Agreement: document.AgreementSingle,
	}
}

func analysis(t *testing.T, side document.Side, fields []document.Field, plan []document.Side) *document.Analysis {
	t.Helper()
	slices.SortFunc(fields, func(left, right document.Field) int { return cmp.Compare(left.Name, right.Name) })
	slices.Sort(plan)
	result := &document.Analysis{
		Side:      side,
		Fields:    fields,
		PlanSides: plan,
		Classification: document.Classification{
			DocumentType: document.DocumentTypePassport,
			IssuingState: "NGA",
			Status:       document.ClassificationDefinitive,
		},
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("invalid test analysis: %v", err)
	}
	return result
}

func hasFinding(findings []consistency.Finding, kind consistency.FindingKind, code string) bool {
	for _, finding := range findings {
		if finding.Kind == kind && finding.Code == code {
			return true
		}
	}
	return false
}
