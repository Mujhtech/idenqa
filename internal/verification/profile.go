// Package verification owns capture profiles, immutable requirement
// snapshots, verification workflow, checks, signals, and decisions.
package verification

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

	"github.com/Mujhtech/idenqa/internal/evidence"
)

// ProfileSchemaVersion is the supported canonical capture-profile version.
const ProfileSchemaVersion uint32 = 1

// Strategy defines how listed acquisition methods satisfy a requirement.
type Strategy string

const (
	// StrategyAnyOf lets the subject use one permitted acquisition method.
	StrategyAnyOf Strategy = "any_of"
	// StrategyAllOf requires every listed acquisition method.
	StrategyAllOf Strategy = "all_of"
)

// FallbackCondition is a bounded, policy-visible reason for making an
// alternative acquisition expression available.
type FallbackCondition string

const (
	// FallbackCapabilityUnavailable applies when the client lacks a method.
	FallbackCapabilityUnavailable FallbackCondition = "capability_unavailable"
	// FallbackMethodUnavailable applies when a method cannot currently run.
	FallbackMethodUnavailable FallbackCondition = "method_unavailable"
	// FallbackCaptureFailed applies after the permitted primary capture fails.
	FallbackCaptureFailed FallbackCondition = "capture_failed"
)

// Acquisition is either a choice among methods or a requirement to use every
// listed method. Method order is preference order for any_of and execution
// order for all_of, and is therefore part of the canonical digest.
type Acquisition struct {
	Strategy Strategy
	Methods  []evidence.Name
}

// ConstraintValue is a bounded tagged value. Arbitrary JSON is deliberately
// excluded so canonicalisation and extension validation remain deterministic.
type ConstraintValue struct {
	Kind       evidence.ValueKind
	String     string
	Integer    int64
	Boolean    bool
	StringList []string
}

// Constraint applies one registry-defined constraint to a requirement.
type Constraint struct {
	Name  evidence.Name
	Value ConstraintValue
}

// Fallback makes an assurance-preserving alternative available only for its
// explicit conditions.
type Fallback struct {
	On          []FallbackCondition
	Acquisition Acquisition
}

// Requirement defines what must be collected and how it may be acquired.
type Requirement struct {
	Key                string
	Purpose            evidence.Name
	EvidenceType       evidence.Name
	Artefacts          []evidence.Name
	Acquisition        Acquisition
	RequiredAssurances []evidence.Name
	Constraints        []Constraint
	Fallbacks          []Fallback
}

// Profile is the version-neutral domain representation of a capture profile.
// Persistence lifecycle and tenant ownership begin in C-03.
type Profile struct {
	SchemaVersion uint32
	Registry      evidence.Reference
	Requirements  []Requirement
}

// ParseProfileFromCatalog extracts the immutable registry pin and then applies
// the full strict profile decoder against that exact deployed revision.
func ParseProfileFromCatalog(encoded []byte, catalog evidence.Catalog) (Profile, error) {
	if catalog.IsZero() {
		return Profile{}, errors.New("capture profile registry catalog is empty")
	}
	var header struct {
		Registry evidence.Reference `json:"registry"`
	}
	if err := json.Unmarshal(encoded, &header); err != nil {
		return Profile{}, fmt.Errorf("decode capture profile registry reference: %w", err)
	}
	registry, err := catalog.Resolve(header.Registry)
	if err != nil {
		return Profile{}, err
	}

	return ParseProfileJSON(encoded, registry)
}

// NewProfile binds requirements to one immutable registry and validates the
// complete profile before returning it.
func NewProfile(registry evidence.Registry, requirements []Requirement) (Profile, error) {
	profile := Profile{
		SchemaVersion: ProfileSchemaVersion,
		Registry:      registry.Reference(),
		Requirements:  cloneRequirements(requirements),
	}
	if err := ValidateProfile(profile, registry); err != nil {
		return Profile{}, err
	}

	return profile, nil
}

// ParseProfileJSON strictly decodes a profile and validates it against the
// exact registry supplied by its caller. Unknown fields and trailing values
// fail closed at this boundary.
func ParseProfileJSON(encoded []byte, registry evidence.Registry) (Profile, error) {
	var document profileDocument
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Profile{}, fmt.Errorf("decode capture profile: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Profile{}, errors.New("decode capture profile: trailing JSON value")
	}

	requirements := make([]Requirement, 0, len(document.Requirements))
	for index, item := range document.Requirements {
		constraints := make([]Constraint, 0, len(item.Constraints))
		for _, encodedConstraint := range item.Constraints {
			definition, exists := registry.Constraint(encodedConstraint.Name)
			if !exists {
				return Profile{}, fmt.Errorf("decode capture requirement %d: unknown constraint %q", index, encodedConstraint.Name)
			}
			value, err := decodeConstraintValue(encodedConstraint.Value, definition.ValueKind)
			if err != nil {
				return Profile{}, fmt.Errorf("decode capture constraint %q: %w", encodedConstraint.Name, err)
			}
			constraints = append(constraints, Constraint{Name: encodedConstraint.Name, Value: value})
		}
		fallbacks := make([]Fallback, 0, len(item.Fallbacks))
		for _, fallback := range item.Fallbacks {
			fallbacks = append(fallbacks, Fallback{
				On: fallback.On,
				Acquisition: Acquisition{
					Strategy: fallback.Acquisition.Strategy,
					Methods:  fallback.Acquisition.Methods,
				},
			})
		}
		requirements = append(requirements, Requirement{
			Key:                item.Key,
			Purpose:            item.Purpose,
			EvidenceType:       item.EvidenceType,
			Artefacts:          item.Artefacts,
			Acquisition:        Acquisition{Strategy: item.Acquisition.Strategy, Methods: item.Acquisition.Methods},
			RequiredAssurances: item.RequiredAssurances,
			Constraints:        constraints,
			Fallbacks:          fallbacks,
		})
	}
	profile := Profile{
		SchemaVersion: document.SchemaVersion,
		Registry:      document.Registry,
		Requirements:  requirements,
	}
	if err := ValidateProfile(profile, registry); err != nil {
		return Profile{}, err
	}

	return cloneProfile(profile), nil
}

// ValidateProfile validates a profile against the exact pinned registry.
func ValidateProfile(profile Profile, registry evidence.Registry) error {
	if profile.SchemaVersion != ProfileSchemaVersion {
		return fmt.Errorf("capture profile schema version %d is unsupported", profile.SchemaVersion)
	}
	if profile.Registry != registry.Reference() {
		return errors.New("capture profile uses a different evidence registry")
	}
	if len(profile.Requirements) == 0 {
		return errors.New("capture profile requires at least one requirement")
	}
	seenKeys := make(map[string]struct{}, len(profile.Requirements))
	for index, requirement := range profile.Requirements {
		if err := validateRequirement(requirement, registry); err != nil {
			return fmt.Errorf("validate capture requirement %d: %w", index, err)
		}
		if _, duplicate := seenKeys[requirement.Key]; duplicate {
			return fmt.Errorf("capture requirement key %q is duplicated", requirement.Key)
		}
		seenKeys[requirement.Key] = struct{}{}
	}

	return nil
}

func validateRequirement(requirement Requirement, registry evidence.Registry) error {
	if !validRequirementKey(requirement.Key) {
		return fmt.Errorf("capture requirement key %q is invalid", requirement.Key)
	}
	if !registry.Has(evidence.KindPurpose, requirement.Purpose) {
		return fmt.Errorf("capture requirement references unknown purpose %q", requirement.Purpose)
	}
	evidenceType, exists := registry.EvidenceType(requirement.EvidenceType)
	if !exists {
		return fmt.Errorf("capture requirement references unknown evidence type %q", requirement.EvidenceType)
	}
	artefacts, err := validateNameSet(evidence.KindArtefact, requirement.Artefacts, evidenceType.Artefacts)
	if err != nil || len(artefacts) == 0 {
		if err == nil {
			err = errors.New("capture requirement requires at least one artefact")
		}
		return err
	}
	assurances, err := validateRegistryNameSet(registry, evidence.KindAssurance, requirement.RequiredAssurances)
	if err != nil {
		return err
	}
	if err := validateAcquisition(requirement.Acquisition, requirement.EvidenceType, artefacts, assurances, registry); err != nil {
		return fmt.Errorf("validate primary acquisition: %w", err)
	}
	if err := validateConstraints(requirement.Constraints, requirement.EvidenceType, registry); err != nil {
		return err
	}
	seenConditions := make(map[FallbackCondition]struct{})
	for index, fallback := range requirement.Fallbacks {
		if len(fallback.On) == 0 {
			return fmt.Errorf("fallback %d requires at least one condition", index)
		}
		for _, condition := range fallback.On {
			if !condition.valid() {
				return fmt.Errorf("fallback %d has invalid condition %q", index, condition)
			}
			if _, duplicate := seenConditions[condition]; duplicate {
				return fmt.Errorf("fallback condition %q is ambiguous", condition)
			}
			seenConditions[condition] = struct{}{}
		}
		if err := validateAcquisition(fallback.Acquisition, requirement.EvidenceType, artefacts, assurances, registry); err != nil {
			return fmt.Errorf("validate fallback %d: %w", index, err)
		}
	}

	return nil
}

func validateAcquisition(acquisition Acquisition, evidenceType evidence.Name, artefacts, assurances []evidence.Name, registry evidence.Registry) error {
	if acquisition.Strategy != StrategyAnyOf && acquisition.Strategy != StrategyAllOf {
		return fmt.Errorf("acquisition strategy %q is invalid", acquisition.Strategy)
	}
	if len(acquisition.Methods) == 0 {
		return errors.New("acquisition requires at least one method")
	}
	seenMethods := make(map[evidence.Name]struct{}, len(acquisition.Methods))
	combinedAssurances := make(map[evidence.Name]struct{})
	for _, methodName := range acquisition.Methods {
		if _, duplicate := seenMethods[methodName]; duplicate {
			return fmt.Errorf("acquisition method %q is duplicated", methodName)
		}
		seenMethods[methodName] = struct{}{}
		method, exists := registry.Method(methodName)
		if !exists {
			return fmt.Errorf("acquisition references unknown method %q", methodName)
		}
		support, exists := supportFor(method, evidenceType)
		if !exists || !containsAll(support.Artefacts, artefacts) {
			return fmt.Errorf("acquisition method %q cannot produce the required artefacts", methodName)
		}
		if acquisition.Strategy == StrategyAnyOf && !containsAll(support.Assurances, assurances) {
			return fmt.Errorf("acquisition method %q cannot establish the required assurance", methodName)
		}
		for _, assurance := range support.Assurances {
			combinedAssurances[assurance] = struct{}{}
		}
	}
	if acquisition.Strategy == StrategyAllOf {
		for _, assurance := range assurances {
			if _, exists := combinedAssurances[assurance]; !exists {
				return errors.New("combined acquisition methods cannot establish the required assurance")
			}
		}
	}

	return nil
}

func validateConstraints(constraints []Constraint, evidenceType evidence.Name, registry evidence.Registry) error {
	seen := make(map[evidence.Name]struct{}, len(constraints))
	for _, constraint := range constraints {
		definition, exists := registry.Constraint(constraint.Name)
		if !exists {
			return fmt.Errorf("capture requirement references unknown constraint %q", constraint.Name)
		}
		if _, duplicate := seen[constraint.Name]; duplicate {
			return fmt.Errorf("capture constraint %q is duplicated", constraint.Name)
		}
		seen[constraint.Name] = struct{}{}
		if definition.ValueKind != constraint.Value.Kind {
			return fmt.Errorf("capture constraint %q requires %s", constraint.Name, definition.ValueKind)
		}
		if len(definition.AppliesTo) > 0 && !slices.Contains(definition.AppliesTo, evidenceType) {
			return fmt.Errorf("capture constraint %q does not apply to evidence type %q", constraint.Name, evidenceType)
		}
		if err := validateConstraintValue(constraint.Value); err != nil {
			return fmt.Errorf("validate capture constraint %q: %w", constraint.Name, err)
		}
	}

	return nil
}

func validateConstraintValue(value ConstraintValue) error {
	switch value.Kind {
	case evidence.ValueString:
		if strings.TrimSpace(value.String) == "" || value.Integer != 0 || value.Boolean || len(value.StringList) != 0 {
			return errors.New("string value is invalid")
		}
	case evidence.ValueInteger:
		if value.Integer < 0 || value.String != "" || value.Boolean || len(value.StringList) != 0 {
			return errors.New("integer value is invalid")
		}
	case evidence.ValueBoolean:
		if value.String != "" || value.Integer != 0 || len(value.StringList) != 0 {
			return errors.New("boolean value contains fields for another kind")
		}
		return nil
	case evidence.ValueStringList:
		if len(value.StringList) == 0 || value.String != "" || value.Integer != 0 || value.Boolean {
			return errors.New("string list is empty")
		}
		seen := make(map[string]struct{}, len(value.StringList))
		for _, item := range value.StringList {
			if strings.TrimSpace(item) != item || item == "" {
				return errors.New("string list contains an invalid item")
			}
			if _, duplicate := seen[item]; duplicate {
				return errors.New("string list contains a duplicate item")
			}
			seen[item] = struct{}{}
		}
	default:
		return fmt.Errorf("constraint value kind %q is invalid", value.Kind)
	}

	return nil
}

func (condition FallbackCondition) valid() bool {
	switch condition {
	case FallbackCapabilityUnavailable, FallbackMethodUnavailable, FallbackCaptureFailed:
		return true
	default:
		return false
	}
}

func validRequirementKey(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
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

func supportFor(method evidence.AcquisitionMethod, evidenceType evidence.Name) (evidence.MethodSupport, bool) {
	for _, support := range method.Supports {
		if support.EvidenceType == evidenceType {
			return support, true
		}
	}

	return evidence.MethodSupport{}, false
}

func containsAll(available, required []evidence.Name) bool {
	set := make(map[evidence.Name]struct{}, len(available))
	for _, name := range available {
		set[name] = struct{}{}
	}
	for _, name := range required {
		if _, exists := set[name]; !exists {
			return false
		}
	}

	return true
}

func validateNameSet(kind evidence.Kind, names, allowed []evidence.Name) ([]evidence.Name, error) {
	allowedSet := make(map[evidence.Name]struct{}, len(allowed))
	for _, name := range allowed {
		allowedSet[name] = struct{}{}
	}
	result := slices.Clone(names)
	seen := make(map[evidence.Name]struct{}, len(result))
	for _, name := range result {
		if _, err := evidence.ParseName(kind, string(name)); err != nil {
			return nil, err
		}
		if _, exists := allowedSet[name]; !exists {
			return nil, fmt.Errorf("unknown or incompatible %s %q", kind, name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("duplicate %s %q", kind, name)
		}
		seen[name] = struct{}{}
	}

	return result, nil
}

func validateRegistryNameSet(registry evidence.Registry, kind evidence.Kind, names []evidence.Name) ([]evidence.Name, error) {
	result := slices.Clone(names)
	seen := make(map[evidence.Name]struct{}, len(result))
	for _, name := range result {
		if !registry.Has(kind, name) {
			return nil, fmt.Errorf("capture requirement references unknown %s %q", kind, name)
		}
		if _, duplicate := seen[name]; duplicate {
			return nil, fmt.Errorf("capture requirement repeats %s %q", kind, name)
		}
		seen[name] = struct{}{}
	}

	return result, nil
}

// EligibleMethods intersects a valid requirement with advertised SDK
// capabilities. The result is routing input only and proves no assurance.
func EligibleMethods(registry evidence.Registry, requirement Requirement, capabilities evidence.Capabilities) ([]evidence.Name, error) {
	if err := validateRequirement(requirement, registry); err != nil {
		return nil, err
	}
	if err := registry.ValidateCapabilities(capabilities); err != nil {
		return nil, err
	}
	available := make(map[evidence.Name]struct{}, len(capabilities.Methods))
	for _, method := range capabilities.Methods {
		available[method] = struct{}{}
	}
	eligible := make([]evidence.Name, 0, len(requirement.Acquisition.Methods))
	for _, method := range requirement.Acquisition.Methods {
		if _, exists := available[method]; exists {
			eligible = append(eligible, method)
		}
	}
	if requirement.Acquisition.Strategy == StrategyAllOf && len(eligible) != len(requirement.Acquisition.Methods) {
		return nil, errors.New("capture capabilities cannot satisfy all required acquisition methods")
	}
	if len(eligible) == 0 {
		return nil, errors.New("capture capabilities cannot satisfy the acquisition requirement")
	}

	return eligible, nil
}

// ValidateForActivation applies the complete activation gate and returns the
// canonical digest that a later persistence brick stores with the immutable
// published version.
func ValidateForActivation(profile Profile, registry evidence.Registry) (string, error) {
	return Digest(profile, registry)
}

// CanonicalJSON returns deterministic JSON for signing, digesting, fixtures,
// and immutable session snapshots.
func CanonicalJSON(profile Profile, registry evidence.Registry) ([]byte, error) {
	if err := ValidateProfile(profile, registry); err != nil {
		return nil, err
	}
	document := canonicalProfile{
		SchemaVersion: profile.SchemaVersion,
		Registry:      profile.Registry,
		Requirements:  make([]canonicalRequirement, 0, len(profile.Requirements)),
	}
	for _, requirement := range profile.Requirements {
		item := canonicalRequirement{
			Key:                requirement.Key,
			Purpose:            requirement.Purpose,
			EvidenceType:       requirement.EvidenceType,
			Artefacts:          nonNilEvidenceNames(requirement.Artefacts),
			Acquisition:        canonicalAcquisition{Strategy: requirement.Acquisition.Strategy, Methods: slices.Clone(requirement.Acquisition.Methods)},
			RequiredAssurances: nonNilEvidenceNames(requirement.RequiredAssurances),
			Constraints:        make([]canonicalConstraint, 0, len(requirement.Constraints)),
			Fallbacks:          make([]canonicalFallback, 0, len(requirement.Fallbacks)),
		}
		slices.Sort(item.Artefacts)
		slices.Sort(item.RequiredAssurances)
		for _, constraint := range requirement.Constraints {
			item.Constraints = append(item.Constraints, canonicalConstraint{Name: constraint.Name, Value: canonicalConstraintValue(constraint.Value)})
		}
		slices.SortFunc(item.Constraints, func(left, right canonicalConstraint) int {
			return strings.Compare(string(left.Name), string(right.Name))
		})
		for _, fallback := range requirement.Fallbacks {
			conditions := slices.Clone(fallback.On)
			slices.Sort(conditions)
			item.Fallbacks = append(item.Fallbacks, canonicalFallback{On: conditions, Acquisition: canonicalAcquisition{Strategy: fallback.Acquisition.Strategy, Methods: slices.Clone(fallback.Acquisition.Methods)}})
		}
		document.Requirements = append(document.Requirements, item)
	}

	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("serialise capture profile: %w", err)
	}

	return encoded, nil
}

// Digest returns the canonical SHA-256 content digest for a valid profile.
func Digest(profile Profile, registry evidence.Registry) (string, error) {
	canonical, err := CanonicalJSON(profile, registry)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(canonical)

	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

type canonicalProfile struct {
	SchemaVersion uint32                 `json:"schema_version"`
	Registry      evidence.Reference     `json:"registry"`
	Requirements  []canonicalRequirement `json:"requirements"`
}

type profileDocument struct {
	SchemaVersion uint32                `json:"schema_version"`
	Registry      evidence.Reference    `json:"registry"`
	Requirements  []requirementDocument `json:"requirements"`
}

type requirementDocument struct {
	Key                string               `json:"key"`
	Purpose            evidence.Name        `json:"purpose"`
	EvidenceType       evidence.Name        `json:"evidence_type"`
	Artefacts          []evidence.Name      `json:"artefacts"`
	Acquisition        canonicalAcquisition `json:"acquisition"`
	RequiredAssurances []evidence.Name      `json:"required_assurances"`
	Constraints        []constraintDocument `json:"constraints"`
	Fallbacks          []canonicalFallback  `json:"fallbacks"`
}

type constraintDocument struct {
	Name  evidence.Name   `json:"name"`
	Value json.RawMessage `json:"value"`
}

type canonicalRequirement struct {
	Key                string                `json:"key"`
	Purpose            evidence.Name         `json:"purpose"`
	EvidenceType       evidence.Name         `json:"evidence_type"`
	Artefacts          []evidence.Name       `json:"artefacts"`
	Acquisition        canonicalAcquisition  `json:"acquisition"`
	RequiredAssurances []evidence.Name       `json:"required_assurances"`
	Constraints        []canonicalConstraint `json:"constraints"`
	Fallbacks          []canonicalFallback   `json:"fallbacks"`
}

type canonicalAcquisition struct {
	Strategy Strategy        `json:"strategy"`
	Methods  []evidence.Name `json:"methods"`
}

type canonicalConstraint struct {
	Name  evidence.Name `json:"name"`
	Value any           `json:"value"`
}

type canonicalFallback struct {
	On          []FallbackCondition  `json:"on"`
	Acquisition canonicalAcquisition `json:"acquisition"`
}

func canonicalConstraintValue(value ConstraintValue) any {
	switch value.Kind {
	case evidence.ValueString:
		return value.String
	case evidence.ValueInteger:
		return value.Integer
	case evidence.ValueBoolean:
		return value.Boolean
	case evidence.ValueStringList:
		items := slices.Clone(value.StringList)
		slices.Sort(items)
		return items
	default:
		return nil
	}
}

func decodeConstraintValue(encoded json.RawMessage, kind evidence.ValueKind) (ConstraintValue, error) {
	value := ConstraintValue{Kind: kind}
	var destination any
	switch kind {
	case evidence.ValueString:
		destination = &value.String
	case evidence.ValueInteger:
		destination = &value.Integer
	case evidence.ValueBoolean:
		destination = &value.Boolean
	case evidence.ValueStringList:
		destination = &value.StringList
	default:
		return ConstraintValue{}, fmt.Errorf("constraint value kind %q is invalid", kind)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	if err := decoder.Decode(destination); err != nil {
		return ConstraintValue{}, err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ConstraintValue{}, errors.New("constraint contains a trailing JSON value")
	}

	return value, nil
}

func cloneProfile(profile Profile) Profile {
	profile.Requirements = cloneRequirements(profile.Requirements)

	return profile
}

func cloneRequirements(requirements []Requirement) []Requirement {
	result := slices.Clone(requirements)
	for index := range result {
		result[index].Artefacts = slices.Clone(result[index].Artefacts)
		result[index].Acquisition.Methods = slices.Clone(result[index].Acquisition.Methods)
		result[index].RequiredAssurances = slices.Clone(result[index].RequiredAssurances)
		result[index].Constraints = slices.Clone(result[index].Constraints)
		for constraintIndex := range result[index].Constraints {
			result[index].Constraints[constraintIndex].Value.StringList = slices.Clone(result[index].Constraints[constraintIndex].Value.StringList)
		}
		result[index].Fallbacks = slices.Clone(result[index].Fallbacks)
		for fallbackIndex := range result[index].Fallbacks {
			result[index].Fallbacks[fallbackIndex].On = slices.Clone(result[index].Fallbacks[fallbackIndex].On)
			result[index].Fallbacks[fallbackIndex].Acquisition.Methods = slices.Clone(result[index].Fallbacks[fallbackIndex].Acquisition.Methods)
		}
	}

	return result
}

func nonNilEvidenceNames(names []evidence.Name) []evidence.Name {
	if len(names) == 0 {
		return []evidence.Name{}
	}

	return slices.Clone(names)
}
