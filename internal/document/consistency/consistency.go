// Package consistency compares canonical document fields within and across
// document sides and classifies expiry and age against an explicit evaluation
// time. Findings carry bounded reason codes and canonical field names only;
// raw field values never appear in a finding.
package consistency

import (
	"errors"
	"slices"
	"sort"
	"time"

	"github.com/Mujhtech/idenqa/internal/document"
)

// ExpiryWarningWindow is the bounded warning horizon before an expiry date.
const ExpiryWarningWindow = 30 * 24 * time.Hour

// MaximumFindings and MaximumAgeYears bound the deterministic output.
const (
	MaximumFindings = 32
	MaximumAgeYears = 120
)

// ErrInvalid means a supplied analysis failed closed validation.
var ErrInvalid = errors.New("document/consistency: invalid analysis")

// ExpiryState classifies a resolved expiry date against the evaluation time.
type ExpiryState string

// Expiry states form the closed v1 vocabulary.
const (
	ExpiryValid        ExpiryState = "valid"
	ExpiryExpiringSoon ExpiryState = "expiring_soon"
	ExpiryExpired      ExpiryState = "expired"
	ExpiryUnavailable  ExpiryState = "unavailable"
)

// FindingKind identifies one bounded consistency finding class.
type FindingKind string

// Finding kinds form the closed v1 vocabulary.
const (
	FindingFieldConflict FindingKind = "field_conflict"
	FindingSideMismatch  FindingKind = "side_mismatch"
	FindingSideMissing   FindingKind = "side_missing"
	FindingInvalid       FindingKind = "invalid"
)

// Finding is one bounded consistency result without values.
type Finding struct {
	Kind FindingKind
	Side document.Side
	Name document.Name
	Code string
}

// Result is the deterministic comparison of one or two document sides.
type Result struct {
	Findings []Finding
	Expiry   ExpiryState
	Age      int
	AgeKnown bool
}

// Compare evaluates within-side source agreement and, when both sides are
// present, cross-side field and classification correspondence. A zero
// evaluation time leaves age and expiry unavailable.
func Compare(front, back *document.Analysis, evaluatedAt time.Time) (Result, error) {
	for _, analysis := range []*document.Analysis{front, back} {
		if analysis != nil && !analysis.Valid() {
			return Result{}, ErrInvalid
		}
	}
	result := Result{Expiry: ExpiryUnavailable}
	analyses := make([]*document.Analysis, 0, 2)
	if front != nil {
		analyses = append(analyses, front)
	}
	if back != nil {
		analyses = append(analyses, back)
	}
	var findings []Finding
	add := func(finding Finding) { findings = appendFinding(findings, finding) }

	for _, analysis := range analyses {
		for _, field := range analysis.Fields {
			if field.Agreement == document.AgreementConflicted {
				add(Finding{Kind: FindingFieldConflict, Side: analysis.Side, Name: field.Name, Code: "document_field_conflict"})
			}
		}
	}
	if front != nil && back != nil {
		for _, field := range front.Fields {
			counterpart, exists := back.Field(field.Name)
			if !exists || counterpart.Value == field.Value {
				continue
			}
			add(Finding{Kind: FindingSideMismatch, Side: back.Side, Name: field.Name, Code: mismatchCode(field.Name)})
		}
		if front.Classification.Status == document.ClassificationDefinitive &&
			back.Classification.Status == document.ClassificationDefinitive {
			if front.Classification.DocumentType != back.Classification.DocumentType {
				add(Finding{Kind: FindingSideMismatch, Side: back.Side, Name: document.NameDocumentType, Code: "document_type_mismatch"})
			}
			if front.Classification.IssuingState != "" && back.Classification.IssuingState != "" &&
				front.Classification.IssuingState != back.Classification.IssuingState {
				add(Finding{Kind: FindingSideMismatch, Side: back.Side, Name: document.NameIssuingState, Code: "document_issuing_state_mismatch"})
			}
		}
	}
	planSides := make([]document.Side, 0, document.MaximumPlanSides)
	for _, analysis := range analyses {
		for _, side := range analysis.PlanSides {
			if !slices.Contains(planSides, side) {
				planSides = append(planSides, side)
			}
		}
	}
	if slices.Contains(planSides, document.SideFront) && slices.Contains(planSides, document.SideBack) {
		if front == nil {
			add(Finding{Kind: FindingSideMissing, Side: document.SideFront, Code: "document_front_missing"})
		}
		if back == nil {
			add(Finding{Kind: FindingSideMissing, Side: document.SideBack, Code: "document_back_missing"})
		}
	}

	if !evaluatedAt.IsZero() {
		if expiry, ok := firstField(analyses, document.NameDateOfExpiry); ok {
			result.Expiry = expiryState(expiry, evaluatedAt)
		}
		if birth, ok := firstField(analyses, document.NameDateOfBirth); ok {
			age, ageKnown := ageAt(birth, evaluatedAt)
			result.Age, result.AgeKnown = age, ageKnown
			if !ageKnown {
				add(Finding{Kind: FindingInvalid, Name: document.NameDateOfBirth, Code: "document_birth_date_invalid"})
			}
		}
	}
	sortFindings(findings)
	result.Findings = findings
	return result, nil
}

func firstField(analyses []*document.Analysis, name document.Name) (string, bool) {
	for _, analysis := range analyses {
		if field, exists := analysis.Field(name); exists {
			return field.Value, true
		}
	}
	return "", false
}

func expiryState(value string, evaluatedAt time.Time) ExpiryState {
	expiry, err := time.Parse("2006-01-02", value)
	if err != nil {
		return ExpiryUnavailable
	}
	today := dateOnly(evaluatedAt)
	if expiry.Before(today) {
		return ExpiryExpired
	}
	if expiry.Sub(today) <= ExpiryWarningWindow {
		return ExpiryExpiringSoon
	}
	return ExpiryValid
}

func ageAt(value string, evaluatedAt time.Time) (int, bool) {
	birth, err := time.Parse("2006-01-02", value)
	if err != nil {
		return 0, false
	}
	today := dateOnly(evaluatedAt)
	if birth.After(today) {
		return 0, false
	}
	years := today.Year() - birth.Year()
	if today.Month() < birth.Month() || (today.Month() == birth.Month() && today.Day() < birth.Day()) {
		years--
	}
	if years < 0 || years > MaximumAgeYears {
		return 0, false
	}
	return years, true
}

func dateOnly(value time.Time) time.Time {
	utc := value.UTC()
	return time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
}

var mismatchCodes = map[document.Name]string{
	document.NameDocumentNumber: "document_number_mismatch",
	document.NameDocumentType:   "document_type_mismatch",
	document.NameIssuingState:   "document_issuing_state_mismatch",
	document.NameNationality:    "document_nationality_mismatch",
	document.NameFirstName:      "document_first_name_mismatch",
	document.NameLastName:       "document_last_name_mismatch",
	document.NameOtherNames:     "document_other_names_mismatch",
	document.NameDateOfBirth:    "document_birth_date_mismatch",
	document.NameDateOfExpiry:   "document_expiry_date_mismatch",
	document.NameDateOfIssue:    "document_issue_date_mismatch",
	document.NameSex:            "document_sex_mismatch",
	document.NamePersonalNumber: "document_personal_number_mismatch",
}

func mismatchCode(name document.Name) string {
	if code, exists := mismatchCodes[name]; exists {
		return code
	}
	return "document_field_mismatch"
}

func appendFinding(findings []Finding, finding Finding) []Finding {
	if len(findings) >= MaximumFindings || slices.Contains(findings, finding) {
		return findings
	}
	return append(findings, finding)
}

func sortFindings(findings []Finding) {
	sort.Slice(findings, func(left, right int) bool {
		if findings[left].Kind != findings[right].Kind {
			return findings[left].Kind < findings[right].Kind
		}
		if findings[left].Side != findings[right].Side {
			return findings[left].Side < findings[right].Side
		}
		if findings[left].Name != findings[right].Name {
			return findings[left].Name < findings[right].Name
		}
		return findings[left].Code < findings[right].Code
	})
}
