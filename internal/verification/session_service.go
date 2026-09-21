package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// SessionIDGenerator is the identifier capability consumed by SessionService.
type SessionIDGenerator interface {
	NewVerification() (id.Verification, error)
	NewCaptureToken() (id.CaptureToken, error)
	NewOutcomeToken() (id.OutcomeToken, error)
	NewEvent() (id.Event, error)
	NewDecision() (id.Decision, error)
}

// SessionRepository is the tenant-scoped persistence boundary consumed by
// session creation and tenant reads.
type SessionRepository interface {
	Create(context.Context, tenant.Scope, SessionCreateMutation) (SessionCreation, error)
	FindSession(context.Context, tenant.Scope, id.Verification) (Session, error)
	Resume(context.Context, tenant.Scope, ResumeMutation) (ResumeResult, error)
}

// SessionCreateInput contains the tenant-selected profile and optional bounded
// lifetimes. Nil lifetimes select deployment defaults.
type SessionCreateInput struct {
	ProfileID       id.Profile
	PolicyID        id.Policy
	VerificationTTL *time.Duration
	CaptureTokenTTL *time.Duration
	OutcomePostTTL  *time.Duration
	// Locale and Experience optionally select and pin the portable capture
	// experience at creation. Nil keeps resolution to the deployment defaults.
	Locale     string
	Experience *experience.ResolutionRequest
}

// ExperiencePinner resolves and pins the portable capture experience for a
// session. Pinning is best-effort: bootstrap resolution falls back to the
// signed safe default, so a pin failure never blocks session creation.
type ExperiencePinner interface {
	PinForSession(context.Context, tenant.Scope, id.Verification, experience.ResolutionRequest) (experience.Pin, error)
}

// CreatedSession carries display-once capture and outcome bearer tokens alongside
// the durable non-secret session. A transport must reveal each token explicitly.
type CreatedSession struct {
	Session           Session
	Credential        access.CaptureCredential
	CaptureToken      access.PresentedCaptureToken
	OutcomeCredential access.OutcomeCredential
	OutcomeToken      access.PresentedOutcomeToken
}

// ResumedSession returns a bearer only when this request freshly committed a
// replacement. Live credentials are referenced for tenant-side reuse, and an
// exact retry never receives bearer material.
type ResumedSession struct {
	Session            Session
	Credential         access.CaptureCredential
	CaptureToken       access.PresentedCaptureToken
	Replaced, Replayed bool
}

// SessionLifetimes holds deployment defaults, maxima, and replay retention.
type SessionLifetimes struct {
	VerificationDefault  time.Duration
	VerificationMaximum  time.Duration
	CaptureTokenDefault  time.Duration
	CaptureTokenMaximum  time.Duration
	OutcomePostDefault   time.Duration
	OutcomePostMaximum   time.Duration
	IdempotencyRetention time.Duration
}

// SessionService authorises and coordinates verification-session use cases.
type SessionService struct {
	repository    SessionRepository
	identifiers   SessionIDGenerator
	captureSigner *access.CaptureTokenSigner
	outcomeSigner *access.OutcomeTokenSigner
	clock         clock.Clock
	lifetimes     SessionLifetimes
	region        string
	experience    ExperiencePinner
}

// WithExperience wires optional portable-experience pinning into session
// creation. It is composed after construction so deployments without
// experience signing keys keep working unchanged.
func (service *SessionService) WithExperience(pinner ExperiencePinner) *SessionService {
	service.experience = pinner
	return service
}

// NewSessionService constructs the verification-session application service.
func NewSessionService(
	repository SessionRepository,
	identifiers SessionIDGenerator,
	captureSigner *access.CaptureTokenSigner,
	outcomeSigner *access.OutcomeTokenSigner,
	source clock.Clock,
	lifetimes SessionLifetimes,
	region string,
) (*SessionService, error) {
	if repository == nil || identifiers == nil || captureSigner == nil || outcomeSigner == nil || source == nil ||
		!validSessionLifetimes(lifetimes) || !validRegion(region) {
		return nil, errors.New("verification: session service dependencies and lifetimes are required")
	}

	return &SessionService{
		repository:    repository,
		identifiers:   identifiers,
		captureSigner: captureSigner,
		outcomeSigner: outcomeSigner,
		clock:         source,
		lifetimes:     lifetimes,
		region:        region,
	}, nil
}

// Create creates a collecting session and reconstructable capture credential.
func (service *SessionService) Create(
	ctx context.Context,
	authority access.Context,
	idempotencyKey string,
	input SessionCreateInput,
) (CreatedSession, error) {
	if err := authority.Require(access.PermissionVerificationSessionsCreate); err != nil {
		return CreatedSession{}, err
	}
	verificationTTL, captureTTL, outcomePostTTL, err := service.resolveLifetimes(input)
	if err != nil {
		return CreatedSession{}, err
	}
	canonical, err := json.Marshal(struct {
		ProfileID              string `json:"capture_profile_id"`
		PolicyID               string `json:"policy_id"`
		VerificationTTLSeconds int64  `json:"verification_ttl_seconds"`
		CaptureTokenTTLSeconds int64  `json:"capture_token_ttl_seconds"`
		OutcomePostTTLSeconds  int64  `json:"outcome_token_post_expiry_ttl_seconds"`
		Region                 string `json:"region"`
	}{
		ProfileID:              input.ProfileID.String(),
		PolicyID:               input.PolicyID.String(),
		VerificationTTLSeconds: int64(verificationTTL / time.Second),
		CaptureTokenTTLSeconds: int64(captureTTL / time.Second),
		OutcomePostTTLSeconds:  int64(outcomePostTTL / time.Second),
		Region:                 service.region,
	})
	if err != nil {
		return CreatedSession{}, fmt.Errorf("serialise verification create command: %w", err)
	}
	now := service.clock.Now().UTC().Truncate(time.Second)
	retry, err := idempotency.NewRequest(
		authority.TenantScope().ID(),
		authority.Principal().KeyID(),
		OperationCreateVerification,
		idempotencyKey,
		canonical,
		now,
		service.lifetimes.IdempotencyRetention,
	)
	if err != nil {
		return CreatedSession{}, err
	}
	sessionID, err := service.identifiers.NewVerification()
	if err != nil {
		return CreatedSession{}, fmt.Errorf("generate verification id: %w", err)
	}
	tokenID, err := service.identifiers.NewCaptureToken()
	if err != nil {
		return CreatedSession{}, fmt.Errorf("generate capture-token id: %w", err)
	}
	outcomeTokenID, err := service.identifiers.NewOutcomeToken()
	if err != nil {
		return CreatedSession{}, fmt.Errorf("generate outcome-token id: %w", err)
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return CreatedSession{}, fmt.Errorf("generate verification event id: %w", err)
	}
	decisionID, err := service.identifiers.NewDecision()
	if err != nil {
		return CreatedSession{}, fmt.Errorf("generate policy decision id: %w", err)
	}
	sessionExpiresAt := now.Add(verificationTTL)
	creation, err := service.repository.Create(ctx, authority.TenantScope(), SessionCreateMutation{
		SessionID:          sessionID,
		CaptureTokenID:     tokenID,
		OutcomeTokenID:     outcomeTokenID,
		EventID:            eventID,
		ProfileID:          input.ProfileID,
		PolicyID:           input.PolicyID,
		DecisionID:         decisionID,
		Region:             service.region,
		Actor:              authority.Principal().KeyID(),
		CaptureKeyVersion:  service.captureSigner.ActiveVersion(),
		OutcomeKeyVersion:  service.outcomeSigner.ActiveVersion(),
		CreatedAt:          now,
		SessionExpiresAt:   sessionExpiresAt,
		CaptureTokenExpiry: now.Add(captureTTL),
		OutcomeTokenExpiry: sessionExpiresAt.Add(outcomePostTTL),
		Idempotency:        retry,
	})
	if err != nil {
		return CreatedSession{}, err
	}
	service.pinExperience(ctx, authority, creation.Session.ID(), input)
	presented, err := service.captureSigner.Sign(creation.Credential)
	if err != nil {
		return CreatedSession{}, fmt.Errorf("sign capture token: %w", err)
	}
	outcomeToken, err := service.outcomeSigner.Sign(creation.OutcomeCredential)
	if err != nil {
		return CreatedSession{}, fmt.Errorf("sign outcome token: %w", err)
	}

	return CreatedSession{
		Session: creation.Session, Credential: creation.Credential,
		CaptureToken: presented, OutcomeCredential: creation.OutcomeCredential,
		OutcomeToken: outcomeToken,
	}, nil
}

// pinExperience resolves and persists the session experience pin. Resolution
// failure is deliberately non-fatal: bootstrap always falls back to the signed
// accessible safe default and resume re-resolves to that same default.
func (service *SessionService) pinExperience(
	ctx context.Context,
	authority access.Context,
	verificationID id.Verification,
	input SessionCreateInput,
) {
	if service.experience == nil {
		return
	}
	request := experience.ResolutionRequest{Locale: input.Locale}
	if input.Experience != nil {
		request = *input.Experience
		if request.Locale == "" {
			request.Locale = input.Locale
		}
	}
	_, _ = service.experience.PinForSession(ctx, authority.TenantScope(), verificationID, request)
}

// Find returns a tenant-owned verification session after application-level authorisation.
func (service *SessionService) Find(
	ctx context.Context,
	authority access.Context,
	identifier id.Verification,
) (Session, error) {
	if err := authority.Require(access.PermissionVerificationSessionsRead); err != nil {
		return Session{}, err
	}

	return service.repository.FindSession(ctx, authority.TenantScope(), identifier)
}

// Resume returns an awaiting-input session to collecting after fresh subject
// authorisation has been recorded. It never extends the session deadline.
func (service *SessionService) Resume(ctx context.Context, authority access.Context, identifier id.Verification, expectedVersion int64, idempotencyKey string) (ResumedSession, error) {
	if err := authority.Require(access.PermissionVerificationSessionsResume); err != nil {
		return ResumedSession{}, err
	}
	if identifier.IsZero() || expectedVersion < 1 {
		return ResumedSession{}, ErrSessionConflict
	}
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	canonical, err := json.Marshal(struct {
		VerificationID  string `json:"verification_id"`
		ExpectedVersion int64  `json:"expected_version"`
	}{identifier.String(), expectedVersion})
	if err != nil {
		return ResumedSession{}, err
	}
	retry, err := idempotency.NewRequest(authority.TenantScope().ID(), authority.Principal().KeyID(), OperationResumeVerification, idempotencyKey, canonical, now, service.lifetimes.IdempotencyRetention)
	if err != nil {
		return ResumedSession{}, err
	}
	replacement, err := service.identifiers.NewCaptureToken()
	if err != nil {
		return ResumedSession{}, fmt.Errorf("generate replacement capture-token id: %w", err)
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return ResumedSession{}, fmt.Errorf("generate resume event id: %w", err)
	}
	result, err := service.repository.Resume(ctx, authority.TenantScope(), ResumeMutation{
		VerificationID: identifier, ExpectedVersion: expectedVersion,
		ReplacementID: replacement, EventID: eventID, Actor: authority.Principal().KeyID(),
		KeyVersion: service.captureSigner.ActiveVersion(), At: now,
		TokenExpiresAt: now.Add(service.lifetimes.CaptureTokenDefault), Idempotency: retry,
	})
	if err != nil {
		return ResumedSession{}, err
	}
	resumed := ResumedSession{Session: result.Session, Credential: result.Credential, Replaced: result.Replaced, Replayed: result.Replayed}
	if result.Replaced && !result.Replayed {
		resumed.CaptureToken, err = service.captureSigner.Sign(result.Credential)
		if err != nil {
			return ResumedSession{}, fmt.Errorf("sign replacement capture token: %w", err)
		}
	}
	return resumed, nil
}

func (service *SessionService) resolveLifetimes(input SessionCreateInput) (time.Duration, time.Duration, time.Duration, error) {
	if input.ProfileID.IsZero() || input.PolicyID.IsZero() {
		return 0, 0, 0, errors.New("verification: capture profile is required")
	}
	verificationTTL := service.lifetimes.VerificationDefault
	if input.VerificationTTL != nil {
		verificationTTL = *input.VerificationTTL
	}
	captureTTL := service.lifetimes.CaptureTokenDefault
	if input.CaptureTokenTTL != nil {
		captureTTL = *input.CaptureTokenTTL
	}
	outcomePostTTL := service.lifetimes.OutcomePostDefault
	if input.OutcomePostTTL != nil {
		outcomePostTTL = *input.OutcomePostTTL
	}
	if verificationTTL <= 0 || verificationTTL > service.lifetimes.VerificationMaximum ||
		captureTTL <= 0 || captureTTL > service.lifetimes.CaptureTokenMaximum ||
		outcomePostTTL <= 0 || outcomePostTTL > service.lifetimes.OutcomePostMaximum ||
		captureTTL > verificationTTL ||
		verificationTTL%time.Second != 0 || captureTTL%time.Second != 0 || outcomePostTTL%time.Second != 0 {
		return 0, 0, 0, errors.New("verification: requested lifetimes are outside configured bounds")
	}

	return verificationTTL, captureTTL, outcomePostTTL, nil
}

func validSessionLifetimes(lifetimes SessionLifetimes) bool {
	return lifetimes.VerificationDefault > 0 &&
		lifetimes.VerificationMaximum >= lifetimes.VerificationDefault &&
		lifetimes.CaptureTokenDefault > 0 &&
		lifetimes.CaptureTokenMaximum >= lifetimes.CaptureTokenDefault &&
		lifetimes.CaptureTokenMaximum <= lifetimes.VerificationMaximum &&
		lifetimes.OutcomePostDefault > 0 &&
		lifetimes.OutcomePostMaximum >= lifetimes.OutcomePostDefault &&
		lifetimes.IdempotencyRetention > 0 &&
		lifetimes.VerificationDefault%time.Second == 0 &&
		lifetimes.VerificationMaximum%time.Second == 0 &&
		lifetimes.CaptureTokenDefault%time.Second == 0 &&
		lifetimes.CaptureTokenMaximum%time.Second == 0 &&
		lifetimes.OutcomePostDefault%time.Second == 0 &&
		lifetimes.OutcomePostMaximum%time.Second == 0
}
