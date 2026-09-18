package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// FanoutName is the stable durable fanout task name.
	FanoutName = "webhook.fanout"
	// FanoutVersion identifies the reference-only fanout payload.
	FanoutVersion uint32 = 1
	// FanoutBatch bounds endpoints committed per fanout transaction.
	FanoutBatch = 256
)

var (
	// FanoutKey identifies the exact supported fanout payload.
	FanoutKey = mustKey(FanoutName, FanoutVersion)
	// FanoutRetry is the bounded fanout retry schedule.
	FanoutRetry = platformtask.RetryPolicy{MaxAttempts: 8, InitialBackoff: time.Second, MaximumBackoff: time.Hour, JitterPercent: 20}
)

// FanoutTarget identifies one pending catalogue event without its payload.
type FanoutTarget struct {
	TenantID id.Tenant
	EventID  id.Event
}

// FanoutIdentifiers supplies fanout and delivery identities.
type FanoutIdentifiers interface {
	IdentifierGenerator
	NewDelivery() (id.Delivery, error)
}

// NewFanoutIntent creates reference-only work for one pending catalogue event.
func NewFanoutIntent(generator IdentifierGenerator, scope tenant.Scope, eventID id.Event, scheduledAt time.Time) (platformtask.Intent, error) {
	return newFanoutIntent(generator, scope, eventID, "", scheduledAt)
}

func newFanoutIntent(generator IdentifierGenerator, scope tenant.Scope, eventID id.Event, cursor string, scheduledAt time.Time) (platformtask.Intent, error) {
	if generator == nil || scope.ID().IsZero() || eventID.IsZero() || scheduledAt.IsZero() || scheduledAt.Location() != time.UTC {
		return platformtask.Intent{}, platformtask.ErrInvalid
	}
	taskID, err := generator.NewTask()
	if err != nil {
		return platformtask.Intent{}, err
	}
	suffix := ""
	if cursor != "" {
		suffix = ":cursor:" + cursor
	}

	return platformtask.NewIntent(platformtask.IntentSpec{
		ID:             taskID,
		TenantID:       scope.ID(),
		Key:            FanoutKey,
		Queue:          taskheadgate.QueueDelivery,
		PartitionKey:   scope.ID().String(),
		IdempotencyKey: "webhook.fanout:" + eventID.String() + suffix,
		Payload: struct {
			EventID string `json:"event_id"`
		}{eventID.String()},
		ScheduledAt: scheduledAt,
		Deadline:    scheduledAt.Add(MaximumDeliveryDuration),
		Retry:       FanoutRetry,
		Retention:   SuccessfulMetadataRetention,
	})
}

func decodeFanout(encoded []byte) (id.Event, error) {
	var wire struct {
		EventID string `json:"event_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return id.Event{}, platformtask.ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return id.Event{}, platformtask.ErrInvalid
	}
	eventID, err := id.ParseEvent(wire.EventID)
	if err != nil {
		return id.Event{}, platformtask.ErrInvalid
	}

	return eventID, nil
}

// FanoutRepository supplies fenced fanout batches.
type FanoutRepository interface {
	FanoutEventWithin(context.Context, tenant.Scope, platformpostgres.Transaction, id.Event) (delivery.FanoutEvent, error)
	SubscribedEndpointsWithin(context.Context, tenant.Scope, platformpostgres.Transaction, webhookv1.Type, string, int) ([]id.WebhookEndpoint, error)
	CreateDeliveryIfAbsentWithin(context.Context, tenant.Scope, platformpostgres.Transaction, delivery.Intent) (bool, error)
	AdvanceFanoutWithin(context.Context, tenant.Scope, platformpostgres.Transaction, id.Event, string, int, bool, time.Time) error
}

// FanoutHandler commits one bounded endpoint batch per fenced task invocation.
type FanoutHandler struct {
	repository  FanoutRepository
	identifiers FanoutIdentifiers
	enqueuer    TransactionalEnqueuer
	now         func() time.Time
}

// NewFanoutHandler constructs bounded resumable fanout.
func NewFanoutHandler(repository FanoutRepository, identifiers FanoutIdentifiers, enqueuer TransactionalEnqueuer, now func() time.Time) (*FanoutHandler, error) {
	if repository == nil || identifiers == nil || enqueuer == nil || now == nil {
		return nil, delivery.ErrInvalid
	}

	return &FanoutHandler{repository: repository, identifiers: identifiers, enqueuer: enqueuer, now: now}, nil
}

// Handle fails closed when the runner cannot provide a fenced transaction.
func (*FanoutHandler) Handle(context.Context, platformtask.Delivery) platformtask.Result {
	return platformtask.Quarantine(errors.New("delivery: fenced transaction required"))
}

// Prepare performs one bounded batch entirely inside the task transaction.
func (handler *FanoutHandler) Prepare(_ context.Context, work platformtask.Delivery) (platformtask.TransactionWork, platformtask.Result) {
	eventID, err := decodeFanout(work.Intent.Payload())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	if work.Intent.Key() != FanoutKey {
		return nil, platformtask.Quarantine(delivery.ErrInvalid)
	}
	scope, err := tenant.NewScope(work.Intent.TenantID())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}

	return func(ctx context.Context, tx platformpostgres.Transaction) platformtask.Result {
		event, err := handler.repository.FanoutEventWithin(ctx, scope, tx, eventID)
		if err != nil {
			return taskError(err)
		}
		if event.State == delivery.EventCompleted {
			return platformtask.Complete()
		}
		endpoints, err := handler.repository.SubscribedEndpointsWithin(ctx, scope, tx, event.Type, event.Cursor, FanoutBatch+1)
		if err != nil {
			return taskError(err)
		}
		batch := endpoints
		more := false
		if len(endpoints) > FanoutBatch {
			batch, more = endpoints[:FanoutBatch], true
		}
		now := handler.now().UTC().Truncate(time.Microsecond)
		cursor := event.Cursor
		created := 0
		intents := make([]platformtask.Intent, 0, len(batch)+1)
		for _, endpointID := range batch {
			deliveryID, err := handler.identifiers.NewDelivery()
			if err != nil {
				return taskError(err)
			}
			intent, err := delivery.NewIntent(deliveryID, endpointID, event.ID, string(event.Type), event.Body, 8, now)
			if err != nil {
				return platformtask.Quarantine(err)
			}
			intent.BodyWrapping = event.BodyWrapping
			inserted, err := handler.repository.CreateDeliveryIfAbsentWithin(ctx, scope, tx, intent)
			if err != nil {
				return taskError(err)
			}
			cursor = endpointID.String()
			if !inserted {
				continue
			}
			created++
			attempt, err := NewAttemptIntent(handler.identifiers, scope, deliveryID, 1, now, now.Add(MaximumDeliveryDuration))
			if err != nil {
				return taskError(err)
			}
			intents = append(intents, attempt)
		}
		if more {
			successor, err := newFanoutIntent(handler.identifiers, scope, event.ID, cursor, now)
			if err != nil {
				return taskError(err)
			}
			intents = append(intents, successor)
		}
		if err := handler.repository.AdvanceFanoutWithin(ctx, scope, tx, event.ID, cursor, created, !more, now); err != nil {
			return taskError(err)
		}
		if len(intents) > 0 {
			if err := handler.enqueuer.EnqueueTx(ctx, tx, intents...); err != nil {
				return taskError(err)
			}
		}

		return platformtask.Complete()
	}, platformtask.Complete()
}

// FanoutDiscovery finds newly emitted catalogue events.
type FanoutDiscovery interface {
	ListReadyEvents(context.Context, time.Time, int) ([]FanoutTarget, error)
}

// FanoutCoordinator schedules one fanout task per newly emitted event.
type FanoutCoordinator struct {
	discovery   FanoutDiscovery
	identifiers IdentifierGenerator
	enqueuer    platformtask.Enqueuer
	clock       clock.Clock
	batchSize   int
}

// NewFanoutCoordinator constructs bounded durable fanout discovery.
func NewFanoutCoordinator(discovery FanoutDiscovery, identifiers IdentifierGenerator, enqueuer platformtask.Enqueuer, source clock.Clock, batchSize int) (*FanoutCoordinator, error) {
	if discovery == nil || identifiers == nil || enqueuer == nil || source == nil || batchSize < 1 || batchSize > CoordinationBatch {
		return nil, delivery.ErrInvalid
	}

	return &FanoutCoordinator{discovery: discovery, identifiers: identifiers, enqueuer: enqueuer, clock: source, batchSize: batchSize}, nil
}

// ScheduleReadyFanouts enqueues one fanout task per newly emitted event.
func (coordinator *FanoutCoordinator) ScheduleReadyFanouts(ctx context.Context) (int, error) {
	if coordinator == nil {
		return 0, delivery.ErrInvalid
	}
	now := coordinator.clock.Now().UTC().Truncate(time.Microsecond)
	targets, err := coordinator.discovery.ListReadyEvents(ctx, now, coordinator.batchSize)
	if err != nil {
		return 0, fmt.Errorf("list ready webhook events: %w", err)
	}
	if len(targets) > coordinator.batchSize {
		return 0, delivery.ErrInvalid
	}
	count := 0
	for _, target := range targets {
		scope, err := tenant.NewScope(target.TenantID)
		if err != nil || target.EventID.IsZero() {
			return count, delivery.ErrInvalid
		}
		intent, err := NewFanoutIntent(coordinator.identifiers, scope, target.EventID, now)
		if err != nil {
			return count, err
		}
		if err := coordinator.enqueuer.Enqueue(ctx, intent); err != nil {
			return count, fmt.Errorf("enqueue webhook fanout: %w", err)
		}
		count++
	}

	return count, nil
}

var _ platformtask.TransactionalHandler = (*FanoutHandler)(nil)
