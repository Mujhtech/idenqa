package access

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// AdminAction describes an explicitly attributed privileged API-key operation.
type AdminAction struct {
	Actor      string
	Reason     string
	occurredAt time.Time
}

// OccurredAt returns the authoritative operation time assigned by the service.
func (action AdminAction) OccurredAt() time.Time { return action.occurredAt }

// Validate checks bounded audit metadata supplied at the CLI boundary.
func (action AdminAction) Validate() error {
	if err := validateAdminText(action.Actor, 200); err != nil {
		return errors.New("API key admin actor is invalid")
	}
	if err := validateAdminText(action.Reason, 500); err != nil {
		return errors.New("API key admin reason is invalid")
	}

	return nil
}

// AdminRepository is the privileged persistence boundary. Every successful
// bypass operation must append its audit record in the same transaction.
type AdminRepository interface {
	Repository
	FindAdministrative(context.Context, tenant.Scope, id.APIKey) (Key, error)
	CreateAdministrative(context.Context, AdminAction, tenant.Scope, Key) error
	ListAdministrative(context.Context, AdminAction, tenant.Scope) ([]Key, error)
	SaveLifecycleAdministrative(context.Context, AdminAction, tenant.Scope, Key, int64) error
	RotateAdministrative(context.Context, AdminAction, tenant.Scope, Key, int64, Key) error
}

// AdministrativeIssuer applies privileged attribution while delegating key
// construction and policy enforcement to the ordinary Issuer service.
type AdministrativeIssuer struct {
	issuer     *Issuer
	repository AdminRepository
}

// NewAdministrativeIssuer constructs the audited issuance boundary.
func NewAdministrativeIssuer(issuer *Issuer, repository AdminRepository) (*AdministrativeIssuer, error) {
	if issuer == nil || issuer.clock == nil || repository == nil {
		return nil, errors.New("administrative API key issuer dependencies are required")
	}

	return &AdministrativeIssuer{issuer: issuer, repository: repository}, nil
}

// Issue creates a key and its audit record atomically.
func (administrator *AdministrativeIssuer) Issue(
	ctx context.Context,
	action AdminAction,
	scope tenant.Scope,
	input IssueInput,
) (IssuedKey, error) {
	if administrator == nil || administrator.issuer == nil || administrator.repository == nil {
		return IssuedKey{}, errors.New("administrative API key issuer is not initialised")
	}
	prepared, err := prepareAdminAction(action, administrator.issuer.clock.Now())
	if err != nil {
		return IssuedKey{}, err
	}
	issuer := *administrator.issuer
	issuer.repository = administrativeIssuanceRepository{
		repository: administrator.repository,
		action:     prepared,
	}

	return issuer.Issue(ctx, scope, input)
}

// Rotate creates a successor, schedules its predecessor, and appends one audit
// record in the same transaction.
func (administrator *AdministrativeIssuer) Rotate(
	ctx context.Context,
	action AdminAction,
	scope tenant.Scope,
	input RotateInput,
) (IssuedKey, error) {
	if administrator == nil || administrator.issuer == nil || administrator.repository == nil {
		return IssuedKey{}, errors.New("administrative API key issuer is not initialised")
	}
	prepared, err := prepareAdminAction(action, administrator.issuer.clock.Now())
	if err != nil {
		return IssuedKey{}, err
	}
	issuer := *administrator.issuer
	issuer.repository = administrativeIssuanceRepository{
		repository: administrator.repository,
		action:     prepared,
	}

	return issuer.Rotate(ctx, scope, input)
}

// Administrator lists and revokes keys through atomically audited persistence.
type Administrator struct {
	repository AdminRepository
	clock      clock.Clock
}

// NewAdministrator constructs privileged API-key lifecycle administration.
func NewAdministrator(repository AdminRepository, source clock.Clock) (*Administrator, error) {
	if repository == nil || source == nil {
		return nil, errors.New("API key administrator dependencies are required")
	}

	return &Administrator{repository: repository, clock: source}, nil
}

// List returns secret-free key metadata and records the privileged read.
func (administrator *Administrator) List(
	ctx context.Context,
	action AdminAction,
	scope tenant.Scope,
) ([]Key, error) {
	if administrator == nil || administrator.repository == nil || administrator.clock == nil {
		return nil, errors.New("API key administrator is not initialised")
	}
	prepared, err := prepareAdminAction(action, administrator.clock.Now())
	if err != nil {
		return nil, err
	}

	return administrator.repository.ListAdministrative(ctx, prepared, scope)
}

// Revoke irreversibly revokes the expected key version and records the action
// atomically. The preliminary scoped read does not itself confer authority.
func (administrator *Administrator) Revoke(
	ctx context.Context,
	action AdminAction,
	scope tenant.Scope,
	identifier id.APIKey,
	expectedVersion int64,
) (Key, error) {
	if administrator == nil || administrator.repository == nil || administrator.clock == nil {
		return Key{}, errors.New("API key administrator is not initialised")
	}
	if identifier.IsZero() || expectedVersion < 1 {
		return Key{}, ErrKeyConflict
	}
	now := administrator.clock.Now().UTC()
	prepared, err := prepareAdminAction(action, now)
	if err != nil {
		return Key{}, err
	}
	key, err := administrator.repository.FindAdministrative(ctx, scope, identifier)
	if err != nil {
		return Key{}, err
	}
	if key.Version() != expectedVersion {
		return Key{}, ErrKeyConflict
	}
	if err := key.Revoke(now); err != nil {
		return Key{}, err
	}
	if err := administrator.repository.SaveLifecycleAdministrative(
		ctx,
		prepared,
		scope,
		key,
		expectedVersion,
	); err != nil {
		return Key{}, err
	}

	return key, nil
}

type administrativeIssuanceRepository struct {
	repository AdminRepository
	action     AdminAction
}

func (repository administrativeIssuanceRepository) Create(
	ctx context.Context,
	scope tenant.Scope,
	key Key,
) error {
	return repository.repository.CreateAdministrative(ctx, repository.action, scope, key)
}

func (repository administrativeIssuanceRepository) Find(
	ctx context.Context,
	scope tenant.Scope,
	identifier id.APIKey,
) (Key, error) {
	return repository.repository.FindAdministrative(ctx, scope, identifier)
}

func (repository administrativeIssuanceRepository) Rotate(
	ctx context.Context,
	scope tenant.Scope,
	predecessor Key,
	expectedVersion int64,
	successor Key,
) error {
	return repository.repository.RotateAdministrative(
		ctx,
		repository.action,
		scope,
		predecessor,
		expectedVersion,
		successor,
	)
}

func prepareAdminAction(action AdminAction, now time.Time) (AdminAction, error) {
	if err := action.Validate(); err != nil {
		return AdminAction{}, err
	}
	action.occurredAt = now.UTC()
	if action.occurredAt.IsZero() {
		return AdminAction{}, errors.New("API key admin occurrence time is required")
	}

	return action, nil
}

func validateAdminText(value string, maximum int) error {
	if strings.TrimSpace(value) != value || value == "" || !utf8.ValidString(value) ||
		utf8.RuneCountInString(value) > maximum {
		return errors.New("invalid administrative text")
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return errors.New("administrative text contains a control character")
		}
	}

	return nil
}
