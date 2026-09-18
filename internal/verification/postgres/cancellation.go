package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempotencypostgres "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// StopIdentifiers supplies reference-only lifecycle event identities.
type StopIdentifiers interface{ NewEvent() (id.Event, error) }

// StopStore serializes cancellation and expiry with all consequential session effects.
type StopStore struct {
	pool        transactionRunner
	identifiers StopIdentifiers
	clock       clock.Clock
	lifecycle   *LifecycleStore
}

// NewStopStore constructs cancellation and task-owned expiry persistence.
func NewStopStore(pool transactionRunner, identifiers StopIdentifiers, source clock.Clock) (*StopStore, error) {
	if identifiers == nil {
		return nil, errors.New("verification postgres: stop identifiers are required")
	}
	lifecycle, err := NewLifecycleStore(pool, source)
	if err != nil {
		return nil, err
	}
	return &StopStore{pool: pool, identifiers: identifiers, clock: source, lifecycle: lifecycle}, nil
}

// Cancel retries only serialization failures, preserving the atomic replay result.
func (store *StopStore) Cancel(ctx context.Context, scope tenant.Scope, mutation verification.CancellationMutation) (verification.CancellationResult, error) {
	if scope.ID().IsZero() || mutation.VerificationID.IsZero() || mutation.Retry.TenantID() != scope.ID() || mutation.Retry.Operation() != "verification.cancel" || mutation.Retry.Principal().IsZero() {
		return verification.CancellationResult{}, verification.ErrSessionConflict
	}
	for attempt := 0; attempt < 3; attempt++ {
		var result verification.CancellationResult
		err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
			var err error
			result, err = store.cancelWithin(ctx, tx, scope, mutation)
			return err
		})
		if err == nil {
			return result, nil
		}
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || (databaseError.Code != "40001" && databaseError.Code != "40P01") {
			return verification.CancellationResult{}, err
		}
		if ctx.Err() != nil {
			return verification.CancellationResult{}, ctx.Err()
		}
	}
	return verification.CancellationResult{}, verification.ErrSessionConflict
}

func (store *StopStore) cancelWithin(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, mutation verification.CancellationMutation) (verification.CancellationResult, error) {
	queries := sqlgen.New(tx)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return verification.CancellationResult{}, err
	}
	current, err := lockLifecycle(ctx, tx, scope, mutation.VerificationID)
	if err != nil {
		return verification.CancellationResult{}, err
	}
	if tokenID, err := id.ParseCaptureToken(mutation.Retry.Principal().String()); err == nil {
		token, err := queries.LockCaptureTokenForUpload(ctx, sqlgen.LockCaptureTokenForUploadParams{TenantID: scope.ID().String(), ID: tokenID.String(), VerificationID: mutation.VerificationID.String()})
		if errors.Is(err, pgx.ErrNoRows) {
			return verification.CancellationResult{}, access.ErrInvalidCaptureToken
		}
		if err != nil {
			return verification.CancellationResult{}, fmt.Errorf("lock cancellation credential: %w", err)
		}
		now := store.clock.Now().UTC()
		if token.RevokedAt.Valid || !token.ExpiresAt.Valid || !now.Before(token.ExpiresAt.Time) {
			return verification.CancellationResult{}, access.ErrInvalidCaptureToken
		}
	}
	reservation, err := idempotencypostgres.Reserve(ctx, queries, mutation.Retry)
	if err != nil {
		return verification.CancellationResult{}, err
	}
	if replay, found := reservation.Result(); found {
		var result verification.CancellationResult
		if err := json.Unmarshal(replay.Body(), &result); err != nil {
			return result, fmt.Errorf("decode cancellation receipt: %w", err)
		}
		return result, nil
	}
	eventID, err := store.identifiers.NewEvent()
	if err != nil {
		return verification.CancellationResult{}, err
	}
	command := verification.LifecycleCommand{EventID: eventID, VerificationID: mutation.VerificationID, ExpectedVersion: mutation.ExpectedVersion, Target: verification.SessionStateCancelled, ActorID: mutation.Retry.Principal().String(), OccurredAt: store.clock.Now().UTC().Truncate(time.Microsecond)}
	if current.Version != mutation.ExpectedVersion {
		return verification.CancellationResult{}, verification.ErrSessionConflict
	}
	receipt, err := store.lifecycle.ApplyWithin(ctx, scope, tx, command)
	if err != nil {
		return verification.CancellationResult{}, err
	}
	result := verification.CancellationResult{EventID: receipt.EventID.String(), VerificationID: receipt.VerificationID.String(), State: receipt.To, Version: receipt.Version, OccurredAt: receipt.OccurredAt}
	encoded, err := json.Marshal(result)
	if err != nil {
		return verification.CancellationResult{}, err
	}
	replay, err := idempotency.NewResult(200, encoded)
	if err != nil {
		return verification.CancellationResult{}, err
	}
	if err := idempotencypostgres.Complete(ctx, queries, mutation.Retry, replay, command.OccurredAt); err != nil {
		return verification.CancellationResult{}, err
	}
	return result, nil
}

// ExpireWithin observes the deadline after the parent lock inside a fenced task effect.
// Terminal or not-yet-due sessions are harmless no-ops; the durable sweep retries due work.
func (store *StopStore) ExpireWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, verificationID id.Verification, actor id.Task) error {
	if tx == nil || scope.ID().IsZero() || verificationID.IsZero() || actor.IsZero() {
		return verification.ErrSessionConflict
	}
	if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
		return err
	}
	current, err := lockLifecycle(ctx, tx, scope, verificationID)
	if errors.Is(err, verification.ErrSessionNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	now := store.clock.Now().UTC().Truncate(time.Microsecond)
	if current.State.Terminal() || current.State == verification.SessionStateCreated || now.Before(current.ExpiresAt) {
		return nil
	}
	eventID, err := store.identifiers.NewEvent()
	if err != nil {
		return err
	}
	_, err = store.lifecycle.ApplyWithin(ctx, scope, tx, verification.LifecycleCommand{EventID: eventID, VerificationID: verificationID, ExpectedVersion: current.Version, Target: verification.SessionStateExpired, ActorID: actor.String(), OccurredAt: now})
	return err
}

// CancelForDeletionWithin freezes a linked verification through its owned
// lifecycle while a subject deletion request is committed. Terminal decisions stay immutable.
func (store *StopStore) CancelForDeletionWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, verificationID id.Verification, actor id.APIKey, requestedAt time.Time) error {
	if tx == nil || scope.ID().IsZero() || verificationID.IsZero() || actor.IsZero() || requestedAt.IsZero() {
		return verification.ErrSessionConflict
	}
	current, err := lockLifecycle(ctx, tx, scope, verificationID)
	if err != nil {
		return err
	}
	if current.State.Terminal() {
		return nil
	}
	now := store.clock.Now().UTC().Truncate(time.Microsecond)
	target := verification.SessionStateCancelled
	if !now.Before(current.ExpiresAt) {
		target = verification.SessionStateExpired
	}
	eventID, err := store.identifiers.NewEvent()
	if err != nil {
		return err
	}
	_, err = store.lifecycle.ApplyWithin(ctx, scope, tx, verification.LifecycleCommand{EventID: eventID, VerificationID: verificationID, ExpectedVersion: current.Version, Target: target, ActorID: actor.String(), OccurredAt: now})
	return err
}
