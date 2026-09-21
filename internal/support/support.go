// Package support owns tenant-granted delegated support access and emergency
// break-glass access. Grants and approvals are time-bounded, permission-scoped,
// immutably recorded, and explicitly revocable; every break-glass use is
// appended to a durable ledger.
package support

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// MaximumGrantDuration bounds one delegated support grant.
	MaximumGrantDuration = 72 * time.Hour
	// MinimumDuration bounds every delegated or emergency access window.
	MinimumDuration = 5 * time.Minute
	// MaximumGrantPatterns bounds one delegated grant's scope patterns.
	MaximumGrantPatterns = 8
	// ApprovalWindow bounds how long a break-glass request may wait for approval.
	ApprovalWindow = time.Hour
	// MaximumEmergencyDuration bounds one approved break-glass access window.
	MaximumEmergencyDuration = 4 * time.Hour
	// MaximumEmergencyPermissions bounds one break-glass permission set.
	MaximumEmergencyPermissions = 4
	// MaximumBreakGlassUses bounds one request's recorded uses.
	MaximumBreakGlassUses = 256
	// GrantIDPrefix is the public delegated-grant identifier prefix.
	GrantIDPrefix id.Prefix = "spt"
	// EmergencyIDPrefix is the public break-glass request identifier prefix.
	EmergencyIDPrefix id.Prefix = "bge"
)

var (
	// ErrInvalid identifies malformed grants, requests, or commands.
	ErrInvalid = errors.New("support: invalid command or state")
	// ErrNotFound identifies a missing grant or request.
	ErrNotFound = errors.New("support: not found")
	// ErrConflict identifies an expected-version mismatch.
	ErrConflict = errors.New("support: version conflict")
	// ErrExpired identifies an access window that has elapsed.
	ErrExpired = errors.New("support: access window expired")
	// ErrForbidden identifies a scope or self-approval violation.
	ErrForbidden = errors.New("support: operation is not permitted")
	// ErrUnavailable identifies persistence availability failures.
	ErrUnavailable = errors.New("support: unavailable")
)

// State is one grant or emergency lifecycle state.
type State string

const (
	// StateActive is an issued delegated support grant.
	StateActive State = "active"
	// StateRevoked is an explicitly revoked grant or request.
	StateRevoked State = "revoked"
	// StateExpired is an elapsed delegated grant.
	StateExpired State = "expired"
	// StateRequested is a break-glass request awaiting approval.
	StateRequested State = "requested"
	// StateApproved is an approved break-glass access window.
	StateApproved State = "approved"
	// StateDenied is a denied break-glass request.
	StateDenied State = "denied"
)

// Grant is one tenant-granted, time-bounded, permission-scoped support grant.
type Grant struct {
	ID               string     `json:"id"`
	Grantee          string     `json:"grantee"`
	Patterns         []string   `json:"patterns"`
	Permissions      []string   `json:"permissions"`
	Reason           string     `json:"reason"`
	GrantedBy        string     `json:"granted_by"`
	State            State      `json:"state"`
	Version          int64      `json:"version"`
	StartsAt         time.Time  `json:"starts_at"`
	ExpiresAt        time.Time  `json:"expires_at"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	RevokedAt        *time.Time `json:"revoked_at,omitempty"`
	RevokedBy        *string    `json:"revoked_by,omitempty"`
	RevocationReason *string    `json:"revocation_reason,omitempty"`
}

// ActiveAt reports whether the grant authorizes at the given instant.
func (grant Grant) ActiveAt(at time.Time) bool {
	return grant.State == StateActive && !at.Before(grant.StartsAt) && at.Before(grant.ExpiresAt)
}

// Allows reports whether the active grant covers one exact permission.
func (grant Grant) Allows(permission access.Permission, at time.Time) bool {
	if !grant.ActiveAt(at) {
		return false
	}

	return slices.Contains(grant.Permissions, string(permission))
}

// Emergency is one break-glass request with its approval and expiry state.
type Emergency struct {
	ID                string        `json:"id"`
	Requester         string        `json:"requester"`
	Reason            string        `json:"reason"`
	Permissions       []string      `json:"permissions"`
	Duration          time.Duration `json:"duration"`
	State             State         `json:"state"`
	Version           int64         `json:"version"`
	RequestedAt       time.Time     `json:"requested_at"`
	ApprovalExpiresAt time.Time     `json:"approval_expires_at"`
	ApprovedAt        *time.Time    `json:"approved_at,omitempty"`
	ApprovedBy        *string       `json:"approved_by,omitempty"`
	UsableUntil       *time.Time    `json:"usable_until,omitempty"`
	DeniedAt          *time.Time    `json:"denied_at,omitempty"`
	DeniedBy          *string       `json:"denied_by,omitempty"`
	RevokedAt         *time.Time    `json:"revoked_at,omitempty"`
	RevokedBy         *string       `json:"revoked_by,omitempty"`
	RevocationReason  *string       `json:"revocation_reason,omitempty"`
}

// ActiveAt reports whether the emergency access window authorizes at at.
func (emergency Emergency) ActiveAt(at time.Time) bool {
	return emergency.State == StateApproved && emergency.UsableUntil != nil && at.Before(*emergency.UsableUntil)
}

// Allows reports whether the approved set covers one exact permission and the
// access window is still open.
func (emergency Emergency) Allows(permission access.Permission, at time.Time) bool {
	if !emergency.ActiveAt(at) {
		return false
	}

	return slices.Contains(emergency.Permissions, string(permission))
}

// Use is one appended break-glass access record.
type Use struct {
	Sequence   int64     `json:"sequence"`
	Permission string    `json:"permission"`
	Target     string    `json:"target"`
	Actor      string    `json:"actor"`
	UsedAt     time.Time `json:"used_at"`
}

// Command is a validated, authorized, idempotent support mutation.
type Command struct {
	Operation       string
	Identifier      string
	ExpectedVersion int64
	Grantee         string
	Patterns        []string
	Permissions     []string
	Duration        time.Duration
	Reason          string
	Target          string
	ActorKeyID      string
	Retry           idempotency.Request
	At              time.Time
}

// Result exposes safe support state without credential material.
type Result struct {
	Grant     *Grant     `json:"grant,omitempty"`
	Emergency *Emergency `json:"emergency,omitempty"`
	Uses      []Use      `json:"uses,omitempty"`
	Grants    []Grant    `json:"grants,omitempty"`
}

// Repository owns transactional support mutations and safe reads.
type Repository interface {
	Execute(context.Context, tenant.Scope, Command) (Result, error)
	Read(context.Context, tenant.Scope, string, string) (Result, error)
}

// Service authorizes and validates support operations at the application
// boundary.
type Service struct {
	repository Repository
	registry   access.Registry
	now        func() time.Time
}

// NewService constructs authorized support operations over the tenant
// permission registry.
func NewService(repository Repository, registry access.Registry, now func() time.Time) (*Service, error) {
	if repository == nil || now == nil {
		return nil, ErrInvalid
	}

	return &Service{repository: repository, registry: registry, now: now}, nil
}

// grantable patterns must never include the support surfaces themselves, so a
// delegated administrator cannot re-delegate, approve, or break glass.
func grantable(registry access.Registry, patterns []access.Pattern) bool {
	for _, pattern := range patterns {
		permissions, err := registry.Resolve(pattern)
		if err != nil {
			return false
		}
		for _, permission := range permissions.Permissions() {
			resource, _, _ := strings.Cut(string(permission), ":")
			if resource == "support_access" || resource == "break_glass" {
				return false
			}
		}
	}

	return true
}

// Execute authorizes and fingerprints one support command.
func (service *Service) Execute(ctx context.Context, auth access.Context, key string, command Command) (Result, error) {
	if ctx == nil || auth.TenantScope().ID().IsZero() {
		return Result{}, ErrInvalid
	}
	permission := access.PermissionSupportAccessWrite
	switch command.Operation {
	case "break_glass_request":
		permission = access.PermissionBreakGlassRequest
	case "break_glass_approve":
		permission = access.PermissionBreakGlassApprove
	case "break_glass_use":
		permission = access.PermissionBreakGlassUse
	}
	if err := auth.Require(permission); err != nil {
		return Result{}, err
	}

	return service.ExecuteDirect(ctx, auth.TenantScope(), auth.Principal().KeyID(), key, command)
}

// ExecuteDirect validates and fingerprints one support command for an
// already-authenticated operator actor. The operator CLI uses it directly;
// HTTP callers use Execute so the permission is always checked.
func (service *Service) ExecuteDirect(ctx context.Context, scope tenant.Scope, actor id.APIKey, key string, command Command) (Result, error) {
	if ctx == nil || scope.ID().IsZero() || actor.IsZero() {
		return Result{}, ErrInvalid
	}
	if err := service.validate(&command); err != nil {
		return Result{}, err
	}
	command.ActorKeyID = actor.String()
	command.At = service.now().UTC().Truncate(time.Microsecond)
	canonical, err := marshalCommand(command, key)
	if err != nil {
		return Result{}, ErrInvalid
	}
	command.Retry, err = idempotency.NewRequest(scope.ID(), actor, "support."+command.Operation, key, canonical, command.At, 30*24*time.Hour)
	if err != nil {
		return Result{}, ErrInvalid
	}

	return service.repository.Execute(ctx, scope, command)
}

// Read authorizes safe support reads.
func (service *Service) Read(ctx context.Context, auth access.Context, kind, reference string) (Result, error) {
	if err := auth.Require(access.PermissionSupportAccessRead); err != nil {
		return Result{}, err
	}

	return service.repository.Read(ctx, auth.TenantScope(), kind, reference)
}

// AuthorizeSupport reports whether an active delegated grant covers permission
// for grantee at the current instant. It is the enforcement seam for a future
// support-credential authentication path.
func (service *Service) AuthorizeSupport(ctx context.Context, scope tenant.Scope, grantee string, permission access.Permission) (Grant, error) {
	result, err := service.repository.Read(ctx, scope, "grant_grantee", grantee)
	if err != nil {
		return Grant{}, err
	}
	if result.Grant == nil || !result.Grant.Allows(permission, service.now().UTC()) {
		return Grant{}, ErrForbidden
	}

	return *result.Grant, nil
}

// AuthorizeEmergency reports whether an approved break-glass window covers
// permission at the current instant.
func (service *Service) AuthorizeEmergency(ctx context.Context, scope tenant.Scope, identifier string, permission access.Permission) (Emergency, error) {
	result, err := service.repository.Read(ctx, scope, "emergency", identifier)
	if err != nil {
		return Emergency{}, err
	}
	if result.Emergency == nil || !result.Emergency.Allows(permission, service.now().UTC()) {
		return Emergency{}, ErrForbidden
	}

	return *result.Emergency, nil
}

func (service *Service) validate(command *Command) error {
	switch command.Operation {
	case "grant":
		if command.ExpectedVersion != 0 || len(command.Patterns) == 0 || len(command.Patterns) > MaximumGrantPatterns ||
			command.Grantee == "" || !validIdentifier(command.Grantee) ||
			command.Duration < MinimumDuration || command.Duration > MaximumGrantDuration {
			return ErrInvalid
		}
		patterns := make([]access.Pattern, 0, len(command.Patterns))
		for _, value := range command.Patterns {
			pattern, err := access.ParsePattern(value)
			if err != nil {
				return ErrInvalid
			}
			patterns = append(patterns, pattern)
		}
		if !grantable(service.registry, patterns) {
			return ErrForbidden
		}
		grant, err := service.registry.Resolve(patterns...)
		if err != nil {
			return ErrInvalid
		}
		for _, permission := range grant.Permissions() {
			command.Permissions = append(command.Permissions, string(permission))
		}
	case "grant_revoke":
		if command.ExpectedVersion < 1 {
			return ErrInvalid
		}
	case "break_glass_request":
		if command.ExpectedVersion != 0 || len(command.Permissions) == 0 ||
			len(command.Permissions) > MaximumEmergencyPermissions ||
			command.Duration < MinimumDuration || command.Duration > MaximumEmergencyDuration {
			return ErrInvalid
		}
		for _, value := range command.Permissions {
			permission, err := access.ParsePermission(value)
			if err != nil || !emergencyAllowed(permission) {
				return ErrForbidden
			}
		}
	case "break_glass_approve", "break_glass_deny", "break_glass_revoke":
		if command.ExpectedVersion < 1 {
			return ErrInvalid
		}
	case "break_glass_use":
		if command.ExpectedVersion < 1 || command.Target == "" || !validIdentifier(command.Target) ||
			len(command.Permissions) != 1 {
			return ErrInvalid
		}
		if _, err := access.ParsePermission(command.Permissions[0]); err != nil {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	if len(command.Reason) < 8 || len(command.Reason) > 1024 {
		return ErrInvalid
	}

	return nil
}

// emergencyAllowed restricts break-glass to the bounded read-oriented incident
// set. A request can never exceed this maximum.
func emergencyAllowed(permission access.Permission) bool {
	switch permission {
	case access.PermissionSubjectsRead, access.PermissionIdentityRead, access.PermissionIdentityReveal,
		access.PermissionVerificationSessionsRead, access.PermissionReviewsRead,
		access.PermissionPrivacyRequestsRead, access.PermissionKMSRead, access.PermissionTenantRead:
		return true
	default:
		return false
	}
}

func marshalCommand(command Command, key string) ([]byte, error) {
	return json.Marshal(struct {
		Operation       string
		Identifier      string
		ExpectedVersion int64
		Grantee         string
		Patterns        []string
		Permissions     []string
		Duration        int64
		Reason          string
		Target          string
		Key             string
	}{
		Operation: command.Operation, Identifier: command.Identifier, ExpectedVersion: command.ExpectedVersion,
		Grantee: command.Grantee, Patterns: command.Patterns, Permissions: command.Permissions,
		Duration: int64(command.Duration), Reason: command.Reason, Target: command.Target, Key: key,
	})
}

func validIdentifier(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '-' && character != '.' {
			return false
		}
	}

	return true
}
