// Package provider defines the public, transport-independent provider adapter
// contract. It contains references and classifications only; credentials and
// raw evidence bytes are deliberately not representable.
package provider

import (
	"context"
	"time"
)

const (
	// MajorVersion changes only for incompatible contract revisions.
	MajorVersion uint16 = 1
	// MinorVersion increases for backwards-compatible contract revisions.
	MinorVersion uint16 = 1
	// MaxResultBytes is the largest encoded provider result accepted by v1.
	MaxResultBytes = 256 * 1024
	// MaximumMRZLines bounds machine-readable-zone lines in one observation.
	MaximumMRZLines = 3
	// MaximumMRZLineBytes bounds one machine-readable-zone line (ICAO TD3).
	MaximumMRZLineBytes = 44
	// MaximumBarcodePayloadBytes bounds one already-decoded barcode payload.
	MaximumBarcodePayloadBytes = 4096
	// MaximumDocumentFields bounds provider-extracted fields in one observation.
	MaximumDocumentFields = 32
	// MaximumDocumentFieldBytes bounds one extracted field name or value.
	MaximumDocumentFieldBytes = 128
)

// Version identifies one compatible contract revision.
type Version struct {
	Major uint16 `json:"major"`
	Minor uint16 `json:"minor"`
}

// CurrentVersion is the contract implemented by this package.
var CurrentVersion = Version{Major: MajorVersion, Minor: MinorVersion}

// Accepts reports whether this implementation can consume requested.
func (version Version) Accepts(requested Version) bool {
	return version.Major != 0 && version.Major == requested.Major && version.Minor >= requested.Minor
}

// PackageProvenance pins the adapter package used for an attempt.
type PackageProvenance struct {
	AdapterID      string  `json:"adapter_id"`
	AdapterVersion string  `json:"adapter_version"`
	PackageDigest  string  `json:"package_digest"`
	Contract       Version `json:"contract"`
}

// ConfigurationSchema identifies the secret-free configuration shape.
type ConfigurationSchema struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

// Capability declares one stable check supported by an adapter.
type Capability struct {
	Check                string   `json:"check"`
	AcceptedEvidence     []string `json:"accepted_evidence"`
	AcceptedInputs       []string `json:"accepted_inputs,omitempty"`
	AcceptedAssurances   []string `json:"accepted_assurances"`
	ProcessingRegions    []string `json:"processing_regions"`
	SupportsIdempotency  bool     `json:"supports_idempotency"`
	SupportsCancellation bool     `json:"supports_cancellation"`
}

// Restrictions declares bounded execution needs enforced by runner policy.
type Restrictions struct {
	NetworkRequired   bool          `json:"network_required"`
	MaximumGrants     uint16        `json:"maximum_grants"`
	MaximumResultSize uint32        `json:"maximum_result_size"`
	MaximumDuration   time.Duration `json:"maximum_duration"`
}

// Manifest is the immutable capability and restriction advertisement.
type Manifest struct {
	Package       PackageProvenance   `json:"package"`
	Configuration ConfigurationSchema `json:"configuration"`
	Capabilities  []Capability        `json:"capabilities"`
	Restrictions  Restrictions        `json:"restrictions"`
}

// ConfigurationReference names externally resolved provider credentials and
// settings. The referenced values never enter this contract.
type ConfigurationReference struct {
	ProviderID        string `json:"provider_id"`
	SchemaDigest      string `json:"schema_digest"`
	SecretReference   string `json:"secret_reference"`
	CredentialVersion string `json:"credential_version"`
}

// EvidenceGrantReference authorises one purpose-bound evidence stream through
// the existing controlled-read service. It is not an object-store location.
type EvidenceGrantReference struct {
	GrantID      string    `json:"grant_id"`
	RedemptionID string    `json:"redemption_id"`
	EvidenceID   string    `json:"evidence_id"`
	Purpose      string    `json:"purpose"`
	Variant      string    `json:"variant"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// InputReference names one purpose-bound, externally resolved structured
// input. The value itself never crosses the public runner envelope.
type InputReference struct {
	Name      string `json:"name"`
	Reference string `json:"reference"`
}

// TraceContext carries W3C propagation fields without installing an OTel SDK.
type TraceContext struct {
	Traceparent string `json:"traceparent,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
}

// Request is the complete immutable provider attempt envelope.
type Request struct {
	Contract          Version                  `json:"contract"`
	AttemptID         string                   `json:"attempt_id"`
	ProviderID        string                   `json:"provider_id"`
	TenantID          string                   `json:"tenant_id"`
	VerificationID    string                   `json:"verification_id"`
	Check             string                   `json:"check"`
	IdempotencyKey    string                   `json:"idempotency_key"`
	CallbackReference string                   `json:"callback_reference,omitempty"`
	Adapter           PackageProvenance        `json:"adapter"`
	Capability        Capability               `json:"capability"`
	Restrictions      Restrictions             `json:"restrictions"`
	Configuration     ConfigurationReference   `json:"configuration"`
	Inputs            []InputReference         `json:"inputs,omitempty"`
	Evidence          []EvidenceGrantReference `json:"evidence"`
	Deadline          time.Time                `json:"deadline"`
	Trace             TraceContext             `json:"trace"`
}

// SignalOutcome is a stable provider observation classification.
type SignalOutcome string

// Supported provider signal outcomes.
const (
	SignalOutcomeSatisfied    SignalOutcome = "satisfied"
	SignalOutcomeNotSatisfied SignalOutcome = "not_satisfied"
	SignalOutcomeInconclusive SignalOutcome = "inconclusive"
)

// Signal is one bounded, normalised provider observation.
type Signal struct {
	Name        string        `json:"name"`
	Outcome     SignalOutcome `json:"outcome"`
	ReasonCodes []string      `json:"reason_codes"`
}

// ResultOutcome distinguishes execution success from operational failure.
type ResultOutcome string

// Supported provider result outcomes.
const (
	ResultOutcomeCompleted ResultOutcome = "completed"
	ResultOutcomeFailed    ResultOutcome = "failed"
)

// FailureClass is stable across provider-specific error vocabularies.
type FailureClass string

// Stable provider failure classes.
const (
	FailureInvalidRequest   FailureClass = "invalid_request"
	FailureUnauthenticated  FailureClass = "unauthenticated"
	FailureUnauthorized     FailureClass = "unauthorized"
	FailureUnsupported      FailureClass = "unsupported"
	FailureUnavailable      FailureClass = "unavailable"
	FailureRateLimited      FailureClass = "rate_limited"
	FailureDeadline         FailureClass = "deadline_exceeded"
	FailureCancelled        FailureClass = "cancelled"
	FailureProviderRejected FailureClass = "provider_rejected"
	FailureInternal         FailureClass = "internal"
)

// RetryDisposition tells the workflow owner whether a retry is meaningful.
type RetryDisposition string

// Supported provider retry dispositions.
const (
	RetryNever     RetryDisposition = "never"
	RetryBackoff   RetryDisposition = "backoff"
	RetryReconcile RetryDisposition = "reconcile"
)

// Failure is a redacted, stable execution failure.
type Failure struct {
	Class      FailureClass     `json:"class"`
	Code       string           `json:"code"`
	Retry      RetryDisposition `json:"retry"`
	RetryAfter time.Duration    `json:"retry_after,omitempty"`
}

// DocumentField is one provider-extracted document field before canonical
// Core analysis. It carries a bounded technical key and an extracted value.
type DocumentField struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// DocumentObservation is transient provider-extracted document data. It may
// traverse TLS and process memory, is consumed by Core document analysis, and
// must never be logged, persisted, audited, or bound into a digest.
type DocumentObservation struct {
	MRZLines       []string        `json:"mrz_lines,omitempty"`
	BarcodePayload string          `json:"barcode_payload,omitempty"`
	Fields         []DocumentField `json:"fields,omitempty"`
}

// Result is the bounded terminal result for one exact attempt.
type Result struct {
	Contract    Version              `json:"contract"`
	AttemptID   string               `json:"attempt_id"`
	Outcome     ResultOutcome        `json:"outcome"`
	Signals     []Signal             `json:"signals"`
	Failure     *Failure             `json:"failure,omitempty"`
	Document    *DocumentObservation `json:"document,omitempty"`
	CompletedAt time.Time            `json:"completed_at"`
}

// HealthState is a safe lifecycle classification without topology details.
type HealthState string

// Supported provider health states.
const (
	HealthReady    HealthState = "ready"
	HealthDegraded HealthState = "degraded"
	HealthNotReady HealthState = "not_ready"
)

// Health is a bounded adapter health snapshot.
type Health struct {
	State     HealthState `json:"state"`
	Code      string      `json:"code"`
	CheckedAt time.Time   `json:"checked_at"`
}

// ManifestProvider advertises immutable adapter capabilities.
type ManifestProvider interface {
	Manifest(context.Context) (Manifest, error)
}

// ConfigurationValidator validates a reference after runner-side resolution.
type ConfigurationValidator interface {
	ValidateConfiguration(context.Context, ConfigurationReference) error
}

// Executor executes exactly one retry-safe provider request.
type Executor interface {
	Execute(context.Context, Request) (Result, error)
}

// HealthChecker reports adapter readiness using safe classifications.
type HealthChecker interface {
	Health(context.Context) (Health, error)
}

// Adapter is the complete v1 provider boundary composed from narrow ports.
type Adapter interface {
	ManifestProvider
	ConfigurationValidator
	Executor
	HealthChecker
}
