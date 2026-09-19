package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

// ExternalWaitIdentifiers supplies one durable event identity per wait edge.
type ExternalWaitIdentifiers interface {
	NewEvent() (id.Event, error)
}

// pendingExternalOperation reports whether any attempt of the verification owns
// an async coordination row whose dispatch has no terminal result yet.
// provider_dispatches.result_body is the terminal-result marker written when an
// authoritative result is persisted, so its absence is the exact pending state.
const pendingExternalOperationSQL = `SELECT EXISTS (
SELECT 1
FROM idenqa.provider_async_operations operations
JOIN idenqa.verification_attempts attempts
  ON attempts.tenant_id = operations.tenant_id AND attempts.id = operations.attempt_id
JOIN idenqa.provider_dispatches dispatches
  ON dispatches.tenant_id = operations.tenant_id AND dispatches.attempt_id = operations.attempt_id
WHERE operations.tenant_id = $1 AND attempts.verification_id = $2 AND dispatches.result_body IS NULL
)`

// ExternalWaitStore owns the parent-session projection around durable external
// work. Enter joins no task transaction: it runs after an external dispatch is
// known durable. Leave must join the caller's fenced result transaction so a
// projection failure rolls the accepted result back with it.
type ExternalWaitStore struct {
	pool        transactionRunner
	lifecycle   *LifecycleStore
	identifiers ExternalWaitIdentifiers
	clock       clock.Clock
}

// NewExternalWaitStore constructs the owned awaiting-external projection.
func NewExternalWaitStore(pool transactionRunner, lifecycle *LifecycleStore, identifiers ExternalWaitIdentifiers, source clock.Clock) (*ExternalWaitStore, error) {
	if pool == nil || lifecycle == nil || identifiers == nil || source == nil {
		return nil, errors.New("verification postgres: external wait dependencies are required")
	}
	return &ExternalWaitStore{pool: pool, lifecycle: lifecycle, identifiers: identifiers, clock: source}, nil
}

// Enter projects the parent into awaiting_external only when the session is
// still processing, no other local check is runnable, and a durable external
// operation is pending. Every other outcome is a benign no-op.
func (store *ExternalWaitStore) Enter(ctx context.Context, scope tenant.Scope, verificationID id.Verification, checkID id.Check, actor string) error {
	if store == nil || scope.ID().IsZero() || verificationID.IsZero() || checkID.IsZero() {
		return verification.ErrInvalidCheck
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return fmt.Errorf("set external wait tenant scope: %w", err)
		}
		var state verification.SessionState
		var version int64
		var expiresAt time.Time
		err := tx.QueryRow(ctx, `SELECT state, version, expires_at FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`,
			scope.ID().String(), verificationID.String()).Scan(&state, &version, &expiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return verification.ErrSessionNotFound
		}
		if err != nil {
			return fmt.Errorf("load external wait session: %w", err)
		}
		now := store.clock.Now().UTC().Truncate(time.Microsecond)
		if state != verification.SessionStateProcessing || !now.Before(expiresAt.UTC()) {
			return nil
		}
		var runnable bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2 AND id<>$3 AND state IN ('queued','running'))`,
			scope.ID().String(), verificationID.String(), checkID.String()).Scan(&runnable); err != nil {
			return fmt.Errorf("check runnable verification work: %w", err)
		}
		if runnable {
			return nil
		}
		var pending bool
		if err := tx.QueryRow(ctx, pendingExternalOperationSQL, scope.ID().String(), verificationID.String()).Scan(&pending); err != nil {
			return fmt.Errorf("check pending external operations: %w", err)
		}
		if !pending {
			return nil
		}
		eventID, err := store.identifiers.NewEvent()
		if err != nil {
			return fmt.Errorf("generate external wait event: %w", err)
		}
		if _, err := store.lifecycle.ApplyWithin(ctx, scope, tx, verification.LifecycleCommand{
			EventID: eventID, VerificationID: verificationID, ExpectedVersion: version,
			Target: verification.SessionStateAwaitingExternal, ActorID: actor, OccurredAt: now,
		}); err != nil && !errors.Is(err, verification.ErrSessionConflict) {
			return err
		}
		return nil
	})
}

// Leave returns an awaiting_external parent to processing inside the caller's
// fenced transaction. It is a no-op for every other lifecycle state.
func (store *ExternalWaitStore) Leave(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, verificationID id.Verification, actor string) error {
	if store == nil || tx == nil || scope.ID().IsZero() || verificationID.IsZero() {
		return verification.ErrInvalidCheck
	}
	if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
		return fmt.Errorf("set external wait tenant scope: %w", err)
	}
	var state verification.SessionState
	var version int64
	err := tx.QueryRow(ctx, `SELECT state, version FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`,
		scope.ID().String(), verificationID.String()).Scan(&state, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return verification.ErrSessionNotFound
	}
	if err != nil {
		return fmt.Errorf("load external wait session: %w", err)
	}
	if state != verification.SessionStateAwaitingExternal {
		return nil
	}
	eventID, err := store.identifiers.NewEvent()
	if err != nil {
		return fmt.Errorf("generate external resume event: %w", err)
	}
	now := store.clock.Now().UTC().Truncate(time.Microsecond)
	if _, err := store.lifecycle.ApplyWithin(ctx, scope, tx, verification.LifecycleCommand{
		EventID: eventID, VerificationID: verificationID, ExpectedVersion: version,
		Target: verification.SessionStateProcessing, ActorID: actor, OccurredAt: now,
	}); err != nil && !errors.Is(err, verification.ErrSessionConflict) {
		return err
	}
	return nil
}
