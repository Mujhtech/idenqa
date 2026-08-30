package evidence

import (
	"crypto/subtle"
	"errors"
	"mime"
	"slices"
	"strings"
	"time"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
)

const (
	// MediaTypeJPEG is the initial JPEG upload media type.
	MediaTypeJPEG = "image/jpeg"
	// MediaTypePNG is the initial PNG upload media type.
	MediaTypePNG = "image/png"

	// DefaultUploadMaximumBytes is the default deployment ceiling per artefact.
	DefaultUploadMaximumBytes int64 = 16 << 20
	// MinimumUploadMaximumBytes is the smallest configurable deployment ceiling.
	MinimumUploadMaximumBytes int64 = 1 << 20
	// MaximumUploadMaximumBytes is the hard v1 deployment ceiling.
	MaximumUploadMaximumBytes int64 = 64 << 20

	// DefaultUploadIntentLifetime is the default time available to start and finish an upload.
	DefaultUploadIntentLifetime = 15 * time.Minute
	// MinimumUploadIntentLifetime is the shortest configurable intent lifetime.
	MinimumUploadIntentLifetime = 5 * time.Minute
	// MaximumUploadIntentLifetime is the longest configurable intent lifetime.
	MaximumUploadIntentLifetime = 60 * time.Minute

	// DefaultUploadAttemptTimeout bounds one claimed whole-body attempt.
	DefaultUploadAttemptTimeout = 10 * time.Minute
	// MinimumUploadAttemptTimeout is the shortest configurable attempt lease.
	MinimumUploadAttemptTimeout = time.Minute
	// MaximumUploadAttemptTimeout is the longest configurable attempt lease.
	MaximumUploadAttemptTimeout = 15 * time.Minute

	// OperationCreateUpload is the durable idempotency operation for intent creation.
	OperationCreateUpload = "evidence.uploads.create"
	// EventEvidenceReady is emitted only after protected evidence becomes durable.
	EventEvidenceReady = "evidence.ready.v1"
)

var (
	// ErrUploadNotFound deliberately also covers cross-tenant upload misses.
	ErrUploadNotFound = errors.New("evidence: upload not found")
	// ErrUploadConflict identifies an invalid upload lifecycle or fencing transition.
	ErrUploadConflict = errors.New("evidence: upload conflict")
	// ErrUploadVersionConflict identifies an optimistic upload-version mismatch.
	ErrUploadVersionConflict = errors.New("evidence: upload version precondition failed")
	// ErrUploadExpired identifies an intent whose absolute lifetime has elapsed.
	ErrUploadExpired = errors.New("evidence: upload expired")
	// ErrUploadMetadata identifies request metadata that does not match an intent.
	ErrUploadMetadata = errors.New("evidence: upload metadata does not match intent")
	// ErrUploadBodyLength identifies a body whose observed size differs from its intent.
	ErrUploadBodyLength = errors.New("evidence: upload body length mismatch")
	// ErrUploadBodyDigest identifies a body whose independently observed digest differs.
	ErrUploadBodyDigest = errors.New("evidence: upload body digest mismatch")
	// ErrUploadSignature identifies bytes that do not carry the declared file signature.
	ErrUploadSignature = errors.New("evidence: upload file signature mismatch")
	// ErrUploadBodyIncomplete identifies a stream whose consumer stopped before validation.
	ErrUploadBodyIncomplete = errors.New("evidence: upload body validation incomplete")
	// ErrUploadAcceptanceOutcomeUnknown prevents destructive compensation until
	// authoritative acceptance state has been reconciled.
	ErrUploadAcceptanceOutcomeUnknown = errors.New("evidence: upload acceptance outcome unknown")
)

// UploadState is the durable lifecycle state of one evidence-upload intent.
type UploadState string

const (
	// UploadStateIssued permits one whole-body attempt to be claimed.
	UploadStateIssued UploadState = "issued"
	// UploadStateUploading has one current fenced attempt.
	UploadStateUploading UploadState = "uploading"
	// UploadStateAccepted means the exact evidence artefact was committed.
	UploadStateAccepted UploadState = "accepted"
	// UploadStateRejected means validation terminally rejected the artefact.
	UploadStateRejected UploadState = "rejected"
	// UploadStateExpired means no further attempt may begin.
	UploadStateExpired UploadState = "expired"
)

// UploadPolicyConfig contains deployment-level upload safety limits.
type UploadPolicyConfig struct {
	MaximumBytes      int64
	IntentLifetime    time.Duration
	AttemptTimeout    time.Duration
	AllowedMediaTypes []string
}

// UploadPolicy is a validated immutable deployment policy.
type UploadPolicy struct {
	maximumBytes      int64
	intentLifetime    time.Duration
	attemptTimeout    time.Duration
	allowedMediaTypes []string
}

// DefaultUploadPolicy returns the selected safe E-03 defaults.
func DefaultUploadPolicy() UploadPolicy {
	return UploadPolicy{
		maximumBytes: DefaultUploadMaximumBytes, intentLifetime: DefaultUploadIntentLifetime,
		attemptTimeout:    DefaultUploadAttemptTimeout,
		allowedMediaTypes: []string{MediaTypeJPEG, MediaTypePNG},
	}
}

// NewUploadPolicy validates deployment limits. V1 deployments may narrow but
// cannot expand the initial JPEG/PNG format contract or hard byte ceiling.
func NewUploadPolicy(config UploadPolicyConfig) (UploadPolicy, error) {
	if config.MaximumBytes < MinimumUploadMaximumBytes || config.MaximumBytes > MaximumUploadMaximumBytes ||
		config.IntentLifetime < MinimumUploadIntentLifetime || config.IntentLifetime > MaximumUploadIntentLifetime ||
		config.AttemptTimeout < MinimumUploadAttemptTimeout || config.AttemptTimeout > MaximumUploadAttemptTimeout ||
		config.IntentLifetime%time.Millisecond != 0 || config.AttemptTimeout%time.Millisecond != 0 {
		return UploadPolicy{}, errors.New("evidence: upload policy limits are invalid")
	}
	mediaTypes, err := validateUploadMediaTypes(config.AllowedMediaTypes)
	if err != nil {
		return UploadPolicy{}, err
	}

	return UploadPolicy{
		maximumBytes: config.MaximumBytes, intentLifetime: config.IntentLifetime,
		attemptTimeout: config.AttemptTimeout, allowedMediaTypes: mediaTypes,
	}, nil
}

// MaximumBytes returns the deployment ceiling available for tenant narrowing.
func (policy UploadPolicy) MaximumBytes() int64 { return policy.maximumBytes }

// IntentLifetime returns the maximum lifetime of a newly issued upload intent.
func (policy UploadPolicy) IntentLifetime() time.Duration { return policy.intentLifetime }

// AttemptTimeout returns the maximum lifetime of one claimed whole-body attempt.
func (policy UploadPolicy) AttemptTimeout() time.Duration { return policy.attemptTimeout }

// AllowedMediaTypes returns a defensive copy of the deployment allow-list.
func (policy UploadPolicy) AllowedMediaTypes() []string {
	return slices.Clone(policy.allowedMediaTypes)
}

// UploadInput is one already-resolved, authorised immutable upload binding.
// Transport credentials and raw evidence bytes are deliberately absent.
type UploadInput struct {
	ID                id.Upload
	TenantID          id.Tenant
	CaptureTokenID    id.CaptureToken
	SubjectID         id.Subject
	VerificationID    id.Verification
	EvidenceID        id.Evidence
	AuthorityID       id.Authority
	ResponseID        id.Acknowledgement
	ProfileID         id.Profile
	ProfileRevision   uint32
	ProfileDigest     string
	RequirementKey    string
	Purpose           Name
	EvidenceType      Name
	Artefact          Name
	AcquisitionMethod Name
	FallbackCondition string
	Assurances        []Name
	AllowedMediaTypes []string
	MaximumBytes      int64
	ExpectedBytes     int64
	ExpectedDigest    string
	MediaType         string
	Region            string
	RetentionClass    string
	CreatedAt         time.Time
	SessionExpiresAt  time.Time
}

// UploadRecord is the complete durable, secret-free upload representation.
type UploadRecord struct {
	ID                id.Upload
	TenantID          id.Tenant
	CaptureTokenID    id.CaptureToken
	SubjectID         id.Subject
	VerificationID    id.Verification
	EvidenceID        id.Evidence
	AuthorityID       id.Authority
	ResponseID        id.Acknowledgement
	ProfileID         id.Profile
	ProfileRevision   uint32
	ProfileDigest     string
	Registry          Reference
	RequirementKey    string
	Purpose           Name
	EvidenceType      Name
	Artefact          Name
	AcquisitionMethod Name
	FallbackCondition string
	Assurances        []Name
	EncryptionPurpose string
	AllowedMediaTypes []string
	MaximumBytes      int64
	ExpectedBytes     int64
	ExpectedDigest    string
	MediaType         string
	Region            string
	RetentionClass    string
	State             UploadState
	Version           int64
	Attempt           uint32
	AttemptTimeout    time.Duration
	LeaseExpiresAt    *time.Time
	CreatedAt         time.Time
	UpdatedAt         time.Time
	ExpiresAt         time.Time
	AcceptedAt        *time.Time
	RejectionReason   string
}

// Upload is one requirement-bound evidence-upload intent.
type Upload struct{ record UploadRecord }

// UploadCreateMutation couples one resolved intent to its durable replay scope.
type UploadCreateMutation struct {
	Upload      Upload
	Idempotency idempotency.Request
}

// UploadAcceptance is the complete secret-free mutation for atomically making
// one protected artefact available and accepting its current fenced upload.
type UploadAcceptance struct {
	UploadID        id.Upload
	CaptureTokenID  id.CaptureToken
	ExpectedVersion int64
	Attempt         uint32
	Asset           Asset
	PlaintextBytes  int64
	EventID         id.Event
	OccurredAt      time.Time
}

// NewUpload creates an issued intent capped by both deployment policy and the
// verification-session expiry.
func NewUpload(input UploadInput, registry Registry, policy UploadPolicy) (Upload, error) {
	createdAt := input.CreatedAt.UTC()
	sessionExpiresAt := input.SessionExpiresAt.UTC()
	if policy.isZero() || createdAt.IsZero() || !sessionExpiresAt.After(createdAt) {
		return Upload{}, errors.New("evidence: upload policy or lifetime is invalid")
	}
	mediaTypes, err := validateUploadMediaTypes(input.AllowedMediaTypes)
	if err != nil {
		return Upload{}, err
	}
	assurances, err := validateAcquisition(
		registry, input.EvidenceType, input.Artefact, input.AcquisitionMethod, input.Assurances,
	)
	if err != nil {
		return Upload{}, err
	}
	if input.MaximumBytes < 1 || input.MaximumBytes > policy.maximumBytes ||
		input.ExpectedBytes < 1 || input.ExpectedBytes > input.MaximumBytes ||
		!uploadMediaTypesAreSubset(mediaTypes, policy.allowedMediaTypes) ||
		!slices.Contains(policy.allowedMediaTypes, input.MediaType) ||
		!slices.Contains(mediaTypes, input.MediaType) {
		return Upload{}, errors.New("evidence: upload media or size is not permitted")
	}
	expiresAt := createdAt.Add(policy.intentLifetime)
	if sessionExpiresAt.Before(expiresAt) {
		expiresAt = sessionExpiresAt
	}
	record := UploadRecord{
		ID: input.ID, TenantID: input.TenantID, CaptureTokenID: input.CaptureTokenID,
		SubjectID: input.SubjectID, VerificationID: input.VerificationID, EvidenceID: input.EvidenceID,
		AuthorityID: input.AuthorityID, ResponseID: input.ResponseID,
		ProfileID: input.ProfileID, ProfileRevision: input.ProfileRevision, ProfileDigest: input.ProfileDigest,
		Registry: registry.Reference(), RequirementKey: input.RequirementKey, Purpose: input.Purpose,
		EvidenceType: input.EvidenceType, Artefact: input.Artefact,
		AcquisitionMethod: input.AcquisitionMethod, FallbackCondition: input.FallbackCondition,
		Assurances:        assurances,
		EncryptionPurpose: ContentEncryptionPurpose,
		AllowedMediaTypes: mediaTypes, MaximumBytes: input.MaximumBytes,
		ExpectedBytes: input.ExpectedBytes, ExpectedDigest: input.ExpectedDigest,
		MediaType: input.MediaType, Region: input.Region, RetentionClass: input.RetentionClass,
		State: UploadStateIssued, Version: 1, AttemptTimeout: policy.attemptTimeout,
		CreatedAt: createdAt, UpdatedAt: createdAt, ExpiresAt: expiresAt,
	}

	return RestoreUpload(record, registry)
}

// RestoreUpload validates an upload loaded from durable state.
func RestoreUpload(record UploadRecord, registry Registry) (Upload, error) {
	record.CreatedAt = record.CreatedAt.UTC()
	record.UpdatedAt = record.UpdatedAt.UTC()
	record.ExpiresAt = record.ExpiresAt.UTC()
	if record.LeaseExpiresAt != nil {
		value := record.LeaseExpiresAt.UTC()
		record.LeaseExpiresAt = &value
	}
	if record.AcceptedAt != nil {
		value := record.AcceptedAt.UTC()
		record.AcceptedAt = &value
	}
	if err := validateUploadRecord(record, registry); err != nil {
		return Upload{}, err
	}
	record.Assurances = slices.Clone(record.Assurances)
	record.AllowedMediaTypes = slices.Clone(record.AllowedMediaTypes)

	return Upload{record: record}, nil
}

// Record returns a defensive durable copy.
func (upload Upload) Record() UploadRecord {
	record := upload.record
	record.Assurances = slices.Clone(record.Assurances)
	record.AllowedMediaTypes = slices.Clone(record.AllowedMediaTypes)
	if record.LeaseExpiresAt != nil {
		value := *record.LeaseExpiresAt
		record.LeaseExpiresAt = &value
	}
	if record.AcceptedAt != nil {
		value := *record.AcceptedAt
		record.AcceptedAt = &value
	}

	return record
}

// ID returns the upload-intent identifier.
func (upload Upload) ID() id.Upload { return upload.record.ID }

// TenantID returns the owning tenant identifier.
func (upload Upload) TenantID() id.Tenant { return upload.record.TenantID }

// State returns the current upload lifecycle state.
func (upload Upload) State() UploadState { return upload.record.State }

// Version returns the optimistic aggregate version and fencing token.
func (upload Upload) Version() int64 { return upload.record.Version }

// Attempt returns the current or most recently claimed whole-body attempt ordinal.
func (upload Upload) Attempt() uint32 { return upload.record.Attempt }

// ClaimAttempt claims a new whole-body attempt. An expired lease may be fenced
// by a later attempt without trusting process-local state.
func (upload Upload) ClaimAttempt(expectedVersion int64, now time.Time) (Upload, error) {
	if expectedVersion != upload.record.Version {
		return Upload{}, ErrUploadVersionConflict
	}
	now = now.UTC()
	if now.IsZero() || now.Before(upload.record.UpdatedAt) {
		return Upload{}, ErrUploadConflict
	}
	if !now.Before(upload.record.ExpiresAt) {
		return Upload{}, ErrUploadExpired
	}
	if upload.record.State == UploadStateUploading && upload.record.LeaseExpiresAt != nil &&
		now.Before(*upload.record.LeaseExpiresAt) {
		return Upload{}, ErrUploadConflict
	}
	if upload.record.State != UploadStateIssued && upload.record.State != UploadStateUploading {
		return Upload{}, ErrUploadConflict
	}
	record := upload.Record()
	record.State = UploadStateUploading
	record.Version++
	record.Attempt++
	record.UpdatedAt = now
	leaseExpiresAt := now.Add(record.AttemptTimeout)
	if record.ExpiresAt.Before(leaseExpiresAt) {
		leaseExpiresAt = record.ExpiresAt
	}
	record.LeaseExpiresAt = &leaseExpiresAt

	return Upload{record: record}, nil
}

// FailAttempt releases the exact current attempt for a complete-body retry.
func (upload Upload) FailAttempt(expectedVersion int64, attempt uint32, now time.Time) (Upload, error) {
	if expectedVersion != upload.record.Version {
		return Upload{}, ErrUploadVersionConflict
	}
	if upload.record.State != UploadStateUploading || upload.record.Attempt != attempt {
		return Upload{}, ErrUploadConflict
	}
	now = now.UTC()
	if now.IsZero() || now.Before(upload.record.UpdatedAt) {
		return Upload{}, ErrUploadConflict
	}
	record := upload.Record()
	record.Version++
	record.UpdatedAt = now
	record.LeaseExpiresAt = nil
	if !now.Before(record.ExpiresAt) {
		record.State = UploadStateExpired
	} else {
		record.State = UploadStateIssued
	}

	return Upload{record: record}, nil
}

// Accept commits the exact protected evidence binding produced by the current
// fenced attempt. Durable adapters must atomically persist both aggregates and
// the evidence-ready outbox intent.
func (upload Upload) Accept(
	expectedVersion int64,
	attempt uint32,
	asset Asset,
	plaintextBytes int64,
	now time.Time,
) (Upload, error) {
	if expectedVersion != upload.record.Version {
		return Upload{}, ErrUploadVersionConflict
	}
	now = now.UTC()
	if upload.record.State != UploadStateUploading || upload.record.Attempt != attempt ||
		upload.record.LeaseExpiresAt == nil || now.IsZero() ||
		now.Before(upload.record.UpdatedAt) ||
		!now.Before(*upload.record.LeaseExpiresAt) || !now.Before(upload.record.ExpiresAt) ||
		plaintextBytes != upload.record.ExpectedBytes || !upload.binds(asset) {
		return Upload{}, ErrUploadConflict
	}
	assetRecord := asset.Record()
	if subtle.ConstantTimeCompare(
		[]byte(assetRecord.Content.PlaintextDigest), []byte(upload.record.ExpectedDigest),
	) != 1 || assetRecord.Content.MediaType != upload.record.MediaType {
		return Upload{}, ErrUploadConflict
	}
	record := upload.Record()
	record.State = UploadStateAccepted
	record.Version++
	record.UpdatedAt = now
	record.LeaseExpiresAt = nil
	record.AcceptedAt = &now

	return Upload{record: record}, nil
}

// Reject terminally records a safe validation reason for the exact current attempt.
func (upload Upload) Reject(
	expectedVersion int64,
	attempt uint32,
	reason string,
	now time.Time,
) (Upload, error) {
	if expectedVersion != upload.record.Version {
		return Upload{}, ErrUploadVersionConflict
	}
	now = now.UTC()
	if upload.record.State != UploadStateUploading || upload.record.Attempt != attempt ||
		upload.record.LeaseExpiresAt == nil || now.IsZero() ||
		now.Before(upload.record.UpdatedAt) ||
		!now.Before(*upload.record.LeaseExpiresAt) || !now.Before(upload.record.ExpiresAt) ||
		!validClassification(reason) {
		return Upload{}, ErrUploadConflict
	}
	record := upload.Record()
	record.State = UploadStateRejected
	record.Version++
	record.UpdatedAt = now
	record.LeaseExpiresAt = nil
	record.RejectionReason = reason

	return Upload{record: record}, nil
}

// Expire terminally closes an unaccepted intent at its absolute boundary.
func (upload Upload) Expire(expectedVersion int64, now time.Time) (Upload, error) {
	if expectedVersion != upload.record.Version {
		return Upload{}, ErrUploadVersionConflict
	}
	now = now.UTC()
	if now.IsZero() || now.Before(upload.record.ExpiresAt) ||
		(upload.record.State != UploadStateIssued && upload.record.State != UploadStateUploading) {
		return Upload{}, ErrUploadConflict
	}
	record := upload.Record()
	record.State = UploadStateExpired
	record.Version++
	record.UpdatedAt = now
	record.LeaseExpiresAt = nil

	return Upload{record: record}, nil
}

func (upload Upload) binds(asset Asset) bool {
	record := asset.Record()
	return record.ID == upload.record.EvidenceID && record.TenantID == upload.record.TenantID &&
		record.SubjectID == upload.record.SubjectID && record.VerificationID == upload.record.VerificationID &&
		record.RequirementKey == upload.record.RequirementKey && record.Registry == upload.record.Registry &&
		record.EvidenceType == upload.record.EvidenceType && record.Artefact == upload.record.Artefact &&
		record.AcquisitionMethod == upload.record.AcquisitionMethod &&
		slices.Equal(record.Assurances, upload.record.Assurances) && record.Region == upload.record.Region &&
		record.RetentionClass == upload.record.RetentionClass && record.ContentRevision == 1 &&
		record.State == StateAvailable && record.Integrity == IntegrityVerified && record.Version == 1
}

func (policy UploadPolicy) isZero() bool {
	return policy.maximumBytes == 0 || policy.intentLifetime == 0 || policy.attemptTimeout == 0 ||
		len(policy.allowedMediaTypes) == 0
}

func validateUploadRecord(record UploadRecord, registry Registry) error {
	if record.ID.IsZero() || record.TenantID.IsZero() || record.CaptureTokenID.IsZero() ||
		record.SubjectID.IsZero() || record.VerificationID.IsZero() || record.EvidenceID.IsZero() ||
		record.AuthorityID.IsZero() || record.ResponseID.IsZero() || record.ProfileID.IsZero() ||
		record.ProfileRevision == 0 || !validRequirementKey(record.RequirementKey) {
		return errors.New("evidence: upload identity binding is invalid")
	}
	if record.Registry != registry.Reference() || !registry.Has(KindPurpose, record.Purpose) {
		return errors.New("evidence: upload registry or purpose is invalid")
	}
	if !validUploadFallbackCondition(record.FallbackCondition) {
		return errors.New("evidence: upload fallback condition is invalid")
	}
	if _, err := platformcrypto.NewDigest(record.ProfileDigest); err != nil {
		return errors.New("evidence: upload profile digest is invalid")
	}
	if _, err := platformcrypto.NewDigest(record.ExpectedDigest); err != nil {
		return errors.New("evidence: upload content digest is invalid")
	}
	assurances, err := validateAcquisition(
		registry, record.EvidenceType, record.Artefact, record.AcquisitionMethod, record.Assurances,
	)
	if err != nil || !slices.Equal(assurances, record.Assurances) {
		if err == nil {
			err = errors.New("evidence: upload assurances are not canonical")
		}
		return err
	}
	mediaTypes, err := validateUploadMediaTypes(record.AllowedMediaTypes)
	if err != nil || !slices.Equal(mediaTypes, record.AllowedMediaTypes) ||
		!validCanonicalUploadMediaType(record.MediaType) || !slices.Contains(mediaTypes, record.MediaType) {
		return errors.New("evidence: upload media binding is invalid")
	}
	if record.MaximumBytes < 1 || record.MaximumBytes > MaximumUploadMaximumBytes ||
		record.ExpectedBytes < 1 || record.ExpectedBytes > record.MaximumBytes ||
		record.EncryptionPurpose != ContentEncryptionPurpose ||
		record.AttemptTimeout < MinimumUploadAttemptTimeout || record.AttemptTimeout > MaximumUploadAttemptTimeout ||
		!validClassification(record.Region) || !validClassification(record.RetentionClass) {
		return errors.New("evidence: upload limits or placement are invalid")
	}
	if record.Version < 1 || record.CreatedAt.IsZero() || record.UpdatedAt.Before(record.CreatedAt) ||
		!record.ExpiresAt.After(record.CreatedAt) ||
		record.ExpiresAt.After(record.CreatedAt.Add(MaximumUploadIntentLifetime)) {
		return errors.New("evidence: upload lifecycle timing is invalid")
	}
	if err := validateUploadState(record); err != nil {
		return err
	}

	return nil
}

func validUploadFallbackCondition(value string) bool {
	return value == "" || value == "capability_unavailable" || value == "method_unavailable" ||
		value == "capture_failed"
}

func validateUploadState(record UploadRecord) error {
	switch record.State {
	case UploadStateIssued:
		if record.LeaseExpiresAt != nil || record.AcceptedAt != nil || record.RejectionReason != "" ||
			!record.UpdatedAt.Before(record.ExpiresAt) {
			return ErrUploadConflict
		}
	case UploadStateUploading:
		if record.Attempt == 0 || record.LeaseExpiresAt == nil || record.AcceptedAt != nil ||
			record.RejectionReason != "" || !record.LeaseExpiresAt.After(record.UpdatedAt) ||
			record.LeaseExpiresAt.After(record.ExpiresAt) {
			return ErrUploadConflict
		}
	case UploadStateAccepted:
		if record.Attempt == 0 || record.LeaseExpiresAt != nil || record.AcceptedAt == nil ||
			*record.AcceptedAt != record.UpdatedAt || record.RejectionReason != "" ||
			!record.UpdatedAt.Before(record.ExpiresAt) {
			return ErrUploadConflict
		}
	case UploadStateRejected:
		if record.Attempt == 0 || record.LeaseExpiresAt != nil || record.AcceptedAt != nil ||
			!validClassification(record.RejectionReason) || !record.UpdatedAt.Before(record.ExpiresAt) {
			return ErrUploadConflict
		}
	case UploadStateExpired:
		if record.LeaseExpiresAt != nil || record.AcceptedAt != nil || record.RejectionReason != "" ||
			record.UpdatedAt.Before(record.ExpiresAt) {
			return ErrUploadConflict
		}
	default:
		return errors.New("evidence: upload state is invalid")
	}

	return nil
}

func validateUploadMediaTypes(values []string) ([]string, error) {
	if len(values) == 0 || len(values) > 2 {
		return nil, errors.New("evidence: upload media types are invalid")
	}
	result := slices.Clone(values)
	slices.Sort(result)
	result = slices.Compact(result)
	if len(result) != len(values) {
		return nil, errors.New("evidence: upload media types contain duplicates")
	}
	for _, value := range result {
		if !validCanonicalUploadMediaType(value) {
			return nil, errors.New("evidence: upload media type is unsupported")
		}
	}

	return result, nil
}

func validCanonicalUploadMediaType(value string) bool {
	mediaType, parameters, err := mime.ParseMediaType(value)
	return err == nil && len(parameters) == 0 && strings.ToLower(mediaType) == value &&
		(value == MediaTypeJPEG || value == MediaTypePNG)
}

func uploadMediaTypesAreSubset(values, allowed []string) bool {
	for _, value := range values {
		if !slices.Contains(allowed, value) {
			return false
		}
	}

	return true
}
