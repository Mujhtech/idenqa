package tenant

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// Repository is the tenant-scoped port consumed by application services.
type Repository interface {
	Find(context.Context, Scope, id.Tenant) (Tenant, error)
}

// AdminRepository is the privileged port consumed only by administrative workflows.
type AdminRepository interface {
	Create(context.Context, AdminAction, Tenant) error
	Provision(context.Context, AdminAction, ProvisionCommand, Tenant) (Tenant, bool, error)
	Inspect(context.Context, AdminAction, id.Tenant) (Tenant, error)
	Disable(context.Context, AdminAction, id.Tenant, int64, time.Time) (Tenant, error)
}

// Admin coordinates privileged tenant lifecycle operations.
type Admin struct {
	repository AdminRepository
	ids        *id.Generator
	clock      clock.Clock
}

// NewAdmin constructs the tenant administration application service.
func NewAdmin(repository AdminRepository, identifiers *id.Generator, source clock.Clock) (*Admin, error) {
	if repository == nil || identifiers == nil || source == nil {
		return nil, errors.New("tenant admin dependencies are required")
	}

	return &Admin{repository: repository, ids: identifiers, clock: source}, nil
}

// Create creates an active tenant and its audit record atomically.
func (admin *Admin) Create(ctx context.Context, action AdminAction, displayName string) (Tenant, error) {
	if err := action.Validate(); err != nil {
		return Tenant{}, err
	}
	identifier, err := admin.ids.NewTenant()
	if err != nil {
		return Tenant{}, fmt.Errorf("generate tenant id: %w", err)
	}
	now := admin.clock.Now().UTC()
	action.occurredAt = now
	created, err := Restore(identifier, displayName, StateActive, 1, now, now, nil)
	if err != nil {
		return Tenant{}, err
	}
	if err := admin.repository.Create(ctx, action, created); err != nil {
		return Tenant{}, fmt.Errorf("create tenant: %w", err)
	}

	return created, nil
}

// Provision creates a tenant exactly once for an immutable managed-deployment
// command. A replay returns the original tenant without creating another.
func (admin *Admin) Provision(
	ctx context.Context,
	action AdminAction,
	command ProvisionCommand,
	displayName string,
) (Tenant, bool, error) {
	if err := action.Validate(); err != nil {
		return Tenant{}, false, err
	}
	if err := command.Validate(); err != nil {
		return Tenant{}, false, err
	}
	identifier, err := admin.ids.NewTenant()
	if err != nil {
		return Tenant{}, false, fmt.Errorf("generate provisioned tenant id: %w", err)
	}
	now := admin.clock.Now().UTC()
	action.occurredAt = now
	candidate, err := Restore(identifier, displayName, StateActive, 1, now, now, nil)
	if err != nil {
		return Tenant{}, false, err
	}
	provisioned, created, err := admin.repository.Provision(ctx, action, command, candidate)
	if err != nil {
		return Tenant{}, false, fmt.Errorf("provision tenant: %w", err)
	}
	return provisioned, created, nil
}

// Inspect retrieves a tenant through an audited administrative bypass.
func (admin *Admin) Inspect(ctx context.Context, action AdminAction, identifier id.Tenant) (Tenant, error) {
	if err := action.Validate(); err != nil {
		return Tenant{}, err
	}

	action.occurredAt = admin.clock.Now().UTC()

	return admin.repository.Inspect(ctx, action, identifier)
}

// Disable performs an optimistic, one-way lifecycle transition.
func (admin *Admin) Disable(ctx context.Context, action AdminAction, identifier id.Tenant, expectedVersion int64) (Tenant, error) {
	if err := action.Validate(); err != nil {
		return Tenant{}, err
	}
	if expectedVersion < 1 {
		return Tenant{}, errors.New("tenant expected version must be positive")
	}

	now := admin.clock.Now().UTC()
	action.occurredAt = now

	return admin.repository.Disable(ctx, action, identifier, expectedVersion, now)
}
