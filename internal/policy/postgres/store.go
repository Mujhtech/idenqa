// Package postgres persists immutable policy snapshots and decision lineage.
package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type transactionRunner interface {
	WithinTransaction(
		context.Context,
		platformpostgres.TransactionOptions,
		func(context.Context, platformpostgres.Transaction) error,
	) error
}

// Store is the PostgreSQL adapter for immutable decision lineage.
type Store struct {
	pool  transactionRunner
	clock clock.Clock
}

var _ policy.Repository = (*Store)(nil)
var _ policy.CatalogRepository = (*Store)(nil)

// New constructs the persistence primitive. Callers own lifecycle and
// authority validation; runnable policy authors use NewGuarded.
func New(pool transactionRunner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("policy postgres: pool is required")
	}
	return &Store{pool: pool}, nil
}

// NewGuarded rechecks lifecycle and current processing authority in the same
// transaction as each newly authored decision.
func NewGuarded(pool transactionRunner, source clock.Clock) (*Store, error) {
	store, err := New(pool)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, errors.New("policy postgres: processing clock is required")
	}
	store.clock = source
	return store, nil
}

// Append atomically persists a snapshot, evaluation, and one decision.
func (store *Store) Append(ctx context.Context, scope tenant.Scope, decision policy.Decision) error {
	if err := validateAppend(scope, decision); err != nil {
		return err
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		return store.AppendWithin(ctx, scope, tx, decision)
	})
}

// AppendWithin persists one decision using a caller-owned fenced transaction.
func (store *Store) AppendWithin(
	ctx context.Context,
	scope tenant.Scope,
	transaction platformpostgres.Transaction,
	decision policy.Decision,
) error {
	if transaction == nil {
		return policy.ErrDecisionConflict
	}
	if err := validateAppend(scope, decision); err != nil {
		return err
	}
	queries := sqlgen.New(transaction)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return fmt.Errorf("set policy tenant scope: %w", err)
	}
	if store.clock != nil {
		// Exact committed decisions remain replayable when the lifecycle has
		// advanced. This read does not author a new snapshot or decision.
		previous, err := findDecision(ctx, queries, scope, decision.ID())
		if err == nil {
			if previous.Digest() != decision.Digest() || !bytes.Equal(previous.Canonical(), decision.Canonical()) {
				return policy.ErrDecisionConflict
			}
			return nil
		}
		if !errors.Is(err, policy.ErrDecisionNotFound) {
			return err
		}
		if err := ValidateDecisionAssuranceWithin(ctx, transaction, scope, decision.Snapshot(), decision.Evaluation().Outcome() == policy.OutcomeVerified, store.clock.Now().UTC()); err != nil {
			return err
		}
		expected := verification.SessionStateProcessing
		state, err := reviewSessionState(ctx, scope, transaction, decision.Snapshot().VerificationID())
		if err != nil {
			return err
		}
		if state == string(verification.SessionStateManualReview) {
			if err := ValidateReviewDecisionWithin(ctx, scope, transaction, decision); err != nil {
				return err
			}
			expected = verification.SessionStateManualReview
		}
		if err := authoritypostgres.ValidateProcessingWithin(ctx, transaction, scope, decision.Snapshot().VerificationID(),
			decision.DecidedAt(), store.clock, expected); err != nil {
			return err
		}
	}
	return appendDecision(ctx, queries, scope, decision)
}

func validateAppend(scope tenant.Scope, decision policy.Decision) error {
	snapshot := decision.Snapshot()
	evaluation := decision.Evaluation()
	if scope.ID().IsZero() || decision.ID().IsZero() || snapshot.TenantID().String() != scope.ID().String() ||
		decision.Digest() == "" || evaluation.Digest() == "" {
		return policy.ErrDecisionConflict
	}
	if _, err := policy.RestoreDecisionCanonical(
		snapshot,
		evaluation,
		decision.Canonical(),
		decision.Digest(),
	); err != nil {
		return policy.ErrDecisionConflict
	}
	return nil
}

func appendDecision(
	ctx context.Context,
	queries *sqlgen.Queries,
	scope tenant.Scope,
	decision policy.Decision,
) error {
	snapshot := decision.Snapshot()
	if !decision.Supersedes().IsZero() {
		previous, err := queries.LockPolicyDecision(ctx, sqlgen.LockPolicyDecisionParams{
			TenantID: scope.ID().String(), ID: decision.Supersedes().String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return policy.ErrDecisionConflict
		}
		if err != nil {
			return fmt.Errorf("lock superseded policy decision: %w", err)
		}
		if previous.VerificationID != snapshot.VerificationID().String() || !previous.DecidedAt.Valid ||
			decision.DecidedAt().Before(previous.DecidedAt.Time) {
			return policy.ErrDecisionConflict
		}
	}
	if err := persistSnapshot(ctx, queries, snapshot); err != nil {
		return err
	}
	if err := persistEvaluation(ctx, queries, snapshot, decision.Evaluation()); err != nil {
		return err
	}
	return persistDecision(ctx, queries, decision)
}

// Find restores one exact decision visible in the explicit tenant scope.
func (store *Store) Find(
	ctx context.Context,
	scope tenant.Scope,
	decisionID id.Decision,
) (policy.Decision, error) {
	if scope.ID().IsZero() || decisionID.IsZero() {
		return policy.Decision{}, policy.ErrDecisionNotFound
	}
	var decision policy.Decision
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		var err error
		decision, err = findDecision(ctx, queries, scope, decisionID)
		return err
	})
	return decision, err
}

// FindWithin restores one exact decision using a caller-owned transaction.
func (store *Store) FindWithin(
	ctx context.Context,
	scope tenant.Scope,
	transaction platformpostgres.Transaction,
	decisionID id.Decision,
) (policy.Decision, error) {
	if transaction == nil || scope.ID().IsZero() || decisionID.IsZero() {
		return policy.Decision{}, policy.ErrDecisionNotFound
	}
	queries := sqlgen.New(transaction)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return policy.Decision{}, fmt.Errorf("set policy tenant scope: %w", err)
	}
	return findDecision(ctx, queries, scope, decisionID)
}

func findDecision(
	ctx context.Context,
	queries *sqlgen.Queries,
	scope tenant.Scope,
	decisionID id.Decision,
) (policy.Decision, error) {
	row, err := queries.FindPolicyDecisionBundle(ctx, sqlgen.FindPolicyDecisionBundleParams{
		TenantID: scope.ID().String(), ID: decisionID.String(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return policy.Decision{}, policy.ErrDecisionNotFound
	}
	if err != nil {
		return policy.Decision{}, fmt.Errorf("find policy decision: %w", err)
	}
	return restoreDecision(decisionRecord{
		ID: row.ID, TenantID: row.TenantID, VerificationID: row.VerificationID,
		SnapshotDigest: row.SnapshotDigest, EvaluationDigest: row.EvaluationDigest,
		DecisionDigest: row.DecisionDigest, Selected: row.Selected, Outcome: row.Outcome,
		Actor: row.Actor, SupersedesID: row.SupersedesID,
		DecidedAt: row.DecidedAt, Canonical: row.Canonical,
		SnapshotCanonical: row.SnapshotCanonical, EvaluationCanonical: row.EvaluationCanonical,
	})
}

// FindLatest restores the latest decision in one tenant-scoped linear lineage.
func (store *Store) FindLatest(
	ctx context.Context,
	scope tenant.Scope,
	verificationID id.Verification,
) (policy.Decision, error) {
	if scope.ID().IsZero() || verificationID.IsZero() {
		return policy.Decision{}, policy.ErrDecisionNotFound
	}
	var decision policy.Decision
	err := store.read(ctx, scope, func(ctx context.Context, queries *sqlgen.Queries) error {
		row, err := queries.FindLatestPolicyDecisionBundle(ctx, sqlgen.FindLatestPolicyDecisionBundleParams{
			TenantID: scope.ID().String(), VerificationID: verificationID.String(),
		})
		if errors.Is(err, pgx.ErrNoRows) {
			return policy.ErrDecisionNotFound
		}
		if err != nil {
			return fmt.Errorf("find latest policy decision: %w", err)
		}
		decision, err = restoreDecision(decisionRecord{
			ID: row.ID, TenantID: row.TenantID, VerificationID: row.VerificationID,
			SnapshotDigest: row.SnapshotDigest, EvaluationDigest: row.EvaluationDigest,
			DecisionDigest: row.DecisionDigest, Selected: row.Selected, Outcome: row.Outcome,
			Actor: row.Actor, SupersedesID: row.SupersedesID,
			DecidedAt: row.DecidedAt, Canonical: row.Canonical,
			SnapshotCanonical: row.SnapshotCanonical, EvaluationCanonical: row.EvaluationCanonical,
		})
		return err
	})
	return decision, err
}

func persistSnapshot(ctx context.Context, queries *sqlgen.Queries, snapshot policy.Snapshot) error {
	reference, evaluator := snapshot.Policy(), snapshot.Evaluator()
	rows, err := queries.InsertPolicySnapshot(ctx, sqlgen.InsertPolicySnapshotParams{
		TenantID: snapshot.TenantID().String(), VerificationID: snapshot.VerificationID().String(),
		SnapshotDigest: snapshot.Digest(), PolicyID: reference.ID.String(),
		PolicyRevision: int64(reference.Revision), PolicySchemaMajor: int32(reference.SchemaMajor),
		PolicySchemaMinor: int32(reference.SchemaMinor), PolicyDigest: reference.Digest,
		EvaluatorMajor: int32(evaluator.Major), EvaluatorMinor: int32(evaluator.Minor),
		EvaluatorDigest: evaluator.Digest, AuthorityID: snapshot.AuthorityID().String(),
		AcknowledgementID: snapshot.AcknowledgementID().String(), Region: snapshot.Region(),
		EvaluatedAt: timestamp(snapshot.EvaluatedAt()), Canonical: string(snapshot.Canonical()),
	})
	if err != nil {
		return classifyWrite("insert policy snapshot", err)
	}
	if rows == 1 {
		return nil
	}
	stored, err := queries.FindPolicySnapshot(ctx, sqlgen.FindPolicySnapshotParams{
		TenantID: snapshot.TenantID().String(), SnapshotDigest: snapshot.Digest(),
	})
	if err != nil || stored.VerificationID != snapshot.VerificationID().String() ||
		!bytes.Equal([]byte(stored.Canonical), snapshot.Canonical()) {
		return policy.ErrDecisionConflict
	}
	return nil
}

func persistEvaluation(
	ctx context.Context,
	queries *sqlgen.Queries,
	snapshot policy.Snapshot,
	evaluation policy.Evaluation,
) error {
	var outcome, assurance *string
	if evaluation.Outcome() != "" {
		value := string(evaluation.Outcome())
		outcome = &value
	}
	if evaluation.Assurance() != "" {
		value := evaluation.Assurance()
		assurance = &value
	}
	rows, err := queries.InsertPolicyEvaluation(ctx, sqlgen.InsertPolicyEvaluationParams{
		TenantID: snapshot.TenantID().String(), VerificationID: snapshot.VerificationID().String(),
		SnapshotDigest: snapshot.Digest(), EvaluationDigest: evaluation.Digest(),
		Selected: string(evaluation.Selected()), Outcome: outcome, Assurance: assurance,
		Canonical: string(evaluation.Canonical()),
	})
	if err != nil {
		return classifyWrite("insert policy evaluation", err)
	}
	if rows == 1 {
		return nil
	}
	stored, err := queries.FindPolicyEvaluation(ctx, sqlgen.FindPolicyEvaluationParams{
		TenantID: snapshot.TenantID().String(), EvaluationDigest: evaluation.Digest(),
	})
	if err != nil || stored.VerificationID != snapshot.VerificationID().String() ||
		stored.SnapshotDigest != snapshot.Digest() || !bytes.Equal([]byte(stored.Canonical), evaluation.Canonical()) {
		return policy.ErrDecisionConflict
	}
	return nil
}

func persistDecision(ctx context.Context, queries *sqlgen.Queries, decision policy.Decision) error {
	snapshot, evaluation := decision.Snapshot(), decision.Evaluation()
	var supersedes *string
	if !decision.Supersedes().IsZero() {
		value := decision.Supersedes().String()
		supersedes = &value
	}
	rows, err := queries.InsertPolicyDecision(ctx, sqlgen.InsertPolicyDecisionParams{
		ID: decision.ID().String(), TenantID: snapshot.TenantID().String(),
		VerificationID: snapshot.VerificationID().String(), SnapshotDigest: snapshot.Digest(),
		EvaluationDigest: evaluation.Digest(), DecisionDigest: decision.Digest(),
		Selected: string(evaluation.Selected()), Outcome: string(evaluation.Outcome()),
		Actor: string(decision.Actor()), SupersedesID: supersedes,
		DecidedAt: timestamp(decision.DecidedAt()), Canonical: string(decision.Canonical()),
	})
	if err != nil {
		return classifyWrite("insert policy decision", err)
	}
	if rows == 1 {
		return nil
	}
	row, err := queries.FindPolicyDecisionBundle(ctx, sqlgen.FindPolicyDecisionBundleParams{
		TenantID: snapshot.TenantID().String(), ID: decision.ID().String(),
	})
	if err != nil || row.DecisionDigest != decision.Digest() ||
		!bytes.Equal([]byte(row.Canonical), decision.Canonical()) {
		return policy.ErrDecisionConflict
	}
	return nil
}

type decisionRecord struct {
	ID                  string
	TenantID            string
	VerificationID      string
	SnapshotDigest      string
	EvaluationDigest    string
	DecisionDigest      string
	Selected            string
	Outcome             string
	Actor               string
	SupersedesID        *string
	DecidedAt           pgtype.Timestamptz
	Canonical           string
	SnapshotCanonical   string
	EvaluationCanonical string
}

func restoreDecision(row decisionRecord) (policy.Decision, error) {
	if !row.DecidedAt.Valid {
		return policy.Decision{}, policy.ErrReproduction
	}
	snapshot, err := policy.RestoreSnapshotCanonical([]byte(row.SnapshotCanonical), row.SnapshotDigest)
	if err != nil || snapshot.TenantID().String() != row.TenantID ||
		snapshot.VerificationID().String() != row.VerificationID {
		return policy.Decision{}, policy.ErrReproduction
	}
	evaluation, err := policy.RestoreEvaluationCanonical(
		snapshot,
		[]byte(row.EvaluationCanonical),
		row.EvaluationDigest,
	)
	if err != nil {
		return policy.Decision{}, policy.ErrReproduction
	}
	decision, err := policy.RestoreDecisionCanonical(
		snapshot,
		evaluation,
		[]byte(row.Canonical),
		row.DecisionDigest,
	)
	if err != nil || decision.ID().String() != row.ID || string(decision.Actor()) != row.Actor ||
		string(decision.Evaluation().Selected()) != row.Selected || string(decision.Evaluation().Outcome()) != row.Outcome ||
		!decision.DecidedAt().Equal(row.DecidedAt.Time) || !sameSupersedes(decision.Supersedes(), row.SupersedesID) {
		return policy.Decision{}, policy.ErrReproduction
	}
	return decision, nil
}

func sameSupersedes(identifier id.Decision, encoded *string) bool {
	if identifier.IsZero() {
		return encoded == nil
	}
	return encoded != nil && identifier.String() == *encoded
}

func classifyWrite(operation string, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && len(postgresError.Code) >= 2 && postgresError.Code[:2] == "23" {
		return policy.ErrDecisionConflict
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func timestamp(value time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: value, Valid: true}
}

func (store *Store) read(
	ctx context.Context,
	scope tenant.Scope,
	work func(context.Context, *sqlgen.Queries) error,
) error {
	return store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, transaction platformpostgres.Transaction) error {
			queries := sqlgen.New(transaction)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return fmt.Errorf("set policy tenant scope: %w", err)
			}
			return work(ctx, queries)
		},
	)
}

func (store *Store) write(
	ctx context.Context,
	scope tenant.Scope,
	work func(context.Context, *sqlgen.Queries) error,
) error {
	return store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationReadCommitted},
		func(ctx context.Context, transaction platformpostgres.Transaction) error {
			queries := sqlgen.New(transaction)
			if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
				return fmt.Errorf("set policy tenant scope: %w", err)
			}
			return work(ctx, queries)
		},
	)
}
