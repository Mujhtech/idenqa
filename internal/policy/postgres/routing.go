package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// FindRouting restores a durable nonterminal receipt without consulting current policy.
func (store *Store) FindRouting(ctx context.Context, scope tenant.Scope, requestID id.Decision) (policy.Routing, error) {
	var result policy.Routing
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var err error
		result, err = store.FindRoutingWithin(ctx, scope, tx, requestID)
		return err
	})
	return result, err
}

// FindRoutingWithin joins an existing fenced transaction for exact replay.
func (store *Store) FindRoutingWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, requestID id.Decision) (policy.Routing, error) {
	if tx == nil || scope.ID().IsZero() || requestID.IsZero() {
		return policy.Routing{}, policy.ErrInvalid
	}
	if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
		return policy.Routing{}, err
	}
	var verification, sd, ed, sc, ec string
	var at time.Time
	err := tx.QueryRow(ctx, `SELECT r.verification_id,r.snapshot_digest,r.evaluation_digest,r.requested_at,s.canonical,e.canonical
 FROM idenqa.policy_routing_receipts r
 JOIN idenqa.policy_snapshots s ON s.tenant_id=r.tenant_id AND s.snapshot_digest=r.snapshot_digest
 JOIN idenqa.policy_evaluations e ON e.tenant_id=r.tenant_id AND e.evaluation_digest=r.evaluation_digest
 WHERE r.tenant_id=$1 AND r.request_id=$2`, scope.ID().String(), requestID.String()).Scan(&verification, &sd, &ed, &at, &sc, &ec)
	if errors.Is(err, pgx.ErrNoRows) {
		return policy.Routing{}, policy.ErrDecisionNotFound
	}
	if err != nil {
		return policy.Routing{}, fmt.Errorf("find policy routing: %w", err)
	}
	snapshot, err := policy.RestoreSnapshotCanonical([]byte(sc), sd)
	if err != nil {
		return policy.Routing{}, err
	}
	evaluation, err := policy.RestoreEvaluationCanonical(snapshot, []byte(ec), ed)
	if err != nil {
		return policy.Routing{}, err
	}
	verificationID, err := id.ParseVerification(verification)
	if err != nil {
		return policy.Routing{}, policy.ErrReproduction
	}
	return policy.NewRouting(scope, policy.AuthorRequest{DecisionID: requestID, VerificationID: verificationID, EvaluatedAt: snapshot.EvaluatedAt(), DecidedAt: at.UTC()}, snapshot, evaluation)
}

// AppendRoutingWithin persists only nonterminal provenance. The caller must lock
// and authorize the session and commit its case and lifecycle in this transaction.
func (store *Store) AppendRoutingWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, routing policy.Routing, eventID id.Event) error {
	if tx == nil || eventID.IsZero() {
		return policy.ErrInvalid
	}
	if err := routing.ValidateReplay(scope, routing.Request()); err != nil {
		return err
	}
	queries := sqlgen.New(tx)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return err
	}
	if err := persistSnapshot(ctx, queries, routing.Snapshot()); err != nil {
		return err
	}
	if err := persistEvaluation(ctx, queries, routing.Snapshot(), routing.Evaluation()); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `INSERT INTO idenqa.policy_routing_receipts (tenant_id,request_id,verification_id,snapshot_digest,evaluation_digest,requested_at,lifecycle_event_id)
 VALUES ($1,$2,$3,$4,$5,$6,$7)`, scope.ID().String(), routing.Request().DecisionID.String(), routing.Request().VerificationID.String(), routing.Snapshot().Digest(), routing.Evaluation().Digest(), routing.Request().DecidedAt, eventID.String())
	if err != nil {
		return classifyWrite("insert policy routing receipt", err)
	}
	return nil
}
