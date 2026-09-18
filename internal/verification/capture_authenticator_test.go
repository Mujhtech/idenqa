package verification

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestCaptureAuthenticatorBuildsContextFromSignedAndDurableState(t *testing.T) {
	t.Parallel()

	authenticator, token, record := newCaptureAuthenticatorFixture(t)
	captureContext, err := authenticator.Authenticate(context.Background(), token.Reveal())
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if captureContext.Session().ID().String() != record.Session.ID().String() ||
		captureContext.TenantScope().ID().String() != record.Session.TenantID().String() {
		t.Fatalf("capture context = %+v", captureContext)
	}
}

func TestCaptureAuthenticatorCollapsesDurableMismatch(t *testing.T) {
	t.Parallel()

	authenticator, token, record := newCaptureAuthenticatorFixture(t)
	otherTokenID, err := id.ParseCaptureToken("ctk_01K3P4NQF00000000000000001")
	if err != nil {
		t.Fatalf("ParseCaptureToken() error = %v", err)
	}
	record.Credential, err = access.NewCaptureCredential(
		otherTokenID,
		record.Session.TenantID(),
		record.Session.ID(),
		1,
		record.Session.CreatedAt(),
		record.Session.CreatedAt().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewCaptureCredential() error = %v", err)
	}
	authenticator.repository = captureRepositoryStub{record: record}
	if _, err := authenticator.Authenticate(context.Background(), token.Reveal()); !errors.Is(err, access.ErrInvalidCaptureToken) {
		t.Fatalf("Authenticate() error = %v, want ErrInvalidCaptureToken", err)
	}
}

func TestOutcomeAuthenticatorReadsAfterSessionAndCaptureExpiry(t *testing.T) {
	t.Parallel()

	_, captureToken, record := newCaptureAuthenticatorFixture(t)
	outcomeTokenID, err := id.ParseOutcomeToken("otk_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseOutcomeToken() error = %v", err)
	}
	credential, err := access.NewOutcomeCredential(
		outcomeTokenID, record.Session.TenantID(), record.Session.ID(), 1,
		record.Session.CreatedAt(), record.Session.ExpiresAt().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewOutcomeCredential() error = %v", err)
	}
	keyring, err := access.NewOutcomeTokenKeyring(1, map[access.OutcomeTokenKeyVersion][]byte{
		1: bytes.Repeat([]byte{4}, 32),
	})
	if err != nil {
		t.Fatalf("NewOutcomeTokenKeyring() error = %v", err)
	}
	authenticationTime := record.Session.ExpiresAt().Add(10 * time.Minute)
	signer, err := access.NewOutcomeTokenSigner(keyring, sessionClock{now: authenticationTime})
	if err != nil {
		t.Fatalf("NewOutcomeTokenSigner() error = %v", err)
	}
	outcomeToken, err := signer.Sign(credential)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	authenticator, err := NewOutcomeAuthenticator(
		outcomeRepositoryStub{credential: credential}, signer, sessionClock{now: authenticationTime},
	)
	if err != nil {
		t.Fatalf("NewOutcomeAuthenticator() error = %v", err)
	}
	context, err := authenticator.Authenticate(t.Context(), outcomeToken.Reveal())
	if err != nil {
		t.Fatalf("Authenticate() error = %v", err)
	}
	if context.VerificationID().String() != record.Session.ID().String() {
		t.Fatalf("outcome verification = %q, want %q", context.VerificationID(), record.Session.ID())
	}
	if _, err := authenticator.Authenticate(t.Context(), captureToken.Reveal()); !errors.Is(err, access.ErrInvalidOutcomeToken) {
		t.Fatalf("capture token accepted as outcome token: %v", err)
	}
}

type outcomeRepositoryStub struct {
	credential access.OutcomeCredential
	err        error
}

func (repository outcomeRepositoryStub) FindForOutcome(
	context.Context,
	access.OutcomeTokenClaims,
) (access.OutcomeCredential, error) {
	return repository.credential, repository.err
}

type captureRepositoryStub struct {
	record SessionCreation
	err    error
}

func (repository captureRepositoryStub) FindForCapture(
	context.Context,
	access.CaptureTokenClaims,
) (SessionCreation, error) {
	return repository.record, repository.err
}

func newCaptureAuthenticatorFixture(
	t *testing.T,
) (*CaptureAuthenticator, access.PresentedCaptureToken, SessionCreation) {
	t.Helper()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	registry, profile, revision, verificationID := publishedProfileFixture(t, now)
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	session, err := NewSession(
		verificationID,
		profile.TenantID(),
		profile,
		revision,
		registry,
		"local",
		policyID,
		now.Add(2*time.Minute),
		now.Add(24*time.Hour),
	)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	tokenID, err := id.ParseCaptureToken("ctk_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseCaptureToken() error = %v", err)
	}
	credential, err := access.NewCaptureCredential(
		tokenID,
		session.TenantID(),
		session.ID(),
		1,
		session.CreatedAt(),
		session.CreatedAt().Add(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewCaptureCredential() error = %v", err)
	}
	keyring, err := access.NewCaptureTokenKeyring(1, map[access.CaptureTokenKeyVersion][]byte{
		1: bytes.Repeat([]byte{9}, 32),
	})
	if err != nil {
		t.Fatalf("NewCaptureTokenKeyring() error = %v", err)
	}
	authenticationTime := session.CreatedAt().Add(10 * time.Minute)
	signer, err := access.NewCaptureTokenSigner(keyring, sessionClock{now: authenticationTime})
	if err != nil {
		t.Fatalf("NewCaptureTokenSigner() error = %v", err)
	}
	token, err := signer.Sign(credential)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	record := SessionCreation{Session: session, Credential: credential}
	authenticator, err := NewCaptureAuthenticator(
		captureRepositoryStub{record: record},
		signer,
		sessionClock{now: authenticationTime},
	)
	if err != nil {
		t.Fatalf("NewCaptureAuthenticator() error = %v", err)
	}

	return authenticator, token, record
}
