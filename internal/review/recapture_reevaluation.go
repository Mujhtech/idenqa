package review

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// OperationRecaptureReevaluate keeps consequential follow-up replay separate from acknowledgement.
const OperationRecaptureReevaluate = "reviews.recapture.reevaluate"

// RecaptureReevaluation identifies the immutable request and its next case version.
type RecaptureReevaluation struct {
	CaseID        id.ReviewCase
	SourceVersion int64
	TargetVersion int64
	DecisionID    id.Decision
	ActorID       id.APIKey
	ReviewerID    string
	RecordedAt    time.Time
}
type recaptureReevaluator interface {
	ReevaluateRecapture(context.Context, tenant.Scope, AcknowledgeRecapture) (RecaptureReevaluation, error)
}

// Reevaluate explicitly requests evaluation of the acknowledged child outcome.
func (service *RecaptureService) Reevaluate(ctx context.Context, auth access.Context, caseID id.ReviewCase, version int64, decisionID id.Decision, key string) (RecaptureReevaluation, error) {
	var empty RecaptureReevaluation
	if err := auth.Require(access.PermissionReviewsWrite); err != nil {
		return empty, err
	}
	if caseID.IsZero() || version < 1 || decisionID.IsZero() {
		return empty, ErrInvalid
	}
	scope := auth.TenantScope()
	value, err := service.repository.FindCase(ctx, scope, caseID)
	if err != nil {
		return empty, err
	}
	if !value.ChallengedDecision.IsZero() {
		return empty, ErrForbidden
	}
	now := service.clock.Now().UTC()
	principal, err := service.authority.ResolveReviewer(ctx, scope, Actor{ID: auth.Principal().KeyID().String()}, value.Region, now)
	if err != nil {
		return empty, err
	}
	if !principal.permits(PermissionResolve) || !slices.Contains(principal.Certifications, value.RequiredCertificate) {
		return empty, ErrForbidden
	}
	encoded, err := json.Marshal(struct {
		CaseID     string `json:"case_id"`
		Version    int64  `json:"expected_version"`
		DecisionID string `json:"decision_id"`
	}{caseID.String(), version, decisionID.String()})
	if err != nil {
		return empty, err
	}
	now = now.Truncate(time.Microsecond)
	retry, err := idempotency.NewRequest(scope.ID(), auth.Principal().KeyID(), OperationRecaptureReevaluate, key, encoded, now, service.retention)
	if err != nil {
		return empty, err
	}
	repository, ok := service.repository.(recaptureReevaluator)
	if !ok {
		return empty, ErrForbidden
	}
	return repository.ReevaluateRecapture(ctx, scope, AcknowledgeRecapture{CaseID: caseID, CaseVersion: version, DecisionID: decisionID, ActorID: auth.Principal().KeyID(), ReviewerID: principal.ID, At: now, Idempotency: retry})
}
