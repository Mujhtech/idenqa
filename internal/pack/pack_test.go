package pack

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/document"
	"github.com/Mujhtech/idenqa/internal/policy"
)

func testDocument(kind DocumentType) Document {
	return Document{
		Type:                       kind,
		KnownVersions:              []KnownVersion{},
		RequiredSides:              []Side{},
		SupportedFields:            []document.Name{},
		SecurityChecks:             []string{},
		Barcode:                    Declaration{State: DeclarationUnknown, Formats: []string{}, Reason: "public_structural_source_not_cited"},
		MRZ:                        Declaration{State: DeclarationUnknown, Formats: []string{}, Reason: "public_structural_source_not_cited"},
		NFC:                        Declaration{State: DeclarationUnknown, Formats: []string{}, Reason: "public_structural_source_not_cited"},
		ModelAndParserRequirements: []RequirementReference{},
		EvaluationCoverage:         EvaluationCoverage{State: EvaluationNotEvaluated, References: []string{}},
		SupportLevel:               SupportUnsupported,
		KnownLimitations:           []string{"pending_public_structural_source"},
		Evidence:                   []string{},
		AssuranceMappings:          []AssuranceMapping{{State: AssuranceNotMapped, Reason: "no_pack_specific_assurance_profile"}},
	}
}

func testPassport() Document {
	value := testDocument(DocumentTypePassport)
	value.KnownVersions = []KnownVersion{{Version: "icao-9303-td3", Reference: "ICAO Doc 9303 Part 4"}}
	value.RequiredSides = []Side{SideFront}
	value.SupportedFields = []document.Name{document.NameDocumentNumber, document.NameDateOfBirth}
	value.MRZ = Declaration{State: DeclarationSupported, Formats: []string{"td3"}, Reference: "ICAO Doc 9303 Part 4"}
	value.ModelAndParserRequirements = []RequirementReference{{Kind: "parser", Reference: "idenqa.document.mrz", Version: "v1"}}
	value.SupportLevel = SupportStructurallySupported
	value.KnownLimitations = []string{"no_legal_review", "no_provider_evaluation"}
	value.Evidence = []string{"core_mrz_parser_v1", "icao_9303_td3_structure"}
	return value
}

func testPack(revision uint32, lifecycle LifecycleState) Pack {
	return Pack{
		SchemaMajor:   SchemaMajor,
		SchemaMinor:   SchemaMinor,
		Country:       "NG",
		CountryAlpha3: "NGA",
		Authority:     "idenqa.requirement.authority",
		Revision:      revision,
		PublishedAt:   time.Date(2026, time.September, 20, 0, 0, 0, 0, time.UTC),
		Lifecycle:     lifecycle,
		LegalReview:   LegalReview{State: LegalReviewNotReviewed},
		RequirementSlots: []RequirementSlot{
			{Name: SlotAuthority, Key: "idenqa.requirement.authority"},
		},
		Documents: []Document{
			testPassport(),
			testDocument(DocumentTypeNationalID),
			testDocument(DocumentTypeDriverLicence),
		},
	}
}

func mustCanonical(t *testing.T, value Pack) Pack {
	t.Helper()
	canonical, err := Canonicalize(value)
	if err != nil {
		t.Fatalf("Canonicalize() error = %v", err)
	}
	return canonical
}

func TestCanonicalizeStableDigestAndParseRoundTrip(t *testing.T) {
	t.Parallel()

	first := mustCanonical(t, testPack(1, LifecycleActive))
	second := mustCanonical(t, testPack(1, LifecycleActive))
	if first.Digest != second.Digest || len(first.Digest) != 64 {
		t.Fatalf("digests = %q and %q, want one stable 64-character digest", first.Digest, second.Digest)
	}
	encoded, err := first.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	parsed, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if parsed.Digest != first.Digest {
		t.Fatalf("parsed digest = %q, want %q", parsed.Digest, first.Digest)
	}
	if _, err := Parse(append(slices.Clone(encoded), []byte(" {}")...)); err == nil {
		t.Fatal("Parse() accepted trailing content")
	}
	unknown := strings.Replace(string(encoded), `{"schema_major"`, `{"unknown_field":true,"schema_major"`, 1)
	if _, err := Parse([]byte(unknown)); err == nil {
		t.Fatal("Parse() accepted an unknown field")
	}
}

func TestValidateFailsClosed(t *testing.T) {
	t.Parallel()

	valid := mustCanonical(t, testPack(1, LifecycleActive))
	tests := []struct {
		name   string
		mutate func(*Pack)
	}{
		{name: "schema major", mutate: func(value *Pack) { value.SchemaMajor = 2 }},
		{name: "schema minor", mutate: func(value *Pack) { value.SchemaMinor = 1 }},
		{name: "unknown country", mutate: func(value *Pack) { value.Country = "XX"; value.CountryAlpha3 = "XXX" }},
		{name: "mismatched country pair", mutate: func(value *Pack) { value.CountryAlpha3 = "GHA" }},
		{name: "zero revision", mutate: func(value *Pack) { value.Revision = 0 }},
		{name: "digest", mutate: func(value *Pack) { value.Digest = strings.Repeat("f", 64) }},
		{name: "published at", mutate: func(value *Pack) { value.PublishedAt = time.Time{} }},
		{name: "published location", mutate: func(value *Pack) {
			value.PublishedAt = value.PublishedAt.In(time.FixedZone("offset", 3600))
		}},
		{name: "lifecycle", mutate: func(value *Pack) { value.Lifecycle = "published" }},
		{name: "legal review", mutate: func(value *Pack) { value.LegalReview.State = "approved" }},
		{name: "legal reviewed without reference", mutate: func(value *Pack) {
			value.LegalReview.State = LegalReviewReviewed
		}},
		{name: "no documents", mutate: func(value *Pack) { value.Documents = []Document{} }},
		{name: "duplicate documents", mutate: func(value *Pack) {
			value.Documents = append(value.Documents, value.Documents[0])
		}},
		{name: "unknown document type", mutate: func(value *Pack) { value.Documents[0].Type = "residence_permit" }},
		{name: "no requirement slots", mutate: func(value *Pack) { value.RequirementSlots = []RequirementSlot{} }},
		{name: "unknown slot", mutate: func(value *Pack) { value.RequirementSlots[0].Name = "lawfulness" }},
		{name: "duplicate slot", mutate: func(value *Pack) {
			value.RequirementSlots = append(value.RequirementSlots, value.RequirementSlots[0])
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid.Clone()
			test.mutate(&value)
			if err := value.Validate(); err == nil {
				t.Fatal("Validate() accepted invalid content")
			}
		})
	}
}

func TestDocumentValidationFailsClosed(t *testing.T) {
	t.Parallel()

	valid := mustCanonical(t, testPack(1, LifecycleActive))
	tests := []struct {
		name   string
		mutate func(*Document)
	}{
		{name: "unknown support level", mutate: func(value *Document) { value.SupportLevel = "certified" }},
		{name: "structural declares evaluated", mutate: func(value *Document) {
			value.EvaluationCoverage = EvaluationCoverage{State: EvaluationEvaluated, References: []string{"report.one"}}
		}},
		{name: "structural without evidence", mutate: func(value *Document) { value.Evidence = []string{} }},
		{name: "structural without sides", mutate: func(value *Document) { value.RequiredSides = []Side{} }},
		{name: "unsupported declares sides", mutate: func(value *Document) {
			value.Type = DocumentTypeNationalID
			value.SupportLevel = SupportUnsupported
			value.KnownVersions = []KnownVersion{}
			value.Evidence = []string{}
			value.MRZ = Declaration{State: DeclarationUnknown, Formats: []string{}, Reason: "public_structural_source_not_cited"}
			value.ModelAndParserRequirements = []RequirementReference{}
			value.RequiredSides = []Side{SideFront}
		}},
		{name: "unsupported declares fields", mutate: func(value *Document) {
			value.Type = DocumentTypeNationalID
			value.SupportLevel = SupportUnsupported
			value.KnownVersions = []KnownVersion{}
			value.Evidence = []string{}
			value.MRZ = Declaration{State: DeclarationUnknown, Formats: []string{}, Reason: "public_structural_source_not_cited"}
			value.ModelAndParserRequirements = []RequirementReference{}
			value.SupportedFields = []document.Name{document.NameSex}
		}},
		{name: "unsupported with evidence", mutate: func(value *Document) {
			value.Type = DocumentTypeNationalID
			value.SupportLevel = SupportUnsupported
			value.KnownVersions = []KnownVersion{}
			value.MRZ = Declaration{State: DeclarationUnknown, Formats: []string{}, Reason: "public_structural_source_not_cited"}
			value.ModelAndParserRequirements = []RequirementReference{}
			value.Evidence = []string{"core_mrz_parser_v1"}
		}},
		{name: "unsupported supported mrz", mutate: func(value *Document) {
			value.Type = DocumentTypeNationalID
			value.SupportLevel = SupportUnsupported
			value.KnownVersions = []KnownVersion{}
			value.Evidence = []string{}
			value.ModelAndParserRequirements = []RequirementReference{}
			value.MRZ = Declaration{State: DeclarationSupported, Formats: []string{"td3"}}
		}},
		{name: "unsupported without limitation", mutate: func(value *Document) {
			value.Type = DocumentTypeNationalID
			value.SupportLevel = SupportUnsupported
			value.KnownVersions = []KnownVersion{}
			value.Evidence = []string{}
			value.MRZ = Declaration{State: DeclarationUnknown, Formats: []string{}, Reason: "public_structural_source_not_cited"}
			value.ModelAndParserRequirements = []RequirementReference{}
			value.KnownLimitations = []string{}
		}},
		{name: "unsupported evaluated", mutate: func(value *Document) {
			value.Type = DocumentTypeNationalID
			value.SupportLevel = SupportUnsupported
			value.KnownVersions = []KnownVersion{}
			value.Evidence = []string{}
			value.MRZ = Declaration{State: DeclarationUnknown, Formats: []string{}, Reason: "public_structural_source_not_cited"}
			value.ModelAndParserRequirements = []RequirementReference{}
			value.EvaluationCoverage = EvaluationCoverage{State: EvaluationEvaluated, References: []string{"report.one"}}
		}},
		{name: "unsorted sides", mutate: func(value *Document) { value.RequiredSides = []Side{SideFront, SideBack} }},
		{name: "duplicate sides", mutate: func(value *Document) { value.RequiredSides = []Side{SideFront, SideFront} }},
		{name: "unsorted fields", mutate: func(value *Document) {
			value.SupportedFields = []document.Name{document.NameSex, document.NameDateOfBirth}
		}},
		{name: "unknown field", mutate: func(value *Document) { value.SupportedFields = []document.Name{"eye_colour"} }},
		{name: "unsorted known versions", mutate: func(value *Document) {
			value.KnownVersions = []KnownVersion{{Version: "zulu", Reference: "a"}, {Version: "alpha", Reference: "b"}}
		}},
		{name: "empty version reference", mutate: func(value *Document) { value.KnownVersions[0].Reference = "" }},
		{name: "supported without formats", mutate: func(value *Document) {
			value.MRZ = Declaration{State: DeclarationSupported, Formats: []string{}}
		}},
		{name: "unknown with formats", mutate: func(value *Document) {
			value.MRZ = Declaration{State: DeclarationUnknown, Formats: []string{"td3"}}
		}},
		{name: "invalid mrz format", mutate: func(value *Document) {
			value.MRZ = Declaration{State: DeclarationSupported, Formats: []string{"td9"}}
		}},
		{name: "unsorted mrz formats", mutate: func(value *Document) {
			value.MRZ = Declaration{State: DeclarationSupported, Formats: []string{"td3", "td1"}}
		}},
		{name: "unknown requirement kind", mutate: func(value *Document) {
			value.ModelAndParserRequirements[0].Kind = "vendor"
		}},
		{name: "evaluation references missing", mutate: func(value *Document) {
			value.EvaluationCoverage = EvaluationCoverage{State: EvaluationPartiallyCovered, References: []string{}}
		}},
		{name: "not evaluated with references", mutate: func(value *Document) {
			value.EvaluationCoverage = EvaluationCoverage{State: EvaluationNotEvaluated, References: []string{"report.one"}}
		}},
		{name: "mapped unknown capability", mutate: func(value *Document) {
			value.AssuranceMappings = []AssuranceMapping{{State: AssuranceMapped, Capability: "idenqa.assurance.invented"}}
		}},
		{name: "mapped with reason", mutate: func(value *Document) {
			value.AssuranceMappings = []AssuranceMapping{{State: AssuranceMapped, Capability: "idenqa.assurance.freshness", Reason: "why"}}
		}},
		{name: "not mapped with capability", mutate: func(value *Document) {
			value.AssuranceMappings = []AssuranceMapping{{State: AssuranceNotMapped, Capability: "idenqa.assurance.freshness", Reason: "why"}}
		}},
		{name: "not mapped without reason", mutate: func(value *Document) {
			value.AssuranceMappings = []AssuranceMapping{{State: AssuranceNotMapped}}
		}},
		{name: "unsorted evidence", mutate: func(value *Document) {
			value.Evidence = []string{"zulu", "alpha"}
		}},
		{name: "uppercase evidence", mutate: func(value *Document) { value.Evidence = []string{"Core"} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := valid.Clone()
			document, ok := value.Document(DocumentTypePassport)
			if !ok {
				t.Fatal("test pack has no passport document")
			}
			test.mutate(&document)
			if err := document.validate(); err == nil {
				t.Fatal("document validate accepted invalid content")
			}
		})
	}
}

func TestLegalReviewReferenceRules(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		review  LegalReview
		invalid bool
	}{
		{name: "default not reviewed", review: LegalReview{State: LegalReviewNotReviewed}},
		{name: "pending with reference", review: LegalReview{State: LegalReviewPending, Reference: "legal-review-pending"}},
		{name: "reviewed with reference", review: LegalReview{State: LegalReviewReviewed, Reference: "legal-review-2026-09"}},
		{name: "reviewed without reference", review: LegalReview{State: LegalReviewReviewed}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := testPack(1, LifecycleActive)
			value.LegalReview = test.review
			_, err := Canonicalize(value)
			if (err != nil) != test.invalid {
				t.Fatalf("Canonicalize() error = %v, invalid = %v", err, test.invalid)
			}
		})
	}
}

func TestDocumentTypeFromCanonical(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]DocumentType{
		"passport":        DocumentTypePassport,
		"national_id":     DocumentTypeNationalID,
		"drivers_license": DocumentTypeDriverLicence,
	} {
		got, ok := DocumentTypeFromCanonical(input)
		if !ok || got != want {
			t.Fatalf("DocumentTypeFromCanonical(%q) = %q, %v", input, got, ok)
		}
	}
	for _, input := range []string{"unknown", "residence_permit", "driver_licence", ""} {
		if _, ok := DocumentTypeFromCanonical(input); ok {
			t.Fatalf("DocumentTypeFromCanonical(%q) accepted a packless type", input)
		}
	}
}

func TestNormalizeCountryRejectsUnassignedCodes(t *testing.T) {
	t.Parallel()

	padded := " Gh "
	for input, want := range map[string][2]string{
		"ng":   {"NG", "NGA"},
		"NGA":  {"NG", "NGA"},
		padded: {"GH", "GHA"},
	} {
		alpha2, alpha3, ok := NormalizeCountry(input)
		if !ok || alpha2 != want[0] || alpha3 != want[1] {
			t.Fatalf("NormalizeCountry(%q) = %q, %q, %v", input, alpha2, alpha3, ok)
		}
	}
	for _, input := range []string{"", "X", "XX", "XXX", "ZZ"} {
		if _, _, ok := NormalizeCountry(input); ok {
			t.Fatalf("NormalizeCountry(%q) accepted an unassigned code", input)
		}
	}
}

func TestPlatformAssuranceCapabilitiesMirrorPolicy(t *testing.T) {
	t.Parallel()

	names := make([]string, 0, len(policy.AssuranceCapabilities()))
	for _, capability := range policy.AssuranceCapabilities() {
		names = append(names, capability.Name)
	}
	if !slices.Equal(names, platformAssuranceCapabilities) {
		t.Fatalf("pack capability mirror = %v, want policy catalog %v", platformAssuranceCapabilities, names)
	}
}

func TestPackJSONFieldOrderIsClosed(t *testing.T) {
	t.Parallel()

	value := mustCanonical(t, testPack(1, LifecycleActive))
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"schema_major"`, `"country"`, `"authority"`, `"revision"`, `"published_at"`, `"digest"`, `"lifecycle_state"`, `"documents"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("canonical pack JSON is missing %s", field)
		}
	}
}
