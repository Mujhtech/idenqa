package apierror_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/fraud"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
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
	"github.com/Mujhtech/idenqa/internal/support"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestStableCodesMatchGeneratedContract(t *testing.T) {
	t.Parallel()

	codes := []string{
		apierror.CodeInvalidRequest,
		apierror.CodeUnauthenticated,
		apierror.CodeInsufficientScope,
		apierror.CodeProcessingAuthorityRequired,
		apierror.CodeNotFound,
		apierror.CodeMethodNotAllowed,
		apierror.CodeConflict,
		apierror.CodeIdempotencyConflict,
		apierror.CodePreconditionFailed,
		apierror.CodeRequestTooLarge,
		apierror.CodePreconditionRequired,
		apierror.CodeRateLimited,
		apierror.CodeRequestCancelled,
		apierror.CodeInternalError,
		apierror.CodeServiceUnavailable,
		apierror.CodeRequestTimeout,
	}
	for _, code := range codes {
		if !openapiv1.ProblemCode(code).Valid() {
			t.Errorf("public code %q is absent from OpenAPI", code)
		}
	}
}

func TestMapAuthorityFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err        error
		wantStatus int
		wantCode   string
	}{
		{authority.ErrNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{authority.ErrConflict, http.StatusConflict, apierror.CodeConflict},
		{authority.ErrVersionConflict, http.StatusPreconditionFailed, apierror.CodePreconditionFailed},
		{authority.ErrProcessingNotPermitted, http.StatusForbidden, apierror.CodeProcessingAuthorityRequired},
		{authority.ErrSubjectResponseRequired, http.StatusForbidden, apierror.CodeProcessingAuthorityRequired},
	}
	for _, test := range tests {
		failure := apierror.Map(test.err)
		if failure.Status() != test.wantStatus || failure.Code() != test.wantCode {
			t.Errorf("Map(%v) = status %d code %q, want status %d code %q",
				test.err, failure.Status(), failure.Code(), test.wantStatus, test.wantCode)
		}
	}
}

func TestMapProposalModelFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"rejected", proposal.ErrModelRejected, http.StatusUnprocessableEntity, apierror.CodeInvalidRequest},
		{"invalid output", proposal.ErrModelOutput, http.StatusBadGateway, apierror.CodeServiceUnavailable},
		{"unavailable", proposal.ErrModelUnavailable, http.StatusServiceUnavailable, apierror.CodeServiceUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failure := apierror.Map(test.err)
			if failure.Status() != test.wantStatus || failure.Code() != test.wantCode {
				t.Fatalf("Map() = status %d code %q, want status %d code %q", failure.Status(), failure.Code(), test.wantStatus, test.wantCode)
			}
		})
	}
}

func TestMapRedactsUnknownFailure(t *testing.T) {
	t.Parallel()

	failure := apierror.Map(errors.New("postgres password=secret"))
	if got, want := failure.Status(), http.StatusInternalServerError; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got, want := failure.Code(), apierror.CodeInternalError; got != want {
		t.Fatalf("code = %q, want %q", got, want)
	}
	if got, want := failure.Error(), apierror.CodeInternalError; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestMapCaptureProfileFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"not found", verification.ErrProfileNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"verification not found", verification.ErrSessionNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"profile unavailable", verification.ErrProfileUnavailable, http.StatusConflict, apierror.CodeConflict},
		{"verification conflict", verification.ErrSessionConflict, http.StatusConflict, apierror.CodeConflict},
		{"idempotency conflict", idempotency.ErrConflict, http.StatusConflict, apierror.CodeIdempotencyConflict},
		{"idempotency in progress", idempotency.ErrInProgress, http.StatusConflict, apierror.CodeConflict},
		{"immutable", verification.ErrPublishedRevisionImmutable, http.StatusConflict, apierror.CodeConflict},
		{"optimistic conflict", verification.ErrProfileConflict, http.StatusPreconditionFailed, apierror.CodePreconditionFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			failure := apierror.Map(test.err)
			if failure.Status() != test.wantStatus || failure.Code() != test.wantCode {
				t.Fatalf("Map() = status %d code %q, want status %d code %q", failure.Status(), failure.Code(), test.wantStatus, test.wantCode)
			}
		})
	}
}

func TestChallengeRejectsHeaderInjection(t *testing.T) {
	t.Parallel()

	failure := apierror.New(http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthenticated", "Authentication is required.", nil)
	_ = failure.WithChallenge("Bearer\r\nX-Injected: yes")
	if failure.Challenge() != "" {
		t.Fatalf("unsafe challenge = %q, want empty", failure.Challenge())
	}
}

func TestMapDomainAdministrationFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{"support invalid", support.ErrInvalid, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{"support forbidden", support.ErrForbidden, http.StatusForbidden, apierror.CodeInsufficientScope},
		{"support not found", support.ErrNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"support conflict", support.ErrConflict, http.StatusConflict, apierror.CodeConflict},
		{"support expired", support.ErrExpired, http.StatusConflict, apierror.CodeConflict},
		{"support unavailable", support.ErrUnavailable, http.StatusServiceUnavailable, apierror.CodeServiceUnavailable},
		{"fraud invalid", fraud.ErrInvalid, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{"fraud not found", fraud.ErrNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"fraud conflict", fraud.ErrConflict, http.StatusConflict, apierror.CodeConflict},
		{"fraud unavailable", fraud.ErrUnavailable, http.StatusServiceUnavailable, apierror.CodeServiceUnavailable},
		{"model registry invalid", model.ErrRegistryInvalid, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{"model registry not found", model.ErrRegistryNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"model registry conflict", model.ErrRegistryConflict, http.StatusConflict, apierror.CodeConflict},
		{"provider registration invalid", provider.ErrRegistrationInvalid, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{"provider registration not found", provider.ErrRegistrationNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"provider registration conflict", provider.ErrRegistrationConflict, http.StatusConflict, apierror.CodeConflict},
		{"pack not found", pack.ErrNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"pack invalid", pack.ErrInvalid, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{"pack conflict", pack.ErrConflict, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{"identity invalid", identity.ErrInvalid, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{"identity not found", identity.ErrNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"identity conflict", identity.ErrConflict, http.StatusConflict, apierror.CodeConflict},
		{"identity unavailable", identity.ErrUnavailable, http.StatusServiceUnavailable, apierror.CodeServiceUnavailable},
		{"webhook invalid", delivery.ErrInvalid, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{"webhook not found", delivery.ErrNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"webhook disabled", delivery.ErrDisabled, http.StatusConflict, apierror.CodeConflict},
		{"webhook conflict", delivery.ErrConflict, http.StatusConflict, apierror.CodeConflict},
		{"webhook expired", delivery.ErrExpired, http.StatusGone, apierror.CodeGone},
		{"webhook replay unavailable", delivery.ErrQueueUnavailable, http.StatusServiceUnavailable, apierror.CodeServiceUnavailable},
		{"webhook signing unavailable", delivery.ErrSigningUnavailable, http.StatusServiceUnavailable, apierror.CodeServiceUnavailable},
		{"evidence not found", evidence.ErrNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"evidence grant denied", evidence.ErrGrantDenied, http.StatusNotFound, apierror.CodeNotFound},
		{"evidence grant conflict", evidence.ErrGrantConflict, http.StatusConflict, apierror.CodeConflict},
		{"policy invalid", policy.ErrInvalid, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{"policy revision not found", policy.ErrRevisionNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"policy activation not found", policy.ErrActivationNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"policy revision conflict", policy.ErrRevisionConflict, http.StatusConflict, apierror.CodeConflict},
		{"policy activation conflict", policy.ErrActivationConflict, http.StatusConflict, apierror.CodeConflict},
		{"policy conflict", policy.ErrConflict, http.StatusConflict, apierror.CodeConflict},
		{"key custody invalid", keycustody.ErrInvalid, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{"key custody not found", keycustody.ErrNotFound, http.StatusNotFound, apierror.CodeNotFound},
		{"key recovery forbidden", keycustody.ErrRecoveryForbidden, http.StatusForbidden, apierror.CodeInsufficientScope},
		{"key referenced", keycustody.ErrReferenced, http.StatusConflict, apierror.CodeConflict},
		{"key destruction blocked", keycustody.ErrDestructionBlocked, http.StatusConflict, apierror.CodeConflict},
		{"key destruction unverified", keycustody.ErrDestructionUnverified, http.StatusConflict, apierror.CodeConflict},
		{"key recovery expired", keycustody.ErrRecoveryExpired, http.StatusConflict, apierror.CodeConflict},
		{"key recovery state", keycustody.ErrRecoveryState, http.StatusConflict, apierror.CodeConflict},
		{"key custody conflict", keycustody.ErrConflict, http.StatusConflict, apierror.CodeConflict},
		{"key custody unavailable", keycustody.ErrUnavailable, http.StatusServiceUnavailable, apierror.CodeServiceUnavailable},
		{"key rewrap invalid", keyrewrap.ErrInvalid, http.StatusBadRequest, apierror.CodeInvalidRequest},
		{"key rewrap conflict", keyrewrap.ErrConflict, http.StatusConflict, apierror.CodeConflict},
		{"key rewrap unavailable", keyrewrap.ErrUnavailable, http.StatusServiceUnavailable, apierror.CodeServiceUnavailable},
		// privacy.ErrInvalid keeps its lifecycle not-found classification; the
		// identity surface overrides it locally for request validation.
		{"privacy invalid", privacy.ErrInvalid, http.StatusNotFound, apierror.CodeNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			failure := apierror.Map(test.err)
			if failure.Status() != test.wantStatus || failure.Code() != test.wantCode {
				t.Fatalf("Map(%v) = status %d code %q, want status %d code %q",
					test.err, failure.Status(), failure.Code(), test.wantStatus, test.wantCode)
			}
		})
	}
}
