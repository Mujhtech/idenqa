package managedlaunch

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// SessionCreator is the narrow real-session capability consumed by managed launch.
type SessionCreator interface {
	CreateManaged(context.Context, tenant.Scope, verification.SessionPrincipal, string, string, verification.SessionCreateInput) (verification.CreatedSession, error)
}

// Service verifies Cloud authority and translates it into a real Core session command.
type Service struct {
	verifier *Verifier
	creator  SessionCreator
	prepare  LaunchPreparation
}

// NewService constructs the managed-launch application boundary.
func NewService(verifier *Verifier, creator SessionCreator, prepare LaunchPreparation) (*Service, error) {
	if verifier == nil || creator == nil || prepare == nil {
		return nil, ErrInvalid
	}
	return &Service{verifier: verifier, creator: creator, prepare: prepare}, nil
}

// Create verifies one signed command and creates the pinned session through the
// same repository, outbox, worker, provider, and webhook path as tenant API use.
func (service *Service) Create(ctx context.Context, envelope Envelope) (verification.CreatedSession, error) {
	claims, err := service.verifier.Verify(envelope)
	if err != nil {
		return verification.CreatedSession{}, err
	}
	if err := service.prepare.Validate(ctx, claims); err != nil {
		return verification.CreatedSession{}, err
	}
	tenantID, err := id.ParseTenant(claims.CoreTenantID)
	if err != nil {
		return verification.CreatedSession{}, ErrInvalid
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return verification.CreatedSession{}, ErrInvalid
	}
	profileID, err := id.ParseProfile(claims.ProfileID)
	if err != nil {
		return verification.CreatedSession{}, ErrInvalid
	}
	policyID, err := id.ParsePolicy(claims.PolicyID)
	if err != nil {
		return verification.CreatedSession{}, ErrInvalid
	}
	principal, err := verification.NewWorkloadSessionPrincipal(claims.WorkloadID)
	if err != nil {
		return verification.CreatedSession{}, ErrInvalid
	}
	created, err := service.creator.CreateManaged(ctx, scope, principal, claims.ActorID, claims.CommandID, verification.SessionCreateInput{
		ProfileID:               profileID,
		PolicyID:                policyID,
		ExpectedProfileRevision: &claims.ProfileRevision,
		Locale:                  claims.Locale,
		Experience:              &experience.ResolutionRequest{Workflow: claims.ProfileID, Origin: claims.Origin, Locale: claims.Locale},
	})
	if err != nil {
		if errors.Is(err, verification.ErrProfileUnavailable) || errors.Is(err, verification.ErrSessionConflict) {
			return verification.CreatedSession{}, err
		}
		return verification.CreatedSession{}, fmt.Errorf("create managed verification session: %w", err)
	}
	if err := service.prepare.Prepare(ctx, claims, created.Session); err != nil {
		return verification.CreatedSession{}, err
	}
	return created, nil
}
