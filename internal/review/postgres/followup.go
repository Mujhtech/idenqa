package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"

	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// FollowupStore owns supervisor arbitration and policy-authored immutable correction receipts.
type FollowupStore struct {
	*Store
	policies  *policypostgres.Store
	evaluator policy.Evaluator
	ids       *id.Generator
}

// NewFollowupStore constructs transactional policy follow-up.
func NewFollowupStore(pool transactionRunner, wrapper platformcrypto.KeyWrapper, authority review.Authority, evaluator policy.Evaluator, ids *id.Generator, source clock.Clock) (*FollowupStore, error) {
	if authority == nil || evaluator == nil || ids == nil {
		return nil, review.ErrInvalid
	}
	store, err := NewWithClock(pool, wrapper, source)
	if err != nil {
		return nil, err
	}
	policies, err := policypostgres.New(pool, wrapper)
	if err != nil {
		return nil, err
	}
	return &FollowupStore{store.WithAuthority(authority), policies, evaluator, ids}, nil
}
func (s *FollowupStore) run(ctx context.Context, scope tenant.Scope, input review.FollowupInput, operation string, effect func(context.Context, pg.Transaction, review.Case, review.Principal) (review.FollowupResult, error)) (review.FollowupResult, error) {
	var result review.FollowupResult
	if input.Version < 1 || input.Version == 9223372036854775807 || input.CaseID.IsZero() || input.Retry.TenantID() != scope.ID() || input.Retry.Principal().String() != input.Actor.ID || input.Retry.Operation() != operation || !review.ValidTime(input.At) {
		return result, review.ErrInvalid
	}
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		value, err := s.findCaseWithin(ctx, scope, tx, input.CaseID)
		if err != nil {
			return err
		}
		principal, err := resolveWithin(ctx, tx, s.authority, scope, input.Actor, value.Region, s.clock.Now().UTC())
		if err != nil {
			return err
		}
		if !slices.Contains(principal.Permissions, review.PermissionResolve) || !slices.Contains(principal.Certifications, value.RequiredCertificate) {
			return review.ErrForbidden
		}
		q := sqlgen.New(tx)
		reservation, err := idempg.Reserve(ctx, q, input.Retry)
		if err != nil {
			return err
		}
		if replay, ok := reservation.Result(); ok {
			return json.Unmarshal(replay.Body(), &result)
		}
		if value.Version != input.Version || input.At.Before(value.UpdatedAt) || input.At.After(s.clock.Now().UTC()) {
			return review.ErrConflict
		}
		result, err = effect(ctx, tx, value, principal)
		if err != nil {
			return err
		}
		body, err := json.Marshal(result)
		if err != nil {
			return err
		}
		replay, err := idempotency.NewResult(200, body)
		if err != nil {
			return err
		}
		return idempg.Complete(ctx, q, input.Retry, replay, input.At)
	})
	return result, err
}

// Arbitrate records an independent certified supervisor finding.
func (s *FollowupStore) Arbitrate(ctx context.Context, scope tenant.Scope, input review.FollowupInput) (review.FollowupResult, error) {
	return s.run(ctx, scope, input, "reviews.arbitrate", func(ctx context.Context, tx pg.Transaction, value review.Case, principal review.Principal) (review.FollowupResult, error) {
		var empty review.FollowupResult
		if value.State != review.CaseEscalated || !slices.Contains(value.PermittedFindings, review.PermittedFinding{Resolution: input.Resolution, ReasonCode: input.Reason}) {
			return empty, review.ErrConflict
		}
		for _, finding := range value.Findings {
			if finding.ReviewerID == principal.ID {
				return empty, review.ErrForbidden
			}
		}
		var encoded []byte
		if err := tx.QueryRow(ctx, `SELECT configuration FROM idenqa.review_case_settings WHERE tenant_id=$1 AND case_id=$2`, scope.ID().String(), value.ID.String()).Scan(&encoded); err != nil {
			return empty, err
		}
		var settings review.PolicySettings
		if json.Unmarshal(encoded, &settings) != nil || settings.Validate() != nil || !slices.Contains(principal.Certifications, settings.EscalationCertificate) {
			return empty, review.ErrForbidden
		}
		if !value.ChallengedDecision.IsZero() {
			if err := independentOfDecision(ctx, tx, scope, value.ChallengedDecision, principal.ID); err != nil {
				return empty, err
			}
		}
		if err := validateCaseProcessing(ctx, tx, scope, value, input.At, s.clock); err != nil {
			return empty, err
		}
		tag, err := tx.Exec(ctx, `UPDATE idenqa.review_cases SET state='resolved',version=version+1,updated_at=$4 WHERE tenant_id=$1 AND id=$2 AND version=$3 AND state='escalated'`, scope.ID().String(), value.ID.String(), value.Version, input.At)
		if err != nil {
			return empty, err
		}
		if tag.RowsAffected() != 1 {
			return empty, review.ErrConflict
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_evaluation_requests(tenant_id,case_id,case_version,created_at) VALUES($1,$2,$3,$4)`, scope.ID().String(), value.ID.String(), value.Version+1, input.At)
		if err != nil {
			return empty, err
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_arbitrations(tenant_id,case_id,case_version,reviewer_id,actor_key_id,resolution,reason_code,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, scope.ID().String(), value.ID.String(), value.Version+1, principal.ID, input.Actor.ID, input.Resolution, input.Reason, input.At)
		if err != nil {
			return empty, err
		}
		if err := appendReviewAudit(ctx, tx, scope, input.Actor, value.ID.String(), "review.case.arbitrated", input.At, value.Version+1); err != nil {
			return empty, err
		}
		return review.FollowupResult{CaseID: value.ID.String(), Version: value.Version + 1}, nil
	})
}

// EvaluateCorrection authors a successor only when the pinned policy permits it.
func (s *FollowupStore) EvaluateCorrection(ctx context.Context, scope tenant.Scope, input review.FollowupInput) (review.FollowupResult, error) {
	return s.run(ctx, scope, input, "reviews.correction.evaluate", func(ctx context.Context, tx pg.Transaction, value review.Case, principal review.Principal) (review.FollowupResult, error) {
		var empty review.FollowupResult
		if value.ChallengedDecision.IsZero() || value.State != review.CaseResolved || !value.SupersedesDecision.IsZero() {
			return empty, review.ErrConflict
		}
		for _, finding := range value.Findings {
			if finding.ReviewerID == principal.ID {
				return empty, review.ErrForbidden
			}
		}
		if err := independentOfDecision(ctx, tx, scope, value.ChallengedDecision, principal.ID); err != nil {
			return empty, err
		}
		if err := authoritypostgres.ValidateReconsiderationWithin(ctx, tx, scope, value.VerificationID, value.ChallengedDecision, input.At, s.clock); err != nil {
			return empty, err
		}
		previous, err := s.policies.FindWithin(ctx, scope, tx, value.ChallengedDecision)
		if err != nil {
			return empty, err
		}
		if previous.Snapshot().VerificationID() != value.VerificationID {
			return empty, review.ErrForbidden
		}
		var hasSuccessor bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_decisions WHERE tenant_id=$1 AND verification_id=$2 AND supersedes_id=$3)`, scope.ID().String(), value.VerificationID.String(), value.ChallengedDecision.String()).Scan(&hasSuccessor); err != nil {
			return empty, err
		}
		if hasSuccessor {
			return empty, review.ErrConflict
		}
		accepted := value
		accepted.RoutingRequest = value.ChallengedDecision
		accepted.ChallengedDecision = id.Decision{}
		fact, digest, err := accepted.AcceptedFact()
		if err != nil {
			var resolution, reason, reviewer string
			var recordedAt time.Time
			if queryErr := tx.QueryRow(ctx, `SELECT resolution,reason_code,reviewer_id,recorded_at FROM idenqa.review_arbitrations WHERE tenant_id=$1 AND case_id=$2 AND case_version<=$3 ORDER BY case_version DESC LIMIT 1`, scope.ID().String(), value.ID.String(), value.Version).Scan(&resolution, &reason, &reviewer, &recordedAt); queryErr != nil {
				return empty, err
			}
			fact.State = review.FindingState(review.Resolution(resolution))
			encoded, _ := json.Marshal([]string{value.ID.String(), resolution, reason, reviewer, recordedAt.UTC().String()})
			sum := sha256.Sum256(encoded)
			digest = hex.EncodeToString(sum[:])
		}
		root := previous
		for depth := 0; !root.Supersedes().IsZero(); depth++ {
			if depth >= 100 {
				return empty, review.ErrConflict
			}
			if err := validateSuccessor(ctx, tx, scope, value.VerificationID, root.Supersedes(), root.ID()); err != nil {
				return empty, err
			}
			root, err = s.policies.FindWithin(ctx, scope, tx, root.Supersedes())
			if err != nil {
				return empty, err
			}
		}
		base := root.Snapshot()
		var childDecision string
		err = tx.QueryRow(ctx, `SELECT coalesce(a.decision_id,'') FROM idenqa.review_recaptures r LEFT JOIN idenqa.review_recapture_acknowledgements a ON a.tenant_id=r.tenant_id AND a.case_id=r.case_id AND a.case_version=r.case_version WHERE r.tenant_id=$1 AND r.case_id=$2 AND r.case_version=$3`, scope.ID().String(), value.ID.String(), value.Version).Scan(&childDecision)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return empty, err
		}
		if err == nil {
			if childDecision == "" {
				return empty, review.ErrConflict
			}
			childID, err := id.ParseDecision(childDecision)
			if err != nil {
				return empty, err
			}
			child, err := s.policies.FindWithin(ctx, scope, tx, childID)
			if err != nil {
				return empty, err
			}
			state := policy.RequirementInconclusive
			switch child.Evaluation().Outcome() {
			case policy.OutcomeVerified:
				state = policy.RequirementSatisfied
			case policy.OutcomeNotVerified:
				state = policy.RequirementNotSatisfied
			}
			base, _, err = review.AppendFollowupFact(base, "review.correction.recapture", state, child.Digest(), child.DecidedAt(), input.At)
			if err != nil {
				return empty, err
			}
		}
		observedAt := value.CreatedAt
		for _, finding := range value.Findings {
			if finding.RecordedAt.After(observedAt) {
				observedAt = finding.RecordedAt
			}
		}

		snapshot, _, err := review.AppendFollowupFact(base, "review.correction", fact.State, struct {
			CaseDigest string
			Reviewer   string
			Actor      string
			At         string
		}{digest, principal.ID, input.Actor.ID, input.At.String()}, observedAt, input.At)
		if err != nil {
			return empty, err
		}
		output, err := s.evaluator.Evaluate(ctx, snapshot)
		if err != nil {
			return empty, err
		}
		evaluation, err := policy.Resolve(snapshot, output.Results, output.Assurance)
		if err != nil {
			return empty, err
		}
		if err := s.policies.AppendEvaluationWithin(ctx, scope, tx, snapshot, evaluation); err != nil {
			return empty, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.review_evaluation_requests(tenant_id,case_id,case_version,created_at) VALUES($1,$2,$3,$4)`, scope.ID().String(), value.ID.String(), value.Version+1, input.At); err != nil {
			return empty, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.review_evaluations(tenant_id,case_id,case_version,verification_id,snapshot_digest,evaluation_digest,case_digest) VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.ID().String(), value.ID.String(), value.Version+1, value.VerificationID.String(), snapshot.Digest(), evaluation.Digest(), digest); err != nil {
			return empty, err
		}

		var decisionID *string
		if evaluation.AuthorisesCompletion() {
			identifier, err := s.ids.NewDecision()
			if err != nil {
				return empty, err
			}
			decision, err := policy.NewDecision(policy.DecisionInput{ID: identifier, Snapshot: snapshot, Evaluation: evaluation, Actor: policy.ActorMachine, DecidedAt: input.At, Supersedes: value.ChallengedDecision})
			if err != nil {
				return empty, err
			}
			if err := s.policies.AppendWithin(ctx, scope, tx, decision); err != nil {
				return empty, err
			}
			if err := s.notifyCorrection(ctx, tx, scope, value, identifier, input); err != nil {
				return empty, err
			}
			encoded := identifier.String()
			decisionID = &encoded
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_correction_evaluations(tenant_id,case_id,case_version,snapshot_digest,evaluation_digest,decision_id,actor_key_id,reviewer_id,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, scope.ID().String(), value.ID.String(), value.Version+1, snapshot.Digest(), evaluation.Digest(), decisionID, input.Actor.ID, principal.ID, input.At)
		if err != nil {
			return empty, err
		}
		tag, err := tx.Exec(ctx, `UPDATE idenqa.review_cases SET version=version+1,updated_at=$4,superseding_decision_id=$5 WHERE tenant_id=$1 AND id=$2 AND version=$3`, scope.ID().String(), value.ID.String(), value.Version, input.At, decisionID)
		if err != nil {
			return empty, err
		}
		if tag.RowsAffected() != 1 {
			return empty, review.ErrConflict
		}
		if err := appendReviewAudit(ctx, tx, scope, input.Actor, value.ID.String(), "review.case.correction_evaluated", input.At, value.Version+1); err != nil {
			return empty, err
		}
		result := review.FollowupResult{CaseID: value.ID.String(), Version: value.Version + 1, Directive: evaluation.Selected()}
		if decisionID != nil {
			result.DecisionID = *decisionID
		}
		return result, nil
	})
}
func independentOfDecision(ctx context.Context, tx pg.Transaction, scope tenant.Scope, decision id.Decision, reviewer string) error {
	var involved bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.review_cases c JOIN idenqa.review_findings f ON f.tenant_id=c.tenant_id AND f.case_id=c.id WHERE c.tenant_id=$1 AND (c.routing_request_id=$2 OR c.superseding_decision_id=$2) AND f.reviewer_id=$3) OR EXISTS(SELECT 1 FROM idenqa.review_correction_evaluations WHERE tenant_id=$1 AND decision_id=$2 AND reviewer_id=$3) OR EXISTS(SELECT 1 FROM idenqa.review_arbitrations a JOIN idenqa.review_cases c ON c.tenant_id=a.tenant_id AND c.id=a.case_id WHERE a.tenant_id=$1 AND c.routing_request_id=$2 AND a.reviewer_id=$3)`, scope.ID().String(), decision.String(), reviewer).Scan(&involved)
	if err != nil {
		return err
	}
	if involved {
		return review.ErrForbidden
	}
	return nil
}
func arbitrationSnapshot(ctx context.Context, tx pg.Transaction, scope tenant.Scope, value review.Case, routing policy.Routing) (policy.Snapshot, string, bool, error) {
	var reviewer, actor, resolution, reason string
	var at time.Time
	err := tx.QueryRow(ctx, `SELECT reviewer_id,actor_key_id,resolution,reason_code,recorded_at FROM idenqa.review_arbitrations WHERE tenant_id=$1 AND case_id=$2 AND case_version=$3`, scope.ID().String(), value.ID.String(), value.Version).Scan(&reviewer, &actor, &resolution, &reason, &at)
	if errors.Is(err, pgx.ErrNoRows) {
		return policy.Snapshot{}, "", false, nil
	}
	if err != nil {
		return policy.Snapshot{}, "", true, err
	}
	snapshot, digest, err := review.AppendFollowupFact(routing.Snapshot(), "review.arbitration", review.FindingState(review.Resolution(resolution)), struct {
		Case                                string
		Version                             int64
		Reviewer, Actor, Resolution, Reason string
	}{value.ID.String(), value.Version, reviewer, actor, resolution, reason}, at.UTC(), at.UTC())
	return snapshot, digest, true, err
}
