package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempotencypostgres "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/outbox"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypg "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

const (
	verificationAggregateType = "verification"
	verificationCreatedEvent  = "verification.created.v1"
	verificationEventSchema   = 1
)

// SessionStore implements tenant-scoped verification-session persistence.
type SessionStore struct {
	pool    transactionRunner
	catalog evidence.Catalog
}

// NewSessionStore constructs a verification-session PostgreSQL adapter.
func NewSessionStore(pool transactionRunner, catalog evidence.Catalog) (*SessionStore, error) {
	if pool == nil || catalog.IsZero() {
		return nil, errors.New("verification postgres: session pool and registry catalog are required")
	}

	return &SessionStore{pool: pool, catalog: catalog}, nil
}

// Create atomically reserves idempotency, locks the selected active published
// profile revision, snapshots it, and persists credential, audit, and outbox intent.
func (store *SessionStore) Create(
	ctx context.Context,
	scope tenant.Scope,
	mutation verification.SessionCreateMutation,
) (verification.SessionCreation, error) {
	if !validSessionMutation(scope, mutation) {
		return verification.SessionCreation{}, verification.ErrSessionConflict
	}

	var creation verification.SessionCreation
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries, tx platformpostgres.Transaction) error {
		reservation, err := idempotencypostgres.Reserve(ctx, queries, mutation.Idempotency)
		if err != nil {
			return err
		}
		if replay, exists := reservation.Result(); exists {
			creation, err = store.restoreReplay(ctx, queries, scope.ID(), replay)

			return err
		}

		locked, err := queries.LockActiveCaptureProfileRevision(
			ctx,
			sqlgen.LockActiveCaptureProfileRevisionParams{
				TenantID: scope.ID().String(),
				ID:       mutation.ProfileID.String(),
			},
		)
		if errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrProfileUnavailable
		}
		if err != nil {
			return fmt.Errorf("lock active capture profile revision: %w", err)
		}
		profile, revision, registry, err := store.restoreLockedProfile(locked)
		if err != nil {
			return err
		}
		session, err := verification.NewSession(
			mutation.SessionID,
			scope.ID(),
			profile,
			revision,
			registry,
			mutation.Region,
			mutation.PolicyID,
			mutation.CreatedAt,
			mutation.SessionExpiresAt,
		)
		if err != nil {
			return err
		}
		credential, err := access.NewCaptureCredential(
			mutation.CaptureTokenID,
			scope.ID(),
			session.ID(),
			mutation.CaptureKeyVersion,
			mutation.CreatedAt,
			mutation.CaptureTokenExpiry,
		)
		if err != nil || credential.ExpiresAt().After(session.ExpiresAt()) {
			return verification.ErrSessionConflict
		}
		outcomeCredential, err := access.NewOutcomeCredential(
			mutation.OutcomeTokenID,
			scope.ID(),
			session.ID(),
			mutation.OutcomeKeyVersion,
			mutation.CreatedAt,
			mutation.OutcomeTokenExpiry,
		)
		if err != nil || !outcomeCredential.ExpiresAt().After(session.ExpiresAt()) {
			return verification.ErrSessionConflict
		}
		creation = verification.SessionCreation{
			Session: session, Credential: credential, OutcomeCredential: outcomeCredential,
		}
		if err := store.insertCreation(ctx, tx, queries, mutation, creation, registry); err != nil {
			return err
		}
		if err := policypg.PinAssuranceWithin(ctx, tx, scope, session.ID().String(), session.PolicyID().String()); err != nil {
			return err
		}
		encodedReplay, err := json.Marshal(sessionReplay{
			VerificationID: session.ID().String(),
			CaptureTokenID: credential.ID().String(),
			OutcomeTokenID: outcomeCredential.ID().String(),
		})
		if err != nil {
			return fmt.Errorf("encode verification idempotency result: %w", err)
		}
		replayResult, err := idempotency.NewResult(201, encodedReplay)
		if err != nil {
			return err
		}

		return idempotencypostgres.Complete(
			ctx,
			queries,
			mutation.Idempotency,
			replayResult,
			mutation.CreatedAt,
		)
	})

	return creation, err
}

// FindSession retrieves a verification only within the explicit tenant scope.
func (store *SessionStore) FindSession(
	ctx context.Context,
	scope tenant.Scope,
	identifier id.Verification,
) (verification.Session, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return verification.Session{}, verification.ErrSessionNotFound
	}
	var session verification.Session
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindVerificationSession(ctx, sqlgen.FindVerificationSessionParams{
			TenantID: scope.ID().String(),
			ID:       identifier.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrSessionNotFound
		}
		if err != nil {
			return fmt.Errorf("find verification session: %w", err)
		}
		session, err = store.restoreSession(row)

		return err
	})

	return session, err
}

// FindCaptureOutcome retrieves the minimum lifecycle and terminal-decision state
// needed for the subject-facing outcome projection.
func (store *SessionStore) FindCaptureOutcome(
	ctx context.Context,
	scope tenant.Scope,
	identifier id.Verification,
) (verification.CaptureOutcomeRecord, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return verification.CaptureOutcomeRecord{}, verification.ErrSessionNotFound
	}
	var outcome verification.CaptureOutcomeRecord
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindCaptureOutcome(ctx, sqlgen.FindCaptureOutcomeParams{
			TenantID: scope.ID().String(),
			ID:       identifier.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrSessionNotFound
		}
		if err != nil {
			return fmt.Errorf("find capture outcome: %w", err)
		}
		verificationID, err := id.ParseVerification(row.VerificationID)
		if err != nil {
			return verification.ErrSessionConflict
		}
		outcome = verification.CaptureOutcomeRecord{
			VerificationID: verificationID,
			SessionState:   verification.SessionState(row.SessionState),
			SessionVersion: row.SessionVersion,
			UpdatedAt:      row.UpdatedAt.Time.UTC(),
		}
		if row.DecisionOutcome != nil {
			outcome.DecisionOutcome = policy.Outcome(*row.DecisionOutcome)
		}
		return nil
	})
	return outcome, err
}

// FindCaptureCredential loads the non-secret credential record bound to all
// authenticated token claims. Callers must still enforce its usability time.
func (store *SessionStore) FindCaptureCredential(
	ctx context.Context,
	scope tenant.Scope,
	claims access.CaptureTokenClaims,
) (access.CaptureCredential, error) {
	if scope.ID().IsZero() || claims.TenantID.String() != scope.ID().String() ||
		claims.TokenID.IsZero() || claims.VerificationID.IsZero() {
		return access.CaptureCredential{}, access.ErrInvalidCaptureToken
	}
	var credential access.CaptureCredential
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindCaptureToken(ctx, sqlgen.FindCaptureTokenParams{
			TenantID:       scope.ID().String(),
			ID:             claims.TokenID.String(),
			VerificationID: claims.VerificationID.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return access.ErrInvalidCaptureToken
		}
		if err != nil {
			return fmt.Errorf("find capture credential: %w", err)
		}
		credential, err = restoreCredential(row)
		if err != nil || credential.KeyVersion() != claims.KeyVersion ||
			!credential.IssuedAt().Equal(claims.IssuedAt) ||
			!credential.ExpiresAt().Equal(claims.ExpiresAt) {
			return access.ErrInvalidCaptureToken
		}

		return nil
	})

	return credential, err
}

// FindForCapture performs the narrow pre-authentication lookup using only the
// signed tenant, token, and verification hints. It does not confer authority.
func (store *SessionStore) FindForCapture(
	ctx context.Context,
	claims access.CaptureTokenClaims,
) (verification.SessionCreation, error) {
	if claims.TenantID.IsZero() || claims.TokenID.IsZero() || claims.VerificationID.IsZero() {
		return verification.SessionCreation{}, access.ErrInvalidCaptureToken
	}
	var creation verification.SessionCreation
	err := store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, claims.TenantID.String()); err != nil {
				return fmt.Errorf("set capture authentication tenant hint: %w", err)
			}
			row, err := queries.FindCaptureContext(ctx, sqlgen.FindCaptureContextParams{
				TenantID:       claims.TenantID.String(),
				ID:             claims.TokenID.String(),
				VerificationID: claims.VerificationID.String(),
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return access.ErrInvalidCaptureToken
			}
			if err != nil {
				return fmt.Errorf("find capture authentication context: %w", err)
			}
			credential, err := restoreCredential(sqlgen.IdenqaCaptureToken{
				ID:             row.TokenID,
				TenantID:       row.TenantID,
				VerificationID: row.VerificationID,
				KeyVersion:     row.KeyVersion,
				IssuedAt:       row.IssuedAt,
				ExpiresAt:      row.TokenExpiresAt,
				RevokedAt:      row.RevokedAt,
			})
			if err != nil {
				return err
			}
			session, err := store.restoreSession(sqlgen.IdenqaVerificationSession{
				ID:                    row.VerificationID,
				TenantID:              row.TenantID,
				State:                 row.SessionState,
				Version:               row.SessionVersion,
				SourceProfileID:       row.SourceProfileID,
				SourceProfileRevision: row.SourceProfileRevision,
				SourceProfileDigest:   row.SourceProfileDigest,
				Requirements:          row.Requirements,
				Region:                row.Region,
				PolicyID:              row.PolicyID,
				DecisionID:            row.DecisionID,
				CreatedAt:             row.SessionCreatedAt,
				UpdatedAt:             row.SessionUpdatedAt,
				ExpiresAt:             row.SessionExpiresAt,
			})
			if err != nil {
				return err
			}
			creation = verification.SessionCreation{Session: session, Credential: credential}

			return nil
		},
	)

	return creation, err
}

// FindForOutcome performs the narrow pre-authentication lookup using only the
// signed tenant, token, and verification hints. It does not load capture state.
func (store *SessionStore) FindForOutcome(
	ctx context.Context,
	claims access.OutcomeTokenClaims,
) (access.OutcomeCredential, error) {
	if claims.TenantID.IsZero() || claims.TokenID.IsZero() || claims.VerificationID.IsZero() {
		return access.OutcomeCredential{}, access.ErrInvalidOutcomeToken
	}
	var credential access.OutcomeCredential
	err := store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, claims.TenantID.String()); err != nil {
				return fmt.Errorf("set outcome authentication tenant hint: %w", err)
			}
			row, err := queries.FindOutcomeContext(ctx, sqlgen.FindOutcomeContextParams{
				TenantID:       claims.TenantID.String(),
				ID:             claims.TokenID.String(),
				VerificationID: claims.VerificationID.String(),
			})
			if errors.Is(err, pgx.ErrNoRows) {
				return access.ErrInvalidOutcomeToken
			}
			if err != nil {
				return fmt.Errorf("find outcome authentication context: %w", err)
			}
			credential, err = restoreOutcomeCredential(sqlgen.IdenqaOutcomeToken{
				ID: row.TokenID, TenantID: row.TenantID, VerificationID: row.VerificationID,
				KeyVersion: row.KeyVersion, IssuedAt: row.IssuedAt,
				ExpiresAt: row.TokenExpiresAt, RevokedAt: row.RevokedAt,
			})

			return err
		},
	)

	return credential, err
}

func (store *SessionStore) write(
	ctx context.Context,
	scope tenant.Scope,
	work func(context.Context, *sqlgen.Queries, platformpostgres.Transaction) error,
) error {
	return store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationReadCommitted},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return fmt.Errorf("set verification session tenant scope: %w", err)
			}

			return work(ctx, queries, tx)
		},
	)
}

func (store *SessionStore) read(
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
				return fmt.Errorf("set verification session tenant scope: %w", err)
			}

			return work(ctx, queries)
		},
	)
}

func (store *SessionStore) insertCreation(
	ctx context.Context,
	tx platformpostgres.Transaction,
	queries *sqlgen.Queries,
	mutation verification.SessionCreateMutation,
	creation verification.SessionCreation,
	registry evidence.Registry,
) error {
	session := creation.Session
	requirements, err := verification.CanonicalJSON(session.Requirements(), registry)
	if err != nil {
		return err
	}
	profileRevision, err := databaseInt32(session.ProfileRevision())
	if err != nil {
		return err
	}
	if err := queries.CreateVerificationSession(ctx, sqlgen.CreateVerificationSessionParams{
		ID:                    session.ID().String(),
		TenantID:              session.TenantID().String(),
		State:                 string(session.State()),
		Version:               session.Version(),
		SourceProfileID:       session.ProfileID().String(),
		SourceProfileRevision: profileRevision,
		SourceProfileDigest:   session.ProfileDigest(),
		Region:                optionalString(session.Region()),
		PolicyID:              optionalString(session.PolicyID().String()),
		DecisionID:            optionalString(mutation.DecisionID.String()),
		Requirements:          requirements,
		CreatedAt:             timestamp(session.CreatedAt()),
		UpdatedAt:             timestamp(session.UpdatedAt()),
		ExpiresAt:             timestamp(session.ExpiresAt()),
	}); err != nil {
		return fmt.Errorf("create verification session: %w", err)
	}
	credential := creation.Credential
	if err := queries.CreateCaptureToken(ctx, sqlgen.CreateCaptureTokenParams{
		ID:             credential.ID().String(),
		TenantID:       credential.TenantID().String(),
		VerificationID: credential.VerificationID().String(),
		KeyVersion:     int32(credential.KeyVersion()),
		IssuedAt:       timestamp(credential.IssuedAt()),
		ExpiresAt:      timestamp(credential.ExpiresAt()),
		RevokedAt:      optionalTimestamp(credential.RevokedAt()),
	}); err != nil {
		return fmt.Errorf("create capture credential: %w", err)
	}
	outcomeCredential := creation.OutcomeCredential
	if err := queries.CreateOutcomeToken(ctx, sqlgen.CreateOutcomeTokenParams{
		ID:             outcomeCredential.ID().String(),
		TenantID:       outcomeCredential.TenantID().String(),
		VerificationID: outcomeCredential.VerificationID().String(),
		KeyVersion:     int32(outcomeCredential.KeyVersion()),
		IssuedAt:       timestamp(outcomeCredential.IssuedAt()),
		ExpiresAt:      timestamp(outcomeCredential.ExpiresAt()),
		RevokedAt:      optionalTimestamp(outcomeCredential.RevokedAt()),
	}); err != nil {
		return fmt.Errorf("create outcome credential: %w", err)
	}
	if err := queries.InsertVerificationSessionAudit(ctx, sqlgen.InsertVerificationSessionAuditParams{
		TenantID:         session.TenantID().String(),
		VerificationID:   session.ID().String(),
		AggregateVersion: session.Version(),
		Action:           "create",
		ActorKeyID:       mutation.Actor.String(),
		OccurredAt:       timestamp(session.CreatedAt()),
	}); err != nil {
		return fmt.Errorf("insert verification session audit: %w", err)
	}
	intent, err := outbox.NewIntent(
		mutation.EventID,
		verificationAggregateType,
		session.ID().String(),
		session.Version(),
		verificationCreatedEvent,
		verificationEventSchema,
		verificationCreatedPayload{
			VerificationID:  session.ID().String(),
			ProfileID:       session.ProfileID().String(),
			ProfileRevision: session.ProfileRevision(),
			PolicyID:        session.PolicyID().String(),
			Region:          session.Region(),
			ExpiresAt:       session.ExpiresAt(),
		},
		session.CreatedAt(),
	)
	if err != nil {
		return err
	}
	eventSchemaVersion, err := databaseInt32(intent.SchemaVersion)
	if err != nil {
		return err
	}
	if err := queries.InsertOutboxEvent(ctx, sqlgen.InsertOutboxEventParams{
		ID:               intent.ID.String(),
		TenantID:         session.TenantID().String(),
		AggregateType:    intent.AggregateType,
		AggregateID:      intent.AggregateID,
		AggregateVersion: intent.AggregateVersion,
		EventType:        intent.EventType,
		SchemaVersion:    eventSchemaVersion,
		Payload:          intent.Payload,
		OccurredAt:       timestamp(intent.OccurredAt),
		CreatedAt:        timestamp(session.CreatedAt()),
	}); err != nil {
		return fmt.Errorf("insert verification outbox intent: %w", err)
	}
	seed := "verification.created:" + session.ID().String() + ":" + strconv.FormatInt(session.Version(), 10)
	if err := deliverypostgres.EmitCatalogueEvent(ctx, tx, session.TenantID().String(), session.Region(), webhookv1.VerificationCreated, seed, session.CreatedAt(), map[string]any{
		"verification_id": session.ID().String(),
		"version":         session.Version(),
		"verification": map[string]any{
			"id":         session.ID().String(),
			"type":       "verification.session",
			"status":     string(session.State()),
			"version":    session.Version(),
			"created_at": session.CreatedAt().UTC().Format(time.RFC3339),
		},
	}); err != nil {
		return err
	}

	return nil
}

func (store *SessionStore) restoreReplay(
	ctx context.Context,
	queries *sqlgen.Queries,
	tenantID id.Tenant,
	replay idempotency.Result,
) (verification.SessionCreation, error) {
	var references sessionReplay
	if err := json.Unmarshal(replay.Body(), &references); err != nil {
		return verification.SessionCreation{}, fmt.Errorf("decode verification idempotency result: %w", err)
	}
	verificationID, err := id.ParseVerification(references.VerificationID)
	if err != nil {
		return verification.SessionCreation{}, fmt.Errorf("parse replay verification id: %w", err)
	}
	tokenID, err := id.ParseCaptureToken(references.CaptureTokenID)
	if err != nil {
		return verification.SessionCreation{}, fmt.Errorf("parse replay capture-token id: %w", err)
	}
	// Idempotency rows written before the outcome-credential migration cannot
	// satisfy the expanded create response without minting authority on replay.
	// Fail as a conflict instead of silently changing the original result.
	if references.OutcomeTokenID == "" {
		return verification.SessionCreation{}, verification.ErrSessionConflict
	}
	outcomeTokenID, err := id.ParseOutcomeToken(references.OutcomeTokenID)
	if err != nil {
		return verification.SessionCreation{}, fmt.Errorf("parse replay outcome-token id: %w", err)
	}
	sessionRow, err := queries.FindVerificationSession(ctx, sqlgen.FindVerificationSessionParams{
		TenantID: tenantID.String(),
		ID:       verificationID.String(),
	})
	if err != nil {
		return verification.SessionCreation{}, fmt.Errorf("find replayed verification session: %w", err)
	}
	// The idempotency result deliberately contains no tenant duplication. The
	// row's tenant is recovered through the transaction's forced RLS scope.
	// Creation returns its original snapshot; GET returns the current lifecycle.
	// These three fields are the only mutable fields in the public session view.
	sessionRow.State = string(verification.SessionStateCollecting)
	sessionRow.Version = 1
	sessionRow.UpdatedAt = sessionRow.CreatedAt
	session, err := store.restoreSession(sessionRow)
	if err != nil {
		return verification.SessionCreation{}, err
	}
	credentialRow, err := queries.FindCaptureToken(ctx, sqlgen.FindCaptureTokenParams{
		TenantID:       session.TenantID().String(),
		ID:             tokenID.String(),
		VerificationID: session.ID().String(),
	})
	if err != nil {
		return verification.SessionCreation{}, fmt.Errorf("find replayed capture credential: %w", err)
	}
	credential, err := restoreCredential(credentialRow)
	if err != nil {
		return verification.SessionCreation{}, err
	}
	outcomeCredentialRow, err := queries.FindOutcomeToken(ctx, sqlgen.FindOutcomeTokenParams{
		TenantID:       session.TenantID().String(),
		ID:             outcomeTokenID.String(),
		VerificationID: session.ID().String(),
	})
	if err != nil {
		return verification.SessionCreation{}, fmt.Errorf("find replayed outcome credential: %w", err)
	}
	outcomeCredential, err := restoreOutcomeCredential(outcomeCredentialRow)
	if err != nil {
		return verification.SessionCreation{}, err
	}

	return verification.SessionCreation{
		Session: session, Credential: credential, OutcomeCredential: outcomeCredential,
	}, nil
}

func (store *SessionStore) restoreLockedProfile(
	row sqlgen.LockActiveCaptureProfileRevisionRow,
) (verification.CaptureProfile, verification.Revision, evidence.Registry, error) {
	profile, err := restoreProfile(sqlgen.IdenqaCaptureProfile{
		ID:                row.ProfileID,
		TenantID:          row.TenantID,
		Name:              row.Name,
		State:             row.ProfileState,
		Version:           row.ProfileVersion,
		LatestRevision:    row.LatestRevision,
		DraftRevision:     row.DraftRevision,
		PublishedRevision: row.PublishedRevision,
		CreatedAt:         row.ProfileCreatedAt,
		UpdatedAt:         row.ProfileUpdatedAt,
		DeactivatedAt:     row.DeactivatedAt,
	})
	if err != nil {
		return verification.CaptureProfile{}, verification.Revision{}, evidence.Registry{}, err
	}
	revision, err := restoreRevision(sqlgen.IdenqaCaptureProfileRevision{
		TenantID:              row.TenantID,
		ProfileID:             row.ProfileID,
		Revision:              row.Revision,
		State:                 row.RevisionState,
		SchemaVersion:         row.SchemaVersion,
		RegistrySchemaVersion: row.RegistrySchemaVersion,
		RegistryRevision:      row.RegistryRevision,
		RegistryDigest:        row.RegistryDigest,
		Document:              row.Document,
		Digest:                row.Digest,
		CreatedAt:             row.RevisionCreatedAt,
		UpdatedAt:             row.RevisionUpdatedAt,
		PublishedAt:           row.PublishedAt,
		EndedAt:               row.EndedAt,
	}, store.catalog)
	if err != nil {
		return verification.CaptureProfile{}, verification.Revision{}, evidence.Registry{}, err
	}
	registry, err := store.catalog.Resolve(revision.Document().Registry)
	if err != nil {
		return verification.CaptureProfile{}, verification.Revision{}, evidence.Registry{}, err
	}

	return profile, revision, registry, nil
}

func (store *SessionStore) restoreSession(row sqlgen.IdenqaVerificationSession) (verification.Session, error) {
	identifier, err := id.ParseVerification(row.ID)
	if err != nil {
		return verification.Session{}, fmt.Errorf("parse stored verification id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return verification.Session{}, fmt.Errorf("parse stored verification tenant id: %w", err)
	}
	profileID, err := id.ParseProfile(row.SourceProfileID)
	if err != nil {
		return verification.Session{}, fmt.Errorf("parse stored verification profile id: %w", err)
	}
	revision, err := domainUint32(row.SourceProfileRevision)
	if err != nil {
		return verification.Session{}, err
	}
	var header struct {
		Registry evidence.Reference `json:"registry"`
	}
	if err := json.Unmarshal(row.Requirements, &header); err != nil {
		return verification.Session{}, fmt.Errorf("decode stored verification registry: %w", err)
	}
	registry, err := store.catalog.Resolve(header.Registry)
	if err != nil {
		return verification.Session{}, err
	}
	requirements, err := verification.ParseProfileJSON(row.Requirements, registry)
	if err != nil {
		return verification.Session{}, fmt.Errorf("parse stored verification requirements: %w", err)
	}
	if row.Region == nil {
		return verification.Session{}, errors.New("restore verification session: processing region is not pinned")
	}
	if row.PolicyID == nil {
		return verification.Session{}, errors.New("restore verification session: policy is not pinned")
	}
	policyID, err := id.ParsePolicy(*row.PolicyID)
	if err != nil {
		return verification.Session{}, fmt.Errorf("parse stored verification policy id: %w", err)
	}

	return verification.RestoreSession(
		identifier,
		tenantID,
		verification.SessionState(row.State),
		row.Version,
		profileID,
		revision,
		row.SourceProfileDigest,
		requirements,
		*row.Region,
		policyID,
		row.CreatedAt.Time,
		row.UpdatedAt.Time,
		row.ExpiresAt.Time,
		registry,
	)
}

func restoreCredential(row sqlgen.IdenqaCaptureToken) (access.CaptureCredential, error) {
	identifier, err := id.ParseCaptureToken(row.ID)
	if err != nil {
		return access.CaptureCredential{}, fmt.Errorf("parse stored capture-token id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return access.CaptureCredential{}, fmt.Errorf("parse stored capture-token tenant id: %w", err)
	}
	verificationID, err := id.ParseVerification(row.VerificationID)
	if err != nil {
		return access.CaptureCredential{}, fmt.Errorf("parse stored capture-token verification id: %w", err)
	}
	if row.KeyVersion < 1 || row.KeyVersion > math.MaxUint16 {
		return access.CaptureCredential{}, errors.New("verification postgres: capture-token key version is invalid")
	}

	return access.RestoreCaptureCredential(
		identifier,
		tenantID,
		verificationID,
		access.CaptureTokenKeyVersion(row.KeyVersion),
		row.IssuedAt.Time,
		row.ExpiresAt.Time,
		timePointer(row.RevokedAt),
	)
}

func restoreOutcomeCredential(row sqlgen.IdenqaOutcomeToken) (access.OutcomeCredential, error) {
	identifier, err := id.ParseOutcomeToken(row.ID)
	if err != nil {
		return access.OutcomeCredential{}, fmt.Errorf("parse stored outcome-token id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return access.OutcomeCredential{}, fmt.Errorf("parse stored outcome-token tenant id: %w", err)
	}
	verificationID, err := id.ParseVerification(row.VerificationID)
	if err != nil {
		return access.OutcomeCredential{}, fmt.Errorf("parse stored outcome-token verification id: %w", err)
	}
	if row.KeyVersion < 1 || row.KeyVersion > math.MaxUint16 {
		return access.OutcomeCredential{}, errors.New("verification postgres: outcome-token key version is invalid")
	}

	return access.RestoreOutcomeCredential(
		identifier,
		tenantID,
		verificationID,
		access.OutcomeTokenKeyVersion(row.KeyVersion),
		row.IssuedAt.Time,
		row.ExpiresAt.Time,
		timePointer(row.RevokedAt),
	)
}

func validSessionMutation(scope tenant.Scope, mutation verification.SessionCreateMutation) bool {
	return validSessionMutationForOperation(scope, mutation, verification.OperationCreateVerification)
}

func validSessionMutationForOperation(scope tenant.Scope, mutation verification.SessionCreateMutation, operation string) bool {
	return !scope.ID().IsZero() && !mutation.SessionID.IsZero() &&
		!mutation.CaptureTokenID.IsZero() && !mutation.OutcomeTokenID.IsZero() && !mutation.EventID.IsZero() &&
		!mutation.ProfileID.IsZero() && !mutation.PolicyID.IsZero() && !mutation.DecisionID.IsZero() && !mutation.Actor.IsZero() &&
		mutation.Region != "" &&
		mutation.CaptureKeyVersion > 0 && mutation.OutcomeKeyVersion > 0 && !mutation.CreatedAt.IsZero() &&
		mutation.SessionExpiresAt.After(mutation.CreatedAt) &&
		mutation.CaptureTokenExpiry.After(mutation.CreatedAt) &&
		!mutation.CaptureTokenExpiry.After(mutation.SessionExpiresAt) &&
		mutation.OutcomeTokenExpiry.After(mutation.SessionExpiresAt) &&
		mutation.Idempotency.TenantID().String() == scope.ID().String() &&
		mutation.Idempotency.Principal().String() == mutation.Actor.String() &&
		mutation.Idempotency.Operation() == operation
}

type sessionReplay struct {
	VerificationID string `json:"verification_id"`
	CaptureTokenID string `json:"capture_token_id"`
	OutcomeTokenID string `json:"outcome_token_id"`
}

type verificationCreatedPayload struct {
	VerificationID  string    `json:"verification_id"`
	ProfileID       string    `json:"profile_id"`
	ProfileRevision uint32    `json:"profile_revision"`
	PolicyID        string    `json:"policy_id"`
	Region          string    `json:"region"`
	ExpiresAt       time.Time `json:"expires_at"`
}
