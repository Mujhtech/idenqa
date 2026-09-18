package review

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Authority resolves server-managed reviewer assignments for an authenticated actor.
// Implementations must enforce tenant, region, validity and revocation on every call.
type Authority interface {
	ResolveReviewer(context.Context, tenant.Scope, Actor, string, time.Time) (Principal, error)
}

// Assignment binds a credential to a stable operator identity. Key rotation must
// retain OperatorID so multiple credentials cannot bypass reviewer independence.
type Assignment struct {
	TenantID       string       `json:"tenant_id"`
	APIKeyID       string       `json:"api_key_id"`
	OperatorID     string       `json:"operator_id"`
	Permissions    []Permission `json:"permissions"`
	Certifications []string     `json:"certifications"`
	Regions        []string     `json:"regions"`
	NotBefore      time.Time    `json:"not_before"`
	ExpiresAt      time.Time    `json:"expires_at"`
}

// Registry is an immutable snapshot of validated operator assignments.
type Registry struct{ assignments []Assignment }

// NewRegistry rejects ambiguous credentials and unbounded authority inputs.
func NewRegistry(assignments []Assignment) (*Registry, error) {
	if len(assignments) > 256 {
		return nil, ErrInvalid
	}
	registry := &Registry{}
	seen := map[string]bool{}
	for _, assignment := range assignments {
		if _, err := id.ParseTenant(assignment.TenantID); err != nil {
			return nil, ErrInvalid
		}
		if _, err := id.ParseAPIKey(assignment.APIKeyID); err != nil {
			return nil, ErrInvalid
		}
		key := assignment.TenantID + "/" + assignment.APIKeyID
		if seen[key] || !authorityLabel(assignment.OperatorID) || assignment.NotBefore.IsZero() || !assignment.ExpiresAt.After(assignment.NotBefore) || len(assignment.Permissions) == 0 || len(assignment.Permissions) > 4 || len(assignment.Regions) == 0 || len(assignment.Regions) > 32 || len(assignment.Certifications) > 32 {
			return nil, ErrInvalid
		}
		seen[key] = true
		for _, permission := range assignment.Permissions {
			if permission != PermissionClaim && permission != PermissionFind && permission != PermissionResolve && permission != PermissionAppeal {
				return nil, ErrInvalid
			}
		}
		for _, label := range append(slices.Clone(assignment.Regions), assignment.Certifications...) {
			if !authorityLabel(label) {
				return nil, ErrInvalid
			}
		}
		assignment.Permissions = slices.Clone(assignment.Permissions)
		assignment.Certifications = slices.Clone(assignment.Certifications)
		assignment.Regions = slices.Clone(assignment.Regions)
		registry.assignments = append(registry.assignments, assignment)
	}
	return registry, nil
}

// ResolveReviewer grants only the current exact tenant, credential and region assignment.
func (registry *Registry) ResolveReviewer(ctx context.Context, scope tenant.Scope, actor Actor, region string, now time.Time) (Principal, error) {
	if err := ctx.Err(); err != nil {
		return Principal{}, err
	}
	if registry == nil || now.IsZero() {
		return Principal{}, ErrForbidden
	}
	for _, assignment := range registry.assignments {
		if assignment.TenantID == scope.ID().String() && assignment.APIKeyID == actor.ID && !now.Before(assignment.NotBefore) && now.Before(assignment.ExpiresAt) && slices.Contains(assignment.Regions, region) {
			return Principal{ID: assignment.OperatorID, Permissions: slices.Clone(assignment.Permissions), Certifications: slices.Clone(assignment.Certifications)}, nil
		}
	}
	return Principal{}, ErrForbidden
}

func authorityLabel(value string) bool {
	return bounded(value, 128) && !strings.ContainsFunc(value, func(r rune) bool { return r <= ' ' || r > '~' })
}
