package postgres

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ListRevisionMetadata returns newest-first metadata without loading canonical
// policy bytes. before is an exclusive revision boundary; zero starts latest.
func (store *Store) ListRevisionMetadata(
	ctx context.Context,
	scope tenant.Scope,
	policyID id.Policy,
	before uint32,
	limit int,
) ([]policy.RevisionMetadata, error) {
	if scope.ID().IsZero() || policyID.IsZero() || limit < 1 ||
		limit > policy.MaximumCatalogPageSize+1 {
		return nil, policy.ErrRevisionConflict
	}
	var metadata []policy.RevisionMetadata
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		rows, err := queries.ListPolicyRevisionMetadata(
			ctx,
			sqlgen.ListPolicyRevisionMetadataParams{
				TenantID: scope.ID().String(), PolicyID: policyID.String(),
				BeforeRevision: int64(before), PageLimit: catalogPageLimit(limit),
			},
		)
		if err != nil {
			return fmt.Errorf("list policy revision metadata: %w", err)
		}
		metadata = make([]policy.RevisionMetadata, len(rows))
		for index, row := range rows {
			metadata[index], err = restoreRevisionMetadata(row)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return metadata, err
}

// ListActivationMetadata returns newest-first immutable switch history without
// loading canonical policy bytes. before is an exclusive version boundary.
func (store *Store) ListActivationMetadata(
	ctx context.Context,
	scope tenant.Scope,
	policyID id.Policy,
	before int64,
	limit int,
) ([]policy.ActivationMetadata, error) {
	if scope.ID().IsZero() || policyID.IsZero() || before < 0 || limit < 1 ||
		limit > policy.MaximumCatalogPageSize+1 {
		return nil, policy.ErrActivationConflict
	}
	var metadata []policy.ActivationMetadata
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		rows, err := queries.ListPolicyActivationMetadata(
			ctx,
			sqlgen.ListPolicyActivationMetadataParams{
				TenantID: scope.ID().String(), PolicyID: policyID.String(),
				BeforeActivationVersion: before, PageLimit: catalogPageLimit(limit),
			},
		)
		if err != nil {
			return fmt.Errorf("list policy activation metadata: %w", err)
		}
		metadata = make([]policy.ActivationMetadata, len(rows))
		for index, row := range rows {
			metadata[index], err = restoreActivationMetadata(row)
			if err != nil {
				return err
			}
		}
		return nil
	})
	return metadata, err
}

func restoreRevisionMetadata(
	row sqlgen.ListPolicyRevisionMetadataRow,
) (policy.RevisionMetadata, error) {
	return restoreRevisionMetadataFields(
		row.PolicyID, row.Revision, row.SchemaMajor, row.SchemaMinor, row.Digest,
		row.EvaluatorMajor, row.EvaluatorMinor, row.EvaluatorDigest,
		row.CreatedAt.Valid, row.CreatedAt.Time,
	)
}

func restoreActivationMetadata(
	row sqlgen.ListPolicyActivationMetadataRow,
) (policy.ActivationMetadata, error) {
	revision, err := restoreRevisionMetadataFields(
		row.PolicyID, row.Revision, row.SchemaMajor, row.SchemaMinor, row.Digest,
		row.EvaluatorMajor, row.EvaluatorMinor, row.EvaluatorDigest,
		row.CreatedAt.Valid, row.CreatedAt.Time,
	)
	if err != nil {
		return policy.ActivationMetadata{}, policy.ErrActivationConflict
	}
	actor, err := id.ParseAPIKey(row.ActorKeyID)
	if err != nil || !row.ActivatedAt.Valid {
		return policy.ActivationMetadata{}, policy.ErrActivationConflict
	}
	previous, err := optionalRevision(row.PreviousRevision)
	if err != nil {
		return policy.ActivationMetadata{}, policy.ErrActivationConflict
	}
	return policy.RestoreActivationMetadata(
		revision, row.ActivationVersion, previous, actor, row.ActivatedAt.Time.UTC(),
	)
}

func restoreRevisionMetadataFields(
	policyValue string,
	revision int64,
	schemaMajor int32,
	schemaMinor int32,
	digest string,
	evaluatorMajor int32,
	evaluatorMinor int32,
	evaluatorDigest string,
	createdAtValid bool,
	createdAtTime time.Time,
) (policy.RevisionMetadata, error) {
	policyID, err := id.ParsePolicy(policyValue)
	if err != nil || revision < 1 || revision > math.MaxUint32 ||
		schemaMajor < 0 || schemaMajor > math.MaxUint16 ||
		schemaMinor < 0 || schemaMinor > math.MaxUint16 ||
		evaluatorMajor < 0 || evaluatorMajor > math.MaxUint16 ||
		evaluatorMinor < 0 || evaluatorMinor > math.MaxUint16 || !createdAtValid {
		return policy.RevisionMetadata{}, policy.ErrRevisionConflict
	}
	return policy.RestoreRevisionMetadata(
		policy.Reference{
			ID: policyID, Revision: uint32(revision),
			SchemaMajor: uint16(schemaMajor), SchemaMinor: uint16(schemaMinor),
			Digest: digest,
		},
		policy.EvaluatorReference{
			Major: uint16(evaluatorMajor), Minor: uint16(evaluatorMinor),
			Digest: evaluatorDigest,
		},
		createdAtTime.UTC(),
	)
}

func catalogPageLimit(limit int) int32 {
	// The repository validates limit in [1, MaximumCatalogPageSize+1] first.
	return int32(limit) //nolint:gosec // The validated maximum is far below MaxInt32.
}
