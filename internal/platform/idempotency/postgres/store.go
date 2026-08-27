// Package postgres adapts durable idempotency operations to PostgreSQL.
package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// Reservation identifies whether the caller owns execution or must replay a result.
type Reservation struct {
	isOwner bool
	result  idempotency.Result
}

// IsOwner reports whether the caller may execute the mutation.
func (reservation Reservation) IsOwner() bool { return reservation.isOwner }

// Result returns the previously committed result for a replay reservation.
func (reservation Reservation) Result() (idempotency.Result, bool) {
	return reservation.result, !reservation.isOwner
}

type queryer interface {
	TryIdempotencyLock(context.Context, sqlgen.TryIdempotencyLockParams) (bool, error)
	DeleteExpiredIdempotencyRecord(context.Context, sqlgen.DeleteExpiredIdempotencyRecordParams) error
	CreateIdempotencyRecord(context.Context, sqlgen.CreateIdempotencyRecordParams) (int64, error)
	FindIdempotencyRecord(context.Context, sqlgen.FindIdempotencyRecordParams) (sqlgen.IdenqaIdempotencyRecord, error)
	CompleteIdempotencyRecord(
		context.Context,
		sqlgen.CompleteIdempotencyRecordParams,
	) (sqlgen.IdenqaIdempotencyRecord, error)
}

// Reserve obtains a transaction-scoped lock and creates or replays a record.
// The caller must use the same transaction for the domain mutation and Complete.
func Reserve(ctx context.Context, queries queryer, request idempotency.Request) (Reservation, error) {
	if queries == nil {
		return Reservation{}, errors.New("idempotency postgres: queries are required")
	}
	parameters := sqlgen.TryIdempotencyLockParams{
		TenantID:       request.TenantID().String(),
		PrincipalID:    request.Principal().String(),
		Operation:      request.Operation(),
		IdempotencyKey: request.Key(),
	}
	locked, err := queries.TryIdempotencyLock(ctx, parameters)
	if err != nil {
		return Reservation{}, fmt.Errorf("lock idempotency scope: %w", err)
	}
	if !locked {
		return Reservation{}, idempotency.ErrInProgress
	}
	if err := queries.DeleteExpiredIdempotencyRecord(ctx, sqlgen.DeleteExpiredIdempotencyRecordParams{
		TenantID:       parameters.TenantID,
		PrincipalID:    parameters.PrincipalID,
		Operation:      parameters.Operation,
		IdempotencyKey: parameters.IdempotencyKey,
		ExpiresAt:      timestamp(request.CreatedAt()),
	}); err != nil {
		return Reservation{}, fmt.Errorf("delete expired idempotency record: %w", err)
	}
	inserted, err := queries.CreateIdempotencyRecord(ctx, sqlgen.CreateIdempotencyRecordParams{
		TenantID:           parameters.TenantID,
		PrincipalID:        parameters.PrincipalID,
		Operation:          parameters.Operation,
		IdempotencyKey:     parameters.IdempotencyKey,
		RequestFingerprint: request.Fingerprint().Bytes(),
		CreatedAt:          timestamp(request.CreatedAt()),
		ExpiresAt:          timestamp(request.ExpiresAt()),
	})
	if err != nil {
		return Reservation{}, fmt.Errorf("create idempotency record: %w", err)
	}
	if inserted == 1 {
		return Reservation{isOwner: true}, nil
	}

	record, err := queries.FindIdempotencyRecord(ctx, sqlgen.FindIdempotencyRecordParams(parameters))
	if errors.Is(err, pgx.ErrNoRows) {
		return Reservation{}, errors.New("idempotency postgres: conflicting record disappeared")
	}
	if err != nil {
		return Reservation{}, fmt.Errorf("find idempotency record: %w", err)
	}
	if !bytes.Equal(record.RequestFingerprint, request.Fingerprint().Bytes()) {
		return Reservation{}, idempotency.ErrConflict
	}
	if record.State != "completed" || record.ResultStatus == nil || record.Result == nil {
		return Reservation{}, idempotency.ErrInProgress
	}
	result, err := idempotency.ValidateStoredResult(*record.ResultStatus, record.Result)
	if err != nil {
		return Reservation{}, err
	}

	return Reservation{result: result}, nil
}

// Replay returns a completed result without reserving a missing key. It lets
// application services replay before loading state that the original command changed.
func Replay(
	ctx context.Context,
	queries queryer,
	request idempotency.Request,
	now time.Time,
) (idempotency.Result, bool, error) {
	if queries == nil || now.IsZero() {
		return idempotency.Result{}, false, errors.New("idempotency postgres: queries and lookup time are required")
	}
	record, err := queries.FindIdempotencyRecord(ctx, sqlgen.FindIdempotencyRecordParams{
		TenantID:       request.TenantID().String(),
		PrincipalID:    request.Principal().String(),
		Operation:      request.Operation(),
		IdempotencyKey: request.Key(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotency.Result{}, false, nil
	}
	if err != nil {
		return idempotency.Result{}, false, fmt.Errorf("find idempotency replay: %w", err)
	}
	if !record.ExpiresAt.Valid || !record.ExpiresAt.Time.After(now) {
		return idempotency.Result{}, false, nil
	}
	if !bytes.Equal(record.RequestFingerprint, request.Fingerprint().Bytes()) {
		return idempotency.Result{}, false, idempotency.ErrConflict
	}
	if record.State != "completed" || record.ResultStatus == nil || record.Result == nil {
		return idempotency.Result{}, false, idempotency.ErrInProgress
	}
	result, err := idempotency.ValidateStoredResult(*record.ResultStatus, record.Result)
	if err != nil {
		return idempotency.Result{}, false, err
	}

	return result, true, nil
}

// Complete stores the safe result in the reservation's transaction.
func Complete(
	ctx context.Context,
	queries queryer,
	request idempotency.Request,
	result idempotency.Result,
	completedAt time.Time,
) error {
	if completedAt.IsZero() || completedAt.Before(request.CreatedAt()) || completedAt.After(request.ExpiresAt()) {
		return errors.New("idempotency postgres: completion time is outside the reservation lifetime")
	}
	// NewResult constrains status to [200, 599], which is representable by int32.
	status := int32(result.Status()) //nolint:gosec // NewResult enforces the safe status range
	_, err := queries.CompleteIdempotencyRecord(ctx, sqlgen.CompleteIdempotencyRecordParams{
		TenantID:           request.TenantID().String(),
		PrincipalID:        request.Principal().String(),
		Operation:          request.Operation(),
		IdempotencyKey:     request.Key(),
		RequestFingerprint: request.Fingerprint().Bytes(),
		ResultStatus:       &status,
		Result:             result.Body(),
		CompletedAt:        timestamp(completedAt.UTC()),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return idempotency.ErrConflict
	}
	if err != nil {
		return fmt.Errorf("complete idempotency record: %w", err)
	}

	return nil
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: !value.IsZero()}
}
