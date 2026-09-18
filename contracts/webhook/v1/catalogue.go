// Package webhook defines the public, versioned webhook event catalogue and
// the canonical reference-only envelope. Raw evidence, biometric templates,
// identity values, credentials, and provider payloads are never in the
// envelope — only stable resource references and safe status fields.
package webhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

// Contract bounds for the v1 webhook catalogue and envelope.
const (
	MajorVersion         uint16 = 1
	MinorVersion         uint16 = 0
	SchemaVersion               = "1.0"
	Wildcard                    = "*"
	MaximumSubscriptions        = 64
	MaximumEventBytes           = 64 * 1024
	MaximumDataBytes            = 16 * 1024
	MaximumRegionBytes          = 64
)

var (
	// ErrInvalidEvent means an envelope violates the canonical contract.
	ErrInvalidEvent = errors.New("webhook: invalid event")
	// ErrInvalidSubscription means an endpoint subscription selection is invalid.
	ErrInvalidSubscription = errors.New("webhook: invalid subscription")
)

// Type is one exact dotted catalogue event name.
type Type string

// Selected catalogue event types.
const (
	SubjectCreated                Type = "subject.created"
	SubjectUpdated                Type = "subject.updated"
	SubjectDeletionRequested      Type = "subject.deletion_requested"
	VerificationCreated           Type = "verification.created"
	VerificationCollecting        Type = "verification.collecting"
	VerificationProcessing        Type = "verification.processing"
	VerificationRequiresInput     Type = "verification.requires_input"
	VerificationRequiresReview    Type = "verification.requires_review"
	VerificationCompleted         Type = "verification.completed"
	VerificationCancelled         Type = "verification.cancelled"
	VerificationFailed            Type = "verification.failed"
	VerificationExpired           Type = "verification.expired"
	DecisionCreated               Type = "decision.created"
	DecisionSuperseded            Type = "decision.superseded"
	DecisionCorrected             Type = "decision.corrected"
	CheckStarted                  Type = "check.started"
	CheckCompleted                Type = "check.completed"
	CheckInconclusive             Type = "check.inconclusive"
	EvidenceReady                 Type = "evidence.ready"
	EvidenceDeleted               Type = "evidence.deleted"
	ConsentRecorded               Type = "consent.recorded"
	ConsentRevoked                Type = "consent.revoked"
	ProcessingAuthorityCreated    Type = "processing_authority.created"
	ProcessingAuthorityRestricted Type = "processing_authority.restricted"
	DeletionRequestUpdated        Type = "deletion_request.updated"
	CaseCreated                   Type = "case.created"
	CaseAssigned                  Type = "case.assigned"
	CaseFindingRecorded           Type = "case.finding_recorded"
	AppealUpdated                 Type = "appeal.updated"
)

// Definition describes one catalogue event type and its closed data fields.
type Definition struct {
	Type          Type
	SchemaVersion string
	RequiredData  []string
	OptionalData  []string
}

// AcceptedData returns the closed, sorted data field set.
func (definition Definition) AcceptedData() []string {
	fields := make([]string, 0, len(definition.RequiredData)+len(definition.OptionalData))
	fields = append(fields, definition.RequiredData...)
	fields = append(fields, definition.OptionalData...)
	sort.Strings(fields)

	return fields
}

// Accepts validates closed data keys for one event definition.
func (definition Definition) Accepts(data map[string]json.RawMessage) error {
	accepted := definition.AcceptedData()
	for _, required := range definition.RequiredData {
		if _, exists := data[required]; !exists {
			return fmt.Errorf("%w: %s requires data field %q", ErrInvalidEvent, definition.Type, required)
		}
	}
	for field := range data {
		index := sort.SearchStrings(accepted, field)
		if index >= len(accepted) || accepted[index] != field {
			return fmt.Errorf("%w: %s rejects data field %q", ErrInvalidEvent, definition.Type, field)
		}
	}

	return nil
}

var definitions = []Definition{
	{Type: SubjectCreated, SchemaVersion: SchemaVersion, RequiredData: []string{"subject_id", "version"}, OptionalData: []string{"state", "external_reference_set", "subject"}},
	{Type: SubjectUpdated, SchemaVersion: SchemaVersion, RequiredData: []string{"subject_id", "version"}, OptionalData: []string{"state", "external_reference_set", "subject"}},
	{Type: SubjectDeletionRequested, SchemaVersion: SchemaVersion, RequiredData: []string{"subject_id", "deletion_id"}, OptionalData: []string{"state", "subject"}},
	{Type: VerificationCreated, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "version"}, OptionalData: []string{"subject_id", "policy_id", "verification"}},
	{Type: VerificationCollecting, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "version"}, OptionalData: []string{"subject_id", "verification"}},
	{Type: VerificationProcessing, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "version"}, OptionalData: []string{"subject_id", "verification"}},
	{Type: VerificationRequiresInput, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "version"}, OptionalData: []string{"case_id", "subject_id", "verification"}},
	{Type: VerificationRequiresReview, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "version"}, OptionalData: []string{"case_id", "subject_id", "verification"}},
	{Type: VerificationCompleted, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "subject_id", "decision_id"}, OptionalData: []string{"outcome", "decided_at", "verification"}},
	{Type: VerificationCancelled, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "version"}, OptionalData: []string{"subject_id", "reason_code", "verification"}},
	{Type: VerificationFailed, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "version"}, OptionalData: []string{"subject_id", "reason_code", "verification"}},
	{Type: VerificationExpired, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "version"}, OptionalData: []string{"subject_id", "reason_code", "verification"}},
	{Type: DecisionCreated, SchemaVersion: SchemaVersion, RequiredData: []string{"decision_id", "verification_id", "policy_id", "revision"}, OptionalData: []string{"outcome", "decided_at", "decision"}},
	{Type: DecisionSuperseded, SchemaVersion: SchemaVersion, RequiredData: []string{"decision_id", "verification_id", "policy_id", "revision"}, OptionalData: []string{"outcome", "decided_at", "previous_decision_id", "decision"}},
	{Type: DecisionCorrected, SchemaVersion: SchemaVersion, RequiredData: []string{"decision_id", "verification_id", "previous_decision_id", "case_id"}, OptionalData: []string{"outcome", "decided_at", "decision"}},
	{Type: CheckStarted, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "check_id", "version"}, OptionalData: []string{"evidence_type", "artefact", "check"}},
	{Type: CheckCompleted, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "check_id", "version"}, OptionalData: []string{"outcome", "reason_code", "check"}},
	{Type: CheckInconclusive, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "check_id", "version"}, OptionalData: []string{"outcome", "reason_code", "check"}},
	{Type: EvidenceReady, SchemaVersion: SchemaVersion, RequiredData: []string{"evidence_id", "verification_id"}, OptionalData: []string{"evidence_type", "artefact", "acquisition_method", "assurances", "media_type", "evidence"}},
	{Type: EvidenceDeleted, SchemaVersion: SchemaVersion, RequiredData: []string{"evidence_id", "verification_id"}, OptionalData: []string{"reason_code", "evidence"}},
	{Type: ConsentRecorded, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "authority_id"}, OptionalData: []string{"notice_id", "subject_id", "authority"}},
	{Type: ConsentRevoked, SchemaVersion: SchemaVersion, RequiredData: []string{"verification_id", "authority_id"}, OptionalData: []string{"notice_id", "subject_id", "authority"}},
	{Type: ProcessingAuthorityCreated, SchemaVersion: SchemaVersion, RequiredData: []string{"authority_id", "verification_id"}, OptionalData: []string{"notice_id", "subject_id", "authority"}},
	{Type: ProcessingAuthorityRestricted, SchemaVersion: SchemaVersion, RequiredData: []string{"authority_id", "verification_id"}, OptionalData: []string{"state", "authority"}},
	{Type: DeletionRequestUpdated, SchemaVersion: SchemaVersion, RequiredData: []string{"deletion_id"}, OptionalData: []string{"state", "deletion"}},
	{Type: CaseCreated, SchemaVersion: SchemaVersion, RequiredData: []string{"case_id", "verification_id"}, OptionalData: []string{"state", "required_certification", "case"}},
	{Type: CaseAssigned, SchemaVersion: SchemaVersion, RequiredData: []string{"case_id", "verification_id"}, OptionalData: []string{"operator_id", "state", "case"}},
	{Type: CaseFindingRecorded, SchemaVersion: SchemaVersion, RequiredData: []string{"case_id", "verification_id"}, OptionalData: []string{"finding_id", "resolution", "reason_code", "case"}},
	{Type: AppealUpdated, SchemaVersion: SchemaVersion, RequiredData: []string{"appeal_id", "case_id"}, OptionalData: []string{"state", "outcome", "appeal"}},
}

// Catalogue returns a defensive copy of the closed event catalogue.
func Catalogue() []Definition {
	result := make([]Definition, len(definitions))
	copy(result, definitions)

	return result
}

// Lookup resolves one exact catalogue event type.
func Lookup(name Type) (Definition, bool) {
	for _, definition := range definitions {
		if definition.Type == name {
			return definition, true
		}
	}

	return Definition{}, false
}

// IsWildcard reports whether the selection subscribes to every catalogue event.
func IsWildcard(selection string) bool { return selection == Wildcard }

// ValidateSubscriptions validates an endpoint selection of exact names or the
// single wildcard entry. The result is a defensive copy in caller order.
func ValidateSubscriptions(selection []string) ([]string, error) {
	if len(selection) == 0 || len(selection) > MaximumSubscriptions {
		return nil, fmt.Errorf("%w: between 1 and %d entries are required", ErrInvalidSubscription, MaximumSubscriptions)
	}
	result := make([]string, 0, len(selection))
	seen := make(map[string]struct{}, len(selection))
	for _, entry := range selection {
		if _, duplicate := seen[entry]; duplicate {
			return nil, fmt.Errorf("%w: duplicate entry %q", ErrInvalidSubscription, entry)
		}
		seen[entry] = struct{}{}
		if IsWildcard(entry) {
			if len(selection) != 1 {
				return nil, fmt.Errorf("%w: wildcard cannot be combined with exact types", ErrInvalidSubscription)
			}
			return []string{Wildcard}, nil
		}
		if _, exists := Lookup(Type(entry)); !exists {
			return nil, fmt.Errorf("%w: unknown event type %q", ErrInvalidSubscription, entry)
		}
		result = append(result, entry)
	}

	return result, nil
}

// Subscribes reports whether a stored selection receives one event type.
func Subscribes(selection []string, eventType Type) bool {
	for _, entry := range selection {
		if IsWildcard(entry) || entry == string(eventType) {
			return true
		}
	}

	return false
}
