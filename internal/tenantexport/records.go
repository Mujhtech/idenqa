package tenantexport

import (
	"encoding/json"
	"time"
)

// TenantRecord is safe tenant lifecycle metadata.
type TenantRecord struct {
	ID         string     `json:"id"`
	State      string     `json:"state"`
	Version    int64      `json:"version"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	DisabledAt *time.Time `json:"disabled_at,omitempty"`
}

// CaptureProfileRecord is one capture profile with its published revision pin.
type CaptureProfileRecord struct {
	ID                string     `json:"id"`
	Name              string     `json:"name"`
	State             string     `json:"state"`
	Version           int64      `json:"version"`
	LatestRevision    int32      `json:"latest_revision"`
	DraftRevision     *int32     `json:"draft_revision,omitempty"`
	PublishedRevision *int32     `json:"published_revision,omitempty"`
	PublishedDigest   string     `json:"published_digest,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	DeactivatedAt     *time.Time `json:"deactivated_at,omitempty"`
}

// PolicyRecord is one policy catalog row and its current activation state.
type PolicyRecord struct {
	ID                string    `json:"id"`
	ActivationVersion int64     `json:"activation_version"`
	ActiveRevision    *int64    `json:"active_revision,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// PolicyRevisionRecord is immutable revision metadata without policy bytes.
type PolicyRevisionRecord struct {
	PolicyID        string    `json:"policy_id"`
	Revision        int64     `json:"revision"`
	SchemaMajor     int32     `json:"schema_major"`
	SchemaMinor     int32     `json:"schema_minor"`
	Digest          string    `json:"digest"`
	EvaluatorMajor  int32     `json:"evaluator_major"`
	EvaluatorMinor  int32     `json:"evaluator_minor"`
	EvaluatorDigest string    `json:"evaluator_digest"`
	CreatedAt       time.Time `json:"created_at"`
}

// PolicyActivationRecord is one immutable activation transition.
type PolicyActivationRecord struct {
	PolicyID          string    `json:"policy_id"`
	ActivationVersion int64     `json:"activation_version"`
	Revision          int64     `json:"revision"`
	PreviousRevision  *int64    `json:"previous_revision,omitempty"`
	ActorID           string    `json:"actor_id"`
	ActivatedAt       time.Time `json:"activated_at"`
}

// VerificationRecord is the immutable verification session snapshot.
type VerificationRecord struct {
	ID                  string          `json:"id"`
	State               string          `json:"state"`
	Version             int64           `json:"version"`
	ProfileID           string          `json:"profile_id"`
	ProfileRevision     int32           `json:"profile_revision"`
	ProfileDigest       string          `json:"profile_digest"`
	PolicyID            string          `json:"policy_id,omitempty"`
	DecisionID          string          `json:"decision_id,omitempty"`
	Region              string          `json:"region,omitempty"`
	Requirements        json.RawMessage `json:"requirements"`
	FailureClass        string          `json:"failure_class,omitempty"`
	FailureCode         string          `json:"failure_code,omitempty"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	ExpiresAt           time.Time       `json:"expires_at"`
	CaptureCompletedAt  *time.Time      `json:"capture_completed_at,omitempty"`
	CompletedDecisionID string          `json:"completed_decision_id,omitempty"`
	ExpiryDiscoveredAt  *time.Time      `json:"expiry_discovered_at,omitempty"`
}

// VerificationTransitionRecord is one immutable lifecycle receipt.
type VerificationTransitionRecord struct {
	EventID          string    `json:"event_id"`
	VerificationID   string    `json:"verification_id"`
	FromState        string    `json:"from_state"`
	ToState          string    `json:"to_state"`
	ExpectedVersion  int64     `json:"expected_version"`
	ResultingVersion int64     `json:"resulting_version"`
	DecisionID       string    `json:"decision_id,omitempty"`
	ActorID          string    `json:"actor_id"`
	CommandDigest    string    `json:"command_digest"`
	OccurredAt       time.Time `json:"occurred_at"`
}

// VerificationCheckRecord is safe check metadata.
type VerificationCheckRecord struct {
	ID             string    `json:"id"`
	VerificationID string    `json:"verification_id"`
	Name           string    `json:"name"`
	State          string    `json:"state"`
	Outcome        string    `json:"outcome,omitempty"`
	Version        int64     `json:"version"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// VerificationAttemptRecord is immutable attempt provenance without diagnostics.
type VerificationAttemptRecord struct {
	ID                     string     `json:"id"`
	VerificationID         string     `json:"verification_id"`
	CheckID                string     `json:"check_id"`
	AttemptNumber          int32      `json:"attempt_number"`
	Fence                  int64      `json:"fence"`
	RunnerKind             string     `json:"runner_kind"`
	RunnerID               string     `json:"runner_id"`
	RunnerVersion          string     `json:"runner_version"`
	PackageDigest          string     `json:"package_digest"`
	ContractMajor          int32      `json:"contract_major"`
	ContractMinor          int32      `json:"contract_minor"`
	RequestDigest          string     `json:"request_digest"`
	ConfigurationDigest    string     `json:"configuration_digest"`
	State                  string     `json:"state"`
	StartedAt              time.Time  `json:"started_at"`
	Deadline               time.Time  `json:"deadline"`
	FinishedAt             *time.Time `json:"finished_at,omitempty"`
	FailureClass           string     `json:"failure_class,omitempty"`
	FailureCode            string     `json:"failure_code,omitempty"`
	RetryDisposition       string     `json:"retry_disposition,omitempty"`
	RetryAfterMilliseconds int64      `json:"retry_after_milliseconds,omitempty"`
	ResultDigest           string     `json:"result_digest,omitempty"`
}

// DecisionRecord carries the exact byte-canonical portable decision bundle.
type DecisionRecord struct {
	DecisionID     string          `json:"decision_id"`
	VerificationID string          `json:"verification_id"`
	DecisionDigest string          `json:"decision_digest"`
	BundleDigest   string          `json:"bundle_digest"`
	Directive      string          `json:"directive"`
	Outcome        string          `json:"outcome"`
	Assurance      string          `json:"assurance,omitempty"`
	Actor          string          `json:"actor"`
	Supersedes     string          `json:"supersedes,omitempty"`
	DecidedAt      time.Time       `json:"decided_at"`
	Bundle         json.RawMessage `json:"bundle"`
}

// AuditChainRecord is one immutable reference-only audit chain entry.
type AuditChainRecord struct {
	Sequence     int64     `json:"sequence"`
	EventID      string    `json:"event_id"`
	EventType    string    `json:"event_type"`
	AggregateID  string    `json:"aggregate_id"`
	ActorID      string    `json:"actor_id"`
	OccurredAt   time.Time `json:"occurred_at"`
	EventDigest  string    `json:"event_digest"`
	PreviousHash string    `json:"previous_hash,omitempty"`
	Hash         string    `json:"hash"`
}

// WebhookEndpointRecord is endpoint configuration without signing secrets.
type WebhookEndpointRecord struct {
	ID                       string     `json:"id"`
	URL                      string     `json:"url"`
	EventTypes               []string   `json:"event_types"`
	SchemaVersion            string     `json:"schema_version"`
	Version                  int64      `json:"version"`
	SecretVersion            int64      `json:"secret_version"`
	PreviousSecretValidUntil *time.Time `json:"previous_secret_valid_until,omitempty"`
	DisabledAt               *time.Time `json:"disabled_at,omitempty"`
	DisabledReason           string     `json:"disabled_reason,omitempty"`
	CreatedAt                time.Time  `json:"created_at"`
	UpdatedAt                time.Time  `json:"updated_at"`
}

// ReviewCaseRecord is safe manual review case metadata.
type ReviewCaseRecord struct {
	ID                    string          `json:"id"`
	VerificationID        string          `json:"verification_id"`
	ChallengedDecisionID  string          `json:"challenged_decision_id"`
	SupersedingDecisionID string          `json:"superseding_decision_id,omitempty"`
	Region                string          `json:"region"`
	RequiredCertification string          `json:"required_certification"`
	Oversight             string          `json:"oversight"`
	State                 string          `json:"state"`
	AssignedReviewer      string          `json:"assigned_reviewer,omitempty"`
	PermittedFindings     json.RawMessage `json:"permitted_findings"`
	Version               int64           `json:"version"`
	CreatedAt             time.Time       `json:"created_at"`
	UpdatedAt             time.Time       `json:"updated_at"`
}

// ReviewFindingRecord is immutable finding metadata with grant references.
type ReviewFindingRecord struct {
	ID               string          `json:"id"`
	CaseID           string          `json:"case_id"`
	ReviewerID       string          `json:"reviewer_id"`
	Resolution       string          `json:"resolution"`
	ReasonCode       string          `json:"reason_code"`
	EvidenceGrantIDs json.RawMessage `json:"evidence_grant_ids"`
	RecordedAt       time.Time       `json:"recorded_at"`
}

// IdentitySubjectRecord is persistent subject metadata without ciphertext.
type IdentitySubjectRecord struct {
	ID                   string     `json:"id"`
	Region               string     `json:"region"`
	State                string     `json:"state"`
	Version              int64      `json:"version"`
	HasExternalReference bool       `json:"has_external_reference"`
	DeletionID           string     `json:"deletion_id,omitempty"`
	CreatedAt            time.Time  `json:"created_at"`
	UpdatedAt            time.Time  `json:"updated_at"`
	ErasedAt             *time.Time `json:"erased_at,omitempty"`
}

// IdentityRecordRecord is safe identity metadata, value mask, and digests.
type IdentityRecordRecord struct {
	ID                  string    `json:"id"`
	SubjectID           string    `json:"subject_id"`
	VerificationID      string    `json:"verification_id"`
	Kind                string    `json:"kind"`
	Name                string    `json:"name"`
	Sequence            int64     `json:"sequence"`
	SeriesID            string    `json:"series_id"`
	Supersedes          string    `json:"supersedes,omitempty"`
	MetadataDigest      string    `json:"metadata_digest"`
	HasValue            bool      `json:"has_value"`
	IdentifierNamespace string    `json:"identifier_namespace,omitempty"`
	IdentifierIssuer    string    `json:"identifier_issuer,omitempty"`
	IdentifierRegion    string    `json:"identifier_region,omitempty"`
	RecordedAt          time.Time `json:"recorded_at"`
	RetainUntil         time.Time `json:"retain_until"`
}

// EvidenceAssetRecord is evidence asset metadata without ciphertext or keys.
type EvidenceAssetRecord struct {
	ID                 string    `json:"id"`
	SubjectID          string    `json:"subject_id"`
	VerificationID     string    `json:"verification_id"`
	RequirementKey     string    `json:"requirement_key"`
	EvidenceType       string    `json:"evidence_type"`
	Artefact           string    `json:"artefact"`
	AcquisitionMethod  string    `json:"acquisition_method"`
	Assurances         []string  `json:"assurances"`
	Region             string    `json:"region"`
	RetentionClass     string    `json:"retention_class"`
	ContentRevision    int32     `json:"content_revision"`
	ObjectKey          string    `json:"object_key"`
	ObjectVersion      string    `json:"object_version"`
	CiphertextSize     int64     `json:"ciphertext_size"`
	CiphertextChecksum string    `json:"ciphertext_checksum"`
	PlaintextDigest    string    `json:"plaintext_digest"`
	MediaType          string    `json:"media_type"`
	Integrity          string    `json:"integrity"`
	State              string    `json:"state"`
	Version            int64     `json:"version"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// FraudConfigurationRecord is one versioned tenant fraud rule revision.
type FraudConfigurationRecord struct {
	Version       int64           `json:"version"`
	Configuration json.RawMessage `json:"configuration"`
	Digest        string          `json:"digest"`
	ActorID       string          `json:"actor_id"`
	RecordedAt    time.Time       `json:"recorded_at"`
}

// PrivacyDeletionRecord is one deletion workflow status row.
type PrivacyDeletionRecord struct {
	ID                     string     `json:"id"`
	AggregateID            string     `json:"aggregate_id"`
	Region                 string     `json:"region"`
	State                  string     `json:"state"`
	BackupRetentionSeconds int64      `json:"backup_retention_seconds"`
	BackupExpiresAt        time.Time  `json:"backup_expires_at"`
	FailureClass           string     `json:"failure_class,omitempty"`
	Version                int64      `json:"version"`
	RequestedAt            time.Time  `json:"requested_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
	CompletedAt            *time.Time `json:"completed_at,omitempty"`
}

// PrivacyHoldRecord is one legal-hold status row.
type PrivacyHoldRecord struct {
	ID          string     `json:"id"`
	AggregateID string     `json:"aggregate_id"`
	Authority   string     `json:"authority"`
	Reason      string     `json:"reason"`
	StartsAt    time.Time  `json:"starts_at"`
	ReviewAt    time.Time  `json:"review_at"`
	ReleasedAt  *time.Time `json:"released_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}
