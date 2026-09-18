package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// CompletionIdentifiers creates identities only inside a new completion effect.
type CompletionIdentifiers interface {
	NewEvent() (id.Event, error)
	NewDelivery() (id.Delivery, error)
	NewTask() (id.Task, error)
}

// CompletionStore commits workflow completion and every subscribed delivery
// atomically with the owning policy author and Headgate completion fence.
type CompletionStore struct {
	identifiers CompletionIdentifiers
	enqueuer    ProcessingEnqueuer
	clock       clock.Clock
	lifecycle   *LifecycleStore
	deliveries  *deliverypostgres.Store
}

// NewCompletionStore constructs the internal machine-decision completion adapter.
func NewCompletionStore(pool transactionRunner, identifiers CompletionIdentifiers, enqueuer ProcessingEnqueuer, source clock.Clock) (*CompletionStore, error) {
	if identifiers == nil || enqueuer == nil || source == nil {
		return nil, errors.New("verification postgres: completion dependencies are required")
	}
	lifecycle, err := NewLifecycleStore(pool, source)
	if err != nil {
		return nil, err
	}
	deliveries, err := deliverypostgres.New(pool)
	if err != nil {
		return nil, err
	}
	return &CompletionStore{identifiers: identifiers, enqueuer: enqueuer, clock: source, lifecycle: lifecycle, deliveries: deliveries}, nil
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
	endpoints, err := completionEndpoints(ctx, tx, scope)
	if err != nil {
		return err
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
	body, err := completionBody(scope, receipt, *session.SubjectID, *session.Region)
	if err != nil {
		return err
	}
	intents := make([]platformtask.Intent, 0, len(endpoints))
	for _, endpoint := range endpoints {
		deliveryID, err := store.identifiers.NewDelivery()
		if err != nil {
			return fmt.Errorf("generate completion delivery: %w", err)
		}
		intent, err := delivery.NewIntent(deliveryID, endpoint, receipt.EventID, "verification.completed", body, 8, now)
		if err != nil {
			return err
		}
		if err := store.deliveries.CreateDeliveryWithin(ctx, scope, tx, intent); err != nil {
			return fmt.Errorf("persist completion delivery: %w", err)
		}
		work, err := deliverytask.NewIntent(store.identifiers, scope, deliveryID, now)
		if err != nil {
			return err
		}
		intents = append(intents, work)
	}
	if len(intents) > 0 {
		if err := store.enqueuer.EnqueueTx(ctx, tx, intents...); err != nil {
			return fmt.Errorf("enqueue completion deliveries: %w", err)
		}
	}
	return nil
}

// Fail closed beyond the synchronous fanout bound rather than silently omit
// endpoints. Serializable predicate reads make concurrent endpoint changes race
// with this snapshot; delivery also rechecks disablement immediately before send.
func completionEndpoints(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope) ([]id.WebhookEndpoint, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM idenqa.webhook_endpoints WHERE tenant_id=$1 AND disabled_at IS NULL ORDER BY id LIMIT 1025`, scope.ID().String())
	if err != nil {
		return nil, fmt.Errorf("load completion subscribers: %w", err)
	}
	defer rows.Close()
	var endpoints []id.WebhookEndpoint
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		endpoint, err := id.ParseWebhookEndpoint(value)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
		if len(endpoints) > 1024 {
			return nil, errors.New("verification postgres: completion subscriber bound exceeded")
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read completion subscribers: %w", err)
	}
	return endpoints, nil
}

func completionBody(scope tenant.Scope, receipt verification.LifecycleReceipt, subject, region string) ([]byte, error) {
	return json.Marshal(struct {
		ID            string    `json:"id"`
		Type          string    `json:"type"`
		SchemaVersion string    `json:"schema_version"`
		CreatedAt     time.Time `json:"created_at"`
		TenantID      string    `json:"tenant_id"`
		Region        string    `json:"region"`
		Data          struct {
			VerificationID string `json:"verification_id"`
			SubjectID      string `json:"subject_id"`
			DecisionID     string `json:"decision_id"`
		} `json:"data"`
	}{ID: receipt.EventID.String(), Type: "verification.completed", SchemaVersion: "1.0", CreatedAt: receipt.OccurredAt, TenantID: scope.ID().String(), Region: region,
		Data: struct {
			VerificationID string `json:"verification_id"`
			SubjectID      string `json:"subject_id"`
			DecisionID     string `json:"decision_id"`
		}{receipt.VerificationID.String(), subject, receipt.DecisionID.String()},
	})
}
