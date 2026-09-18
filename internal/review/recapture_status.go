package review

import (
	"context"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// RecaptureStatus projects immutable linkage and the child's own completion.
// A child outcome is information for an authorized follow-up, never a parent decision.
type RecaptureStatus struct {
	AcknowledgedAt        *time.Time
	AcknowledgedBy        string
	CaseVersion           int64
	ChildID               id.Verification
	State                 verification.SessionState
	CaptureTokenID        id.CaptureToken
	CaptureTokenExpiresAt time.Time
	ExpiresAt             time.Time
	DecisionID            id.Decision
	Outcome               policy.Outcome
}
type recaptureStatusReader interface {
	ListRecaptures(context.Context, tenant.Scope, id.ReviewCase) ([]RecaptureStatus, error)
}

// List returns reference-only child status; it never discloses bearer credentials.
func (service *RecaptureService) List(ctx context.Context, auth access.Context, caseID id.ReviewCase) ([]RecaptureStatus, error) {
	if err := auth.Require(access.PermissionReviewsRead); err != nil {
		return nil, err
	}
	if _, err := service.repository.FindCase(ctx, auth.TenantScope(), caseID); err != nil {
		return nil, err
	}
	reader, ok := service.repository.(recaptureStatusReader)
	if !ok {
		return nil, ErrForbidden
	}
	return reader.ListRecaptures(ctx, auth.TenantScope(), caseID)
}
