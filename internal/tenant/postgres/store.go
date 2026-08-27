// Package postgres adapts tenant application ports to PostgreSQL.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

var errAdminPrivilege = errors.New("tenant postgres: administrative database role is required")

type transactionRunner interface {
	WithinTransaction(
		context.Context,
		platformpostgres.TransactionOptions,
		func(context.Context, platformpostgres.Transaction) error,
	) error
}

// Store implements scoped and explicitly privileged tenant persistence.
type Store struct{ pool transactionRunner }

// New constructs a tenant PostgreSQL adapter.
func New(pool transactionRunner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("tenant postgres: pool is required")
	}

	return &Store{pool: pool}, nil
}

// Find retrieves only a resource belonging to the explicit scope.
func (store *Store) Find(ctx context.Context, scope tenant.Scope, identifier id.Tenant) (tenant.Tenant, error) {
	if scope.ID().IsZero() || identifier.IsZero() || scope.ID().String() != identifier.String() {
		return tenant.Tenant{}, tenant.ErrNotFound
	}

	var found tenant.Tenant
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set tenant scope: %w", err)
		}
		row, err := queries.FindTenant(ctx, identifier.String())
		if errors.Is(err, pgx.ErrNoRows) {
			return tenant.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("find tenant: %w", err)
		}
		found, err = restore(row)

		return err
	})

	return found, err
}

// Create persists a tenant and its administrative audit record atomically.
func (store *Store) Create(ctx context.Context, action tenant.AdminAction, value tenant.Tenant) error {
	return store.adminTransaction(ctx, func(ctx context.Context, queries *sqlgen.Queries) error {
		if err := queries.CreateTenant(ctx, sqlgen.CreateTenantParams{
			ID: value.ID().String(), State: string(value.State()), Version: value.Version(),
			CreatedAt: timestamp(value.CreatedAt()), UpdatedAt: timestamp(value.UpdatedAt()),
		}); err != nil {
			return fmt.Errorf("insert tenant: %w", err)
		}

		return insertAudit(ctx, queries, action, value.ID(), "create")
	})
}

// Inspect retrieves a tenant and records the successful privileged read.
func (store *Store) Inspect(ctx context.Context, action tenant.AdminAction, identifier id.Tenant) (tenant.Tenant, error) {
	var found tenant.Tenant
	err := store.adminTransaction(ctx, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindTenant(ctx, identifier.String())
		if errors.Is(err, pgx.ErrNoRows) {
			return tenant.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("inspect tenant: %w", err)
		}
		found, err = restore(row)
		if err != nil {
			return err
		}

		return insertAudit(ctx, queries, action, identifier, "inspect")
	})

	return found, err
}

// Disable performs an optimistic transition and records it atomically.
func (store *Store) Disable(ctx context.Context, action tenant.AdminAction, identifier id.Tenant, expectedVersion int64, now time.Time) (tenant.Tenant, error) {
	var disabled tenant.Tenant
	err := store.adminTransaction(ctx, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.DisableTenant(ctx, sqlgen.DisableTenantParams{ID: identifier.String(), Version: expectedVersion, UpdatedAt: timestamp(now)})
		if errors.Is(err, pgx.ErrNoRows) {
			return tenant.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("disable tenant: %w", err)
		}
		disabled, err = restore(row)
		if err != nil {
			return err
		}

		return insertAudit(ctx, queries, action, identifier, "disable")
	})

	return disabled, err
}

func (store *Store) adminTransaction(ctx context.Context, work func(context.Context, *sqlgen.Queries) error) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		var allowed bool
		err := tx.QueryRow(ctx, `SELECT rolsuper OR rolbypassrls FROM pg_catalog.pg_roles WHERE rolname = current_user`).Scan(&allowed)
		if err != nil {
			return fmt.Errorf("check tenant admin privilege: %w", err)
		}
		if !allowed {
			return errAdminPrivilege
		}

		return work(ctx, queries)
	})
}

func insertAudit(ctx context.Context, queries *sqlgen.Queries, action tenant.AdminAction, identifier id.Tenant, operation string) error {
	if err := queries.InsertTenantAdminAudit(ctx, sqlgen.InsertTenantAdminAuditParams{
		TenantID: identifier.String(), Action: operation, Actor: action.Actor,
		Reason: action.Reason, OccurredAt: timestamp(action.OccurredAt()),
	}); err != nil {
		return fmt.Errorf("insert tenant admin audit: %w", err)
	}

	return nil
}

func restore(row sqlgen.IdenqaTenant) (tenant.Tenant, error) {
	identifier, err := id.ParseTenant(row.ID)
	if err != nil {
		return tenant.Tenant{}, fmt.Errorf("parse stored tenant id: %w", err)
	}
	var disabledAt *time.Time
	if row.DisabledAt.Valid {
		disabledAt = &row.DisabledAt.Time
	}

	return tenant.Restore(identifier, tenant.State(row.State), row.Version, row.CreatedAt.Time, row.UpdatedAt.Time, disabledAt)
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}

var _ tenant.Repository = (*Store)(nil)
var _ tenant.AdminRepository = (*Store)(nil)
