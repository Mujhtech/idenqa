// Package pack owns immutable country, document, jurisdiction, and assurance
// pack documents and the registry that manages their lifecycle.
//
// A pack records only metadata that is publicly verifiable — ISO 3166-1
// country codes, ICAO 9303 machine-readable-zone formats, evidence artefact
// names, and deterministic capabilities implemented inside Core — plus an
// explicit support level whose evidence is a bounded reason code or reference.
// It never asserts legal conclusions, provider approvals, evaluation results,
// or security-feature findings. Country authority, controller, processor,
// recipient, region, and transfer inputs are referenced as configurable
// requirement-slot keys and are resolved by tenant or deployment policy, never
// by the pack itself.
package pack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Mujhtech/idenqa/internal/document"
	"github.com/Mujhtech/idenqa/internal/evidence"
)

// Schema version of the closed canonical pack document.
const (
	// SchemaMajor is the only accepted canonical pack schema major version.
	SchemaMajor uint32 = 1
	// SchemaMinor is the only accepted canonical pack schema minor version.
	SchemaMinor uint32 = 0
)

// Document bounds keep every list and string fail-closed.
const (
	// MaximumDocuments bounds documents in one pack.
	MaximumDocuments = 8
	// MaximumKnownVersions bounds known document versions per document.
	MaximumKnownVersions = 16
	// MaximumSupportedFields bounds canonical fields per document.
	MaximumSupportedFields = 32
	// MaximumSecurityChecks bounds declared security-check capabilities.
	MaximumSecurityChecks = 16
	// MaximumDeclaredFormats bounds declared formats per declaration.
	MaximumDeclaredFormats = 8
	// MaximumRequirementReferences bounds model and parser references.
	MaximumRequirementReferences = 16
	// MaximumEvaluationReferences bounds evaluation-coverage references.
	MaximumEvaluationReferences = 16
	// MaximumLimitations bounds known limitations per document.
	MaximumLimitations = 32
	// MaximumEvidence bounds evidence reason codes per document.
	MaximumEvidence = 32
	// MaximumAssuranceMappings bounds assurance mappings per document.
	MaximumAssuranceMappings = 16
	// MaximumRequirementSlots bounds configurable requirement slots per pack.
	MaximumRequirementSlots = 16
	// MaximumSliceBytes bounds one canonical document.
	MaximumSliceBytes = 262144
)

// DefaultLegalReviewState is the fail-closed legal-review default.
const DefaultLegalReviewState = LegalReviewNotReviewed

// ErrInvalid means a pack value failed closed validation.
var ErrInvalid = errors.New("pack: invalid document")

// ErrNotFound means no registered pack, revision, or document matched.
var ErrNotFound = errors.New("pack: not found")

// ErrConflict means an expected-version or lifecycle transition conflicted.
var ErrConflict = errors.New("pack: conflict")

// LifecycleState is the registry lifecycle of one immutable pack revision.
type LifecycleState string

// Pack lifecycle states. Draft and active are registry-owned starting states;
// deprecation and retirement are recorded transitions.
const (
	LifecycleDraft      LifecycleState = "draft"
	LifecycleActive     LifecycleState = "active"
	LifecycleDeprecated LifecycleState = "deprecated"
	LifecycleRetired    LifecycleState = "retired"
)

// Valid reports whether the lifecycle state is part of the closed vocabulary.
func (state LifecycleState) Valid() bool {
	switch state {
	case LifecycleDraft, LifecycleActive, LifecycleDeprecated, LifecycleRetired:
		return true
	default:
		return false
	}
}

// LegalReviewState is the honest, bounded legal-review status of a pack.
type LegalReviewState string

// Legal review states. A pack never claims review without an opaque reference.
const (
	LegalReviewNotReviewed LegalReviewState = "not_reviewed"
	LegalReviewPending     LegalReviewState = "pending"
	LegalReviewReviewed    LegalReviewState = "reviewed"
)

// Valid reports whether the legal-review state is part of the closed vocabulary.
func (state LegalReviewState) Valid() bool {
	switch state {
	case LegalReviewNotReviewed, LegalReviewPending, LegalReviewReviewed:
		return true
	default:
		return false
	}
}

// LegalReview is an honest status with an optional opaque reference.
type LegalReview struct {
	State     LegalReviewState `json:"state"`
	Reference string           `json:"reference,omitempty"`
}

// DocumentType is the closed pack document-type vocabulary. It is deliberately
// smaller than the canonical analysis vocabulary: residence permits have no
// pack contract yet.
type DocumentType string

// Pack document types.
const (
	DocumentTypePassport      DocumentType = "passport"
	DocumentTypeNationalID    DocumentType = "national_id"
	DocumentTypeDriverLicence DocumentType = "driver_licence"
)

// Valid reports whether the type is part of the closed vocabulary.
func (value DocumentType) Valid() bool {
	switch value {
	case DocumentTypePassport, DocumentTypeNationalID, DocumentTypeDriverLicence:
		return true
	default:
		return false
	}
}

// DocumentTypeFromCanonical maps a canonical analysis document type onto the
// closed pack vocabulary. Unknown or packless canonical types report false.
func DocumentTypeFromCanonical(value string) (DocumentType, bool) {
	switch value {
	case string(DocumentTypePassport):
		return DocumentTypePassport, true
	case string(DocumentTypeNationalID):
		return DocumentTypeNationalID, true
	case document.DocumentTypeDriversLicense:
		return DocumentTypeDriverLicence, true
	default:
		return "", false
	}
}

// Side is one physical document side as captured by evidence artefacts.
type Side string

// Document sides.
const (
	SideFront Side = "front"
	SideBack  Side = "back"
)

// Valid reports whether the side is part of the closed vocabulary.
func (side Side) Valid() bool { return side == SideFront || side == SideBack }

// Artefact maps a side onto the deployed evidence artefact name.
func (side Side) Artefact() (evidence.Name, bool) {
	switch side {
	case SideFront:
		return evidence.ArtefactDocumentFront, true
	case SideBack:
		return evidence.ArtefactDocumentBack, true
	default:
		return "", false
	}
}

// SupportLevel is the honest capability classification of one document pack.
// It is not a legal or provider approval.
type SupportLevel string

// Support levels.
const (
	SupportFullySupported        SupportLevel = "fully_supported"
	SupportStructurallySupported SupportLevel = "structurally_supported"
	SupportProviderOnly          SupportLevel = "provider_only"
	SupportBestEffort            SupportLevel = "best_effort"
	SupportUnsupported           SupportLevel = "unsupported"
)

// Valid reports whether the support level is part of the closed vocabulary.
func (level SupportLevel) Valid() bool {
	switch level {
	case SupportFullySupported, SupportStructurallySupported, SupportProviderOnly, SupportBestEffort, SupportUnsupported:
		return true
	default:
		return false
	}
}

// DeclarationState is the bounded support declaration of one acquisition path.
type DeclarationState string

// Declaration states. Unknown is preferred over a fabricated negative.
const (
	DeclarationSupported   DeclarationState = "supported"
	DeclarationUnsupported DeclarationState = "unsupported"
	DeclarationUnknown     DeclarationState = "unknown"
)

// Valid reports whether the declaration state is part of the closed vocabulary.
func (state DeclarationState) Valid() bool {
	switch state {
	case DeclarationSupported, DeclarationUnsupported, DeclarationUnknown:
		return true
	default:
		return false
	}
}

// Declaration declares one barcode, MRZ, or NFC capability without asserting
// provider or legal approval.
type Declaration struct {
	State     DeclarationState `json:"state"`
	Formats   []string         `json:"formats"`
	Reference string           `json:"reference,omitempty"`
	Reason    string           `json:"reason,omitempty"`
}

// EvaluationState is the bounded evaluation-coverage state.
type EvaluationState string

// Evaluation states.
const (
	EvaluationNotEvaluated     EvaluationState = "not_evaluated"
	EvaluationPartiallyCovered EvaluationState = "partially_evaluated"
	EvaluationEvaluated        EvaluationState = "evaluated"
)

// Valid reports whether the evaluation state is part of the closed vocabulary.
func (state EvaluationState) Valid() bool {
	switch state {
	case EvaluationNotEvaluated, EvaluationPartiallyCovered, EvaluationEvaluated:
		return true
	default:
		return false
	}
}

// EvaluationCoverage records evaluation coverage and its opaque references.
type EvaluationCoverage struct {
	State      EvaluationState `json:"state"`
	References []string        `json:"references"`
}

// KnownVersion is one publicly referenced document version or format edition.
type KnownVersion struct {
	Version   string `json:"version"`
	Reference string `json:"reference"`
}

// RequirementReference names one parser or model requirement. It is a
// reference, never a claim that the referenced implementation was accepted.
type RequirementReference struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference"`
	Version   string `json:"version"`
}

// RequirementSlot names one configurable country or jurisdiction requirement
// input. The pack supplies a tenant-policy key, never a legal value.
type RequirementSlot struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// Closed requirement-slot names. They mirror the architecture's configurable
// authority, controller, processor, recipient, region, and transfer inputs.
const (
	SlotAuthority  = "authority"
	SlotController = "controller"
	SlotProcessor  = "processor"
	SlotRecipient  = "recipient"
	SlotRegion     = "region"
	SlotTransfer   = "transfer"
)

// AssuranceMappingState is the bounded assurance-mapping status.
type AssuranceMappingState string

// Assurance mapping states.
const (
	AssuranceMapped    AssuranceMappingState = "mapped"
	AssuranceNotMapped AssuranceMappingState = "not_mapped"
)

// Valid reports whether the mapping state is part of the closed vocabulary.
func (state AssuranceMappingState) Valid() bool {
	return state == AssuranceMapped || state == AssuranceNotMapped
}

// AssuranceMapping references one existing platform assurance capability or
// records that no pack-specific mapping exists. It never approves a provider.
type AssuranceMapping struct {
	Capability string                `json:"capability,omitempty"`
	State      AssuranceMappingState `json:"state"`
	Reason     string                `json:"reason,omitempty"`
}

// platformAssuranceCapabilities mirrors policy.AssuranceCapabilities names so a
// reviewed pack cannot invent a capability meaning. A package test asserts the
// mirror stays exact.
var platformAssuranceCapabilities = []string{
	"idenqa.assurance.authority_record",
	"idenqa.assurance.document_authenticity",
	"idenqa.assurance.capture_quality",
	"idenqa.assurance.face_match_1to1",
	"idenqa.assurance.passive_liveness",
	"idenqa.assurance.active_liveness",
	"idenqa.assurance.freshness",
	"idenqa.assurance.live_capture",
	"idenqa.assurance.capture_integrity",
	"idenqa.assurance.human_review",
	"idenqa.assurance.identity_corroboration",
	"idenqa.assurance.fraud_controls",
}

// platformSecurityChecks is the closed vocabulary of deterministic checks Core
// implements. It deliberately excludes anti-tamper or template inspection,
// which remain provider- or model-gated.
var platformSecurityChecks = []string{
	"core.barcode_structure",
	"core.cross_field_consistency",
	"core.front_back_consistency",
	"core.mrz_check_digits",
}

// Document is one immutable document entry inside a pack.
type Document struct {
	Type                       DocumentType           `json:"type"`
	KnownVersions              []KnownVersion         `json:"known_versions"`
	RequiredSides              []Side                 `json:"required_sides"`
	SupportedFields            []document.Name        `json:"supported_fields"`
	SecurityChecks             []string               `json:"security_checks"`
	Barcode                    Declaration            `json:"barcode"`
	MRZ                        Declaration            `json:"mrz"`
	NFC                        Declaration            `json:"nfc"`
	ModelAndParserRequirements []RequirementReference `json:"model_and_parser_requirements"`
	EvaluationCoverage         EvaluationCoverage     `json:"evaluation_coverage"`
	SupportLevel               SupportLevel           `json:"support_level"`
	KnownLimitations           []string               `json:"known_limitations"`
	Evidence                   []string               `json:"evidence"`
	AssuranceMappings          []AssuranceMapping     `json:"assurance_mappings"`
}

// Pack is one immutable, versioned country documentary pack.
//
// Lifecycle is the as-published starting lifecycle and is covered by the
// content digest. Registry transitions never rewrite it: the registry records
// current state separately so a digest always identifies exact immutable
// content.
type Pack struct {
	SchemaMajor      uint32            `json:"schema_major"`
	SchemaMinor      uint32            `json:"schema_minor"`
	Country          string            `json:"country"`
	CountryAlpha3    string            `json:"country_alpha3"`
	Authority        string            `json:"authority"`
	Revision         uint32            `json:"revision"`
	PublishedAt      time.Time         `json:"published_at"`
	Digest           string            `json:"digest"`
	Lifecycle        LifecycleState    `json:"lifecycle_state"`
	LegalReview      LegalReview       `json:"legal_review"`
	RequirementSlots []RequirementSlot `json:"requirement_slots"`
	Documents        []Document        `json:"documents"`
}

// Clone returns a deep copy that shares no backing slice with the receiver.
func (value Pack) Clone() Pack {
	cloned := value
	cloned.RequirementSlots = slices.Clone(value.RequirementSlots)
	cloned.Documents = make([]Document, len(value.Documents))
	for index, entry := range value.Documents {
		cloned.Documents[index] = entry.clone()
	}
	return cloned
}

func (entry Document) clone() Document {
	cloned := entry
	cloned.KnownVersions = slices.Clone(entry.KnownVersions)
	cloned.RequiredSides = slices.Clone(entry.RequiredSides)
	cloned.SupportedFields = slices.Clone(entry.SupportedFields)
	cloned.SecurityChecks = slices.Clone(entry.SecurityChecks)
	cloned.Barcode = entry.Barcode.clone()
	cloned.MRZ = entry.MRZ.clone()
	cloned.NFC = entry.NFC.clone()
	cloned.ModelAndParserRequirements = slices.Clone(entry.ModelAndParserRequirements)
	cloned.EvaluationCoverage.References = slices.Clone(entry.EvaluationCoverage.References)
	cloned.KnownLimitations = slices.Clone(entry.KnownLimitations)
	cloned.Evidence = slices.Clone(entry.Evidence)
	cloned.AssuranceMappings = slices.Clone(entry.AssuranceMappings)
	return cloned
}

func (value Declaration) clone() Declaration {
	value.Formats = slices.Clone(value.Formats)
	return value
}

// Document returns the exact document entry for the type, when present.
func (value Pack) Document(documentType DocumentType) (Document, bool) {
	for _, entry := range value.Documents {
		if entry.Type == documentType {
			return entry.clone(), true
		}
	}
	return Document{}, false
}

// Canonicalize validates a pack document, normalises non-nil sorted slices,
// and returns the value with its content digest set. It fails closed on any
// unbounded, unsorted, duplicated, or contradictory content.
func Canonicalize(value Pack) (Pack, error) {
	canonical := value.Clone()
	canonical.groom()
	digest, err := canonical.computeDigest()
	if err != nil {
		return Pack{}, err
	}
	canonical.Digest = digest
	if err := canonical.Validate(); err != nil {
		return Pack{}, err
	}
	return canonical, nil
}

// Validate fails closed on any invalid canonical pack content.
func (value Pack) Validate() error {
	if value.SchemaMajor != SchemaMajor || value.SchemaMinor != SchemaMinor {
		return fmt.Errorf("%w: schema version", ErrInvalid)
	}
	alpha3, ok := iso3166Alpha3[value.Country]
	if !ok || alpha3 != value.CountryAlpha3 {
		return fmt.Errorf("%w: country", ErrInvalid)
	}
	if !validToken(value.Authority, 100) || value.Revision == 0 || value.Digest == "" {
		return fmt.Errorf("%w: identity", ErrInvalid)
	}
	if len(value.Digest) != 64 || !isHex(value.Digest) {
		return fmt.Errorf("%w: digest", ErrInvalid)
	}
	if !validUTC(value.PublishedAt) || !value.Lifecycle.Valid() {
		return fmt.Errorf("%w: publication", ErrInvalid)
	}
	if !value.legalReviewValid() {
		return fmt.Errorf("%w: legal review", ErrInvalid)
	}
	if len(value.Documents) == 0 || len(value.Documents) > MaximumDocuments {
		return fmt.Errorf("%w: documents", ErrInvalid)
	}
	if !validRequirementSlots(value.RequirementSlots) {
		return fmt.Errorf("%w: requirement slots", ErrInvalid)
	}
	previous := DocumentType("")
	for _, entry := range value.Documents {
		if !entry.Type.Valid() || entry.Type <= previous {
			return fmt.Errorf("%w: document type", ErrInvalid)
		}
		previous = entry.Type
		if err := entry.validate(); err != nil {
			return err
		}
	}
	computed, err := value.computeDigest()
	if err != nil || computed != value.Digest {
		return fmt.Errorf("%w: content digest", ErrInvalid)
	}
	return nil
}

// legalReviewValid requires a reviewed claim to carry an opaque reference and
// leaves pending and not_reviewed references optional.
func (value Pack) legalReviewValid() bool {
	if !value.LegalReview.State.Valid() || !optionalReference(value.LegalReview.Reference) {
		return false
	}
	return value.LegalReview.State != LegalReviewReviewed || value.LegalReview.Reference != ""
}

func (entry Document) validate() error {
	if len(entry.KnownVersions) > MaximumKnownVersions || len(entry.RequiredSides) > 2 ||
		len(entry.SupportedFields) > MaximumSupportedFields || len(entry.SecurityChecks) > MaximumSecurityChecks ||
		len(entry.ModelAndParserRequirements) > MaximumRequirementReferences ||
		len(entry.EvaluationCoverage.References) > MaximumEvaluationReferences ||
		len(entry.KnownLimitations) > MaximumLimitations || len(entry.Evidence) > MaximumEvidence ||
		len(entry.AssuranceMappings) > MaximumAssuranceMappings {
		return fmt.Errorf("%w: document bounds", ErrInvalid)
	}
	if !entry.SupportLevel.Valid() {
		return fmt.Errorf("%w: support level", ErrInvalid)
	}
	if !sortedUniqueTokens(entry.KnownLimitations, 100) || !sortedUniqueTokens(entry.Evidence, 100) ||
		!validSecurityChecks(entry.SecurityChecks) || !validDeclaredFormats(entry) {
		return fmt.Errorf("%w: document vocabulary", ErrInvalid)
	}
	if !validSides(entry.RequiredSides) || !validFields(entry.SupportedFields) ||
		!validKnownVersions(entry.KnownVersions) || !validRequirements(entry.ModelAndParserRequirements) ||
		!validEvaluation(entry.EvaluationCoverage) || !validAssuranceMappings(entry.AssuranceMappings) {
		return fmt.Errorf("%w: document structure", ErrInvalid)
	}
	if entry.MRZ.State == DeclarationSupported {
		for _, format := range entry.MRZ.Formats {
			switch format {
			case "td1", "td2", "td3":
			default:
				return fmt.Errorf("%w: mrz format", ErrInvalid)
			}
		}
	}
	switch entry.SupportLevel {
	case SupportUnsupported:
		if len(entry.KnownVersions) != 0 || len(entry.RequiredSides) != 0 || len(entry.SupportedFields) != 0 ||
			len(entry.SecurityChecks) != 0 || len(entry.ModelAndParserRequirements) != 0 ||
			len(entry.Evidence) != 0 || entry.EvaluationCoverage.State != EvaluationNotEvaluated ||
			entry.Barcode.State == DeclarationSupported || entry.MRZ.State == DeclarationSupported ||
			entry.NFC.State == DeclarationSupported {
			return fmt.Errorf("%w: unsupported document declares support", ErrInvalid)
		}
		if len(entry.KnownLimitations) == 0 {
			return fmt.Errorf("%w: unsupported document limitation", ErrInvalid)
		}
	case SupportFullySupported:
		if entry.EvaluationCoverage.State != EvaluationEvaluated || len(entry.Evidence) == 0 || len(entry.RequiredSides) == 0 {
			return fmt.Errorf("%w: fully supported evidence", ErrInvalid)
		}
	default:
		if entry.EvaluationCoverage.State == EvaluationEvaluated || len(entry.Evidence) == 0 || len(entry.RequiredSides) == 0 {
			return fmt.Errorf("%w: partial support evidence", ErrInvalid)
		}
	}
	return nil
}

func validSecurityChecks(checks []string) bool {
	for index, check := range checks {
		if !slices.Contains(platformSecurityChecks, check) || (index > 0 && check <= checks[index-1]) {
			return false
		}
	}
	return true
}

func validDeclaredFormats(entry Document) bool {
	for _, declaration := range []Declaration{entry.Barcode, entry.MRZ, entry.NFC} {
		if !declaration.State.Valid() || !sortedUniqueTokens(declaration.Formats, 32) ||
			!optionalToken(declaration.Reason, 100) || !optionalReference(declaration.Reference) {
			return false
		}
		if declaration.State == DeclarationSupported && len(declaration.Formats) == 0 {
			return false
		}
		if declaration.State != DeclarationSupported && len(declaration.Formats) != 0 {
			return false
		}
	}
	return true
}

func validSides(sides []Side) bool {
	for index, side := range sides {
		if !side.Valid() || (index > 0 && side <= sides[index-1]) {
			return false
		}
	}
	return true
}

func validFields(fields []document.Name) bool {
	for index, name := range fields {
		if !name.Valid() || (index > 0 && name <= fields[index-1]) {
			return false
		}
	}
	return true
}

func validKnownVersions(versions []KnownVersion) bool {
	for index, version := range versions {
		if !validToken(version.Version, 100) || !validReference(version.Reference) {
			return false
		}
		if index > 0 && version.Version <= versions[index-1].Version {
			return false
		}
	}
	return true
}

func validRequirements(requirements []RequirementReference) bool {
	for index, requirement := range requirements {
		if requirement.Kind != "parser" && requirement.Kind != "model" {
			return false
		}
		if !validToken(requirement.Reference, 100) || !validToken(requirement.Version, 32) {
			return false
		}
		if index > 0 {
			previous := requirements[index-1]
			if requirement.Kind < previous.Kind ||
				(requirement.Kind == previous.Kind && requirement.Reference <= previous.Reference) {
				return false
			}
		}
	}
	return true
}

func validEvaluation(coverage EvaluationCoverage) bool {
	if !coverage.State.Valid() || !sortedUniqueTokens(coverage.References, 100) {
		return false
	}
	if coverage.State == EvaluationNotEvaluated {
		return len(coverage.References) == 0
	}
	return len(coverage.References) > 0
}

func validAssuranceMappings(mappings []AssuranceMapping) bool {
	for index, mapping := range mappings {
		if !mapping.State.Valid() {
			return false
		}
		switch mapping.State {
		case AssuranceMapped:
			if !slices.Contains(platformAssuranceCapabilities, mapping.Capability) || mapping.Reason != "" {
				return false
			}
		case AssuranceNotMapped:
			if mapping.Capability != "" || !validToken(mapping.Reason, 100) {
				return false
			}
		}
		if index > 0 && mapping.Capability == "" && mappings[index-1].Capability == "" &&
			mapping.Reason <= mappings[index-1].Reason {
			return false
		}
		if index > 0 && mapping.Capability != "" && mapping.Capability <= mappings[index-1].Capability {
			return false
		}
	}
	return true
}

func validRequirementSlots(slots []RequirementSlot) bool {
	if len(slots) > MaximumRequirementSlots {
		return false
	}
	seen := make(map[string]struct{}, len(slots))
	for _, slot := range slots {
		if !validSlotName(slot.Name) || !validToken(slot.Key, 100) {
			return false
		}
		if _, exists := seen[slot.Name]; exists {
			return false
		}
		seen[slot.Name] = struct{}{}
	}
	return len(slots) > 0
}

func validSlotName(value string) bool {
	switch value {
	case SlotAuthority, SlotController, SlotProcessor, SlotRecipient, SlotRegion, SlotTransfer:
		return true
	default:
		return false
	}
}

// Parse decodes one strict canonical JSON pack document. Unknown fields,
// duplicate content, a missing or mismatched digest, and non-canonical
// content fail closed.
func Parse(input []byte) (Pack, error) {
	if len(input) == 0 || len(input) > MaximumSliceBytes || !utf8.Valid(input) {
		return Pack{}, fmt.Errorf("%w: encoding", ErrInvalid)
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.DisallowUnknownFields()
	var value Pack
	if err := decoder.Decode(&value); err != nil {
		return Pack{}, fmt.Errorf("%w: decode", ErrInvalid)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Pack{}, fmt.Errorf("%w: trailing content", ErrInvalid)
	}
	canonical, err := Canonicalize(value)
	if err != nil {
		return Pack{}, err
	}
	if canonical.Digest != value.Digest {
		return Pack{}, fmt.Errorf("%w: content digest", ErrInvalid)
	}
	return canonical, nil
}

// CanonicalJSON returns deterministic bytes for distribution, pinning, and
// digest verification. All slices are non-nil and canonically ordered.
func (value Pack) CanonicalJSON() ([]byte, error) {
	canonical := value.Clone()
	canonical.groom()
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("pack: serialise canonical document: %w", err)
	}
	return encoded, nil
}

// groom normalises nil slices to empty slices and sorts every list into the
// canonical order. It does not validate.
func (value *Pack) groom() {
	if value.RequirementSlots == nil {
		value.RequirementSlots = []RequirementSlot{}
	}
	slices.SortFunc(value.RequirementSlots, func(left, right RequirementSlot) int {
		return strings.Compare(left.Name, right.Name)
	})
	if value.Documents == nil {
		value.Documents = []Document{}
	}
	slices.SortFunc(value.Documents, func(left, right Document) int {
		return strings.Compare(string(left.Type), string(right.Type))
	})
	for index := range value.Documents {
		entry := &value.Documents[index]
		entry.KnownVersions = nonNil(entry.KnownVersions)
		slices.SortFunc(entry.KnownVersions, func(left, right KnownVersion) int {
			return strings.Compare(left.Version, right.Version)
		})
		entry.RequiredSides = nonNil(entry.RequiredSides)
		slices.Sort(entry.RequiredSides)
		entry.SupportedFields = nonNil(entry.SupportedFields)
		slices.Sort(entry.SupportedFields)
		entry.SecurityChecks = nonNil(entry.SecurityChecks)
		slices.Sort(entry.SecurityChecks)
		entry.Barcode = groomDeclaration(entry.Barcode)
		entry.MRZ = groomDeclaration(entry.MRZ)
		entry.NFC = groomDeclaration(entry.NFC)
		entry.ModelAndParserRequirements = nonNil(entry.ModelAndParserRequirements)
		slices.SortFunc(entry.ModelAndParserRequirements, func(left, right RequirementReference) int {
			if left.Kind != right.Kind {
				return strings.Compare(left.Kind, right.Kind)
			}
			return strings.Compare(left.Reference, right.Reference)
		})
		entry.EvaluationCoverage.References = nonNil(entry.EvaluationCoverage.References)
		slices.Sort(entry.EvaluationCoverage.References)
		entry.KnownLimitations = nonNil(entry.KnownLimitations)
		slices.Sort(entry.KnownLimitations)
		entry.Evidence = nonNil(entry.Evidence)
		slices.Sort(entry.Evidence)
		entry.AssuranceMappings = nonNil(entry.AssuranceMappings)
		slices.SortFunc(entry.AssuranceMappings, func(left, right AssuranceMapping) int {
			if left.Capability != right.Capability {
				return strings.Compare(left.Capability, right.Capability)
			}
			if left.Reason != right.Reason {
				return strings.Compare(left.Reason, right.Reason)
			}
			return strings.Compare(string(left.State), string(right.State))
		})
	}
}

func groomDeclaration(value Declaration) Declaration {
	value.Formats = nonNil(value.Formats)
	slices.Sort(value.Formats)
	return value
}

func nonNil[T any](values []T) []T {
	if values == nil {
		return []T{}
	}
	return values
}

// computeDigest hashes the canonical content with the digest field cleared.
func (value Pack) computeDigest() (string, error) {
	content := value.Clone()
	content.Digest = ""
	encoded, err := content.CanonicalJSON()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// NormalizeCountry accepts an ISO 3166-1 alpha-2 or alpha-3 code in any case
// and returns the canonical alpha-2 and alpha-3 pair.
func NormalizeCountry(value string) (string, string, bool) {
	trimmed := strings.ToUpper(strings.TrimSpace(value))
	if len(trimmed) == 2 {
		alpha3, ok := iso3166Alpha3[trimmed]
		return trimmed, alpha3, ok
	}
	if len(trimmed) == 3 {
		for alpha2, alpha3 := range iso3166Alpha3 {
			if alpha3 == trimmed {
				return alpha2, alpha3, true
			}
		}
	}
	return "", "", false
}

func validToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z':
		case index > 0 && character >= '0' && character <= '9':
		case index > 0 && (character == '.' || character == '_' || character == '-'):
		default:
			return false
		}
	}
	return true
}

func optionalToken(value string, maximum int) bool { return value == "" || validToken(value, maximum) }

func validReference(value string) bool {
	return value != "" && len(value) <= 256 && utf8.ValidString(value) && printable(value)
}

func optionalReference(value string) bool { return value == "" || validReference(value) }

func printable(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func sortedUniqueTokens(values []string, maximum int) bool {
	for index, value := range values {
		if !validToken(value, maximum) || (index > 0 && value <= values[index-1]) {
			return false
		}
	}
	return true
}

func validUTC(value time.Time) bool {
	return !value.IsZero() && value.Location() == time.UTC
}

func isHex(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}
