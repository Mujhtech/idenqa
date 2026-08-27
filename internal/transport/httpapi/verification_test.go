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
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/go-chi/chi/v5"
)

func TestVerificationRoutesCreateAndCaptureSnapshot(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("verification_sessions:*"))
	registry, catalog, document := profileHTTPDocument(t)
	now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	verificationID, err := id.ParseVerification("ver_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseVerification() error = %v", err)
	}
	profileID, err := id.ParseProfile("prf_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	digest, err := verification.Digest(document, registry)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	session, err := verification.RestoreSession(
		verificationID,
		fixture.tenantID,
		verification.SessionStateCollecting,
		1,
		profileID,
		1,
		digest,
		document,
		"local",
		policyID,
		now,
		now,
		now.Add(24*time.Hour),
		registry,
	)
	if err != nil {
		t.Fatalf("RestoreSession() error = %v", err)
	}
	tokenID, err := id.ParseCaptureToken("ctk_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseCaptureToken() error = %v", err)
	}
	credential, err := access.NewCaptureCredential(
		tokenID,
		fixture.tenantID,
		verificationID,
		1,
		now,
		now.Add(30*time.Minute),
	)
	if err != nil {
		t.Fatalf("NewCaptureCredential() error = %v", err)
	}
	keyring, err := access.NewCaptureTokenKeyring(1, map[access.CaptureTokenKeyVersion][]byte{
		1: bytes.Repeat([]byte{6}, 32),
	})
	if err != nil {
		t.Fatalf("NewCaptureTokenKeyring() error = %v", err)
	}
	signer, err := access.NewCaptureTokenSigner(keyring, httpAccessClock{now: now.Add(time.Minute)})
	if err != nil {
		t.Fatalf("NewCaptureTokenSigner() error = %v", err)
	}
	presented, err := signer.Sign(credential)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	creation := verification.SessionCreation{Session: session, Credential: credential}
	captureAuthenticator, err := verification.NewCaptureAuthenticator(
		httpCaptureRepository{creation: creation},
		signer,
		httpAccessClock{now: now.Add(time.Minute)},
	)
	if err != nil {
		t.Fatalf("NewCaptureAuthenticator() error = %v", err)
	}
	captureMiddleware, err := NewCaptureAccessMiddleware(captureAuthenticator, fixture.logger)
	if err != nil {
		t.Fatalf("NewCaptureAccessMiddleware() error = %v", err)
	}
	service := &verificationHTTPServiceStub{
		created: verification.CreatedSession{Session: session, CaptureToken: presented},
		session: session,
	}
	routes, err := NewVerificationRoutes(fixture.middleware, captureMiddleware, service, catalog, fixture.logger)
	if err != nil {
		t.Fatalf("NewVerificationRoutes() error = %v", err)
	}
	router := chi.NewRouter()
	routes.Register(router)

	body, err := json.Marshal(openapiv1.VerificationCreate{
		CaptureProfileID:       profileID.String(),
		PolicyID:               policyID.String(),
		VerificationTTLSeconds: int64Pointer(3600),
		CaptureTokenTTLSeconds: int64Pointer(600),
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/v1/verifications",
		bytes.NewReader(body),
	)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", `"attempt-1"`)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body)
	}
	if service.idempotencyKey != "attempt-1" || service.input.ProfileID.String() != profileID.String() ||
		service.input.VerificationTTL == nil || *service.input.VerificationTTL != time.Hour ||
		service.input.CaptureTokenTTL == nil || *service.input.CaptureTokenTTL != 10*time.Minute {
		t.Fatalf("service input key=%q input=%+v", service.idempotencyKey, service.input)
	}
	var created openapiv1.VerificationCreated
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.CaptureToken == nil || *created.CaptureToken != presented.Reveal() ||
		created.Session.ProfileDigest != digest {
		t.Fatalf("created response = %+v", created)
	}

	captureRequest := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/v1/capture/session",
		nil,
	)
	captureRequest.Header.Set("Authorization", "Bearer "+presented.Reveal())
	captureResponse := httptest.NewRecorder()
	router.ServeHTTP(captureResponse, captureRequest)
	if captureResponse.Code != http.StatusOK {
		t.Fatalf("capture status = %d, want %d; body=%s", captureResponse.Code, http.StatusOK, captureResponse.Body)
	}
	var snapshot openapiv1.VerificationSession
	if err := json.Unmarshal(captureResponse.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode capture response: %v", err)
	}
	if snapshot.ID != verificationID.String() || !bytes.Equal(snapshot.Requirements, created.Session.Requirements) {
		t.Fatalf("capture snapshot = %+v", snapshot)
	}
}

type httpCaptureRepository struct{ creation verification.SessionCreation }

func (repository httpCaptureRepository) FindForCapture(
	context.Context,
	access.CaptureTokenClaims,
) (verification.SessionCreation, error) {
	return repository.creation, nil
}

type verificationHTTPServiceStub struct {
	created        verification.CreatedSession
	session        verification.Session
	idempotencyKey string
	input          verification.SessionCreateInput
}

func (service *verificationHTTPServiceStub) Create(
	_ context.Context,
	_ access.Context,
	idempotencyKey string,
	input verification.SessionCreateInput,
) (verification.CreatedSession, error) {
	service.idempotencyKey = idempotencyKey
	service.input = input

	return service.created, nil
}

func (service *verificationHTTPServiceStub) Find(
	context.Context,
	access.Context,
	id.Verification,
) (verification.Session, error) {
	return service.session, nil
}

func int64Pointer(value int64) *int64 { return &value }
