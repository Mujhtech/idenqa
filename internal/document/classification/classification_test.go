package classification_test

import (
	"testing"

	"github.com/Mujhtech/idenqa/internal/document"
	"github.com/Mujhtech/idenqa/internal/document/classification"
)

func TestDerivePrecedenceAndConflicts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		assertions []classification.Assertion
		wantType   string
		wantState  string
		wantStatus string
		wantReason string
	}{
		{
			name: "provider and mrz agree",
			assertions: []classification.Assertion{
				{DocumentType: "passport", IssuingState: "NG", Source: document.SourceProvider},
				{DocumentType: "passport", IssuingState: "NGA", Source: document.SourceMRZ},
			},
			wantType: document.DocumentTypePassport, wantState: "NGA", wantStatus: document.ClassificationDefinitive,
		},
		{
			name: "provider fills type and mrz fills state",
			assertions: []classification.Assertion{
				{DocumentType: "passport", Source: document.SourceProvider},
				{IssuingState: "GHA", Source: document.SourceMRZ},
			},
			wantType: document.DocumentTypePassport, wantState: "GHA", wantStatus: document.ClassificationDefinitive,
		},
		{
			name: "provider conflicts with mrz",
			assertions: []classification.Assertion{
				{DocumentType: "passport", IssuingState: "NGA", Source: document.SourceProvider},
				{DocumentType: "national_id", IssuingState: "GHA", Source: document.SourceMRZ},
			},
			wantType: document.DocumentTypeUnknown, wantState: "", wantStatus: document.ClassificationProvisional,
			wantReason: classification.ReasonConflict,
		},
		{
			name:       "absent",
			assertions: nil,
			wantType:   document.DocumentTypeUnknown, wantStatus: document.ClassificationProvisional,
			wantReason: classification.ReasonUnknown,
		},
		{
			name: "unknown type stays provisional",
			assertions: []classification.Assertion{
				{DocumentType: "arc_card", IssuingState: "KEN", Source: document.SourceProvider},
			},
			wantType: document.DocumentTypeUnknown, wantState: "KEN", wantStatus: document.ClassificationProvisional,
			wantReason: classification.ReasonUnrecognised,
		},
		{
			name: "unrecognised provider keeps mrz type provisional",
			assertions: []classification.Assertion{
				{DocumentType: "arc_card", Source: document.SourceProvider},
				{DocumentType: "passport", IssuingState: "KEN", Source: document.SourceMRZ},
			},
			wantType: document.DocumentTypePassport, wantState: "KEN", wantStatus: document.ClassificationProvisional,
			wantReason: classification.ReasonUnrecognised,
		},
		{
			name: "unmapped country stays provisional",
			assertions: []classification.Assertion{
				{DocumentType: "passport", IssuingState: "US", Source: document.SourceProvider},
			},
			wantType: document.DocumentTypePassport, wantState: "", wantStatus: document.ClassificationProvisional,
			wantReason: classification.ReasonCountryUnknown,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := classification.Derive(test.assertions)
			if result.DocumentType != test.wantType || result.IssuingState != test.wantState || result.Status != test.wantStatus {
				t.Fatalf("result = %+v", result)
			}
			if test.wantReason != "" && !contains(result.Reasons, test.wantReason) {
				t.Fatalf("reasons = %v", result.Reasons)
			}
		})
	}
}

func TestTypeMappings(t *testing.T) {
	t.Parallel()
	if got := classification.TypeFromMRZCode("P<"); got != document.DocumentTypePassport {
		t.Fatalf("P< = %s", got)
	}
	if got := classification.TypeFromMRZCode("IP"); got != document.DocumentTypePassport {
		t.Fatalf("IP = %s", got)
	}
	if got := classification.TypeFromMRZCode("ID"); got != document.DocumentTypeNationalID {
		t.Fatalf("ID = %s", got)
	}
	if got := classification.TypeFromMRZCode("V<"); got != document.DocumentTypeUnknown {
		t.Fatalf("V< = %s", got)
	}
	if got := classification.TypeFromAAMVADesignator("DL"); got != document.DocumentTypeDriversLicense {
		t.Fatalf("DL = %s", got)
	}
	if got, ok := classification.NormaliseCountry("ZA"); !ok || got != "ZAF" {
		t.Fatalf("ZA = %s, %t", got, ok)
	}
	if _, ok := classification.NormaliseCountry("US"); ok {
		t.Fatal("unmapped alpha-2 accepted")
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
