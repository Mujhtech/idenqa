// Package tenant owns tenant identity, lifecycle, scope, and application ports.
package tenant

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

var (
	// ErrNotFound intentionally does not distinguish absent and cross-tenant resources.
	ErrNotFound = errors.New("tenant: not found")
	// ErrConflict means the expected lifecycle version no longer matches.
	ErrConflict = errors.New("tenant: lifecycle conflict")
)

// State is a tenant lifecycle state.
type State string

const (
	// StateActive permits tenant-scoped operations.
	StateActive State = "active"
	// StateDisabled prevents new tenant activity while retaining history.
	StateDisabled State = "disabled"
)

// Tenant is the tenant lifecycle aggregate.
type Tenant struct {
	id         id.Tenant
	state      State
	version    int64
	createdAt  time.Time
	updatedAt  time.Time
	disabledAt *time.Time
}

// Restore validates and reconstructs a tenant from durable state.
func Restore(identifier id.Tenant, state State, version int64, createdAt, updatedAt time.Time, disabledAt *time.Time) (Tenant, error) {
	createdAt = createdAt.UTC()
	updatedAt = updatedAt.UTC()
	if disabledAt != nil {
		value := disabledAt.UTC()
		disabledAt = &value
	}
	if identifier.IsZero() || version < 1 || createdAt.IsZero() || updatedAt.IsZero() || updatedAt.Before(createdAt) {
		return Tenant{}, errors.New("tenant state is invalid")
	}
	switch state {
	case StateActive:
		if disabledAt != nil {
			return Tenant{}, errors.New("active tenant cannot have a disabled time")
		}
	case StateDisabled:
		if disabledAt == nil || disabledAt.Before(createdAt) {
			return Tenant{}, errors.New("disabled tenant requires a valid disabled time")
		}
	default:
		return Tenant{}, fmt.Errorf("tenant state %q is invalid", state)
	}

	return Tenant{id: identifier, state: state, version: version, createdAt: createdAt, updatedAt: updatedAt, disabledAt: disabledAt}, nil
}

// ID returns the tenant identifier.
func (tenant Tenant) ID() id.Tenant { return tenant.id }

// State returns the lifecycle state.
func (tenant Tenant) State() State { return tenant.state }

// Version returns the optimistic-concurrency version.
func (tenant Tenant) Version() int64 { return tenant.version }

// CreatedAt returns the authoritative creation time.
func (tenant Tenant) CreatedAt() time.Time { return tenant.createdAt }

// UpdatedAt returns the latest lifecycle transition time.
func (tenant Tenant) UpdatedAt() time.Time { return tenant.updatedAt }

// DisabledAt returns a copy of the disable time when disabled.
func (tenant Tenant) DisabledAt() *time.Time {
	if tenant.disabledAt == nil {
		return nil
	}
	value := *tenant.disabledAt

	return &value
}

// Scope makes the effective tenant explicit on repository operations.
type Scope struct{ id id.Tenant }

// NewScope constructs a scope after the caller has verified its authority.
// Pre-authentication tenant hints must never be passed to this constructor.
func NewScope(identifier id.Tenant) (Scope, error) {
	if identifier.IsZero() {
		return Scope{}, errors.New("tenant scope requires an identifier")
	}

	return Scope{id: identifier}, nil
}

// ID returns the effective tenant.
func (scope Scope) ID() id.Tenant { return scope.id }

// AdminAction describes an explicit, auditable privileged operation.
type AdminAction struct {
	Actor      string
	Reason     string
	occurredAt time.Time
}

// OccurredAt returns the authoritative operation time assigned by Admin.
func (action AdminAction) OccurredAt() time.Time { return action.occurredAt }

// Validate checks bounded audit metadata.
func (action AdminAction) Validate() error {
	if err := validateAuditText("actor", action.Actor, 200); err != nil {
		return err
	}

	return validateAuditText("reason", action.Reason, 500)
}

func validateAuditText(name, value string, maximum int) error {
	if strings.TrimSpace(value) != value || value == "" || !utf8.ValidString(value) || utf8.RuneCountInString(value) > maximum {
		return fmt.Errorf("tenant admin %s is invalid", name)
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return fmt.Errorf("tenant admin %s contains control characters", name)
		}
	}

	return nil
}
