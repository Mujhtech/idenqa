// Package task owns the durable background-work adapter for policy decisions.
// Payloads contain only opaque identifiers and exact workflow timestamps;
// authoritative facts and policy documents remain in PostgreSQL.
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
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// AuthorName is the stable durable machine-decision task name.
	AuthorName = "policy.author"
	// AuthorVersion is the exact first authoring payload version.
	AuthorVersion uint32 = 1
	// SuccessfulMetadataRetention bounds successful queue metadata retention.
	SuccessfulMetadataRetention = 30 * 24 * time.Hour
	// MaximumAuthorDuration caps one authoring deadline from scheduling.
	MaximumAuthorDuration = 2 * time.Minute
)

var (
	// AuthorKey identifies the exact first authoring task contract.
	AuthorKey = mustKey(AuthorName, AuthorVersion)
	// AuthorRetry is the bounded authoring retry policy.
	AuthorRetry = platformtask.RetryPolicy{
		MaxAttempts: 5, InitialBackoff: time.Second,
		MaximumBackoff: 30 * time.Second, JitterPercent: 20,
	}
)

// IdentifierGenerator supplies opaque durable task identifiers.
type IdentifierGenerator interface{ NewTask() (id.Task, error) }

// IntentMetadata contains safe scheduling and tracing metadata.
type IntentMetadata struct {
	ScheduledAt   time.Time
	Deadline      time.Time
	CorrelationID string
	CausationID   string
	Traceparent   string
	Tracestate    string
}

// NewAuthorIntent constructs one exact replayable machine-decision task.
func NewAuthorIntent(
	generator IdentifierGenerator,
	scope tenant.Scope,
	request policy.AuthorRequest,
	metadata IntentMetadata,
) (platformtask.Intent, error) {
	if generator == nil || scope.ID().IsZero() || validateRequest(request) != nil {
		return platformtask.Intent{}, fmt.Errorf("%w: policy author intent", platformtask.ErrInvalid)
	}
	deadline, err := boundedDeadline(metadata.ScheduledAt, metadata.Deadline)
	if err != nil {
		return platformtask.Intent{}, err
	}
	taskID, err := generator.NewTask()
	if err != nil {
		return platformtask.Intent{}, fmt.Errorf("generate policy task id: %w", err)
	}
	return platformtask.NewIntent(platformtask.IntentSpec{
		ID: taskID, TenantID: scope.ID(), Key: AuthorKey,
		Queue: taskheadgate.QueueVerification, PartitionKey: scope.ID().String(),
		IdempotencyKey: "policy.author:" + request.DecisionID.String(),
		Payload:        encodeRequest(request), ScheduledAt: metadata.ScheduledAt,
		Deadline: deadline, Retry: AuthorRetry, Retention: SuccessfulMetadataRetention,
		CorrelationID: metadata.CorrelationID, CausationID: metadata.CausationID,
		Traceparent: metadata.Traceparent, Tracestate: metadata.Tracestate,
	})
}

// DecodeAuthorRequest strictly decodes the exact version-1 task payload.
func DecodeAuthorRequest(encoded []byte) (policy.AuthorRequest, error) {
	var wire struct {
		DecisionID     string `json:"decision_id"`
		VerificationID string `json:"verification_id"`
		SupersedesID   string `json:"supersedes_id,omitempty"`
		EvaluatedAt    string `json:"evaluated_at"`
		DecidedAt      string `json:"decided_at"`
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&wire); err != nil {
		return policy.AuthorRequest{}, fmt.Errorf("%w: policy author payload", platformtask.ErrInvalid)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return policy.AuthorRequest{}, fmt.Errorf("%w: policy author trailing data", platformtask.ErrInvalid)
	}
	decisionID, err := id.ParseDecision(wire.DecisionID)
	if err != nil {
		return policy.AuthorRequest{}, fmt.Errorf("%w: policy author decision id", platformtask.ErrInvalid)
	}
	verificationID, err := id.ParseVerification(wire.VerificationID)
	if err != nil {
		return policy.AuthorRequest{}, fmt.Errorf("%w: policy author verification id", platformtask.ErrInvalid)
	}
	var supersedes id.Decision
	if wire.SupersedesID != "" {
		supersedes, err = id.ParseDecision(wire.SupersedesID)
		if err != nil {
			return policy.AuthorRequest{}, fmt.Errorf("%w: policy author predecessor id", platformtask.ErrInvalid)
		}
	}
	evaluatedAt, err := time.Parse(time.RFC3339Nano, wire.EvaluatedAt)
	if err != nil {
		return policy.AuthorRequest{}, fmt.Errorf("%w: policy author evaluation time", platformtask.ErrInvalid)
	}
	decidedAt, err := time.Parse(time.RFC3339Nano, wire.DecidedAt)
	if err != nil {
		return policy.AuthorRequest{}, fmt.Errorf("%w: policy author decision time", platformtask.ErrInvalid)
	}
	request := policy.AuthorRequest{
		DecisionID: decisionID, VerificationID: verificationID, Supersedes: supersedes,
		EvaluatedAt: evaluatedAt, DecidedAt: decidedAt,
	}
	if err := validateRequest(request); err != nil {
		return policy.AuthorRequest{}, err
	}
	return request, nil
}

func encodeRequest(request policy.AuthorRequest) any {
	supersedes := ""
	if !request.Supersedes.IsZero() {
		supersedes = request.Supersedes.String()
	}
	return struct {
		DecisionID     string `json:"decision_id"`
		VerificationID string `json:"verification_id"`
		SupersedesID   string `json:"supersedes_id,omitempty"`
		EvaluatedAt    string `json:"evaluated_at"`
		DecidedAt      string `json:"decided_at"`
	}{
		DecisionID: request.DecisionID.String(), VerificationID: request.VerificationID.String(),
		SupersedesID: supersedes, EvaluatedAt: request.EvaluatedAt.Format(time.RFC3339Nano),
		DecidedAt: request.DecidedAt.Format(time.RFC3339Nano),
	}
}

func validateRequest(request policy.AuthorRequest) error {
	if request.DecisionID.IsZero() || request.VerificationID.IsZero() ||
		request.EvaluatedAt.IsZero() || request.EvaluatedAt.Location() != time.UTC ||
		request.DecidedAt.IsZero() || request.DecidedAt.Location() != time.UTC ||
		request.DecidedAt.Before(request.EvaluatedAt) ||
		(!request.Supersedes.IsZero() && request.Supersedes.String() == request.DecisionID.String()) {
		return fmt.Errorf("%w: policy author request", platformtask.ErrInvalid)
	}
	return nil
}

func boundedDeadline(scheduledAt, requested time.Time) (time.Time, error) {
	if scheduledAt.IsZero() || scheduledAt.Location() != time.UTC || requested.IsZero() ||
		requested.Location() != time.UTC || !requested.After(scheduledAt) {
		return time.Time{}, fmt.Errorf("%w: policy author deadline", platformtask.ErrInvalid)
	}
	if maximum := scheduledAt.Add(MaximumAuthorDuration); requested.After(maximum) {
		return maximum, nil
	}
	return requested, nil
}

func mustKey(name string, version uint32) platformtask.Key {
	parsed, err := platformtask.NewName(name)
	if err != nil {
		panic(err)
	}
	return platformtask.Key{Name: parsed, Version: version}
}
