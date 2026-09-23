package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type acknowledgementAuthority struct{ denied bool }

func (authority *acknowledgementAuthority) ResolveReviewer(context.Context, tenant.Scope, review.Actor, string, time.Time) (review.Principal, error) {
	if authority.denied {
		return review.Principal{}, review.ErrForbidden
	}
	return review.Principal{ID: "reviewer.followup", Permissions: []review.Permission{review.PermissionResolve}, Certifications: []string{"document.level2"}}, nil
}

type acknowledgementRepository struct{ calls int }

func (*acknowledgementRepository) FindCase(context.Context, tenant.Scope, id.ReviewCase) (review.Case, error) {
	return review.Case{Region: "local", RequiredCertificate: "document.level2"}, nil
}
func (*acknowledgementRepository) CreateRecapture(context.Context, tenant.Scope, id.ReviewCase, int64, verification.SessionCreateMutation) (verification.SessionCreation, error) {
	return verification.SessionCreation{}, review.ErrForbidden
}
func (*acknowledgementRepository) RenewRecapture(context.Context, tenant.Scope, id.ReviewCase, int64, verification.CaptureRenewal) (verification.SessionCreation, error) {
	return verification.SessionCreation{}, review.ErrForbidden
}
func (repo *acknowledgementRepository) AcknowledgeRecapture(_ context.Context, _ tenant.Scope, input review.AcknowledgeRecapture) (review.RecaptureAcknowledgement, error) {
	repo.calls++
	return review.RecaptureAcknowledgement{CaseID: input.CaseID, CaseVersion: input.CaseVersion, DecisionID: input.DecisionID, ActorID: input.ActorID, ReviewerID: input.ReviewerID, RecordedAt: input.At}, nil
}

func TestRecaptureAcknowledgementRechecksReviewerAuthority(t *testing.T) {
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("reviews:write"))
	repository := &acknowledgementRepository{}
	authority := &acknowledgementAuthority{}
	// Acknowledgement does not generate session identifiers or sign credentials.
	service, err := review.NewRecaptureService(repository, authority, &id.Generator{}, &access.CaptureTokenSigner{}, &access.OutcomeTokenSigner{}, clock.System{}, time.Hour, time.Minute, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	routes := &ReviewRoutes{access: fixture.middleware, handlerBase: newHandlerBase(fixture.logger, "review"), recapture: service}
	router := versionedRouter(t, routes)
	for _, scenario := range []struct {
		body   string
		denied bool
		status int
	}{
		{`{"expected_version":3,"decision_id":"dec_01K4AR9V8FQ2G7ZXCPNM5T6JWH","reviewer_id":"spoof"}`, false, http.StatusBadRequest},
		{`{"expected_version":3,"decision_id":"dec_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}`, false, http.StatusOK},
		{`{"expected_version":3,"decision_id":"dec_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}`, true, http.StatusForbidden},
	} {
		authority.denied = scenario.denied
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/review-cases/rvc_01K4AR9V8FQ2G7ZXCPNM5T6JWH/recaptures/acknowledgements", strings.NewReader(scenario.body))
		request.Header.Set("Authorization", "Bearer "+fixture.encoded)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", `"ack-1"`)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != scenario.status {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if repository.calls != 1 {
		t.Fatalf("unauthorized replay reached store: %d", repository.calls)
	}
}

func (repo *acknowledgementRepository) ReevaluateRecapture(_ context.Context, _ tenant.Scope, input review.AcknowledgeRecapture) (review.RecaptureReevaluation, error) {
	repo.calls++
	return review.RecaptureReevaluation{CaseID: input.CaseID, SourceVersion: input.CaseVersion, TargetVersion: input.CaseVersion + 1, DecisionID: input.DecisionID, ActorID: input.ActorID, ReviewerID: input.ReviewerID, RecordedAt: input.At}, nil
}
func TestRecaptureReevaluationRechecksReviewerAuthority(t *testing.T) {
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("reviews:write"))
	repository := &acknowledgementRepository{}
	authority := &acknowledgementAuthority{}
	// Acknowledgement does not generate session identifiers or sign credentials.
	service, err := review.NewRecaptureService(repository, authority, &id.Generator{}, &access.CaptureTokenSigner{}, &access.OutcomeTokenSigner{}, clock.System{}, time.Hour, time.Minute, time.Hour, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	routes := &ReviewRoutes{access: fixture.middleware, handlerBase: newHandlerBase(fixture.logger, "review"), recapture: service}
	router := versionedRouter(t, routes)
	for _, scenario := range []struct {
		body   string
		denied bool
		status int
	}{
		{`{"expected_version":3,"decision_id":"dec_01K4AR9V8FQ2G7ZXCPNM5T6JWH","reviewer_id":"spoof"}`, false, http.StatusBadRequest},
		{`{"expected_version":3,"decision_id":"dec_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}`, false, http.StatusAccepted},
		{`{"expected_version":3,"decision_id":"dec_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}`, true, http.StatusForbidden},
	} {
		authority.denied = scenario.denied
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/review-cases/rvc_01K4AR9V8FQ2G7ZXCPNM5T6JWH/recaptures/reevaluations", strings.NewReader(scenario.body))
		request.Header.Set("Authorization", "Bearer "+fixture.encoded)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", `"ack-1"`)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != scenario.status {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if repository.calls != 1 {
		t.Fatalf("unauthorized replay reached store: %d", repository.calls)
	}
}
