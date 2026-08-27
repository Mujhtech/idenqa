package verification

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
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
		repository.mutation.CaptureKeyVersion != 1 || repository.mutation.Region != "local" {
		t.Fatalf("mutation = %+v, calls = %d", repository.mutation, repository.createCalls)
	}
	if got := repository.mutation.SessionExpiresAt.Sub(repository.mutation.CreatedAt); got != 24*time.Hour {
		t.Fatalf("verification lifetime = %v", got)
	}
	if got := repository.mutation.CaptureTokenExpiry.Sub(repository.mutation.CreatedAt); got != 30*time.Minute {
		t.Fatalf("capture-token lifetime = %v", got)
	}
	if created.CaptureToken.IsZero() || created.CaptureToken.String() != "[REDACTED]" {
		t.Fatal("service did not return a redacting display-once token")
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

	return SessionCreation{Credential: credential}, err
}

func (*sessionServiceRepositoryStub) FindSession(context.Context, tenant.Scope, id.Verification) (Session, error) {
	return Session{}, ErrSessionNotFound
}

type sessionServiceIDGenerator struct {
	verification id.Verification
	token        id.CaptureToken
	event        id.Event
	decision     id.Decision
}

func (generator sessionServiceIDGenerator) NewVerification() (id.Verification, error) {
	return generator.verification, nil
}

func (generator sessionServiceIDGenerator) NewCaptureToken() (id.CaptureToken, error) {
	return generator.token, nil
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
	repository := &sessionServiceRepositoryStub{}
	service, err := NewSessionService(
		repository,
		sessionServiceIDGenerator{verification: verificationID, token: tokenID, event: eventID, decision: decisionID},
		signer,
		profileServiceClock{now: now},
		SessionLifetimes{
			VerificationDefault:  24 * time.Hour,
			VerificationMaximum:  7 * 24 * time.Hour,
			CaptureTokenDefault:  30 * time.Minute,
			CaptureTokenMaximum:  2 * time.Hour,
			IdempotencyRetention: 24 * time.Hour,
		},
		"local",
	)
	if err != nil {
		t.Fatalf("NewSessionService() error = %v", err)
	}

	return service, repository
}
