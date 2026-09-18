package delivery

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

var webhookSecretPurpose = mustPurpose("delivery.webhook-secret")

// Repository owns tenant-scoped durable endpoint, secret, delivery, and attempt state.
type Repository interface {
	CreateEndpoint(context.Context, tenant.Scope, Endpoint) error
	UpdateEndpoint(context.Context, tenant.Scope, Endpoint, int64) error
	FindEndpoint(context.Context, tenant.Scope, id.WebhookEndpoint) (Endpoint, error)
	CreateDelivery(context.Context, tenant.Scope, Intent) error
	FindDelivery(context.Context, tenant.Scope, id.Delivery) (Intent, error)
}

// IdentifierGenerator supplies owned endpoint and delivery identifiers.
type IdentifierGenerator interface {
	NewWebhookEndpoint() (id.WebhookEndpoint, error)
	NewDelivery() (id.Delivery, error)
}

// Manager configures endpoints, rotates secrets, disables endpoints, and creates authorised replay intents.
type Manager struct {
	repository  Repository
	identifiers IdentifierGenerator
	wrapper     platformcrypto.KeyWrapper
	random      io.Reader
	now         func() time.Time
}

// NewManager constructs an endpoint and delivery manager.
func NewManager(repository Repository, identifiers IdentifierGenerator, wrapper platformcrypto.KeyWrapper, now func() time.Time) (*Manager, error) {
	if repository == nil || identifiers == nil || wrapper == nil || now == nil {
		return nil, ErrInvalid
	}
	return &Manager{repository: repository, identifiers: identifiers, wrapper: wrapper, random: rand.Reader, now: now}, nil
}

// CreateEndpoint stores a fresh 256-bit secret and the default completion subscription.
func (manager *Manager) CreateEndpoint(ctx context.Context, scope tenant.Scope, targetURL string) (Endpoint, []byte, error) {
	return manager.CreateEndpointSubscribed(ctx, scope, targetURL, DefaultEventTypes)
}

// CreateEndpointSubscribed stores a fresh secret and an explicit event selection.
func (manager *Manager) CreateEndpointSubscribed(ctx context.Context, scope tenant.Scope, targetURL string, eventTypes []string) (Endpoint, []byte, error) {
	if len(eventTypes) == 0 {
		eventTypes = DefaultEventTypes
	}
	selection, err := webhookv1.ValidateSubscriptions(eventTypes)
	if err != nil {
		return Endpoint{}, nil, ErrInvalid
	}
	identifier, err := manager.identifiers.NewWebhookEndpoint()
	if err != nil {
		return Endpoint{}, nil, fmt.Errorf("generate webhook endpoint id: %w", err)
	}
	now := manager.now().UTC()
	secret, wrapped, err := manager.newSecret(ctx, scope, identifier, 1)
	if err != nil {
		return Endpoint{}, nil, err
	}
	endpoint := Endpoint{
		ID:         identifier,
		URL:        targetURL,
		EventTypes: selection,
		Active:     Secret{Version: 1, Wrapped: wrapped, CreatedAt: now},
		Version:    1,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if endpoint.Validate() != nil {
		clear(secret)
		return Endpoint{}, nil, ErrInvalid
	}
	if err := manager.repository.CreateEndpoint(ctx, scope, endpoint); err != nil {
		clear(secret)
		return Endpoint{}, nil, fmt.Errorf("persist webhook endpoint: %w", err)
	}
	return endpoint, secret, nil
}

// Rotate creates a new secret and retains the previous version only for overlap.
func (manager *Manager) Rotate(ctx context.Context, scope tenant.Scope, endpointID id.WebhookEndpoint, overlap time.Duration) (Endpoint, []byte, error) {
	if overlap <= 0 || overlap > 24*time.Hour {
		return Endpoint{}, nil, ErrInvalid
	}
	current, err := manager.repository.FindEndpoint(ctx, scope, endpointID)
	if err != nil || current.Validate() != nil || !current.DisabledAt.IsZero() {
		return Endpoint{}, nil, errors.Join(ErrNotFound, err)
	}
	now := manager.now().UTC()
	version := current.Active.Version + 1
	secret, wrapped, err := manager.newSecret(ctx, scope, endpointID, version)
	if err != nil {
		return Endpoint{}, nil, err
	}
	previous := current.Active
	next := current
	next.Active = Secret{Version: version, Wrapped: wrapped, CreatedAt: now}
	next.Previous, next.PreviousValidUntil = &previous, now.Add(overlap)
	next.Version, next.UpdatedAt = current.Version+1, now
	if err := manager.repository.UpdateEndpoint(ctx, scope, next, current.Version); err != nil {
		clear(secret)
		return Endpoint{}, nil, err
	}
	return next, secret, nil
}

// Subscribe replaces an endpoint's event selection under optimistic concurrency.
func (manager *Manager) Subscribe(ctx context.Context, scope tenant.Scope, endpointID id.WebhookEndpoint, expectedVersion int64, eventTypes []string) (Endpoint, error) {
	selection, err := webhookv1.ValidateSubscriptions(eventTypes)
	if err != nil {
		return Endpoint{}, ErrInvalid
	}
	current, err := manager.repository.FindEndpoint(ctx, scope, endpointID)
	if err != nil {
		return Endpoint{}, err
	}
	if current.Version != expectedVersion || !current.DisabledAt.IsZero() {
		return Endpoint{}, ErrConflict
	}
	current.EventTypes = selection
	current.Version, current.UpdatedAt = current.Version+1, manager.now().UTC()
	if current.Validate() != nil {
		return Endpoint{}, ErrInvalid
	}
	if err := manager.repository.UpdateEndpoint(ctx, scope, current, expectedVersion); err != nil {
		return Endpoint{}, err
	}

	return current, nil
}

// Disable prevents new attempts while retaining immutable history.
func (manager *Manager) Disable(ctx context.Context, scope tenant.Scope, endpointID id.WebhookEndpoint, reason string) error {
	current, err := manager.repository.FindEndpoint(ctx, scope, endpointID)
	if err != nil {
		return err
	}
	if current.DisabledAt.IsZero() {
		current.DisabledAt, current.DisabledReason = manager.now().UTC(), reason
		current.UpdatedAt, current.Version = current.DisabledAt, current.Version+1
	}
	if current.Validate() != nil {
		return ErrInvalid
	}
	return manager.repository.UpdateEndpoint(ctx, scope, current, current.Version-1)
}

// CreateDelivery persists one at-least-once event intent.
func (manager *Manager) CreateDelivery(ctx context.Context, scope tenant.Scope, endpointID id.WebhookEndpoint, eventID id.Event, eventType string, body []byte) (Intent, error) {
	endpoint, err := manager.repository.FindEndpoint(ctx, scope, endpointID)
	if err != nil {
		return Intent{}, err
	}
	if !endpoint.DisabledAt.IsZero() {
		return Intent{}, ErrDisabled
	}
	identifier, err := manager.identifiers.NewDelivery()
	if err != nil {
		return Intent{}, err
	}
	intent, err := NewIntent(identifier, endpointID, eventID, eventType, body, 8, manager.now().UTC())
	if err != nil {
		return Intent{}, err
	}
	if err := manager.repository.CreateDelivery(ctx, scope, intent); err != nil {
		return Intent{}, err
	}
	return intent, nil
}

// Replay creates a distinct delivery while preserving the signed event identity and exact body.
func (manager *Manager) Replay(ctx context.Context, scope tenant.Scope, originalID id.Delivery, eventID id.Event) (Intent, error) {
	original, err := manager.repository.FindDelivery(ctx, scope, originalID)
	if err != nil {
		return Intent{}, err
	}
	if eventID != original.EventID {
		return Intent{}, ErrConflict
	}
	endpoint, err := manager.repository.FindEndpoint(ctx, scope, original.EndpointID)
	if err != nil || !endpoint.DisabledAt.IsZero() {
		return Intent{}, errors.Join(ErrDisabled, err)
	}
	identifier, err := manager.identifiers.NewDelivery()
	if err != nil {
		return Intent{}, err
	}
	replay, err := NewIntent(identifier, original.EndpointID, eventID, original.EventType, original.Body, original.MaxAttempts, manager.now().UTC())
	if err != nil {
		return Intent{}, err
	}
	replay.ReplayOf = original.ID
	replay.BodyWrapping = original.BodyWrapping
	if err := manager.repository.CreateDelivery(ctx, scope, replay); err != nil {
		return Intent{}, err
	}
	return replay, nil
}

func (manager *Manager) newSecret(ctx context.Context, scope tenant.Scope, endpoint id.WebhookEndpoint, version int64) ([]byte, kms.WrappedKey, error) {
	secret := make([]byte, 32)
	if _, err := io.ReadFull(manager.random, secret); err != nil {
		return nil, kms.WrappedKey{}, errors.New("delivery: generate signing secret")
	}
	contextData := []byte(fmt.Sprintf("v1\n%s\n%s\n%d", scope.ID().String(), endpoint.String(), version))
	wrapped, err := manager.wrapper.Wrap(ctx, webhookSecretPurpose, secret, contextData)
	if err != nil {
		clear(secret)
		return nil, kms.WrappedKey{}, errors.New("delivery: protect signing secret")
	}
	return secret, wrapped, nil
}

func mustPurpose(value string) kms.Purpose {
	purpose, err := kms.NewPurpose(value)
	if err != nil {
		panic(err)
	}
	return purpose
}
