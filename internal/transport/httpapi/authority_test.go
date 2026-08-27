package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/authority"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

func TestAuthorityRoutesCreateImmutableNotice(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("notices:*"))
	captureMiddleware, err := NewCaptureAccessMiddleware(authorityCaptureAuthenticator{}, fixture.logger)
	if err != nil {
		t.Fatalf("NewCaptureAccessMiddleware() error = %v", err)
	}
	now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	notice, err := authority.NewNotice(authority.NoticeRecord{
		ID: mustHTTPNotice(t, "ntc_01ARZ3NDEKTSV4RRFFQ69G5FAV"), TenantID: fixture.tenantID,
		Key: "tenant.notice.identity_verification", Locale: "en-NG",
		Controller: "Example Controller", Recipient: "Example Recipient",
		Copy: authority.NoticeCopy{Title: "Identity verification", Summary: "We verify identity.",
			Purpose: "Identity verification only.", Consequences: "Collection stops if refused."},
		EffectiveAt: now, CreatedAt: now, CreatedBy: mustHTTPAPIKey(t, "key_01ARZ3NDEKTSV4RRFFQ69G5FAW"),
	})
	if err != nil {
		t.Fatalf("NewNotice() error = %v", err)
	}
	service := &authorityHTTPServiceStub{notice: notice}
	routes, err := NewAuthorityRoutes(fixture.middleware, captureMiddleware, service, fixture.logger)
	if err != nil {
		t.Fatalf("NewAuthorityRoutes() error = %v", err)
	}
	router := chi.NewRouter()
	routes.Register(router)
	body, err := json.Marshal(openapiv1.NoticeVersionCreate{
		Key: notice.Key(), Locale: notice.Locale(), Controller: notice.Controller(), Recipient: notice.Recipient(),
		Copy: openapiv1.NoticeCopy{Title: notice.Copy().Title, Summary: notice.Copy().Summary,
			Purpose: notice.Copy().Purpose, Consequences: notice.Copy().Consequences},
		EffectiveAt: notice.EffectiveAt(),
	})
	if err != nil {
		t.Fatalf("marshal notice request: %v", err)
	}
	request := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/v1/notices", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", `"notice-1"`)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body)
	}
	if service.key != "notice-1" || service.input.Key != notice.Key() || service.input.Copy != notice.Copy() {
		t.Fatalf("service input key=%q input=%+v", service.key, service.input)
	}
	if got := response.Header().Get("Location"); got != "/v1/notices/"+notice.ID().String() {
		t.Fatalf("Location = %q", got)
	}
	var result openapiv1.NoticeVersion
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode notice response: %v", err)
	}
	if result.ID != notice.ID().String() || result.Digest != notice.Digest() {
		t.Fatalf("notice response = %+v", result)
	}
}

type authorityCaptureAuthenticator struct{}

func (authorityCaptureAuthenticator) Authenticate(context.Context, string) (verification.CaptureContext, error) {
	return verification.CaptureContext{}, access.ErrInvalidCaptureToken
}

type authorityHTTPServiceStub struct {
	notice authority.Notice
	key    string
	input  authority.NoticeInput
}

func (service *authorityHTTPServiceStub) CreateNotice(
	_ context.Context, _ access.Context, key string, input authority.NoticeInput,
) (authority.Notice, error) {
	service.key, service.input = key, input
	return service.notice, nil
}

func (service *authorityHTTPServiceStub) FindNotice(
	context.Context, access.Context, id.Notice,
) (authority.Notice, error) {
	return service.notice, nil
}

func (service *authorityHTTPServiceStub) Declare(
	context.Context, access.Context, string, authority.DeclarationInput,
) (authority.Authority, error) {
	return authority.Authority{}, nil
}

func (service *authorityHTTPServiceStub) FindByVerification(
	context.Context, access.Context, id.Verification,
) (authority.Authority, error) {
	return authority.Authority{}, nil
}

func (service *authorityHTTPServiceStub) Transition(
	context.Context, access.Context, id.Verification, string, int64, authority.State,
) (authority.Authority, error) {
	return authority.Authority{}, nil
}

func (service *authorityHTTPServiceStub) CaptureSnapshot(
	context.Context, verification.CaptureContext,
) (authority.Snapshot, error) {
	return authority.Snapshot{}, nil
}

func (service *authorityHTTPServiceStub) Respond(
	context.Context, verification.CaptureContext, string, authority.ResponseInput,
) (authority.Response, error) {
	return authority.Response{}, nil
}

func mustHTTPNotice(t *testing.T, value string) id.Notice {
	t.Helper()
	identifier, err := id.ParseNotice(value)
	if err != nil {
		t.Fatal(err)
	}
	return identifier
}

func mustHTTPAPIKey(t *testing.T, value string) id.APIKey {
	t.Helper()
	identifier, err := id.ParseAPIKey(value)
	if err != nil {
		t.Fatal(err)
	}
	return identifier
}
