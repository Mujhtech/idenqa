package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// AppendRevision atomically creates a policy root and appends one immutable revision.
func (store *Store) AppendRevision(
	ctx context.Context,
	scope tenant.Scope,
	revision policy.Revision,
) error {
	reference := revision.Reference()
	if scope.ID().IsZero() || reference.ID.IsZero() || reference.Revision == 0 {
		return policy.ErrRevisionConflict
	}
	if _, err := policy.RestoreRevision(
		reference, revision.Evaluator(), revision.Canonical(), revision.CreatedAt(),
	); err != nil {
		return policy.ErrRevisionConflict
	}

	return store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		if _, err := queries.InsertPolicy(ctx, sqlgen.InsertPolicyParams{
			TenantID: scope.ID().String(), PolicyID: reference.ID.String(),
			CreatedAt: timestamp(revision.CreatedAt()),
		}); err != nil {
			return classifyCatalogWrite("insert policy", policy.ErrRevisionConflict, err)
		}
		rows, err := queries.InsertPolicyRevision(ctx, revisionParams(scope, revision))
		if err != nil {
			return classifyCatalogWrite("insert policy revision", policy.ErrRevisionConflict, err)
		}
		if rows == 1 {
			return nil
		}
		stored, err := queries.FindPolicyRevision(ctx, sqlgen.FindPolicyRevisionParams{
			TenantID: scope.ID().String(), PolicyID: reference.ID.String(),
			Revision: int64(reference.Revision),
		})
		if err != nil {
			return policy.ErrRevisionConflict
		}
		restored, err := restoreRevision(stored)
		if err != nil || restored.Reference() != reference ||
			restored.Evaluator() != revision.Evaluator() ||
			!restored.CreatedAt().Equal(revision.CreatedAt()) ||
			!bytes.Equal(restored.Canonical(), revision.Canonical()) {
			return policy.ErrRevisionConflict
		}
		return nil
	})
}

// FindRevision restores one exact immutable revision in the tenant scope.
func (store *Store) FindRevision(
	ctx context.Context,
	scope tenant.Scope,
	policyID id.Policy,
	revision uint32,
) (policy.Revision, error) {
	if scope.ID().IsZero() || policyID.IsZero() || revision == 0 {
		return policy.Revision{}, policy.ErrRevisionNotFound
	}
	var found policy.Revision
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindPolicyRevision(ctx, sqlgen.FindPolicyRevisionParams{
			TenantID: scope.ID().String(), PolicyID: policyID.String(), Revision: int64(revision),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return policy.ErrRevisionNotFound
		}
		if err != nil {
			return fmt.Errorf("find policy revision: %w", err)
		}
		found, err = restoreRevision(row)
		return err
	})
	return found, err
}

// Activate atomically compare-and-swaps the active pointer and appends history.
func (store *Store) Activate(
	ctx context.Context,
	scope tenant.Scope,
	policyID id.Policy,
	revision uint32,
	expectedVersion int64,
	actor id.APIKey,
	activatedAt time.Time,
) (policy.Activation, error) {
	if scope.ID().IsZero() || policyID.IsZero() || revision == 0 || expectedVersion < 0 ||
		actor.IsZero() || activatedAt.IsZero() || activatedAt.Location() != time.UTC {
		return policy.Activation{}, policy.ErrActivationConflict
	}

	var activation policy.Activation
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		revisionRow, err := queries.FindPolicyRevision(ctx, sqlgen.FindPolicyRevisionParams{
			TenantID: scope.ID().String(), PolicyID: policyID.String(), Revision: int64(revision),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return policy.ErrRevisionNotFound
		}
		if err != nil {
			return fmt.Errorf("find policy revision for activation: %w", err)
		}
		storedRevision, err := restoreRevision(revisionRow)
		if err != nil {
			return err
		}
		root, err := queries.LockPolicy(ctx, sqlgen.LockPolicyParams{
			TenantID: scope.ID().String(), PolicyID: policyID.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return policy.ErrRevisionNotFound
		}
		if err != nil {
			return fmt.Errorf("lock policy activation: %w", err)
		}
		if root.ActivationVersion != expectedVersion || !root.UpdatedAt.Valid ||
			activatedAt.Before(root.UpdatedAt.Time) || activatedAt.Before(storedRevision.CreatedAt()) {
			return policy.ErrActivationConflict
		}
		previous := uint32(0)
		var previousDatabase *int64
		if root.ActiveRevision != nil {
			if *root.ActiveRevision < 1 || *root.ActiveRevision > math.MaxUint32 ||
				uint32(*root.ActiveRevision) == revision {
				return policy.ErrActivationConflict
			}
			previous = uint32(*root.ActiveRevision)
			previousValue := *root.ActiveRevision
			previousDatabase = &previousValue
		}
		newVersion := expectedVersion + 1
		if newVersion < 1 {
			return policy.ErrActivationConflict
		}
		if _, err := queries.ActivatePolicy(ctx, sqlgen.ActivatePolicyParams{
			NewVersion: newVersion, Revision: int64(revision), ActivatedAt: timestamp(activatedAt),
			TenantID: scope.ID().String(), PolicyID: policyID.String(), ExpectedVersion: expectedVersion,
		}); errors.Is(err, pgx.ErrNoRows) {
			return policy.ErrActivationConflict
		} else if err != nil {
			return classifyCatalogWrite("activate policy", policy.ErrActivationConflict, err)
		}
		if err := queries.InsertPolicyActivation(ctx, sqlgen.InsertPolicyActivationParams{
			TenantID: scope.ID().String(), PolicyID: policyID.String(),
			ActivationVersion: newVersion, Revision: int64(revision),
			PreviousRevision: previousDatabase, ActorKeyID: actor.String(),
			ActivatedAt: timestamp(activatedAt),
		}); err != nil {
			return classifyCatalogWrite("insert policy activation", policy.ErrActivationConflict, err)
		}
		activation, err = policy.RestoreActivation(
			storedRevision, newVersion, previous, actor, activatedAt,
		)
		return err
	})
	return activation, err
}

// FindActive restores the current activation and exact immutable revision.
func (store *Store) FindActive(
	ctx context.Context,
	scope tenant.Scope,
	policyID id.Policy,
) (policy.Activation, error) {
	if scope.ID().IsZero() || policyID.IsZero() {
		return policy.Activation{}, policy.ErrActivationNotFound
	}
	var activation policy.Activation
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindActivePolicy(ctx, sqlgen.FindActivePolicyParams{
			TenantID: scope.ID().String(), PolicyID: policyID.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return policy.ErrActivationNotFound
		}
		if err != nil {
			return fmt.Errorf("find active policy: %w", err)
		}
		revision, err := restoreActiveRevision(row)
		if err != nil {
			return err
		}
		actor, err := id.ParseAPIKey(row.ActorKeyID)
		if err != nil || !row.ActivatedAt.Valid {
			return policy.ErrActivationConflict
		}
		previous, err := optionalRevision(row.PreviousRevision)
		if err != nil {
			return err
		}
		activation, err = policy.RestoreActivation(
			revision, row.ActivationVersion, previous, actor, row.ActivatedAt.Time.UTC(),
		)
		return err
	})
	return activation, err
}

func revisionParams(scope tenant.Scope, revision policy.Revision) sqlgen.InsertPolicyRevisionParams {
	reference, evaluator := revision.Reference(), revision.Evaluator()
	return sqlgen.InsertPolicyRevisionParams{
		TenantID: scope.ID().String(), PolicyID: reference.ID.String(), Revision: int64(reference.Revision),
		SchemaMajor: int32(reference.SchemaMajor), SchemaMinor: int32(reference.SchemaMinor),
		Digest: reference.Digest, EvaluatorMajor: int32(evaluator.Major),
		EvaluatorMinor: int32(evaluator.Minor), EvaluatorDigest: evaluator.Digest,
		Canonical: string(revision.Canonical()), CreatedAt: timestamp(revision.CreatedAt()),
	}
}

func restoreRevision(row sqlgen.IdenqaPolicyRevision) (policy.Revision, error) {
	policyID, err := id.ParsePolicy(row.PolicyID)
	if err != nil || row.Revision < 1 || row.Revision > math.MaxUint32 ||
		row.SchemaMajor < 0 || row.SchemaMajor > math.MaxUint16 ||
		row.SchemaMinor < 0 || row.SchemaMinor > math.MaxUint16 ||
		row.EvaluatorMajor < 0 || row.EvaluatorMajor > math.MaxUint16 ||
		row.EvaluatorMinor < 0 || row.EvaluatorMinor > math.MaxUint16 || !row.CreatedAt.Valid {
		return policy.Revision{}, policy.ErrRevisionConflict
	}
	return policy.RestoreRevision(
		policy.Reference{
			ID: policyID, Revision: uint32(row.Revision),
			SchemaMajor: uint16(row.SchemaMajor), SchemaMinor: uint16(row.SchemaMinor),
			Digest: row.Digest,
		},
		policy.EvaluatorReference{
			Major: uint16(row.EvaluatorMajor), Minor: uint16(row.EvaluatorMinor),
			Digest: row.EvaluatorDigest,
		},
		[]byte(row.Canonical), row.CreatedAt.Time.UTC(),
	)
}

func restoreActiveRevision(row sqlgen.FindActivePolicyRow) (policy.Revision, error) {
	return restoreRevision(sqlgen.IdenqaPolicyRevision{
		TenantID: row.TenantID, PolicyID: row.PolicyID, Revision: row.Revision,
		SchemaMajor: row.SchemaMajor, SchemaMinor: row.SchemaMinor, Digest: row.Digest,
		EvaluatorMajor: row.EvaluatorMajor, EvaluatorMinor: row.EvaluatorMinor,
		EvaluatorDigest: row.EvaluatorDigest, Canonical: row.Canonical, CreatedAt: row.CreatedAt,
	})
}

func optionalRevision(value *int64) (uint32, error) {
	if value == nil {
		return 0, nil
	}
	if *value < 1 || *value > math.MaxUint32 {
		return 0, policy.ErrActivationConflict
	}
	return uint32(*value), nil
}

func classifyCatalogWrite(operation string, conflict error, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && len(postgresError.Code) >= 2 && postgresError.Code[:2] == "23" {
		return conflict
	}
	return fmt.Errorf("%s: %w", operation, err)
}
