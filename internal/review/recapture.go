package review

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// OperationRecapture separates recapture request replay from ordinary session creation.
const OperationRecapture = "reviews.recapture"

// OperationRecaptureRenew is independent from immutable creation replay.
const OperationRecaptureRenew = "reviews.recapture.renew"

// RecaptureRepository atomically creates a fresh child and immutable case lineage.
type RecaptureRepository interface {
	FindCase(context.Context, tenant.Scope, id.ReviewCase) (Case, error)
	RenewRecapture(context.Context, tenant.Scope, id.ReviewCase, int64, verification.CaptureRenewal) (verification.SessionCreation, error)
	CreateRecapture(context.Context, tenant.Scope, id.ReviewCase, int64, verification.SessionCreateMutation) (verification.SessionCreation, error)
}

// RecaptureService requires current operator authority even for credential replay.
type RecaptureService struct {
	repository                RecaptureRepository
	authority                 Authority
	ids                       verification.SessionIDGenerator
	captureSigner             *access.CaptureTokenSigner
	outcomeSigner             *access.OutcomeTokenSigner
	clock                     clock.Clock
	ttl, timeToCapture        time.Duration
	outcomePostTTL, retention time.Duration
}

// NewRecaptureService uses deployment defaults; request bodies cannot select weaker profiles.
func NewRecaptureService(repository RecaptureRepository, authority Authority, ids verification.SessionIDGenerator, captureSigner *access.CaptureTokenSigner, outcomeSigner *access.OutcomeTokenSigner, source clock.Clock, ttl, capture, outcomePostTTL, retention time.Duration) (*RecaptureService, error) {
	if repository == nil || authority == nil || ids == nil || captureSigner == nil || outcomeSigner == nil || source == nil || ttl <= 0 || capture <= 0 || capture > ttl || outcomePostTTL <= 0 || retention <= 0 {
		return nil, ErrInvalid
	}
	return &RecaptureService{
		repository: repository, authority: authority, ids: ids,
		captureSigner: captureSigner, outcomeSigner: outcomeSigner, clock: source,
		ttl: ttl, timeToCapture: capture, outcomePostTTL: outcomePostTTL, retention: retention,
	}, nil
}

// Create requires a policy-approved request_input evaluation of an accepted case.
func (service *RecaptureService) Create(ctx context.Context, auth access.Context, caseID id.ReviewCase, version int64, key string) (verification.CreatedSession, error) {
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
	canonical, err := json.Marshal(struct {
		CaseID  string `json:"case_id"`
		Version int64  `json:"expected_version"`
	}{caseID.String(), version})
	if err != nil {
		return verification.CreatedSession{}, err
	}
	retry, err := idempotency.NewRequest(scope.ID(), auth.Principal().KeyID(), OperationRecapture, key, canonical, now, service.retention)
	if err != nil {
		return verification.CreatedSession{}, err
	}
	if key == "" || version < 1 {
		return verification.CreatedSession{}, ErrInvalid
	}
	child, err := service.ids.NewVerification()
	if err != nil {
		return verification.CreatedSession{}, err
	}
	token, err := service.ids.NewCaptureToken()
	if err != nil {
		return verification.CreatedSession{}, err
	}
	outcomeToken, err := service.ids.NewOutcomeToken()
	if err != nil {
		return verification.CreatedSession{}, err
	}
	event, err := service.ids.NewEvent()
	if err != nil {
		return verification.CreatedSession{}, err
	}
	decision, err := service.ids.NewDecision()
	if err != nil {
		return verification.CreatedSession{}, err
	}
	sessionExpiresAt := now.Add(service.ttl)
	result, err := service.repository.CreateRecapture(ctx, scope, caseID, version, verification.SessionCreateMutation{
		SessionID: child, CaptureTokenID: token, OutcomeTokenID: outcomeToken,
		EventID: event, DecisionID: decision, Actor: auth.Principal().KeyID(),
		CaptureKeyVersion: service.captureSigner.ActiveVersion(),
		OutcomeKeyVersion: service.outcomeSigner.ActiveVersion(), CreatedAt: now,
		SessionExpiresAt: sessionExpiresAt, CaptureTokenExpiry: now.Add(service.timeToCapture),
		OutcomeTokenExpiry: sessionExpiresAt.Add(service.outcomePostTTL), Idempotency: retry,
	})
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
