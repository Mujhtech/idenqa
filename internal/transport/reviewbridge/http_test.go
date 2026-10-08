package reviewbridge

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/reviewdelegation"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type executorStub struct {
	called     bool
	operation  string
	resolution review.Resolution
	grantIDs   []id.Grant
	claims     reviewdelegation.Claims
}

func (stub *executorStub) Claim(ctx context.Context, _ tenant.Scope, actor review.Actor, _ id.ReviewCase, version int64) (review.Case, error) {
	stub.called = actor.ID == "usr_1" && version == 7
	stub.operation = "claim"
	stub.claims, _ = reviewdelegation.ClaimsFromContext(ctx)
	return review.Case{State: review.CaseClaimed}, nil
}

func (stub *executorStub) SubmitFinding(ctx context.Context, _ tenant.Scope, actor review.Actor, _ id.ReviewCase, resolution review.Resolution, _ string, grantIDs []id.Grant, version int64) (review.Case, error) {
	stub.called = actor.ID == "usr_1" && version == 7
	stub.operation = "submit_finding"
	stub.resolution = resolution
	stub.grantIDs = append([]id.Grant(nil), grantIDs...)
	stub.claims, _ = reviewdelegation.ClaimsFromContext(ctx)
	return review.Case{State: review.CaseResolved}, nil
}

type observerStub struct{}

func (observerStub) ObserveReviewCommand(context.Context, tenant.Scope, reviewdelegation.Claims) (review.Case, error) {
	return review.Case{}, reviewdelegation.ErrNotFound
}

func TestHandlerExecutesOnlyExactSignedIntent(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	handler, executor, private := handlerFixture(t, now)
	payload := commandFixture(t, private, now)

	recorder := request(t, handler, payload)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !executor.called || executor.claims.CommandID != payload.ID {
		t.Fatalf("executor called=%t claims=%#v", executor.called, executor.claims)
	}
}

// This test exists because the outer command is routing input controlled by
// Cloud storage; it must not be able to widen or redirect the signed intent.
func TestHandlerRejectsOuterCommandTampering(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	handler, executor, private := handlerFixture(t, now)
	payload := commandFixture(t, private, now)
	payload.ActorID = "usr_attacker"

	recorder := request(t, handler, payload)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if executor.called {
		t.Fatal("executor was called for a tampered command")
	}
}

func TestHandlerExecutesSignedFindingIntent(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	handler, executor, private := handlerFixture(t, now)
	payload := commandFixture(t, private, now)
	payload.Operation = "submit_finding"
	payload.Permission = "review:find"
	payload.Purpose = "review-finding"
	payload.ReasonCode = "document_consistent"
	payload.Resolution = "satisfy"
	payload.EvidenceGrantIDs = []string{"grt_01M3NRK3Z6BA1MMMR66QMM1NRN"}
	payload.Authority.Claims.Operation = payload.Operation
	payload.Authority.Claims.Permission = payload.Permission
	payload.Authority.Claims.Purpose = payload.Purpose
	payload.Authority.Claims.ReasonCode = payload.ReasonCode
	payload.Authority.Claims.Resolution = payload.Resolution
	payload.Authority.Claims.EvidenceGrantIDs = append([]string(nil), payload.EvidenceGrantIDs...)
	digest := sha256.Sum256([]byte("submit_finding\x00" + payload.TargetID + "\x007\x00satisfy\x00document_consistent\x00" + payload.EvidenceGrantIDs[0]))
	payload.RequestDigest = hex.EncodeToString(digest[:])
	payload.Authority.Claims.RequestDigest = payload.RequestDigest
	signCommand(t, &payload, private)

	recorder := request(t, handler, payload)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if !executor.called || executor.operation != "submit_finding" || executor.resolution != review.ResolutionSatisfy || len(executor.grantIDs) != 1 {
		t.Fatalf("executor = %#v", executor)
	}
}

func TestHandlerRejectsFindingGrantTampering(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	handler, executor, private := handlerFixture(t, now)
	payload := commandFixture(t, private, now)
	payload.EvidenceGrantIDs = []string{"grt_01M3NRK3Z6BA1MMMR66QMM1NRN"}

	recorder := request(t, handler, payload)
	if recorder.Code != http.StatusForbidden || executor.called {
		t.Fatalf("status = %d called=%t body=%s", recorder.Code, executor.called, recorder.Body.String())
	}
}

func handlerFixture(t *testing.T, now time.Time) (*Handler, *executorStub, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	verifier, err := reviewdelegation.NewVerifier(map[string]string{"cloud_key_1": base64.StdEncoding.EncodeToString(public)}, reviewdelegation.Binding{
		OrganisationID: "org_1", EnvironmentID: "env_1", DeploymentID: "dep_1", Region: "eu-west-1",
	}, time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	executor := &executorStub{}
	handler, err := NewHandler(executor, observerStub{}, verifier)
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	return handler, executor, private
}

func commandFixture(t *testing.T, private ed25519.PrivateKey, now time.Time) command {
	t.Helper()
	target := "rvc_01M3NRK3Z6BA1MMMR66QMM1NRN"
	digest := sha256.Sum256([]byte("claim\x00" + target + "\x007"))
	claims := reviewdelegation.Claims{Version: reviewdelegation.Version, Audience: reviewdelegation.Audience, Intent: reviewdelegation.Intent{
		CommandID: "rcm_1", Scope: reviewdelegation.Scope{OrganisationID: "org_1", TenantID: "ten_01M3NRK3Z6BA1MMMR66QMM1NRN", EnvironmentID: "env_1", DeploymentID: "dep_1", Region: "eu-west-1"},
		ActorID: "usr_1", Permission: "review:claim", Purpose: "review-case-assignment", TargetKind: "review_case", TargetID: target,
		Operation: "claim", ExpectedVersion: 7, ReasonCode: "self-assignment", RequestDigest: hex.EncodeToString(digest[:]), IssuedAt: now, ExpiresAt: now.Add(30 * time.Second),
	}}
	encoded, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	signed := sha256.Sum256(encoded)
	return command{
		ID: claims.CommandID, Scope: claims.Scope, ActorID: claims.ActorID, Permission: claims.Permission, Purpose: claims.Purpose,
		TargetKind: claims.TargetKind, TargetID: claims.TargetID, Operation: claims.Operation, ExpectedVersion: claims.ExpectedVersion,
		ReasonCode: claims.ReasonCode, RequestDigest: claims.RequestDigest, State: "applying", Attempt: 1,
		Authority: reviewdelegation.Envelope{KeyID: "cloud_key_1", Algorithm: "Ed25519", Claims: claims, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, signed[:]))},
		CreatedAt: json.RawMessage(`"2026-09-29T09:59:00Z"`), UpdatedAt: json.RawMessage(`"2026-09-29T10:00:00Z"`),
	}
}

func signCommand(t *testing.T, payload *command, private ed25519.PrivateKey) {
	t.Helper()
	encoded, err := json.Marshal(payload.Authority.Claims)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	digest := sha256.Sum256(encoded)
	payload.Authority.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, digest[:]))
}

func request(t *testing.T, handler http.Handler, payload command) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/local/v1/review-commands/execute", bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}
