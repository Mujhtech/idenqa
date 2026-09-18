package review

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// Renew replaces an expired credential while preserving explicitly linked accepted progress.
func (service *RecaptureService) Renew(ctx context.Context, auth access.Context, caseID id.ReviewCase, version int64, expected id.CaptureToken, key string) (verification.CreatedSession, error) {
	if err := auth.Require(access.PermissionReviewsWrite); err != nil {
		return verification.CreatedSession{}, err
	}
	if err := auth.Require(access.PermissionVerificationSessionsCreate); err != nil {
		return verification.CreatedSession{}, err
	}
	scope := auth.TenantScope()
	value, err := service.repository.FindCase(ctx, scope, caseID)
	if err != nil {
		return verification.CreatedSession{}, err
	}
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	principal, err := service.authority.ResolveReviewer(ctx, scope, Actor{ID: auth.Principal().KeyID().String()}, value.Region, service.clock.Now().UTC())
	if err != nil {
		return verification.CreatedSession{}, err
	}
	if !principal.permits(PermissionFind) || !slices.Contains(principal.Certifications, value.RequiredCertificate) {
		return verification.CreatedSession{}, ErrForbidden
	}
	if expected.IsZero() || version < 1 {
		return verification.CreatedSession{}, ErrInvalid
	}
	encoded, err := json.Marshal(struct {
		CaseID  string `json:"case_id"`
		Version int64  `json:"expected_version"`
		Token   string `json:"expected_capture_token_id"`
	}{caseID.String(), version, expected.String()})
	if err != nil {
		return verification.CreatedSession{}, err
	}
	retry, err := idempotency.NewRequest(scope.ID(), auth.Principal().KeyID(), OperationRecaptureRenew, key, encoded, now, service.retention)
	if err != nil {
		return verification.CreatedSession{}, err
	}
	token, err := service.ids.NewCaptureToken()
	if err != nil {
		return verification.CreatedSession{}, err
	}
	result, err := service.repository.RenewRecapture(ctx, scope, caseID, version, verification.CaptureRenewal{ExpectedToken: expected, Token: token, Actor: auth.Principal().KeyID(), KeyVersion: service.captureSigner.ActiveVersion(), At: now, ExpiresAt: now.Add(service.timeToCapture), Idempotency: retry})
	if err != nil {
		return verification.CreatedSession{}, err
	}
	presented, err := service.captureSigner.Sign(result.Credential)
	if err != nil {
		return verification.CreatedSession{}, err
	}
	outcomePresented, err := service.outcomeSigner.Sign(result.OutcomeCredential)
	if err != nil {
		return verification.CreatedSession{}, err
	}
	return verification.CreatedSession{
		Session: result.Session, Credential: result.Credential,
		CaptureToken: presented, OutcomeCredential: result.OutcomeCredential,
		OutcomeToken: outcomePresented,
	}, nil
}
