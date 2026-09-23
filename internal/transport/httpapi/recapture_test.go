package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type recaptureProbe struct {
	acknowledged bool
	calls        int
	version      int64
	key          string
}

func (probe *recaptureProbe) Create(_ context.Context, _ access.Context, _ id.ReviewCase, version int64, key string) (verification.CreatedSession, error) {
	probe.calls++
	probe.version = version
	probe.key = key
	return verification.CreatedSession{}, review.ErrConflict
}
func TestRecaptureRouteRequiresBothScopesAndClosedBody(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		scopes []access.Pattern
		body   string
		calls  int
	}{
		{"review only", []access.Pattern{"reviews:write"}, `{"expected_version":3}`, 0},
		{"creation only", []access.Pattern{"verification_sessions:create"}, `{"expected_version":3}`, 0},
		{"profile injection", []access.Pattern{"reviews:write", "verification_sessions:create"}, `{"expected_version":3,"capture_profile_id":"other"}`, 0},
		{"valid", []access.Pattern{"reviews:write", "verification_sessions:create"}, `{"expected_version":3}`, 1},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture := newHTTPAccessFixture(t, nil, scenario.scopes...)
			probe := &recaptureProbe{}
			routes := &ReviewRoutes{access: fixture.middleware, handlerBase: newHandlerBase(fixture.logger, "review"), recapture: probe}
			router := versionedRouter(t, routes)
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/review-cases/rvc_01K4AR9V8FQ2G7ZXCPNM5T6JWH/recaptures", strings.NewReader(scenario.body))
			request.Header.Set("Authorization", "Bearer "+fixture.encoded)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", `"attempt-1"`)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if probe.calls != scenario.calls {
				t.Fatalf("calls=%d status=%d body=%s", probe.calls, response.Code, response.Body.String())
			}
			if scenario.calls == 1 && (probe.version != 3 || probe.key != "attempt-1") {
				t.Fatal("lost command")
			}
		})
	}
}

func (probe *recaptureProbe) Renew(_ context.Context, _ access.Context, _ id.ReviewCase, version int64, _ id.CaptureToken, key string) (verification.CreatedSession, error) {
	probe.calls++
	probe.version = version
	probe.key = key
	return verification.CreatedSession{}, review.ErrConflict
}
func (probe *recaptureProbe) List(_ context.Context, _ access.Context, _ id.ReviewCase) ([]review.RecaptureStatus, error) {
	child, _ := id.ParseVerification("ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	decision, _ := id.ParseDecision("dec_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	var at *time.Time
	if probe.acknowledged {
		value := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
		at = &value
	}
	return []review.RecaptureStatus{{AcknowledgedAt: at, CaseVersion: 3, ChildID: child, State: verification.SessionStateCompleted, DecisionID: decision, Outcome: "verified"}}, nil
}
func TestRecaptureStatusContainsOutcomeWithoutBearer(t *testing.T) {
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("reviews:read"))
	probe := &recaptureProbe{}
	routes := &ReviewRoutes{access: fixture.middleware, handlerBase: newHandlerBase(fixture.logger, "review"), recapture: probe}
	router := versionedRouter(t, routes)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/review-cases/rvc_01K4AR9V8FQ2G7ZXCPNM5T6JWH/recaptures", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), `"capture_token":`) || !strings.Contains(response.Body.String(), `"follow_up_required":true`) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
	probe.acknowledged = true
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"follow_up_required":false`) || !strings.Contains(response.Body.String(), `"acknowledged_at"`) {
		t.Fatalf("acknowledged status=%s", response.Body.String())
	}

}
