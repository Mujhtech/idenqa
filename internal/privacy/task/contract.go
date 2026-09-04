// Package task owns privacy lifecycle background-work contracts. Payloads
// contain opaque deletion identifiers only; exact targets remain in PostgreSQL.
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
	// DeleteName is the stable durable lifecycle task name.
	DeleteName = "privacy.delete"
	// DeleteVersion is the exact first deletion payload version.
	DeleteVersion uint32 = 1
	// SuccessfulMetadataRetention bounds successful queue metadata.
	SuccessfulMetadataRetention = 30 * 24 * time.Hour
	// MaximumDeleteDuration bounds one attempt without limiting the workflow.
	MaximumDeleteDuration = 15 * time.Minute
	// CoordinationBatch bounds installation-wide discovery.
	CoordinationBatch = 100
)

var (
	// DeleteKey identifies the version-1 deletion task.
	DeleteKey = mustKey(DeleteName, DeleteVersion)
	// DeleteRetry is the selected bounded transient retry policy.
	DeleteRetry = platformtask.RetryPolicy{MaxAttempts: 8, InitialBackoff: time.Second,
		MaximumBackoff: time.Hour, JitterPercent: 20}
)

// IdentifierGenerator supplies opaque durable task identifiers.
type IdentifierGenerator interface{ NewTask() (id.Task, error) }

// DeletePayload addresses one workflow without exposing its target set.
type DeletePayload struct{ DeletionID id.Deletion }

// IntentMetadata contains safe scheduling and correlation metadata.
type IntentMetadata struct {
	ScheduledAt     time.Time
	WorkflowVersion int64
	CorrelationID   string
	CausationID     string
}

// NewDeleteIntent constructs one exact replay-safe deletion task.
func NewDeleteIntent(generator IdentifierGenerator, scope tenant.Scope, payload DeletePayload, metadata IntentMetadata) (platformtask.Intent, error) {
	if generator == nil || scope.ID().IsZero() || payload.DeletionID.IsZero() || metadata.ScheduledAt.IsZero() || metadata.ScheduledAt.Location() != time.UTC || metadata.WorkflowVersion < 1 {
		return platformtask.Intent{}, fmt.Errorf("%w: privacy deletion intent", platformtask.ErrInvalid)
	}
	taskID, err := generator.NewTask()
	if err != nil {
		return platformtask.Intent{}, fmt.Errorf("generate privacy task id: %w", err)
	}
	return platformtask.NewIntent(platformtask.IntentSpec{
		ID: taskID, TenantID: scope.ID(), Key: DeleteKey, Queue: taskheadgate.QueueEvidence,
		PartitionKey: scope.ID().String(), IdempotencyKey: fmt.Sprintf("privacy.delete:%s:%d", payload.DeletionID.String(), metadata.WorkflowVersion),
		Payload: struct {
			DeletionID string `json:"deletion_id"`
		}{DeletionID: payload.DeletionID.String()}, ScheduledAt: metadata.ScheduledAt,
		Deadline: metadata.ScheduledAt.Add(MaximumDeleteDuration), Retry: DeleteRetry,
		Retention: SuccessfulMetadataRetention, CorrelationID: metadata.CorrelationID,
		CausationID: metadata.CausationID,
	})
}

// DecodeDelete strictly restores the exact version-1 payload.
func DecodeDelete(encoded []byte) (DeletePayload, error) {
	var wire struct {
		DeletionID string `json:"deletion_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return DeletePayload{}, fmt.Errorf("%w: privacy deletion payload", platformtask.ErrInvalid)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return DeletePayload{}, fmt.Errorf("%w: privacy deletion trailing data", platformtask.ErrInvalid)
	}
	identifier, err := id.ParseDeletion(wire.DeletionID)
	if err != nil {
		return DeletePayload{}, fmt.Errorf("%w: privacy deletion identifier", platformtask.ErrInvalid)
	}
	return DeletePayload{DeletionID: identifier}, nil
}

func mustKey(name string, version uint32) platformtask.Key {
	parsed, err := platformtask.NewName(name)
	if err != nil {
		panic(err)
	}
	return platformtask.Key{Name: parsed, Version: version}
}
