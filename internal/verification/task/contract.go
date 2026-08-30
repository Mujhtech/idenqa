// Package task owns the durable background-work adapter for verification.
// Payloads contain opaque identifiers only; runner inputs remain authoritative
// verification state and never travel through the queue.
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
	// ExecuteName is the stable durable verification execution task name.
	ExecuteName = "verification.execute"
	// ExecuteVersion is the exact first execution payload version.
	ExecuteVersion uint32 = 1
	// ReconcileName is the stable durable verification reconciliation task name.
	ReconcileName = "verification.reconcile"
	// ReconcileVersion is the exact first reconciliation payload version.
	ReconcileVersion uint32 = 1

	// SuccessfulMetadataRetention bounds successful queue metadata retention.
	SuccessfulMetadataRetention = 30 * 24 * time.Hour
	// MaximumExecuteDuration caps one execution deadline from scheduling.
	MaximumExecuteDuration = 10 * time.Minute
	// MaximumReconcileDuration caps one reconciliation deadline from scheduling.
	MaximumReconcileDuration = 24 * time.Hour
)

var (
	// ExecuteKey identifies the exact first execution task contract.
	ExecuteKey = mustKey(ExecuteName, ExecuteVersion)
	// ReconcileKey identifies the exact first reconciliation task contract.
	ReconcileKey = mustKey(ReconcileName, ReconcileVersion)
	// ExecuteRetry is the selected bounded execution retry policy.
	ExecuteRetry = platformtask.RetryPolicy{MaxAttempts: 5, InitialBackoff: time.Second,
		MaximumBackoff: 30 * time.Second, JitterPercent: 20}
	// ReconcileRetry is the selected bounded reconciliation retry policy.
	ReconcileRetry = platformtask.RetryPolicy{MaxAttempts: 8, InitialBackoff: 30 * time.Second,
		MaximumBackoff: 15 * time.Minute, JitterPercent: 20}
)

// IdentifierGenerator supplies opaque durable task identifiers.
type IdentifierGenerator interface{ NewTask() (id.Task, error) }

// ExecutePayload addresses one durable check attempt without carrying evidence.
type ExecutePayload struct {
	CheckID   id.Check
	AttemptID id.Attempt
}

// ReconcilePayload addresses one durable attempt requiring reconciliation.
type ReconcilePayload struct {
	CheckID   id.Check
	AttemptID id.Attempt
}

// IntentMetadata contains safe queue scheduling and correlation metadata.
type IntentMetadata struct {
	ScheduledAt   time.Time
	Deadline      time.Time
	CorrelationID string
	CausationID   string
	Traceparent   string
	Tracestate    string
}

// NewExecuteIntent constructs the selected execution task contract.
func NewExecuteIntent(
	generator IdentifierGenerator,
	scope tenant.Scope,
	payload ExecutePayload,
	metadata IntentMetadata,
) (platformtask.Intent, error) {
	if generator == nil || scope.ID().IsZero() || payload.CheckID.IsZero() || payload.AttemptID.IsZero() {
		return platformtask.Intent{}, fmt.Errorf("%w: verification execute intent", platformtask.ErrInvalid)
	}
	deadline, err := boundedDeadline(metadata.ScheduledAt, metadata.Deadline, MaximumExecuteDuration)
	if err != nil {
		return platformtask.Intent{}, err
	}
	return newIntent(generator, scope, ExecuteKey, taskheadgate.QueueVerification,
		"verification.execute:"+payload.AttemptID.String(), encodeExecute(payload), metadata, deadline, ExecuteRetry)
}

// NewReconcileIntent constructs the selected reconciliation task contract.
func NewReconcileIntent(
	generator IdentifierGenerator,
	scope tenant.Scope,
	payload ReconcilePayload,
	metadata IntentMetadata,
) (platformtask.Intent, error) {
	if generator == nil || scope.ID().IsZero() || payload.CheckID.IsZero() || payload.AttemptID.IsZero() {
		return platformtask.Intent{}, fmt.Errorf("%w: verification reconcile intent", platformtask.ErrInvalid)
	}
	deadline, err := boundedDeadline(metadata.ScheduledAt, metadata.Deadline, MaximumReconcileDuration)
	if err != nil {
		return platformtask.Intent{}, err
	}
	return newIntent(generator, scope, ReconcileKey, taskheadgate.QueueMaintenance,
		"verification.reconcile:"+payload.AttemptID.String(), encodeReconcile(payload), metadata, deadline, ReconcileRetry)
}

// DecodeExecute strictly decodes an exact version-1 execution payload.
func DecodeExecute(encoded []byte) (ExecutePayload, error) {
	var wire struct {
		CheckID   string `json:"check_id"`
		AttemptID string `json:"attempt_id"`
	}
	if err := decodeStrict(encoded, &wire); err != nil {
		return ExecutePayload{}, err
	}
	checkID, err := id.ParseCheck(wire.CheckID)
	if err != nil {
		return ExecutePayload{}, fmt.Errorf("%w: execute check id", platformtask.ErrInvalid)
	}
	attemptID, err := id.ParseAttempt(wire.AttemptID)
	if err != nil {
		return ExecutePayload{}, fmt.Errorf("%w: execute attempt id", platformtask.ErrInvalid)
	}
	return ExecutePayload{CheckID: checkID, AttemptID: attemptID}, nil
}

// DecodeReconcile strictly decodes an exact version-1 reconciliation payload.
func DecodeReconcile(encoded []byte) (ReconcilePayload, error) {
	var wire struct {
		CheckID   string `json:"check_id"`
		AttemptID string `json:"attempt_id"`
	}
	if err := decodeStrict(encoded, &wire); err != nil {
		return ReconcilePayload{}, err
	}
	checkID, err := id.ParseCheck(wire.CheckID)
	if err != nil {
		return ReconcilePayload{}, fmt.Errorf("%w: reconcile check id", platformtask.ErrInvalid)
	}
	attemptID, err := id.ParseAttempt(wire.AttemptID)
	if err != nil {
		return ReconcilePayload{}, fmt.Errorf("%w: reconcile attempt id", platformtask.ErrInvalid)
	}
	return ReconcilePayload{CheckID: checkID, AttemptID: attemptID}, nil
}

func newIntent(
	generator IdentifierGenerator,
	scope tenant.Scope,
	key platformtask.Key,
	queue string,
	idempotencyKey string,
	payload any,
	metadata IntentMetadata,
	deadline time.Time,
	retry platformtask.RetryPolicy,
) (platformtask.Intent, error) {
	taskID, err := generator.NewTask()
	if err != nil {
		return platformtask.Intent{}, fmt.Errorf("generate verification task id: %w", err)
	}
	return platformtask.NewIntent(platformtask.IntentSpec{
		ID: taskID, TenantID: scope.ID(), Key: key, Queue: queue,
		PartitionKey: scope.ID().String(), IdempotencyKey: idempotencyKey, Payload: payload,
		ScheduledAt: metadata.ScheduledAt, Deadline: deadline, Retry: retry,
		Retention: SuccessfulMetadataRetention, CorrelationID: metadata.CorrelationID,
		CausationID: metadata.CausationID, Traceparent: metadata.Traceparent, Tracestate: metadata.Tracestate,
	})
}

func encodeExecute(payload ExecutePayload) any {
	return struct {
		CheckID   string `json:"check_id"`
		AttemptID string `json:"attempt_id"`
	}{CheckID: payload.CheckID.String(), AttemptID: payload.AttemptID.String()}
}

func encodeReconcile(payload ReconcilePayload) any {
	return struct {
		CheckID   string `json:"check_id"`
		AttemptID string `json:"attempt_id"`
	}{CheckID: payload.CheckID.String(), AttemptID: payload.AttemptID.String()}
}

func boundedDeadline(scheduledAt, requested time.Time, maximum time.Duration) (time.Time, error) {
	if scheduledAt.IsZero() || scheduledAt.Location() != time.UTC || requested.IsZero() ||
		requested.Location() != time.UTC || !requested.After(scheduledAt) {
		return time.Time{}, fmt.Errorf("%w: verification task deadline", platformtask.ErrInvalid)
	}
	deadlineCap := scheduledAt.Add(maximum)
	if requested.After(deadlineCap) {
		return deadlineCap, nil
	}
	return requested, nil
}

func decodeStrict(encoded []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("%w: verification task payload", platformtask.ErrInvalid)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: verification task trailing data", platformtask.ErrInvalid)
	}
	return nil
}

func mustKey(name string, version uint32) platformtask.Key {
	parsed, err := platformtask.NewName(name)
	if err != nil {
		panic(err)
	}
	return platformtask.Key{Name: parsed, Version: version}
}
