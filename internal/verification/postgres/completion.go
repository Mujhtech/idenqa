package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// CompletionIdentifiers creates identities only inside a new completion effect.
type CompletionIdentifiers interface {
	NewEvent() (id.Event, error)
}

// CompletionStore commits workflow completion and one subscribed catalogue
// event atomically with the owning policy author and Headgate completion fence.
type CompletionStore struct {
	identifiers CompletionIdentifiers
	clock       clock.Clock
	lifecycle   *LifecycleStore
	wrapper     platformcrypto.KeyWrapper
	metrics     Metrics
}

// NewCompletionStore constructs the internal machine-decision completion adapter.
func NewCompletionStore(pool transactionRunner, wrapper platformcrypto.KeyWrapper, identifiers CompletionIdentifiers, source clock.Clock) (*CompletionStore, error) {
	if identifiers == nil || source == nil {
		return nil, errors.New("verification postgres: completion dependencies are required")
	}
	lifecycle, err := NewLifecycleStore(pool, wrapper, source)
	if err != nil {
		return nil, err
	}
	return &CompletionStore{identifiers: identifiers, clock: source, lifecycle: lifecycle, wrapper: wrapper}, nil
}

// WithMetrics attaches the bounded verification metric receiver to completion
// and to the lifecycle store it owns.
func (store *CompletionStore) WithMetrics(metrics Metrics) *CompletionStore {
	if store != nil && metrics != nil {
		store.metrics = metrics
		store.lifecycle.WithMetrics(metrics)
	}
	return store
}

// completionChecks builds the per-check snapshot for the completion event.
func completionChecks(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, verificationID id.Verification) ([]map[string]any, error) {
	rows, err := tx.Query(ctx, `SELECT id,state,COALESCE(outcome,'') FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2 ORDER BY id`, scope.ID().String(), verificationID.String())
	if err != nil {
		return nil, fmt.Errorf("load completion checks: %w", err)
	}
	defer rows.Close()
	checks := []map[string]any{}
	for rows.Next() {
		var identifier, state, outcome string
		if err := rows.Scan(&identifier, &state, &outcome); err != nil {
			return nil, err
		}
		check := map[string]any{"id": identifier, "state": state}
		if outcome != "" {
			check["outcome"] = outcome
		}
		checks = append(checks, check)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read completion checks: %w", err)
	}

	return checks, nil
}

// CompleteWithin requires the same transaction that appended or verified the
// immutable decision. Exact completed replay is a no-op even after expiry or
// endpoint changes; it never retroactively fans an old event to new subscribers.
func (store *CompletionStore) CompleteWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, decision policy.Decision, actor id.Task) error {
	if tx == nil || actor.IsZero() || decision.ID().IsZero() || scope.ID().IsZero() || decision.Snapshot().TenantID() != scope.ID() {
		return policy.ErrInvalid
	}
	queries := sqlgen.New(tx)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return fmt.Errorf("scope verification completion: %w", err)
	}
	verificationID := decision.Snapshot().VerificationID()
	session, err := queries.LockVerificationForUpload(ctx, sqlgen.LockVerificationForUploadParams{TenantID: scope.ID().String(), ID: verificationID.String()})
	if err != nil {
		return fmt.Errorf("lock verification completion: %w", err)
	}
	if session.DecisionID == nil || *session.DecisionID != decision.ID().String() ||
		session.PolicyID == nil || *session.PolicyID != decision.Snapshot().Policy().ID.String() ||
		session.AuthorityID == nil || *session.AuthorityID != decision.Snapshot().AuthorityID().String() ||
		session.Region == nil || *session.Region != decision.Snapshot().Region() ||
		!decision.Supersedes().IsZero() || decision.Actor() != policy.ActorMachine || !decision.Evaluation().AuthorisesCompletion() {
		return policy.ErrInvalid
	}
	if session.State == string(verification.SessionStateCompleted) {
		if session.CompletedDecisionID == nil || *session.CompletedDecisionID != decision.ID().String() {
			return policy.ErrDecisionConflict
		}
		return nil
	}
	now := store.clock.Now().UTC().Truncate(time.Microsecond)
	if decision.DecidedAt().After(now) {
		return policy.ErrInvalid
	}
	if err := policypostgres.ValidateDecisionAssuranceWithin(ctx, tx, scope, decision.Snapshot(), decision.Evaluation().Outcome() == policy.OutcomeVerified, now); err != nil {
		return err
	}
	expected := verification.SessionStateProcessing
	if session.State == string(verification.SessionStateManualReview) {
		if err := policypostgres.ValidateReviewDecisionWithin(ctx, scope, tx, decision); err != nil {
			return err
		}
		expected = verification.SessionStateManualReview
	}
	if err := authoritypostgres.ValidateProcessingWithin(ctx, tx, scope, verificationID, now, store.clock, expected); err != nil {
		return err
	}
	var ready bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2)
AND NOT EXISTS(SELECT 1 FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2
AND state NOT IN ('completed','skipped_by_policy','timed_out','cancelled','failed'))`, scope.ID().String(), verificationID.String()).Scan(&ready); err != nil {
		return fmt.Errorf("check completion readiness: %w", err)
	}
	if !ready {
		return policy.ErrInvalid
	}
	eventID, err := store.identifiers.NewEvent()
	if err != nil {
		return fmt.Errorf("generate completion event: %w", err)
	}
	receipt, err := store.lifecycle.ApplyWithin(ctx, scope, tx, verification.LifecycleCommand{EventID: eventID, VerificationID: verificationID, ExpectedVersion: session.Version, Target: verification.SessionStateCompleted, DecisionID: decision.ID(), ActorID: actor.String(), OccurredAt: now})
	if err != nil {
		return err
	}
	if session.SubjectID == nil || session.Region == nil {
		return policy.ErrInvalid
	}
	seed := "verification.completed:" + verificationID.String() + ":" + decision.ID().String()
	checks, err := completionChecks(ctx, tx, scope, verificationID)
	if err != nil {
		return err
	}
	data, err := delivery.EventData(map[string]any{
		"verification_id": verificationID.String(),
		"subject_id":      *session.SubjectID,
		"decision_id":     decision.ID().String(),
		"outcome":         string(decision.Evaluation().Outcome()),
		"decided_at":      decision.DecidedAt().UTC().Format(time.RFC3339),
		"verification": map[string]any{
			"id":               verificationID.String(),
			"type":             "verification.session",
			"status":           string(verification.SessionStateCompleted),
			"outcome":          string(decision.Evaluation().Outcome()),
			"created_at":       session.CreatedAt.Time.UTC().Format(time.RFC3339),
			"completed_at":     receipt.OccurredAt.UTC().Format(time.RFC3339),
			"decision_id":      decision.ID().String(),
			"decision_outcome": string(decision.Evaluation().Outcome()),
			"checks":           checks,
		},
	})
	if err != nil {
		return err
	}
	event, err := webhookv1.NewEvent(webhookv1.DeterministicEventID(seed), scope.ID().String(), *session.Region, webhookv1.VerificationCompleted, webhookv1.SchemaVersion, receipt.OccurredAt, data)
	if err != nil {
		return err
	}
	if _, err := deliverypostgres.EmitEventWithin(ctx, tx, store.wrapper, event, seed); err != nil {
		return fmt.Errorf("emit completion event: %w", err)
	}
	if store.metrics != nil {
		createdAt := session.CreatedAt.Time.UTC()
		duration := receipt.OccurredAt.Sub(createdAt)
		if duration < 0 {
			duration = 0
		}
		timeToDecision := decision.DecidedAt().Sub(createdAt)
		if timeToDecision < 0 {
			timeToDecision = 0
		}
		store.metrics.RecordVerificationCompletion(observability.WorkflowCompletion{
			Outcome:        policyOutcome(decision.Evaluation().Outcome()),
			Duration:       duration,
			TimeToDecision: timeToDecision,
			Region:         observability.Region(*session.Region),
		})
	}
	return nil
}
