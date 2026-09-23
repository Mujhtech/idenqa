// Package apierror classifies application failures for safe HTTP mapping.
package apierror

import (
	"strings"
	"time"
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
