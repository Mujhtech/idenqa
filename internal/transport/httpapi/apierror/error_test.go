package apierror_test

import (
	"errors"
	"net/http"
	"testing"

	"github.com/Mujhtech/idenqa/internal/authority"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
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
