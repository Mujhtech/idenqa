package task

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
)

func TestNewAuthorIntentPinsBoundedReferenceOnlyMeaning(t *testing.T) {
	t.Parallel()

	fixture := newTaskFixture(t)
	intent, err := NewAuthorIntent(
		taskIDGenerator{id: mustTask(t, "tsk_01ARZ3NDEKTSV4RRFFQ69G5FB9")},
		fixture.scope,
		fixture.request,
		IntentMetadata{
			ScheduledAt:   fixture.request.DecidedAt,
			Deadline:      fixture.request.DecidedAt.Add(10 * time.Minute),
			CorrelationID: "req_01ARZ3NDEKTSV4RRFFQ69G5FBA",
			CausationID:   "evt_01ARZ3NDEKTSV4RRFFQ69G5FBB",
		},
	)
	if err != nil {
		t.Fatalf("NewAuthorIntent() error = %v", err)
	}
	if intent.Key() != AuthorKey || intent.Queue() != taskheadgate.QueueVerification ||
		intent.PartitionKey() != fixture.scope.ID().String() ||
		intent.IdempotencyKey() != "policy.author:"+fixture.request.DecisionID.String() ||
		intent.Retry() != AuthorRetry || intent.Retention() != SuccessfulMetadataRetention ||
		!intent.Deadline().Equal(fixture.request.DecidedAt.Add(MaximumAuthorDuration)) {
		t.Fatalf("intent = %+v", intent)
	}
	request, err := DecodeAuthorRequest(intent.Payload())
	if err != nil {
		t.Fatalf("DecodeAuthorRequest() error = %v", err)
	}
	if request != fixture.request {
		t.Fatalf("request = %+v, want %+v", request, fixture.request)
	}
	encoded := string(intent.Payload())
	for _, forbidden := range []string{"facts", "policy_document", "evidence", "provider", "reason_codes"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("payload contains forbidden field %q: %s", forbidden, encoded)
		}
	}
}

func TestDecodeAuthorRequestRejectsAmbiguousPayloads(t *testing.T) {
	t.Parallel()

	fixture := newTaskFixture(t)
	valid, err := json.Marshal(encodeRequest(fixture.request))
	if err != nil {
		t.Fatal(err)
	}
	local := fixture.request
	local.EvaluatedAt = local.EvaluatedAt.In(time.FixedZone("local", 60*60))
	localEncoded, _ := json.Marshal(encodeRequest(local))
	tests := []struct {
		name    string
		payload []byte
	}{
		{name: "empty", payload: nil},
		{name: "unknown", payload: bytes.Replace(valid, []byte("}"), []byte(`,"facts":{}}`), 1)},
		{name: "trailing", payload: append(append([]byte(nil), valid...), []byte(` {}`)...)},
		{name: "local time", payload: localEncoded},
		{name: "self supersession", payload: []byte(`{"decision_id":"` + fixture.request.DecisionID.String() +
			`","verification_id":"` + fixture.request.VerificationID.String() +
			`","supersedes_id":"` + fixture.request.DecisionID.String() +
			`","evaluated_at":"2026-08-31T12:00:00Z","decided_at":"2026-08-31T12:00:01Z"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := DecodeAuthorRequest(test.payload); !errors.Is(err, platformtask.ErrInvalid) {
				t.Fatalf("DecodeAuthorRequest() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestNewAuthorIntentRejectsInvalidSchedulingAndGeneration(t *testing.T) {
	t.Parallel()

	fixture := newTaskFixture(t)
	metadata := IntentMetadata{
		ScheduledAt: fixture.request.DecidedAt,
		Deadline:    fixture.request.DecidedAt,
	}
	if _, err := NewAuthorIntent(taskIDGenerator{id: mustTask(t, "tsk_01ARZ3NDEKTSV4RRFFQ69G5FB9")},
		fixture.scope, fixture.request, metadata); !errors.Is(err, platformtask.ErrInvalid) {
		t.Fatalf("NewAuthorIntent(deadline) error = %v", err)
	}
	metadata.Deadline = metadata.ScheduledAt.Add(time.Minute)
	boom := errors.New("entropy unavailable")
	if _, err := NewAuthorIntent(taskIDGenerator{err: boom}, fixture.scope, fixture.request, metadata); !errors.Is(err, boom) {
		t.Fatalf("NewAuthorIntent(generator) error = %v", err)
	}
}

type taskIDGenerator struct {
	id  id.Task
	err error
}

func (generator taskIDGenerator) NewTask() (id.Task, error) { return generator.id, generator.err }

func mustTask(t *testing.T, encoded string) id.Task {
	t.Helper()
	identifier, err := id.ParseTask(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return identifier
}
