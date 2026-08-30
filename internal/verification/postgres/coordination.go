package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/realtime"
	realtimepostgres "github.com/Mujhtech/idenqa/internal/realtime/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationtask "github.com/Mujhtech/idenqa/internal/verification/task"
)

// CheckProgressNotificationChannel carries tenant identifiers as lossy routing
// hints. Durable outbox rows remain the source of truth.
const CheckProgressNotificationChannel = "idenqa_check_progress_v1"

const (
	listDueReconciliationsSQL = `SELECT tenant_id, check_id, attempt_id
FROM idenqa.list_due_verification_reconciliations($1, $2)`
	listPendingProgressTenantsSQL = `SELECT tenant_id
FROM idenqa.list_pending_check_progress_tenants($1)`
)

type checkProgressPayload struct {
	VerificationID string    `json:"verification_id"`
	CheckID        string    `json:"check_id"`
	State          string    `json:"state"`
	Version        int64     `json:"version"`
	OccurredAt     time.Time `json:"occurred_at"`
}

// ListDueReconciliations performs bounded identifier-only installation-wide
// discovery through the explicitly granted SECURITY DEFINER function.
func (store *CheckStore) ListDueReconciliations(
	ctx context.Context,
	observedAt time.Time,
	batchSize int,
) ([]verificationtask.ReconciliationTarget, error) {
	if store == nil || !utcTime(observedAt) || batchSize < 1 || batchSize > verificationtask.CoordinationBatch {
		return nil, verification.ErrInvalidCheck
	}
	var targets []verificationtask.ReconciliationTarget
	err := store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, transaction platformpostgres.Transaction) error {
			rows, err := transaction.Query(ctx, listDueReconciliationsSQL, observedAt, batchSize)
			if err != nil {
				return fmt.Errorf("query due verification reconciliations: %w", err)
			}
			defer rows.Close()
			for rows.Next() {
				var tenantValue, checkValue, attemptValue string
				if err := rows.Scan(&tenantValue, &checkValue, &attemptValue); err != nil {
					return fmt.Errorf("scan due verification reconciliation: %w", err)
				}
				tenantID, err := id.ParseTenant(tenantValue)
				if err != nil {
					return verification.ErrInvalidCheck
				}
				checkID, err := id.ParseCheck(checkValue)
				if err != nil {
					return verification.ErrInvalidCheck
				}
				attemptID, err := id.ParseAttempt(attemptValue)
				if err != nil {
					return verification.ErrInvalidCheck
				}
				targets = append(targets, verificationtask.ReconciliationTarget{
					TenantID: tenantID, CheckID: checkID, AttemptID: attemptID,
				})
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("iterate due verification reconciliations: %w", err)
			}
			return nil
		},
	)
	return targets, err
}

// ListPendingProgressTenants discovers only tenant routing identifiers.
func (store *CheckStore) ListPendingProgressTenants(
	ctx context.Context,
	batchSize int,
) ([]id.Tenant, error) {
	if store == nil || batchSize < 1 || batchSize > verificationtask.CoordinationBatch {
		return nil, verification.ErrInvalidCheck
	}
	var tenantIDs []id.Tenant
	err := store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, transaction platformpostgres.Transaction) error {
			rows, err := transaction.Query(ctx, listPendingProgressTenantsSQL, batchSize)
			if err != nil {
				return fmt.Errorf("query pending check progress tenants: %w", err)
			}
			defer rows.Close()
			for rows.Next() {
				var value string
				if err := rows.Scan(&value); err != nil {
					return fmt.Errorf("scan pending check progress tenant: %w", err)
				}
				tenantID, err := id.ParseTenant(value)
				if err != nil {
					return verification.ErrInvalidCheck
				}
				tenantIDs = append(tenantIDs, tenantID)
			}
			if err := rows.Err(); err != nil {
				return fmt.Errorf("iterate pending check progress tenants: %w", err)
			}
			return nil
		},
	)
	return tenantIDs, err
}

// ProjectCheckProgress atomically appends capture-safe durable events and marks
// their exact outbox sources published under one tenant scope.
func (store *CheckStore) ProjectCheckProgress(
	ctx context.Context,
	scope tenant.Scope,
	observedAt time.Time,
	batchSize int,
) (int, error) {
	if store == nil || scope.ID().IsZero() || !utcTime(observedAt) || batchSize < 1 ||
		batchSize > verificationtask.CoordinationBatch {
		return 0, verification.ErrInvalidCheck
	}
	// CoordinationBatch is 100, so this conversion is bounded well below int32.
	batchSize32 := int32(batchSize)
	projected := 0
	err := store.write(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		rows, err := queries.ListPendingCheckProgressEvents(ctx, sqlgen.ListPendingCheckProgressEventsParams{
			TenantID: scope.ID().String(), BatchSize: batchSize32,
		})
		if err != nil {
			return fmt.Errorf("list pending check progress events: %w", err)
		}
		for _, row := range rows {
			intent, err := progressEventIntent(scope.ID(), row)
			if err != nil {
				return err
			}
			if _, err := realtimepostgres.AppendEventWithinTransaction(ctx, queries, intent); err != nil {
				return fmt.Errorf("append check progress realtime event: %w", err)
			}
			publishedAt := observedAt
			if publishedAt.Before(row.CreatedAt.Time.UTC()) {
				publishedAt = row.CreatedAt.Time.UTC()
			}
			updated, err := queries.MarkCheckProgressPublished(ctx, sqlgen.MarkCheckProgressPublishedParams{
				PublishedAt: timestamp(publishedAt), TenantID: scope.ID().String(), ID: row.ID,
			})
			if err != nil {
				return fmt.Errorf("mark check progress published: %w", err)
			}
			if updated != 1 {
				return errors.New("verification postgres: check progress publication lost its claim")
			}
			projected++
		}
		return nil
	})
	return projected, err
}

func progressEventIntent(
	tenantID id.Tenant,
	row sqlgen.ListPendingCheckProgressEventsRow,
) (realtime.EventIntent, error) {
	if row.TenantID != tenantID.String() || row.AggregateType != checkAggregateType ||
		row.EventType != checkProgressEvent || row.SchemaVersion != checkEventSchema ||
		row.AggregateVersion < 1 || !row.OccurredAt.Valid || !row.CreatedAt.Valid ||
		!row.SessionExpiresAt.Valid {
		return realtime.EventIntent{}, verification.ErrInvalidCheck
	}
	payload, err := decodeCheckProgressPayload(row.Payload)
	if err != nil || payload.CheckID != row.AggregateID || payload.Version != row.AggregateVersion ||
		!payload.OccurredAt.Equal(row.OccurredAt.Time.UTC()) {
		return realtime.EventIntent{}, verification.ErrInvalidCheck
	}
	eventID, err := id.ParseEvent(row.ID)
	if err != nil {
		return realtime.EventIntent{}, verification.ErrInvalidCheck
	}
	verificationID, err := id.ParseVerification(payload.VerificationID)
	if err != nil {
		return realtime.EventIntent{}, verification.ErrInvalidCheck
	}
	checkID, err := id.ParseCheck(payload.CheckID)
	if err != nil {
		return realtime.EventIntent{}, verification.ErrInvalidCheck
	}
	return realtime.NewEventIntent(
		eventID,
		tenantID,
		verificationID,
		id.Command{},
		id.Message{},
		id.Message{},
		row.OccurredAt.Time.UTC(),
		row.SessionExpiresAt.Time.UTC(),
		realtime.VerificationCheckProgress{
			CheckID: checkID, State: payload.State, CheckVersion: payload.Version,
		},
	)
}

func decodeCheckProgressPayload(encoded []byte) (checkProgressPayload, error) {
	var payload checkProgressPayload
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return checkProgressPayload{}, fmt.Errorf("decode check progress outbox payload: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return checkProgressPayload{}, verification.ErrInvalidCheck
	}
	if payload.VerificationID == "" || payload.CheckID == "" || payload.State == "" ||
		payload.Version < 1 || !utcTime(payload.OccurredAt) {
		return checkProgressPayload{}, verification.ErrInvalidCheck
	}
	return payload, nil
}

var _ verificationtask.CoordinationRepository = (*CheckStore)(nil)
