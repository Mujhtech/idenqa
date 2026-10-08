package localreadinessbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

type serviceStub struct {
	snapshot Snapshot
	err      error
}

func (stub serviceStub) Snapshot(context.Context) (Snapshot, error) { return stub.snapshot, stub.err }

func TestHandlerExposesOnlyFixedReadinessSnapshot(t *testing.T) {
	handler, err := NewHandler(serviceStub{snapshot: Snapshot{Version: ContractVersion, CoreRevision: "revision", APIVersion: "1.0.0-alpha.1", URIMajor: 1, Checks: []Check{{Name: "database", Status: "pass"}}}})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/local/v1/readiness", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	for _, target := range []string{"/local/v1/readiness/other", "/local/v1/command"} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("target=%s status=%d", target, response.Code)
		}
	}
}
