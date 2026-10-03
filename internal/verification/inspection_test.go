package verification

import (
	"context"
	"errors"
	"testing"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type inspectionRepositoryStub struct{ calls int }

func (repository *inspectionRepositoryStub) Inspect(context.Context, tenant.Scope, id.Verification) (Inspection, error) {
	repository.calls++
	return Inspection{Evidence: []EvidenceInspection{}, Checks: []CheckInspection{}, Retention: []RetentionInspection{}, Webhooks: []WebhookInspection{}}, nil
}

func TestInspectionServiceRequiresEverySensitiveReadPermission(t *testing.T) {
	t.Parallel()
	repository := &inspectionRepositoryStub{}
	service, err := NewInspectionService(repository)
	if err != nil {
		t.Fatal(err)
	}
	identifier, err := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}

	for _, permissions := range [][]access.Pattern{
		{"verification_sessions:read"},
		{"verification_sessions:read", "evidence:read"},
	} {
		authority := newProfileServiceAuthority(t, permissions...)
		if _, inspectErr := service.Inspect(t.Context(), authority, identifier); !errors.Is(inspectErr, access.ErrInsufficientScope) {
			t.Fatalf("Inspect() error = %v, want ErrInsufficientScope", inspectErr)
		}
	}
	if repository.calls != 0 {
		t.Fatal("partially authorised inspection reached persistence")
	}

	authority := newProfileServiceAuthority(t, "verification_sessions:read", "evidence:read", "webhooks:read")
	if _, err = service.Inspect(t.Context(), authority, identifier); err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if repository.calls != 1 {
		t.Fatalf("repository calls = %d, want 1", repository.calls)
	}
}
