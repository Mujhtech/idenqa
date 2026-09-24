// Package model defines the public, transport-independent model adapter
// contract. Inputs are scoped evidence-grant references; raw evidence,
// credentials, model weights, and unrestricted provider responses are absent.
package model

import (
	"context"
	"time"
)

// Version and result-size bounds for the v1 model contract.
const (
	MajorVersion   uint16 = 1
	MinorVersion   uint16 = 1
	MaxResultBytes        = 256 * 1024
)

// Version identifies one compatible model contract revision.
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

// Provenance pins every execution-relevant model artefact.
type Provenance struct {
	ModelID             string  `json:"model_id"`
	ModelVersion        string  `json:"model_version"`
	ModelDigest         string  `json:"model_digest"`
	RuntimeDigest       string  `json:"runtime_digest"`
	PreprocessingDigest string  `json:"preprocessing_digest"`
	OutputSchemaDigest  string  `json:"output_schema_digest"`
	Contract            Version `json:"contract"`
}

// Capability declares one stable evaluation supported by a model.
type Capability struct {
	Evaluation         string   `json:"evaluation"`
	AcceptedEvidence   []string `json:"accepted_evidence"`
	RequiredAssurances []string `json:"required_assurances"`
	OutputSignals      []string `json:"output_signals"`
	TemporalEvidence   bool     `json:"temporal_evidence"`
}

// Restrictions pins resource policy for one model execution.
type Restrictions struct {
	NetworkAllowed bool `json:"network_allowed"`
	// PersistDerivedData must remain false in v1. Embeddings, extracted
	// portraits, tensors, crops and model caches are workload-local transient
	// values and may not cross the model result boundary.
	PersistDerivedData bool          `json:"persist_derived_data"`
	DerivedRetention   time.Duration `json:"derived_retention"`
	MaximumGrants      uint16        `json:"maximum_grants"`
	MaximumInputBytes  uint64        `json:"maximum_input_bytes"`
	MaximumResultSize  uint32        `json:"maximum_result_size"`
	MaximumDuration    time.Duration `json:"maximum_duration"`
}

// Manifest is the immutable model capability and resource advertisement.
type Manifest struct {
	Provenance   Provenance   `json:"provenance"`
	Capabilities []Capability `json:"capabilities"`
	Restrictions Restrictions `json:"restrictions"`
}

// ConfigurationReference names immutable runtime configuration outside the request.
type ConfigurationReference struct {
	ModelID             string `json:"model_registration_id"`
	ConfigurationDigest string `json:"configuration_digest"`
	ConfigurationRef    string `json:"configuration_reference"`
}

// EvidenceGrantReference names one purpose-bound controlled-read capability.
type EvidenceGrantReference struct {
	GrantID      string    `json:"grant_id"`
	RedemptionID string    `json:"redemption_id"`
	EvidenceID   string    `json:"evidence_id"`
	Purpose      string    `json:"purpose"`
	Variant      string    `json:"variant"`
	ExpiresAt    time.Time `json:"expires_at"`
}

// TraceContext carries W3C propagation fields without coupling to an OTel SDK.
type TraceContext struct {
	Traceparent string `json:"traceparent,omitempty"`
	Tracestate  string `json:"tracestate,omitempty"`
}

// Request is the immutable, retry-safe model execution envelope.
type Request struct {
	Contract       Version                  `json:"contract"`
	AttemptID      string                   `json:"attempt_id"`
	ModelID        string                   `json:"model_registration_id"`
	TenantID       string                   `json:"tenant_id"`
	VerificationID string                   `json:"verification_id"`
	Evaluation     string                   `json:"evaluation"`
	IdempotencyKey string                   `json:"idempotency_key"`
	Provenance     Provenance               `json:"provenance"`
	Capability     Capability               `json:"capability"`
	Restrictions   Restrictions             `json:"restrictions"`
	Configuration  ConfigurationReference   `json:"configuration"`
	Evidence       []EvidenceGrantReference `json:"evidence"`
	Sequences      []EvidenceSequence       `json:"sequences,omitempty"`
	Deadline       time.Time                `json:"deadline"`
	Trace          TraceContext             `json:"trace"`
}

// EvidenceSequence binds an ordered digest chain to the exact grant used for
// each frame. It is provenance metadata, not client-authored assurance.
type EvidenceSequence struct {
	SequenceDigest string                  `json:"sequence_digest"`
	Frames         []EvidenceSequenceFrame `json:"frames"`
}

// EvidenceSequenceFrame binds one ordered frame to its exact evidence grant.
type EvidenceSequenceFrame struct {
	GrantID        string    `json:"grant_id"`
	ChallengeID    string    `json:"challenge_id"`
	Index          uint16    `json:"index"`
	CapturedAt     time.Time `json:"captured_at"`
	PreviousDigest string    `json:"previous_digest,omitempty"`
	ContentDigest  string    `json:"content_digest"`
}

// SignalOutcome classifies a bounded model observation.
type SignalOutcome string

// Supported model signal outcomes.
const (
	SignalOutcomeSatisfied    SignalOutcome = "satisfied"
	SignalOutcomeNotSatisfied SignalOutcome = "not_satisfied"
	SignalOutcomeInconclusive SignalOutcome = "inconclusive"
)

// Signal is a stable model output. Threshold application remains pinned policy.
type Signal struct {
	Name        string         `json:"name"`
	Outcome     SignalOutcome  `json:"outcome"`
	ReasonCodes []string       `json:"reason_codes"`
	Quality     *SignalQuality `json:"quality,omitempty"`
}

// SignalQuality reports only bounded capture suitability. Unacceptable input
// is always inconclusive; it is never a negative identity conclusion.
type SignalQuality struct {
	Acceptable bool     `json:"acceptable"`
	Codes      []string `json:"codes"`
}

// ResultOutcome distinguishes completed inference from operational failure.
type ResultOutcome string

// Supported model result outcomes.
const (
	ResultOutcomeCompleted ResultOutcome = "completed"
	ResultOutcomeFailed    ResultOutcome = "failed"
)

// FailureClass is stable across runtimes and model implementations.
type FailureClass string

// Stable model failure classes.
const (
	FailureInvalidInput      FailureClass = "invalid_input"
	FailureUnauthenticated   FailureClass = "unauthenticated"
	FailureUnauthorized      FailureClass = "unauthorized"
	FailureUnsupported       FailureClass = "unsupported"
	FailureUnavailable       FailureClass = "unavailable"
	FailureResourceExhausted FailureClass = "resource_exhausted"
	FailureDeadline          FailureClass = "deadline_exceeded"
	FailureCancelled         FailureClass = "cancelled"
	FailureInternal          FailureClass = "internal"
)

// RetryDisposition tells the workflow owner whether a retry is meaningful.
type RetryDisposition string

// Supported model retry dispositions.
const (
	RetryNever     RetryDisposition = "never"
	RetryBackoff   RetryDisposition = "backoff"
	RetryReconcile RetryDisposition = "reconcile"
)

// Failure is a stable, redacted model execution failure.
type Failure struct {
	Class      FailureClass     `json:"class"`
	Code       string           `json:"code"`
	Retry      RetryDisposition `json:"retry"`
	RetryAfter time.Duration    `json:"retry_after,omitempty"`
}

// Result is the bounded terminal result for one exact attempt.
type Result struct {
	Contract    Version       `json:"contract"`
	AttemptID   string        `json:"attempt_id"`
	Outcome     ResultOutcome `json:"outcome"`
	Signals     []Signal      `json:"signals"`
	Failure     *Failure      `json:"failure,omitempty"`
	CompletedAt time.Time     `json:"completed_at"`
}

// HealthState is a safe lifecycle classification.
type HealthState string

// Supported model health states.
const (
	HealthReady    HealthState = "ready"
	HealthDegraded HealthState = "degraded"
	HealthNotReady HealthState = "not_ready"
)

// Health is a bounded model-adapter health snapshot.
type Health struct {
	State     HealthState `json:"state"`
	Code      string      `json:"code"`
	CheckedAt time.Time   `json:"checked_at"`
}

// ManifestProvider advertises immutable model capabilities.
type ManifestProvider interface {
	Manifest(context.Context) (Manifest, error)
}

// ConfigurationValidator validates a reference after runner-side resolution.
type ConfigurationValidator interface {
	ValidateConfiguration(context.Context, ConfigurationReference) error
}

// Executor executes exactly one retry-safe model request.
type Executor interface {
	Execute(context.Context, Request) (Result, error)
}

// HealthChecker reports model readiness using safe classifications.
type HealthChecker interface {
	Health(context.Context) (Health, error)
}

// Adapter composes the narrow ports required by the v1 model runner.
type Adapter interface {
	ManifestProvider
	ConfigurationValidator
	Executor
	HealthChecker
}
