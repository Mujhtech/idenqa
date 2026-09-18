package postgres

import (
	"context"
	"errors"

	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

type reviewCompletion interface {
	CompleteWithin(context.Context, tenant.Scope, platformpostgres.Transaction, policy.Decision, id.Task) error
}

// EvaluationStore builds and commits review results through owned policy ports.
type EvaluationStore struct {
	pool       transactionRunner
	cases      *Store
	policies   *policypostgres.Store
	evaluator  policy.Evaluator
	completion reviewCompletion
	clock      clock.Clock
}

// NewEvaluationStore requires the same completion adapter used for ordinary decisions.
func NewEvaluationStore(pool transactionRunner, wrapper platformcrypto.KeyWrapper, evaluator policy.Evaluator, completion reviewCompletion, source clock.Clock) (*EvaluationStore, error) {
	if evaluator == nil || completion == nil || source == nil {
		return nil, review.ErrInvalid
	}
	cases, err := NewWithClock(pool, wrapper, source)
	if err != nil {
		return nil, err
	}
	policies, err := policypostgres.NewGuarded(pool, wrapper, source)
	if err != nil {
		return nil, err
	}
	return &EvaluationStore{pool: pool, cases: cases, policies: policies, evaluator: evaluator, completion: completion, clock: source}, nil
}

// Build evaluates the original pinned policy using immutable accepted findings.
func (store *EvaluationStore) Build(ctx context.Context, scope tenant.Scope, request review.EvaluationRequest) (review.Evaluation, error) {
	var value review.Case
	var snapshot policy.Snapshot
	var digest string
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var err error
		value, err = store.cases.findCaseWithin(ctx, scope, tx, request.CaseID)
		if err != nil {
			return err
		}
		if value.Version != request.Version {
			return review.ErrConflict
		}
		routing, err := store.policies.FindRoutingWithin(ctx, scope, tx, value.RoutingRequest)
		if err != nil {
			return err
		}
		snapshot, digest, err = store.snapshotWithin(ctx, scope, tx, value, routing)
		return err
	})
	if err != nil {
		return review.Evaluation{}, err
	}
	output, err := store.evaluator.Evaluate(ctx, snapshot)
	if err != nil {
		return review.Evaluation{}, err
	}
	result, err := policy.Resolve(snapshot, output.Results, output.Assurance)
	if err != nil {
		return review.Evaluation{}, err
	}
	return review.Evaluation{Request: request, DecisionID: value.RoutingRequest, Snapshot: snapshot, Result: result, CaseDigest: digest}, nil
}

// Find restores committed evaluation without rereading current policy or authority.
func (store *EvaluationStore) Find(ctx context.Context, scope tenant.Scope, request review.EvaluationRequest) (review.Evaluation, error) {
	var result review.Evaluation
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var err error
		result, err = store.findWithin(ctx, scope, tx, request)
		return err
	})
	return result, err
}

func (store *EvaluationStore) findWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, request review.EvaluationRequest) (review.Evaluation, error) {
	if request.CaseID.IsZero() || request.Version < 1 {
		return review.Evaluation{}, review.ErrInvalid
	}
	if err := setScope(ctx, tx, scope); err != nil {
		return review.Evaluation{}, err
	}
	var decision, sd, ed, sc, ec, digest string
	err := tx.QueryRow(ctx, `SELECT c.routing_request_id,e.snapshot_digest,e.evaluation_digest,s.canonical,p.canonical,e.case_digest
 FROM idenqa.review_evaluations e JOIN idenqa.review_cases c ON c.tenant_id=e.tenant_id AND c.id=e.case_id
 JOIN idenqa.policy_snapshots s ON s.tenant_id=e.tenant_id AND s.snapshot_digest=e.snapshot_digest
 JOIN idenqa.policy_evaluations p ON p.tenant_id=e.tenant_id AND p.evaluation_digest=e.evaluation_digest
 WHERE e.tenant_id=$1 AND e.case_id=$2 AND e.case_version=$3`, scope.ID().String(), request.CaseID.String(), request.Version).Scan(&decision, &sd, &ed, &sc, &ec, &digest)
	if errors.Is(err, pgx.ErrNoRows) {
		return review.Evaluation{}, review.ErrEvaluationNotFound
	}
	if err != nil {
		return review.Evaluation{}, err
	}
	snapshot, err := policy.RestoreSnapshotCanonical([]byte(sc), sd)
	if err != nil {
		return review.Evaluation{}, err
	}
	result, err := policy.RestoreEvaluationCanonical(snapshot, []byte(ec), ed)
	if err != nil {
		return review.Evaluation{}, err
	}
	decisionID, err := id.ParseDecision(decision)
	if err != nil || snapshot.TenantID() != scope.ID() {
		return review.Evaluation{}, policy.ErrReproduction
	}
	return review.Evaluation{Request: request, DecisionID: decisionID, Snapshot: snapshot, Result: result, CaseDigest: digest}, nil
}

// CommitWithin joins Headgate's serializable fenced effect and never trusts a caller outcome.
func (store *EvaluationStore) CommitWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, candidate review.Evaluation, actor id.Task) error {
	if tx == nil || actor.IsZero() || candidate.Snapshot.TenantID() != scope.ID() {
		return review.ErrInvalid
	}
	if err := setScope(ctx, tx, scope); err != nil {
		return err
	}
	var state string
	if err := tx.QueryRow(ctx, `SELECT state FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), candidate.Snapshot.VerificationID().String()).Scan(&state); err != nil {
		return err
	}
	saved, err := store.findWithin(ctx, scope, tx, candidate.Request)
	if err == nil {
		if saved.Snapshot.Digest() != candidate.Snapshot.Digest() || saved.Result.Digest() != candidate.Result.Digest() || saved.CaseDigest != candidate.CaseDigest || saved.DecisionID != candidate.DecisionID {
			return review.ErrConflict
		}
		if saved.Result.AuthorisesCompletion() {
			decision, err := store.policies.FindWithin(ctx, scope, tx, saved.DecisionID)
			if err != nil {
				return err
			}
			expected, err := saved.Decision()
			if err != nil {
				return err
			}
			if decision.Digest() != expected.Digest() {
				return policy.ErrReproduction
			}
			return store.completion.CompleteWithin(ctx, scope, tx, decision, actor)
		}
		return nil
	}
	if !errors.Is(err, review.ErrEvaluationNotFound) {
		return err
	}
	if state != string(verification.SessionStateManualReview) {
		return review.ErrConflict
	}
	if err := authoritypostgres.ValidateProcessingWithin(ctx, tx, scope, candidate.Snapshot.VerificationID(), candidate.Snapshot.EvaluatedAt(), store.clock, verification.SessionStateManualReview); err != nil {
		return err
	}
	// Reconstruct the accepted case and routed inputs inside the effect transaction.
	value, err := store.cases.findCaseWithin(ctx, scope, tx, candidate.Request.CaseID)
	if err != nil {
		return err
	}
	if value.Version != candidate.Request.Version {
		return review.ErrConflict
	}
	routing, err := store.policies.FindRoutingWithin(ctx, scope, tx, value.RoutingRequest)
	if err != nil {
		return err
	}
	snapshot, digest, err := store.snapshotWithin(ctx, scope, tx, value, routing)
	if err != nil {
		return err
	}
	if snapshot.Digest() != candidate.Snapshot.Digest() || digest != candidate.CaseDigest || value.RoutingRequest != candidate.DecisionID {
		return review.ErrConflict
	}
	if _, err := policy.RestoreEvaluationCanonical(snapshot, candidate.Result.Canonical(), candidate.Result.Digest()); err != nil {
		return err
	}
	if err := store.policies.AppendEvaluationWithin(ctx, scope, tx, snapshot, candidate.Result); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_evaluations(tenant_id,case_id,case_version,verification_id,snapshot_digest,evaluation_digest,case_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.ID().String(), value.ID.String(), value.Version, value.VerificationID.String(), snapshot.Digest(), candidate.Result.Digest(), digest)
	if err != nil {
		return err
	}
	if err := appendReviewAudit(ctx, tx, scope, review.Actor{ID: actor.String()}, value.ID.String(), "review.case.evaluated", store.clock.Now().UTC(), value.Version); err != nil {
		return err
	}
	if candidate.Result.AuthorisesCompletion() {
		decision, err := candidate.Decision()
		if err != nil {
			return err
		}
		if err := store.policies.AppendWithin(ctx, scope, tx, decision); err != nil {
			return err
		}
		return store.completion.CompleteWithin(ctx, scope, tx, decision, actor)
	}
	return nil
}
