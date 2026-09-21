// Package classification derives one bounded document type and issuing state
// from already-normalised assertions. Provider and model assertions take
// precedence over the machine-readable zone and the barcode, but a recognised
// disagreement is never resolved by guessing: it stays provisional with a
// stable reason code. Country mapping beyond the initial deployment set
// remains pack-gated.
package classification

import (
	"slices"
	"strings"

	"github.com/Mujhtech/idenqa/internal/document"
)

// Stable classification reason codes.
const (
	// ReasonConflict means recognised sources disagreed.
	ReasonConflict = "document_classification_conflict"
	// ReasonUnknown means no source asserted a document type.
	ReasonUnknown = "document_classification_unknown"
	// ReasonUnrecognised means a source asserted an unmappable document type.
	ReasonUnrecognised = "document_classification_unrecognized"
	// ReasonCountryUnknown means no source asserted an issuing state.
	ReasonCountryUnknown = "document_country_unknown"
)

// Assertion is one canonical document-type or issuing-state claim. Values are
// normalised before an assertion is constructed; unrecognised raw values are
// passed through unchanged so Derive can report them without guessing.
type Assertion struct {
	DocumentType string
	IssuingState string
	Source       document.Source
}

// Derive resolves type and issuing state in assertion order. It returns a
// provisional result whenever a value is unknown, unrecognised, or conflicting.
func Derive(assertions []Assertion) document.Classification {
	result := document.Classification{
		DocumentType: document.DocumentTypeUnknown,
		Status:       document.ClassificationProvisional,
		Reasons:      []string{},
	}
	typeValues := make([]string, 0, len(assertions))
	countryValues := make([]string, 0, len(assertions))
	unrecognised := false
	for _, assertion := range assertions {
		if assertion.DocumentType != "" {
			if canonical, ok := NormaliseDocumentType(assertion.DocumentType); ok {
				if !slices.Contains(typeValues, canonical) {
					typeValues = append(typeValues, canonical)
				}
			} else {
				unrecognised = true
			}
		}
		if assertion.IssuingState != "" {
			if canonical, ok := NormaliseCountry(assertion.IssuingState); ok && !slices.Contains(countryValues, canonical) {
				countryValues = append(countryValues, canonical)
			}
		}
	}
	switch {
	case len(typeValues) > 1:
		result.Reasons = append(result.Reasons, ReasonConflict)
	case len(typeValues) == 1:
		result.DocumentType = typeValues[0]
		if unrecognised {
			result.Reasons = append(result.Reasons, ReasonUnrecognised)
		}
	case unrecognised:
		result.Reasons = append(result.Reasons, ReasonUnrecognised)
	default:
		result.Reasons = append(result.Reasons, ReasonUnknown)
	}
	switch {
	case len(countryValues) > 1:
		if !slices.Contains(result.Reasons, ReasonConflict) {
			result.Reasons = append(result.Reasons, ReasonConflict)
		}
	case len(countryValues) == 1:
		result.IssuingState = countryValues[0]
	default:
		result.Reasons = append(result.Reasons, ReasonCountryUnknown)
	}
	if len(result.Reasons) == 0 {
		result.Status = document.ClassificationDefinitive
	}
	return result
}

// NormaliseDocumentType maps one raw type claim to the canonical closed set.
func NormaliseDocumentType(raw string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	key = strings.NewReplacer(" ", "_", "-", "_", "/", "_").Replace(key)
	if canonical, ok := typeAliases[key]; ok {
		return canonical, true
	}
	if document.ValidDocumentType(key) && key != document.DocumentTypeUnknown {
		return key, true
	}
	return "", false
}

// NormaliseCountry maps a two- or three-letter issuing-state claim to ISO 3166
// alpha-3. Only the initial deployment set is mapped; broader coverage is
// pack-gated.
func NormaliseCountry(raw string) (string, bool) {
	value := strings.ToUpper(strings.TrimSpace(raw))
	switch len(value) {
	case 3:
		return value, isoAlpha3(value)
	case 2:
		mapped, ok := alpha2ToAlpha3[value]
		return mapped, ok
	default:
		return "", false
	}
}

// TypeFromMRZCode maps an ICAO 9303 document code to a canonical type.
func TypeFromMRZCode(code string) string {
	value := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "<", ""))
	switch value {
	case "":
		return document.DocumentTypeUnknown
	case "IP", "P", "PP", "PC", "PO":
		return document.DocumentTypePassport
	case "ID", "I", "A", "C", "AC":
		return document.DocumentTypeNationalID
	default:
		return document.DocumentTypeUnknown
	}
}

// TypeFromAAMVADesignator maps one AAMVA subfile designator to a canonical type.
func TypeFromAAMVADesignator(designator string) string {
	switch strings.ToUpper(strings.TrimSpace(designator)) {
	case "DL":
		return document.DocumentTypeDriversLicense
	case "ID":
		return document.DocumentTypeNationalID
	case "P", "PC", "PP":
		return document.DocumentTypePassport
	default:
		return document.DocumentTypeUnknown
	}
}

var typeAliases = map[string]string{
	"passport":          document.DocumentTypePassport,
	"passport_book":     document.DocumentTypePassport,
	"passportbook":      document.DocumentTypePassport,
	"passport_card":     document.DocumentTypePassport,
	"passportcard":      document.DocumentTypePassport,
	"national_id":       document.DocumentTypeNationalID,
	"nationalid":        document.DocumentTypeNationalID,
	"national_identity": document.DocumentTypeNationalID,
	"id_card":           document.DocumentTypeNationalID,
	"idcard":            document.DocumentTypeNationalID,
	"identity_card":     document.DocumentTypeNationalID,
	"identitycard":      document.DocumentTypeNationalID,
	"id":                document.DocumentTypeNationalID,
	"nid":               document.DocumentTypeNationalID,
	"drivers_license":   document.DocumentTypeDriversLicense,
	"driver_license":    document.DocumentTypeDriversLicense,
	"driverslicence":    document.DocumentTypeDriversLicense,
	"driverlicence":     document.DocumentTypeDriversLicense,
	"drivers_licence":   document.DocumentTypeDriversLicense,
	"dl":                document.DocumentTypeDriversLicense,
	"residence_permit":  document.DocumentTypeResidencePermit,
	"residencepermit":   document.DocumentTypeResidencePermit,
	"permit":            document.DocumentTypeResidencePermit,
}

// The initial deployment set only; every other alpha-2 code remains pack-gated.
var alpha2ToAlpha3 = map[string]string{
	"NG": "NGA",
	"GH": "GHA",
	"KE": "KEN",
	"ZA": "ZAF",
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
