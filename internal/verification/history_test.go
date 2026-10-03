package verification

import (
	"context"
	"errors"
	"testing"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type historyRepositoryStub struct{ calls int }

func (repository *historyRepositoryStub) FindHistory(context.Context, tenant.Scope, id.Verification) (LifecycleHistory, error) {
	repository.calls++
	return LifecycleHistory{Transitions: []LifecycleTransition{}}, nil
}

func TestHistoryServiceRequiresVerificationRead(t *testing.T) {
	t.Parallel()
	repository := &historyRepositoryStub{}
	service, err := NewHistoryService(repository)
	if err != nil {
		t.Fatal(err)
	}
	identifier, err := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}

	if _, findErr := service.Find(t.Context(), newProfileServiceAuthority(t, "capture_profiles:read"), identifier); !errors.Is(findErr, access.ErrInsufficientScope) {
		t.Fatalf("Find() error = %v, want ErrInsufficientScope", findErr)
	}
	if repository.calls != 0 {
		t.Fatal("unauthorised history read reached persistence")
	}

	if _, err = service.Find(t.Context(), newProfileServiceAuthority(t, "verification_sessions:read"), identifier); err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if repository.calls != 1 {
		t.Fatalf("repository calls = %d, want 1", repository.calls)
	}
}
