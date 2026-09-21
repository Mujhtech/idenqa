package keycustody

import (
	"context"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	// RecoveryIDPrefix is the public identifier prefix for recovery ceremonies.
	RecoveryIDPrefix id.Prefix = "krc"
	// RecoveryApprovalWindow bounds how long an unapproved ceremony may wait
	// for its second principal.
	RecoveryApprovalWindow = 24 * time.Hour
	// RecoveryUseWindow bounds how long an approved ceremony may be completed.
	RecoveryUseWindow = time.Hour
)

// Recovery ceremony kinds.
const (
	// RecoveryKindRewrap immediately rewraps one class for one tenant.
	RecoveryKindRewrap = "rewrap"
	// RecoveryKindMigrateEpoch authorises a new fleet sweep generation.
	RecoveryKindMigrateEpoch = "migrate_epoch"
)

// Recovery ceremony states.
const (
	RecoveryStarted   = "started"
	RecoveryApproved  = "approved"
	RecoveryCompleted = "completed"
	RecoveryAborted   = "aborted"
)

var (
	// ErrRecoveryForbidden identifies a self-approval attempt or an operation
	// outside the authenticated principal's role.
	ErrRecoveryForbidden = errors.New("key custody: recovery requires a distinct approving principal")
	// ErrRecoveryExpired identifies an approval or completion outside the
	// bounded ceremony validity window.
	ErrRecoveryExpired = errors.New("key custody: recovery ceremony validity window elapsed")
	// ErrRecoveryState identifies an operation the ceremony state does not permit.
	ErrRecoveryState = errors.New("key custody: recovery ceremony state does not permit this operation")
)

// RecoveryCeremony is one auditable dual-control key recovery or epoch
// migration authorisation.
type RecoveryCeremony struct {
	ID          string
	Kind        string
	Class       string
	TenantID    id.Tenant
	Target      DestructionTarget
	State       string
	Version     int64
	StartedBy   string
	StartedAt   time.Time
	ApproveBy   time.Time
	ApprovedBy  string
	ApprovedAt  time.Time
	UsableUntil time.Time
	CompletedBy string
	CompletedAt time.Time
	AbortedBy   string
	AbortedAt   time.Time
	AbortReason string
	Receipt     map[string]any
	Reason      string
	UpdatedAt   time.Time
}

// RecoveryCommand is one validated ceremony operation.
type RecoveryCommand struct {
	Operation       string
	Identifier      string
	Kind            string
	Class           string
	TenantID        string
	Target          DestructionTarget
	ExpectedVersion int64
	Reason          string
}

// RecoveryResult exposes the ceremony after one transition.
type RecoveryResult struct {
	Ceremony RecoveryCeremony
}

// RecoveryRepository persists the ceremony state machine.
type RecoveryRepository interface {
	Create(context.Context, RecoveryCeremony) (RecoveryCeremony, error)
	Load(context.Context, string) (RecoveryCeremony, error)
	Advance(context.Context, RecoveryCeremony, RecoveryCeremony) (RecoveryCeremony, error)
}

// TenantClassRewrapper immediately rewraps one class for one tenant. It is
// consumed by the rewrap ceremony kind.
type TenantClassRewrapper interface {
	RewrapTenantClass(context.Context, string, id.Tenant, int) (int, error)
}

// EpochAuthorizer verifies a target provider identity and begins a new fleet
// sweep generation for one class. It is consumed by the epoch-migration kind.
type EpochAuthorizer interface {
	AuthorizeEpoch(context.Context, string, string, string, string, string) (int64, error)
}

// RecoveryService enforces the dual-control ceremony state machine.
type RecoveryService struct {
	repository  RecoveryRepository
	rewrapper   TenantClassRewrapper
	epochs      EpochAuthorizer
	identifiers *id.Generator
	now         func() time.Time
}

// NewRecoveryService composes the ceremony service. The rewrap and epoch
// executors may be nil; the matching completion then fails closed.
func NewRecoveryService(
	repository RecoveryRepository,
	rewrapper TenantClassRewrapper,
	epochs EpochAuthorizer,
	identifiers *id.Generator,
	now func() time.Time,
) (*RecoveryService, error) {
	if repository == nil || identifiers == nil || now == nil {
		return nil, ErrInvalid
	}

	return &RecoveryService{repository: repository, rewrapper: rewrapper, epochs: epochs, identifiers: identifiers, now: now}, nil
}

// Execute applies one ceremony transition for the authenticated operator.
func (service *RecoveryService) Execute(ctx context.Context, auth access.Context, command RecoveryCommand) (RecoveryResult, error) {
	if ctx == nil || auth.TenantScope().ID().IsZero() {
		return RecoveryResult{}, ErrInvalid
	}
	if err := auth.Require(access.PermissionKMSWrite); err != nil {
		return RecoveryResult{}, err
	}

	return service.ExecuteDirect(ctx, auth.Principal().KeyID(), command)
}

// ExecuteDirect applies one ceremony transition for an already-authenticated
// operator actor. The operator CLI uses it directly.
func (service *RecoveryService) ExecuteDirect(ctx context.Context, actor id.APIKey, command RecoveryCommand) (RecoveryResult, error) {
	if service == nil || ctx == nil || actor.IsZero() || validateReason(command.Reason) != nil {
		return RecoveryResult{}, ErrInvalid
	}
	at := service.now().UTC().Truncate(time.Microsecond)
	switch command.Operation {
	case "start":
		return service.start(ctx, actor, command, at)
	case "approve":
		return service.approve(ctx, actor, command, at)
	case "complete":
		return service.complete(ctx, actor, command, at)
	case "abort":
		return service.abort(ctx, actor, command, at)
	default:
		return RecoveryResult{}, ErrInvalid
	}
}

// Read returns one ceremony by identifier.
func (service *RecoveryService) Read(ctx context.Context, auth access.Context, identifier string) (RecoveryResult, error) {
	if service == nil || ctx == nil || identifier == "" {
		return RecoveryResult{}, ErrInvalid
	}
	if err := auth.Require(access.PermissionKMSRead); err != nil {
		return RecoveryResult{}, err
	}

	return service.ReadDirect(ctx, identifier)
}

// ReadDirect returns one ceremony to an already-authenticated operator CLI
// actor. HTTP callers use Read so the permission is always checked.
func (service *RecoveryService) ReadDirect(ctx context.Context, identifier string) (RecoveryResult, error) {
	if service == nil || ctx == nil || identifier == "" {
		return RecoveryResult{}, ErrInvalid
	}
	ceremony, err := service.repository.Load(ctx, identifier)
	if err != nil {
		return RecoveryResult{}, err
	}

	return RecoveryResult{Ceremony: ceremony}, nil
}

func (service *RecoveryService) start(ctx context.Context, actor id.APIKey, command RecoveryCommand, at time.Time) (RecoveryResult, error) {
	if command.ExpectedVersion != 0 || command.Identifier != "" {
		return RecoveryResult{}, ErrInvalid
	}
	if !validRecoveryClass(command.Class) {
		return RecoveryResult{}, ErrInvalid
	}
	var tenantID id.Tenant
	switch command.Kind {
	case RecoveryKindRewrap:
		if command.TenantID == "" {
			return RecoveryResult{}, ErrInvalid
		}
		parsed, err := id.ParseTenant(command.TenantID)
		if err != nil {
			return RecoveryResult{}, ErrInvalid
		}
		tenantID = parsed
	case RecoveryKindMigrateEpoch:
		if command.TenantID != "" || !command.Target.Valid() {
			return RecoveryResult{}, ErrInvalid
		}
	default:
		return RecoveryResult{}, ErrInvalid
	}
	identifier, err := service.identifiers.New(RecoveryIDPrefix)
	if err != nil {
		return RecoveryResult{}, ErrUnavailable
	}
	ceremony := RecoveryCeremony{
		ID: identifier.String(), Kind: command.Kind, Class: command.Class, TenantID: tenantID,
		Target: command.Target, State: RecoveryStarted, Version: 1,
		StartedBy: actor.String(), StartedAt: at, ApproveBy: at.Add(RecoveryApprovalWindow),
		Reason: command.Reason, UpdatedAt: at,
	}
	created, err := service.repository.Create(ctx, ceremony)
	if err != nil {
		return RecoveryResult{}, err
	}

	return RecoveryResult{Ceremony: created}, nil
}

func (service *RecoveryService) approve(ctx context.Context, actor id.APIKey, command RecoveryCommand, at time.Time) (RecoveryResult, error) {
	current, err := service.loadFor(ctx, command)
	if err != nil {
		return RecoveryResult{}, err
	}
	if current.State != RecoveryStarted {
		return RecoveryResult{}, ErrRecoveryState
	}
	if actor.String() == current.StartedBy {
		return RecoveryResult{}, ErrRecoveryForbidden
	}
	if at.After(current.ApproveBy) {
		return RecoveryResult{}, ErrRecoveryExpired
	}
	next := current
	next.State, next.Version = RecoveryApproved, current.Version+1
	next.ApprovedBy, next.ApprovedAt = actor.String(), at
	next.UsableUntil, next.UpdatedAt = at.Add(RecoveryUseWindow), at
	advanced, err := service.repository.Advance(ctx, current, next)
	if err != nil {
		return RecoveryResult{}, err
	}

	return RecoveryResult{Ceremony: advanced}, nil
}

func (service *RecoveryService) complete(ctx context.Context, actor id.APIKey, command RecoveryCommand, at time.Time) (RecoveryResult, error) {
	current, err := service.loadFor(ctx, command)
	if err != nil {
		return RecoveryResult{}, err
	}
	if current.State != RecoveryApproved {
		return RecoveryResult{}, ErrRecoveryState
	}
	if at.After(current.UsableUntil) {
		return RecoveryResult{}, ErrRecoveryExpired
	}
	receipt, err := service.perform(ctx, current)
	if err != nil {
		// The ceremony stays approved: a failed execution changes no state and
		// may be retried inside the validity window.
		return RecoveryResult{}, err
	}
	next := current
	next.State, next.Version = RecoveryCompleted, current.Version+1
	next.CompletedBy, next.CompletedAt, next.Receipt, next.UpdatedAt = actor.String(), at, receipt, at
	advanced, err := service.repository.Advance(ctx, current, next)
	if err != nil {
		return RecoveryResult{}, err
	}

	return RecoveryResult{Ceremony: advanced}, nil
}

func (service *RecoveryService) abort(ctx context.Context, actor id.APIKey, command RecoveryCommand, at time.Time) (RecoveryResult, error) {
	current, err := service.loadFor(ctx, command)
	if err != nil {
		return RecoveryResult{}, err
	}
	if current.State != RecoveryStarted && current.State != RecoveryApproved {
		return RecoveryResult{}, ErrRecoveryState
	}
	next := current
	next.State, next.Version = RecoveryAborted, current.Version+1
	next.AbortedBy, next.AbortedAt, next.AbortReason, next.UpdatedAt = actor.String(), at, command.Reason, at
	advanced, err := service.repository.Advance(ctx, current, next)
	if err != nil {
		return RecoveryResult{}, err
	}

	return RecoveryResult{Ceremony: advanced}, nil
}

func (service *RecoveryService) perform(ctx context.Context, ceremony RecoveryCeremony) (map[string]any, error) {
	switch ceremony.Kind {
	case RecoveryKindRewrap:
		if service.rewrapper == nil || ceremony.TenantID.IsZero() {
			return nil, ErrUnavailable
		}
		rewrapped, err := service.rewrapper.RewrapTenantClass(ctx, ceremony.Class, ceremony.TenantID, 10000)
		if err != nil {
			return nil, err
		}

		return map[string]any{"rewrapped": rewrapped}, nil
	case RecoveryKindMigrateEpoch:
		if service.epochs == nil {
			return nil, ErrUnavailable
		}
		generation, err := service.epochs.AuthorizeEpoch(ctx, ceremony.Class,
			ceremony.Target.Provider, ceremony.Target.Reference, ceremony.Target.Version, ceremony.Target.Algorithm)
		if err != nil {
			return nil, err
		}

		return map[string]any{"generation": generation}, nil
	default:
		return nil, ErrInvalid
	}
}

func (service *RecoveryService) loadFor(ctx context.Context, command RecoveryCommand) (RecoveryCeremony, error) {
	if command.Identifier == "" || command.ExpectedVersion < 1 {
		return RecoveryCeremony{}, ErrInvalid
	}
	current, err := service.repository.Load(ctx, command.Identifier)
	if err != nil {
		return RecoveryCeremony{}, err
	}
	if current.Version != command.ExpectedVersion {
		return RecoveryCeremony{}, ErrConflict
	}

	return current, nil
}

func validRecoveryClass(value string) bool {
	if len(value) < 3 || len(value) > 100 {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
			character != '.' && character != '_' && character != '-' {
			return false
		}
	}

	return value[0] >= 'a' && value[0] <= 'z'
}
