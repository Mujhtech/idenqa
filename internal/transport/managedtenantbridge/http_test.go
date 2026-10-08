package managedtenantbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/managedtenant"
)

type serviceStub struct {
	request          managedtenant.Request
	result           managedtenant.Result
	err              error
	syntheticRequest managedtenant.SyntheticRunRequest
	syntheticResult  managedtenant.SyntheticRunResult
}

func (stub *serviceStub) Provision(_ context.Context, request managedtenant.Request) (managedtenant.Result, error) {
	stub.request = request
	return stub.result, stub.err
}

func (stub *serviceStub) Renew(_ context.Context, request managedtenant.RenewRequest) (managedtenant.Result, error) {
	stub.request = managedtenant.Request{CommandID: request.CommandID, DeliveryPublicKey: request.DeliveryPublicKey}
	return stub.result, stub.err
}

func (stub *serviceStub) ProvisionSynthetic(context.Context, managedtenant.SyntheticProvisionRequest) (managedtenant.SyntheticProvisionResult, error) {
	return managedtenant.SyntheticProvisionResult{}, stub.err
}

func (stub *serviceStub) RunSynthetic(_ context.Context, request managedtenant.SyntheticRunRequest) (managedtenant.SyntheticRunResult, error) {
	stub.syntheticRequest = request
	return stub.syntheticResult, stub.err
}

func TestHandlerAcceptsOnlyTheFixedProvisionContract(t *testing.T) {
	t.Parallel()
	service := &serviceStub{result: managedtenant.Result{TenantID: "ten_01K6C3F6M7Z8W9X0Y1A2B3C4D5"}}
	handler, err := NewHandler(service)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/local/v1/managed-tenants/provision", strings.NewReader(`{"commandId":"onboarding-command-0001","deliveryPublicKey":"public"}`))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if service.request.CommandID != "onboarding-command-0001" || service.request.DeliveryPublicKey != "public" {
		t.Fatalf("request = %#v", service.request)
	}
	var result managedtenant.Result
	if err := json.NewDecoder(response.Body).Decode(&result); err != nil || result.TenantID != service.result.TenantID {
		t.Fatalf("result = %#v, error = %v", result, err)
	}
}

func TestHandlerRunsOnlyTheFixedSyntheticContract(t *testing.T) {
	t.Parallel()
	observed := time.Date(2026, time.October, 1, 10, 0, 0, 0, time.UTC)
	service := &serviceStub{syntheticResult: managedtenant.SyntheticRunResult{TenantID: "ten_01K6C3F6M7Z8W9X0Y1A2B3C4D5", Outcome: "verified", ObservedAt: observed}}
	handler, err := NewHandler(service)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"runId":"sjr_12345678","deploymentId":"dep_12345678","coreEndpoint":"https://core.example.test","profileDocument":{},"policyDocument":{},"fixtureVersion":"builtin.synthetic.selfie.v1","region":"ng-lagos-1","expectedOutcome":"verified","timeoutSeconds":60}`
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/local/v1/synthetic-journeys/run", strings.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if service.syntheticRequest.RunID != "sjr_12345678" || service.syntheticRequest.DeploymentID != "dep_12345678" {
		t.Fatalf("request=%#v", service.syntheticRequest)
	}
	if strings.Contains(response.Body.String(), "credential") {
		t.Fatalf("response exposed credential metadata: %s", response.Body.String())
	}
}

func TestHandlerRejectsUnknownAndTrailingInput(t *testing.T) {
	t.Parallel()
	handler, _ := NewHandler(&serviceStub{})
	for _, body := range []string{
		`{"commandId":"onboarding-command-0001","deliveryPublicKey":"public","executable":"/bin/sh"}`,
		`{"commandId":"onboarding-command-0001","deliveryPublicKey":"public"} {}`,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/local/v1/managed-tenants/provision", strings.NewReader(body)))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("body %q status = %d, want %d", body, response.Code, http.StatusBadRequest)
		}
	}
}

func TestHandlerDoesNotExposeProvisioningFailure(t *testing.T) {
	t.Parallel()
	handler, _ := NewHandler(&serviceStub{err: errors.New("database detail")})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/local/v1/managed-tenants/provision", strings.NewReader(`{"commandId":"onboarding-command-0001","deliveryPublicKey":"public"}`)))
	if response.Code != http.StatusConflict || strings.Contains(response.Body.String(), "database detail") {
		t.Fatalf("response = %d %q", response.Code, response.Body.String())
	}
}
