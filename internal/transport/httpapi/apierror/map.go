package apierror

import (
	"context"
	"errors"
	"net/http"
	"time"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/fraud"
	"github.com/Mujhtech/idenqa/internal/identity"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	"github.com/Mujhtech/idenqa/internal/keyrewrap"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/pack"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/proposal"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/support"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// rule classifies one or more sentinel failures as a single public response.
// Rules are matched in order and the first match wins, so a specific failure
// must precede any broader rule that would also match it.
type rule struct {
	targets    []error
	status     int
	code       string
	title      string
	detail     string
	retryAfter time.Duration
}

// rules is the ordered classification table applied by Map. Matching uses
// errors.Is, so wrapped, joined, and custom Is implementations are honoured
// across the whole error tree.
var rules = []rule{
	{
		targets: []error{access.ErrInvalidCredential},
		status:  http.StatusUnauthorized,
		code:    CodeUnauthenticated,
		title:   "Unauthenticated",
		detail:  "Authentication is required.",
	},
	{
		targets: []error{access.ErrInsufficientScope},
		status:  http.StatusForbidden,
		code:    CodeInsufficientScope,
		title:   "Insufficient scope",
		detail:  "The authenticated credential cannot perform this operation.",
	},
	{
		targets: []error{review.ErrForbidden},
		status:  http.StatusForbidden,
		code:    CodeInsufficientScope,
		title:   "Forbidden",
		detail:  "The reviewer cannot perform this operation.",
	},
	{
		targets: []error{review.ErrInvalid},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The requested resource was not found.",
	},
	{
		targets: []error{review.ErrConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The review operation conflicts with current state.",
	},
	{
		targets: []error{privacy.ErrHeld},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Legal hold",
		detail:  "Deletion is suspended by a legal hold.",
	},
	{
		targets: []error{privacy.ErrConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The lifecycle operation conflicts with current state.",
	},
	{
		targets: []error{privacy.ErrInvalid},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The requested lifecycle resource was not found.",
	},
	{
		targets: []error{
			tenant.ErrNotFound,
			policy.ErrDecisionNotFound,
			verification.ErrProfileNotFound,
			verification.ErrSessionNotFound,
			authority.ErrNotFound,
		},
		status: http.StatusNotFound,
		code:   CodeNotFound,
		title:  "Not found",
		detail: "The requested resource was not found.",
	},
	{
		targets: []error{
			authority.ErrProcessingNotPermitted,
			authority.ErrSubjectResponseRequired,
		},
		status: http.StatusForbidden,
		code:   CodeProcessingAuthorityRequired,
		title:  "Processing authority required",
		detail: "Current processing authority does not permit this operation.",
	},
	{
		targets: []error{idempotency.ErrConflict},
		status:  http.StatusConflict,
		code:    CodeIdempotencyConflict,
		title:   "Idempotency conflict",
		detail:  "The idempotency key was already used with different request input.",
	},
	{
		targets:    []error{idempotency.ErrInProgress},
		status:     http.StatusConflict,
		code:       CodeConflict,
		title:      "Operation in progress",
		detail:     "An identical operation is still in progress.",
		retryAfter: time.Second,
	},
	{
		targets: []error{verification.ErrPublishedRevisionImmutable},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "Published capture-profile revisions are immutable.",
	},
	{
		targets: []error{authority.ErrConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The processing-authority operation conflicts with current state.",
	},
	{
		targets: []error{authority.ErrVersionConflict},
		status:  http.StatusPreconditionFailed,
		code:    CodePreconditionFailed,
		title:   "Precondition failed",
		detail:  "The processing authority changed before this operation completed.",
	},
	{
		targets: []error{evidence.ErrUploadNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The requested resource was not found.",
	},
	{
		targets: []error{evidence.ErrUploadVersionConflict},
		status:  http.StatusPreconditionFailed,
		code:    CodePreconditionFailed,
		title:   "Precondition failed",
		detail:  "The evidence upload changed before this operation completed.",
	},
	{
		targets: []error{
			evidence.ErrUploadMetadata,
			evidence.ErrUploadConflict,
			evidence.ErrUploadExpired,
		},
		status: http.StatusConflict,
		code:   CodeConflict,
		title:  "Conflict",
		detail: "The evidence upload conflicts with its immutable binding or current state.",
	},
	{
		targets: []error{
			evidence.ErrUploadBodyLength,
			evidence.ErrUploadBodyDigest,
			evidence.ErrUploadSignature,
			evidence.ErrUploadBodyIncomplete,
		},
		status: http.StatusBadRequest,
		code:   CodeInvalidRequest,
		title:  "Invalid request",
		detail: "The evidence body does not satisfy the upload intent.",
	},
	{
		targets:    []error{evidence.ErrUploadAcceptanceOutcomeUnknown},
		status:     http.StatusServiceUnavailable,
		code:       CodeServiceUnavailable,
		title:      "Service unavailable",
		detail:     "The evidence upload outcome is being reconciled.",
		retryAfter: time.Second,
	},
	{
		targets: []error{proposal.ErrInvalid},
		status:  http.StatusBadRequest,
		code:    CodeInvalidRequest,
		title:   "Invalid request",
		detail:  "The proposal request is invalid.",
	},
	{
		targets: []error{proposal.ErrNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The requested proposal was not found.",
	},
	{
		targets: []error{proposal.ErrConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The proposal conflicts with current state.",
	},
	{
		targets: []error{proposal.ErrNotAllowed},
		status:  http.StatusForbidden,
		code:    CodeInsufficientScope,
		title:   "Forbidden",
		detail:  "The proposal operation is not allowed.",
	},
	{
		targets:    []error{proposal.ErrRateLimited},
		status:     http.StatusTooManyRequests,
		code:       CodeRateLimited,
		title:      "Rate limited",
		detail:     "The proposal operation is rate limited.",
		retryAfter: time.Second,
	},
	{
		targets: []error{proposal.ErrModeDisabled},
		status:  http.StatusForbidden,
		code:    CodeInsufficientScope,
		title:   "Forbidden",
		detail:  "AI proposals are disabled for this workflow.",
	},
	{
		targets: []error{proposal.ErrApprovalNeeded},
		status:  http.StatusForbidden,
		code:    CodeInsufficientScope,
		title:   "Forbidden",
		detail:  "Human approval is required for this proposal.",
	},
	{
		targets: []error{verification.ErrProfileUnavailable},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The selected capture profile is not available for verification creation.",
	},
	{
		targets: []error{verification.ErrSessionConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The verification session cannot make this transition.",
	},
	{
		targets: []error{verification.ErrProfileConflict},
		status:  http.StatusPreconditionFailed,
		code:    CodePreconditionFailed,
		title:   "Precondition failed",
		detail:  "The capture profile changed or cannot make this transition.",
	},
	{
		targets: []error{experience.ErrNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The requested resource was not found.",
	},
	{
		targets: []error{experience.ErrConflict, experience.ErrRevoked},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The experience lifecycle conflicts with current state.",
	},
	{
		targets: []error{
			experience.ErrSignature,
			experience.ErrInvalid,
			experience.ErrUnknownMandatoryCopy,
			experience.ErrAssetUnavailable,
			contract.ErrInvalid,
			contract.ErrUnsupportedVersion,
			contract.ErrTooLarge,
			contract.ErrReservedCopy,
		},
		status: http.StatusBadRequest,
		code:   CodeInvalidRequest,
		title:  "Invalid request",
		detail: "The experience document or command is invalid.",
	},
	// Support access.
	{
		targets: []error{support.ErrInvalid},
		status:  http.StatusBadRequest,
		code:    CodeInvalidRequest,
		title:   "Invalid request",
		detail:  "The request does not satisfy the public contract.",
	},
	{
		targets: []error{support.ErrForbidden},
		status:  http.StatusForbidden,
		code:    CodeInsufficientScope,
		title:   "Forbidden",
		detail:  "The support operation is not permitted.",
	},
	{
		targets: []error{support.ErrNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The support record was not found.",
	},
	{
		targets: []error{support.ErrConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The support record changed since it was read.",
	},
	{
		targets: []error{support.ErrExpired},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The support access window has expired.",
	},
	{
		targets: []error{support.ErrUnavailable},
		status:  http.StatusServiceUnavailable,
		code:    CodeServiceUnavailable,
		title:   "Unavailable",
		detail:  "Support access is unavailable.",
	},
	// Fraud controls.
	{
		targets: []error{fraud.ErrInvalid},
		status:  http.StatusBadRequest,
		code:    CodeInvalidRequest,
		title:   "Invalid request",
		detail:  "The request does not satisfy the public contract.",
	},
	{
		targets: []error{fraud.ErrNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The fraud resource was not found.",
	},
	{
		targets: []error{fraud.ErrConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The fraud revision or source conflicts with current state.",
	},
	{
		targets: []error{fraud.ErrUnavailable},
		status:  http.StatusServiceUnavailable,
		code:    CodeServiceUnavailable,
		title:   "Unavailable",
		detail:  "Fraud key custody is unavailable.",
	},
	// Model registry.
	{
		targets: []error{model.ErrRegistryInvalid},
		status:  http.StatusBadRequest,
		code:    CodeInvalidRequest,
		title:   "Invalid request",
		detail:  "The request does not satisfy the public contract.",
	},
	{
		targets: []error{model.ErrRegistryNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The model resource was not found.",
	},
	{
		targets: []error{model.ErrRegistryConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The model registry version conflicts with current state.",
	},
	// Provider registration.
	{
		targets: []error{provider.ErrRegistrationInvalid},
		status:  http.StatusBadRequest,
		code:    CodeInvalidRequest,
		title:   "Invalid request",
		detail:  "The request does not satisfy the public contract.",
	},
	{
		targets: []error{provider.ErrRegistrationNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The provider registration was not found.",
	},
	{
		targets: []error{provider.ErrRegistrationConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The provider registration version conflicts with current state.",
	},
	// Portable packs.
	{
		targets: []error{pack.ErrNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The pack resource was not found.",
	},
	{
		targets: []error{pack.ErrInvalid, pack.ErrConflict},
		status:  http.StatusBadRequest,
		code:    CodeInvalidRequest,
		title:   "Invalid request",
		detail:  "The request does not satisfy the public contract.",
	},
	// Identity records.
	{
		targets: []error{identity.ErrInvalid},
		status:  http.StatusBadRequest,
		code:    CodeInvalidRequest,
		title:   "Invalid request",
		detail:  "The request does not satisfy the public contract.",
	},
	{
		targets: []error{identity.ErrNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The identity resource was not found.",
	},
	{
		targets: []error{identity.ErrConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The identity version, source or lifecycle conflicts with current state.",
	},
	{
		targets: []error{identity.ErrUnavailable},
		status:  http.StatusServiceUnavailable,
		code:    CodeServiceUnavailable,
		title:   "Unavailable",
		detail:  "Identity data or complete projection coverage is unavailable.",
	},
	// Webhook delivery.
	{
		targets: []error{delivery.ErrInvalid},
		status:  http.StatusBadRequest,
		code:    CodeInvalidRequest,
		title:   "Invalid request",
		detail:  "The request does not satisfy the public contract.",
	},
	{
		targets: []error{delivery.ErrNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The webhook resource was not found.",
	},
	{
		targets: []error{delivery.ErrDisabled, delivery.ErrConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The webhook operation conflicts with current state.",
	},
	{
		targets: []error{delivery.ErrExpired},
		status:  http.StatusGone,
		code:    CodeGone,
		title:   "Gone",
		detail:  "The webhook payload retention window has closed.",
	},
	{
		targets: []error{delivery.ErrQueueUnavailable},
		status:  http.StatusServiceUnavailable,
		code:    CodeServiceUnavailable,
		title:   "Unavailable",
		detail:  "Webhook replay configuration is unavailable.",
	},
	{
		targets: []error{delivery.ErrSigningUnavailable},
		status:  http.StatusServiceUnavailable,
		code:    CodeServiceUnavailable,
		title:   "Unavailable",
		detail:  "Webhook signing configuration is unavailable.",
	},
	// Evidence administration.
	{
		targets: []error{evidence.ErrNotFound, evidence.ErrGrantDenied},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The evidence resource was not found.",
	},
	{
		targets: []error{evidence.ErrGrantConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The evidence access grant cannot transition.",
	},
	// Policy administration and simulation.
	{
		targets: []error{policy.ErrInvalid},
		status:  http.StatusBadRequest,
		code:    CodeInvalidRequest,
		title:   "Invalid request",
		detail:  "The request does not satisfy the public contract.",
	},
	{
		targets: []error{policy.ErrRevisionNotFound, policy.ErrActivationNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The policy resource was not found.",
	},
	{
		targets: []error{policy.ErrRevisionConflict, policy.ErrActivationConflict, policy.ErrConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The policy operation conflicts with current state.",
	},
	// Key custody and key operations.
	{
		targets: []error{keycustody.ErrInvalid, keyrewrap.ErrInvalid},
		status:  http.StatusBadRequest,
		code:    CodeInvalidRequest,
		title:   "Invalid request",
		detail:  "The request does not satisfy the public contract.",
	},
	{
		targets: []error{keycustody.ErrNotFound},
		status:  http.StatusNotFound,
		code:    CodeNotFound,
		title:   "Not found",
		detail:  "The key record was not found.",
	},
	{
		targets: []error{keycustody.ErrRecoveryForbidden},
		status:  http.StatusForbidden,
		code:    CodeInsufficientScope,
		title:   "Forbidden",
		detail:  "The key operation is not permitted.",
	},
	{
		targets: []error{keycustody.ErrReferenced},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The key version is still referenced and cannot be retired.",
	},
	{
		targets: []error{keycustody.ErrDestructionBlocked},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "Live references still target the key material.",
	},
	{
		targets: []error{keycustody.ErrDestructionUnverified},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "An unexpired verified receipt is required.",
	},
	{
		targets: []error{keycustody.ErrRecoveryExpired},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The recovery ceremony validity window has elapsed.",
	},
	{
		targets: []error{keycustody.ErrRecoveryState},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The recovery ceremony state does not permit this operation.",
	},
	{
		targets: []error{keycustody.ErrConflict, keyrewrap.ErrConflict},
		status:  http.StatusConflict,
		code:    CodeConflict,
		title:   "Conflict",
		detail:  "The key record changed since it was read.",
	},
	{
		targets: []error{keycustody.ErrUnavailable, keyrewrap.ErrUnavailable},
		status:  http.StatusServiceUnavailable,
		code:    CodeServiceUnavailable,
		title:   "Unavailable",
		detail:  "Key custody is unavailable.",
	},
}

// matchesAny reports whether err matches any of the target sentinels.
func matchesAny(err error, targets []error) bool {
	for _, target := range targets {
		if errors.Is(err, target) {
			return true
		}
	}

	return false
}

// Map returns the public classification for err. Unknown failures are reduced
// to one generic internal response so operational causes cannot leak.
func Map(err error) *Error {
	var classified *Error
	if errors.As(err, &classified) {
		return classified
	}

	for _, rule := range rules {
		if !matchesAny(err, rule.targets) {
			continue
		}

		failure := New(
			rule.status,
			rule.code,
			rule.title,
			rule.detail,
			err,
		)
		if rule.retryAfter > 0 {
			failure = failure.WithRetryAfter(rule.retryAfter)
		}

		return failure
	}

	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
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
