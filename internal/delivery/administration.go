package delivery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// PermissionConfigure is required for endpoint creation, rotation, and disablement.
	PermissionConfigure = "webhooks:configure"
	// PermissionReplay is separately required for deliberate delivery replay.
	PermissionReplay = "webhooks:replay"
)

// Authorizer rechecks the authenticated actor at the application boundary.
type Authorizer interface {
	Authorize(context.Context, tenant.Scope, string, string) error
}

// Administration exposes only authorised endpoint and replay operations.
type Administration struct {
	authorizer Authorizer
	manager    *Manager
}

// NewAdministration constructs the authorised delivery administration boundary.
func NewAdministration(authorizer Authorizer, manager *Manager) (*Administration, error) {
	if authorizer == nil || manager == nil {
		return nil, ErrInvalid
	}
	return &Administration{authorizer: authorizer, manager: manager}, nil
}

// CreateEndpoint rechecks configure permission and returns the secret once.
func (admin *Administration) CreateEndpoint(ctx context.Context, scope tenant.Scope, actorID, targetURL string) (Endpoint, []byte, error) {
	if err := admin.authorize(ctx, scope, actorID, PermissionConfigure); err != nil {
		return Endpoint{}, nil, err
	}
	return admin.manager.CreateEndpoint(ctx, scope, targetURL)
}

// Rotate rechecks configure permission and returns the new secret once.
func (admin *Administration) Rotate(ctx context.Context, scope tenant.Scope, actorID string, endpointID id.WebhookEndpoint, overlap time.Duration) (Endpoint, []byte, error) {
	if err := admin.authorize(ctx, scope, actorID, PermissionConfigure); err != nil {
		return Endpoint{}, nil, err
	}
	return admin.manager.Rotate(ctx, scope, endpointID, overlap)
}

// Disable rechecks configure permission before the irreversible transition.
func (admin *Administration) Disable(ctx context.Context, scope tenant.Scope, actorID string, endpointID id.WebhookEndpoint, reason string) error {
	if reason == "" {
		return ErrInvalid
	}
	if err := admin.authorize(ctx, scope, actorID, PermissionConfigure); err != nil {
		return err
	}
	return admin.manager.Disable(ctx, scope, endpointID, reason)
}

// Replay rechecks the separate replay permission and preserves delivery lineage.
func (admin *Administration) Replay(ctx context.Context, scope tenant.Scope, actorID string, originalID id.Delivery, eventID id.Event) (Intent, error) {
	if err := admin.authorize(ctx, scope, actorID, PermissionReplay); err != nil {
		return Intent{}, err
	}
	return admin.manager.Replay(ctx, scope, originalID, eventID)
}

func (admin *Administration) authorize(ctx context.Context, scope tenant.Scope, actorID, permission string) error {
	if admin == nil || admin.authorizer == nil || admin.manager == nil || actorID == "" || scope.ID().IsZero() {
		return ErrInvalid
	}
	if err := admin.authorizer.Authorize(ctx, scope, actorID, permission); err != nil {
		return fmt.Errorf("authorize delivery administration: %w", errors.Join(ErrUnauthorized, err))
	}
	return nil
}
