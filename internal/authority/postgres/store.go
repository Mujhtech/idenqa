// Package postgres adapts processing-authority persistence to PostgreSQL.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/authority"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempotencypostgres "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/outbox"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

const eventSchemaVersion = 1

type transactionRunner interface {
	WithinTransaction(context.Context, platformpostgres.TransactionOptions, func(context.Context, platformpostgres.Transaction) error) error
}

// Store implements forced-RLS notice, authority, and response persistence.
type Store struct {
	pool    transactionRunner
	clock   clock.Clock
	wrapper platformcrypto.KeyWrapper
}

// New constructs the authority PostgreSQL adapter.
func New(pool transactionRunner, wrapper platformcrypto.KeyWrapper) (*Store, error) {
	return NewWithClock(pool, wrapper, clock.System{})
}

// NewWithClock supplies a deterministic clock for live response-credential checks.
func NewWithClock(pool transactionRunner, wrapper platformcrypto.KeyWrapper, source clock.Clock) (*Store, error) {
	if pool == nil || source == nil {
		return nil, errors.New("authority postgres: transaction runner and clock are required")
	}
	return &Store{pool: pool, clock: source, wrapper: wrapper}, nil
}

// CreateNotice atomically reserves idempotency and appends the notice, audit, and outbox intent.
func (store *Store) CreateNotice(ctx context.Context, scope tenant.Scope, mutation authority.NoticeMutation) (authority.Notice, error) {
	if scope.ID().IsZero() || mutation.Notice.TenantID().String() != scope.ID().String() ||
		mutation.EventID.IsZero() || mutation.Idempotency.TenantID().String() != scope.ID().String() ||
		mutation.Idempotency.Principal().String() != mutation.Notice.CreatedBy().String() {
		return authority.Notice{}, authority.ErrConflict
	}
	var result authority.Notice
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		reservation, err := idempotencypostgres.Reserve(ctx, queries, mutation.Idempotency)
		if err != nil {
			return err
		}
		if replay, exists := reservation.Result(); exists {
			identifier, err := replayNoticeID(replay)
			if err != nil {
				return err
			}
			row, err := queries.FindNoticeVersion(ctx, sqlgen.FindNoticeVersionParams{TenantID: scope.ID().String(), ID: identifier.String()})
			if err != nil {
				return fmt.Errorf("find replayed notice: %w", err)
			}
			result, err = restoreNotice(row)
			return err
		}
		notice := mutation.Notice
		noticeCopy := notice.Copy()
		row, err := queries.CreateNoticeVersion(ctx, sqlgen.CreateNoticeVersionParams{
			ID: notice.ID().String(), TenantID: notice.TenantID().String(), SemanticKey: notice.Key(),
			Locale: notice.Locale(), ControllerDisplayName: notice.Controller(),
			RecipientDisplayName: notice.Recipient(), Title: noticeCopy.Title, Summary: noticeCopy.Summary,
			PurposeCopy: noticeCopy.Purpose, ConsequenceCopy: noticeCopy.Consequences,
			EffectiveAt: timestamp(notice.EffectiveAt()), CreatedAt: timestamp(notice.CreatedAt()),
			CreatedBy: notice.CreatedBy().String(), Digest: notice.Digest(),
		})
		if err != nil {
			return fmt.Errorf("create notice version: %w", err)
		}
		result, err = restoreNotice(row)
		if err != nil {
			return err
		}
		if err := queries.InsertNoticeVersionAudit(ctx, sqlgen.InsertNoticeVersionAuditParams{
			TenantID: notice.TenantID().String(), NoticeID: notice.ID().String(), Action: "create",
			ActorKeyID: notice.CreatedBy().String(), OccurredAt: timestamp(notice.CreatedAt()),
		}); err != nil {
			return fmt.Errorf("insert notice audit: %w", err)
		}
		if err := insertEvent(ctx, queries, mutation.EventID, notice.TenantID(), "notice", notice.ID().String(), 1,
			"notice.created.v1", map[string]any{"notice_id": notice.ID().String(), "digest": notice.Digest()}, notice.CreatedAt()); err != nil {
			return err
		}
		return completeReference(ctx, queries, mutation.Idempotency, 201, map[string]string{"notice_id": notice.ID().String()}, notice.CreatedAt())
	})
	return result, err
}

// FindNotice retrieves one immutable notice within the explicit tenant scope.
func (store *Store) FindNotice(ctx context.Context, scope tenant.Scope, identifier id.Notice) (authority.Notice, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return authority.Notice{}, authority.ErrNotFound
	}
	var result authority.Notice
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindNoticeVersion(ctx, sqlgen.FindNoticeVersionParams{TenantID: scope.ID().String(), ID: identifier.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return authority.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("find notice version: %w", err)
		}
		result, err = restoreNotice(row)
		return err
	})
	return result, err
}

// Declare atomically creates a verification-local subject, declaration, immutable session binding, audit event, outbox intent, and replay result.
func (store *Store) Declare(ctx context.Context, scope tenant.Scope, mutation authority.DeclarationMutation) (authority.Authority, error) {
	record := mutation.Authority.Record()
	if scope.ID().IsZero() || record.TenantID.String() != scope.ID().String() || mutation.EventID.IsZero() ||
		mutation.Idempotency.TenantID().String() != scope.ID().String() ||
		mutation.Idempotency.Principal().String() != record.CreatedBy.String() {
		return authority.Authority{}, authority.ErrConflict
	}
	var result authority.Authority
	err := store.writeTx(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction, queries *sqlgen.Queries) error {
		reservation, err := idempotencypostgres.Reserve(ctx, queries, mutation.Idempotency)
		if err != nil {
			return err
		}
		if replay, exists := reservation.Result(); exists {
			identifier, err := replayAuthorityID(replay)
			if err != nil {
				return err
			}
			row, err := queries.FindProcessingAuthority(ctx, sqlgen.FindProcessingAuthorityParams{TenantID: scope.ID().String(), ID: identifier.String()})
			if err != nil {
				return fmt.Errorf("find replayed authority: %w", err)
			}
			result, err = restoreAuthority(row)
			return err
		}
		locked, err := queries.LockVerificationForAuthority(ctx, sqlgen.LockVerificationForAuthorityParams{TenantID: scope.ID().String(), ID: record.VerificationID.String()})
		if errors.Is(err, pgx.ErrNoRows) || locked.AuthorityID != nil {
			return authority.ErrConflict
		}
		if err != nil {
			return fmt.Errorf("lock verification for authority: %w", err)
		}
		if locked.State != "collecting" || !locked.ExpiresAt.Valid || record.ExpiresAt.After(locked.ExpiresAt.Time) ||
			!requirementsMatch(locked.Requirements, record.RequirementPurposes, record.EvidenceTypes) {
			return authority.ErrConflict
		}
		if _, err := queries.FindNoticeVersion(ctx, sqlgen.FindNoticeVersionParams{TenantID: scope.ID().String(), ID: record.NoticeID.String()}); errors.Is(err, pgx.ErrNoRows) {
			return authority.ErrNotFound
		} else if err != nil {
			return fmt.Errorf("find declaration notice: %w", err)
		}
		if err := queries.CreateVerificationSubject(ctx, sqlgen.CreateVerificationSubjectParams{
			ID: record.SubjectID.String(), TenantID: record.TenantID.String(), VerificationID: record.VerificationID.String(), CreatedAt: timestamp(record.CreatedAt),
		}); err != nil {
			return fmt.Errorf("create verification subject: %w", err)
		}
		row, err := queries.CreateProcessingAuthority(ctx, createAuthorityParams(record))
		if err != nil {
			return fmt.Errorf("create processing authority: %w", err)
		}
		result, err = restoreAuthority(row)
		if err != nil {
			return err
		}
		subjectID, authorityID, noticeID := record.SubjectID.String(), record.ID.String(), record.NoticeID.String()
		affected, err := queries.BindVerificationAuthority(ctx, sqlgen.BindVerificationAuthorityParams{
			TenantID: record.TenantID.String(), ID: record.VerificationID.String(), SubjectID: &subjectID, AuthorityID: &authorityID, NoticeID: &noticeID,
		})
		if err != nil {
			return fmt.Errorf("bind verification authority: %w", err)
		}
		if affected != 1 {
			return authority.ErrConflict
		}
		if err := insertAuthorityAudit(ctx, queries, record, "declare", record.CreatedBy); err != nil {
			return err
		}
		if err := insertEvent(ctx, queries, mutation.EventID, record.TenantID, "authority", record.ID.String(), record.Version,
			"authority.declared.v1", map[string]any{"authority_id": record.ID.String(), "verification_id": record.VerificationID.String(),
				"notice_id": record.NoticeID.String(), "subject_id": record.SubjectID.String()}, record.CreatedAt); err != nil {
			return err
		}
		region, err := store.verificationRegion(ctx, tx, scope, record.VerificationID)
		if err != nil {
			return err
		}
		if region == "" {
			return completeReference(ctx, queries, mutation.Idempotency, 201, map[string]string{"authority_id": record.ID.String()}, record.CreatedAt)
		}
		if err := deliverypostgres.EmitCatalogueEvent(ctx, tx, store.wrapper, scope.ID().String(), region, webhookv1.ProcessingAuthorityCreated,
			"processing_authority.created:"+record.ID.String(), record.CreatedAt,
			map[string]any{"authority_id": record.ID.String(), "verification_id": record.VerificationID.String(),
				"authority": map[string]any{"id": record.ID.String(), "type": "processing_authority", "verification_id": record.VerificationID.String(), "state": string(record.State), "notice_id": record.NoticeID.String()}}); err != nil {
			return err
		}
		return completeReference(ctx, queries, mutation.Idempotency, 201, map[string]string{"authority_id": record.ID.String()}, record.CreatedAt)
	})
	return result, err
}

// FindByVerification retrieves a declaration without disclosing cross-tenant existence.
func (store *Store) FindByVerification(ctx context.Context, scope tenant.Scope, verificationID id.Verification) (authority.Authority, error) {
	if scope.ID().IsZero() || verificationID.IsZero() {
		return authority.Authority{}, authority.ErrNotFound
	}
	var result authority.Authority
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindProcessingAuthorityByVerification(ctx, sqlgen.FindProcessingAuthorityByVerificationParams{
			TenantID: scope.ID().String(), VerificationID: verificationID.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return authority.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("find processing authority: %w", err)
		}
		result, err = restoreAuthority(row)
		return err
	})
	return result, err
}

// ReplayAuthority returns a completed authority command before state-dependent preparation.
func (store *Store) ReplayAuthority(
	ctx context.Context,
	scope tenant.Scope,
	request idempotency.Request,
) (authority.Authority, bool, error) {
	if scope.ID().IsZero() || request.TenantID().String() != scope.ID().String() {
		return authority.Authority{}, false, authority.ErrConflict
	}
	var result authority.Authority
	var found bool
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		replay, exists, err := idempotencypostgres.Replay(ctx, queries, request, request.CreatedAt())
		if err != nil || !exists {
			return err
		}
		identifier, err := replayAuthorityID(replay)
		if err != nil {
			return err
		}
		row, err := queries.FindProcessingAuthority(ctx, sqlgen.FindProcessingAuthorityParams{
			TenantID: scope.ID().String(), ID: identifier.String(),
		})
		if err != nil {
			return fmt.Errorf("find replayed authority: %w", err)
		}
		result, err = restoreAuthority(row)
		if err == nil {
			found = true
		}
		return err
	})
	return result, found, err
}

// Transition atomically applies one irreversible optimistic lifecycle transition.
func (store *Store) Transition(ctx context.Context, scope tenant.Scope, mutation authority.TransitionMutation) (authority.Authority, error) {
	record := mutation.Authority.Record()
	if scope.ID().IsZero() || record.TenantID.String() != scope.ID().String() || mutation.ExpectedVersion < 1 ||
		mutation.EventID.IsZero() || mutation.Actor.IsZero() || mutation.Idempotency.TenantID().String() != scope.ID().String() ||
		mutation.Idempotency.Principal().String() != mutation.Actor.String() {
		return authority.Authority{}, authority.ErrConflict
	}
	var result authority.Authority
	err := store.writeTx(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction, queries *sqlgen.Queries) error {
		reservation, err := idempotencypostgres.Reserve(ctx, queries, mutation.Idempotency)
		if err != nil {
			return err
		}
		if replay, exists := reservation.Result(); exists {
			identifier, err := replayAuthorityID(replay)
			if err != nil {
				return err
			}
			row, err := queries.FindProcessingAuthority(ctx, sqlgen.FindProcessingAuthorityParams{TenantID: scope.ID().String(), ID: identifier.String()})
			if err != nil {
				return fmt.Errorf("find replayed authority transition: %w", err)
			}
			result, err = restoreAuthority(row)
			return err
		}
		if err := lockAuthoritySession(ctx, queries, scope, record.VerificationID, record.ID); err != nil {
			return err
		}
		row, err := queries.TransitionProcessingAuthority(ctx, sqlgen.TransitionProcessingAuthorityParams{
			TenantID: record.TenantID.String(), ID: record.ID.String(), Version: mutation.ExpectedVersion,
			State: string(record.State), Version_2: record.Version, UpdatedAt: timestamp(record.UpdatedAt),
			RestrictedAt: optionalTimestamp(record.RestrictedAt), WithdrawnAt: optionalTimestamp(record.WithdrawnAt),
			SupersededAt: optionalTimestamp(record.SupersededAt),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return authority.ErrVersionConflict
		}
		if err != nil {
			return fmt.Errorf("transition processing authority: %w", err)
		}
		result, err = restoreAuthority(row)
		if err != nil {
			return err
		}
		if err := insertAuthorityAudit(ctx, queries, record, transitionAction(mutation.Action), mutation.Actor); err != nil {
			return err
		}
		if err := insertEvent(ctx, queries, mutation.EventID, record.TenantID, "authority", record.ID.String(), record.Version,
			"authority."+string(mutation.Action)+".v1", map[string]any{"authority_id": record.ID.String(), "state": record.State}, record.UpdatedAt); err != nil {
			return err
		}
		region, err := store.verificationRegion(ctx, tx, scope, record.VerificationID)
		if err != nil {
			return err
		}
		if region == "" {
			return completeReference(ctx, queries, mutation.Idempotency, 200, map[string]string{"authority_id": record.ID.String()}, record.UpdatedAt)
		}
		if err := deliverypostgres.EmitCatalogueEvent(ctx, tx, store.wrapper, scope.ID().String(), region, webhookv1.ProcessingAuthorityRestricted,
			"processing_authority.restricted:"+record.ID.String()+":"+strconv.FormatInt(record.Version, 10), record.UpdatedAt,
			map[string]any{"authority_id": record.ID.String(), "verification_id": record.VerificationID.String(), "state": string(record.State),
				"authority": map[string]any{"id": record.ID.String(), "type": "processing_authority", "verification_id": record.VerificationID.String(), "state": string(record.State)}}); err != nil {
			return err
		}
		return completeReference(ctx, queries, mutation.Idempotency, 200, map[string]string{"authority_id": record.ID.String()}, record.UpdatedAt)
	})
	return result, err
}

func transitionAction(state authority.State) string {
	switch state {
	case authority.StateRestricted:
		return "restrict"
	case authority.StateWithdrawn:
		return "withdraw"
	case authority.StateSuperseded:
		return "supersede"
	default:
		return string(state)
	}
}

// AppendResponse atomically appends one immutable subject response and its event.
func (store *Store) AppendResponse(ctx context.Context, scope tenant.Scope, mutation authority.ResponseMutation) (authority.Response, error) {
	record := mutation.Response.Record()
	if scope.ID().IsZero() || record.TenantID.String() != scope.ID().String() || mutation.EventID.IsZero() ||
		mutation.Idempotency.TenantID().String() != scope.ID().String() ||
		mutation.Idempotency.Principal().String() != record.CaptureTokenID.String() {
		return authority.Response{}, authority.ErrConflict
	}
	var result authority.Response
	err := store.writeTx(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction, queries *sqlgen.Queries) error {
		reservation, err := idempotencypostgres.Reserve(ctx, queries, mutation.Idempotency)
		if err != nil {
			return err
		}
		if replay, exists := reservation.Result(); exists {
			identifier, err := replayResponseID(replay)
			if err != nil {
				return err
			}
			row, err := queries.FindSubjectResponse(ctx, sqlgen.FindSubjectResponseParams{
				TenantID: scope.ID().String(), ID: identifier.String(),
			})
			if err != nil {
				return fmt.Errorf("find replayed subject response: %w", err)
			}
			result, err = restoreResponse(row)
			return err
		}
		if err := lockAuthoritySession(ctx, queries, scope, record.VerificationID, record.AuthorityID); err != nil {
			return err
		}
		credential, err := queries.FindCaptureToken(ctx, sqlgen.FindCaptureTokenParams{TenantID: scope.ID().String(), ID: record.CaptureTokenID.String(), VerificationID: record.VerificationID.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return authority.ErrProcessingNotPermitted
		}
		if err != nil {
			return err
		}
		now := store.clock.Now().UTC()
		if credential.RevokedAt.Valid || credential.IssuedAt.Time.After(now) || !credential.ExpiresAt.Time.After(now) || record.RecordedAt.Before(credential.IssuedAt.Time) || !record.RecordedAt.Before(credential.ExpiresAt.Time) {
			return authority.ErrProcessingNotPermitted
		}
		current, err := queries.FindProcessingAuthority(ctx, sqlgen.FindProcessingAuthorityParams{TenantID: scope.ID().String(), ID: record.AuthorityID.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return authority.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("find response authority: %w", err)
		}
		declaration, err := restoreAuthority(current)
		if err != nil {
			return err
		}
		state := declaration.Record()
		if state.State != authority.StateActive || record.NoticeID.String() != state.NoticeID.String() ||
			record.SubjectID.String() != state.SubjectID.String() || record.VerificationID.String() != state.VerificationID.String() ||
			record.RecordedAt.Before(state.ValidFrom) || !record.RecordedAt.Before(state.ExpiresAt) {
			return authority.ErrProcessingNotPermitted
		}
		row, err := queries.CreateSubjectResponse(ctx, sqlgen.CreateSubjectResponseParams{
			ID: record.ID.String(), TenantID: record.TenantID.String(), AuthorityID: record.AuthorityID.String(),
			NoticeID: record.NoticeID.String(), SubjectID: record.SubjectID.String(), VerificationID: record.VerificationID.String(),
			CaptureTokenID: record.CaptureTokenID.String(), Action: string(record.Action), Locale: record.Locale,
			RenderedExperienceVersion: optionalString(record.RenderedExperienceVersion), RecordedAt: timestamp(record.RecordedAt),
		})
		if err != nil {
			return fmt.Errorf("create subject response: %w", err)
		}
		result, err = restoreResponse(row)
		if err != nil {
			return err
		}
		if err := insertEvent(ctx, queries, mutation.EventID, record.TenantID, "subject_response", record.ID.String(), 1,
			"authority.subject_response_recorded.v1", map[string]any{"response_id": record.ID.String(), "authority_id": record.AuthorityID.String(),
				"verification_id": record.VerificationID.String(), "action": record.Action}, record.RecordedAt); err != nil {
			return err
		}
		if record.Action == authority.ResponseAcknowledge || record.Action == authority.ResponseConsent || record.Action == authority.ResponseRefuse {
			eventType := webhookv1.ConsentRecorded
			if record.Action == authority.ResponseRefuse {
				eventType = webhookv1.ConsentRevoked
			}
			region, err := store.verificationRegion(ctx, tx, scope, record.VerificationID)
			if err != nil {
				return err
			}
			if region != "" {
				if err := deliverypostgres.EmitCatalogueEvent(ctx, tx, store.wrapper, scope.ID().String(), region, eventType,
					string(eventType)+":"+record.ID.String(), record.RecordedAt,
					map[string]any{"verification_id": record.VerificationID.String(), "authority_id": record.AuthorityID.String(),
						"authority": map[string]any{"id": record.AuthorityID.String(), "type": "processing_authority", "verification_id": record.VerificationID.String(), "state": string(state.State), "action": string(record.Action), "notice_id": record.NoticeID.String()}}); err != nil {
					return err
				}
			}
		}
		return completeReference(ctx, queries, mutation.Idempotency, 201, map[string]string{"response_id": record.ID.String()}, record.RecordedAt)
	})
	return result, err
}

// CaptureSnapshot loads current authority and notice plus the latest response.
func (store *Store) CaptureSnapshot(ctx context.Context, scope tenant.Scope, verificationID id.Verification) (authority.Snapshot, error) {
	if scope.ID().IsZero() || verificationID.IsZero() {
		return authority.Snapshot{}, authority.ErrNotFound
	}
	var result authority.Snapshot
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		authorityRow, err := queries.FindProcessingAuthorityByVerification(ctx, sqlgen.FindProcessingAuthorityByVerificationParams{
			TenantID: scope.ID().String(), VerificationID: verificationID.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return authority.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("find capture authority: %w", err)
		}
		declaration, err := restoreAuthority(authorityRow)
		if err != nil {
			return err
		}
		noticeRow, err := queries.FindNoticeVersion(ctx, sqlgen.FindNoticeVersionParams{TenantID: scope.ID().String(), ID: declaration.NoticeID().String()})
		if err != nil {
			return fmt.Errorf("find capture notice: %w", err)
		}
		notice, err := restoreNotice(noticeRow)
		if err != nil {
			return err
		}
		result = authority.Snapshot{Authority: declaration, Notice: notice}
		responseRow, err := queries.FindLatestSubjectResponse(ctx, sqlgen.FindLatestSubjectResponseParams{
			TenantID: scope.ID().String(), AuthorityID: declaration.ID().String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("find latest subject response: %w", err)
		}
		response, err := restoreResponse(responseRow)
		if err != nil {
			return err
		}
		latestToken, err := queries.FindLatestCaptureRecoveryToken(ctx, sqlgen.FindLatestCaptureRecoveryTokenParams{TenantID: scope.ID().String(), VerificationID: verificationID.String()})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil && latestToken != response.Record().CaptureTokenID.String() {
			return nil
		}

		result.Response = &response
		return nil
	})
	return result, err
}

func (store *Store) writeTx(ctx context.Context, scope tenant.Scope, work func(context.Context, platformpostgres.Transaction, *sqlgen.Queries) error) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		queries := sqlgen.New(tx)
		if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set authority tenant scope: %w", err)
		}

		return work(ctx, tx, queries)
	})
}

func (store *Store) verificationRegion(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, verificationID id.Verification) (string, error) {
	var region string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(region,'') FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), verificationID.String()).Scan(&region); err != nil {
		return "", fmt.Errorf("load authority verification region: %w", err)
	}
	return region, nil
}

func (store *Store) write(ctx context.Context, scope tenant.Scope, work func(context.Context, *sqlgen.Queries) error) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationReadCommitted},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return fmt.Errorf("set authority tenant scope: %w", err)
			}
			return work(ctx, queries)
		})
}

func (store *Store) read(ctx context.Context, scope tenant.Scope, work func(context.Context, *sqlgen.Queries) error) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return fmt.Errorf("set authority tenant scope: %w", err)
			}
			return work(ctx, queries)
		})
}

func restoreNotice(row sqlgen.IdenqaNoticeVersion) (authority.Notice, error) {
	identifier, err := id.ParseNotice(row.ID)
	if err != nil {
		return authority.Notice{}, fmt.Errorf("parse stored notice id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return authority.Notice{}, fmt.Errorf("parse stored notice tenant id: %w", err)
	}
	actor, err := id.ParseAPIKey(row.CreatedBy)
	if err != nil {
		return authority.Notice{}, fmt.Errorf("parse stored notice actor id: %w", err)
	}
	return authority.RestoreNotice(authority.NoticeRecord{
		ID: identifier, TenantID: tenantID, Key: row.SemanticKey, Locale: row.Locale,
		Controller: row.ControllerDisplayName, Recipient: row.RecipientDisplayName,
		Copy:        authority.NoticeCopy{Title: row.Title, Summary: row.Summary, Purpose: row.PurposeCopy, Consequences: row.ConsequenceCopy},
		EffectiveAt: row.EffectiveAt.Time, CreatedAt: row.CreatedAt.Time, CreatedBy: actor, Digest: row.Digest,
	})
}

func restoreAuthority(row sqlgen.IdenqaProcessingAuthority) (authority.Authority, error) {
	identifier, err := id.ParseAuthority(row.ID)
	if err != nil {
		return authority.Authority{}, fmt.Errorf("parse stored authority id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return authority.Authority{}, fmt.Errorf("parse stored authority tenant id: %w", err)
	}
	subjectID, err := id.ParseSubject(row.SubjectID)
	if err != nil {
		return authority.Authority{}, fmt.Errorf("parse stored subject id: %w", err)
	}
	verificationID, err := id.ParseVerification(row.VerificationID)
	if err != nil {
		return authority.Authority{}, fmt.Errorf("parse stored verification id: %w", err)
	}
	noticeID, err := id.ParseNotice(row.NoticeID)
	if err != nil {
		return authority.Authority{}, fmt.Errorf("parse stored authority notice id: %w", err)
	}
	actor, err := id.ParseAPIKey(row.CreatedBy)
	if err != nil {
		return authority.Authority{}, fmt.Errorf("parse stored authority actor id: %w", err)
	}
	return authority.Restore(authority.Record{
		ID: identifier, TenantID: tenantID, SubjectID: subjectID, VerificationID: verificationID, NoticeID: noticeID,
		Category: row.Category, Purpose: row.Purpose, Jurisdiction: row.Jurisdiction, PolicyPack: row.PolicyPack,
		IsConsentRequired: row.ConsentRequired, RequirementPurposes: row.RequirementPurposes, EvidenceTypes: row.EvidenceTypes,
		RecipientReference: row.RecipientReference, RecipientDisplayName: row.RecipientDisplayName, Regions: row.Regions,
		RetentionReference: row.RetentionReference, State: authority.State(row.State), Version: row.Version,
		ValidFrom: row.ValidFrom.Time, ExpiresAt: row.ExpiresAt.Time, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time,
		CreatedBy: actor, RestrictedAt: timePointer(row.RestrictedAt), WithdrawnAt: timePointer(row.WithdrawnAt),
		SupersededAt: timePointer(row.SupersededAt),
	})
}

func restoreResponse(row sqlgen.IdenqaSubjectResponse) (authority.Response, error) {
	identifier, err := id.ParseAcknowledgement(row.ID)
	if err != nil {
		return authority.Response{}, fmt.Errorf("parse stored response id: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return authority.Response{}, fmt.Errorf("parse stored response tenant id: %w", err)
	}
	authorityID, err := id.ParseAuthority(row.AuthorityID)
	if err != nil {
		return authority.Response{}, fmt.Errorf("parse stored response authority id: %w", err)
	}
	noticeID, err := id.ParseNotice(row.NoticeID)
	if err != nil {
		return authority.Response{}, fmt.Errorf("parse stored response notice id: %w", err)
	}
	subjectID, err := id.ParseSubject(row.SubjectID)
	if err != nil {
		return authority.Response{}, fmt.Errorf("parse stored response subject id: %w", err)
	}
	verificationID, err := id.ParseVerification(row.VerificationID)
	if err != nil {
		return authority.Response{}, fmt.Errorf("parse stored response verification id: %w", err)
	}
	tokenID, err := id.ParseCaptureToken(row.CaptureTokenID)
	if err != nil {
		return authority.Response{}, fmt.Errorf("parse stored response token id: %w", err)
	}
	return authority.NewResponse(authority.ResponseRecord{
		ID: identifier, TenantID: tenantID, AuthorityID: authorityID, NoticeID: noticeID, SubjectID: subjectID,
		VerificationID: verificationID, CaptureTokenID: tokenID, Action: authority.ResponseAction(row.Action),
		Locale: row.Locale, RenderedExperienceVersion: stringValue(row.RenderedExperienceVersion), RecordedAt: row.RecordedAt.Time,
	})
}

func createAuthorityParams(record authority.Record) sqlgen.CreateProcessingAuthorityParams {
	return sqlgen.CreateProcessingAuthorityParams{
		ID: record.ID.String(), TenantID: record.TenantID.String(), SubjectID: record.SubjectID.String(),
		VerificationID: record.VerificationID.String(), NoticeID: record.NoticeID.String(), Category: record.Category,
		Purpose: record.Purpose, Jurisdiction: record.Jurisdiction, PolicyPack: record.PolicyPack,
		ConsentRequired: record.IsConsentRequired, RequirementPurposes: record.RequirementPurposes,
		EvidenceTypes: record.EvidenceTypes, RecipientReference: record.RecipientReference,
		RecipientDisplayName: record.RecipientDisplayName, Regions: record.Regions, RetentionReference: record.RetentionReference,
		State: string(record.State), Version: record.Version, ValidFrom: timestamp(record.ValidFrom), ExpiresAt: timestamp(record.ExpiresAt),
		CreatedAt: timestamp(record.CreatedAt), UpdatedAt: timestamp(record.UpdatedAt), CreatedBy: record.CreatedBy.String(),
		RestrictedAt: optionalTimestamp(record.RestrictedAt), WithdrawnAt: optionalTimestamp(record.WithdrawnAt),
		SupersededAt: optionalTimestamp(record.SupersededAt),
	}
}

func insertAuthorityAudit(ctx context.Context, queries *sqlgen.Queries, record authority.Record, action string, actor id.APIKey) error {
	if err := queries.InsertAuthorityAudit(ctx, sqlgen.InsertAuthorityAuditParams{
		TenantID: record.TenantID.String(), AuthorityID: record.ID.String(), AggregateVersion: record.Version,
		Action: action, ActorType: "api_key", ActorID: actor.String(), OccurredAt: timestamp(record.UpdatedAt),
	}); err != nil {
		return fmt.Errorf("insert authority audit: %w", err)
	}
	return nil
}

func insertEvent(ctx context.Context, queries *sqlgen.Queries, eventID id.Event, tenantID id.Tenant,
	aggregateType, aggregateID string, version int64, eventType string, payload any, occurredAt time.Time) error {
	intent, err := outbox.NewIntent(eventID, aggregateType, aggregateID, version, eventType, eventSchemaVersion, payload, occurredAt)
	if err != nil {
		return err
	}
	if err := queries.InsertOutboxEvent(ctx, sqlgen.InsertOutboxEventParams{
		ID: intent.ID.String(), TenantID: tenantID.String(), AggregateType: intent.AggregateType, AggregateID: intent.AggregateID,
		AggregateVersion: intent.AggregateVersion, EventType: intent.EventType, SchemaVersion: eventSchemaVersion,
		Payload: intent.Payload, OccurredAt: timestamp(intent.OccurredAt), CreatedAt: timestamp(occurredAt),
	}); err != nil {
		return fmt.Errorf("insert authority outbox intent: %w", err)
	}
	return nil
}

func completeReference(ctx context.Context, queries *sqlgen.Queries, request idempotency.Request, status int, reference any, completedAt time.Time) error {
	encoded, err := json.Marshal(reference)
	if err != nil {
		return fmt.Errorf("encode authority replay reference: %w", err)
	}
	result, err := idempotency.NewResult(status, encoded)
	if err != nil {
		return err
	}
	return idempotencypostgres.Complete(ctx, queries, request, result, completedAt)
}

func replayNoticeID(result idempotency.Result) (id.Notice, error) {
	var reference struct {
		NoticeID string `json:"notice_id"`
	}
	if err := json.Unmarshal(result.Body(), &reference); err != nil {
		return id.Notice{}, fmt.Errorf("decode notice replay: %w", err)
	}
	return id.ParseNotice(reference.NoticeID)
}
func replayAuthorityID(result idempotency.Result) (id.Authority, error) {
	var reference struct {
		AuthorityID string `json:"authority_id"`
	}
	if err := json.Unmarshal(result.Body(), &reference); err != nil {
		return id.Authority{}, fmt.Errorf("decode authority replay: %w", err)
	}
	return id.ParseAuthority(reference.AuthorityID)
}
func replayResponseID(result idempotency.Result) (id.Acknowledgement, error) {
	var reference struct {
		ResponseID string `json:"response_id"`
	}
	if err := json.Unmarshal(result.Body(), &reference); err != nil {
		return id.Acknowledgement{}, fmt.Errorf("decode response replay: %w", err)
	}
	return id.ParseAcknowledgement(reference.ResponseID)
}

func requirementsMatch(encoded []byte, purposes, evidenceTypes []string) bool {
	var document struct {
		Requirements []struct {
			Purpose      string `json:"purpose"`
			EvidenceType string `json:"evidence_type"`
		} `json:"requirements"`
	}
	if err := json.Unmarshal(encoded, &document); err != nil || len(document.Requirements) == 0 {
		return false
	}
	gotPurposes := make([]string, 0, len(document.Requirements))
	gotEvidence := make([]string, 0, len(document.Requirements))
	for _, requirement := range document.Requirements {
		gotPurposes = append(gotPurposes, requirement.Purpose)
		gotEvidence = append(gotEvidence, requirement.EvidenceType)
	}
	slices.Sort(gotPurposes)
	slices.Sort(gotEvidence)
	return slices.Equal(slices.Compact(gotPurposes), purposes) && slices.Equal(slices.Compact(gotEvidence), evidenceTypes)
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value.UTC(), Valid: !value.IsZero()}
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
	cloned := value.Time.UTC()
	return &cloned
}
func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
