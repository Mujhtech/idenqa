// Package document owns deterministic, dependency-free document understanding
// built from data already available inside Core: machine-readable zones,
// already-decoded barcode payloads, provider or model extracted fields, and
// capture-plan metadata. It never decodes images, runs OCR, inspects security
// features, or asserts authenticity; those remain provider- or model-gated.
// Signals and findings derived from this package carry bounded reason codes
// only, never raw document text, MRZ lines, barcode payloads, or field values.
package document

import (
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"
)

// Bounded canonical shapes. Every producer must fail closed rather than
// accept an unbounded field, issue, or reason list.
const (
	// MaximumFields bounds canonical fields in one analysis.
	MaximumFields = 32
	// MaximumIssues bounds parser and merge issues in one analysis.
	MaximumIssues = 32
	// MaximumFieldLength bounds one canonical field value.
	MaximumFieldLength = 256
	// MaximumReasonCodes bounds one classification or signal reason list.
	MaximumReasonCodes = 16
	// MaximumPlanSides bounds capture-plan side metadata.
	MaximumPlanSides = 2
	// MaximumBarcodeFormats bounds decoded payload format identifiers.
	MaximumBarcodeFormats = 8
)

// ErrInvalid means a canonical document value failed closed validation.
var ErrInvalid = errors.New("document: invalid analysis")

// Side identifies one physical side of a document.
type Side string

const (
	// SideFront is the primary document side.
	SideFront Side = "front"
	// SideBack is the secondary document side.
	SideBack Side = "back"
)

// Valid reports whether the side is part of the closed v1 vocabulary.
func (side Side) Valid() bool { return side == SideFront || side == SideBack }

// String returns the wire form of the side.
func (side Side) String() string { return string(side) }

// Source identifies where a canonical value came from.
type Source string

const (
	// SourceProvider is a provider-extracted field.
	SourceProvider Source = "provider"
	// SourceModel is a model-extracted field.
	SourceModel Source = "model"
	// SourceMRZ is a machine-readable zone field.
	SourceMRZ Source = "mrz"
	// SourceBarcode is an already-decoded barcode payload field.
	SourceBarcode Source = "barcode"
)

// Valid reports whether the source is part of the closed v1 vocabulary.
func (source Source) Valid() bool {
	switch source {
	case SourceProvider, SourceModel, SourceMRZ, SourceBarcode:
		return true
	default:
		return false
	}
}

// Agreement states whether independent sources supplied the same canonical
// value. Agreement is computed only between exact canonical values; no fuzzy
// matching is applied.
type Agreement string

const (
	// AgreementSingle means exactly one source supplied the field.
	AgreementSingle Agreement = "single"
	// AgreementAgreed means multiple sources supplied the same canonical value.
	AgreementAgreed Agreement = "agreed"
	// AgreementConflicted means sources supplied different canonical values.
	AgreementConflicted Agreement = "conflicted"
)

// Valid reports whether the agreement state is part of the closed vocabulary.
func (agreement Agreement) Valid() bool {
	switch agreement {
	case AgreementSingle, AgreementAgreed, AgreementConflicted:
		return true
	default:
		return false
	}
}

// Name is one canonical identity field name.
type Name string

const (
	// NameDocumentNumber is the document or card number.
	NameDocumentNumber Name = "document_number"
	// NameDocumentType is the canonical document type.
	NameDocumentType Name = "document_type"
	// NameIssuingState is the issuing state or country code.
	NameIssuingState Name = "issuing_state"
	// NameNationality is the declared nationality or citizenship code.
	NameNationality Name = "nationality"
	// NameFirstName is the given-name part.
	NameFirstName Name = "first_name"
	// NameLastName is the family-name part.
	NameLastName Name = "last_name"
	// NameOtherNames carries additional declared names beyond first and last.
	NameOtherNames Name = "other_names"
	// NameDateOfBirth is the resolved birth date.
	NameDateOfBirth Name = "date_of_birth"
	// NameDateOfExpiry is the resolved expiry date.
	NameDateOfExpiry Name = "date_of_expiry"
	// NameDateOfIssue is the resolved issue date.
	NameDateOfIssue Name = "date_of_issue"
	// NameSex is the declared sex.
	NameSex Name = "sex"
	// NamePersonalNumber is an optional personal or national number.
	NamePersonalNumber Name = "personal_number"
)

// Valid reports whether the name is part of the closed v1 vocabulary.
func (name Name) Valid() bool {
	switch name {
	case NameDocumentNumber, NameDocumentType, NameIssuingState, NameNationality,
		NameFirstName, NameLastName, NameOtherNames, NameDateOfBirth, NameDateOfExpiry,
		NameDateOfIssue, NameSex, NamePersonalNumber:
		return true
	default:
		return false
	}
}

// Field is one canonical value with bounded provenance and agreement state.
type Field struct {
	Name      Name
	Value     string
	Source    Source
	Sources   []Source
	Agreement Agreement
}

// HasSource reports whether the field was supplied by an exact source.
func (field Field) HasSource(source Source) bool { return slices.Contains(field.Sources, source) }

// Issue is one bounded parser or merge finding. It never carries a raw value.
type Issue struct {
	Code   string
	Field  string
	Source Source
}

// MRZ summarises one parsed machine-readable zone without retaining it.
type MRZ struct {
	Format string
	Valid  bool
	Issues []Issue
}

// Classification states document type and issuing state with an explicit
// definitive or provisional status.
type Classification struct {
	DocumentType string
	IssuingState string
	Status       string
	Reasons      []string
}

// Classification statuses form the closed v1 vocabulary.
const (
	// ClassificationDefinitive means sources agreed on a known type and state.
	ClassificationDefinitive = "definitive"
	// ClassificationProvisional means unknown, conflicting, or unrecognised input.
	ClassificationProvisional = "provisional"
)

// Canonical document types form the closed v1 vocabulary. Country-specific
// document packs may refine these later; this layer never guesses.
const (
	// DocumentTypeUnknown means no source established a known type.
	DocumentTypeUnknown = "unknown"
	// DocumentTypePassport is a passport booklet or passport card.
	DocumentTypePassport = "passport"
	// DocumentTypeNationalID is a national identity card.
	DocumentTypeNationalID = "national_id"
	// DocumentTypeDriversLicense is a driving licence.
	DocumentTypeDriversLicense = "drivers_license"
	// DocumentTypeResidencePermit is a residence permit.
	DocumentTypeResidencePermit = "residence_permit"
)

// ValidDocumentType reports whether the value is a canonical v1 document type.
func ValidDocumentType(value string) bool {
	switch value {
	case DocumentTypeUnknown, DocumentTypePassport, DocumentTypeNationalID,
		DocumentTypeDriversLicense, DocumentTypeResidencePermit:
		return true
	default:
		return false
	}
}

// Analysis is one bounded, canonical understanding of a single document side.
// It is an in-memory domain value and must never be logged or persisted whole.
type Analysis struct {
	Side           Side
	Fields         []Field
	MRZ            *MRZ
	Barcodes       []string
	PlanSides      []Side
	Classification Classification
	Issues         []Issue
}

// Field returns the canonical field with the exact name, when present.
func (analysis Analysis) Field(name Name) (Field, bool) {
	for _, field := range analysis.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return Field{}, false
}

// Valid reports whether the analysis contains no closed-vocabulary issues.
func (analysis Analysis) Valid() bool { return analysis.Validate() == nil }

// Validate fails closed on any unbounded, unsorted, or unknown content.
func (analysis Analysis) Validate() error {
	if !analysis.Side.Valid() {
		return fmt.Errorf("%w: side", ErrInvalid)
	}
	if len(analysis.Fields) > MaximumFields || len(analysis.Issues) > MaximumIssues ||
		len(analysis.Barcodes) > MaximumBarcodeFormats || len(analysis.PlanSides) > MaximumPlanSides {
		return fmt.Errorf("%w: bounds", ErrInvalid)
	}
	previous := Name("")
	for _, field := range analysis.Fields {
		if !field.Name.Valid() || field.Name <= previous || field.Value == "" ||
			len(field.Value) > MaximumFieldLength || !printable(field.Value) ||
			!field.Source.Valid() || !field.Agreement.Valid() || len(field.Sources) == 0 {
			return fmt.Errorf("%w: field", ErrInvalid)
		}
		previous = field.Name
		if !sortedUniqueSources(field.Sources) {
			return fmt.Errorf("%w: field sources", ErrInvalid)
		}
	}
	if analysis.MRZ != nil {
		if analysis.MRZ.Format == "" || len(analysis.MRZ.Issues) > MaximumIssues || !validIssues(analysis.MRZ.Issues) {
			return fmt.Errorf("%w: mrz summary", ErrInvalid)
		}
	}
	for _, format := range analysis.Barcodes {
		if !safeToken(format, 32) {
			return fmt.Errorf("%w: barcode format", ErrInvalid)
		}
	}
	if !sortedUniqueSides(analysis.PlanSides) {
		return fmt.Errorf("%w: plan sides", ErrInvalid)
	}
	if !validClassification(analysis.Classification) || !validIssues(analysis.Issues) {
		return fmt.Errorf("%w: classification or issues", ErrInvalid)
	}
	return nil
}

func validClassification(classification Classification) bool {
	if classification.Status != ClassificationDefinitive && classification.Status != ClassificationProvisional {
		return false
	}
	if !ValidDocumentType(classification.DocumentType) {
		return false
	}
	if classification.IssuingState != "" && !isoAlpha3(classification.IssuingState) {
		return false
	}
	if len(classification.Reasons) > MaximumReasonCodes {
		return false
	}
	for _, reason := range classification.Reasons {
		if !safeToken(reason, 100) {
			return false
		}
	}
	return true
}

func validIssues(issues []Issue) bool {
	seen := make(map[[3]string]struct{}, len(issues))
	for _, issue := range issues {
		key := [3]string{issue.Code, issue.Field, string(issue.Source)}
		if _, exists := seen[key]; exists {
			return false
		}
		seen[key] = struct{}{}
		if !safeToken(issue.Code, 64) || (issue.Field != "" && !safeToken(issue.Field, 64)) ||
			(issue.Source != "" && !issue.Source.Valid()) {
			return false
		}
	}
	return true
}

func sortedUniqueSources(sources []Source) bool {
	for index, source := range sources {
		if !source.Valid() || (index > 0 && source <= sources[index-1]) {
			return false
		}
	}
	return true
}

func sortedUniqueSides(sides []Side) bool {
	for index, side := range sides {
		if !side.Valid() || (index > 0 && side <= sides[index-1]) {
			return false
		}
	}
	return true
}

func isoAlpha3(value string) bool {
	if len(value) != 3 {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 'A' || value[index] > 'Z' {
			return false
		}
	}
	return true
}

func printable(value string) bool {
	if !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func safeToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}
