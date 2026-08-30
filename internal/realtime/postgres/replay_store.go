package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/realtime"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// ReplayStore persists verification-local durable realtime streams.
type ReplayStore struct{ pool transactionRunner }

// NewReplayStore constructs the durable replay PostgreSQL adapter.
func NewReplayStore(pool transactionRunner) (*ReplayStore, error) {
	if pool == nil {
		return nil, errors.New("realtime postgres: replay pool is required")
	}

	return &ReplayStore{pool: pool}, nil
}

// Append serialises cursor assignment per verification and makes stable event
// identity replay idempotent.
func (store *ReplayStore) Append(
	ctx context.Context,
	intent realtime.EventIntent,
) (realtime.DurableEvent, error) {
	if intent.ID().IsZero() || intent.TenantID().IsZero() || intent.VerificationID().IsZero() {
		return realtime.DurableEvent{}, realtime.ErrInvalidDurableEvent
	}
	var durable realtime.DurableEvent
	err := store.withTenant(ctx, intent.TenantID(), false, func(ctx context.Context, queries *sqlgen.Queries) error {
		var err error
		durable, err = AppendEventWithinTransaction(ctx, queries, intent)
		return err
	})

	return durable, err
}

// AppendEventWithinTransaction appends through an existing tenant-scoped
// transaction so application state, outbox intent, and notification are one
// atomic PostgreSQL unit.
func AppendEventWithinTransaction(
	ctx context.Context,
	queries *sqlgen.Queries,
	intent realtime.EventIntent,
) (realtime.DurableEvent, error) {
	if queries == nil || intent.ID().IsZero() || intent.TenantID().IsZero() ||
		intent.VerificationID().IsZero() {
		return realtime.DurableEvent{}, realtime.ErrInvalidDurableEvent
	}
	payload, err := realtime.EncodeDurablePayload(intent.Payload())
	if err != nil {
		return realtime.DurableEvent{}, err
	}
	if _, err := queries.EnsureRealtimeStream(ctx, sqlgen.EnsureRealtimeStreamParams{
		TenantID: intent.TenantID().String(), VerificationID: intent.VerificationID().String(),
		UpdatedAt: timestamp(intent.OccurredAt()),
	}); err != nil {
		return realtime.DurableEvent{}, fmt.Errorf("ensure realtime stream: %w", err)
	}
	stream, err := queries.LockRealtimeStream(ctx, sqlgen.LockRealtimeStreamParams{
		TenantID: intent.TenantID().String(), VerificationID: intent.VerificationID().String(),
	})
	if err != nil {
		return realtime.DurableEvent{}, fmt.Errorf("lock realtime stream: %w", err)
	}
	if stream.LatestCursor > 0 && (!stream.LatestExpiresAt.Valid ||
		intent.ExpiresAt().Before(stream.LatestExpiresAt.Time.UTC())) {
		return realtime.DurableEvent{}, realtime.ErrDurableEventConflict
	}
	stored, err := queries.FindRealtimeEventByID(ctx, sqlgen.FindRealtimeEventByIDParams{
		TenantID: intent.TenantID().String(), EventID: intent.ID().String(),
	})
	if err == nil {
		durable, restoreErr := restoreRealtimeEvent(stored)
		if restoreErr != nil {
			return realtime.DurableEvent{}, restoreErr
		}
		if !sameEventIntent(durable.Intent(), intent, payload) {
			return realtime.DurableEvent{}, realtime.ErrDurableEventConflict
		}
		return durable, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return realtime.DurableEvent{}, fmt.Errorf("find realtime event: %w", err)
	}
	cursor, err := queries.AdvanceRealtimeStream(ctx, sqlgen.AdvanceRealtimeStreamParams{
		ExpiresAt: timestamp(intent.ExpiresAt()), UpdatedAt: timestamp(intent.OccurredAt()),
		TenantID: intent.TenantID().String(), VerificationID: intent.VerificationID().String(),
	})
	if err != nil || cursor < 1 {
		return realtime.DurableEvent{}, fmt.Errorf("advance realtime stream: %w", err)
	}
	if err := queries.InsertRealtimeEvent(ctx, sqlgen.InsertRealtimeEventParams{
		EventID: intent.ID().String(), TenantID: intent.TenantID().String(),
		VerificationID: intent.VerificationID().String(), Cursor: cursor,
		MessageType: string(intent.Type()), CommandID: optionalID(intent.CommandID().String()),
		CorrelationID: optionalID(intent.CorrelationID().String()),
		CausationID:   optionalID(intent.CausationID().String()), Payload: payload,
		OccurredAt: timestamp(intent.OccurredAt()), ExpiresAt: timestamp(intent.ExpiresAt()),
	}); err != nil {
		return realtime.DurableEvent{}, fmt.Errorf("insert realtime event: %w", err)
	}
	if err := queries.NotifyRealtimeEvent(ctx, sqlgen.NotifyRealtimeEventParams{
		TenantID: intent.TenantID().String(), VerificationID: intent.VerificationID().String(),
	}); err != nil {
		return realtime.DurableEvent{}, fmt.Errorf("notify realtime event: %w", err)
	}
	return realtime.RestoreDurableEvent(intent, realtime.EventCursor(cursor))
}

// Replay returns one bounded contiguous page or an explicit retention gap.
func (store *ReplayStore) Replay(
	ctx context.Context,
	ticket realtime.Ticket,
	after realtime.EventCursor,
	limit uint16,
	observedAt time.Time,
) (realtime.ReplayWindow, error) {
	if err := validateReplayRequest(ticket, limit, observedAt); err != nil {
		return realtime.ReplayWindow{}, err
	}
	afterCursor, err := cursorInt64(after)
	if err != nil {
		return realtime.ReplayWindow{}, err
	}

	var window realtime.ReplayWindow
	err = store.withTenant(ctx, ticket.TenantID(), true, func(ctx context.Context, queries *sqlgen.Queries) error {
		authority, err := loadReplayAuthority(ctx, queries, ticket, observedAt)
		if err != nil {
			return err
		}
		if authority.LatestCursor < 0 || authority.RetainedFromCursor < 1 {
			return realtime.ErrInvalidDurableEvent
		}
		// #nosec G115 -- PostgreSQL values were checked as non-negative above.
		latest := realtime.EventCursor(authority.LatestCursor)
		// #nosec G115 -- PostgreSQL value was checked as positive above.
		retainedFrom := realtime.EventCursor(authority.RetainedFromCursor)
		if after > latest {
			return realtime.ErrReplayCursorAhead
		}
		if after+1 < retainedFrom {
			window, err = realtime.NewReplayWindow(after, latest, retainedFrom, nil, false)

			return err
		}
		rows, err := queries.ListRealtimeEvents(ctx, sqlgen.ListRealtimeEventsParams{
			TenantID: ticket.TenantID().String(), VerificationID: ticket.VerificationID().String(),
			AfterCursor: afterCursor, ObservedAt: timestamp(observedAt), PageSize: int32(limit) + 1,
		})
		if err != nil {
			return fmt.Errorf("list realtime events: %w", err)
		}
		hasMore := len(rows) > int(limit)
		if hasMore {
			rows = rows[:limit]
		}
		events := make([]realtime.DurableEvent, 0, len(rows))
		for _, row := range rows {
			event, err := restoreRealtimeEvent(row)
			if err != nil {
				return err
			}
			events = append(events, event)
		}
		window, err = realtime.NewReplayWindow(after, latest, retainedFrom, events, hasMore)

		return err
	})

	return window, err
}

// Acknowledge advances the exact capture token's durable position
// monotonically and refuses cursors beyond committed stream state.
func (store *ReplayStore) Acknowledge(
	ctx context.Context,
	ticket realtime.Ticket,
	cursor realtime.EventCursor,
	observedAt time.Time,
) error {
	if err := validateReplayRequest(ticket, 1, observedAt); err != nil {
		return err
	}
	encodedCursor, err := cursorInt64(cursor)
	if err != nil {
		return err
	}
	return store.withTenant(ctx, ticket.TenantID(), false, func(ctx context.Context, queries *sqlgen.Queries) error {
		authority, err := loadReplayAuthority(ctx, queries, ticket, observedAt)
		if err != nil {
			return err
		}
		if encodedCursor > authority.LatestCursor {
			return realtime.ErrReplayCursorAhead
		}
		if encodedCursor == 0 || encodedCursor <= authority.AcknowledgedCursor {
			return nil
		}
		rows, err := queries.UpsertRealtimeAcknowledgement(ctx, sqlgen.UpsertRealtimeAcknowledgementParams{
			TenantID: ticket.TenantID().String(), VerificationID: ticket.VerificationID().String(),
			CaptureTokenID: ticket.CaptureTokenID().String(), AcknowledgedCursor: encodedCursor,
			UpdatedAt: timestamp(observedAt),
		})
		if err != nil {
			return fmt.Errorf("advance realtime acknowledgement: %w", err)
		}
		if rows != 1 {
			return errors.New("realtime postgres: acknowledgement was not persisted")
		}

		return nil
	})
}

// DeleteExpiredEvents removes a bounded tenant batch and advances the replay
// floor for every affected verification in the same transaction.
func (store *ReplayStore) DeleteExpiredEvents(
	ctx context.Context,
	scope tenant.Scope,
	observedAt time.Time,
	batchSize int32,
) (int, error) {
	if scope.ID().IsZero() || observedAt.IsZero() || observedAt.Location() != time.UTC ||
		batchSize < 1 || batchSize > 1000 {
		return 0, errors.New("realtime postgres: replay cleanup input is invalid")
	}
	deleted := 0
	err := store.withTenant(ctx, scope.ID(), false, func(ctx context.Context, queries *sqlgen.Queries) error {
		verificationIDs, err := queries.DeleteExpiredRealtimeEvents(
			ctx,
			sqlgen.DeleteExpiredRealtimeEventsParams{
				TenantID: scope.ID().String(), ObservedAt: timestamp(observedAt), BatchSize: batchSize,
			},
		)
		if err != nil {
			return fmt.Errorf("delete expired realtime events: %w", err)
		}
		deleted = len(verificationIDs)
		seen := make(map[string]struct{}, len(verificationIDs))
		for _, verificationID := range verificationIDs {
			if _, exists := seen[verificationID]; exists {
				continue
			}
			seen[verificationID] = struct{}{}
			rows, err := queries.RefreshRealtimeRetainedFrom(ctx, sqlgen.RefreshRealtimeRetainedFromParams{
				UpdatedAt: timestamp(observedAt), TenantID: scope.ID().String(),
				VerificationID: verificationID,
			})
			if err != nil {
				return fmt.Errorf("refresh realtime replay floor: %w", err)
			}
			if rows != 1 {
				return errors.New("realtime postgres: replay floor was not refreshed")
			}
		}

		return nil
	})

	return deleted, err
}

func (store *ReplayStore) withTenant(
	ctx context.Context,
	tenantID id.Tenant,
	readOnly bool,
	work func(context.Context, *sqlgen.Queries) error,
) error {
	return store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{
			Isolation: platformpostgres.IsolationReadCommitted,
			ReadOnly:  readOnly,
		},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			queries := sqlgen.New(tx)
			if _, err := queries.SetTenantScope(ctx, tenantID.String()); err != nil {
				return fmt.Errorf("set realtime replay tenant scope: %w", err)
			}

			return work(ctx, queries)
		},
	)
}

func loadReplayAuthority(
	ctx context.Context,
	queries *sqlgen.Queries,
	ticket realtime.Ticket,
	observedAt time.Time,
) (sqlgen.LoadRealtimeReplayAuthorityRow, error) {
	connectionID := ticket.ConnectionID().String()
	row, err := queries.LoadRealtimeReplayAuthority(ctx, sqlgen.LoadRealtimeReplayAuthorityParams{
		TenantID: ticket.TenantID().String(), TicketID: ticket.ID().String(),
		VerificationID: ticket.VerificationID().String(), CaptureTokenID: ticket.CaptureTokenID().String(),
		ConnectionID: &connectionID, ObservedAt: timestamp(observedAt),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return sqlgen.LoadRealtimeReplayAuthorityRow{}, realtime.ErrSessionUnavailable
	}
	if err != nil {
		return sqlgen.LoadRealtimeReplayAuthorityRow{}, fmt.Errorf("load realtime replay authority: %w", err)
	}
	if row.LatestCursor < 0 || row.RetainedFromCursor < 1 || row.AcknowledgedCursor < 0 ||
		row.RetainedFromCursor > row.LatestCursor+1 || row.AcknowledgedCursor > row.LatestCursor {
		return sqlgen.LoadRealtimeReplayAuthorityRow{}, errors.New("realtime postgres: invalid replay authority state")
	}

	return row, nil
}

func restoreRealtimeEvent(row sqlgen.IdenqaRealtimeEvent) (realtime.DurableEvent, error) {
	if row.Cursor < 1 || !row.OccurredAt.Valid || !row.ExpiresAt.Valid {
		return realtime.DurableEvent{}, errors.New("realtime postgres: invalid durable event state")
	}
	eventID, err := id.ParseEvent(row.EventID)
	if err != nil {
		return realtime.DurableEvent{}, fmt.Errorf("parse realtime event ID: %w", err)
	}
	tenantID, err := id.ParseTenant(row.TenantID)
	if err != nil {
		return realtime.DurableEvent{}, fmt.Errorf("parse realtime event tenant: %w", err)
	}
	verificationID, err := id.ParseVerification(row.VerificationID)
	if err != nil {
		return realtime.DurableEvent{}, fmt.Errorf("parse realtime event verification: %w", err)
	}
	commandID, err := parseOptionalCommand(row.CommandID)
	if err != nil {
		return realtime.DurableEvent{}, err
	}
	correlationID, err := parseOptionalMessage(row.CorrelationID)
	if err != nil {
		return realtime.DurableEvent{}, err
	}
	causationID, err := parseOptionalMessage(row.CausationID)
	if err != nil {
		return realtime.DurableEvent{}, err
	}
	payload, err := realtime.DecodeDurablePayload(realtime.MessageType(row.MessageType), row.Payload)
	if err != nil {
		return realtime.DurableEvent{}, fmt.Errorf("decode realtime event payload: %w", err)
	}
	intent, err := realtime.NewEventIntent(
		eventID, tenantID, verificationID, commandID, correlationID, causationID,
		row.OccurredAt.Time.UTC(), row.ExpiresAt.Time.UTC(), payload,
	)
	if err != nil {
		return realtime.DurableEvent{}, fmt.Errorf("restore realtime event: %w", err)
	}
	durable, err := realtime.RestoreDurableEvent(intent, realtime.EventCursor(row.Cursor))
	if err != nil {
		return realtime.DurableEvent{}, fmt.Errorf("restore realtime cursor: %w", err)
	}

	return durable, nil
}

func sameEventIntent(stored realtime.EventIntent, candidate realtime.EventIntent, payload []byte) bool {
	storedPayload, err := realtime.EncodeDurablePayload(stored.Payload())
	return err == nil && bytes.Equal(storedPayload, payload) && stored.TenantID() == candidate.TenantID() &&
		stored.VerificationID() == candidate.VerificationID() && stored.CommandID() == candidate.CommandID() &&
		stored.CorrelationID() == candidate.CorrelationID() && stored.CausationID() == candidate.CausationID() &&
		stored.OccurredAt().Equal(candidate.OccurredAt()) && stored.ExpiresAt().Equal(candidate.ExpiresAt()) &&
		stored.Type() == candidate.Type()
}

func validateReplayRequest(ticket realtime.Ticket, limit uint16, observedAt time.Time) error {
	if ticket.TenantID().IsZero() || ticket.VerificationID().IsZero() || ticket.CaptureTokenID().IsZero() ||
		ticket.ID().IsZero() || ticket.ConnectionID().IsZero() || ticket.RedeemedAt() == nil ||
		observedAt.IsZero() || observedAt.Location() != time.UTC {
		return realtime.ErrSessionUnavailable
	}
	return realtime.ValidateReplayLimit(limit)
}

func cursorInt64(cursor realtime.EventCursor) (int64, error) {
	if cursor.Uint64() > math.MaxInt64 {
		return 0, realtime.ErrReplayCursorAhead
	}

	// #nosec G115 -- the cursor is bounded by math.MaxInt64 above.
	return int64(cursor), nil
}

func optionalID(value string) *string {
	if value == "" {
		return nil
	}

	return &value
}

func parseOptionalCommand(value *string) (id.Command, error) {
	if value == nil {
		return id.Command{}, nil
	}
	parsed, err := id.ParseCommand(*value)
	if err != nil {
		return id.Command{}, fmt.Errorf("parse realtime event command: %w", err)
	}

	return parsed, nil
}

func parseOptionalMessage(value *string) (id.Message, error) {
	if value == nil {
		return id.Message{}, nil
	}
	parsed, err := id.ParseMessage(*value)
	if err != nil {
		return id.Message{}, fmt.Errorf("parse realtime event message reference: %w", err)
	}

	return parsed, nil
}

var _ realtime.ReplayRepository = (*ReplayStore)(nil)
var _ realtime.ReplayCleanupRepository = (*ReplayStore)(nil)
