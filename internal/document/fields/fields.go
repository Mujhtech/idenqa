// Package fields merges provider, model, machine-readable zone, and barcode
// fields into one bounded canonical document.Analysis per document side. It
// normalises names, dates, document numbers, and codes conservatively: it
// never applies fuzzy matching or invents a value a source did not provide.
package fields

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/document"
	"github.com/Mujhtech/idenqa/internal/document/barcode"
	"github.com/Mujhtech/idenqa/internal/document/classification"
	"github.com/Mujhtech/idenqa/internal/document/mrz"
)

// Stable merge issue codes.
const (
	// IssueMRZUnparsable means the supplied lines matched no MRZ layout.
	IssueMRZUnparsable = "mrz_unparsable"
	// IssueBarcodeUnparsable means a supplied decoded payload was rejected.
	IssueBarcodeUnparsable = "barcode_unparsable"
	// IssueFieldConflict means sources disagreed on one canonical field.
	IssueFieldConflict = "document_field_conflict"
	// IssueFieldValueInvalid means a recognised field value was not canonical.
	IssueFieldValueInvalid = "document_field_value_invalid"
)

// Analyse input bounds.
const (
	maximumRawFields  = 64
	maximumRawLength  = 512
	maximumMRZLines   = 3
	maximumBarcodes   = 4
	dateLayout        = "2006-01-02"
	aamvaDateLayout   = "01022006"
	compactDateLayout = "20060102"
)

// ErrInvalid means the analysis input failed closed validation.
var ErrInvalid = errors.New("document/fields: invalid input")

// RawField is one already-extracted provider or model field before
// canonicalisation.
type RawField struct {
	Name  string
	Value string
}

// Input carries every optional document-knowledge source for one side.
type Input struct {
	Side           document.Side
	ProviderFields []RawField
	ModelFields    []RawField
	MRZLines       []string
	Barcodes       []string
	PlanSides      []document.Side
	Reference      time.Time
}

// Analyse canonicalises and merges all supplied sources into one analysis.
func Analyse(input Input) (document.Analysis, error) {
	if !input.Side.Valid() || len(input.ProviderFields) > maximumRawFields ||
		len(input.ModelFields) > maximumRawFields || len(input.MRZLines) > maximumMRZLines ||
		len(input.Barcodes) > maximumBarcodes || len(input.PlanSides) > document.MaximumPlanSides {
		return document.Analysis{}, ErrInvalid
	}
	analysis := document.Analysis{Side: input.Side}
	var issues []document.Issue
	addIssue := func(issue document.Issue) { issues = appendIssue(issues, issue) }
	merged := newContributions()
	analysis.PlanSides = normaliseSides(input.PlanSides)

	var zone *mrz.Zone
	if len(input.MRZLines) > 0 {
		parsed, mrzIssues, err := mrz.Parse(input.MRZLines, input.Reference)
		summary := &document.MRZ{Format: string(parsed.Format), Valid: err == nil && len(mrzIssues) == 0}
		if err != nil {
			summary.Format = "unparsable"
			addIssue(document.Issue{Code: IssueMRZUnparsable, Field: "zone", Source: document.SourceMRZ})
		}
		for _, issue := range mrzIssues {
			summary.Issues = appendIssue(summary.Issues, document.Issue{
				Code: issue.Code, Field: issue.Field, Source: document.SourceMRZ,
			})
		}
		analysis.MRZ = summary
		if err == nil {
			zone = &parsed
		}
	}

	payloads := make([]barcode.Payload, 0, len(input.Barcodes))
	for _, payload := range input.Barcodes {
		parsed, err := barcode.Parse(payload)
		if err != nil {
			addIssue(document.Issue{Code: IssueBarcodeUnparsable, Field: "barcode", Source: document.SourceBarcode})
			continue
		}
		payloads = append(payloads, parsed)
		analysis.Barcodes = append(analysis.Barcodes, string(parsed.Format))
		applyBarcode(parsed, merged, addIssue)
	}

	applyRawFields(document.SourceProvider, input.ProviderFields, merged, addIssue)
	applyRawFields(document.SourceModel, input.ModelFields, merged, addIssue)

	var assertions []classification.Assertion
	assertions = append(assertions, fieldAssertions(input.ProviderFields, document.SourceProvider)...)
	assertions = append(assertions, fieldAssertions(input.ModelFields, document.SourceModel)...)
	if zone != nil {
		if documentType := classification.TypeFromMRZCode(zone.DocumentCode); documentType != document.DocumentTypeUnknown {
			assertions = append(assertions, classification.Assertion{DocumentType: documentType, Source: document.SourceMRZ})
		}
		if zone.IssuingState != "" {
			assertions = append(assertions, classification.Assertion{IssuingState: zone.IssuingState, Source: document.SourceMRZ})
		}
	}
	for _, payload := range payloads {
		assertions = append(assertions, barcodeAssertions(payload)...)
	}

	if zone != nil {
		applyMRZ(*zone, merged, addIssue)
	}
	analysis.Fields = merged.fields(addIssue)
	analysis.Issues = issues
	analysis.Classification = classification.Derive(assertions)
	if err := analysis.Validate(); err != nil {
		return document.Analysis{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return analysis, nil
}

func normaliseSides(sides []document.Side) []document.Side {
	unique := make([]document.Side, 0, len(sides))
	for _, side := range sides {
		if !side.Valid() {
			continue
		}
		if !containsSide(unique, side) {
			unique = append(unique, side)
		}
	}
	sort.Slice(unique, func(left, right int) bool { return unique[left] < unique[right] })
	return unique
}

func containsSide(sides []document.Side, wanted document.Side) bool {
	for _, side := range sides {
		if side == wanted {
			return true
		}
	}
	return false
}

func applyRawFields(source document.Source, raw []RawField, merged *contributions, addIssue func(document.Issue)) {
	for _, field := range raw {
		name, ok := canonicalFieldName(field.Name)
		if !ok || len(field.Value) > maximumRawLength {
			continue
		}
		contribute(merged, name, source, field.Value, addIssue)
	}
}

func fieldAssertions(raw []RawField, source document.Source) []classification.Assertion {
	assertions := make([]classification.Assertion, 0, 2)
	for _, field := range raw {
		name, ok := canonicalFieldName(field.Name)
		if !ok || len(field.Value) > maximumRawLength {
			continue
		}
		switch name {
		case document.NameDocumentType:
			assertions = append(assertions, classification.Assertion{DocumentType: field.Value, Source: source})
		case document.NameIssuingState:
			assertions = append(assertions, classification.Assertion{IssuingState: field.Value, Source: source})
		}
	}
	return assertions
}

func barcodeAssertions(payload barcode.Payload) []classification.Assertion {
	assertions := make([]classification.Assertion, 0, 2)
	for _, field := range payload.Fields {
		switch field.Key {
		case "subfile":
			if documentType := classification.TypeFromAAMVADesignator(field.Value); documentType != document.DocumentTypeUnknown {
				assertions = append(assertions, classification.Assertion{DocumentType: documentType, Source: document.SourceBarcode})
			}
		case "dcg":
			assertions = append(assertions, classification.Assertion{IssuingState: field.Value, Source: document.SourceBarcode})
		default:
			name, ok := canonicalFieldName(field.Key)
			if !ok {
				continue
			}
			switch name {
			case document.NameDocumentType:
				assertions = append(assertions, classification.Assertion{DocumentType: field.Value, Source: document.SourceBarcode})
			case document.NameIssuingState:
				assertions = append(assertions, classification.Assertion{IssuingState: field.Value, Source: document.SourceBarcode})
			}
		}
	}
	return assertions
}

func applyMRZ(zone mrz.Zone, merged *contributions, addIssue func(document.Issue)) {
	contribute(merged, document.NameDocumentNumber, document.SourceMRZ, zone.DocumentNumber, addIssue)
	if documentType := classification.TypeFromMRZCode(zone.DocumentCode); documentType != document.DocumentTypeUnknown {
		merged.add(document.NameDocumentType, document.SourceMRZ, documentType, addIssue)
	}
	contribute(merged, document.NameIssuingState, document.SourceMRZ, zone.IssuingState, addIssue)
	contribute(merged, document.NameNationality, document.SourceMRZ, zone.Nationality, addIssue)
	contribute(merged, document.NameLastName, document.SourceMRZ, zone.Name.Primary, addIssue)
	contribute(merged, document.NameFirstName, document.SourceMRZ, zone.Name.Secondary, addIssue)
	if zone.DateOfBirth.Valid {
		merged.add(document.NameDateOfBirth, document.SourceMRZ, zone.DateOfBirth.Value.Format(dateLayout), addIssue)
	}
	if zone.DateOfExpiry.Valid {
		merged.add(document.NameDateOfExpiry, document.SourceMRZ, zone.DateOfExpiry.Value.Format(dateLayout), addIssue)
	}
	contribute(merged, document.NameSex, document.SourceMRZ, zone.Sex, addIssue)
	contribute(merged, document.NamePersonalNumber, document.SourceMRZ, zone.PersonalNumber, addIssue)
}

func applyBarcode(payload barcode.Payload, merged *contributions, addIssue func(document.Issue)) {
	for _, field := range payload.Fields {
		switch field.Key {
		case "subfile":
			if documentType := classification.TypeFromAAMVADesignator(field.Value); documentType != document.DocumentTypeUnknown {
				merged.add(document.NameDocumentType, document.SourceBarcode, documentType, addIssue)
			}
		case "dcs":
			contribute(merged, document.NameLastName, document.SourceBarcode, field.Value, addIssue)
		case "dac":
			contribute(merged, document.NameFirstName, document.SourceBarcode, field.Value, addIssue)
		case "dbb":
			contributeAAMVADate(merged, document.NameDateOfBirth, field.Value, addIssue)
		case "dba":
			contributeAAMVADate(merged, document.NameDateOfExpiry, field.Value, addIssue)
		case "daq":
			contribute(merged, document.NameDocumentNumber, document.SourceBarcode, field.Value, addIssue)
		case "dcg":
			contribute(merged, document.NameIssuingState, document.SourceBarcode, field.Value, addIssue)
		case "dbc":
			if value, ok := canonicalAAMVASex(field.Value); ok {
				merged.add(document.NameSex, document.SourceBarcode, value, addIssue)
			}
		case "n":
			applyStructuredName(field.Value, merged, addIssue)
		default:
			name, ok := canonicalFieldName(field.Key)
			if !ok || len(field.Value) > maximumRawLength {
				continue
			}
			contribute(merged, name, document.SourceBarcode, field.Value, addIssue)
		}
	}
}

func applyStructuredName(value string, merged *contributions, addIssue func(document.Issue)) {
	parts := strings.Split(value, ";")
	if len(parts) > 0 {
		contribute(merged, document.NameLastName, document.SourceBarcode, parts[0], addIssue)
	}
	if len(parts) > 1 {
		contribute(merged, document.NameFirstName, document.SourceBarcode, parts[1], addIssue)
	}
}

type contributions struct {
	values map[document.Name]map[document.Source]string
}

func newContributions() *contributions {
	return &contributions{values: make(map[document.Name]map[document.Source]string)}
}

func (merged *contributions) add(name document.Name, source document.Source, value string, addIssue func(document.Issue)) {
	if name == "" || value == "" {
		return
	}
	if merged.values[name] == nil {
		merged.values[name] = make(map[document.Source]string)
	}
	if existing, exists := merged.values[name][source]; exists {
		if existing != value {
			addIssue(document.Issue{Code: IssueFieldConflict, Field: string(name), Source: source})
		}
		return
	}
	merged.values[name][source] = value
}

var sourceOrder = []document.Source{document.SourceProvider, document.SourceModel, document.SourceMRZ, document.SourceBarcode}

func (merged *contributions) fields(addIssue func(document.Issue)) []document.Field {
	names := make([]document.Name, 0, len(merged.values))
	for name := range merged.values {
		names = append(names, name)
	}
	sort.Slice(names, func(left, right int) bool { return names[left] < names[right] })
	result := make([]document.Field, 0, len(names))
	for _, name := range names {
		bySource := merged.values[name]
		sources := make([]document.Source, 0, len(bySource))
		values := make(map[string]struct{}, len(bySource))
		winner, winnerValue := document.Source(""), ""
		for _, source := range sourceOrder {
			value, exists := bySource[source]
			if !exists {
				continue
			}
			sources = append(sources, source)
			values[value] = struct{}{}
			if winner == "" {
				winner, winnerValue = source, value
			}
		}
		sort.Slice(sources, func(left, right int) bool { return sources[left] < sources[right] })
		agreement := document.AgreementSingle
		switch {
		case len(values) > 1:
			agreement = document.AgreementConflicted
			addIssue(document.Issue{Code: IssueFieldConflict, Field: string(name), Source: winner})
		case len(sources) > 1:
			agreement = document.AgreementAgreed
		}
		result = append(result, document.Field{
			Name: name, Value: winnerValue, Source: winner, Sources: sources, Agreement: agreement,
		})
	}
	return result
}

func contribute(merged *contributions, name document.Name, source document.Source, raw string, addIssue func(document.Issue)) {
	normalise := normaliserFor(name)
	if normalise == nil {
		return
	}
	value, ok := normalise(raw)
	if !ok {
		if strings.TrimSpace(raw) != "" {
			addIssue(document.Issue{Code: IssueFieldValueInvalid, Field: string(name), Source: source})
		}
		return
	}
	merged.add(name, source, value, addIssue)
}

func contributeAAMVADate(merged *contributions, name document.Name, raw string, addIssue func(document.Issue)) {
	value, ok := canonicalAAMVADate(raw)
	if !ok {
		if strings.TrimSpace(raw) != "" {
			addIssue(document.Issue{Code: IssueFieldValueInvalid, Field: string(name), Source: document.SourceBarcode})
		}
		return
	}
	merged.add(name, document.SourceBarcode, value, addIssue)
}

func normaliserFor(name document.Name) func(string) (string, bool) {
	switch name {
	case document.NameDocumentNumber, document.NamePersonalNumber:
		return canonicalIdentifier
	case document.NameDocumentType:
		return canonicalDocumentType
	case document.NameIssuingState, document.NameNationality:
		return canonicalCountry
	case document.NameFirstName, document.NameLastName:
		return canonicalName
	case document.NameDateOfBirth, document.NameDateOfExpiry, document.NameDateOfIssue:
		return canonicalDate
	case document.NameSex:
		return canonicalSex
	default:
		return nil
	}
}

func canonicalIdentifier(raw string) (string, bool) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	value = strings.NewReplacer(" ", "", "-", "", "<", "", "/", "").Replace(value)
	if value == "" || len(value) > 64 {
		return "", false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') {
			continue
		}
		return "", false
	}
	return value, true
}

func canonicalDocumentType(raw string) (string, bool) {
	return classification.NormaliseDocumentType(raw)
}

func canonicalCountry(raw string) (string, bool) {
	return classification.NormaliseCountry(raw)
}

func canonicalName(raw string) (string, bool) {
	collapsed := strings.Join(strings.Fields(raw), " ")
	if collapsed == "" || len(collapsed) > maximumRawLength {
		return "", false
	}
	transliterated, ok := mrz.Transliterate(collapsed)
	if !ok {
		return "", false
	}
	value := strings.Join(strings.Fields(strings.ReplaceAll(transliterated, "<", " ")), " ")
	if value == "" || len(value) > document.MaximumFieldLength {
		return "", false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == ' ' {
			continue
		}
		return "", false
	}
	return value, true
}

func canonicalSex(raw string) (string, bool) {
	switch strings.ToUpper(strings.TrimSpace(raw)) {
	case "M", "MALE":
		return "M", true
	case "F", "FEMALE":
		return "F", true
	default:
		return "", false
	}
}

func canonicalAAMVASex(raw string) (string, bool) {
	switch strings.TrimSpace(raw) {
	case "1":
		return "M", true
	case "2":
		return "F", true
	default:
		return "", false
	}
}

func canonicalDate(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if len(value) >= len(dateLayout) {
		if parsed, err := time.Parse(dateLayout, value[:len(dateLayout)]); err == nil {
			return parsed.Format(dateLayout), true
		}
	}
	if parsed, err := time.Parse("2006/01/02", value); err == nil {
		return parsed.Format(dateLayout), true
	}
	return "", false
}

func canonicalAAMVADate(raw string) (string, bool) {
	value := strings.TrimSpace(raw)
	if len(value) != len(compactDateLayout) || !allDigits(value) {
		return "", false
	}
	candidates := make([]string, 0, 2)
	if parsed, err := time.Parse(aamvaDateLayout, value); err == nil {
		candidates = append(candidates, parsed.Format(dateLayout))
	}
	if parsed, err := time.Parse(compactDateLayout, value); err == nil && !contains(candidates, parsed.Format(dateLayout)) {
		candidates = append(candidates, parsed.Format(dateLayout))
	}
	if len(candidates) != 1 {
		return "", false
	}
	return candidates[0], true
}

var fieldAliases = map[string]document.Name{
	"document_number":      document.NameDocumentNumber,
	"documentnumber":       document.NameDocumentNumber,
	"doc_number":           document.NameDocumentNumber,
	"docnumber":            document.NameDocumentNumber,
	"document_id":          document.NameDocumentNumber,
	"document_no":          document.NameDocumentNumber,
	"id_number":            document.NameDocumentNumber,
	"idnumber":             document.NameDocumentNumber,
	"document_type":        document.NameDocumentType,
	"documenttype":         document.NameDocumentType,
	"doc_type":             document.NameDocumentType,
	"doctype":              document.NameDocumentType,
	"issuing_state":        document.NameIssuingState,
	"issuing_country":      document.NameIssuingState,
	"issuing_country_code": document.NameIssuingState,
	"country":              document.NameIssuingState,
	"country_code":         document.NameIssuingState,
	"nationality":          document.NameNationality,
	"nationality_code":     document.NameNationality,
	"citizenship":          document.NameNationality,
	"first_name":           document.NameFirstName,
	"other_names":          document.NameOtherNames,
	"other_name":           document.NameOtherNames,
	"firstname":            document.NameFirstName,
	"given_name":           document.NameFirstName,
	"givenname":            document.NameFirstName,
	"given_names":          document.NameFirstName,
	"forename":             document.NameFirstName,
	"forenames":            document.NameFirstName,
	"last_name":            document.NameLastName,
	"lastname":             document.NameLastName,
	"surname":              document.NameLastName,
	"family_name":          document.NameLastName,
	"familyname":           document.NameLastName,
	"date_of_birth":        document.NameDateOfBirth,
	"dateofbirth":          document.NameDateOfBirth,
	"birth_date":           document.NameDateOfBirth,
	"birthdate":            document.NameDateOfBirth,
	"dob":                  document.NameDateOfBirth,
	"date_of_expiry":       document.NameDateOfExpiry,
	"dateofexpiry":         document.NameDateOfExpiry,
	"expiry_date":          document.NameDateOfExpiry,
	"expirydate":           document.NameDateOfExpiry,
	"expiration_date":      document.NameDateOfExpiry,
	"expirationdate":       document.NameDateOfExpiry,
	"date_of_expiration":   document.NameDateOfExpiry,
	"expiry":               document.NameDateOfExpiry,
	"expires":              document.NameDateOfExpiry,
	"date_of_issue":        document.NameDateOfIssue,
	"dateofissue":          document.NameDateOfIssue,
	"issue_date":           document.NameDateOfIssue,
	"issuedate":            document.NameDateOfIssue,
	"personal_number":      document.NamePersonalNumber,
	"personalnumber":       document.NamePersonalNumber,
	"personal_id":          document.NamePersonalNumber,
	"personal_no":          document.NamePersonalNumber,
	"sex":                  document.NameSex,
	"gender":               document.NameSex,
}

func canonicalFieldName(raw string) (document.Name, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	key = strings.NewReplacer(" ", "_", "-", "_", "/", "_").Replace(key)
	for strings.Contains(key, "__") {
		key = strings.ReplaceAll(key, "__", "_")
	}
	key = strings.Trim(key, "_")
	if key == "" || len(key) > 64 {
		return "", false
	}
	if name, ok := fieldAliases[key]; ok {
		return name, true
	}
	if index := strings.LastIndexByte(key, '.'); index >= 0 && index+1 < len(key) {
		if name, ok := fieldAliases[key[index+1:]]; ok {
			return name, true
		}
	}
	if document.Name(key).Valid() {
		return document.Name(key), true
	}
	return "", false
}

func appendIssue(issues []document.Issue, issue document.Issue) []document.Issue {
	if len(issues) >= document.MaximumIssues {
		return issues
	}
	for _, existing := range issues {
		if existing == issue {
			return issues
		}
	}
	return append(issues, issue)
}

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
