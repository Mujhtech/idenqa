package fields_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/document"
	"github.com/Mujhtech/idenqa/internal/document/fields"
)

const (
	td3Line1 = "P<UTOERIKSSON<<ANNA<MARIA<<<<<<<<<<<<<<<<<<<"
	td3Line2 = "L898902C36UTO7408122F1204159ZE184226B<<<<<10"
)

var reference = time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

func TestAnalyseMergesProviderMRZAndBarcode(t *testing.T) {
	t.Parallel()
	analysis, err := fields.Analyse(fields.Input{
		Side:     document.SideFront,
		MRZLines: []string{td3Line1, td3Line2},
		ProviderFields: []fields.RawField{
			{Name: "document_number", Value: "L898902C3"},
			{Name: "first_name", Value: "Anna Maria"},
			{Name: "last_name", Value: "Eriksson"},
			{Name: "document_type", Value: "passport"},
			{Name: "issuing_country", Value: "UTO"},
			{Name: "date_of_birth", Value: "1974-08-12"},
		},
		Barcodes: []string{
			"@\n\x1e\rANSI 636000080002PP00410272\nDCSERIKSSON\nDBB08121974\nDAQL898902C3\nDCGUTO\n",
		},
		PlanSides: []document.Side{document.SideFront, document.SideBack},
		Reference: reference,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := analysis.Validate(); err != nil {
		t.Fatalf("validate = %v", err)
	}
	if analysis.MRZ == nil || !analysis.MRZ.Valid || analysis.MRZ.Format != "td3" {
		t.Fatalf("mrz = %+v", analysis.MRZ)
	}
	assertField(t, analysis, document.NameDocumentNumber, "L898902C3", document.AgreementAgreed)
	assertField(t, analysis, document.NameFirstName, "ANNA MARIA", document.AgreementAgreed)
	assertField(t, analysis, document.NameLastName, "ERIKSSON", document.AgreementAgreed)
	assertField(t, analysis, document.NameDateOfBirth, "1974-08-12", document.AgreementAgreed)
	if analysis.Classification.Status != document.ClassificationDefinitive ||
		analysis.Classification.DocumentType != document.DocumentTypePassport ||
		analysis.Classification.IssuingState != "UTO" {
		t.Fatalf("classification = %+v", analysis.Classification)
	}
	if !containsSide(analysis.PlanSides, document.SideBack) {
		t.Fatalf("plan sides = %v", analysis.PlanSides)
	}
}

func TestAnalyseConflictingSourcesFailSafe(t *testing.T) {
	t.Parallel()
	analysis, err := fields.Analyse(fields.Input{
		Side:     document.SideFront,
		MRZLines: []string{td3Line1, td3Line2},
		ProviderFields: []fields.RawField{
			{Name: "document_number", Value: "X0000000"},
		},
		Reference: reference,
	})
	if err != nil {
		t.Fatal(err)
	}
	field, ok := analysis.Field(document.NameDocumentNumber)
	if !ok || field.Agreement != document.AgreementConflicted || field.Value != "X0000000" {
		t.Fatalf("field = %+v", field)
	}
	if !hasIssue(analysis.Issues, fields.IssueFieldConflict, string(document.NameDocumentNumber)) {
		t.Fatalf("issues = %v", analysis.Issues)
	}
}

func TestAnalyseUnparsableAndAmbiguousInputs(t *testing.T) {
	t.Parallel()
	analysis, err := fields.Analyse(fields.Input{
		Side:     document.SideBack,
		MRZLines: []string{"not-an-mrz"},
		Barcodes: []string{"document_number=X&document_number=Y"},
		ProviderFields: []fields.RawField{
			{Name: "document_number", Value: "X123"},
			{Name: "nationality", Value: "NGA"},
		},
		Reference: reference,
	})
	if err != nil {
		t.Fatal(err)
	}
	if analysis.MRZ == nil || analysis.MRZ.Valid || analysis.MRZ.Format != "unparsable" {
		t.Fatalf("mrz = %+v", analysis.MRZ)
	}
	if !hasIssue(analysis.Issues, fields.IssueMRZUnparsable, "zone") {
		t.Fatalf("issues = %v", analysis.Issues)
	}
	if !hasIssue(analysis.Issues, fields.IssueBarcodeUnparsable, "barcode") {
		t.Fatalf("issues = %v", analysis.Issues)
	}
	assertField(t, analysis, document.NameDocumentNumber, "X123", document.AgreementSingle)
	assertField(t, analysis, document.NameNationality, "NGA", document.AgreementSingle)
}

func TestAnalyseRejectsUnboundedAndInvalidInput(t *testing.T) {
	t.Parallel()
	if _, err := fields.Analyse(fields.Input{Side: "sideways"}); !errors.Is(err, fields.ErrInvalid) {
		t.Fatalf("invalid side = %v", err)
	}
	tooMany := make([]fields.RawField, 65)
	for index := range tooMany {
		tooMany[index] = fields.RawField{Name: "document_number", Value: "X"}
	}
	if _, err := fields.Analyse(fields.Input{Side: document.SideFront, ProviderFields: tooMany}); !errors.Is(err, fields.ErrInvalid) {
		t.Fatalf("unbounded fields = %v", err)
	}
}

func TestAnalyseConservativeDateNormalisation(t *testing.T) {
	t.Parallel()
	analysis, err := fields.Analyse(fields.Input{
		Side: document.SideFront,
		ProviderFields: []fields.RawField{
			{Name: "date_of_birth", Value: "1974-08-12T00:00:00Z"},
			{Name: "date_of_issue", Value: "15/08/2024"},
			{Name: "date_of_expiry", Value: "120415"},
		},
		Reference: reference,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertField(t, analysis, document.NameDateOfBirth, "1974-08-12", document.AgreementSingle)
	if _, ok := analysis.Field(document.NameDateOfIssue); ok {
		t.Fatal("ambiguous slash date accepted")
	}
	if _, ok := analysis.Field(document.NameDateOfExpiry); ok {
		t.Fatal("compact provider date accepted as unambiguous")
	}
	if !hasIssue(analysis.Issues, fields.IssueFieldValueInvalid, string(document.NameDateOfIssue)) {
		t.Fatalf("issues = %v", analysis.Issues)
	}
}

func assertField(t *testing.T, analysis document.Analysis, name document.Name, value string, agreement document.Agreement) {
	t.Helper()
	field, ok := analysis.Field(name)
	if !ok {
		t.Fatalf("missing field %s", name)
	}
	if field.Value != value || field.Agreement != agreement {
		t.Fatalf("field %s = %+v", name, field)
	}
}

func hasIssue(issues []document.Issue, code, field string) bool {
	for _, issue := range issues {
		if issue.Code == code && issue.Field == field {
			return true
		}
	}
	return false
}

func containsSide(sides []document.Side, wanted document.Side) bool {
	for _, side := range sides {
		if side == wanted {
			return true
		}
	}
	return false
}
