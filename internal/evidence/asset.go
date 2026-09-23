package evidence

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	// ContentContextSchemaVersion is the canonical evidence encryption-context version.
	ContentContextSchemaVersion uint32 = 1
	// ContentEncryptionPurpose is the immutable key and object purpose for evidence content.
	ContentEncryptionPurpose = "idenqa.evidence.content"
	maxClassificationLength  = 200
)

var (
	classificationExpression = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)+$`)

	// ErrNotFound deliberately covers absent and cross-tenant evidence records.
	ErrNotFound = errors.New("evidence: record not found")
	// ErrConflict identifies an invalid evidence lifecycle transition.
	ErrConflict = errors.New("evidence: lifecycle conflict")
	// ErrVersionConflict identifies an optimistic-concurrency mismatch.
	ErrVersionConflict = errors.New("evidence: version precondition failed")
	// ErrReadDenied means current evidence state does not permit plaintext release.
	ErrReadDenied = errors.New("evidence: controlled read denied")
)

// State is the lifecycle state of protected evidence content.
type State string

const (
	// StateAvailable allows purpose-bound read-grant evaluation.
	StateAvailable State = "available"
	// StateQuarantined denies every content read while retaining metadata.
	StateQuarantined State = "quarantined"
	// StateDeleted proves ciphertext deletion while retaining reference-only history.
	StateDeleted State = "deleted"
)

// LifecycleEvent is one safe, append-only evidence metadata transition.
type LifecycleEvent struct {
	Version    int64
	Action     string
	Reason     string
	OccurredAt time.Time
}

// Integrity is the current content-integrity state.
type Integrity string

const (
	// IntegrityVerified means the persisted ciphertext and expected plaintext digest were verified.
	IntegrityVerified Integrity = "verified"
	// IntegrityFailed means an integrity check failed and the evidence is quarantined.
	IntegrityFailed Integrity = "failed"
)

// Record is the complete durable metadata representation of one collected
// evidence artefact. Raw evidence bytes are deliberately absent.
type Record struct {
	ID                id.Evidence
	TenantID          id.Tenant
	SubjectID         id.Subject
	VerificationID    id.Verification
	RequirementKey    string
	EvidenceType      Name
	Artefact          Name
	AcquisitionMethod Name
	Assurances        []Name
	Registry          Reference
	Region            string
	RetentionClass    string
	ContentRevision   uint32
	Content           ContentRecord
	Integrity         Integrity
	State             State
	Version           int64
	CreatedAt         time.Time
	UpdatedAt         time.Time
	QuarantineReason  string
	QuarantinedAt     *time.Time
}

// Asset is one tenant-owned protected evidence artefact.
type Asset struct {
	record  Record
	content Content
}

// NewAvailable creates metadata for newly persisted and integrity-verified content.
func NewAvailable(record Record, registry Registry) (Asset, error) {
	record.Integrity = IntegrityVerified
	record.State = StateAvailable
	record.Version = 1
	record.UpdatedAt = record.CreatedAt
	record.QuarantineReason = ""
	record.QuarantinedAt = nil
	return restoreAsset(record, registry)
}

// RestoreAsset validates evidence metadata loaded from durable state.
func RestoreAsset(record Record, registry Registry) (Asset, error) {
	return restoreAsset(record, registry)
}

func restoreAsset(record Record, registry Registry) (Asset, error) {
	record.CreatedAt = record.CreatedAt.UTC()
	record.UpdatedAt = record.UpdatedAt.UTC()
	if record.ID.IsZero() || record.TenantID.IsZero() || record.SubjectID.IsZero() ||
		record.VerificationID.IsZero() || record.ContentRevision == 0 {
		return Asset{}, errors.New("evidence: identity and content revision are required")
	}
	if !validRequirementKey(record.RequirementKey) || !validClassification(record.Region) ||
		!validClassification(record.RetentionClass) {
		return Asset{}, errors.New("evidence: requirement, region, or retention class is invalid")
	}
	if record.Registry != registry.Reference() {
		return Asset{}, errors.New("evidence: immutable registry reference does not match")
	}
	assurances, err := validateAcquisition(registry, record.EvidenceType, record.Artefact,
		record.AcquisitionMethod, record.Assurances)
	if err != nil {
		return Asset{}, err
	}
	record.Assurances = assurances
	if record.Version < 1 || record.CreatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) {
		return Asset{}, errors.New("evidence: lifecycle version or times are invalid")
	}
	if err := validateLifecycle(record); err != nil {
		return Asset{}, err
	}
	context, err := AuthenticatedContext(record)
	if err != nil {
		return Asset{}, err
	}
	content, err := NewContent(record.Content, context)
	if err != nil {
		return Asset{}, err
	}
	record.Content = content.Record()

	return Asset{record: record, content: content}, nil
}

// AuthenticatedContext builds the canonical non-secret context bound to this
// evidence content revision.
func AuthenticatedContext(record Record) (platformcrypto.Context, error) {
	if record.ID.IsZero() || record.TenantID.IsZero() || record.VerificationID.IsZero() ||
		record.RequirementKey == "" || record.EvidenceType == "" || record.Artefact == "" ||
		record.AcquisitionMethod == "" || record.ContentRevision == 0 {
		return platformcrypto.Context{}, errors.New("evidence: encryption context identity is invalid")
	}
	document := struct {
		Domain            string    `json:"domain"`
		SchemaVersion     uint32    `json:"schema_version"`
		TenantID          string    `json:"tenant_id"`
		VerificationID    string    `json:"verification_id"`
		EvidenceID        string    `json:"evidence_id"`
		RequirementKey    string    `json:"requirement_key"`
		EvidenceType      Name      `json:"evidence_type"`
		Artefact          Name      `json:"artefact"`
		AcquisitionMethod Name      `json:"acquisition_method"`
		Registry          Reference `json:"registry"`
		ObjectPurpose     string    `json:"object_purpose"`
		ContentRevision   uint32    `json:"content_revision"`
	}{
		Domain: ContentEncryptionPurpose, SchemaVersion: ContentContextSchemaVersion,
		TenantID: record.TenantID.String(), VerificationID: record.VerificationID.String(),
		EvidenceID: record.ID.String(), RequirementKey: record.RequirementKey,
		EvidenceType: record.EvidenceType, Artefact: record.Artefact,
		AcquisitionMethod: record.AcquisitionMethod, Registry: record.Registry,
		ObjectPurpose:   ContentEncryptionPurpose,
		ContentRevision: record.ContentRevision,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return platformcrypto.Context{}, fmt.Errorf("encode evidence encryption context: %w", err)
	}

	return platformcrypto.NewContext(ContentContextSchemaVersion, encoded)
}

// Record returns a defensive durable copy.
func (asset Asset) Record() Record {
	record := asset.record
	record.Assurances = slices.Clone(asset.record.Assurances)
	record.Content = asset.content.Record()
	if asset.record.QuarantinedAt != nil {
		value := *asset.record.QuarantinedAt
		record.QuarantinedAt = &value
	}
	return record
}

// ID returns the public evidence identifier.
func (asset Asset) ID() id.Evidence { return asset.record.ID }

// TenantID returns the owning tenant identifier.
func (asset Asset) TenantID() id.Tenant { return asset.record.TenantID }

// State returns the current evidence lifecycle state.
func (asset Asset) State() State { return asset.record.State }

// Version returns the optimistic aggregate version.
func (asset Asset) Version() int64 { return asset.record.Version }

// Content returns protected-content metadata without raw evidence bytes.
func (asset Asset) Content() Content { return asset.content }

// CanRead reports whether controlled-read grant evaluation may proceed.
func (asset Asset) CanRead() bool {
	return asset.record.State == StateAvailable && asset.record.Integrity == IntegrityVerified
}

// Quarantine denies reads with an audited safe reason code.
func (asset Asset) Quarantine(expectedVersion int64, reason string, now time.Time) (Asset, error) {
	if expectedVersion != asset.record.Version {
		return Asset{}, ErrVersionConflict
	}
	if asset.record.State != StateAvailable || !validClassification(reason) ||
		now.IsZero() || !now.UTC().After(asset.record.UpdatedAt) {
		return Asset{}, ErrConflict
	}
	record := asset.Record()
	record.State = StateQuarantined
	record.Version++
	record.UpdatedAt = now.UTC()
	record.QuarantineReason = reason
	record.QuarantinedAt = &record.UpdatedAt

	return Asset{record: record, content: asset.content}, nil
}

// FailIntegrity quarantines content after an authenticated digest or context failure.
func (asset Asset) FailIntegrity(expectedVersion int64, reason string, now time.Time) (Asset, error) {
	quarantined, err := asset.Quarantine(expectedVersion, reason, now)
	if err != nil {
		return Asset{}, err
	}
	quarantined.record.Integrity = IntegrityFailed
	return quarantined, nil
}

func validateAcquisition(registry Registry, evidenceType, artefact, method Name, assurances []Name) ([]Name, error) {
	typeDefinition, exists := registry.EvidenceType(evidenceType)
	if !exists || !slices.Contains(typeDefinition.Artefacts, artefact) {
		return nil, errors.New("evidence: evidence type does not contain artefact")
	}
	methodDefinition, exists := registry.Method(method)
	if !exists {
		return nil, errors.New("evidence: acquisition method is not registered")
	}
	var support MethodSupport
	for _, candidate := range methodDefinition.Supports {
		if candidate.EvidenceType == evidenceType {
			support = candidate
			break
		}
	}
	if support.EvidenceType == "" || !slices.Contains(support.Artefacts, artefact) {
		return nil, errors.New("evidence: acquisition method cannot produce artefact")
	}
	validated := slices.Clone(assurances)
	slices.Sort(validated)
	validated = slices.Compact(validated)
	for _, assurance := range validated {
		if !slices.Contains(support.Assurances, assurance) {
			return nil, fmt.Errorf("evidence: acquisition method cannot establish assurance %q", assurance)
		}
	}
	return validated, nil
}

func validateLifecycle(record Record) error {
	switch record.State {
	case StateAvailable:
		if record.Integrity != IntegrityVerified || record.QuarantineReason != "" || record.QuarantinedAt != nil {
			return errors.New("evidence: available lifecycle metadata is inconsistent")
		}
	case StateQuarantined:
		if (record.Integrity != IntegrityVerified && record.Integrity != IntegrityFailed) ||
			!validClassification(record.QuarantineReason) || record.QuarantinedAt == nil ||
			record.QuarantinedAt.UTC() != record.UpdatedAt {
			return errors.New("evidence: quarantine lifecycle metadata is inconsistent")
		}
	case StateDeleted:
		if record.Integrity != IntegrityVerified && record.Integrity != IntegrityFailed {
			return errors.New("evidence: deleted lifecycle metadata is inconsistent")
		}
		if (record.QuarantineReason == "") != (record.QuarantinedAt == nil) {
			return errors.New("evidence: deleted quarantine metadata is inconsistent")
		}
		if record.QuarantinedAt != nil && (!validClassification(record.QuarantineReason) || record.QuarantinedAt.After(record.UpdatedAt)) {
			return errors.New("evidence: deleted quarantine metadata is inconsistent")
		}
	default:
		return errors.New("evidence: lifecycle state is invalid")
	}
	return nil
}

func validRequirementKey(value string) bool {
	if value == "" || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validClassification(value string) bool {
	if strings.TrimSpace(value) != value || value == "" || len(value) > maxClassificationLength {
		return false
	}
	return classificationExpression.MatchString(value)
}
