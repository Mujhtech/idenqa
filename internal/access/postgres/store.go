// Package postgres adapts access persistence ports to PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

type transactionRunner interface {
	WithinTransaction(
		context.Context,
		platformpostgres.TransactionOptions,
		func(context.Context, platformpostgres.Transaction) error,
	) error
}

// Store implements tenant-scoped API-key persistence and the narrow
// pre-authentication verification lookup.
type Store struct{ pool transactionRunner }

// New constructs an access PostgreSQL adapter.
func New(pool transactionRunner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("access postgres: pool is required")
	}

	return &Store{pool: pool}, nil
}

// Create persists a key inside its explicit tenant scope.
func (store *Store) Create(ctx context.Context, scope tenant.Scope, key access.Key) error {
	if !sameTenant(scope.ID(), key.TenantID()) {
		return errors.New("access postgres: key tenant does not match scope")
	}

	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set API key tenant scope: %w", err)
		}

		return create(ctx, queries, key)
	})
}

// Find retrieves a key only when both its owner and explicit scope match.
func (store *Store) Find(ctx context.Context, scope tenant.Scope, identifier id.APIKey) (access.Key, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return access.Key{}, access.ErrKeyNotFound
	}

	return store.find(ctx, scope.ID(), identifier)
}

// FindForVerification performs the deliberately narrow pre-authentication
// lookup. The tenant value remains an untrusted RLS hint and is paired with the
// key identifier in the query.
func (store *Store) FindForVerification(
	ctx context.Context,
	tenantHint id.Tenant,
	identifier id.APIKey,
) (access.VerificationRecord, error) {
	if tenantHint.IsZero() || identifier.IsZero() {
		return access.VerificationRecord{}, access.ErrKeyNotFound
	}

	var found access.VerificationRecord
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, tenantHint.String()); err != nil {
			return fmt.Errorf("set API key tenant scope: %w", err)
		}
		row, err := queries.FindAPIKeyForVerification(ctx, sqlgen.FindAPIKeyForVerificationParams{
			TenantID: tenantHint.String(),
			ID:       identifier.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("find API key for verification: %w", err)
		}
		key, err := restore(row.IdenqaApiKey)
		if err != nil {
			return err
		}
		found, err = access.NewVerificationRecord(key, tenant.State(row.TenantState))

		return err
	})

	return found, err
}

// SaveLifecycle applies exactly one terminal transition with optimistic concurrency.
func (store *Store) SaveLifecycle(ctx context.Context, scope tenant.Scope, key access.Key, expectedVersion int64) error {
	if !sameTenant(scope.ID(), key.TenantID()) || !validLifecycleChange(key, expectedVersion) {
		return access.ErrKeyConflict
	}

	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set API key tenant scope: %w", err)
		}

		return saveLifecycle(ctx, queries, key, expectedVersion)
	})
}

// Rotate atomically creates a successor and schedules its predecessor's retirement.
func (store *Store) Rotate(
	ctx context.Context,
	scope tenant.Scope,
	predecessor access.Key,
	expectedVersion int64,
	successor access.Key,
) error {
	if !sameTenant(scope.ID(), predecessor.TenantID()) || !sameTenant(scope.ID(), successor.TenantID()) ||
		successor.ReplacesID().String() != predecessor.ID().String() || !validLifecycleChange(predecessor, expectedVersion) ||
		predecessor.RevokedAt() != nil || predecessor.RetiredAt() == nil || successor.Version() != 1 ||
		successor.RevokedAt() != nil || successor.RetiredAt() != nil {
		return access.ErrKeyConflict
	}

	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set API key tenant scope: %w", err)
		}
		if err := create(ctx, queries, successor); err != nil {
			return err
		}

		return saveLifecycle(ctx, queries, predecessor, expectedVersion)
	})
}

// FindAdministrative checks the privileged role before reading key metadata
// used by a later administrative transition.
func (store *Store) FindAdministrative(
	ctx context.Context,
	scope tenant.Scope,
	identifier id.APIKey,
) (access.Key, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return access.Key{}, access.ErrKeyNotFound
	}

	var found access.Key
	err := store.adminTransaction(ctx, func(ctx context.Context, queries *sqlgen.Queries) error {
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set API key tenant scope: %w", err)
		}
		row, err := queries.FindAPIKey(ctx, sqlgen.FindAPIKeyParams{
			TenantID: scope.ID().String(),
			ID:       identifier.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("find API key administratively: %w", err)
		}
		found, err = restore(row)

		return err
	})

	return found, err
}

// CreateAdministrative persists a key and its privileged audit record atomically.
func (store *Store) CreateAdministrative(
	ctx context.Context,
	action access.AdminAction,
	scope tenant.Scope,
	key access.Key,
) error {
	if !sameTenant(scope.ID(), key.TenantID()) {
		return errors.New("access postgres: key tenant does not match scope")
	}

	return store.adminTransaction(ctx, func(ctx context.Context, queries *sqlgen.Queries) error {
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set API key tenant scope: %w", err)
		}
		if err := create(ctx, queries, key); err != nil {
			return err
		}

		return insertAdminAudit(ctx, queries, action, scope.ID(), key.ID(), "issue")
	})
}

// ListAdministrative returns secret-free tenant key metadata and records the
// privileged read in the same transaction.
func (store *Store) ListAdministrative(
	ctx context.Context,
	action access.AdminAction,
	scope tenant.Scope,
) ([]access.Key, error) {
	if scope.ID().IsZero() {
		return nil, access.ErrKeyNotFound
	}

	var keys []access.Key
	err := store.adminTransaction(ctx, func(ctx context.Context, queries *sqlgen.Queries) error {
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set API key tenant scope: %w", err)
		}
		rows, err := queries.ListAPIKeys(ctx, scope.ID().String())
		if err != nil {
			return fmt.Errorf("list API keys: %w", err)
		}
		keys = make([]access.Key, 0, len(rows))
		for _, row := range rows {
			key, err := restore(row)
			if err != nil {
				return err
			}
			keys = append(keys, key)
		}

		return insertAdminAudit(ctx, queries, action, scope.ID(), id.APIKey{}, "list")
	})

	return keys, err
}

// SaveLifecycleAdministrative applies one lifecycle transition and records the
// privileged action atomically.
func (store *Store) SaveLifecycleAdministrative(
	ctx context.Context,
	action access.AdminAction,
	scope tenant.Scope,
	key access.Key,
	expectedVersion int64,
) error {
	if !sameTenant(scope.ID(), key.TenantID()) || !validLifecycleChange(key, expectedVersion) {
		return access.ErrKeyConflict
	}

	return store.adminTransaction(ctx, func(ctx context.Context, queries *sqlgen.Queries) error {
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set API key tenant scope: %w", err)
		}
		if err := saveLifecycle(ctx, queries, key, expectedVersion); err != nil {
			return err
		}

		return insertAdminAudit(ctx, queries, action, scope.ID(), key.ID(), "revoke")
	})
}

// RotateAdministrative creates the successor, schedules the predecessor, and
// records the privileged action atomically.
func (store *Store) RotateAdministrative(
	ctx context.Context,
	action access.AdminAction,
	scope tenant.Scope,
	predecessor access.Key,
	expectedVersion int64,
	successor access.Key,
) error {
	if !sameTenant(scope.ID(), predecessor.TenantID()) || !sameTenant(scope.ID(), successor.TenantID()) ||
		successor.ReplacesID().String() != predecessor.ID().String() ||
		!validLifecycleChange(predecessor, expectedVersion) || predecessor.RevokedAt() != nil ||
		predecessor.RetiredAt() == nil || successor.Version() != 1 || successor.RevokedAt() != nil ||
		successor.RetiredAt() != nil {
		return access.ErrKeyConflict
	}

	return store.adminTransaction(ctx, func(ctx context.Context, queries *sqlgen.Queries) error {
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set API key tenant scope: %w", err)
		}
		if err := create(ctx, queries, successor); err != nil {
			return err
		}
		if err := saveLifecycle(ctx, queries, predecessor, expectedVersion); err != nil {
			return err
		}

		return insertAdminAudit(ctx, queries, action, scope.ID(), predecessor.ID(), "rotate")
	})
}

func (store *Store) adminTransaction(
	ctx context.Context,
	work func(context.Context, *sqlgen.Queries) error,
) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(
		ctx context.Context,
		tx platformpostgres.Transaction,
	) error {
		if err := requireAdminPrivilege(ctx, tx); err != nil {
			return err
		}

		return work(ctx, sqlgen.New(tx))
	})
}

func (store *Store) find(ctx context.Context, tenantID id.Tenant, identifier id.APIKey) (access.Key, error) {
	var found access.Key
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, tenantID.String()); err != nil {
			return fmt.Errorf("set API key tenant scope: %w", err)
		}
		row, err := queries.FindAPIKey(ctx, sqlgen.FindAPIKeyParams{TenantID: tenantID.String(), ID: identifier.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrKeyNotFound
		}
		if err != nil {
			return fmt.Errorf("find API key: %w", err)
		}
		found, err = restore(row)

		return err
	})

	return found, err
}

func restore(row sqlgen.IdenqaApiKey) (access.Key, error) {
	identifier, err := id.ParseAPIKey(row.ID)
	if err != nil {
		return access.Key{}, fmt.Errorf("parse stored API key id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return access.Key{}, fmt.Errorf("parse stored API key tenant id: %w", err)
	}
	digest, err := access.ParseDigest(row.Digest)
	if err != nil {
		return access.Key{}, fmt.Errorf("parse stored API key digest: %w", err)
	}
	if row.PepperVersion < 1 || row.PepperVersion > 65535 {
		return access.Key{}, errors.New("stored API key pepper version is invalid")
	}
	pepperVersion := access.PepperVersion(row.PepperVersion)
	patterns := make([]access.Pattern, len(row.RequestedScopes))
	for index, value := range row.RequestedScopes {
		patterns[index] = access.Pattern(value)
	}
	permissions := make([]access.Permission, len(row.ResolvedScopes))
	for index, value := range row.ResolvedScopes {
		permissions[index] = access.Permission(value)
	}
	grant, err := access.RestoreGrant(patterns, permissions)
	if err != nil {
		return access.Key{}, fmt.Errorf("parse stored API key grant: %w", err)
	}
	var replacesID id.APIKey
	if row.ReplacesKeyID != nil {
		replacesID, err = id.ParseAPIKey(*row.ReplacesKeyID)
		if err != nil {
			return access.Key{}, fmt.Errorf("parse replaced API key id: %w", err)
		}
	}

	return access.RestoreKey(access.KeyRecord{
		ID: identifier, TenantID: tenantID, Label: row.Label, Digest: digest,
		PepperVersion: pepperVersion, Grant: grant, Version: row.Version,
		CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		ExpiresAt: timestampPointer(row.ExpiresAt), RevokedAt: timestampPointer(row.RevokedAt),
		RetiredAt: timestampPointer(row.RetiredAt), ReplacesID: replacesID,
	})
}

func create(ctx context.Context, queries *sqlgen.Queries, key access.Key) error {
	grant := key.Grant()
	created, err := queries.CreateAPIKey(ctx, sqlgen.CreateAPIKeyParams{
		ID: key.ID().String(), TenantID: key.TenantID().String(), Label: key.Label(),
		Digest: key.Digest().Bytes(), PepperVersion: int32(key.PepperVersion()),
		RequestedScopes: patternStrings(grant.Patterns()), ResolvedScopes: permissionStrings(grant.Permissions()),
		Version: key.Version(), CreatedAt: timestamp(key.CreatedAt()), UpdatedAt: timestamp(key.UpdatedAt()),
		ExpiresAt: optionalTimestamp(key.ExpiresAt()), RevokedAt: optionalTimestamp(key.RevokedAt()),
		RetiredAt: optionalTimestamp(key.RetiredAt()), ReplacesKeyID: optionalKeyID(key.ReplacesID()),
	})
	if err != nil {
		return fmt.Errorf("insert API key: %w", err)
	}
	if created != 1 {
		return access.ErrTenantInactive
	}

	return nil
}

func saveLifecycle(ctx context.Context, queries *sqlgen.Queries, key access.Key, expectedVersion int64) error {
	updated, err := queries.SaveAPIKeyLifecycle(ctx, sqlgen.SaveAPIKeyLifecycleParams{
		TenantID: key.TenantID().String(), ID: key.ID().String(), ExpectedVersion: expectedVersion,
		NewVersion: key.Version(), UpdatedAt: timestamp(key.UpdatedAt()),
		RevokedAt: optionalTimestamp(key.RevokedAt()), RetiredAt: optionalTimestamp(key.RetiredAt()),
	})
	if err != nil {
		return fmt.Errorf("update API key lifecycle: %w", err)
	}
	if updated != 1 {
		return access.ErrKeyConflict
	}

	return nil
}

func validLifecycleChange(key access.Key, expectedVersion int64) bool {
	return expectedVersion >= 1 && key.Version() == expectedVersion+1 &&
		(key.RevokedAt() != nil || key.RetiredAt() != nil)
}

func sameTenant(first, second id.Tenant) bool {
	return !first.IsZero() && !second.IsZero() && first.String() == second.String()
}

func patternStrings(values []access.Pattern) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}

	return result
}

func permissionStrings(values []access.Permission) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}

	return result
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: !value.IsZero()}
}

func optionalTimestamp(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}

	return pgtype.Timestamptz{Time: value.UTC(), Valid: true}
}

func timestampPointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	timestamp := value.Time.UTC()

	return &timestamp
}

func optionalKeyID(identifier id.APIKey) *string {
	if identifier.IsZero() {
		return nil
	}
	value := identifier.String()

	return &value
}

func requireAdminPrivilege(ctx context.Context, tx platformpostgres.Transaction) error {
	var allowed bool
	if err := tx.QueryRow(
		ctx,
		`SELECT rolsuper OR rolbypassrls FROM pg_catalog.pg_roles WHERE rolname = current_user`,
	).Scan(&allowed); err != nil {
		return fmt.Errorf("check API key admin privilege: %w", err)
	}
	if !allowed {
		return errors.New("access postgres: administrative database role is required")
	}

	return nil
}

func insertAdminAudit(
	ctx context.Context,
	queries *sqlgen.Queries,
	action access.AdminAction,
	tenantID id.Tenant,
	keyID id.APIKey,
	operation string,
) error {
	if err := action.Validate(); err != nil || action.OccurredAt().IsZero() {
		return errors.New("access postgres: administrative action is invalid")
	}
	if err := queries.InsertAPIKeyAdminAudit(ctx, sqlgen.InsertAPIKeyAdminAuditParams{
		TenantID: tenantID.String(), KeyID: optionalKeyID(keyID), Action: operation,
		Actor: action.Actor, Reason: action.Reason, OccurredAt: timestamp(action.OccurredAt()),
	}); err != nil {
		return fmt.Errorf("insert API key admin audit: %w", err)
	}

	return nil
}

var _ access.Repository = (*Store)(nil)
var _ access.VerificationRepository = (*Store)(nil)
var _ access.AdminRepository = (*Store)(nil)
