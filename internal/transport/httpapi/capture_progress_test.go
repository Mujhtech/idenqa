package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mujhtech/idenqa/internal/evidence"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/go-chi/chi/v5"
)

type captureProgressFinderStub struct {
	principal evidence.UploadPrincipal
	progress  evidence.CaptureProgress
}

func (stub *captureProgressFinderStub) Find(
	_ context.Context,
	principal evidence.UploadPrincipal,
) (evidence.CaptureProgress, error) {
	stub.principal = principal

	return stub.progress, nil
}

func TestCaptureProgressRoutesUseExactAuthenticatedPrincipal(t *testing.T) {
	t.Parallel()

	fixture := newEvidenceUploadHTTPFixture(t)
	record := fixture.upload.Record()
	finder := &captureProgressFinderStub{progress: evidence.CaptureProgress{
		VerificationID: record.VerificationID,
		Completions: []evidence.CaptureCompletion{{
			UploadID: record.ID, EvidenceID: record.EvidenceID,
			RequirementKey: record.RequirementKey, EvidenceType: record.EvidenceType,
			Artefact: record.Artefact, AcquisitionMethod: record.AcquisitionMethod,
		}},
	}}
	routes, err := NewCaptureProgressRoutes(fixture.capture, finder, fixture.logger)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	routes.Register(router)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/capture/progress", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
	if response.Header().Get("ETag") == "" ||
		response.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("conditional headers=%v", response.Header())
	}
	if finder.principal.CaptureTokenID != record.CaptureTokenID ||
		finder.principal.VerificationID != record.VerificationID ||
		finder.principal.Scope.ID() != record.TenantID {
		t.Fatalf("principal=%+v", finder.principal)
	}
	var progress openapiv1.CaptureProgress
	if err := json.Unmarshal(response.Body.Bytes(), &progress); err != nil {
		t.Fatal(err)
	}
	if progress.VerificationID != record.VerificationID.String() || len(progress.Completions) != 1 ||
		progress.Completions[0].UploadID != record.ID.String() {
		t.Fatalf("progress=%+v", progress)
	}
	conditional := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/capture/progress", nil)
	conditional.Header.Set("Authorization", "Bearer "+fixture.token)
	conditional.Header.Set("If-None-Match", response.Header().Get("ETag"))
	notModified := httptest.NewRecorder()
	router.ServeHTTP(notModified, conditional)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 ||
		notModified.Header().Get("ETag") != response.Header().Get("ETag") {
		t.Fatalf("conditional status=%d headers=%v body=%q", notModified.Code, notModified.Header(), notModified.Body.String())
	}
}
