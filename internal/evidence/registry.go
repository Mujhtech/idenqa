// Package evidence owns evidence, artefact, acquisition-method, assurance,
// purpose, constraint, and capture-capability vocabulary.
package evidence

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const (
	// RegistrySchemaVersion is the canonical registry document version.
	RegistrySchemaVersion uint32 = 1
	reservedNamespace            = "idenqa"
)

// Kind identifies a registry vocabulary category.
type Kind string

const (
	// KindEvidence identifies evidence-type definitions.
	KindEvidence Kind = "evidence"
	// KindArtefact identifies evidence-component definitions.
	KindArtefact Kind = "artefact"
	// KindMethod identifies acquisition-method definitions.
	KindMethod Kind = "method"
	// KindPurpose identifies processing-purpose definitions.
	KindPurpose Kind = "purpose"
	// KindAssurance identifies assurance-property definitions.
	KindAssurance Kind = "assurance"
	// KindConstraint identifies profile-constraint definitions.
	KindConstraint Kind = "constraint"
)

// Name is an owner-namespaced registry name, such as
// idenqa.evidence.selfie_image or com.example.method.document_scanner.
type Name string

// ParseName validates a name for the expected vocabulary kind. Idenqa owns
// the two-segment idenqa.<kind> namespace; extensions require an owner
// namespace containing at least two segments before the kind.
func ParseName(kind Kind, value string) (Name, error) {
	if !kind.valid() {
		return "", fmt.Errorf("evidence registry kind %q is invalid", kind)
	}
	parts := strings.Split(value, ".")
	if len(parts) < 3 || parts[len(parts)-2] != string(kind) {
		return "", fmt.Errorf("evidence registry name %q is not a %s name", value, kind)
	}
	if parts[0] == reservedNamespace {
		if len(parts) != 3 {
			return "", fmt.Errorf("reserved evidence registry name %q has an invalid namespace", value)
		}
	} else if len(parts) < 4 {
		return "", fmt.Errorf("extension evidence registry name %q requires an owner namespace", value)
	}
	for _, part := range parts {
		if !validSegment(part) {
			return "", fmt.Errorf("evidence registry name %q contains an invalid segment", value)
		}
	}

	return Name(value), nil
}

func (kind Kind) valid() bool {
	switch kind {
	case KindEvidence, KindArtefact, KindMethod, KindPurpose, KindAssurance, KindConstraint:
		return true
	default:
		return false
	}
}

func validSegment(value string) bool {
	if value == "" || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for index := 1; index < len(value); index++ {
		character := value[index]
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}

	return true
}

// ValueKind identifies the bounded value shape accepted by a constraint.
// Richer shapes require a future schema version rather than arbitrary JSON.
type ValueKind string

const (
	// ValueString is a scalar string constraint value.
	ValueString ValueKind = "string"
	// ValueInteger is a scalar integer constraint value.
	ValueInteger ValueKind = "integer"
	// ValueBoolean is a scalar boolean constraint value.
	ValueBoolean ValueKind = "boolean"
	// ValueStringList is an unordered string-set constraint value.
	ValueStringList ValueKind = "string_list"
)

// Type defines the artefacts that make up one evidence type.
type Type struct {
	Name      Name
	Artefacts []Name
}

// MethodSupport declares what one acquisition method can produce and the
// maximum acquisition assurance it can establish. It does not prove that an
// individual capture achieved those assurances.
type MethodSupport struct {
	EvidenceType Name
	Artefacts    []Name
	Assurances   []Name
}

// AcquisitionMethod defines the evidence supported by one capture method.
type AcquisitionMethod struct {
	Name     Name
	Supports []MethodSupport
}

// ConstraintDefinition declares a constraint's value shape and the evidence
// types to which it may be applied. An empty AppliesTo list permits all types.
type ConstraintDefinition struct {
	Name      Name
	ValueKind ValueKind
	AppliesTo []Name
}

// Definitions is the complete input for one immutable registry revision.
type Definitions struct {
	Artefacts   []Name
	Evidence    []Type
	Methods     []AcquisitionMethod
	Purposes    []Name
	Assurances  []Name
	Constraints []ConstraintDefinition
}

// Reference pins a profile to an immutable registry revision.
type Reference struct {
	SchemaVersion uint32 `json:"schema_version"`
	Revision      uint32 `json:"revision"`
	Digest        string `json:"digest"`
}

// Registry is an immutable, validated vocabulary revision.
type Registry struct {
	revision    uint32
	digest      string
	artefacts   map[Name]struct{}
	evidence    map[Name]Type
	methods     map[Name]AcquisitionMethod
	purposes    map[Name]struct{}
	assurances  map[Name]struct{}
	constraints map[Name]ConstraintDefinition
}

// NewRegistry validates and freezes a registry revision. Names are immutable:
// changed semantics require a new name or a new registry revision and digest.
func NewRegistry(revision uint32, definitions Definitions) (Registry, error) {
	if revision == 0 {
		return Registry{}, errors.New("evidence registry revision must be positive")
	}
	registry := Registry{
		revision:    revision,
		artefacts:   make(map[Name]struct{}, len(definitions.Artefacts)),
		evidence:    make(map[Name]Type, len(definitions.Evidence)),
		methods:     make(map[Name]AcquisitionMethod, len(definitions.Methods)),
		purposes:    make(map[Name]struct{}, len(definitions.Purposes)),
		assurances:  make(map[Name]struct{}, len(definitions.Assurances)),
		constraints: make(map[Name]ConstraintDefinition, len(definitions.Constraints)),
	}
	for _, name := range definitions.Artefacts {
		if err := registry.addName(KindArtefact, name, registry.artefacts); err != nil {
			return Registry{}, err
		}
	}
	for _, name := range definitions.Purposes {
		if err := registry.addName(KindPurpose, name, registry.purposes); err != nil {
			return Registry{}, err
		}
	}
	for _, name := range definitions.Assurances {
		if err := registry.addName(KindAssurance, name, registry.assurances); err != nil {
			return Registry{}, err
		}
	}
	for _, definition := range definitions.Evidence {
		if _, err := ParseName(KindEvidence, string(definition.Name)); err != nil {
			return Registry{}, err
		}
		if _, exists := registry.evidence[definition.Name]; exists {
			return Registry{}, fmt.Errorf("duplicate evidence registry name %q", definition.Name)
		}
		artefacts, err := validatedNames(KindArtefact, definition.Artefacts, registry.artefacts)
		if err != nil || len(artefacts) == 0 {
			if err == nil {
				err = errors.New("evidence type requires at least one artefact")
			}
			return Registry{}, fmt.Errorf("validate evidence type %q: %w", definition.Name, err)
		}
		definition.Artefacts = artefacts
		registry.evidence[definition.Name] = definition
	}
	for _, definition := range definitions.Methods {
		if _, err := ParseName(KindMethod, string(definition.Name)); err != nil {
			return Registry{}, err
		}
		if _, exists := registry.methods[definition.Name]; exists {
			return Registry{}, fmt.Errorf("duplicate evidence registry name %q", definition.Name)
		}
		if len(definition.Supports) == 0 {
			return Registry{}, fmt.Errorf("acquisition method %q requires support declarations", definition.Name)
		}
		seenEvidence := make(map[Name]struct{}, len(definition.Supports))
		for index := range definition.Supports {
			support := &definition.Supports[index]
			evidenceType, exists := registry.evidence[support.EvidenceType]
			if !exists {
				return Registry{}, fmt.Errorf("acquisition method %q references unknown evidence type %q", definition.Name, support.EvidenceType)
			}
			if _, duplicate := seenEvidence[support.EvidenceType]; duplicate {
				return Registry{}, fmt.Errorf("acquisition method %q repeats evidence type %q", definition.Name, support.EvidenceType)
			}
			seenEvidence[support.EvidenceType] = struct{}{}
			artefactSet := namesToSet(evidenceType.Artefacts)
			var err error
			support.Artefacts, err = validatedNames(KindArtefact, support.Artefacts, artefactSet)
			if err != nil || len(support.Artefacts) == 0 {
				if err == nil {
					err = errors.New("support requires at least one artefact")
				}
				return Registry{}, fmt.Errorf("validate acquisition method %q: %w", definition.Name, err)
			}
			support.Assurances, err = validatedNames(KindAssurance, support.Assurances, registry.assurances)
			if err != nil {
				return Registry{}, fmt.Errorf("validate acquisition method %q: %w", definition.Name, err)
			}
		}
		registry.methods[definition.Name] = cloneMethod(definition)
	}
	for _, definition := range definitions.Constraints {
		if _, err := ParseName(KindConstraint, string(definition.Name)); err != nil {
			return Registry{}, err
		}
		if _, exists := registry.constraints[definition.Name]; exists {
			return Registry{}, fmt.Errorf("duplicate evidence registry name %q", definition.Name)
		}
		if !definition.ValueKind.valid() {
			return Registry{}, fmt.Errorf("constraint %q has invalid value kind %q", definition.Name, definition.ValueKind)
		}
		appliesTo, err := validatedNames(KindEvidence, definition.AppliesTo, evidenceNameSet(registry.evidence))
		if err != nil {
			return Registry{}, fmt.Errorf("validate constraint %q: %w", definition.Name, err)
		}
		definition.AppliesTo = appliesTo
		registry.constraints[definition.Name] = definition
	}

	canonical, err := registry.CanonicalJSON()
	if err != nil {
		return Registry{}, err
	}
	digest := sha256.Sum256(canonical)
	registry.digest = "sha256:" + hex.EncodeToString(digest[:])

	return registry, nil
}

// CanonicalJSON returns deterministic JSON for registry distribution,
// pinning, signing, and digest verification.
func (registry Registry) CanonicalJSON() ([]byte, error) {
	canonical, err := json.Marshal(canonicalDefinitions(registry))
	if err != nil {
		return nil, fmt.Errorf("serialise evidence registry: %w", err)
	}

	return canonical, nil
}

func (kind ValueKind) valid() bool {
	switch kind {
	case ValueString, ValueInteger, ValueBoolean, ValueStringList:
		return true
	default:
		return false
	}
}

func (registry Registry) addName(kind Kind, name Name, destination map[Name]struct{}) error {
	if _, err := ParseName(kind, string(name)); err != nil {
		return err
	}
	if _, exists := destination[name]; exists {
		return fmt.Errorf("duplicate evidence registry name %q", name)
	}
	destination[name] = struct{}{}

	return nil
}

// Reference returns the immutable pin stored in capture profiles and sessions.
func (registry Registry) Reference() Reference {
	return Reference{SchemaVersion: RegistrySchemaVersion, Revision: registry.revision, Digest: registry.digest}
}

// Has reports whether name exists for kind in this revision.
func (registry Registry) Has(kind Kind, name Name) bool {
	switch kind {
	case KindArtefact:
		_, ok := registry.artefacts[name]
		return ok
	case KindEvidence:
		_, ok := registry.evidence[name]
		return ok
	case KindMethod:
		_, ok := registry.methods[name]
		return ok
	case KindPurpose:
		_, ok := registry.purposes[name]
		return ok
	case KindAssurance:
		_, ok := registry.assurances[name]
		return ok
	case KindConstraint:
		_, ok := registry.constraints[name]
		return ok
	default:
		return false
	}
}

// EvidenceType returns a defensive copy of an evidence definition.
func (registry Registry) EvidenceType(name Name) (Type, bool) {
	definition, ok := registry.evidence[name]
	definition.Artefacts = slices.Clone(definition.Artefacts)

	return definition, ok
}

// Method returns a defensive copy of an acquisition-method definition.
func (registry Registry) Method(name Name) (AcquisitionMethod, bool) {
	definition, ok := registry.methods[name]
	return cloneMethod(definition), ok
}

// Constraint returns a defensive copy of a constraint definition.
func (registry Registry) Constraint(name Name) (ConstraintDefinition, bool) {
	definition, ok := registry.constraints[name]
	definition.AppliesTo = slices.Clone(definition.AppliesTo)

	return definition, ok
}

// Capabilities is an SDK's registry-bound acquisition-method advertisement.
// It guides method selection but is never evidence that assurance was achieved.
type Capabilities struct {
	Registry Reference
	Methods  []Name
}

// ValidateCapabilities rejects stale registry pins and unknown methods.
func (registry Registry) ValidateCapabilities(capabilities Capabilities) error {
	if capabilities.Registry != registry.Reference() {
		return errors.New("capture capabilities use a different evidence registry")
	}
	_, err := validatedNames(KindMethod, capabilities.Methods, methodNameSet(registry.methods))
	return err
}

func validatedNames(kind Kind, names []Name, allowed map[Name]struct{}) ([]Name, error) {
	result := slices.Clone(names)
	seen := make(map[Name]struct{}, len(result))
	for _, name := range result {
		if _, err := ParseName(kind, string(name)); err != nil {
			return nil, err
		}
		if _, exists := allowed[name]; !exists {
			return nil, fmt.Errorf("unknown %s registry name %q", kind, name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("duplicate %s registry name %q", kind, name)
		}
		seen[name] = struct{}{}
	}
	slices.Sort(result)

	return result, nil
}

func cloneMethod(method AcquisitionMethod) AcquisitionMethod {
	method.Supports = slices.Clone(method.Supports)
	for index := range method.Supports {
		method.Supports[index].Artefacts = slices.Clone(method.Supports[index].Artefacts)
		method.Supports[index].Assurances = slices.Clone(method.Supports[index].Assurances)
	}

	return method
}

func namesToSet(names []Name) map[Name]struct{} {
	result := make(map[Name]struct{}, len(names))
	for _, name := range names {
		result[name] = struct{}{}
	}

	return result
}

func evidenceNameSet(definitions map[Name]Type) map[Name]struct{} {
	result := make(map[Name]struct{}, len(definitions))
	for name := range definitions {
		result[name] = struct{}{}
	}

	return result
}

func methodNameSet(definitions map[Name]AcquisitionMethod) map[Name]struct{} {
	result := make(map[Name]struct{}, len(definitions))
	for name := range definitions {
		result[name] = struct{}{}
	}

	return result
}

type registryDocument struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Revision      uint32                 `json:"revision"`
	Artefacts     []Name                 `json:"artefacts"`
	Evidence      []evidenceTypeDocument `json:"evidence_types"`
	Methods       []methodDocument       `json:"acquisition_methods"`
	Purposes      []Name                 `json:"purposes"`
	Assurances    []Name                 `json:"assurances"`
	Constraints   []constraintDocument   `json:"constraints"`
}

type evidenceTypeDocument struct {
	Name      Name   `json:"name"`
	Artefacts []Name `json:"artefacts"`
}

type methodDocument struct {
	Name     Name                    `json:"name"`
	Supports []methodSupportDocument `json:"supports"`
}

type methodSupportDocument struct {
	EvidenceType Name   `json:"evidence_type"`
	Artefacts    []Name `json:"artefacts"`
	Assurances   []Name `json:"assurances"`
}

type constraintDocument struct {
	Name      Name      `json:"name"`
	ValueKind ValueKind `json:"value_kind"`
	AppliesTo []Name    `json:"applies_to"`
}

func canonicalDefinitions(registry Registry) registryDocument {
	document := registryDocument{
		SchemaVersion: RegistrySchemaVersion,
		Revision:      registry.revision,
		Artefacts:     make([]Name, 0, len(registry.artefacts)),
		Evidence:      make([]evidenceTypeDocument, 0, len(registry.evidence)),
		Methods:       make([]methodDocument, 0, len(registry.methods)),
		Purposes:      make([]Name, 0, len(registry.purposes)),
		Assurances:    make([]Name, 0, len(registry.assurances)),
		Constraints:   make([]constraintDocument, 0, len(registry.constraints)),
	}
	for name := range registry.artefacts {
		document.Artefacts = append(document.Artefacts, name)
	}
	for _, definition := range registry.evidence {
		document.Evidence = append(document.Evidence, evidenceTypeDocument{Name: definition.Name, Artefacts: slices.Clone(definition.Artefacts)})
	}
	for _, definition := range registry.methods {
		method := methodDocument{Name: definition.Name, Supports: make([]methodSupportDocument, 0, len(definition.Supports))}
		for _, support := range definition.Supports {
			method.Supports = append(method.Supports, methodSupportDocument{
				EvidenceType: support.EvidenceType,
				Artefacts:    nonNilNames(support.Artefacts),
				Assurances:   nonNilNames(support.Assurances),
			})
		}
		slices.SortFunc(method.Supports, func(left, right methodSupportDocument) int {
			return strings.Compare(string(left.EvidenceType), string(right.EvidenceType))
		})
		document.Methods = append(document.Methods, method)
	}
	for name := range registry.purposes {
		document.Purposes = append(document.Purposes, name)
	}
	for name := range registry.assurances {
		document.Assurances = append(document.Assurances, name)
	}
	for _, definition := range registry.constraints {
		document.Constraints = append(document.Constraints, constraintDocument{
			Name:      definition.Name,
			ValueKind: definition.ValueKind,
			AppliesTo: nonNilNames(definition.AppliesTo),
		})
	}
	slices.Sort(document.Artefacts)
	slices.SortFunc(document.Evidence, func(left, right evidenceTypeDocument) int {
		return strings.Compare(string(left.Name), string(right.Name))
	})
	slices.SortFunc(document.Methods, func(left, right methodDocument) int { return strings.Compare(string(left.Name), string(right.Name)) })
	slices.Sort(document.Purposes)
	slices.Sort(document.Assurances)
	slices.SortFunc(document.Constraints, func(left, right constraintDocument) int {
		return strings.Compare(string(left.Name), string(right.Name))
	})

	return document
}

func nonNilNames(names []Name) []Name {
	if len(names) == 0 {
		return []Name{}
	}

	return slices.Clone(names)
}
