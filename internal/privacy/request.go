package privacy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// RequestType is the closed data-subject privacy-request vocabulary.
type RequestType string

// The selected privacy-request types.
const (
	RequestAccess      RequestType = "access"
	RequestPortability RequestType = "portability"
	RequestCorrection  RequestType = "correction"
	RequestRestriction RequestType = "restriction"
	RequestObjection   RequestType = "objection"
	RequestErasure     RequestType = "erasure"
)

var requestTypes = []RequestType{
	RequestAccess, RequestPortability, RequestCorrection,
	RequestRestriction, RequestObjection, RequestErasure,
}

// Valid reports whether the request type is in the selected vocabulary.
func (value RequestType) Valid() bool { return slices.Contains(requestTypes, value) }

// Deterministic reports whether an approved request is executed by an
// idempotent effect rather than decided again.
func (value RequestType) Deterministic() bool { return value.Valid() }

// Channel identifies who submitted the request. The tenant is the controller.
type Channel string

// The selected submission channels.
const (
	ChannelTenantAPI      Channel = "tenant_api"
	ChannelSubjectOutcome Channel = "subject_outcome"
)

// Valid reports whether the channel is in the selected vocabulary.
func (value Channel) Valid() bool {
	return value == ChannelTenantAPI || value == ChannelSubjectOutcome
}

// RequestState is the immutable-workflow state vocabulary.
type RequestState string

// The selected privacy-request states.
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
)

var requestStates = []RequestState{
	RequestStateRequested, RequestStateInReview, RequestStateApproved,
	RequestStatePartiallyApproved, RequestStateDenied, RequestStateExecuting,
	RequestStateCompleted, RequestStateFailed, RequestStateWithdrawn,
	RequestStateExpired,
}

// Valid reports whether the state is in the selected vocabulary.
func (value RequestState) Valid() bool { return slices.Contains(requestStates, value) }

// Terminal reports whether the state is immutable. Terminal states never
// transition, even by the tenant that approved them.
func (value RequestState) Terminal() bool {
	return value == RequestStateDenied || value == RequestStateCompleted ||
		value == RequestStateWithdrawn || value == RequestStateExpired
}

// Decided reports whether a decision outcome has been recorded.
func (value RequestState) Decided() bool {
	return value == RequestStateApproved || value == RequestStatePartiallyApproved || value == RequestStateDenied
}

// DecisionOutcome is the closed decision-outcome vocabulary.
type DecisionOutcome string

// The selected decision outcomes.
const (
	OutcomeApproved          DecisionOutcome = "approved"
	OutcomePartiallyApproved DecisionOutcome = "partially_approved"
	OutcomeDenied            DecisionOutcome = "denied"
)

// Valid reports whether the outcome is in the selected vocabulary.
func (value DecisionOutcome) Valid() bool {
	return value == OutcomeApproved || value == OutcomePartiallyApproved || value == OutcomeDenied
}

// state returns the request state produced by the outcome.
func (value DecisionOutcome) state() RequestState {
	switch value {
	case OutcomeApproved:
		return RequestStateApproved
	case OutcomePartiallyApproved:
		return RequestStatePartiallyApproved
	default:
		return RequestStateDenied
	}
}

// requestTransitions is the selected transition graph. A transition that is
// not listed fails with ErrConflict.
var requestTransitions = map[RequestState][]RequestState{
	RequestStateRequested:         {RequestStateInReview, RequestStateWithdrawn, RequestStateExpired},
	RequestStateInReview:          {RequestStateApproved, RequestStatePartiallyApproved, RequestStateDenied, RequestStateWithdrawn, RequestStateExpired},
	RequestStateApproved:          {RequestStateExecuting},
	RequestStatePartiallyApproved: {RequestStateExecuting},
	RequestStateExecuting:         {RequestStateCompleted, RequestStateFailed},
	RequestStateFailed:            {RequestStateExecuting},
}

// canTransition reports whether the selected graph permits from -> to.
func canTransition(from, to RequestState) bool {
	return slices.Contains(requestTransitions[from], to)
}

// ReasonCode is a bounded audit reason from the selected vocabulary.
type ReasonCode string

// The selected approve reason codes.
const (
	ReasonAccessApproved       ReasonCode = "access_approved"
	ReasonPortabilityApproved  ReasonCode = "portability_approved"
	ReasonCorrectionApproved   ReasonCode = "correction_approved"
	ReasonRestrictionApproved  ReasonCode = "restriction_approved"
	ReasonObjectionApproved    ReasonCode = "objection_approved"
	ReasonErasureApproved      ReasonCode = "erasure_approved"
	ReasonPartiallyApproved    ReasonCode = "partially_approved"
	ReasonWithdrawnByTenant    ReasonCode = "withdrawn_by_tenant"
	ReasonExpiredWithoutAction ReasonCode = "expired_without_decision"
)

// The selected deny reason codes.
const (
	ReasonIdentityUnverified  ReasonCode = "identity_unverified"
	ReasonInsufficientProof   ReasonCode = "insufficient_proof"
	ReasonLegalObligation     ReasonCode = "legal_obligation"
	ReasonThirdPartyRights    ReasonCode = "third_party_rights"
	ReasonManifestlyUnfounded ReasonCode = "manifestly_unfounded"
	ReasonExcessiveRequest    ReasonCode = "excessive_request"
)

var approveReasonCodes = []ReasonCode{
	ReasonAccessApproved, ReasonPortabilityApproved, ReasonCorrectionApproved,
	ReasonRestrictionApproved, ReasonObjectionApproved, ReasonErasureApproved,
	ReasonPartiallyApproved,
}

var denyReasonCodes = []ReasonCode{
	ReasonIdentityUnverified, ReasonInsufficientProof, ReasonLegalObligation,
	ReasonThirdPartyRights, ReasonManifestlyUnfounded, ReasonExcessiveRequest,
}

// ValidApproval reports whether the reason code is an allowed approval reason.
func (value ReasonCode) ValidApproval() bool {
	return slices.Contains(approveReasonCodes, value) || value.ValidWithdrawal() || value.ValidExpiry()
}

// ValidWithdrawal reports whether the reason code is the selected withdrawal reason.
func (value ReasonCode) ValidWithdrawal() bool { return value == ReasonWithdrawnByTenant }

// ValidExpiry reports whether the reason code is the selected expiry reason.
func (value ReasonCode) ValidExpiry() bool { return value == ReasonExpiredWithoutAction }

// ValidDenial reports whether the reason code is an allowed denial reason.
func (value ReasonCode) ValidDenial() bool { return slices.Contains(denyReasonCodes, value) }

// Event is one append-only privacy-request audit event. Detail carries only
// bounded reference material chosen by the owning transition.
type Event struct {
	Sequence    int64
	Type        string
	From        RequestState
	To          RequestState
	ReasonCode  ReasonCode
	ActorDigest string
	Detail      string
	Digest      string
	OccurredAt  time.Time
}

// Decision is one immutable decision statement. Actor is the authenticated
// principal reference; it is never a subject credential.
type Decision struct {
	ID         id.PrivacyDecision
	Outcome    DecisionOutcome
	ReasonCode ReasonCode
	Actor      string
	DecidedAt  time.Time
	Version    int64
}

// Request is one replay-safe, optimistic-concurrency workflow. The
// payload is canonical JSON validated against the selected request type; it
// never carries raw evidence bytes.
type Request struct {
	ID             id.PrivacyRequest
	Type           RequestType
	State          RequestState
	Channel        Channel
	SubjectID      string
	VerificationID string
	Region         string
	Payload        []byte
	ReasonCode     ReasonCode
	FailureClass   string
	EffectKind     string
	EffectRef      string
	EffectDigest   string
	ExpiresAt      time.Time
	RequestedAt    time.Time
	UpdatedAt      time.Time
	Version        int64
	Decisions      []Decision
	Events         []Event
}

// RequestPayload is the bounded type-specific request instruction. It is
// serialized as canonical JSON with unknown fields rejected.
type RequestPayload struct {
	Purpose       string `json:"purpose,omitempty"`
	RecordID      string `json:"record_id,omitempty"`
	DecisionID    string `json:"decision_id,omitempty"`
	Name          string `json:"name,omitempty"`
	Value         string `json:"value,omitempty"`
	Kind          string `json:"kind,omitempty"`
	Normalization string `json:"normalization,omitempty"`
}

// ValidatePayload decodes and validates the bounded instruction against the
// selected request type.
func ValidatePayload(requestType RequestType, payload []byte) (RequestPayload, error) {
	if !requestType.Valid() || len(payload) == 0 || len(payload) > 4096 {
		return RequestPayload{}, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var value RequestPayload
	if err := decoder.Decode(&value); err != nil || decoder.More() {
		return RequestPayload{}, ErrInvalid
	}
	if value.Purpose != "" && !token(value.Purpose, 128) {
		return RequestPayload{}, ErrInvalid
	}
	switch requestType {
	case RequestCorrection:
		if value.Purpose != "" {
			return RequestPayload{}, ErrInvalid
		}
		if value.RecordID != "" && value.DecisionID != "" {
			return RequestPayload{}, ErrInvalid
		}
		if value.RecordID == "" && value.DecisionID == "" {
			return RequestPayload{}, ErrInvalid
		}
		if value.DecisionID != "" {
			if value.Name != "" || value.Value != "" || value.Kind != "" || value.Normalization != "" {
				return RequestPayload{}, ErrInvalid
			}
			if _, err := id.ParseDecision(value.DecisionID); err != nil {
				return RequestPayload{}, ErrInvalid
			}
			return value, nil
		}
		if !ValidIdentityRecordID(value.RecordID) || !token(value.Name, 128) || value.Value == "" || len(value.Value) > 1024 ||
			(value.Kind != "observation" && value.Kind != "claim" && value.Kind != "fact") ||
			(value.Normalization != "identity.exact.v1" && value.Normalization != "identity.trim.v1" && value.Normalization != "identity.ascii_upper.v1") {
			return RequestPayload{}, ErrInvalid
		}
	case RequestObjection:
		if value.Purpose == "" || value.RecordID != "" || value.DecisionID != "" || value.Name != "" ||
			value.Value != "" || value.Kind != "" || value.Normalization != "" {
			return RequestPayload{}, ErrInvalid
		}
	case RequestRestriction:
		if value.RecordID != "" || value.DecisionID != "" || value.Name != "" || value.Value != "" ||
			value.Kind != "" || value.Normalization != "" {
			return RequestPayload{}, ErrInvalid
		}
	case RequestAccess, RequestPortability, RequestErasure:
		if value.Purpose != "" || value.RecordID != "" || value.DecisionID != "" || value.Name != "" ||
			value.Value != "" || value.Kind != "" || value.Normalization != "" {
			return RequestPayload{}, ErrInvalid
		}
	default:
		return RequestPayload{}, ErrInvalid
	}
	return value, nil
}

// canonicalPayload validates and canonicalizes one bounded instruction payload.
func canonicalPayload(requestType RequestType, payload []byte) ([]byte, error) {
	if _, err := ValidatePayload(requestType, payload); err != nil {
		return nil, err
	}
	var value RequestPayload
	if err := json.Unmarshal(payload, &value); err != nil {
		return nil, ErrInvalid
	}
	return json.Marshal(value)
}

// ValidIdentityRecordID accepts the four owned identity record prefixes.
func ValidIdentityRecordID(value string) bool {
	for _, prefix := range []id.Prefix{"obs", "fct", "clm", "idi"} {
		if _, err := id.Parse(prefix, value); err == nil {
			return true
		}
	}
	return false
}

// NewPrivacyRequest creates one requested workflow.
func NewPrivacyRequest(identifier id.PrivacyRequest, requestType RequestType, channel Channel, subjectID, verificationID, region string, payload []byte, requestedAt, expiresAt time.Time) (Request, error) {
	request := Request{
		ID: identifier, Type: requestType, State: RequestStateRequested, Channel: channel,
		SubjectID: subjectID, VerificationID: verificationID, Region: region,
		Payload: append([]byte(nil), payload...), RequestedAt: requestedAt, UpdatedAt: requestedAt,
		ExpiresAt: expiresAt, Version: 1,
	}
	if request.Validate() != nil {
		return Request{}, ErrInvalid
	}
	return request, nil
}

// Validate checks identity, channel, bounded instruction, and lifecycle times.
func (request Request) Validate() error {
	if request.ID.IsZero() || !request.Type.Valid() || !request.State.Valid() || !request.Channel.Valid() ||
		(request.SubjectID != "" && !token(request.SubjectID, 200)) || !validRegion(request.Region) || request.Version < 1 ||
		request.RequestedAt.IsZero() || request.RequestedAt.Location() != time.UTC ||
		request.UpdatedAt.Before(request.RequestedAt) || request.UpdatedAt.Location() != time.UTC ||
		!request.ExpiresAt.After(request.RequestedAt) || request.ExpiresAt.Location() != time.UTC {
		return ErrInvalid
	}
	if request.VerificationID != "" {
		if _, err := id.ParseVerification(request.VerificationID); err != nil {
			return ErrInvalid
		}
	}
	if request.Channel == ChannelSubjectOutcome && request.VerificationID == "" {
		return ErrInvalid
	}
	if _, err := ValidatePayload(request.Type, request.Payload); err != nil {
		return ErrInvalid
	}
	if request.ReasonCode != "" && !token(string(request.ReasonCode), 64) {
		return ErrInvalid
	}
	if request.FailureClass != "" && !token(request.FailureClass, 64) {
		return ErrInvalid
	}
	if request.State.Terminal() && len(request.Events) == 0 {
		return ErrInvalid
	}
	return nil
}

// transition applies one graph transition and appends its audit event.
func (request Request) transition(to RequestState, reason ReasonCode, actorDigest, detail, eventType string, now time.Time) (Request, error) {
	if request.Validate() != nil || now.IsZero() || now.Location() != time.UTC || now.Before(request.UpdatedAt) || !canTransition(request.State, to) {
		return Request{}, ErrConflict
	}
	next := request
	next.State, next.ReasonCode, next.UpdatedAt, next.Version = to, reason, now, request.Version+1
	next.Payload = append([]byte(nil), request.Payload...)
	next.Events = append(append([]Event(nil), request.Events...), Event{
		Sequence: int64(len(request.Events) + 1), Type: eventType, From: request.State, To: to,
		ReasonCode: reason, ActorDigest: actorDigest, Detail: detail, OccurredAt: now,
	})
	next.Decisions = append([]Decision(nil), request.Decisions...)
	return next, nil
}

// BeginReview moves a requested workflow into review. Replay is idempotent.
func (request Request) BeginReview(actorDigest string, now time.Time) (Request, error) {
	if request.Validate() != nil {
		return Request{}, ErrInvalid
	}
	if request.State == RequestStateInReview {
		return request.withPayloadCopy(), nil
	}
	if request.State != RequestStateRequested {
		return Request{}, ErrConflict
	}
	return request.transition(RequestStateInReview, "", actorDigest, "", "privacy.request.review_started", now)
}

// Decide records one immutable decision. Replay of the same outcome and reason
// is idempotent; a conflicting replay fails.
func (request Request) Decide(identifier id.PrivacyDecision, outcome DecisionOutcome, reason ReasonCode, actor string, now time.Time) (Request, error) {
	if request.Validate() != nil || identifier.IsZero() || !outcome.Valid() || !token(actor, 200) || now.IsZero() || now.Location() != time.UTC {
		return Request{}, ErrInvalid
	}
	if !validDecisionReason(outcome, reason) {
		return Request{}, ErrInvalid
	}
	if request.State.Decided() {
		if len(request.Decisions) == 0 {
			return Request{}, ErrConflict
		}
		existing := request.Decisions[len(request.Decisions)-1]
		if existing.Outcome == outcome && existing.ReasonCode == reason {
			return request.withPayloadCopy(), nil
		}
		return Request{}, ErrConflict
	}
	if request.State != RequestStateInReview {
		return Request{}, ErrConflict
	}
	next, err := request.transition(outcome.state(), reason, targetReferenceDigest(actor), identifier.String(), "privacy.request.decided", now)
	if err != nil {
		return Request{}, err
	}
	next.Decisions = append(append([]Decision(nil), request.Decisions...),
		Decision{ID: identifier, Outcome: outcome, ReasonCode: reason, Actor: actor, DecidedAt: now, Version: next.Version})
	return next, nil
}

// BeginExecution enters the executing state for an approved workflow.
func (request Request) BeginExecution(actorDigest string, now time.Time) (Request, error) {
	if request.Validate() != nil {
		return Request{}, ErrInvalid
	}
	if request.State == RequestStateExecuting {
		return request.withPayloadCopy(), nil
	}
	if request.State != RequestStateApproved && request.State != RequestStatePartiallyApproved && request.State != RequestStateFailed {
		return Request{}, ErrConflict
	}
	return request.transition(RequestStateExecuting, "", actorDigest, "", "privacy.request.executing", now)
}

// Complete records the effect result and finalises the workflow.
func (request Request) Complete(effectKind, effectRef, effectDigest string, now time.Time) (Request, error) {
	if !token(effectKind, 64) || !token(effectRef, 512) || !token(effectDigest, 128) {
		return Request{}, ErrInvalid
	}
	next, err := request.transition(RequestStateCompleted, ReasonCode(effectKind), "", "", "privacy.request.completed", now)
	if err != nil {
		return Request{}, err
	}
	next.EffectKind, next.EffectRef, next.EffectDigest = effectKind, effectRef, effectDigest
	return next, nil
}

// Fail records a bounded failure class so a later execution can retry.
func (request Request) Fail(failureClass string, now time.Time) (Request, error) {
	if !token(failureClass, 64) {
		return Request{}, ErrInvalid
	}
	next, err := request.transition(RequestStateFailed, "", "", failureClass, "privacy.request.failed", now)
	if err != nil {
		return Request{}, err
	}
	next.FailureClass = failureClass
	return next, nil
}

// Withdraw ends a non-terminal workflow at tenant request.
func (request Request) Withdraw(actorDigest string, now time.Time) (Request, error) {
	if request.Validate() != nil {
		return Request{}, ErrInvalid
	}
	if request.State == RequestStateWithdrawn {
		return request.withPayloadCopy(), nil
	}
	if request.State != RequestStateRequested && request.State != RequestStateInReview {
		return Request{}, ErrConflict
	}
	return request.transition(RequestStateWithdrawn, ReasonWithdrawnByTenant, actorDigest, "", "privacy.request.withdrawn", now)
}

// Expire ends an undecided workflow at its expiry boundary.
func (request Request) Expire(now time.Time) (Request, error) {
	if request.Validate() != nil || now.IsZero() || now.Location() != time.UTC || now.Before(request.ExpiresAt) {
		return Request{}, ErrConflict
	}
	if request.State == RequestStateExpired {
		return request.withPayloadCopy(), nil
	}
	if request.State != RequestStateRequested && request.State != RequestStateInReview {
		return Request{}, ErrConflict
	}
	return request.transition(RequestStateExpired, ReasonExpiredWithoutAction, "", "", "privacy.request.expired", now)
}

// EffectRefDigest projects the effect reference for subject-safe reads.
func (request Request) EffectRefDigest() string {
	if request.EffectRef == "" {
		return ""
	}
	return digestToken("effect", request.EffectRef)
}

// SubjectStatus is the closed subject-safe projection. It deliberately omits
// identifiers, reason codes, tenant state, effect references, and actor data.
type SubjectStatus string

// The closed subject-safe status vocabulary.
const (
	SubjectStatusReceived   SubjectStatus = "received"
	SubjectStatusInReview   SubjectStatus = "in_review"
	SubjectStatusInProgress SubjectStatus = "in_progress"
	SubjectStatusCompleted  SubjectStatus = "completed"
	SubjectStatusClosed     SubjectStatus = "closed"
	SubjectStatusWithdrawn  SubjectStatus = "withdrawn"
	SubjectStatusExpired    SubjectStatus = "expired"
)

// SubjectRequest is one closed subject-safe request projection. Type is the
// caller's own submission; no other field can identify people or workflows.
type SubjectRequest struct {
	Type        RequestType
	Status      SubjectStatus
	RequestedAt time.Time
	UpdatedAt   time.Time
}

// SubjectProjection projects one request into the closed subject-safe shape.
func (request Request) SubjectProjection() SubjectRequest {
	status := SubjectStatusReceived
	switch request.State {
	case RequestStateRequested:
		status = SubjectStatusReceived
	case RequestStateInReview:
		status = SubjectStatusInReview
	case RequestStateApproved, RequestStatePartiallyApproved, RequestStateExecuting, RequestStateFailed:
		status = SubjectStatusInProgress
	case RequestStateCompleted:
		status = SubjectStatusCompleted
	case RequestStateDenied:
		status = SubjectStatusClosed
	case RequestStateWithdrawn:
		status = SubjectStatusWithdrawn
	case RequestStateExpired:
		status = SubjectStatusExpired
	}
	return SubjectRequest{Type: request.Type, Status: status, RequestedAt: request.RequestedAt, UpdatedAt: request.UpdatedAt}
}

// RequestFilter selects a bounded tenant-scoped request page.
type RequestFilter struct {
	State     RequestState
	Type      RequestType
	SubjectID string
}

// RequestPage is one bounded ascending page of privacy requests.
type RequestPage struct {
	Requests []Request
	HasMore  bool
}

// SubjectRequestPage is one bounded subject-safe page. NextPosition is the
// opaque keyset position for the next page; it is never part of a projection.
type SubjectRequestPage struct {
	Requests     []SubjectRequest
	HasMore      bool
	NextPosition string
}

func (request Request) withPayloadCopy() Request {
	request.Payload = append([]byte(nil), request.Payload...)
	request.Events = append([]Event(nil), request.Events...)
	request.Decisions = append([]Decision(nil), request.Decisions...)
	return request
}

func validDecisionReason(outcome DecisionOutcome, reason ReasonCode) bool {
	switch outcome {
	case OutcomeApproved, OutcomePartiallyApproved:
		return reason.ValidApproval()
	case OutcomeDenied:
		return reason.ValidDenial()
	default:
		return false
	}
}

func digestToken(prefix, value string) string {
	return fmt.Sprintf("%s:%s", prefix, targetReferenceDigest(value))
}
