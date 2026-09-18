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

// OperationRecaptureAcknowledge reserves acknowledgement replay independently of creation.
const OperationRecaptureAcknowledge = "reviews.recapture.acknowledge"

// RecaptureAcknowledgement records who acknowledged the child's exact decision.
// It conveys no identity finding and no authorization to complete the parent.
type RecaptureAcknowledgement struct {
	CaseID      id.ReviewCase
	CaseVersion int64
	ChildID     id.Verification
	DecisionID  id.Decision
	ActorID     id.APIKey
	ReviewerID  string
	RecordedAt  time.Time
}

// AcknowledgeRecapture contains only an expected immutable outcome and attributed actor.
type AcknowledgeRecapture struct {
	CaseID      id.ReviewCase
	CaseVersion int64
	DecisionID  id.Decision
	ActorID     id.APIKey
	ReviewerID  string
	At          time.Time
	Idempotency idempotency.Request
}
type recaptureAcknowledger interface {
	AcknowledgeRecapture(context.Context, tenant.Scope, AcknowledgeRecapture) (RecaptureAcknowledgement, error)
}

// Acknowledge marks a completed child outcome as seen without changing parent meaning.
func (service *RecaptureService) Acknowledge(ctx context.Context, auth access.Context, caseID id.ReviewCase, version int64, decisionID id.Decision, key string) (RecaptureAcknowledgement, error) {
	var empty RecaptureAcknowledgement
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
	retry, err := idempotency.NewRequest(scope.ID(), auth.Principal().KeyID(), OperationRecaptureAcknowledge, key, encoded, now, service.retention)
	if err != nil {
		return empty, err
	}
	repository, ok := service.repository.(recaptureAcknowledger)
	if !ok {
		return empty, ErrForbidden
	}
	return repository.AcknowledgeRecapture(ctx, scope, AcknowledgeRecapture{CaseID: caseID, CaseVersion: version, DecisionID: decisionID, ActorID: auth.Principal().KeyID(), ReviewerID: principal.ID, At: now, Idempotency: retry})
}
