// Package task owns the durable webhook-delivery task contract.
package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// DeliverName is the stable durable task name.
	DeliverName = "webhook.deliver"
	// DeliverVersion identifies logical callback attempts independently of worker retries.
	DeliverVersion uint32 = 2
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
	return NewAttemptIntent(generator, scope, deliveryID, 1, scheduledAt, scheduledAt.Add(MaximumDeliveryDuration))
}

// MaximumDeliveryDuration bounds the complete delivery lifecycle across attempts.
const MaximumDeliveryDuration = 24 * time.Hour

// NewAttemptIntent creates reference-only v2 work for one logical callback attempt.
func NewAttemptIntent(generator IdentifierGenerator, scope tenant.Scope, deliveryID id.Delivery, attempt int32, scheduledAt, deadline time.Time) (platformtask.Intent, error) {
	return newAttemptIntent(generator, scope, deliveryID, attempt, scheduledAt, deadline, "")
}

func newAttemptIntent(generator IdentifierGenerator, scope tenant.Scope, deliveryID id.Delivery, attempt int32, scheduledAt, deadline time.Time, suffix string) (platformtask.Intent, error) {
	if generator == nil || scope.ID().IsZero() || deliveryID.IsZero() || attempt < 1 || attempt > 20 || scheduledAt.IsZero() || scheduledAt.Location() != time.UTC || deadline.Location() != time.UTC || !deadline.After(scheduledAt) {
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
			IdempotencyKey: "webhook.deliver:" + deliveryID.String() + ":attempt:" + strconv.FormatInt(int64(attempt), 10) + suffix,
			Payload: struct {
				DeliveryID    string `json:"delivery_id"`
				AttemptNumber int32  `json:"attempt_number"`
			}{deliveryID.String(), attempt},
			ScheduledAt: scheduledAt,
			Deadline:    deadline,
			Retry:       DeliverRetry,
			Retention:   SuccessfulMetadataRetention,
		},
	)
}

// Decode strictly reads a reference-only delivery payload.
func Decode(encoded []byte) (id.Delivery, error) {
	deliveryID, _, err := decodeAttempt(encoded)
	return deliveryID, err
}

func decodeAttempt(encoded []byte) (id.Delivery, int32, error) {
	var wire struct {
		DeliveryID    string `json:"delivery_id"`
		AttemptNumber int32  `json:"attempt_number"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return id.Delivery{}, 0, platformtask.ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return id.Delivery{}, 0, platformtask.ErrInvalid
	}
	deliveryID, err := id.ParseDelivery(wire.DeliveryID)
	if err != nil {
		return id.Delivery{}, 0, fmt.Errorf("%w: delivery id", platformtask.ErrInvalid)
	}
	if wire.AttemptNumber < 1 || wire.AttemptNumber > 20 {
		return id.Delivery{}, 0, platformtask.ErrInvalid
	}
	return deliveryID, wire.AttemptNumber, nil
}

func mustKey(name string, version uint32) platformtask.Key {
	parsed, err := platformtask.NewName(name)
	if err != nil {
		panic(err)
	}
	return platformtask.Key{Name: parsed, Version: version}
}
