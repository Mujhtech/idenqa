package review

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// FollowupInput is an attributed reference-only workflow command.
type FollowupInput struct {
	CaseID     id.ReviewCase
	Version    int64
	Resolution Resolution
	Reason     string
	Actor      Actor
	At         time.Time
	Retry      idempotency.Request
}

// FollowupResult identifies the committed case version and policy result.
type FollowupResult struct {
	CaseID     string           `json:"case_id"`
	Version    int64            `json:"version"`
	DecisionID string           `json:"decision_id,omitempty"`
	Directive  policy.Directive `json:"directive,omitempty"`
}

// FollowupRepository owns atomic arbitration and correction evaluation.
type FollowupRepository interface {
	Arbitrate(context.Context, tenant.Scope, FollowupInput) (FollowupResult, error)
	EvaluateCorrection(context.Context, tenant.Scope, FollowupInput) (FollowupResult, error)
}

// FollowupService authorizes policy-controlled follow-up commands.
type FollowupService struct {
	repository FollowupRepository
	now        func() time.Time
}

// NewFollowupService constructs the follow-up application service.
func NewFollowupService(repository FollowupRepository, now func() time.Time) (*FollowupService, error) {
	if repository == nil || now == nil {
		return nil, ErrInvalid
	}
	return &FollowupService{repository, now}, nil
}

// Execute authorizes and dispatches one idempotent follow-up.
func (s *FollowupService) Execute(ctx context.Context, auth access.Context, caseID id.ReviewCase, version int64, resolution Resolution, reason, key string, correction bool) (FollowupResult, error) {
	if err := auth.Require(access.PermissionReviewsWrite); err != nil {
		return FollowupResult{}, err
	}
	if caseID.IsZero() || version < 1 || (!correction && (!validResolution(resolution) || !findingReasonPattern.MatchString(reason))) {
		return FollowupResult{}, ErrInvalid
	}
	operation := "reviews.arbitrate"
	if correction {
		operation = "reviews.correction.evaluate"
	}
	body, err := json.Marshal(struct {
		CaseID     string
		Version    int64
		Resolution Resolution
		Reason     string
	}{caseID.String(), version, resolution, reason})
	if err != nil {
		return FollowupResult{}, err
	}
	at := s.now().UTC().Truncate(time.Microsecond)
	retry, err := idempotency.NewRequest(auth.TenantScope().ID(), auth.Principal().KeyID(), operation, key, body, at, 24*time.Hour)
	if err != nil {
		return FollowupResult{}, err
	}
	input := FollowupInput{caseID, version, resolution, reason, Actor{auth.Principal().KeyID().String()}, at, retry}
	if correction {
		return s.repository.EvaluateCorrection(ctx, auth.TenantScope(), input)
	}
	return s.repository.Arbitrate(ctx, auth.TenantScope(), input)
}

// AppendFollowupFact preserves original fact meaning and refuses reserved-name collisions.
func AppendFollowupFact(original policy.Snapshot, key string, state policy.RequirementState, reference any, observedAt, at time.Time) (policy.Snapshot, string, error) {
	factKey, err := policy.NewFactKey(key)
	if err != nil {
		return policy.Snapshot{}, "", err
	}
	for _, fact := range original.Facts() {
		if fact.Key == factKey {
			return policy.Snapshot{}, "", ErrInvalid
		}
	}
	encoded, err := json.Marshal(reference)
	if err != nil {
		return policy.Snapshot{}, "", err
	}
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	facts := append(original.Facts(), policy.Fact{Key: factKey, State: state, Source: policy.FactSource{Kind: policy.FactSourceReviewFinding, ReviewFinding: &policy.ReviewFindingSource{Reference: "review:" + digest}}, ObservedAt: observedAt, ReasonCodes: []string{key}})
	result, err := policy.NewSnapshot(policy.SnapshotInput{Context: original.Context(), TenantID: original.TenantID(), VerificationID: original.VerificationID(), AuthorityID: original.AuthorityID(), AcknowledgementID: original.AcknowledgementID(), Region: original.Region(), Policy: original.Policy(), Evaluator: original.Evaluator(), EvaluatedAt: at, Facts: facts})
	return result, digest, err
}

// FindingState maps the closed finding vocabulary to policy fact states.
func FindingState(resolution Resolution) policy.RequirementState {
	switch resolution {
	case ResolutionSatisfy:
		return policy.RequirementSatisfied
	case ResolutionNotSatisfy:
		return policy.RequirementNotSatisfied
	default:
		return policy.RequirementInconclusive
	}
}

// Intake opens a correction against an eligible immutable decision.
func (s *FollowupService) Intake(ctx context.Context, auth access.Context, decisionID id.Decision, key string) (FollowupResult, error) {
	if err := auth.Require(access.PermissionAppealsWrite); err != nil {
		return FollowupResult{}, err
	}
	if decisionID.IsZero() {
		return FollowupResult{}, ErrInvalid
	}
	retry, err := idempotency.NewRequest(auth.TenantScope().ID(), auth.Principal().KeyID(), "reviews.correction.intake", key, []byte(decisionID.String()), s.now().UTC().Truncate(time.Microsecond), 24*time.Hour)
	if err != nil {
		return FollowupResult{}, err
	}
	repository, ok := s.repository.(interface {
		OpenCorrection(context.Context, tenant.Scope, id.Decision, Actor, idempotency.Request) (FollowupResult, error)
	})
	if !ok {
		return FollowupResult{}, ErrForbidden
	}
	return repository.OpenCorrection(ctx, auth.TenantScope(), decisionID, Actor{auth.Principal().KeyID().String()}, retry)
}
