package delivery

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// EndpointView is safe configuration metadata, without wrapped or plaintext keys.
type EndpointView struct {
	ID                 string     `json:"id"`
	URL                string     `json:"url"`
	Version            int64      `json:"version"`
	SecretVersion      int64      `json:"secret_version"`
	PreviousValidUntil *time.Time `json:"previous_valid_until,omitempty"`
	DisabledAt         *time.Time `json:"disabled_at,omitempty"`
	DisabledReason     string     `json:"disabled_reason,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// View omits the event payload and signature material.
type View struct {
	ID            string     `json:"id"`
	EndpointID    string     `json:"endpoint_id"`
	EventID       string     `json:"event_id"`
	EventType     string     `json:"event_type"`
	State         State      `json:"state"`
	AttemptCount  int32      `json:"attempt_count"`
	MaxAttempts   int32      `json:"max_attempts"`
	NextAttemptAt time.Time  `json:"next_attempt_at"`
	DeliveredAt   *time.Time `json:"delivered_at,omitempty"`
	ReplayOf      string     `json:"replay_of,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// AttemptView exposes bounded callback diagnostics and a sanitised response
// excerpt. The excerpt is untrusted receiver content and may be truncated.
type AttemptView struct {
	Number            int32     `json:"number"`
	SecretVersion     int64     `json:"secret_version"`
	StatusCode        int       `json:"status_code"`
	ErrorClass        string    `json:"error_class"`
	RetryAfterMS      int64     `json:"retry_after_ms"`
	ResponseBody      *string   `json:"response_body"`
	ResponseTruncated bool      `json:"response_truncated"`
	CompletedAt       time.Time `json:"completed_at"`
}

// ManagementResult is the safe snapshot retained for principal-scoped replay.
type ManagementResult struct {
	Endpoint *EndpointView `json:"endpoint,omitempty"`
	Delivery *View         `json:"delivery,omitempty"`
	Replayed bool          `json:"replayed"`
}

// ManagementCommand is a closed administrative operation, never an arbitrary update.
type ManagementCommand struct {
	Operation       string `json:"operation"`
	EndpointID      string `json:"endpoint_id,omitempty"`
	DeliveryID      string `json:"delivery_id,omitempty"`
	URL             string `json:"url,omitempty"`
	ExpectedVersion int64  `json:"expected_version,omitempty"`
	OverlapSeconds  int64  `json:"overlap_seconds,omitempty"`
	Reason          string `json:"reason,omitempty"`
}

// ManagementRepository runs the existing Manager against a transaction-bound
// repository and atomically retains safe audit/replay data. Secret bytes returned
// by work must be cleared on any failure and never persisted in the replay result.
type ManagementRepository interface {
	ApplyManagement(context.Context, tenant.Scope, idempotency.Request, id.Event, ManagementCommand, func(Repository) (ManagementResult, []byte, error)) (ManagementResult, []byte, error)
	Endpoints(context.Context, tenant.Scope, string, int) ([]EndpointView, error)
	Endpoint(context.Context, tenant.Scope, id.WebhookEndpoint) (EndpointView, error)
	Deliveries(context.Context, tenant.Scope, id.WebhookEndpoint, string, int) ([]View, error)
	Delivery(context.Context, tenant.Scope, id.Delivery) (View, error)
	Attempts(context.Context, tenant.Scope, id.Delivery) ([]AttemptView, error)
}

// ManagementIdentifiers supplies internal identities, never client event IDs.
type ManagementIdentifiers interface {
	IdentifierGenerator
	NewEvent() (id.Event, error)
}

// Management exposes public webhook use cases through existing owned semantics.
type Management struct {
	repository  ManagementRepository
	identifiers ManagementIdentifiers
	wrapper     platformcrypto.KeyWrapper
	now         func() time.Time
	retention   time.Duration
}

// NewManagement constructs administration and safe inspection. A nil wrapper
// leaves inspection, disablement and replay available but cannot create secrets.
func NewManagement(repository ManagementRepository, identifiers ManagementIdentifiers, wrapper platformcrypto.KeyWrapper, now func() time.Time, retention time.Duration) (*Management, error) {
	if repository == nil || identifiers == nil || now == nil || retention <= 0 {
		return nil, ErrInvalid
	}
	return &Management{
		repository:  repository,
		identifiers: identifiers,
		wrapper:     wrapper,
		now:         now,
		retention:   retention,
	}, nil
}

// Execute checks application permission before any state or replay access.
func (service *Management) Execute(ctx context.Context, actor access.Context, key string, command ManagementCommand) (ManagementResult, []byte, error) {
	permission := access.PermissionWebhooksConfigure
	if command.Operation == "replay" {
		permission = access.PermissionWebhooksReplay
	}
	if err := actor.Require(permission); err != nil {
		return ManagementResult{}, nil, err
	}
	if err := validateManagementCommand(command); err != nil {
		return ManagementResult{}, nil, err
	}
	canonical, err := json.Marshal(command)
	if err != nil {
		return ManagementResult{}, nil, err
	}
	now := service.now().UTC().Truncate(time.Microsecond)
	retry, err := idempotency.NewRequest(actor.TenantScope().ID(), actor.Principal().KeyID(), "webhooks."+command.Operation, key, canonical, now, service.retention)
	if err != nil {
		return ManagementResult{}, nil, err
	}
	event, err := service.identifiers.NewEvent()
	if err != nil {
		return ManagementResult{}, nil, err
	}
	return service.repository.ApplyManagement(ctx, actor.TenantScope(), retry, event, command, func(repository Repository) (ManagementResult, []byte, error) {
		// Replay lookup in the repository occurs before any generation or KMS call.
		manager := &Manager{repository: repository, identifiers: service.identifiers, wrapper: service.wrapper, random: rand.Reader, now: func() time.Time { return service.now().UTC().Truncate(time.Microsecond) }}
		return service.execute(ctx, actor.TenantScope(), manager, command)
	})
}

func (service *Management) execute(ctx context.Context, scope tenant.Scope, manager *Manager, command ManagementCommand) (ManagementResult, []byte, error) {
	if (command.Operation == "create" || command.Operation == "rotate") && service.wrapper == nil {
		return ManagementResult{}, nil, ErrSigningUnavailable
	}
	var endpoint Endpoint
	var secret []byte
	var err error
	switch command.Operation {
	case "create":
		endpoint, secret, err = manager.CreateEndpoint(ctx, scope, command.URL)
	case "replay":
		identifier, _ := id.ParseDelivery(command.DeliveryID)
		original, findErr := manager.repository.FindDelivery(ctx, scope, identifier)
		if findErr != nil {
			return ManagementResult{}, nil, findErr
		}
		if original.State != StateExhausted {
			return ManagementResult{}, nil, ErrConflict
		}
		replay, replayErr := manager.Replay(ctx, scope, identifier, original.EventID)
		if replayErr != nil {
			return ManagementResult{}, nil, replayErr
		}
		view := ViewDelivery(replay)
		return ManagementResult{Delivery: &view}, nil, nil
	default:
		identifier, _ := id.ParseWebhookEndpoint(command.EndpointID)
		current, findErr := manager.repository.FindEndpoint(ctx, scope, identifier)
		if findErr != nil {
			return ManagementResult{}, nil, findErr
		}
		if current.Version != command.ExpectedVersion || !current.DisabledAt.IsZero() {
			return ManagementResult{}, nil, ErrConflict
		}
		if command.Operation == "rotate" {
			if current.Previous != nil && service.now().UTC().Before(current.PreviousValidUntil) {
				return ManagementResult{}, nil, ErrConflict
			}
			endpoint, secret, err = manager.Rotate(ctx, scope, identifier, time.Duration(command.OverlapSeconds)*time.Second)
		} else {
			err = manager.Disable(ctx, scope, identifier, command.Reason)
			if err == nil {
				endpoint, err = manager.repository.FindEndpoint(ctx, scope, identifier)
			}
		}
	}
	if err != nil {
		clear(secret)
		return ManagementResult{}, nil, err
	}
	view := ViewEndpoint(endpoint)
	return ManagementResult{Endpoint: &view}, secret, nil
}

func validateManagementCommand(command ManagementCommand) error {
	switch command.Operation {
	case "create":
		if command.URL == "" || len(command.URL) > 2048 {
			return ErrInvalid
		}
	case "rotate", "disable":
		if _, err := id.ParseWebhookEndpoint(command.EndpointID); err != nil {
			return ErrNotFound
		}
		if command.ExpectedVersion < 1 {
			return ErrInvalid
		}
		if command.Operation == "rotate" && (command.OverlapSeconds < 1 || command.OverlapSeconds > 86400) {
			return ErrInvalid
		}
		if command.Operation == "disable" && (!validToken(command.Reason) || len(command.Reason) > 64) {
			return ErrInvalid
		}
	case "replay":
		if _, err := id.ParseDelivery(command.DeliveryID); err != nil {
			return ErrNotFound
		}
		if !validToken(command.Reason) || len(command.Reason) > 64 {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}

// Endpoints returns one bounded page of metadata.
func (service *Management) Endpoints(ctx context.Context, actor access.Context, after string, limit int) ([]EndpointView, error) {
	if err := actor.Require(access.PermissionWebhooksRead); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	return service.repository.Endpoints(ctx, actor.TenantScope(), after, limit)
}

// Endpoint returns one tenant-owned endpoint's safe metadata.
func (service *Management) Endpoint(ctx context.Context, actor access.Context, identifier id.WebhookEndpoint) (EndpointView, error) {
	if err := actor.Require(access.PermissionWebhooksRead); err != nil {
		return EndpointView{}, err
	}
	return service.repository.Endpoint(ctx, actor.TenantScope(), identifier)
}

// Deliveries returns bounded metadata for one endpoint.
func (service *Management) Deliveries(ctx context.Context, actor access.Context, endpoint id.WebhookEndpoint, after string, limit int) ([]View, error) {
	if err := actor.Require(access.PermissionWebhooksRead); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	return service.repository.Deliveries(ctx, actor.TenantScope(), endpoint, after, limit)
}

// Delivery returns one payload-free delivery record.
func (service *Management) Delivery(ctx context.Context, actor access.Context, identifier id.Delivery) (View, error) {
	if err := actor.Require(access.PermissionWebhooksRead); err != nil {
		return View{}, err
	}
	return service.repository.Delivery(ctx, actor.TenantScope(), identifier)
}

// Attempts returns the bounded append-only diagnostics for one visible delivery.
func (service *Management) Attempts(ctx context.Context, actor access.Context, identifier id.Delivery) ([]AttemptView, error) {
	if err := actor.Require(access.PermissionWebhooksRead); err != nil {
		return nil, err
	}
	return service.repository.Attempts(ctx, actor.TenantScope(), identifier)
}

// ViewEndpoint strips all key material from internal state.
func ViewEndpoint(value Endpoint) EndpointView {
	return EndpointView{
		ID:                 value.ID.String(),
		URL:                value.URL,
		Version:            value.Version,
		SecretVersion:      value.Active.Version,
		PreviousValidUntil: optionalTime(value.PreviousValidUntil),
		DisabledAt:         optionalTime(value.DisabledAt),
		DisabledReason:     value.DisabledReason,
		CreatedAt:          value.CreatedAt.UTC(),
		UpdatedAt:          value.UpdatedAt.UTC(),
	}
}

// ViewDelivery strips event bodies and integrity/signature material from inspection.
func ViewDelivery(value Intent) View {
	return View{
		ID:            value.ID.String(),
		EndpointID:    value.EndpointID.String(),
		EventID:       value.EventID.String(),
		EventType:     value.EventType,
		State:         value.State,
		AttemptCount:  value.AttemptCount,
		MaxAttempts:   value.MaxAttempts,
		NextAttemptAt: value.NextAttemptAt.UTC(),
		DeliveredAt:   optionalTime(value.DeliveredAt),
		ReplayOf:      value.ReplayOf.String(),
		CreatedAt:     value.CreatedAt.UTC(),
		UpdatedAt:     value.UpdatedAt.UTC(),
	}
}
func optionalTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	utc := value.UTC()
	return &utc
}

// String prevents accidental command logging from exposing callback configuration.
func (ManagementCommand) String() string { return "[REDACTED]" }

// GoString redacts callback configuration in diagnostic formatting.
func (ManagementCommand) GoString() string { return "[REDACTED]" }
