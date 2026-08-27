package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// SessionIDGenerator is the identifier capability consumed by SessionService.
type SessionIDGenerator interface {
	NewVerification() (id.Verification, error)
	NewCaptureToken() (id.CaptureToken, error)
	NewEvent() (id.Event, error)
	NewDecision() (id.Decision, error)
}

// SessionRepository is the tenant-scoped persistence boundary consumed by
// session creation and tenant reads.
type SessionRepository interface {
	Create(context.Context, tenant.Scope, SessionCreateMutation) (SessionCreation, error)
	FindSession(context.Context, tenant.Scope, id.Verification) (Session, error)
}

// SessionCreateInput contains the tenant-selected profile and optional bounded
// lifetimes. Nil lifetimes select deployment defaults.
type SessionCreateInput struct {
	ProfileID       id.Profile
	PolicyID        id.Policy
	VerificationTTL *time.Duration
	CaptureTokenTTL *time.Duration
}

// CreatedSession carries a display-once bearer token alongside the durable
// non-secret session. The token must be explicitly revealed by its transport.
type CreatedSession struct {
	Session      Session
	CaptureToken access.PresentedCaptureToken
}

// SessionLifetimes holds deployment defaults, maxima, and replay retention.
type SessionLifetimes struct {
	VerificationDefault  time.Duration
	VerificationMaximum  time.Duration
	CaptureTokenDefault  time.Duration
	CaptureTokenMaximum  time.Duration
	IdempotencyRetention time.Duration
}

// SessionService authorises and coordinates verification-session use cases.
type SessionService struct {
	repository  SessionRepository
	identifiers SessionIDGenerator
	signer      *access.CaptureTokenSigner
	clock       clock.Clock
	lifetimes   SessionLifetimes
	region      string
}

// NewSessionService constructs the verification-session application service.
func NewSessionService(
	repository SessionRepository,
	identifiers SessionIDGenerator,
	signer *access.CaptureTokenSigner,
	source clock.Clock,
	lifetimes SessionLifetimes,
	region string,
) (*SessionService, error) {
	if repository == nil || identifiers == nil || signer == nil || source == nil ||
		!validSessionLifetimes(lifetimes) || !validRegion(region) {
		return nil, errors.New("verification: session service dependencies and lifetimes are required")
	}

	return &SessionService{
		repository:  repository,
		identifiers: identifiers,
		signer:      signer,
		clock:       source,
		lifetimes:   lifetimes,
		region:      region,
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
	verificationTTL, captureTTL, err := service.resolveLifetimes(input)
	if err != nil {
		return CreatedSession{}, err
	}
	canonical, err := json.Marshal(struct {
		ProfileID              string `json:"capture_profile_id"`
		PolicyID               string `json:"policy_id"`
		VerificationTTLSeconds int64  `json:"verification_ttl_seconds"`
		CaptureTokenTTLSeconds int64  `json:"capture_token_ttl_seconds"`
		Region                 string `json:"region"`
	}{
		ProfileID:              input.ProfileID.String(),
		PolicyID:               input.PolicyID.String(),
		VerificationTTLSeconds: int64(verificationTTL / time.Second),
		CaptureTokenTTLSeconds: int64(captureTTL / time.Second),
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
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return CreatedSession{}, fmt.Errorf("generate verification event id: %w", err)
	}
	decisionID, err := service.identifiers.NewDecision()
	if err != nil {
		return CreatedSession{}, fmt.Errorf("generate policy decision id: %w", err)
	}
	creation, err := service.repository.Create(ctx, authority.TenantScope(), SessionCreateMutation{
		SessionID:          sessionID,
		CaptureTokenID:     tokenID,
		EventID:            eventID,
		ProfileID:          input.ProfileID,
		PolicyID:           input.PolicyID,
		DecisionID:         decisionID,
		Region:             service.region,
		Actor:              authority.Principal().KeyID(),
		CaptureKeyVersion:  service.signer.ActiveVersion(),
		CreatedAt:          now,
		SessionExpiresAt:   now.Add(verificationTTL),
		CaptureTokenExpiry: now.Add(captureTTL),
		Idempotency:        retry,
	})
	if err != nil {
		return CreatedSession{}, err
	}
	presented, err := service.signer.Sign(creation.Credential)
	if err != nil {
		return CreatedSession{}, fmt.Errorf("sign capture token: %w", err)
	}

	return CreatedSession{Session: creation.Session, CaptureToken: presented}, nil
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

func (service *SessionService) resolveLifetimes(input SessionCreateInput) (time.Duration, time.Duration, error) {
	if input.ProfileID.IsZero() || input.PolicyID.IsZero() {
		return 0, 0, errors.New("verification: capture profile is required")
	}
	verificationTTL := service.lifetimes.VerificationDefault
	if input.VerificationTTL != nil {
		verificationTTL = *input.VerificationTTL
	}
	captureTTL := service.lifetimes.CaptureTokenDefault
	if input.CaptureTokenTTL != nil {
		captureTTL = *input.CaptureTokenTTL
	}
	if verificationTTL <= 0 || verificationTTL > service.lifetimes.VerificationMaximum ||
		captureTTL <= 0 || captureTTL > service.lifetimes.CaptureTokenMaximum ||
		captureTTL > verificationTTL ||
		verificationTTL%time.Second != 0 || captureTTL%time.Second != 0 {
		return 0, 0, errors.New("verification: requested lifetimes are outside configured bounds")
	}

	return verificationTTL, captureTTL, nil
}

func validSessionLifetimes(lifetimes SessionLifetimes) bool {
	return lifetimes.VerificationDefault > 0 &&
		lifetimes.VerificationMaximum >= lifetimes.VerificationDefault &&
		lifetimes.CaptureTokenDefault > 0 &&
		lifetimes.CaptureTokenMaximum >= lifetimes.CaptureTokenDefault &&
		lifetimes.CaptureTokenMaximum <= lifetimes.VerificationMaximum &&
		lifetimes.IdempotencyRetention > 0 &&
		lifetimes.VerificationDefault%time.Second == 0 &&
		lifetimes.VerificationMaximum%time.Second == 0 &&
		lifetimes.CaptureTokenDefault%time.Second == 0 &&
		lifetimes.CaptureTokenMaximum%time.Second == 0
}
