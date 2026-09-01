// Package task owns the durable webhook-delivery task contract.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// DeliverName is the stable durable task name.
	DeliverName = "webhook.deliver"
	// DeliverVersion is the exact initial payload version.
	DeliverVersion uint32 = 1
	// SuccessfulMetadataRetention bounds successful queue metadata retention.
	SuccessfulMetadataRetention = 30 * 24 * time.Hour
)

var (
	// DeliverKey identifies the exact supported task payload.
	DeliverKey = mustKey(DeliverName, DeliverVersion)
	// DeliverRetry is the bounded delivery retry schedule.
	DeliverRetry = platformtask.RetryPolicy{MaxAttempts: 8, InitialBackoff: time.Second, MaximumBackoff: time.Hour, JitterPercent: 20}
)

// IdentifierGenerator supplies opaque durable task identifiers.
type IdentifierGenerator interface{ NewTask() (id.Task, error) }

// NewIntent creates reference-only work for one durable delivery.
func NewIntent(generator IdentifierGenerator, scope tenant.Scope, deliveryID id.Delivery, scheduledAt time.Time) (platformtask.Intent, error) {
	if generator == nil || scope.ID().IsZero() || deliveryID.IsZero() || scheduledAt.IsZero() || scheduledAt.Location() != time.UTC {
		return platformtask.Intent{}, platformtask.ErrInvalid
	}
	taskID, err := generator.NewTask()
	if err != nil {
		return platformtask.Intent{}, err
	}
	return platformtask.NewIntent(
		platformtask.IntentSpec{
			ID:             taskID,
			TenantID:       scope.ID(),
			Key:            DeliverKey,
			Queue:          taskheadgate.QueueDelivery,
			PartitionKey:   scope.ID().String(),
			IdempotencyKey: "webhook.deliver:" + deliveryID.String(),
			Payload: struct {
				DeliveryID string `json:"delivery_id"`
			}{deliveryID.String()},
			ScheduledAt: scheduledAt,
			Deadline:    scheduledAt.Add(24 * time.Hour),
			Retry:       DeliverRetry,
			Retention:   SuccessfulMetadataRetention,
		},
	)
}

// Decode strictly reads the v1 reference-only payload.
func Decode(encoded []byte) (id.Delivery, error) {
	var wire struct {
		DeliveryID string `json:"delivery_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return id.Delivery{}, platformtask.ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return id.Delivery{}, platformtask.ErrInvalid
	}
	deliveryID, err := id.ParseDelivery(wire.DeliveryID)
	if err != nil {
		return id.Delivery{}, fmt.Errorf("%w: delivery id", platformtask.ErrInvalid)
	}
	return deliveryID, nil
}

func mustKey(name string, version uint32) platformtask.Key {
	parsed, err := platformtask.NewName(name)
	if err != nil {
		panic(err)
	}
	return platformtask.Key{Name: parsed, Version: version}
}
