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
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
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
	outcomeTokenID, err := id.ParseOutcomeToken("otk_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseOutcomeToken() error = %v", err)
	}
	outcomeCredential, err := access.NewOutcomeCredential(
		outcomeTokenID, fixture.tenantID, verificationID, 1, now, now.Add(25*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewOutcomeCredential() error = %v", err)
	}
	outcomeKeyring, err := access.NewOutcomeTokenKeyring(1, map[access.OutcomeTokenKeyVersion][]byte{
		1: bytes.Repeat([]byte{7}, 32),
	})
	if err != nil {
		t.Fatalf("NewOutcomeTokenKeyring() error = %v", err)
	}
	outcomeSigner, err := access.NewOutcomeTokenSigner(
		outcomeKeyring,
		httpAccessClock{now: now.Add(time.Minute)},
	)
	if err != nil {
		t.Fatalf("NewOutcomeTokenSigner() error = %v", err)
	}
	outcomePresented, err := outcomeSigner.Sign(outcomeCredential)
	if err != nil {
		t.Fatalf("Sign(outcome) error = %v", err)
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
	outcomeAuthenticator, err := verification.NewOutcomeAuthenticator(
		httpOutcomeRepository{credential: outcomeCredential},
		outcomeSigner,
		httpAccessClock{now: now.Add(time.Minute)},
	)
	if err != nil {
		t.Fatalf("NewOutcomeAuthenticator() error = %v", err)
	}
	outcomeMiddleware, err := NewOutcomeAccessMiddleware(outcomeAuthenticator, fixture.logger)
	if err != nil {
		t.Fatalf("NewOutcomeAccessMiddleware() error = %v", err)
	}
	service := &verificationHTTPServiceStub{
		created: verification.CreatedSession{
			Session: session, CaptureToken: presented,
			OutcomeCredential: outcomeCredential, OutcomeToken: outcomePresented,
		},
		session: session,
	}
	routes, err := NewVerificationRoutes(fixture.middleware, captureMiddleware, service, catalog, nil, nil, fixture.logger)
	if err != nil {
		t.Fatalf("NewVerificationRoutes() error = %v", err)
	}
	outcomeRoutes, err := NewCaptureOutcomeRoutes(
		outcomeMiddleware,
		captureOutcomeHTTPServiceStub{outcome: verification.CaptureOutcome{
			VerificationID: verificationID,
			State:          verification.CaptureOutcomeProcessing,
			SessionVersion: 2,
			UpdatedAt:      now.Add(2 * time.Minute),
		}},
		fixture.logger,
	)
	if err != nil {
		t.Fatalf("NewCaptureOutcomeRoutes() error = %v", err)
	}
	router := versionedRouter(t, routes, outcomeRoutes)

	body, err := json.Marshal(openapiv1.VerificationCreate{
		CaptureProfileID:                 profileID.String(),
		PolicyID:                         policyID.String(),
		VerificationTTLSeconds:           int64Pointer(3600),
		CaptureTokenTTLSeconds:           int64Pointer(600),
		OutcomeTokenPostExpiryTTLSeconds: int64Pointer(7200),
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
		service.input.CaptureTokenTTL == nil || *service.input.CaptureTokenTTL != 10*time.Minute ||
		service.input.OutcomePostTTL == nil || *service.input.OutcomePostTTL != 2*time.Hour {
		t.Fatalf("service input key=%q input=%+v", service.idempotencyKey, service.input)
	}
	var created openapiv1.VerificationCreated
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if created.CaptureToken == nil || *created.CaptureToken != presented.Reveal() ||
		created.OutcomeToken == nil || *created.OutcomeToken != outcomePresented.Reveal() ||
		!created.OutcomeTokenExpiresAt.Equal(outcomeCredential.ExpiresAt()) ||
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

	outcomeRequest := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/v1/capture/outcome",
		nil,
	)
	outcomeRequest.Header.Set("Authorization", "Bearer "+outcomePresented.Reveal())
	outcomeResponse := httptest.NewRecorder()
	router.ServeHTTP(outcomeResponse, outcomeRequest)
	if outcomeResponse.Code != http.StatusOK {
		t.Fatalf("outcome status = %d, want %d; body=%s", outcomeResponse.Code, http.StatusOK, outcomeResponse.Body)
	}
	var outcome openapiv1.CaptureOutcome
	if err := json.Unmarshal(outcomeResponse.Body.Bytes(), &outcome); err != nil {
		t.Fatalf("decode outcome response: %v", err)
	}
	if outcome.VerificationID != verificationID.String() || outcome.State != openapiv1.CaptureOutcomeStateProcessing ||
		outcome.SessionVersion != 2 || !outcome.UpdatedAt.Equal(now.Add(2*time.Minute)) {
		t.Fatalf("capture outcome = %+v", outcome)
	}

	for _, misuse := range []struct {
		name  string
		path  string
		token string
	}{
		{name: "outcome token on capture route", path: "/v1/capture/session", token: outcomePresented.Reveal()},
		{name: "capture token on outcome route", path: "/v1/capture/outcome", token: presented.Reveal()},
	} {
		t.Run(misuse.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, misuse.path, nil)
			request.Header.Set("Authorization", "Bearer "+misuse.token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("credential misuse status = %d, want %d; body=%s", response.Code, http.StatusUnauthorized, response.Body)
			}
		})
	}
}

func TestVerificationRoutesProjectCurrentDecisionAndCase(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(
		t, nil,
		access.Pattern("verification_sessions:read"),
		access.Pattern("decisions:read"),
		access.Pattern("reviews:read"),
	)
	registry, catalog, document := profileHTTPDocument(t)
	now := time.Date(2026, time.September, 19, 10, 0, 0, 0, time.UTC)
	verificationID, err := id.ParseVerification("ver_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseVerification() error = %v", err)
	}
	profileID, err := id.ParseProfile("prf_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseProfile() error = %v", err)
	}
	policyID, err := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("ParsePolicy() error = %v", err)
	}
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
	captureAuthenticator, err := verification.NewCaptureAuthenticator(
		httpCaptureRepository{creation: verification.SessionCreation{Session: session, Credential: credential}},
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
	decisionID, err := id.ParseDecision("dec_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseDecision() error = %v", err)
	}
	caseID, err := id.ParseReviewCase("rvc_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseReviewCase() error = %v", err)
	}
	decisions := &verificationDecisionReaderStub{report: policy.ReproductionReport{
		DecisionID: decisionID.String(),
		Outcome:    policy.OutcomeVerified,
		Directive:  policy.DirectiveCompleteVerified,
		DecidedAt:  now,
	}}
	cases := &verificationCaseReaderStub{value: review.Case{
		ID: caseID, VerificationID: verificationID, State: review.CaseClaimed, Version: 2,
	}}
	routes, err := NewVerificationRoutes(
		fixture.middleware,
		captureMiddleware,
		&verificationHTTPServiceStub{session: session},
		catalog,
		decisions,
		cases,
		fixture.logger,
	)
	if err != nil {
		t.Fatalf("NewVerificationRoutes() error = %v", err)
	}
	router := versionedRouter(t, routes)

	readRequest := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/v1/verifications/"+verificationID.String(),
		nil,
	)
	readRequest.Header.Set("Authorization", "Bearer "+fixture.encoded)
	readResponse := httptest.NewRecorder()
	router.ServeHTTP(readResponse, readRequest)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("read status = %d, want %d; body=%s", readResponse.Code, http.StatusOK, readResponse.Body)
	}
	var snapshot openapiv1.VerificationSession
	if err := json.Unmarshal(readResponse.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("decode read response: %v", err)
	}
	if snapshot.CurrentDecision == nil ||
		snapshot.CurrentDecision.DecisionID != decisionID.String() ||
		snapshot.CurrentDecision.Outcome != openapiv1.PolicyOutcome(policy.OutcomeVerified) ||
		snapshot.CurrentDecision.Directive != openapiv1.PolicyDirective(policy.DirectiveCompleteVerified) ||
		!snapshot.CurrentDecision.DecidedAt.Equal(now) {
		t.Fatalf("current_decision = %+v", snapshot.CurrentDecision)
	}
	if snapshot.CurrentCase == nil || snapshot.CurrentCase.CaseID != caseID.String() ||
		snapshot.CurrentCase.State != string(review.CaseClaimed) || snapshot.CurrentCase.Version != 2 {
		t.Fatalf("current_case = %+v", snapshot.CurrentCase)
	}
	if decisions.calls != 1 || cases.calls != 1 {
		t.Fatalf("reader calls = decisions:%d cases:%d, want 1 and 1", decisions.calls, cases.calls)
	}

	limited := newHTTPAccessFixture(t, nil, access.Pattern("verification_sessions:read"))
	limitedRoutes, err := NewVerificationRoutes(
		limited.middleware,
		captureMiddleware,
		&verificationHTTPServiceStub{session: session},
		catalog,
		decisions,
		cases,
		limited.logger,
	)
	if err != nil {
		t.Fatalf("NewVerificationRoutes(limited) error = %v", err)
	}
	limitedRequest := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/v1/verifications/"+verificationID.String(),
		nil,
	)
	limitedRequest.Header.Set("Authorization", "Bearer "+limited.encoded)
	limitedResponse := httptest.NewRecorder()
	versionedRouter(t, limitedRoutes).ServeHTTP(limitedResponse, limitedRequest)
	if limitedResponse.Code != http.StatusOK {
		t.Fatalf("limited status = %d, want %d; body=%s", limitedResponse.Code, http.StatusOK, limitedResponse.Body)
	}
	var limitedSnapshot openapiv1.VerificationSession
	if err := json.Unmarshal(limitedResponse.Body.Bytes(), &limitedSnapshot); err != nil {
		t.Fatalf("decode limited response: %v", err)
	}
	if limitedSnapshot.CurrentDecision != nil || limitedSnapshot.CurrentCase != nil {
		t.Fatalf("limited projections = decision:%+v case:%+v", limitedSnapshot.CurrentDecision, limitedSnapshot.CurrentCase)
	}
	if decisions.calls != 1 || cases.calls != 1 {
		t.Fatalf("limited reader calls = decisions:%d cases:%d, want 1 and 1", decisions.calls, cases.calls)
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
	if bytes.Contains(captureResponse.Body.Bytes(), []byte("current_decision")) ||
		bytes.Contains(captureResponse.Body.Bytes(), []byte("current_case")) {
		t.Fatalf("capture response leaked projections: %s", captureResponse.Body)
	}
	if decisions.calls != 1 || cases.calls != 1 {
		t.Fatalf("capture reader calls = decisions:%d cases:%d, want 1 and 1", decisions.calls, cases.calls)
	}
}

func TestVerificationRoutesProjectRequestedInputOnlyOnTenantReads(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("verification_sessions:read"))
	registry, catalog, document := profileHTTPDocument(t)
	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, time.UTC)
	verificationID, err := id.ParseVerification("ver_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	profileID, err := id.ParseProfile("prf_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	policyID, err := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := verification.Digest(document, registry)
	if err != nil {
		t.Fatal(err)
	}
	session, err := verification.RestoreSession(
		verificationID, fixture.tenantID, verification.SessionStateAwaitingInput, 3,
		profileID, 1, digest, document, "local", policyID,
		now, now.Add(time.Minute), now.Add(time.Hour), registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	requestID, err := id.ParseInputRequest("inp_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	actor, err := id.ParseTask("tsk_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	request, err := verification.NewInputRequest(requestID, verificationID, id.ReviewCase{},
		[]string{"document_unavailable", "freshness_required"}, actor.String(), now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	session, err = session.WithInputRequest(request)
	if err != nil {
		t.Fatal(err)
	}

	keyring, err := access.NewCaptureTokenKeyring(1, map[access.CaptureTokenKeyVersion][]byte{1: bytes.Repeat([]byte{6}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	signer, err := access.NewCaptureTokenSigner(keyring, httpAccessClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	captureAuthenticator, err := verification.NewCaptureAuthenticator(httpCaptureRepository{}, signer, httpAccessClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	captureMiddleware, err := NewCaptureAccessMiddleware(captureAuthenticator, fixture.logger)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewVerificationRoutes(fixture.middleware, captureMiddleware, &verificationHTTPServiceStub{session: session}, catalog, nil, nil, fixture.logger)
	if err != nil {
		t.Fatal(err)
	}
	router := versionedRouter(t, routes)

	readRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/verifications/"+verificationID.String(), nil)
	readRequest.Header.Set("Authorization", "Bearer "+fixture.encoded)
	readResponse := httptest.NewRecorder()
	router.ServeHTTP(readResponse, readRequest)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("read status = %d; body=%s", readResponse.Code, readResponse.Body)
	}
	var snapshot openapiv1.VerificationSession
	if err := json.Unmarshal(readResponse.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.RequestedInput == nil ||
		len(snapshot.RequestedInput.ReasonCodes) != 2 ||
		snapshot.RequestedInput.ReasonCodes[0] != "document_unavailable" ||
		snapshot.RequestedInput.ReasonCodes[1] != "freshness_required" ||
		!snapshot.RequestedInput.RequestedAt.Equal(now.Add(2*time.Minute)) ||
		snapshot.RequestedInput.CaseID != nil {
		t.Fatalf("requested_input = %+v", snapshot.RequestedInput)
	}
	// The shared capture-safe builder never projects the request, even when the
	// aggregate happens to carry one.
	safe, err := verificationSessionResponse(catalog, session)
	if err != nil {
		t.Fatal(err)
	}
	if safe.RequestedInput != nil {
		t.Fatalf("capture-safe projection leaked requested_input: %+v", safe.RequestedInput)
	}

	bare := newHTTPAccessFixture(t, nil, access.Pattern("verification_sessions:read"))
	bareSession, err := verification.RestoreSession(
		verificationID, bare.tenantID, verification.SessionStateAwaitingInput, 3,
		profileID, 1, digest, document, "local", policyID,
		now, now.Add(time.Minute), now.Add(time.Hour), registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	bareRoutes, err := NewVerificationRoutes(bare.middleware, captureMiddleware, &verificationHTTPServiceStub{session: bareSession}, catalog, nil, nil, bare.logger)
	if err != nil {
		t.Fatal(err)
	}
	bareRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/verifications/"+verificationID.String(), nil)
	bareRequest.Header.Set("Authorization", "Bearer "+bare.encoded)
	bareResponse := httptest.NewRecorder()
	versionedRouter(t, bareRoutes).ServeHTTP(bareResponse, bareRequest)
	if bareResponse.Code != http.StatusOK {
		t.Fatalf("bare status = %d; body=%s", bareResponse.Code, bareResponse.Body)
	}
	if bytes.Contains(bareResponse.Body.Bytes(), []byte("requested_input")) {
		t.Fatalf("unrouted awaiting-input session projected requested_input: %s", bareResponse.Body)
	}
}

type httpCaptureRepository struct{ creation verification.SessionCreation }

func (repository httpCaptureRepository) FindForCapture(
	context.Context,
	access.CaptureTokenClaims,
) (verification.SessionCreation, error) {
	return repository.creation, nil
}

type httpOutcomeRepository struct{ credential access.OutcomeCredential }

func (repository httpOutcomeRepository) FindForOutcome(
	context.Context,
	access.OutcomeTokenClaims,
) (access.OutcomeCredential, error) {
	return repository.credential, nil
}

type verificationHTTPServiceStub struct {
	created        verification.CreatedSession
	session        verification.Session
	idempotencyKey string
	input          verification.SessionCreateInput
}

type captureOutcomeHTTPServiceStub struct{ outcome verification.CaptureOutcome }

func (service captureOutcomeHTTPServiceStub) Find(
	context.Context,
	verification.OutcomeContext,
) (verification.CaptureOutcome, error) {
	return service.outcome, nil
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

func (service *verificationHTTPServiceStub) Resume(
	context.Context,
	access.Context,
	id.Verification,
	int64,
	string,
) (verification.ResumedSession, error) {
	return verification.ResumedSession{}, verification.ErrSessionConflict
}

type verificationDecisionReaderStub struct {
	report policy.ReproductionReport
	err    error
	calls  int
}

func (reader *verificationDecisionReaderStub) FindLatest(
	context.Context,
	access.Context,
	id.Verification,
) (policy.ReproductionReport, error) {
	reader.calls++
	return reader.report, reader.err
}

type verificationCaseReaderStub struct {
	value review.Case
	err   error
	calls int
}

func (reader *verificationCaseReaderStub) FindCaseForVerification(
	context.Context,
	tenant.Scope,
	review.Actor,
	id.Verification,
) (review.Case, error) {
	reader.calls++
	return reader.value, reader.err
}

func int64Pointer(value int64) *int64 { return &value }

func TestVerificationSessionResponseProjectsOperationalFailure(t *testing.T) {
	t.Parallel()

	registry, catalog, document := profileHTTPDocument(t)
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	verificationID, err := id.ParseVerification("ver_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	tenantID, err := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	profileID, err := id.ParseProfile("prf_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	policyID, err := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	digest, err := verification.Digest(document, registry)
	if err != nil {
		t.Fatal(err)
	}
	session, err := verification.RestoreSession(
		verificationID, tenantID, verification.SessionStateFailed, 2,
		profileID, 1, digest, document, "local", policyID,
		now, now.Add(time.Minute), now.Add(time.Hour), registry,
	)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := session.WithFailure(verification.SessionFailure{Class: "policy", Code: "workflow_prohibited"})
	if err != nil {
		t.Fatal(err)
	}
	response, err := verificationSessionResponse(catalog, failed)
	if err != nil {
		t.Fatal(err)
	}
	if response.Failure == nil || response.Failure.Class != "policy" || response.Failure.Code != "workflow_prohibited" {
		t.Fatalf("tenant failure projection = %+v", response.Failure)
	}
	// The capture-token projection never loads failure detail, so the shared
	// builder must omit the property even for a failed state.
	captureSafe, err := verificationSessionResponse(catalog, session)
	if err != nil {
		t.Fatal(err)
	}
	if captureSafe.Failure != nil {
		t.Fatalf("subject-safe projection leaked failure: %+v", captureSafe.Failure)
	}
}
