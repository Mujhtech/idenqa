package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

type routingIdentifiers interface {
	NewReviewCase() (id.ReviewCase, error)
	NewEvent() (id.Event, error)
}

// RoutingStore composes evaluation, case, lifecycle, outbox and audit effects.
type RoutingStore struct {
	policy    *policypostgres.Store
	cases     *Store
	lifecycle *verificationpostgres.LifecycleStore
	ids       routingIdentifiers
	clock     clock.Clock
	rules     []review.RoutingRule
}

// NewRoutingStore has no implicit reviewer certification or oversight defaults.
func NewRoutingStore(pool transactionRunner, wrapper platformcrypto.KeyWrapper, ids routingIdentifiers, source clock.Clock, rules []review.RoutingRule) (*RoutingStore, error) {
	if ids == nil || source == nil || len(rules) > 256 {
		return nil, review.ErrInvalid
	}
	rules = append([]review.RoutingRule(nil), rules...)
	for index := range rules {
		rules[index].PermittedFindings = append([]review.PermittedFinding(nil), rules[index].PermittedFindings...)
	}
	seen := map[string]bool{}
	for _, rule := range rules {
		if _, err := id.ParseTenant(rule.TenantID); err != nil {
			return nil, review.ErrInvalid
		}
		if _, err := id.ParsePolicy(rule.PolicyID); err != nil {
			return nil, review.ErrInvalid
		}
		key := fmt.Sprintf("%s/%s/%d", rule.TenantID, rule.PolicyID, rule.Revision)
		if review.ValidateFindingRules(rule.PermittedFindings) != nil {
			return nil, review.ErrInvalid
		}
		if seen[key] || rule.Revision == 0 || len(rule.PolicyDigest) != 64 || len(rule.RequiredCertificate) == 0 || len(rule.RequiredCertificate) > 128 || (rule.Oversight != review.OversightSingle && rule.Oversight != review.OversightDual) {
			return nil, review.ErrInvalid
		}
		seen[key] = true
	}
	policies, err := policypostgres.New(pool, wrapper)
	if err != nil {
		return nil, err
	}
	cases, err := New(pool, wrapper)
	if err != nil {
		return nil, err
	}
	lifecycle, err := verificationpostgres.NewLifecycleStore(pool, wrapper, source)
	if err != nil {
		return nil, err
	}
	return &RoutingStore{policy: policies, cases: cases, lifecycle: lifecycle, ids: ids, clock: source, rules: append([]review.RoutingRule(nil), rules...)}, nil
}

// FindRouting returns committed provenance before any current-policy evaluation.
func (store *RoutingStore) FindRouting(ctx context.Context, scope tenant.Scope, request id.Decision) (policy.Routing, error) {
	return store.policy.FindRouting(ctx, scope, request)
}

// RouteWithin runs only within the caller's serializable, fence-verified transaction.
func (store *RoutingStore) RouteWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, candidate policy.Routing, actor id.Task) error {
	if tx == nil || actor.IsZero() {
		return policy.ErrInvalid
	}
	request := candidate.Request()
	if err := candidate.ValidateReplay(scope, request); err != nil {
		return err
	}
	queries := sqlgen.New(tx)
	if _, err := queries.SetTenantScope(ctx, scope.ID().String()); err != nil {
		return err
	}
	session, err := queries.LockVerificationForUpload(ctx, sqlgen.LockVerificationForUploadParams{TenantID: scope.ID().String(), ID: request.VerificationID.String()})
	if err != nil {
		return err
	}
	existing, err := store.policy.FindRoutingWithin(ctx, scope, tx, request.DecisionID)
	if err == nil {
		if err := existing.ValidateReplay(scope, request); err != nil {
			return err
		}
		if existing.Snapshot().Digest() != candidate.Snapshot().Digest() || existing.Evaluation().Digest() != candidate.Evaluation().Digest() {
			return policy.ErrDecisionConflict
		}
		target, err := workflowTarget(existing.Evaluation().Selected())
		if err != nil {
			return err
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1
	 FROM idenqa.policy_routing_receipts r
	 JOIN idenqa.verification_transitions t ON t.tenant_id=r.tenant_id AND t.event_id=r.lifecycle_event_id
	 LEFT JOIN idenqa.review_cases c ON c.tenant_id=r.tenant_id AND c.verification_id=r.verification_id AND c.routing_request_id=r.request_id
	 WHERE r.tenant_id=$1 AND r.verification_id=$2 AND r.request_id=$3
	 AND t.verification_id=r.verification_id AND t.to_state=$4
	 AND ($4 <> 'manual_review' OR c.id IS NOT NULL))`, scope.ID().String(), request.VerificationID.String(), request.DecisionID.String(), string(target)).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return policy.ErrReproduction
		}
		return nil
	}
	if !errors.Is(err, policy.ErrDecisionNotFound) {
		return err
	}
	snapshot := candidate.Snapshot()
	if session.DecisionID == nil || *session.DecisionID != request.DecisionID.String() || session.PolicyID == nil || *session.PolicyID != snapshot.Policy().ID.String() || session.AuthorityID == nil || *session.AuthorityID != snapshot.AuthorityID().String() || session.Region == nil || *session.Region != snapshot.Region() {
		return policy.ErrInvalid
	}
	now := store.clock.Now().UTC().Truncate(time.Microsecond)
	if request.DecidedAt.After(now) {
		return policy.ErrInvalid
	}
	if err := authoritypostgres.ValidateProcessingWithin(ctx, tx, scope, request.VerificationID, now, store.clock, verification.SessionStateProcessing); err != nil {
		return err
	}
	response, err := queries.FindLatestSubjectResponse(ctx, sqlgen.FindLatestSubjectResponseParams{TenantID: scope.ID().String(), AuthorityID: snapshot.AuthorityID().String()})
	if err != nil {
		return err
	}
	if response.ID != snapshot.AcknowledgementID().String() {
		return policy.ErrInvalid
	}
	var ready bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2)
 AND NOT EXISTS(SELECT 1 FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2 AND state NOT IN ('completed','skipped_by_policy','timed_out','cancelled','failed'))`, scope.ID().String(), request.VerificationID.String()).Scan(&ready); err != nil {
		return err
	}
	if !ready {
		return policy.ErrInvalid
	}
	target, err := workflowTarget(candidate.Evaluation().Selected())
	if err != nil {
		return err
	}
	eventID, err := store.ids.NewEvent()
	if err != nil {
		return err
	}
	if err := store.policy.AppendRoutingWithin(ctx, scope, tx, candidate, eventID); err != nil {
		return err
	}
	if target != verification.SessionStateManualReview {
		_, err = store.lifecycle.ApplyWithin(ctx, scope, tx, verification.LifecycleCommand{EventID: eventID, VerificationID: request.VerificationID, ExpectedVersion: session.Version, Target: target, ActorID: actor.String(), OccurredAt: now})
		return err
	}
	var rule *review.RoutingRule
	for index := range store.rules {
		if store.rules[index].Matches(snapshot) {
			rule = &store.rules[index]
			break
		}
	}
	settings, err := findPolicySettings(ctx, tx, scope, snapshot)
	if err != nil {
		return err
	}
	if settings != nil {
		rule = &settings.RoutingRule
	}

	if rule == nil {
		return fmt.Errorf("%w: review routing configuration unavailable", policy.ErrInvalid)
	}
	caseID, err := store.ids.NewReviewCase()
	if err != nil {
		return err
	}
	value, err := review.NewRoutedCase(caseID, request.VerificationID, request.DecisionID, snapshot.Region(), rule.RequiredCertificate, rule.Oversight, now)
	if err != nil {
		return err
	}
	value.PermittedFindings = append([]review.PermittedFinding(nil), rule.PermittedFindings...)
	if err := store.cases.CreateCaseWithin(ctx, scope, tx, review.Actor{ID: actor.String()}, value); err != nil {
		return err
	}
	if settings != nil {
		if err := pinCaseSettings(ctx, tx, scope, value, *settings, now); err != nil {
			return err
		}
	}

	_, err = store.lifecycle.ApplyWithin(ctx, scope, tx, verification.LifecycleCommand{EventID: eventID, VerificationID: request.VerificationID, ExpectedVersion: session.Version, Target: verification.SessionStateManualReview, ActorID: actor.String(), OccurredAt: now})
	return err
}

func workflowTarget(directive policy.Directive) (verification.SessionState, error) {
	switch directive {
	case policy.DirectiveRequestInput:
		return verification.SessionStateAwaitingInput, nil
	case policy.DirectiveRouteManualReview:
		return verification.SessionStateManualReview, nil
	case policy.DirectiveFailWorkflow:
		return verification.SessionStateFailed, nil
	default:
		return "", policy.ErrInvalid
	}
}
