// Package apierror classifies application failures for safe HTTP mapping.
package apierror

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/proposal"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// Stable public error codes owned by the HTTP foundation.
const (
	CodeInvalidRequest              = "INVALID_REQUEST"
	CodeInternalError               = "INTERNAL_ERROR"
	CodeNotFound                    = "NOT_FOUND"
	CodeGone                        = "GONE"
	CodeMethodNotAllowed            = "METHOD_NOT_ALLOWED"
	CodeConflict                    = "CONFLICT"
	CodeIdempotencyConflict         = "IDEMPOTENCY_CONFLICT"
	CodePreconditionFailed          = "PRECONDITION_FAILED"
	CodeRequestTooLarge             = "REQUEST_TOO_LARGE"
	CodePreconditionRequired        = "PRECONDITION_REQUIRED"
	CodeRateLimited                 = "RATE_LIMITED"
	CodeRequestTimeout              = "REQUEST_TIMEOUT"
	CodeRequestCancelled            = "REQUEST_CANCELLED"
	CodeServiceUnavailable          = "SERVICE_UNAVAILABLE"
	CodeUnauthenticated             = "UNAUTHENTICATED"
	CodeInsufficientScope           = "INSUFFICIENT_SCOPE"
	CodeProcessingAuthorityRequired = "PROCESSING_AUTHORITY_REQUIRED"
	CodeCaptureOriginNotAllowed     = "CAPTURE_ORIGIN_NOT_ALLOWED"
)

// Error contains an explicitly safe public representation and an optional
// wrapped operational cause.
type Error struct {
	status     int
	code       string
	title      string
	detail     string
	retryAfter time.Duration
	challenge  string
	cause      error
}

// New creates a classified public error. Callers must supply a stable code and
// a detail that is safe to disclose to an unauthenticated client.
func New(status int, code, title, detail string, cause error) *Error {
	return &Error{
		status: status,
		code:   code,
		title:  title,
		detail: detail,
		cause:  cause,
	}
}

// Error returns only the stable public code, never the wrapped cause.
func (failure *Error) Error() string {
	return failure.code
}

// Unwrap exposes the operational cause to errors.Is and errors.As.
func (failure *Error) Unwrap() error {
	return failure.cause
}

// Status returns the HTTP status associated with the failure.
func (failure *Error) Status() int {
	return failure.status
}

// Code returns the stable machine-readable code.
func (failure *Error) Code() string {
	return failure.code
}

// Title returns the short public title.
func (failure *Error) Title() string {
	return failure.title
}

// Detail returns the safe public detail.
func (failure *Error) Detail() string {
	return failure.detail
}

// RetryAfter returns the requested retry delay, if any.
func (failure *Error) RetryAfter() time.Duration {
	return failure.retryAfter
}

// Challenge returns a safe WWW-Authenticate challenge, if any.
func (failure *Error) Challenge() string {
	return failure.challenge
}

// WithRetryAfter adds Retry-After semantics.
func (failure *Error) WithRetryAfter(delay time.Duration) *Error {
	failure.retryAfter = delay

	return failure
}

// WithChallenge adds WWW-Authenticate semantics. Values containing control
// characters are discarded to prevent response-header injection.
func (failure *Error) WithChallenge(challenge string) *Error {
	if !strings.ContainsAny(challenge, "\r\n") {
		failure.challenge = challenge
	}

	return failure
}

// Map returns the public classification for err. Unknown failures are reduced
// to one generic internal response so operational causes cannot leak.
func Map(err error) *Error {
	var classified *Error
	if errors.As(err, &classified) {
		return classified
	}
	if errors.Is(err, access.ErrInvalidCredential) {
		return New(
			http.StatusUnauthorized,
			CodeUnauthenticated,
			"Unauthenticated",
			"Authentication is required.",
			err,
		)
	}
	if errors.Is(err, access.ErrInsufficientScope) {
		return New(
			http.StatusForbidden,
			CodeInsufficientScope,
			"Insufficient scope",
			"The authenticated credential cannot perform this operation.",
			err,
		)
	}
	if errors.Is(err, review.ErrForbidden) {
		return New(http.StatusForbidden, CodeInsufficientScope, "Forbidden", "The reviewer cannot perform this operation.", err)
	}
	if errors.Is(err, review.ErrInvalid) {
		return New(http.StatusNotFound, CodeNotFound, "Not found", "The requested resource was not found.", err)
	}
	if errors.Is(err, review.ErrConflict) {
		return New(http.StatusConflict, CodeConflict, "Conflict", "The review operation conflicts with current state.", err)
	}
	if errors.Is(err, privacy.ErrHeld) {
		return New(http.StatusConflict, CodeConflict, "Legal hold", "Deletion is suspended by a legal hold.", err)
	}
	if errors.Is(err, privacy.ErrConflict) {
		return New(http.StatusConflict, CodeConflict, "Conflict", "The lifecycle operation conflicts with current state.", err)
	}
	if errors.Is(err, privacy.ErrInvalid) {
		return New(http.StatusNotFound, CodeNotFound, "Not found", "The requested lifecycle resource was not found.", err)
	}
	if errors.Is(err, tenant.ErrNotFound) {
		return New(
			http.StatusNotFound,
			CodeNotFound,
			"Not found",
			"The requested resource was not found.",
			err,
		)
	}
	if errors.Is(err, policy.ErrDecisionNotFound) {
		return New(
			http.StatusNotFound,
			CodeNotFound,
			"Not found",
			"The requested resource was not found.",
			err,
		)
	}
	if errors.Is(err, verification.ErrProfileNotFound) || errors.Is(err, verification.ErrSessionNotFound) {
		return New(
			http.StatusNotFound,
			CodeNotFound,
			"Not found",
			"The requested resource was not found.",
			err,
		)
	}
	if errors.Is(err, authority.ErrNotFound) {
		return New(
			http.StatusNotFound,
			CodeNotFound,
			"Not found",
			"The requested resource was not found.",
			err,
		)
	}
	if errors.Is(err, authority.ErrProcessingNotPermitted) ||
		errors.Is(err, authority.ErrSubjectResponseRequired) {
		return New(
			http.StatusForbidden,
			CodeProcessingAuthorityRequired,
			"Processing authority required",
			"Current processing authority does not permit this operation.",
			err,
		)
	}
	if errors.Is(err, idempotency.ErrConflict) {
		return New(
			http.StatusConflict,
			CodeIdempotencyConflict,
			"Idempotency conflict",
			"The idempotency key was already used with different request input.",
			err,
		)
	}
	if errors.Is(err, idempotency.ErrInProgress) {
		return New(
			http.StatusConflict,
			CodeConflict,
			"Operation in progress",
			"An identical operation is still in progress.",
			err,
		).WithRetryAfter(time.Second)
	}
	if errors.Is(err, verification.ErrPublishedRevisionImmutable) {
		return New(
			http.StatusConflict,
			CodeConflict,
			"Conflict",
			"Published capture-profile revisions are immutable.",
			err,
		)
	}
	if errors.Is(err, authority.ErrConflict) {
		return New(
			http.StatusConflict,
			CodeConflict,
			"Conflict",
			"The processing-authority operation conflicts with current state.",
			err,
		)
	}
	if errors.Is(err, authority.ErrVersionConflict) {
		return New(
			http.StatusPreconditionFailed,
			CodePreconditionFailed,
			"Precondition failed",
			"The processing authority changed before this operation completed.",
			err,
		)
	}
	if errors.Is(err, evidence.ErrUploadNotFound) {
		return New(http.StatusNotFound, CodeNotFound, "Not found", "The requested resource was not found.", err)
	}
	if errors.Is(err, evidence.ErrUploadVersionConflict) {
		return New(
			http.StatusPreconditionFailed,
			CodePreconditionFailed,
			"Precondition failed",
			"The evidence upload changed before this operation completed.",
			err,
		)
	}
	if errors.Is(err, evidence.ErrUploadMetadata) || errors.Is(err, evidence.ErrUploadConflict) ||
		errors.Is(err, evidence.ErrUploadExpired) {
		return New(
			http.StatusConflict,
			CodeConflict,
			"Conflict",
			"The evidence upload conflicts with its immutable binding or current state.",
			err,
		)
	}
	if errors.Is(err, evidence.ErrUploadBodyLength) || errors.Is(err, evidence.ErrUploadBodyDigest) ||
		errors.Is(err, evidence.ErrUploadSignature) || errors.Is(err, evidence.ErrUploadBodyIncomplete) {
		return New(
			http.StatusBadRequest,
			CodeInvalidRequest,
			"Invalid request",
			"The evidence body does not satisfy the upload intent.",
			err,
		)
	}
	if errors.Is(err, evidence.ErrUploadAcceptanceOutcomeUnknown) {
		return New(
			http.StatusServiceUnavailable,
			CodeServiceUnavailable,
			"Service unavailable",
			"The evidence upload outcome is being reconciled.",
			err,
		).WithRetryAfter(time.Second)
	}
	if errors.Is(err, proposal.ErrInvalid) {
		return New(http.StatusBadRequest, CodeInvalidRequest, "Invalid request", "The proposal request is invalid.", err)
	}
	if errors.Is(err, proposal.ErrNotFound) {
		return New(http.StatusNotFound, CodeNotFound, "Not found", "The requested proposal was not found.", err)
	}
	if errors.Is(err, proposal.ErrConflict) {
		return New(http.StatusConflict, CodeConflict, "Conflict", "The proposal conflicts with current state.", err)
	}
	if errors.Is(err, proposal.ErrNotAllowed) {
		return New(http.StatusForbidden, CodeInsufficientScope, "Forbidden", "The proposal operation is not allowed.", err)
	}
	if errors.Is(err, proposal.ErrRateLimited) {
		return New(http.StatusTooManyRequests, CodeRateLimited, "Rate limited", "The proposal operation is rate limited.", err).WithRetryAfter(time.Second)
	}
	if errors.Is(err, proposal.ErrModeDisabled) {
		return New(http.StatusForbidden, CodeInsufficientScope, "Forbidden", "AI proposals are disabled for this workflow.", err)
	}
	if errors.Is(err, proposal.ErrApprovalNeeded) {
		return New(http.StatusForbidden, CodeInsufficientScope, "Forbidden", "Human approval is required for this proposal.", err)
	}
	if errors.Is(err, verification.ErrProfileUnavailable) {
		return New(
			http.StatusConflict,
			CodeConflict,
			"Conflict",
			"The selected capture profile is not available for verification creation.",
			err,
		)
	}
	if errors.Is(err, verification.ErrSessionConflict) {
		return New(
			http.StatusConflict,
			CodeConflict,
			"Conflict",
			"The verification session cannot make this transition.",
			err,
		)
	}
	if errors.Is(err, verification.ErrProfileConflict) {
		return New(
			http.StatusPreconditionFailed,
			CodePreconditionFailed,
			"Precondition failed",
			"The capture profile changed or cannot make this transition.",
			err,
		)
	}
	var maxBytes *http.MaxBytesError
	if errors.As(err, &maxBytes) {
		return New(
			http.StatusRequestEntityTooLarge,
			CodeRequestTooLarge,
			"Request too large",
			"The request body exceeds the permitted size.",
			err,
		)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return New(
			http.StatusGatewayTimeout,
			CodeRequestTimeout,
			"Request timed out",
			"The request exceeded its processing deadline.",
			err,
		)
	}
	if errors.Is(err, context.Canceled) {
		return New(
			http.StatusRequestTimeout,
			CodeRequestCancelled,
			"Request cancelled",
			"The request was cancelled before completion.",
			err,
		)
	}

	return New(
		http.StatusInternalServerError,
		CodeInternalError,
		"Internal server error",
		"The request could not be completed.",
		err,
	)
}
