package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

const lifecycleEvent = "verification.transitioned.v1"

// LifecycleStore is an internal persistence primitive. Owning application
// services must authorise the transition cause and validate current authority.
// It is deliberately not composed into a public state-setting transport.
type LifecycleStore struct {
	pool  transactionRunner
	clock clock.Clock
}

// NewLifecycleStore constructs an adapter with an explicit observation clock.
func NewLifecycleStore(pool transactionRunner, source clock.Clock) (*LifecycleStore, error) {
	if pool == nil || source == nil {
		return nil, errors.New("verification postgres: lifecycle pool and clock are required")
	}
	return &LifecycleStore{pool: pool, clock: source}, nil
}

// Apply commits an already-authorised transition in a serializable transaction.
// Use ApplyWithin when the owning effect also appends tasks or domain records.
func (store *LifecycleStore) Apply(ctx context.Context, scope tenant.Scope, command verification.LifecycleCommand) (verification.LifecycleReceipt, error) {
	var receipt verification.LifecycleReceipt
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var err error
		receipt, err = store.ApplyWithin(ctx, scope, tx, command)
		return err
	})
	if err != nil {
		return verification.LifecycleReceipt{}, err
	}
	return receipt, nil
}

// ApplyWithin joins the owning serializable effect transaction. The caller must
// propagate any error and roll back; a returned receipt is not durable until the
// enclosing commit, including any task intent and execution fence, succeeds.
func (store *LifecycleStore) ApplyWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, command verification.LifecycleCommand) (verification.LifecycleReceipt, error) {
	if tx == nil || scope.ID().IsZero() || command.Validate() != nil {
		return verification.LifecycleReceipt{}, verification.ErrSessionConflict
	}
	var isolation string
	if err := tx.QueryRow(ctx, `SELECT current_setting('transaction_isolation'), set_config('idenqa.tenant_id', $1, true)`, scope.ID().String()).Scan(&isolation, new(string)); err != nil {
		return verification.LifecycleReceipt{}, fmt.Errorf("set lifecycle tenant scope: %w", err)
	}
	if isolation != "serializable" {
		return verification.LifecycleReceipt{}, errors.New("verification postgres: lifecycle effect requires serializable isolation")
	}
	canonical, digest, err := lifecycleCommandDigest(command)
	if err != nil {
		return verification.LifecycleReceipt{}, err
	}
	current, err := lockLifecycle(ctx, tx, scope, command.VerificationID)
	if err != nil {
		return verification.LifecycleReceipt{}, err
	}
	receipt, replay, err := replayLifecycle(ctx, tx, scope, command, digest)
	if err != nil || replay {
		return receipt, err
	}
	observedAt := store.clock.Now().UTC()
	if observedAt.IsZero() || command.OccurredAt.After(observedAt) ||
		(command.Target != verification.SessionStateExpired && !observedAt.Before(current.ExpiresAt)) {
		return verification.LifecycleReceipt{}, verification.ErrSessionConflict
	}
	next, err := verification.AdvanceLifecycle(current, command)
	if err != nil {
		return verification.LifecycleReceipt{}, err
	}
	if err := validateCompletion(ctx, tx, scope, command); err != nil {
		return verification.LifecycleReceipt{}, err
	}
	if err := persistLifecycle(ctx, tx, scope, command, current, next, canonical, digest); err != nil {
		return verification.LifecycleReceipt{}, err
	}
	return lifecycleReceipt(command, current.State), nil
}

func lockLifecycle(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, verificationID id.Verification) (verification.Lifecycle, error) {
	var current verification.Lifecycle
	var decision *string
	err := tx.QueryRow(ctx, `SELECT state, version, created_at, updated_at, expires_at, completed_decision_id
FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), verificationID.String()).Scan(
		&current.State, &current.Version, &current.CreatedAt, &current.UpdatedAt, &current.ExpiresAt, &decision)
	if errors.Is(err, pgx.ErrNoRows) {
		return verification.Lifecycle{}, verification.ErrSessionNotFound
	}
	if err != nil {
		return verification.Lifecycle{}, fmt.Errorf("lock verification lifecycle: %w", err)
	}
	current.CreatedAt, current.UpdatedAt, current.ExpiresAt = current.CreatedAt.UTC(), current.UpdatedAt.UTC(), current.ExpiresAt.UTC()
	if decision != nil {
		current.DecisionID, err = id.ParseDecision(*decision)
		if err != nil {
			return verification.Lifecycle{}, verification.ErrSessionConflict
		}
	}
	return current, nil
}

func replayLifecycle(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, command verification.LifecycleCommand, digest string) (verification.LifecycleReceipt, bool, error) {
	var storedDigest string
	var from verification.SessionState
	err := tx.QueryRow(ctx, `SELECT command_digest, from_state FROM idenqa.verification_transitions
WHERE tenant_id=$1 AND event_id=$2`, scope.ID().String(), command.EventID.String()).Scan(&storedDigest, &from)
	if errors.Is(err, pgx.ErrNoRows) {
		return verification.LifecycleReceipt{}, false, nil
	}
	if err != nil {
		return verification.LifecycleReceipt{}, false, fmt.Errorf("find lifecycle replay: %w", err)
	}
	if storedDigest != digest {
		return verification.LifecycleReceipt{}, false, verification.ErrSessionConflict
	}
	return lifecycleReceipt(command, from), true, nil
}

func validateCompletion(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, command verification.LifecycleCommand) error {
	if command.Target != verification.SessionStateCompleted {
		return nil
	}
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM idenqa.verification_decisions
WHERE tenant_id=$1 AND verification_id=$2 AND id=$3
AND decided_at <= $4 AND outcome IN ('verified', 'not_verified', 'inconclusive'))`,
		scope.ID().String(), command.VerificationID.String(), command.DecisionID.String(), command.OccurredAt).Scan(&exists)
	if err != nil {
		return fmt.Errorf("validate lifecycle completion decision: %w", err)
	}
	if !exists {
		return verification.ErrSessionConflict
	}
	return nil
}

func persistLifecycle(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, command verification.LifecycleCommand, current, next verification.Lifecycle, canonical []byte, digest string) error {
	var decision *string
	if !command.DecisionID.IsZero() {
		value := command.DecisionID.String()
		decision = &value
	}
	result, err := tx.Exec(ctx, `UPDATE idenqa.verification_sessions SET state=$3, version=$4, updated_at=$5, completed_decision_id=$6
WHERE tenant_id=$1 AND id=$2 AND version=$7`, scope.ID().String(), command.VerificationID.String(), next.State, next.Version, next.UpdatedAt, decision, command.ExpectedVersion)
	if err != nil {
		return fmt.Errorf("update verification lifecycle: %w", err)
	}
	if result.RowsAffected() != 1 {
		return verification.ErrSessionConflict
	}
	_, err = tx.Exec(ctx, `INSERT INTO idenqa.verification_transitions
(tenant_id, event_id, verification_id, from_state, to_state, expected_version, resulting_version, decision_id, actor_id, command_digest, occurred_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, scope.ID().String(), command.EventID.String(), command.VerificationID.String(),
		current.State, next.State, current.Version, next.Version, decision, command.ActorID, digest, command.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert lifecycle receipt: %w", err)
	}
	_, err = tx.Exec(ctx, `INSERT INTO idenqa.outbox_events
(id, tenant_id, aggregate_type, aggregate_id, aggregate_version, event_type, schema_version, payload, occurred_at, created_at)
VALUES ($1,$2,'verification',$3,$4,$5,1,$6,$7,$7)`, command.EventID.String(), scope.ID().String(), command.VerificationID.String(),
		next.Version, lifecycleEvent, string(canonical), command.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert lifecycle outbox: %w", err)
	}
	_, err = auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{
		EventID: command.EventID.String(), EventType: lifecycleEvent, AggregateID: command.VerificationID.String(),
		ActorID: command.ActorID, EventDigest: digest, OccurredAt: command.OccurredAt,
	})
	return err
}

func lifecycleCommandDigest(command verification.LifecycleCommand) ([]byte, string, error) {
	encoded, err := json.Marshal(struct {
		EventID         string                    `json:"event_id"`
		VerificationID  string                    `json:"verification_id"`
		ExpectedVersion int64                     `json:"expected_version"`
		Target          verification.SessionState `json:"target"`
		DecisionID      string                    `json:"decision_id,omitempty"`
		ActorID         string                    `json:"actor_id"`
		OccurredAt      time.Time                 `json:"occurred_at"`
	}{command.EventID.String(), command.VerificationID.String(), command.ExpectedVersion,
		command.Target, command.DecisionID.String(), command.ActorID, command.OccurredAt})
	if err != nil {
		return nil, "", fmt.Errorf("encode lifecycle command: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(digest[:]), nil
}

func lifecycleReceipt(command verification.LifecycleCommand, from verification.SessionState) verification.LifecycleReceipt {
	return verification.LifecycleReceipt{EventID: command.EventID, VerificationID: command.VerificationID,
		From: from, To: command.Target, Version: command.ExpectedVersion + 1,
		DecisionID: command.DecisionID, OccurredAt: command.OccurredAt}
}
