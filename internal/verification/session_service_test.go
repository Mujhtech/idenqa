package verification

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestSessionServiceCreateRequiresPermission(t *testing.T) {
	t.Parallel()

	service, repository := newSessionServiceFixture(t)
	profileID := mustProfileID(t)
	if _, err := service.Create(context.Background(), access.Context{}, "attempt-1", SessionCreateInput{ProfileID: profileID}); !errors.Is(err, access.ErrInsufficientScope) {
		t.Fatalf("Create() error = %v, want ErrInsufficientScope", err)
	}
	if repository.createCalls != 0 {
		t.Fatal("unauthorised create reached persistence")
	}
}

func TestSessionServiceCreateBuildsBoundedMutationAndSignsReplay(t *testing.T) {
	t.Parallel()

	service, repository := newSessionServiceFixture(t)
	authority := newProfileServiceAuthority(t, access.Pattern("verification_sessions:create"))
	profileID := mustProfileID(t)
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	created, err := service.Create(
		context.Background(),
		authority,
		"attempt-1",
		SessionCreateInput{ProfileID: profileID, PolicyID: policyID},
	)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if repository.createCalls != 1 ||
		repository.mutation.Idempotency.Operation() != OperationCreateVerification ||
		repository.mutation.Idempotency.Key() != "attempt-1" ||
		repository.mutation.CaptureKeyVersion != 1 || repository.mutation.OutcomeKeyVersion != 1 ||
		repository.mutation.Region != "local" {
		t.Fatalf("mutation = %+v, calls = %d", repository.mutation, repository.createCalls)
	}
	if got := repository.mutation.SessionExpiresAt.Sub(repository.mutation.CreatedAt); got != 24*time.Hour {
		t.Fatalf("verification lifetime = %v", got)
	}
	if got := repository.mutation.CaptureTokenExpiry.Sub(repository.mutation.CreatedAt); got != 30*time.Minute {
		t.Fatalf("capture-token lifetime = %v", got)
	}
	if got := repository.mutation.OutcomeTokenExpiry.Sub(repository.mutation.SessionExpiresAt); got != 24*time.Hour {
		t.Fatalf("outcome-token post-expiry lifetime = %v", got)
	}
	if created.CaptureToken.IsZero() || created.CaptureToken.String() != "[REDACTED]" ||
		created.OutcomeToken.IsZero() || created.OutcomeToken.String() != "[REDACTED]" {
		t.Fatal("service did not return redacting display-once tokens")
	}
}

func TestSessionServiceRejectsTenantLifetimeOutsideDeploymentBounds(t *testing.T) {
	t.Parallel()

	service, repository := newSessionServiceFixture(t)
	authority := newProfileServiceAuthority(t, access.Pattern("verification_sessions:create"))
	profileID := mustProfileID(t)
	tooLong := 8 * 24 * time.Hour
	if _, err := service.Create(context.Background(), authority, "attempt-1", SessionCreateInput{
		ProfileID:       profileID,
		VerificationTTL: &tooLong,
	}); err == nil {
		t.Fatal("Create() accepted a verification lifetime above the deployment maximum")
	}
	if repository.createCalls != 0 {
		t.Fatal("invalid lifetime reached persistence")
	}
}

type sessionServiceRepositoryStub struct {
	mutation    SessionCreateMutation
	createCalls int
}

func (repository *sessionServiceRepositoryStub) Create(
	_ context.Context,
	_ tenant.Scope,
	mutation SessionCreateMutation,
) (SessionCreation, error) {
	repository.createCalls++
	repository.mutation = mutation
	credential, err := access.NewCaptureCredential(
		mutation.CaptureTokenID,
		mutation.Idempotency.TenantID(),
		mutation.SessionID,
		mutation.CaptureKeyVersion,
		mutation.CreatedAt,
		mutation.CaptureTokenExpiry,
	)
	if err != nil {
		return SessionCreation{}, err
	}
	outcomeCredential, err := access.NewOutcomeCredential(
		mutation.OutcomeTokenID,
		mutation.Idempotency.TenantID(),
		mutation.SessionID,
		mutation.OutcomeKeyVersion,
		mutation.CreatedAt,
		mutation.OutcomeTokenExpiry,
	)

	return SessionCreation{Credential: credential, OutcomeCredential: outcomeCredential}, err
}

func (*sessionServiceRepositoryStub) FindSession(context.Context, tenant.Scope, id.Verification) (Session, error) {
	return Session{}, ErrSessionNotFound
}

func (*sessionServiceRepositoryStub) Resume(context.Context, tenant.Scope, ResumeMutation) (ResumeResult, error) {
	return ResumeResult{}, ErrSessionConflict
}

type sessionServiceIDGenerator struct {
	verification id.Verification
	token        id.CaptureToken
	outcomeToken id.OutcomeToken
	event        id.Event
	decision     id.Decision
}

func (generator sessionServiceIDGenerator) NewVerification() (id.Verification, error) {
	return generator.verification, nil
}

func (generator sessionServiceIDGenerator) NewCaptureToken() (id.CaptureToken, error) {
	return generator.token, nil
}

func (generator sessionServiceIDGenerator) NewOutcomeToken() (id.OutcomeToken, error) {
	return generator.outcomeToken, nil
}

func (generator sessionServiceIDGenerator) NewEvent() (id.Event, error) { return generator.event, nil }

func (generator sessionServiceIDGenerator) NewDecision() (id.Decision, error) {
	return generator.decision, nil
}

func newSessionServiceFixture(t *testing.T) (*SessionService, *sessionServiceRepositoryStub) {
	t.Helper()

	now := time.Date(2026, time.August, 27, 20, 0, 0, 0, time.UTC)
	identifiers, err := id.NewGenerator(profileServiceClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{3}, 128)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	verificationID, err := identifiers.NewVerification()
	if err != nil {
		t.Fatalf("NewVerification() error = %v", err)
	}
	tokenID, err := identifiers.NewCaptureToken()
	if err != nil {
		t.Fatalf("NewCaptureToken() error = %v", err)
	}
	outcomeTokenID, err := identifiers.NewOutcomeToken()
	if err != nil {
		t.Fatalf("NewOutcomeToken() error = %v", err)
	}
	eventID, err := identifiers.NewEvent()
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	decisionID, err := identifiers.NewDecision()
	if err != nil {
		t.Fatalf("NewDecision() error = %v", err)
	}
	keyring, err := access.NewCaptureTokenKeyring(1, map[access.CaptureTokenKeyVersion][]byte{
		1: bytes.Repeat([]byte{5}, 32),
	})
	if err != nil {
		t.Fatalf("NewCaptureTokenKeyring() error = %v", err)
	}
	signer, err := access.NewCaptureTokenSigner(keyring, profileServiceClock{now: now})
	if err != nil {
		t.Fatalf("NewCaptureTokenSigner() error = %v", err)
	}
	outcomeKeyring, err := access.NewOutcomeTokenKeyring(1, map[access.OutcomeTokenKeyVersion][]byte{
		1: bytes.Repeat([]byte{6}, 32),
	})
	if err != nil {
		t.Fatalf("NewOutcomeTokenKeyring() error = %v", err)
	}
	outcomeSigner, err := access.NewOutcomeTokenSigner(outcomeKeyring, profileServiceClock{now: now})
	if err != nil {
		t.Fatalf("NewOutcomeTokenSigner() error = %v", err)
	}
	repository := &sessionServiceRepositoryStub{}
	service, err := NewSessionService(
		repository,
		sessionServiceIDGenerator{
			verification: verificationID, token: tokenID, outcomeToken: outcomeTokenID,
			event: eventID, decision: decisionID,
		},
		signer,
		outcomeSigner,
		profileServiceClock{now: now},
		SessionLifetimes{
			VerificationDefault:  24 * time.Hour,
			VerificationMaximum:  7 * 24 * time.Hour,
			CaptureTokenDefault:  30 * time.Minute,
			CaptureTokenMaximum:  2 * time.Hour,
			OutcomePostDefault:   24 * time.Hour,
			OutcomePostMaximum:   7 * 24 * time.Hour,
			IdempotencyRetention: 24 * time.Hour,
		},
		"local",
	)
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	return service, repository
}

type pinningStub struct {
	called       bool
	verification id.Verification
	request      experience.ResolutionRequest
}

func (stub *pinningStub) PinForSession(
	_ context.Context,
	_ tenant.Scope,
	verificationID id.Verification,
	request experience.ResolutionRequest,
) (experience.Pin, error) {
	stub.called, stub.verification, stub.request = true, verificationID, request
	return experience.Pin{}, nil
}

func TestSessionServicePinsExperienceAtCreation(t *testing.T) {
	t.Parallel()

	service, _ := newSessionServiceFixture(t)
	pinner := &pinningStub{}
	service.WithExperience(pinner)
	authority := newProfileServiceAuthority(t, access.Pattern("verification_sessions:create"))
	profileID := mustProfileID(t)
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	created, err := service.Create(context.Background(), authority, "attempt-1", SessionCreateInput{
		ProfileID: profileID, PolicyID: policyID, Locale: "fr",
		Experience: &experience.ResolutionRequest{Workflow: "capture.identity", Country: "NG"},
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !pinner.called || pinner.verification.String() != created.Session.ID().String() {
		t.Fatalf("pin = %+v, session = %s", pinner, created.Session.ID())
	}
	if pinner.request.Workflow != "capture.identity" || pinner.request.Country != "NG" || pinner.request.Locale != "fr" {
		t.Fatalf("resolution request = %+v", pinner.request)
	}
}
