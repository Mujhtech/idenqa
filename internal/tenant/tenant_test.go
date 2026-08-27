package tenant_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type adminRepository struct {
	created tenant.Tenant
	action  tenant.AdminAction
}

func (repository *adminRepository) Create(_ context.Context, action tenant.AdminAction, value tenant.Tenant) error {
	repository.action, repository.created = action, value

	return nil
}

func (repository *adminRepository) Inspect(_ context.Context, _ tenant.AdminAction, _ id.Tenant) (tenant.Tenant, error) {
	return repository.created, nil
}

func (repository *adminRepository) Disable(_ context.Context, _ tenant.AdminAction, identifier id.Tenant, version int64, now time.Time) (tenant.Tenant, error) {
	if version != repository.created.Version() {
		return tenant.Tenant{}, tenant.ErrConflict
	}

	return tenant.Restore(identifier, tenant.StateDisabled, version+1, repository.created.CreatedAt(), now, &now)
}

func TestAdminLifecycle(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 15, 0, 0, 0, time.UTC)
	generator, err := id.NewGenerator(fixedClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{3}, 32)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	repository := &adminRepository{}
	admin, err := tenant.NewAdmin(repository, generator, fixedClock{now: now})
	if err != nil {
		t.Fatalf("NewAdmin() error = %v", err)
	}
	action := tenant.AdminAction{Actor: "test-operator", Reason: "tenant lifecycle test"}
	created, err := admin.Create(t.Context(), action)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.State() != tenant.StateActive || created.Version() != 1 || repository.action.OccurredAt() != now {
		t.Fatalf("created tenant = %+v", created)
	}
	disabled, err := admin.Disable(t.Context(), action, created.ID(), 1)
	if err != nil {
		t.Fatalf("Disable() error = %v", err)
	}
	if disabled.State() != tenant.StateDisabled || disabled.Version() != 2 || disabled.DisabledAt() == nil {
		t.Fatalf("disabled tenant = %+v", disabled)
	}
}

func TestAdminActionRejectsUnsafeAuditText(t *testing.T) {
	t.Parallel()

	tests := []tenant.AdminAction{
		{Actor: "", Reason: "reason"},
		{Actor: "operator", Reason: " line"},
		{Actor: "operator\nforged", Reason: "reason"},
	}
	for _, action := range tests {
		if err := action.Validate(); err == nil {
			t.Errorf("Validate(%+v) error = nil", action)
		}
	}
}

func TestNewScopeRejectsZeroTenant(t *testing.T) {
	t.Parallel()

	if _, err := tenant.NewScope(id.Tenant{}); err == nil {
		t.Error("NewScope(zero) error = nil")
	}
}
