package verification

import (
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	// InputRequestFallbackReason is the deterministic reason code recorded when
	// a policy evaluation carries no bounded reason code.
	InputRequestFallbackReason = "policy_request_input"
	// MaximumInputRequestReasons bounds the projected reason-code list.
	MaximumInputRequestReasons = 8
)

var inputRequestReasonPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,63}$`)

// ErrInputRequestNotFound deliberately also covers cross-tenant misses.
var ErrInputRequestNotFound = errors.New("verification: input request not found")

// InputRequest is immutable policy-routing provenance naming the further
// subject input a policy-authored request_input directive requires. It carries
// bounded reason codes only; it never carries evidence, policy facts, or
// decision meaning.
type InputRequest struct {
	id             id.InputRequest
	verificationID id.Verification
	caseID         id.ReviewCase
	reasonCodes    []string
	actorID        string
	requestedAt    time.Time
}

// NewInputRequest validates one bounded policy-authored subject-input request.
// RequestedAt must be UTC with microsecond precision, matching PostgreSQL.
func NewInputRequest(
	identifier id.InputRequest,
	verificationID id.Verification,
	caseID id.ReviewCase,
	reasonCodes []string,
	actorID string,
	requestedAt time.Time,
) (InputRequest, error) {
	if identifier.IsZero() || verificationID.IsZero() || !lifecycleActor(actorID) ||
		!utcNonZero(requestedAt) || requestedAt.Nanosecond()%1000 != 0 {
		return InputRequest{}, ErrSessionConflict
	}
	if len(reasonCodes) == 0 || len(reasonCodes) > MaximumInputRequestReasons {
		return InputRequest{}, ErrSessionConflict
	}
	reasons := make([]string, len(reasonCodes))
	for index, code := range reasonCodes {
		if !validInputRequestReason(code) || slices.Contains(reasons[:index], code) {
			return InputRequest{}, ErrSessionConflict
		}
		reasons[index] = code
	}

	return InputRequest{
		id:             identifier,
		verificationID: verificationID,
		caseID:         caseID,
		reasonCodes:    reasons,
		actorID:        actorID,
		requestedAt:    requestedAt.UTC(),
	}, nil
}

// BoundedInputRequestReasons keeps only reason codes within the closed table
// grammar, removes duplicates, truncates to the projection bound, and falls
// back to the deterministic policy reason when none remain. Evaluation reason
// codes are already sorted and unique; this defends the persistence boundary.
func BoundedInputRequestReasons(values []string) []string {
	result := make([]string, 0, MaximumInputRequestReasons)
	for _, value := range values {
		if !validInputRequestReason(value) || slices.Contains(result, value) {
			continue
		}
		result = append(result, value)
		if len(result) == MaximumInputRequestReasons {
			break
		}
	}
	if len(result) == 0 {
		return []string{InputRequestFallbackReason}
	}
	return result
}

func validInputRequestReason(value string) bool {
	return inputRequestReasonPattern.MatchString(value)
}

// ID returns the immutable input-request identifier.
func (request InputRequest) ID() id.InputRequest { return request.id }

// VerificationID returns the owning verification session identifier.
func (request InputRequest) VerificationID() id.Verification { return request.verificationID }

// CaseID returns the optional review-case origin, or the zero value.
func (request InputRequest) CaseID() id.ReviewCase { return request.caseID }

// ReasonCodes returns a defensive copy of the bounded reason codes.
func (request InputRequest) ReasonCodes() []string { return slices.Clone(request.reasonCodes) }

// ActorID returns the verified principal reference that recorded the request.
func (request InputRequest) ActorID() string { return request.actorID }

// RequestedAt returns the routing instant that produced the request.
func (request InputRequest) RequestedAt() time.Time { return request.requestedAt }

// WithInputRequest attaches the current policy-authored input request loaded by
// a tenant session read. It is the additive read path that leaves
// RestoreSession stable; it accepts only an awaiting-input session so the
// projection can never outlive the state that justifies it.
func (session Session) WithInputRequest(request InputRequest) (Session, error) {
	if session.state != SessionStateAwaitingInput || request.verificationID.String() != session.id.String() {
		return Session{}, errors.New("verification: input request is invalid for session")
	}
	cloned := request
	cloned.reasonCodes = slices.Clone(request.reasonCodes)
	session.inputRequest = &cloned
	return session, nil
}

// InputRequest returns the current input request when the session read loaded
// one. Subject-safe capture reads never load it.
func (session Session) InputRequest() (InputRequest, bool) {
	if session.inputRequest == nil {
		return InputRequest{}, false
	}
	request := *session.inputRequest
	request.reasonCodes = slices.Clone(session.inputRequest.reasonCodes)
	return request, true
}
