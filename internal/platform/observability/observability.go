// Package observability owns the bounded, low-cardinality domain metric
// vocabulary and the value types recorded at owning application boundaries.
// It deliberately imports no telemetry SDK, evidence content, or identifier
// package; the platform telemetry adapter implements the receiver contracts.
package observability

import (
	"strings"
	"time"
)

const other = "other"

// State is a bounded verification lifecycle state label.
type State string

// Bounded verification lifecycle state labels.
const (
	StateCreated       State = "created"
	StateCollecting    State = "collecting"
	StateProcessing    State = "processing"
	StateAwaitingInput State = "awaiting_input"
	StateAwaitingExt   State = "awaiting_external"
	StateManualReview  State = "manual_review"
	StateCompleted     State = "completed"
	StateCancelled     State = "cancelled"
	StateFailed        State = "failed"
	StateExpired       State = "expired"
	StateOther         State = other
)

// Safe returns the canonical label, collapsing unknown values.
func (state State) Safe() string {
	switch state {
	case StateCreated, StateCollecting, StateProcessing, StateAwaitingInput,
		StateAwaitingExt, StateManualReview, StateCompleted, StateCancelled,
		StateFailed, StateExpired:
		return string(state)
	default:
		return other
	}
}

// Outcome is a bounded terminal workflow or decision outcome label.
type Outcome string

// Bounded outcome labels.
const (
	OutcomeVerified     Outcome = "verified"
	OutcomeNotVerified  Outcome = "not_verified"
	OutcomeInconclusive Outcome = "inconclusive"
	OutcomeCancelled    Outcome = "cancelled"
	OutcomeExpired      Outcome = "expired"
	OutcomeFailed       Outcome = "failed"
	OutcomeCompleted    Outcome = "completed"
	OutcomeOther        Outcome = other
)

// Safe returns the canonical label, collapsing unknown values.
func (outcome Outcome) Safe() string {
	switch outcome {
	case OutcomeVerified, OutcomeNotVerified, OutcomeInconclusive, OutcomeCancelled,
		OutcomeExpired, OutcomeFailed, OutcomeCompleted:
		return string(outcome)
	default:
		return other
	}
}

// FailureClass is a bounded operational failure label.
type FailureClass string

// Bounded operational failure labels.
const (
	FailureNone        FailureClass = "none"
	FailurePolicy      FailureClass = "policy"
	FailureProvider    FailureClass = "provider"
	FailureModel       FailureClass = "model"
	FailureTimeout     FailureClass = "timeout"
	FailureTransport   FailureClass = "transport"
	FailureValidation  FailureClass = "validation"
	FailureAuthority   FailureClass = "authority"
	FailureUnavailable FailureClass = "unavailable"
	FailureInternal    FailureClass = "internal"
	FailureOther       FailureClass = other
)

// Safe returns the canonical label, collapsing unknown classes.
func (class FailureClass) Safe() string {
	switch class {
	case FailureNone, FailurePolicy, FailureProvider, FailureModel, FailureTimeout,
		FailureTransport, FailureValidation, FailureAuthority, FailureUnavailable,
		FailureInternal:
		return string(class)
	default:
		return other
	}
}

// DeliveryOutcome is a bounded webhook attempt outcome label.
type DeliveryOutcome string

// Bounded webhook attempt outcomes.
const (
	Delivered      DeliveryOutcome = "delivered"
	DeliveredDup   DeliveryOutcome = "delivered_duplicate"
	Retried        DeliveryOutcome = "retried"
	Exhausted      DeliveryOutcome = "exhausted"
	DeliveryStop   DeliveryOutcome = "cancelled"
	DeliveryReject DeliveryOutcome = "rejected"
	DeliveryOther  DeliveryOutcome = other
)

// Safe returns the canonical label, collapsing unknown outcomes.
func (outcome DeliveryOutcome) Safe() string {
	switch outcome {
	case Delivered, DeliveredDup, Retried, Exhausted, DeliveryStop, DeliveryReject:
		return string(outcome)
	default:
		return other
	}
}

// DispatchOutcome is a bounded provider or model dispatch result label.
type DispatchOutcome string

// Bounded dispatch outcomes.
const (
	DispatchCompleted    DispatchOutcome = "completed"
	DispatchFailed       DispatchOutcome = "failed"
	DispatchRecovered    DispatchOutcome = "recovered"
	DispatchDeduplicated DispatchOutcome = "deduplicated"
	DispatchUncertain    DispatchOutcome = "uncertain"
	DispatchOther        DispatchOutcome = other
)

// Safe returns the canonical label, collapsing unknown outcomes.
func (outcome DispatchOutcome) Safe() string {
	switch outcome {
	case DispatchCompleted, DispatchFailed, DispatchRecovered, DispatchDeduplicated, DispatchUncertain:
		return string(outcome)
	default:
		return other
	}
}

// HealthState is a bounded adapter readiness label.
type HealthState string

// Bounded readiness labels.
const (
	HealthReady    HealthState = "ready"
	HealthDegraded HealthState = "degraded"
	HealthNotReady HealthState = "not_ready"
	HealthUnknown  HealthState = "unknown"
)

// Safe returns the canonical label, collapsing unknown states.
func (state HealthState) Safe() string {
	switch state {
	case HealthReady, HealthDegraded, HealthNotReady:
		return string(state)
	default:
		return string(HealthUnknown)
	}
}

// DeletionState is a bounded privacy deletion workflow state label.
type DeletionState string

// Bounded privacy deletion states.
const (
	DeletionRequested  DeletionState = "requested"
	DeletionHeld       DeletionState = "blocked_by_legal_hold"
	DeletionInProgress DeletionState = "in_progress"
	DeletionAwaiting   DeletionState = "awaiting_backup_expiry"
	DeletionCompleted  DeletionState = "completed"
	DeletionFailed     DeletionState = "failed"
	DeletionOther      DeletionState = other
)

// Safe returns the canonical label, collapsing unknown states.
func (state DeletionState) Safe() string {
	switch state {
	case DeletionRequested, DeletionHeld, DeletionInProgress, DeletionAwaiting,
		DeletionCompleted, DeletionFailed:
		return string(state)
	default:
		return other
	}
}

// DataClass is a bounded deletion target class label.
type DataClass string

// Bounded deletion target classes.
const (
	DataRawEvidence     DataClass = "raw_evidence"
	DataDerivedEvidence DataClass = "derived_evidence"
	DataIdentity        DataClass = "identity"
	DataWebhookPayload  DataClass = "webhook_payload"
	DataWorkflowMeta    DataClass = "workflow_metadata"
	DataAuditRecord     DataClass = "audit_record"
	DataDeletionProof   DataClass = "deletion_proof"
	DataBackup          DataClass = "backup"
	DataMixed           DataClass = "mixed"
	DataOther           DataClass = other
)

// Safe returns the canonical label, collapsing unknown classes.
func (class DataClass) Safe() string {
	switch class {
	case DataRawEvidence, DataDerivedEvidence, DataIdentity, DataWebhookPayload,
		DataWorkflowMeta, DataAuditRecord, DataDeletionProof, DataBackup, DataMixed:
		return string(class)
	default:
		return other
	}
}

// ReviewOutcome is a bounded reviewer resolution label.
type ReviewOutcome string

// Bounded reviewer resolutions.
const (
	ReviewSatisfied     ReviewOutcome = "satisfied"
	ReviewNotSatisfied  ReviewOutcome = "not_satisfied"
	ReviewInconclusive  ReviewOutcome = "inconclusive"
	ReviewInputRequired ReviewOutcome = "input_required"
	ReviewOther         ReviewOutcome = other
)

// Safe returns the canonical label, collapsing unknown outcomes.
func (outcome ReviewOutcome) Safe() string {
	switch outcome {
	case ReviewSatisfied, ReviewNotSatisfied, ReviewInconclusive, ReviewInputRequired:
		return string(outcome)
	default:
		return other
	}
}

// Oversight is a bounded review oversight label.
type Oversight string

// Bounded review oversight labels.
const (
	OversightSingle Oversight = "single"
	OversightDual   Oversight = "dual"
	OversightOther  Oversight = other
)

// Safe returns the canonical label, collapsing unknown oversight.
func (oversight Oversight) Safe() string {
	switch oversight {
	case OversightSingle, OversightDual:
		return string(oversight)
	default:
		return other
	}
}

// CaptureStep is a bounded capture artefact step label.
type CaptureStep string

// Bounded capture artefact steps.
const (
	StepDocumentFront CaptureStep = "document_front"
	StepDocumentBack  CaptureStep = "document_back"
	StepSelfieImage   CaptureStep = "selfie_image"
	StepOther         CaptureStep = other
)

// NewCaptureStep maps a catalogue artefact name to a bounded step label.
func NewCaptureStep(artefact string) CaptureStep {
	switch artefact {
	case "idenqa.artefact.document_front":
		return StepDocumentFront
	case "idenqa.artefact.document_back":
		return StepDocumentBack
	case "idenqa.artefact.selfie_image":
		return StepSelfieImage
	default:
		return StepOther
	}
}

// Safe returns the canonical label, collapsing unknown steps.
func (step CaptureStep) Safe() string {
	switch step {
	case StepDocumentFront, StepDocumentBack, StepSelfieImage:
		return string(step)
	default:
		return other
	}
}

// CaptureOutcome is a bounded capture step outcome label.
type CaptureOutcome string

// Bounded capture step outcomes.
const (
	CaptureAccepted CaptureOutcome = "accepted"
	CaptureOther    CaptureOutcome = other
)

// Safe returns the canonical label, collapsing unknown outcomes.
func (outcome CaptureOutcome) Safe() string {
	switch outcome {
	case CaptureAccepted:
		return string(outcome)
	default:
		return other
	}
}

// RecaptureReason is a bounded recapture request label.
type RecaptureReason string

// Bounded recapture reasons.
const (
	RecaptureRequested RecaptureReason = "requested"
	RecaptureRenewed   RecaptureReason = "renewed"
	RecaptureOther     RecaptureReason = other
)

// Safe returns the canonical label, collapsing unknown reasons.
func (reason RecaptureReason) Safe() string {
	switch reason {
	case RecaptureRequested, RecaptureRenewed:
		return string(reason)
	default:
		return other
	}
}

// Provider is a provider identity label. Deployment adapter identifiers and
// contract provider identifiers are accepted; anything else collapses.
type Provider string

// Safe returns the canonical provider label.
func (provider Provider) Safe() string { return boundedIdentity(string(provider), "pvd") }

// Model is a model identity label. Deployment model names and contract model
// identifiers are accepted; anything else collapses.
type Model string

// Safe returns the canonical model label.
func (model Model) Safe() string { return boundedIdentity(string(model), "mdl") }

const ulidAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// boundedIdentity accepts a prefixed contract identifier or a bounded lowercase
// deployment name. Mixed-case identifiers such as tenant, subject, evidence,
// and attempt identifiers can never become a metric label.
func boundedIdentity(value, prefix string) string {
	if prefixedIdentifier(value, prefix) {
		return value
	}
	if value == "" || len(value) > 128 || value[0] < 'a' || value[0] > 'z' {
		return other
	}
	for _, character := range value {
		isLower := character >= 'a' && character <= 'z'
		isDigit := character >= '0' && character <= '9'
		if !isLower && !isDigit && character != '.' && character != '_' && character != '-' {
			return other
		}
	}
	return value
}

func prefixedIdentifier(value, prefix string) bool {
	if len(value) != len(prefix)+1+26 || !strings.HasPrefix(value, prefix+"_") {
		return false
	}
	for _, character := range value[len(prefix)+1:] {
		if !strings.ContainsRune(ulidAlphabet, character) {
			return false
		}
	}
	return true
}

// Region is a bounded deployment region label.
type Region string

// Safe returns the canonical region label, rejecting unbounded values.
func (region Region) Safe() string {
	value := string(region)
	if value == "" || len(value) > 63 || value[0] < 'a' || value[0] > 'z' {
		return "unknown"
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return "unknown"
		}
	}
	return value
}

// Transition is one applied verification lifecycle transition. It carries no
// tenant, subject, evidence, or transition identifier.
type Transition struct {
	From         State
	To           State
	FailureClass FailureClass
	Region       Region
}

// WorkflowCompletion is one terminal verification workflow observation.
// TimeToDecision is zero when the terminal state carries no decision.
type WorkflowCompletion struct {
	Outcome        Outcome
	Duration       time.Duration
	TimeToDecision time.Duration
	Region         Region
}

// OperationalFailure is one non-decision operational failure observation.
type OperationalFailure struct {
	FailureClass FailureClass
	Region       Region
}

// SessionStart is one accepted verification session creation.
type SessionStart struct {
	Region Region
}

// Recapture is one accepted recapture session creation.
type Recapture struct {
	Reason RecaptureReason
	Region Region
}

// CaptureEvent is one accepted capture artefact completion.
type CaptureEvent struct {
	Step    CaptureStep
	Outcome CaptureOutcome
}

// DeliveryAttempt is one committed signed webhook attempt.
type DeliveryAttempt struct {
	Outcome  DeliveryOutcome
	Attempt  int32
	Duration time.Duration
}

// ProviderDispatch is one provider dispatch claim, recovery, or completion.
type ProviderDispatch struct {
	Provider     Provider
	Outcome      DispatchOutcome
	FailureClass FailureClass
}

// ProviderCallbackDelay is the observed delay between a provider dispatch
// claim and its adopted terminal callback receipt.
type ProviderCallbackDelay struct {
	Provider Provider
	Delay    time.Duration
}

// ModelDispatch is one durable model execution result.
type ModelDispatch struct {
	Model        Model
	Outcome      DispatchOutcome
	FailureClass FailureClass
	Duration     time.Duration
}

// ModelHealth is one supervised model readiness probe result.
type ModelHealth struct {
	Model Model
	State HealthState
}

// ProviderHealth is one derived provider readiness snapshot. It carries only
// the bounded deployment provider label, the bounded state and the deployment
// region.
type ProviderHealth struct {
	Provider Provider
	State    HealthState
	Region   Region
}

// ProviderThrottle is one bounded provider admission refusal. It carries only
// the bounded deployment provider label.
type ProviderThrottle struct {
	Provider Provider
}

// DeletionTransition is one privacy deletion workflow state change.
type DeletionTransition struct {
	From   DeletionState
	To     DeletionState
	Kind   DataClass
	Region Region
}

// DeletionBacklog is one bounded count of due deletion workflows.
type DeletionBacklog struct {
	State DeletionState
	Count int64
}

// BackupExpiry is the current age of a deletion awaiting backup expiry.
type BackupExpiry struct {
	Age    time.Duration
	Region Region
}

// ReviewResolution is one reviewer case resolution observation.
type ReviewResolution struct {
	Outcome   ReviewOutcome
	Oversight Oversight
	Duration  time.Duration
	Region    Region
}

// RequestType is a bounded privacy-request type label.
type RequestType string

// Bounded privacy-request types.
const (
	RequestAccess      RequestType = "access"
	RequestPortability RequestType = "portability"
	RequestCorrection  RequestType = "correction"
	RequestRestriction RequestType = "restriction"
	RequestObjection   RequestType = "objection"
	RequestErasure     RequestType = "erasure"
	RequestOther       RequestType = other
)

// Safe returns the canonical label, collapsing unknown request types.
func (requestType RequestType) Safe() string {
	switch requestType {
	case RequestAccess, RequestPortability, RequestCorrection, RequestRestriction, RequestObjection, RequestErasure:
		return string(requestType)
	default:
		return other
	}
}

// RequestState is a bounded privacy-request workflow state label.
type RequestState string

// Bounded privacy-request states.
const (
	RequestStateRequested         RequestState = "requested"
	RequestStateInReview          RequestState = "in_review"
	RequestStateApproved          RequestState = "approved"
	RequestStatePartiallyApproved RequestState = "partially_approved"
	RequestStateDenied            RequestState = "denied"
	RequestStateExecuting         RequestState = "executing"
	RequestStateCompleted         RequestState = "completed"
	RequestStateFailed            RequestState = "failed"
	RequestStateWithdrawn         RequestState = "withdrawn"
	RequestStateExpired           RequestState = "expired"
	RequestStateOther             RequestState = other
)

// Safe returns the canonical label, collapsing unknown states.
func (state RequestState) Safe() string {
	switch state {
	case RequestStateRequested, RequestStateInReview, RequestStateApproved,
		RequestStatePartiallyApproved, RequestStateDenied, RequestStateExecuting,
		RequestStateCompleted, RequestStateFailed, RequestStateWithdrawn, RequestStateExpired:
		return string(state)
	default:
		return other
	}
}

// PrivacyRequestTransition is one privacy-request workflow state change.
type PrivacyRequestTransition struct {
	Type   RequestType
	From   RequestState
	To     RequestState
	Region Region
}

// PrivacyRequestAge is the current age of one open privacy-request workflow.
type PrivacyRequestAge struct {
	Type    RequestType
	State   RequestState
	Overdue bool
	Age     time.Duration
}
