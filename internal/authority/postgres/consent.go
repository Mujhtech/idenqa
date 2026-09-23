package postgres

import (
	"context"
	"errors"
	"fmt"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/authority"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idempotencypostgres "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// FindResponse loads one exact tenant-owned immutable subject response.
func (store *Store) FindResponse(ctx context.Context, scope tenant.Scope, identifier id.Acknowledgement) (authority.Response, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return authority.Response{}, authority.ErrNotFound
	}
	var result authority.Response
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindSubjectResponse(ctx, sqlgen.FindSubjectResponseParams{TenantID: scope.ID().String(), ID: identifier.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return authority.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("find consent receipt: %w", err)
		}
		result, err = restoreResponse(row)
		return err
	})
	return result, err
}

// RevokeConsent atomically appends a refusal, event, webhook and replay receipt.
func (store *Store) RevokeConsent(ctx context.Context, scope tenant.Scope, mutation authority.ConsentRevocationMutation) (authority.Response, error) {
	record := mutation.Response.Record()
	if scope.ID().IsZero() || mutation.Original.IsZero() || mutation.Actor.IsZero() || mutation.EventID.IsZero() || record.Action != authority.ResponseRefuse || mutation.Idempotency.TenantID() != scope.ID() || mutation.Idempotency.Principal().String() != mutation.Actor.String() {
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
			row, err := queries.FindSubjectResponse(ctx, sqlgen.FindSubjectResponseParams{TenantID: scope.ID().String(), ID: identifier.String()})
			if err != nil {
				return err
			}
			result, err = restoreResponse(row)
			return err
		}
		originalRow, err := queries.FindSubjectResponse(ctx, sqlgen.FindSubjectResponseParams{TenantID: scope.ID().String(), ID: mutation.Original.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return authority.ErrNotFound
		}
		if err != nil {
			return err
		}
		original, err := restoreResponse(originalRow)
		if err != nil {
			return err
		}
		originalRecord := original.Record()
		if originalRecord.Action != authority.ResponseConsent || record.AuthorityID != originalRecord.AuthorityID || record.NoticeID != originalRecord.NoticeID || record.SubjectID != originalRecord.SubjectID || record.VerificationID != originalRecord.VerificationID || record.CaptureTokenID != originalRecord.CaptureTokenID || record.RecordedAt.Before(originalRecord.RecordedAt) {
			return authority.ErrConflict
		}
		latestRow, err := queries.FindLatestSubjectResponse(ctx, sqlgen.FindLatestSubjectResponseParams{TenantID: scope.ID().String(), AuthorityID: record.AuthorityID.String()})
		if err != nil {
			return err
		}
		latest, err := restoreResponse(latestRow)
		if err != nil {
			return err
		}
		if latest.Record().Action == authority.ResponseRefuse {
			return authority.ErrConflict
		}
		row, err := queries.CreateSubjectResponse(ctx, sqlgen.CreateSubjectResponseParams{ID: record.ID.String(), TenantID: record.TenantID.String(), AuthorityID: record.AuthorityID.String(), NoticeID: record.NoticeID.String(), SubjectID: record.SubjectID.String(), VerificationID: record.VerificationID.String(), CaptureTokenID: record.CaptureTokenID.String(), Action: string(record.Action), Locale: record.Locale, RenderedExperienceVersion: optionalString(record.RenderedExperienceVersion), RecordedAt: timestamp(record.RecordedAt)})
		if err != nil {
			return fmt.Errorf("append consent revocation: %w", err)
		}
		result, err = restoreResponse(row)
		if err != nil {
			return err
		}
		if err := insertEvent(ctx, queries, mutation.EventID, record.TenantID, "subject_response", record.ID.String(), 1, "authority.subject_response_recorded.v1", map[string]any{"response_id": record.ID.String(), "authority_id": record.AuthorityID.String(), "verification_id": record.VerificationID.String(), "action": record.Action, "actor_type": "api_key", "actor_id": mutation.Actor.String(), "reason": mutation.Reason, "withdrawal_of": mutation.Original.String()}, record.RecordedAt); err != nil {
			return err
		}
		region, err := store.verificationRegion(ctx, tx, scope, record.VerificationID)
		if err != nil {
			return err
		}
		if region != "" {
			if err := deliverypostgres.EmitCatalogueEvent(ctx, tx, store.wrapper, scope.ID().String(), region, webhookv1.ConsentRevoked, string(webhookv1.ConsentRevoked)+":"+record.ID.String(), record.RecordedAt, map[string]any{"verification_id": record.VerificationID.String(), "authority_id": record.AuthorityID.String(), "authority": map[string]any{"id": record.AuthorityID.String(), "type": "processing_authority", "verification_id": record.VerificationID.String(), "action": string(record.Action), "notice_id": record.NoticeID.String()}}); err != nil {
				return err
			}
		}
		return completeReference(ctx, queries, mutation.Idempotency, 201, map[string]string{"response_id": record.ID.String()}, record.RecordedAt)
	})
	return result, err
}
