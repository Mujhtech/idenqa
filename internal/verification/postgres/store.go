// Package postgres adapts capture-profile persistence to PostgreSQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempotencypostgres "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
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

// Store implements tenant-scoped capture-profile persistence.
type Store struct {
	pool    transactionRunner
	catalog evidence.Catalog
}

// New constructs a capture-profile PostgreSQL adapter.
func New(pool transactionRunner, catalog evidence.Catalog) (*Store, error) {
	if pool == nil || catalog.IsZero() {
		return nil, errors.New("verification postgres: pool and registry catalog are required")
	}

	return &Store{pool: pool, catalog: catalog}, nil
}

// FindProfile retrieves only a profile belonging to the explicit scope.
func (store *Store) FindProfile(
	ctx context.Context,
	scope tenant.Scope,
	identifier id.Profile,
) (verification.CaptureProfile, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return verification.CaptureProfile{}, verification.ErrProfileNotFound
	}

	var found verification.CaptureProfile
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindCaptureProfile(ctx, sqlgen.FindCaptureProfileParams{
			TenantID: scope.ID().String(),
			ID:       identifier.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrProfileNotFound
		}
		if err != nil {
			return fmt.Errorf("find capture profile: %w", err)
		}
		found, err = restoreProfile(row)

		return err
	})

	return found, err
}

// FindRevision retrieves one numeric revision under the explicit scope.
func (store *Store) FindRevision(
	ctx context.Context,
	scope tenant.Scope,
	identifier id.Profile,
	revision uint32,
) (verification.Revision, error) {
	if scope.ID().IsZero() || identifier.IsZero() || revision == 0 || revision > math.MaxInt32 {
		return verification.Revision{}, verification.ErrProfileNotFound
	}

	var found verification.Revision
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindCaptureProfileRevision(ctx, sqlgen.FindCaptureProfileRevisionParams{
			TenantID:  scope.ID().String(),
			ProfileID: identifier.String(),
			Revision:  int32(revision), //nolint:gosec // bounded above before the transaction
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrProfileNotFound
		}
		if err != nil {
			return fmt.Errorf("find capture profile revision: %w", err)
		}
		found, err = restoreRevision(row, store.catalog)

		return err
	})

	return found, err
}

// ListProfiles returns limit resources and an unsigned next position.
func (store *Store) ListProfiles(
	ctx context.Context,
	scope tenant.Scope,
	after *verification.ListPosition,
	limit int,
) (verification.Page, error) {
	if scope.ID().IsZero() || limit < 1 || limit > 100 {
		return verification.Page{}, errors.New("verification postgres: invalid profile list scope or limit")
	}

	page := verification.Page{Profiles: []verification.CaptureProfile{}}
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		var rows []sqlgen.IdenqaCaptureProfile
		var err error
		pageSize := int32(limit + 1) //nolint:gosec // limit is constrained to [1, 100]
		if after == nil {
			rows, err = queries.ListCaptureProfilesFirst(ctx, sqlgen.ListCaptureProfilesFirstParams{
				TenantID: scope.ID().String(),
				Limit:    pageSize,
			})
		} else {
			if after.CreatedAt.IsZero() {
				return errors.New("verification postgres: invalid profile list position")
			}
			if _, parseErr := id.ParseProfile(after.ID); parseErr != nil {
				return errors.New("verification postgres: invalid profile list position")
			}
			rows, err = queries.ListCaptureProfilesAfter(ctx, sqlgen.ListCaptureProfilesAfterParams{
				TenantID:       scope.ID().String(),
				AfterCreatedAt: timestamp(after.CreatedAt),
				AfterID:        after.ID,
				PageSize:       pageSize,
			})
		}
		if err != nil {
			return fmt.Errorf("list capture profiles: %w", err)
		}
		hasMore := len(rows) > limit
		if hasMore {
			rows = rows[:limit]
		}
		page.Profiles = make([]verification.CaptureProfile, 0, len(rows))
		for _, row := range rows {
			profile, err := restoreProfile(row)
			if err != nil {
				return err
			}
			page.Profiles = append(page.Profiles, profile)
		}
		if hasMore && len(rows) > 0 {
			last := rows[len(rows)-1]
			page.Next = &verification.ListPosition{CreatedAt: last.CreatedAt.Time, ID: last.ID}
		}

		return nil
	})

	return page, err
}

// SaveDraft atomically replaces a draft, advances the root, and audits the actor.
func (store *Store) SaveDraft(
	ctx context.Context,
	scope tenant.Scope,
	actor id.APIKey,
	profile verification.CaptureProfile,
	revision verification.Revision,
	expectedVersion int64,
) error {
	if !validScope(scope, actor, profile) || revision.ProfileID().String() != profile.ID().String() ||
		revision.State() != verification.RevisionStateDraft {
		return verification.ErrProfileConflict
	}

	return store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		parameters, err := store.draftParams(revision)
		if err != nil {
			return err
		}
		if _, err := queries.SaveCaptureProfileDraft(ctx, parameters); errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrProfileConflict
		} else if err != nil {
			return fmt.Errorf("save capture profile draft: %w", err)
		}
		if err := saveProfile(ctx, queries, profile, expectedVersion); err != nil {
			return err
		}

		return insertAudit(ctx, queries, profile, revision.Number(), verification.MutationUpdateDraft, actor)
	})
}

// Replay returns a completed, unexpired result before state-dependent command preparation.
func (store *Store) Replay(
	ctx context.Context,
	scope tenant.Scope,
	request idempotency.Request,
) (verification.MutationResult, bool, error) {
	if scope.ID().IsZero() || request.TenantID().String() != scope.ID().String() {
		return verification.MutationResult{}, false, verification.ErrProfileConflict
	}

	var result verification.MutationResult
	var found bool
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		replay, exists, err := idempotencypostgres.Replay(ctx, queries, request, request.CreatedAt())
		if err != nil || !exists {
			return err
		}
		if err := json.Unmarshal(replay.Body(), &result); err != nil {
			return fmt.Errorf("decode idempotency replay result: %w", err)
		}
		found = true

		return nil
	})

	return result, found, err
}

// Apply atomically reserves idempotency, persists a validated transition,
// appends its audit event, and completes the safe replay result.
func (store *Store) Apply(
	ctx context.Context,
	scope tenant.Scope,
	mutation verification.Mutation,
) (verification.MutationResult, error) {
	if !validScope(scope, mutation.Actor, mutation.Profile) ||
		mutation.Idempotency.TenantID().String() != scope.ID().String() ||
		mutation.Idempotency.Principal().String() != mutation.Actor.String() {
		return verification.MutationResult{}, verification.ErrProfileConflict
	}

	var result verification.MutationResult
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		reservation, err := idempotencypostgres.Reserve(ctx, queries, mutation.Idempotency)
		if err != nil {
			return err
		}
		if replay, ok := reservation.Result(); ok {
			if err := json.Unmarshal(replay.Body(), &result); err != nil {
				return fmt.Errorf("decode idempotency replay result: %w", err)
			}

			return nil
		}
		if !reservation.IsOwner() {
			return idempotency.ErrInProgress
		}
		if err := store.persistMutation(ctx, queries, mutation); err != nil {
			return err
		}
		encoded, err := json.Marshal(mutation.Result)
		if err != nil {
			return fmt.Errorf("encode idempotency result: %w", err)
		}
		replayResult, err := idempotency.NewResult(mutation.Status, encoded)
		if err != nil {
			return err
		}
		if err := idempotencypostgres.Complete(
			ctx,
			queries,
			mutation.Idempotency,
			replayResult,
			mutation.Profile.UpdatedAt(),
		); err != nil {
			return err
		}
		result = mutation.Result

		return nil
	})

	return result, err
}

func (store *Store) persistMutation(
	ctx context.Context,
	queries *sqlgen.Queries,
	mutation verification.Mutation,
) error {
	switch mutation.Kind {
	case verification.MutationCreate:
		if err := createProfile(ctx, queries, mutation.Profile); err != nil {
			return err
		}
		if err := store.createRevision(ctx, queries, mutation.Revision); err != nil {
			return err
		}
	case verification.MutationPublish:
		revisionNumber, err := databaseInt32(mutation.Revision.Number())
		if err != nil {
			return err
		}
		if _, err := queries.PublishCaptureProfileRevision(ctx, sqlgen.PublishCaptureProfileRevisionParams{
			TenantID: mutation.Profile.TenantID().String(), ProfileID: mutation.Profile.ID().String(),
			Revision: revisionNumber, UpdatedAt: timestamp(mutation.Profile.UpdatedAt()),
		}); errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrProfileConflict
		} else if err != nil {
			return fmt.Errorf("publish capture profile revision: %w", err)
		}
		if mutation.PreviousRevision != nil {
			previousNumber, err := databaseInt32(mutation.PreviousRevision.Number())
			if err != nil {
				return err
			}
			if _, err := queries.SupersedeCaptureProfileRevision(ctx, sqlgen.SupersedeCaptureProfileRevisionParams{
				TenantID: mutation.Profile.TenantID().String(), ProfileID: mutation.Profile.ID().String(),
				Revision: previousNumber, UpdatedAt: timestamp(mutation.Profile.UpdatedAt()),
			}); errors.Is(err, pgx.ErrNoRows) {
				return verification.ErrProfileConflict
			} else if err != nil {
				return fmt.Errorf("supersede capture profile revision: %w", err)
			}
		}
		if err := saveProfile(ctx, queries, mutation.Profile, mutation.ExpectedVersion); err != nil {
			return err
		}
	case verification.MutationSupersede:
		if err := store.createRevision(ctx, queries, mutation.Revision); err != nil {
			return err
		}
		if err := saveProfile(ctx, queries, mutation.Profile, mutation.ExpectedVersion); err != nil {
			return err
		}
	case verification.MutationDeactivate:
		if mutation.Revision.State() == verification.RevisionStateWithdrawn {
			revisionNumber, err := databaseInt32(mutation.Revision.Number())
			if err != nil {
				return err
			}
			if _, err := queries.WithdrawCaptureProfileRevision(ctx, sqlgen.WithdrawCaptureProfileRevisionParams{
				TenantID: mutation.Profile.TenantID().String(), ProfileID: mutation.Profile.ID().String(),
				Revision: revisionNumber, UpdatedAt: timestamp(mutation.Profile.UpdatedAt()),
			}); errors.Is(err, pgx.ErrNoRows) {
				return verification.ErrProfileConflict
			} else if err != nil {
				return fmt.Errorf("withdraw capture profile revision: %w", err)
			}
		}
		if err := saveProfile(ctx, queries, mutation.Profile, mutation.ExpectedVersion); err != nil {
			return err
		}
	default:
		return errors.New("verification postgres: unsupported profile mutation")
	}

	return insertAudit(
		ctx,
		queries,
		mutation.Profile,
		mutation.Revision.Number(),
		mutation.Kind,
		mutation.Actor,
	)
}

func (store *Store) read(
	ctx context.Context,
	scope tenant.Scope,
	work func(context.Context, *sqlgen.Queries) error,
) error {
	return store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return fmt.Errorf("set capture profile tenant scope: %w", err)
			}

			return work(ctx, queries)
		},
	)
}

func (store *Store) write(
	ctx context.Context,
	scope tenant.Scope,
	work func(context.Context, *sqlgen.Queries) error,
) error {
	return store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationReadCommitted},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return fmt.Errorf("set capture profile tenant scope: %w", err)
			}

			return work(ctx, queries)
		},
	)
}

func createProfile(ctx context.Context, queries *sqlgen.Queries, profile verification.CaptureProfile) error {
	parameters, err := profileParams(profile)
	if err != nil {
		return err
	}
	if err := queries.CreateCaptureProfile(ctx, parameters); err != nil {
		return fmt.Errorf("create capture profile: %w", err)
	}

	return nil
}

func (store *Store) createRevision(
	ctx context.Context,
	queries *sqlgen.Queries,
	revision verification.Revision,
) error {
	parameters, err := store.revisionParams(revision)
	if err != nil {
		return err
	}
	if err := queries.CreateCaptureProfileRevision(ctx, parameters); err != nil {
		return fmt.Errorf("create capture profile revision: %w", err)
	}

	return nil
}

func saveProfile(
	ctx context.Context,
	queries *sqlgen.Queries,
	profile verification.CaptureProfile,
	expectedVersion int64,
) error {
	parameters, err := saveParams(profile, expectedVersion)
	if err != nil {
		return err
	}
	if _, err := queries.SaveCaptureProfile(ctx, parameters); errors.Is(err, pgx.ErrNoRows) {
		return verification.ErrProfileConflict
	} else if err != nil {
		return fmt.Errorf("save capture profile: %w", err)
	}

	return nil
}

func insertAudit(
	ctx context.Context,
	queries *sqlgen.Queries,
	profile verification.CaptureProfile,
	revision uint32,
	action verification.MutationKind,
	actor id.APIKey,
) error {
	revisionValue, err := databaseInt32(revision)
	if err != nil {
		return err
	}
	if err := queries.InsertCaptureProfileAudit(ctx, sqlgen.InsertCaptureProfileAuditParams{
		TenantID:         profile.TenantID().String(),
		ProfileID:        profile.ID().String(),
		AggregateVersion: profile.Version(),
		Revision:         &revisionValue,
		Action:           string(action),
		ActorKeyID:       actor.String(),
		OccurredAt:       timestamp(profile.UpdatedAt()),
	}); err != nil {
		return fmt.Errorf("insert capture profile audit: %w", err)
	}

	return nil
}

func profileParams(profile verification.CaptureProfile) (sqlgen.CreateCaptureProfileParams, error) {
	latestRevision, err := databaseInt32(profile.LatestRevision())
	if err != nil {
		return sqlgen.CreateCaptureProfileParams{}, err
	}
	draftRevision, err := databaseInt32Pointer(profile.DraftRevision())
	if err != nil {
		return sqlgen.CreateCaptureProfileParams{}, err
	}
	publishedRevision, err := databaseInt32Pointer(profile.PublishedRevision())
	if err != nil {
		return sqlgen.CreateCaptureProfileParams{}, err
	}

	return sqlgen.CreateCaptureProfileParams{
		ID:                profile.ID().String(),
		TenantID:          profile.TenantID().String(),
		Name:              profile.Name(),
		State:             string(profile.State()),
		Version:           profile.Version(),
		LatestRevision:    latestRevision,
		DraftRevision:     draftRevision,
		PublishedRevision: publishedRevision,
		CreatedAt:         timestamp(profile.CreatedAt()),
		UpdatedAt:         timestamp(profile.UpdatedAt()),
		DeactivatedAt:     optionalTimestamp(profile.DeactivatedAt()),
	}, nil
}

func saveParams(profile verification.CaptureProfile, expectedVersion int64) (sqlgen.SaveCaptureProfileParams, error) {
	latestRevision, err := databaseInt32(profile.LatestRevision())
	if err != nil {
		return sqlgen.SaveCaptureProfileParams{}, err
	}
	draftRevision, err := databaseInt32Pointer(profile.DraftRevision())
	if err != nil {
		return sqlgen.SaveCaptureProfileParams{}, err
	}
	publishedRevision, err := databaseInt32Pointer(profile.PublishedRevision())
	if err != nil {
		return sqlgen.SaveCaptureProfileParams{}, err
	}

	return sqlgen.SaveCaptureProfileParams{
		TenantID:          profile.TenantID().String(),
		ID:                profile.ID().String(),
		Name:              profile.Name(),
		State:             string(profile.State()),
		Version:           profile.Version(),
		LatestRevision:    latestRevision,
		DraftRevision:     draftRevision,
		PublishedRevision: publishedRevision,
		UpdatedAt:         timestamp(profile.UpdatedAt()),
		DeactivatedAt:     optionalTimestamp(profile.DeactivatedAt()),
		ExpectedVersion:   expectedVersion,
	}, nil
}

func (store *Store) revisionParams(
	revision verification.Revision,
) (sqlgen.CreateCaptureProfileRevisionParams, error) {
	canonical, err := store.canonicalRevision(revision)
	if err != nil {
		return sqlgen.CreateCaptureProfileRevisionParams{}, err
	}
	reference := revision.Document().Registry
	revisionNumber, err := databaseInt32(revision.Number())
	if err != nil {
		return sqlgen.CreateCaptureProfileRevisionParams{}, err
	}
	schemaVersion, err := databaseInt32(revision.Document().SchemaVersion)
	if err != nil {
		return sqlgen.CreateCaptureProfileRevisionParams{}, err
	}
	registrySchemaVersion, err := databaseInt32(reference.SchemaVersion)
	if err != nil {
		return sqlgen.CreateCaptureProfileRevisionParams{}, err
	}
	registryRevision, err := databaseInt32(reference.Revision)
	if err != nil {
		return sqlgen.CreateCaptureProfileRevisionParams{}, err
	}

	return sqlgen.CreateCaptureProfileRevisionParams{
		TenantID:              revision.TenantID().String(),
		ProfileID:             revision.ProfileID().String(),
		Revision:              revisionNumber,
		State:                 string(revision.State()),
		SchemaVersion:         schemaVersion,
		RegistrySchemaVersion: registrySchemaVersion,
		RegistryRevision:      registryRevision,
		RegistryDigest:        reference.Digest,
		Document:              canonical,
		Digest:                revision.Digest(),
		CreatedAt:             timestamp(revision.CreatedAt()),
		UpdatedAt:             timestamp(revision.UpdatedAt()),
		PublishedAt:           optionalTimestamp(revision.PublishedAt()),
		EndedAt:               optionalTimestamp(revision.EndedAt()),
	}, nil
}

func (store *Store) draftParams(
	revision verification.Revision,
) (sqlgen.SaveCaptureProfileDraftParams, error) {
	canonical, err := store.canonicalRevision(revision)
	if err != nil {
		return sqlgen.SaveCaptureProfileDraftParams{}, err
	}
	reference := revision.Document().Registry
	revisionNumber, err := databaseInt32(revision.Number())
	if err != nil {
		return sqlgen.SaveCaptureProfileDraftParams{}, err
	}
	schemaVersion, err := databaseInt32(revision.Document().SchemaVersion)
	if err != nil {
		return sqlgen.SaveCaptureProfileDraftParams{}, err
	}
	registrySchemaVersion, err := databaseInt32(reference.SchemaVersion)
	if err != nil {
		return sqlgen.SaveCaptureProfileDraftParams{}, err
	}
	registryRevision, err := databaseInt32(reference.Revision)
	if err != nil {
		return sqlgen.SaveCaptureProfileDraftParams{}, err
	}

	return sqlgen.SaveCaptureProfileDraftParams{
		TenantID:              revision.TenantID().String(),
		ProfileID:             revision.ProfileID().String(),
		Revision:              revisionNumber,
		SchemaVersion:         schemaVersion,
		RegistrySchemaVersion: registrySchemaVersion,
		RegistryRevision:      registryRevision,
		RegistryDigest:        reference.Digest,
		Document:              canonical,
		Digest:                revision.Digest(),
		UpdatedAt:             timestamp(revision.UpdatedAt()),
	}, nil
}

func (store *Store) canonicalRevision(revision verification.Revision) ([]byte, error) {
	registry, err := store.catalog.Resolve(revision.Document().Registry)
	if err != nil {
		return nil, err
	}

	return verification.CanonicalJSON(revision.Document(), registry)
}

func restoreProfile(row sqlgen.IdenqaCaptureProfile) (verification.CaptureProfile, error) {
	identifier, err := id.ParseProfile(row.ID)
	if err != nil {
		return verification.CaptureProfile{}, fmt.Errorf("parse stored capture profile id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return verification.CaptureProfile{}, fmt.Errorf("parse stored capture profile tenant id: %w", err)
	}
	latestRevision, err := domainUint32(row.LatestRevision)
	if err != nil {
		return verification.CaptureProfile{}, err
	}
	draftRevision, err := domainUint32Pointer(row.DraftRevision)
	if err != nil {
		return verification.CaptureProfile{}, err
	}
	publishedRevision, err := domainUint32Pointer(row.PublishedRevision)
	if err != nil {
		return verification.CaptureProfile{}, err
	}

	return verification.RestoreCaptureProfile(
		identifier,
		tenantID,
		row.Name,
		verification.ProfileState(row.State),
		row.Version,
		latestRevision,
		draftRevision,
		publishedRevision,
		row.CreatedAt.Time,
		row.UpdatedAt.Time,
		timePointer(row.DeactivatedAt),
	)
}

func restoreRevision(
	row sqlgen.IdenqaCaptureProfileRevision,
	catalog evidence.Catalog,
) (verification.Revision, error) {
	identifier, err := id.ParseProfile(row.ProfileID)
	if err != nil {
		return verification.Revision{}, fmt.Errorf("parse stored capture profile revision id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return verification.Revision{}, fmt.Errorf("parse stored capture profile revision tenant id: %w", err)
	}
	registrySchemaVersion, err := domainUint32(row.RegistrySchemaVersion)
	if err != nil {
		return verification.Revision{}, err
	}
	registryRevision, err := domainUint32(row.RegistryRevision)
	if err != nil {
		return verification.Revision{}, err
	}
	schemaVersion, err := domainUint32(row.SchemaVersion)
	if err != nil {
		return verification.Revision{}, err
	}
	revisionNumber, err := domainUint32(row.Revision)
	if err != nil {
		return verification.Revision{}, err
	}
	reference := evidence.Reference{
		SchemaVersion: registrySchemaVersion,
		Revision:      registryRevision,
		Digest:        row.RegistryDigest,
	}
	registry, err := catalog.Resolve(reference)
	if err != nil {
		return verification.Revision{}, err
	}
	document, err := verification.ParseProfileJSON(row.Document, registry)
	if err != nil {
		return verification.Revision{}, fmt.Errorf("parse stored capture profile document: %w", err)
	}
	if document.SchemaVersion != schemaVersion || document.Registry != reference {
		return verification.Revision{}, errors.New("verification postgres: stored profile metadata does not match document")
	}

	return verification.RestoreRevision(
		identifier,
		tenantID,
		revisionNumber,
		verification.RevisionState(row.State),
		document,
		row.Digest,
		row.CreatedAt.Time,
		row.UpdatedAt.Time,
		timePointer(row.PublishedAt),
		timePointer(row.EndedAt),
		registry,
	)
}

func validScope(scope tenant.Scope, actor id.APIKey, profile verification.CaptureProfile) bool {
	return !scope.ID().IsZero() && !actor.IsZero() && !profile.ID().IsZero() &&
		scope.ID().String() == profile.TenantID().String()
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}

func optionalTimestamp(value *time.Time) pgtype.Timestamptz {
	if value == nil {
		return pgtype.Timestamptz{}
	}

	return timestamp(*value)
}

func timePointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time

	return &result
}

func databaseInt32(value uint32) (int32, error) {
	if value == 0 || value > math.MaxInt32 {
		return 0, errors.New("verification postgres: revision does not fit PostgreSQL integer")
	}

	return int32(value), nil
}

func databaseInt32Pointer(value *uint32) (*int32, error) {
	if value == nil {
		return nil, nil
	}
	result, err := databaseInt32(*value)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

func domainUint32(value int32) (uint32, error) {
	if value <= 0 {
		return 0, errors.New("verification postgres: stored revision is not positive")
	}

	return uint32(value), nil
}

func domainUint32Pointer(value *int32) (*uint32, error) {
	if value == nil {
		return nil, nil
	}
	result, err := domainUint32(*value)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

var _ verification.Repository = (*Store)(nil)
