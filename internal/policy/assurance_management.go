package policy

import (
	"context"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// AssuranceSelection addresses an exact immutable profile, never an active alias.
type AssuranceSelection struct {
	Name     string `json:"name"`
	Revision uint32 `json:"revision"`
	Digest   string `json:"digest"`
}

// AssuranceCommand publishes a revision or version-checks future session assignment.
type AssuranceCommand struct {
	Operation       string              `json:"operation"`
	Profile         *AssuranceProfile   `json:"profile,omitempty"`
	PolicyID        string              `json:"policy_id,omitempty"`
	Selection       *AssuranceSelection `json:"selection,omitempty"`
	ExpectedVersion int64               `json:"expected_version"`
	Actor           id.APIKey           `json:"-"`
	Key             string              `json:"-"`
	At              time.Time           `json:"-"`
}

// AssuranceQuery supports bounded discovery and pinned session inspection.
type AssuranceQuery struct {
	Name           string
	Revision       uint32
	PolicyID       string
	VerificationID string
	After          string
	Limit          int
}

// AssuranceResource exposes safe profile and assignment metadata.
type AssuranceResource struct {
	Profile    *AssuranceProfile    `json:"profile,omitempty"`
	Digest     string               `json:"digest,omitempty"`
	Profiles   []AssuranceSelection `json:"profiles,omitzero"`
	Selection  *AssuranceSelection  `json:"selection,omitempty"`
	Version    int64                `json:"version,omitempty"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// AssuranceRepository owns immutable profiles and atomic assignment changes.
type AssuranceRepository interface {
	WriteAssurance(context.Context, tenant.Scope, AssuranceCommand) (AssuranceResource, error)
	ReadAssurance(context.Context, tenant.Scope, AssuranceQuery) (AssuranceResource, error)
}

// AssuranceManagement enforces policy permissions in the application boundary.
type AssuranceManagement struct {
	repository AssuranceRepository
	now        func() time.Time
}

// NewAssuranceManagement constructs the authorised assurance application boundary.
func NewAssuranceManagement(r AssuranceRepository, now func() time.Time) (*AssuranceManagement, error) {
	if r == nil || now == nil {
		return nil, ErrInvalid
	}
	return &AssuranceManagement{r, now}, nil
}
func (s *AssuranceManagement) Write(ctx context.Context, a access.Context, key string, c AssuranceCommand) (AssuranceResource, error) {
	permission := access.PermissionPoliciesWrite
	if c.Operation == "assign" {
		permission = access.PermissionPoliciesActivate
	}
	if e := a.Require(permission); e != nil {
		return AssuranceResource{}, e
	}
	if key == "" || len(key) > 200 || c.ExpectedVersion < 0 || c.ExpectedVersion == 9223372036854775807 {
		return AssuranceResource{}, ErrInvalid
	}
	switch c.Operation {
	case "publish":
		if c.Profile == nil || c.PolicyID != "" || c.Selection != nil || c.ExpectedVersion != 0 {
			return AssuranceResource{}, ErrInvalid
		}
		p, _, e := CanonicalAssuranceProfile(*c.Profile)
		if e != nil {
			return AssuranceResource{}, e
		}
		c.Profile = &p
	case "assign":
		if _, e := id.ParsePolicy(c.PolicyID); e != nil || c.Profile != nil {
			return AssuranceResource{}, ErrInvalid
		}
		if c.Selection != nil && (!validToken(c.Selection.Name, 64) || c.Selection.Revision == 0 || !validDigest(c.Selection.Digest)) {
			return AssuranceResource{}, ErrInvalid
		}
	default:
		return AssuranceResource{}, ErrInvalid
	}
	c.Actor = a.Principal().KeyID()
	c.Key = key
	c.At = s.now().UTC().Truncate(time.Microsecond)
	return s.repository.WriteAssurance(ctx, a.TenantScope(), c)
}
func (s *AssuranceManagement) Read(ctx context.Context, a access.Context, q AssuranceQuery) (AssuranceResource, error) {
	if e := a.Require(access.PermissionPoliciesRead); e != nil {
		return AssuranceResource{}, e
	}
	if q.Limit == 0 {
		q.Limit = 50
	}
	if q.Limit < 1 || q.Limit > 100 || len(q.After) > 80 {
		return AssuranceResource{}, ErrInvalid
	}
	modes := 0
	if q.Name != "" {
		modes++
		if !validToken(q.Name, 64) || q.Revision == 0 {
			return AssuranceResource{}, ErrInvalid
		}
	}
	if q.PolicyID != "" {
		modes++
		if _, e := id.ParsePolicy(q.PolicyID); e != nil {
			return AssuranceResource{}, ErrInvalid
		}
	}
	if q.VerificationID != "" {
		modes++
		if _, e := id.ParseVerification(q.VerificationID); e != nil {
			return AssuranceResource{}, ErrInvalid
		}
	}
	if modes > 1 {
		return AssuranceResource{}, ErrInvalid
	}
	return s.repository.ReadAssurance(ctx, a.TenantScope(), q)
}

// Validate checks a profile without publishing or selecting it.
func (s *AssuranceManagement) Validate(ctx context.Context, a access.Context, p AssuranceProfile) (AssuranceResource, error) {
	if e := a.Require(access.PermissionPoliciesWrite); e != nil {
		return AssuranceResource{}, e
	}
	if e := ctx.Err(); e != nil {
		return AssuranceResource{}, e
	}
	p, d, e := CanonicalAssuranceProfile(p)
	if e != nil {
		return AssuranceResource{}, e
	}
	return AssuranceResource{Profile: &p, Digest: d}, nil
}

// Capabilities exposes platform semantics and owned core derivation identifiers.
func (s *AssuranceManagement) Capabilities(ctx context.Context, a access.Context) ([]AssuranceCapability, error) {
	if e := a.Require(access.PermissionPoliciesRead); e != nil {
		return nil, e
	}
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	return AssuranceCapabilities(), nil
}
