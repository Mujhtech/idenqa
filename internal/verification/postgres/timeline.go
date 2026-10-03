package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func (store *SessionStore) RecordJourneyEvent(ctx context.Context, authority verification.CaptureContext, event verification.JourneyEvent) (verification.JourneyEvent, error) {
	err := store.write(ctx, authority.TenantScope(), func(ctx context.Context, queries *sqlgen.Queries, _ platformpostgres.Transaction) error {
		rows, err := queries.InsertCaptureJourneyEvent(ctx, sqlgen.InsertCaptureJourneyEventParams{
			TenantID: authority.TenantScope().ID().String(), VerificationID: authority.Session().ID().String(),
			CaptureTokenID: authority.TokenID().String(), EventID: event.EventID, Sequence: int32(event.Sequence),
			EventType: event.EventType, Screen: event.Screen, Action: optionalText(event.Action),
			RequirementKey: optionalText(event.RequirementKey), Artefact: optionalText(event.Artefact),
			AcquisitionMethod: optionalText(event.AcquisitionMethod),
			ClientOccurredAt:  pgtype.Timestamptz{Time: event.ClientOccurredAt, Valid: true},
			ReceivedAt:        pgtype.Timestamptz{Time: event.ReceivedAt, Valid: true}, PayloadDigest: event.Digest,
		})
		if err != nil {
			var conflict *pgconn.PgError
			if errors.As(err, &conflict) && conflict.Code == "23505" {
				return verification.ErrSessionConflict
			}
			return fmt.Errorf("insert capture journey event: %w", err)
		}
		if rows == 1 {
			return nil
		}
		digest, err := queries.FindCaptureJourneyEventDigest(ctx, sqlgen.FindCaptureJourneyEventDigestParams{TenantID: authority.TenantScope().ID().String(), EventID: event.EventID})
		if err != nil {
			return fmt.Errorf("find capture journey replay: %w", err)
		}
		if digest != event.Digest {
			return verification.ErrSessionConflict
		}
		return nil
	})
	return event, err
}

func (store *SessionStore) FindTimeline(ctx context.Context, scope tenant.Scope, identifier id.Verification) (verification.Timeline, error) {
	if scope.ID().IsZero() || identifier.IsZero() {
		return verification.Timeline{}, verification.ErrSessionNotFound
	}
	result := verification.Timeline{Events: []verification.TimelineEvent{}}
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		tenantID, verificationID := scope.ID().String(), identifier.String()
		if _, err := queries.FindVerificationTimelineSession(ctx, sqlgen.FindVerificationTimelineSessionParams{TenantID: tenantID, ID: verificationID}); errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrSessionNotFound
		} else if err != nil {
			return fmt.Errorf("find verification timeline session: %w", err)
		}
		rows, err := queries.ListVerificationTimeline(ctx, sqlgen.ListVerificationTimelineParams{TenantID: tenantID, VerificationID: verificationID})
		if err != nil {
			return fmt.Errorf("list verification timeline: %w", err)
		}
		if len(rows) > 500 {
			result.Truncated = true
			rows = rows[:500]
		}
		for _, row := range rows {
			result.Events = append(result.Events, verification.TimelineEvent{
				ID: row.EventID, Category: row.Category, Source: row.Source, Name: row.EventName,
				Status: row.Status, Detail: row.Detail, OccurredAt: row.OccurredAt.Time,
				Authoritative: row.Authoritative,
			})
		}
		return nil
	})
	return result, err
}

func optionalText(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
