//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/authority"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	policytask "github.com/Mujhtech/idenqa/internal/policy/task"
	reviewpostgres "github.com/Mujhtech/idenqa/internal/review/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/go-chi/chi/v5"
)

// inputRequestEvaluationBuilder supplies the policy fixture that evaluates to
// request_input. It implements the exact builder the real policy worker
// consumes so the routing path under test is the production handler.
type inputRequestEvaluationBuilder struct {
	snapshot    policy.Snapshot
	evaluation  policy.Evaluation
	evaluations int
}

func (builder *inputRequestEvaluationBuilder) Build(context.Context, tenant.Scope, policy.AuthorRequest) (policy.Decision, error) {
	return policy.Decision{}, errors.New("terminal authoring is not part of this fixture")
}

func (builder *inputRequestEvaluationBuilder) Evaluate(context.Context, tenant.Scope, policy.AuthorRequest) (policy.Snapshot, policy.Evaluation, error) {
	builder.evaluations++
	return builder.snapshot, builder.evaluation, nil
}

// TestVerificationInputRequestProjectionResumeAndCompletion composes the whole
// request_input orchestration: the real policy worker routes the transition,
// records the bounded request, the tenant read projects it, the subject outcome
// reports action_required, resume returns the session to collecting, and the
// normal decision path still completes the journey.
func TestVerificationInputRequestProjectionResumeAndCompletion(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		base := prepareCompletion(t, f)
		now := f.now
		source := fixedIntegrationClock{now: now}

		// The routing fixture is derived from the committed terminal evaluation
		// and pinned to the real session authority and subject response.
		previous := base.Snapshot()
		var responseValue string
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT id FROM idenqa.subject_responses WHERE tenant_id=$1 AND authority_id=$2 ORDER BY recorded_at DESC,id DESC LIMIT 1`, f.scope.ID().String(), f.declaration.ID().String()).Scan(&responseValue); err != nil {
			t.Fatal(err)
		}
		responseID, err := id.ParseAcknowledgement(responseValue)
		if err != nil {
			t.Fatal(err)
		}
		snapshot, err := policy.NewSnapshot(policy.SnapshotInput{TenantID: f.scope.ID(), VerificationID: previous.VerificationID(), AuthorityID: previous.AuthorityID(), AcknowledgementID: responseID, Region: previous.Region(), Policy: previous.Policy(), Evaluator: previous.Evaluator(), EvaluatedAt: previous.EvaluatedAt(), Facts: previous.Facts()})
		if err != nil {
			t.Fatal(err)
		}
		results := base.Evaluation().Results()
		overLong := "r" + strings.Repeat("a", 66)
		reasons := []string{overLong}
		for index := 0; index < 10; index++ {
			reasons = append(reasons, fmt.Sprintf("reason_%02d", index))
		}
		for index := range results {
			results[index].State = policy.RequirementUnavailable
			results[index].Candidate = policy.DirectiveRequestInput
			results[index].ReasonCodes = reasons
		}
		evaluation, err := policy.Resolve(snapshot, results, "")
		if err != nil {
			t.Fatal(err)
		}
		request := policy.AuthorRequest{DecisionID: base.ID(), VerificationID: snapshot.VerificationID(), EvaluatedAt: snapshot.EvaluatedAt(), DecidedAt: base.DecidedAt()}
		if _, err := policy.NewRouting(f.scope, request, snapshot, evaluation); err != nil {
			t.Fatal(err)
		}

		decisions, err := policypostgres.NewGuarded(f.runtime, integrationProtector{}, source)
		if err != nil {
			t.Fatal(err)
		}
		completion, err := verificationpostgres.NewCompletionStore(f.runtime, integrationProtector{}, f.ids, source)
		if err != nil {
			t.Fatal(err)
		}
		routingStore, err := reviewpostgres.NewRoutingStore(f.runtime, integrationProtector{}, f.ids, source, nil)
		if err != nil {
			t.Fatal(err)
		}
		builder := &inputRequestEvaluationBuilder{snapshot: snapshot, evaluation: evaluation}
		worker, err := policytask.NewHandlerWithRouting(decisions, builder, completion, routingStore)
		if err != nil {
			t.Fatal(err)
		}
		actor, err := f.ids.NewTask()
		if err != nil {
			t.Fatal(err)
		}
		intent, err := policytask.NewAuthorIntent(f.ids, f.scope, request, policytask.IntentMetadata{ScheduledAt: now, Deadline: now.Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		runWorker := func() error {
			work, result := worker.Prepare(t.Context(), task.Delivery{Intent: intent, Attempt: 1, Fence: 1})
			if result.Outcome != task.OutcomeComplete || work == nil {
				return fmt.Errorf("policy worker prepare outcome=%v error=%w", result.Outcome, result.Err)
			}
			return f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
				if outcome := work(ctx, tx); outcome.Outcome != task.OutcomeComplete {
					return fmt.Errorf("policy worker effect outcome=%v error=%w", outcome.Outcome, outcome.Err)
				}
				return nil
			})
		}
		if err := runWorker(); err != nil {
			t.Fatal(err)
		}
		assertWorkflowRouting(t, f, "awaiting_input")

		inputStore, err := verificationpostgres.NewInputRequestStore(f.runtime)
		if err != nil {
			t.Fatal(err)
		}
		wantReasons := []string{"reason_00", "reason_01", "reason_02", "reason_03", "reason_04", "reason_05", "reason_06", "reason_07"}
		current, err := inputStore.FindCurrent(t.Context(), f.scope, f.creation.Session.ID())
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(current.ReasonCodes(), wantReasons) || !current.CaseID().IsZero() ||
			current.ActorID() != intent.ID().String() || !current.RequestedAt().Equal(now) ||
			current.VerificationID().String() != f.creation.Session.ID().String() {
			t.Fatalf("recorded input request = %+v", current)
		}
		var rows, active int
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*), count(*) FILTER (WHERE superseded_at IS NULL) FROM idenqa.verification_input_requests WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&rows, &active); err != nil {
			t.Fatal(err)
		}
		if rows != 1 || active != 1 {
			t.Fatalf("input request rows=%d active=%d", rows, active)
		}
		otherTenant, otherVerification := seedExecutionVerification(t, f.admin, f.ids, now)
		otherScope, err := tenant.NewScope(otherTenant)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inputStore.FindCurrent(t.Context(), otherScope, otherVerification); !errors.Is(err, verification.ErrInputRequestNotFound) {
			t.Fatalf("cross-tenant input request read = %v", err)
		}

		// Exact replay must neither re-evaluate nor append a second request.
		if err := runWorker(); err != nil {
			t.Fatalf("routing replay: %v", err)
		}
		if builder.evaluations != 1 {
			t.Fatalf("routing replay re-evaluated %d times", builder.evaluations)
		}
		assertWorkflowRouting(t, f, "awaiting_input")
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*), count(*) FILTER (WHERE superseded_at IS NULL) FROM idenqa.verification_input_requests WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&rows, &active); err != nil {
			t.Fatal(err)
		}
		if rows != 1 || active != 1 {
			t.Fatalf("replay changed input requests rows=%d active=%d", rows, active)
		}

		// Drive the real tenant, capture and subject-safe outcome routes.
		router, presentedKey, signedCapture, signedOutcome := f.inputRequestRoutes(t, source)
		tenantResponse := serveVerificationRoute(t, router, http.MethodGet, "/v1/verifications/"+f.creation.Session.ID().String(), presentedKey, "", nil)
		if tenantResponse.Code != http.StatusOK {
			t.Fatalf("tenant read status = %d body=%s", tenantResponse.Code, tenantResponse.Body)
		}
		var sessionView openapiv1.VerificationSession
		if err := json.Unmarshal(tenantResponse.Body.Bytes(), &sessionView); err != nil {
			t.Fatal(err)
		}
		if sessionView.State != openapiv1.VerificationSessionStateAwaitingInput || sessionView.RequestedInput == nil ||
			!slices.Equal(sessionView.RequestedInput.ReasonCodes, wantReasons) ||
			!sessionView.RequestedInput.RequestedAt.Equal(now) || sessionView.RequestedInput.CaseID != nil {
			t.Fatalf("tenant requested_input = %+v state=%s", sessionView.RequestedInput, sessionView.State)
		}
		if captureResponse := serveVerificationRoute(t, router, http.MethodGet, "/v1/capture/session", signedCapture, "", nil); captureResponse.Code != http.StatusUnauthorized {
			t.Fatalf("capture read while awaiting input status = %d body=%s", captureResponse.Code, captureResponse.Body)
		}
		outcomeResponse := serveVerificationRoute(t, router, http.MethodGet, "/v1/capture/outcome", signedOutcome, "", nil)
		if outcomeResponse.Code != http.StatusOK {
			t.Fatalf("outcome read status = %d body=%s", outcomeResponse.Code, outcomeResponse.Body)
		}
		var outcome openapiv1.CaptureOutcome
		if err := json.Unmarshal(outcomeResponse.Body.Bytes(), &outcome); err != nil {
			t.Fatal(err)
		}
		if outcome.State != openapiv1.CaptureOutcomeStateActionRequired || outcome.VerificationID != f.creation.Session.ID().String() {
			t.Fatalf("capture outcome = %+v", outcome)
		}

		// A newer request supersedes the previous one. The general routing path
		// currently reuses the immutable session decision identifier, so the
		// replace rule is proven through the same persistence primitive it uses.
		newerID, err := f.ids.NewInputRequest()
		if err != nil {
			t.Fatal(err)
		}
		newer, err := verification.NewInputRequest(newerID, f.creation.Session.ID(), id.ReviewCase{}, []string{"newer_reason"}, actor.String(), now.Add(time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			return inputStore.RecordWithin(ctx, f.scope, tx, newer)
		}); err != nil {
			t.Fatal(err)
		}
		activeRequest, err := inputStore.FindCurrent(t.Context(), f.scope, f.creation.Session.ID())
		if err != nil {
			t.Fatal(err)
		}
		if activeRequest.ID() != newerID || !slices.Equal(activeRequest.ReasonCodes(), []string{"newer_reason"}) {
			t.Fatalf("active request after supersede = %+v", activeRequest)
		}
		var superseded int
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.verification_input_requests WHERE tenant_id=$1 AND id=$2 AND superseded_at=$3`, f.scope.ID().String(), current.ID().String(), now.Add(time.Second)).Scan(&superseded); err != nil {
			t.Fatal(err)
		}
		if superseded != 1 {
			t.Fatalf("previous request was not superseded: %d", superseded)
		}
		if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.verification_input_requests SET reason_codes=ARRAY['tampered'] WHERE tenant_id=$1`, f.scope.ID().String()); err == nil {
			t.Fatal("input request reason codes were mutable")
		}
		if _, err := f.admin.Native().Exec(t.Context(), `DELETE FROM idenqa.verification_input_requests WHERE tenant_id=$1`, f.scope.ID().String()); err == nil {
			t.Fatal("input request row was deletable")
		}

		// Fresh subject authorisation then the real resume command. The original
		// credential is revoked so resume replaces it and rebinds accepted
		// uploads to the replacement token for the later processing validation.
		responseID, err = f.ids.NewAcknowledgement()
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := authority.NewResponse(authority.ResponseRecord{
			ID: responseID, TenantID: f.scope.ID(), AuthorityID: f.declaration.ID(), NoticeID: f.declaration.NoticeID(),
			SubjectID: f.declaration.SubjectID(), VerificationID: f.creation.Session.ID(), CaptureTokenID: f.creation.Credential.ID(),
			Action: authority.ResponseConsent, Locale: "en-NG", RenderedExperienceVersion: "capture.resume.v1", RecordedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		freshRequest, err := idempotency.NewRequest(f.scope.ID(), f.creation.Credential.ID(), "authorities.respond", "input-request-fresh-consent", []byte(`{"action":"consent"}`), now, 24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.authorities.AppendResponse(t.Context(), f.scope, authority.ResponseMutation{Response: fresh, EventID: mustCaptureEvent(t, f.ids), Idempotency: freshRequest}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.capture_tokens SET revoked_at=$3 WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.creation.Credential.ID().String(), now); err != nil {
			t.Fatal(err)
		}
		resumeBody, err := json.Marshal(openapiv1.VerificationResume{ExpectedVersion: 3})
		if err != nil {
			t.Fatal(err)
		}
		resumedResponse := serveVerificationRoute(t, router, http.MethodPost, "/v1/verifications/"+f.creation.Session.ID().String()+"/resume", presentedKey, "input-request-resume", resumeBody)
		if resumedResponse.Code != http.StatusOK {
			t.Fatalf("resume status = %d body=%s", resumedResponse.Code, resumedResponse.Body)
		}
		var resumed openapiv1.VerificationResumed
		if err := json.Unmarshal(resumedResponse.Body.Bytes(), &resumed); err != nil {
			t.Fatal(err)
		}
		if resumed.Session.State != openapiv1.VerificationSessionStateCollecting || resumed.Session.Version != 4 ||
			resumed.Replayed || !resumed.Replaced || resumed.CaptureToken == nil {
			t.Fatalf("resumed session = %+v", resumed)
		}
		replacementID, err := id.ParseCaptureToken(resumed.CaptureTokenID)
		if err != nil {
			t.Fatal(err)
		}
		responseID, err = f.ids.NewAcknowledgement()
		if err != nil {
			t.Fatal(err)
		}
		replacementConsent, err := authority.NewResponse(authority.ResponseRecord{
			ID: responseID, TenantID: f.scope.ID(), AuthorityID: f.declaration.ID(), NoticeID: f.declaration.NoticeID(),
			SubjectID: f.declaration.SubjectID(), VerificationID: f.creation.Session.ID(), CaptureTokenID: replacementID,
			Action: authority.ResponseConsent, Locale: "en-NG", RenderedExperienceVersion: "capture.resume.v1", RecordedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		replacementRequest, err := idempotency.NewRequest(f.scope.ID(), replacementID, "authorities.respond", "input-request-replacement-consent", []byte(`{"action":"consent"}`), now, 24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.authorities.AppendResponse(t.Context(), f.scope, authority.ResponseMutation{Response: replacementConsent, EventID: mustCaptureEvent(t, f.ids), Idempotency: replacementRequest}); err != nil {
			t.Fatal(err)
		}
		replayedResponse := serveVerificationRoute(t, router, http.MethodPost, "/v1/verifications/"+f.creation.Session.ID().String()+"/resume", presentedKey, "input-request-resume", resumeBody)
		if replayedResponse.Code != http.StatusOK {
			t.Fatalf("resume replay status = %d body=%s", replayedResponse.Code, replayedResponse.Body)
		}
		var replayed openapiv1.VerificationResumed
		if err := json.Unmarshal(replayedResponse.Body.Bytes(), &replayed); err != nil {
			t.Fatal(err)
		}
		if !replayed.Replayed || replayed.Session.State != openapiv1.VerificationSessionStateCollecting || replayed.Session.Version != 4 {
			t.Fatalf("resume replay = %+v", replayed)
		}
		resumedRead := serveVerificationRoute(t, router, http.MethodGet, "/v1/verifications/"+f.creation.Session.ID().String(), presentedKey, "", nil)
		if resumedRead.Code != http.StatusOK || bytes.Contains(resumedRead.Body.Bytes(), []byte("requested_input")) {
			t.Fatalf("collecting read projected requested_input: %d %s", resumedRead.Code, resumedRead.Body)
		}
		// A replacement capture credential is usable again once the session is
		// collecting, and the subject-safe read still omits the persisted request.
		resumedCapture := serveVerificationRoute(t, router, http.MethodGet, "/v1/capture/session", string(*resumed.CaptureToken), "", nil)
		if resumedCapture.Code != http.StatusOK {
			t.Fatalf("capture read status = %d body=%s", resumedCapture.Code, resumedCapture.Body)
		}
		if bytes.Contains(resumedCapture.Body.Bytes(), []byte("requested_input")) {
			t.Fatalf("capture read leaked requested_input: %s", resumedCapture.Body)
		}

		// Resume returns the workflow to processing so the normal decision path
		// can complete it; the plan already exists and its checks are terminal.
		lifecycle, err := verificationpostgres.NewLifecycleStore(f.runtime, integrationProtector{}, source)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lifecycle.Apply(t.Context(), f.scope, verification.LifecycleCommand{EventID: mustCaptureEvent(t, f.ids), VerificationID: f.creation.Session.ID(), ExpectedVersion: 4, Target: verification.SessionStateProcessing, ActorID: actor.String(), OccurredAt: now}); err != nil {
			t.Fatal(err)
		}
		if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			if err := decisions.AppendWithin(ctx, f.scope, tx, base); err != nil {
				return err
			}
			return completion.CompleteWithin(ctx, f.scope, tx, base, actor)
		}); err != nil {
			t.Fatal(err)
		}
		var state string
		var completedDecision *string
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT state,completed_decision_id FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.creation.Session.ID().String()).Scan(&state, &completedDecision); err != nil {
			t.Fatal(err)
		}
		if state != "completed" || completedDecision == nil || *completedDecision != base.ID().String() {
			t.Fatalf("completion state=%s decision=%v", state, completedDecision)
		}
		completedRead := serveVerificationRoute(t, router, http.MethodGet, "/v1/verifications/"+f.creation.Session.ID().String(), presentedKey, "", nil)
		if completedRead.Code != http.StatusOK || bytes.Contains(completedRead.Body.Bytes(), []byte("requested_input")) {
			t.Fatalf("completed read projected requested_input: %d %s", completedRead.Code, completedRead.Body)
		}
	})
}

// inputRequestRoutes constructs the real tenant, capture and outcome routes with
// real credentials over the fixture session store.
func (f captureAcceptanceFixture) inputRequestRoutes(t *testing.T, source fixedIntegrationClock) (http.Handler, string, string, string) {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	sessions, err := verificationpostgres.NewSessionStore(f.runtime, integrationProtector{}, f.catalog)
	if err != nil {
		t.Fatal(err)
	}
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x5c}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	accessStore, err := accesspostgres.New(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	key, presented := newIntegrationCredentialKey(t, f.ids, f.scope.ID(), f.now, peppers,
		access.Pattern("verification_sessions:read"), access.Pattern("verification_sessions:resume"))
	if err := accessStore.Create(t.Context(), f.scope, key); err != nil {
		t.Fatal(err)
	}
	authenticator, err := access.NewAuthenticator(accessStore, peppers, source)
	if err != nil {
		t.Fatal(err)
	}
	accessMiddleware, err := httpapi.NewAccessMiddleware(authenticator, logger)
	if err != nil {
		t.Fatal(err)
	}
	captureKeyring, err := access.NewCaptureTokenKeyring(1, map[access.CaptureTokenKeyVersion][]byte{1: bytes.Repeat([]byte{0x6d}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	captureSigner, err := access.NewCaptureTokenSigner(captureKeyring, source)
	if err != nil {
		t.Fatal(err)
	}
	outcomeKeyring, err := access.NewOutcomeTokenKeyring(1, map[access.OutcomeTokenKeyVersion][]byte{1: bytes.Repeat([]byte{0x6e}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	outcomeSigner, err := access.NewOutcomeTokenSigner(outcomeKeyring, source)
	if err != nil {
		t.Fatal(err)
	}
	captureAuthenticator, err := verification.NewCaptureAuthenticator(sessions, captureSigner, source)
	if err != nil {
		t.Fatal(err)
	}
	captureMiddleware, err := httpapi.NewCaptureAccessMiddleware(captureAuthenticator, logger)
	if err != nil {
		t.Fatal(err)
	}
	service, err := verification.NewSessionService(sessions, f.ids, captureSigner, outcomeSigner, source, verification.SessionLifetimes{
		VerificationDefault: 24 * time.Hour, VerificationMaximum: 48 * time.Hour,
		CaptureTokenDefault: 30 * time.Minute, CaptureTokenMaximum: time.Hour,
		OutcomePostDefault: 24 * time.Hour, OutcomePostMaximum: 48 * time.Hour,
		IdempotencyRetention: 24 * time.Hour,
	}, "local")
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpapi.NewVerificationRoutes(accessMiddleware, captureMiddleware, service, f.catalog, nil, nil, logger)
	if err != nil {
		t.Fatal(err)
	}
	outcomeAuthenticator, err := verification.NewOutcomeAuthenticator(sessions, outcomeSigner, source)
	if err != nil {
		t.Fatal(err)
	}
	outcomeMiddleware, err := httpapi.NewOutcomeAccessMiddleware(outcomeAuthenticator, logger)
	if err != nil {
		t.Fatal(err)
	}
	outcomeService, err := verification.NewCaptureOutcomeService(sessions)
	if err != nil {
		t.Fatal(err)
	}
	outcomeRoutes, err := httpapi.NewCaptureOutcomeRoutes(outcomeMiddleware, outcomeService, logger)
	if err != nil {
		t.Fatal(err)
	}
	signedCapture, err := captureSigner.Sign(f.creation.Credential)
	if err != nil {
		t.Fatal(err)
	}
	signedOutcome, err := outcomeSigner.Sign(f.creation.OutcomeCredential)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Route(httpapi.VersionPrefix, func(versioned chi.Router) {
		routes.Register(versioned)
		outcomeRoutes.Register(versioned)
	})
	return router, presented.Reveal(), signedCapture.Reveal(), signedOutcome.Reveal()
}

func serveVerificationRoute(t *testing.T, router http.Handler, method, path, bearer, idempotencyKey string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request := httptest.NewRequestWithContext(t.Context(), method, path, reader)
	request.Header.Set("Authorization", "Bearer "+bearer)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", fmt.Sprintf("%q", idempotencyKey))
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
