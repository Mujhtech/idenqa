package verification

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type cancellationStub struct {
	calls    int
	mutation CancellationMutation
}

func (repository *cancellationStub) Cancel(_ context.Context, _ tenant.Scope, mutation CancellationMutation) (CancellationResult, error) {
	repository.calls++
	repository.mutation = mutation
	return CancellationResult{}, nil
}

func TestCancellationApplicationAuthorityAndReplayScope(t *testing.T) {
	repository := &cancellationStub{}
	service, err := NewCancellationService(repository, sessionClock{now: time.Date(2026, 9, 6, 12, 0, 0, 1234, time.UTC)}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	auth, token, record := newCaptureAuthenticatorFixture(t)
	if _, err := service.CancelTenant(t.Context(), access.Context{}, record.Session.ID(), 1, "cancel-1"); !errors.Is(err, access.ErrInsufficientScope) || repository.calls != 0 {
		t.Fatalf("unauthorised cancellation: %v", err)
	}
	actor := newProfileServiceAuthority(t, access.Pattern("verification_sessions:cancel"))
	if _, err := service.CancelTenant(t.Context(), actor, record.Session.ID(), 1, "cancel-1"); err != nil {
		t.Fatal(err)
	}
	first := repository.mutation.Retry
	if first.CreatedAt().Nanosecond()%1000 != 0 || first.Principal().String() != actor.Principal().KeyID().String() {
		t.Fatal("noncanonical cancellation scope")
	}
	if _, err := service.CancelTenant(t.Context(), actor, record.Session.ID(), 2, "cancel-1"); err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() == repository.mutation.Retry.Fingerprint() {
		t.Fatal("version absent from fingerprint")
	}
	capture, err := (CancellationAuthenticator{auth}).Authenticate(t.Context(), token.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CancelSubject(t.Context(), capture, 1, "subject-1"); err != nil {
		t.Fatal(err)
	}
	if repository.mutation.VerificationID != record.Session.ID() || repository.mutation.Retry.Principal().String() != capture.TokenID().String() {
		t.Fatal("subject cancellation was not credential-bound")
	}
}

func TestCancellationAuthenticationDoesNotReopenCapture(t *testing.T) {
	for _, state := range []SessionState{SessionStateProcessing, SessionStateCancelled, SessionStateExpired, SessionStateCompleted} {
		t.Run(string(state), func(t *testing.T) {
			auth, token, record := newCaptureAuthenticatorFixture(t)
			record.Session.state = state
			auth.repository = captureRepositoryStub{record: record}
			if _, err := auth.Authenticate(t.Context(), token.Reveal()); !errors.Is(err, access.ErrInvalidCaptureToken) {
				t.Fatalf("capture reopened: %v", err)
			}
			if _, err := (CancellationAuthenticator{auth}).Authenticate(t.Context(), token.Reveal()); err != nil {
				t.Fatal(err)
			}
			if _, err := (CancellationAuthenticator{auth}).Authenticate(t.Context(), token.Reveal()+"invalid"); !errors.Is(err, access.ErrInvalidCaptureToken) {
				t.Fatal("invalid signature accepted")
			}
		})
	}
}
