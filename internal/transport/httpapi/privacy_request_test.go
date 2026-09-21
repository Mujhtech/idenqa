package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type privacyRequestTestService struct {
	created     privacy.CreateRequestInput
	subject     privacy.CreateRequestInput
	decided     privacy.ReasonCode
	withdrawn   int64
	executed    int64
	lifted      privacy.RestrictionReason
	disclosures int
	processors  int
}

func (service *privacyRequestTestService) Create(_ context.Context, _ tenant.Scope, _ privacy.Actor, input privacy.CreateRequestInput) (privacy.Request, error) {
	service.created = input
	identifier, _ := id.ParsePrivacyRequest("prq_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	return privacy.Request{ID: identifier, Type: input.Type, State: privacy.RequestStateRequested, Channel: privacy.ChannelTenantAPI, SubjectID: input.SubjectID, Region: input.Region, Payload: input.Payload, RequestedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Version: 1}, nil
}

func (service *privacyRequestTestService) CreateSubject(_ context.Context, _ privacy.SubjectAuthority, input privacy.CreateRequestInput) (privacy.SubjectRequest, error) {
	service.subject = input
	return privacy.SubjectRequest{Type: input.Type, Status: privacy.SubjectStatusReceived, RequestedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)}, nil
}

func (service *privacyRequestTestService) Find(context.Context, tenant.Scope, privacy.Actor, id.PrivacyRequest) (privacy.Request, error) {
	return privacy.Request{}, errors.New("not used")
}

func (service *privacyRequestTestService) List(context.Context, tenant.Scope, privacy.Actor, privacy.RequestFilter, string, int) (privacy.RequestPage, error) {
	return privacy.RequestPage{}, errors.New("not used")
}

func (service *privacyRequestTestService) SubjectList(context.Context, privacy.SubjectAuthority, string, int) (privacy.SubjectRequestPage, error) {
	return privacy.SubjectRequestPage{Requests: []privacy.SubjectRequest{{Type: privacy.RequestErasure, Status: privacy.SubjectStatusInReview, RequestedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 20, 12, 5, 0, 0, time.UTC)}}}, nil
}

func (service *privacyRequestTestService) Decide(_ context.Context, _ tenant.Scope, _ privacy.Actor, _ id.PrivacyRequest, _ privacy.DecisionOutcome, reason privacy.ReasonCode, _ int64) (privacy.Request, error) {
	service.decided = reason
	identifier, _ := id.ParsePrivacyRequest("prq_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	return privacy.Request{ID: identifier, Type: privacy.RequestAccess, State: privacy.RequestStateApproved, Channel: privacy.ChannelTenantAPI, Region: "ng-1", RequestedAt: time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 20, 12, 5, 0, 0, time.UTC), ExpiresAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC), Version: 3}, nil
}

func (service *privacyRequestTestService) Withdraw(_ context.Context, _ tenant.Scope, _ privacy.Actor, _ id.PrivacyRequest, expectedVersion int64) (privacy.Request, error) {
	service.withdrawn = expectedVersion
	return privacy.Request{}, errors.New("not used")
}

func (service *privacyRequestTestService) Execute(_ context.Context, _ tenant.Scope, _ privacy.Actor, _ id.PrivacyRequest, expectedVersion int64) (privacy.Request, error) {
	service.executed = expectedVersion
	return privacy.Request{}, errors.New("not used")
}

func (service *privacyRequestTestService) Restrictions(context.Context, tenant.Scope, privacy.Actor, string, string, int) ([]privacy.Restriction, error) {
	return nil, errors.New("not used")
}

func (service *privacyRequestTestService) LiftRestriction(_ context.Context, _ tenant.Scope, _ privacy.Actor, _ id.PrivacyRestriction, reason privacy.RestrictionReason, _ int64) (privacy.Restriction, error) {
	service.lifted = reason
	return privacy.Restriction{}, errors.New("not used")
}

func (service *privacyRequestTestService) CreateDisclosure(context.Context, tenant.Scope, privacy.Actor, id.PrivacyRequest, string, string, privacy.DisclosureClass, string, string, string) (privacy.Disclosure, error) {
	service.disclosures++
	return privacy.Disclosure{}, errors.New("not used")
}

func (service *privacyRequestTestService) ListDisclosures(context.Context, tenant.Scope, privacy.Actor, id.PrivacyRequest, string, int) ([]privacy.Disclosure, error) {
	return nil, errors.New("not used")
}

func (service *privacyRequestTestService) PutProcessor(context.Context, tenant.Scope, privacy.Actor, id.Processor, int64, string, privacy.ProcessorRole, string, []privacy.DataClass, []string, string) (privacy.Processor, error) {
	service.processors++
	return privacy.Processor{}, errors.New("not used")
}

func (service *privacyRequestTestService) ListProcessors(context.Context, tenant.Scope, privacy.Actor, string, int) ([]privacy.Processor, error) {
	return nil, errors.New("not used")
}

func (service *privacyRequestTestService) FindProcessor(context.Context, tenant.Scope, privacy.Actor, id.Processor) (privacy.Processor, error) {
	return privacy.Processor{}, errors.New("not used")
}

func privacyRequestOutcomeFixture(t *testing.T, fixture *httpAccessFixture) (*OutcomeAccessMiddleware, string) {
	t.Helper()
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	verificationID, err := id.ParseVerification("ver_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	tokenID, err := id.ParseOutcomeToken("otk_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	credential, err := access.NewOutcomeCredential(tokenID, fixture.tenantID, verificationID, 1, now, now.Add(25*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	keyring, err := access.NewOutcomeTokenKeyring(1, map[access.OutcomeTokenKeyVersion][]byte{1: bytes.Repeat([]byte{7}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := access.NewOutcomeTokenSigner(keyring, httpAccessClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	presented, err := signer.Sign(credential)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := verification.NewOutcomeAuthenticator(httpOutcomeRepository{credential: credential}, signer, httpAccessClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := NewOutcomeAccessMiddleware(authenticator, fixture.logger)
	if err != nil {
		t.Fatal(err)
	}
	return middleware, presented.Reveal()
}

func privacyRequestTestRouter(t *testing.T, fixture *httpAccessFixture, service *privacyRequestTestService, outcome *OutcomeAccessMiddleware) http.Handler {
	t.Helper()
	routes, err := NewPrivacyRoutes(fixture.middleware, &privacyTestService{}, service, outcome, privacyTestCursor(t), fixture.logger)
	if err != nil {
		t.Fatal(err)
	}
	return versionedRouter(t, routes)
}

func TestSubjectPrivacyRequestSurfaceIsClosedProjection(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("privacy_requests:read"))
	outcome, outcomeToken := privacyRequestOutcomeFixture(t, fixture)
	service := &privacyRequestTestService{}
	handler := privacyRequestTestRouter(t, fixture, service, outcome)

	body := `{"type":"erasure","region":"ng-1","subject_id":"sub_01ARZ3NDEKTSV4RRFFQ69G5FAV"}`
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/capture/privacy-requests", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+outcomeToken)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
	if service.subject.Type != privacy.RequestErasure || service.subject.Region != "ng-1" {
		t.Fatalf("subject input = %+v", service.subject)
	}
	var projection openapiv1.SubjectPrivacyRequest
	if err := json.Unmarshal(response.Body.Bytes(), &projection); err != nil {
		t.Fatal(err)
	}
	encoded := response.Body.String()
	for _, forbidden := range []string{"prq_", "reason", "request_id", "effect", "actor", "channel"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("subject projection leaked %q: %s", forbidden, encoded)
		}
	}
	if projection.Status != openapiv1.SubjectPrivacyStatus("received") || projection.Type != openapiv1.PrivacyRequestType("erasure") {
		t.Fatalf("projection = %+v", projection)
	}

	list := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/capture/privacy-requests", nil)
	list.Header.Set("Authorization", "Bearer "+outcomeToken)
	listResponse := httptest.NewRecorder()
	handler.ServeHTTP(listResponse, list)
	if listResponse.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", listResponse.Code, listResponse.Body)
	}
	if !strings.Contains(listResponse.Body.String(), `"status":"in_review"`) || strings.Contains(listResponse.Body.String(), "prq_") {
		t.Fatalf("subject list = %s", listResponse.Body)
	}
}

func TestSubjectCredentialCannotDecideOrExecute(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("privacy_requests:read"))
	outcome, outcomeToken := privacyRequestOutcomeFixture(t, fixture)
	service := &privacyRequestTestService{}
	handler := privacyRequestTestRouter(t, fixture, service, outcome)

	for _, path := range []string{
		"/v1/privacy-requests/prq_01ARZ3NDEKTSV4RRFFQ69G5FAV/approve",
		"/v1/privacy-requests/prq_01ARZ3NDEKTSV4RRFFQ69G5FAV/deny",
		"/v1/privacy-requests/prq_01ARZ3NDEKTSV4RRFFQ69G5FAV/withdraw",
		"/v1/privacy-requests/prq_01ARZ3NDEKTSV4RRFFQ69G5FAV/execute",
		"/v1/privacy-requests",
		"/v1/privacy-processors",
	} {
		request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, strings.NewReader(`{"expected_version":2,"reason_code":"access_approved"}`))
		request.Header.Set("Authorization", "Bearer "+outcomeToken)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d body=%s", path, response.Code, response.Body)
		}
	}
	if service.decided != "" || service.executed != 0 || service.withdrawn != 0 || service.processors != 0 || service.disclosures != 0 {
		t.Fatalf("outcome credential reached consequential service: %+v", service)
	}
}

func TestTenantPrivacyRequestPermissionSeparation(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("privacy_requests:write"))
	outcome, _ := privacyRequestOutcomeFixture(t, fixture)
	service := &privacyRequestTestService{}
	handler := privacyRequestTestRouter(t, fixture, service, outcome)

	create := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/privacy-requests", strings.NewReader(`{"type":"access","subject_id":"sub_01ARZ3NDEKTSV4RRFFQ69G5FAV","region":"ng-1"}`))
	create.Header.Set("Authorization", "Bearer "+fixture.encoded)
	create.Header.Set("Content-Type", "application/json")
	createResponse := httptest.NewRecorder()
	handler.ServeHTTP(createResponse, create)
	if createResponse.Code != http.StatusAccepted {
		t.Fatalf("create status = %d body=%s", createResponse.Code, createResponse.Body)
	}

	approve := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/privacy-requests/prq_01ARZ3NDEKTSV4RRFFQ69G5FAV/approve", strings.NewReader(`{"expected_version":2,"reason_code":"access_approved"}`))
	approve.Header.Set("Authorization", "Bearer "+fixture.encoded)
	approve.Header.Set("Content-Type", "application/json")
	approveResponse := httptest.NewRecorder()
	handler.ServeHTTP(approveResponse, approve)
	if approveResponse.Code != http.StatusForbidden {
		t.Fatalf("approve without approve scope = %d body=%s", approveResponse.Code, approveResponse.Body)
	}
	if service.decided != "" {
		t.Fatalf("write-only credential decided: %q", service.decided)
	}
}
