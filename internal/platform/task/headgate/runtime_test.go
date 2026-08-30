package headgate

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/task"
	libheadgate "github.com/mujhtech/headgate/go"
)

func TestDefaultWorkerConfigIsBounded(t *testing.T) {
	t.Parallel()
	configuration := DefaultWorkerConfig()
	if err := configuration.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	configuration.QueueWorkers[QueueEvidence] = 0
	if err := configuration.Validate(); !errors.Is(err, task.ErrInvalid) {
		t.Fatalf("Validate() error = %v, want ErrInvalid", err)
	}
	configuration = DefaultWorkerConfig()
	configuration.QueuePolicies[QueueEvidence] = QueuePolicy{}
	if err := configuration.Validate(); !errors.Is(err, task.ErrInvalid) {
		t.Fatalf("Validate() policy error = %v, want ErrInvalid", err)
	}
}

func TestIntentOfRoundTripsOwnedIntent(t *testing.T) {
	t.Parallel()
	want := testIntent(t)
	envelope, err := envelopeOf(want, "idenqa-dev")
	if err != nil {
		t.Fatal(err)
	}
	got, err := intentOf(envelope)
	if err != nil {
		t.Fatalf("intentOf() error = %v", err)
	}
	if got.ID() != want.ID() || got.TenantID() != want.TenantID() || got.Key() != want.Key() ||
		got.Queue() != want.Queue() || got.IdempotencyKey() != want.IdempotencyKey() ||
		string(got.Payload()) != string(want.Payload()) || got.Retry() != want.Retry() ||
		got.Retention() != want.Retention() {
		t.Fatalf("intentOf() = %+v, want %+v", got, want)
	}
}

func TestExecuteMapsOwnedHandlerResults(t *testing.T) {
	t.Parallel()
	intent := testIntent(t)
	envelope, err := envelopeOf(intent, "idenqa-dev")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name   string
		result task.Result
		check  func(error) bool
	}{
		{name: "complete", result: task.Complete(), check: func(err error) bool { return err == nil }},
		{name: "retry", result: task.Retry(task.RetryClassTransient, errors.New("temporary")), check: func(err error) bool {
			var retry *retryError
			return errors.As(err, &retry) && retry.class == task.RetryClassTransient
		}},
		{name: "cancel", result: task.Cancel(errors.New("cancelled")), check: func(err error) bool {
			return errors.Is(err, libheadgate.ErrSkipJob)
		}},
		{name: "quarantine", result: task.Quarantine(errors.New("poison")), check: func(err error) bool {
			var undecodable *libheadgate.UndecodableError
			return errors.As(err, &undecodable)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			registry := task.NewRegistry()
			if err := registry.Register(intent.Key(), task.HandlerFunc(func(context.Context, task.Delivery) task.Result {
				return test.result
			})); err != nil {
				t.Fatal(err)
			}
			err := execute(t.Context(), registry, libheadgate.Claim{
				Envelope: envelope, Fence: 7, Expires: time.Now().Add(time.Minute),
			})
			if !test.check(err) {
				t.Fatalf("execute() error = %v", err)
			}
		})
	}
}

func TestExecutePresentsOneBasedAttempt(t *testing.T) {
	t.Parallel()
	intent := testIntent(t)
	envelope, err := envelopeOf(intent, "idenqa-dev")
	if err != nil {
		t.Fatal(err)
	}
	envelope.Attempt = 2
	registry := task.NewRegistry()
	var got uint32
	if err := registry.Register(intent.Key(), task.HandlerFunc(
		func(_ context.Context, delivery task.Delivery) task.Result {
			got = delivery.Attempt
			return task.Complete()
		},
	)); err != nil {
		t.Fatal(err)
	}
	if err := execute(t.Context(), registry, libheadgate.Claim{Envelope: envelope}); err != nil {
		t.Fatal(err)
	}
	if got != 3 {
		t.Fatalf("delivery attempt = %d, want 3", got)
	}
}

func TestExecuteSnoozesMissingNameAndQuarantinesUnsupportedVersion(t *testing.T) {
	t.Parallel()
	intent := testIntent(t)
	envelope, err := envelopeOf(intent, "idenqa-dev")
	if err != nil {
		t.Fatal(err)
	}
	err = execute(t.Context(), task.NewRegistry(), libheadgate.Claim{Envelope: envelope})
	var snooze *libheadgate.SnoozeError
	if !errors.As(err, &snooze) {
		t.Fatalf("missing handler error = %v, want SnoozeError", err)
	}
	registry := task.NewRegistry()
	if err := registry.Register(task.Key{Name: intent.Key().Name, Version: 1}, task.HandlerFunc(
		func(context.Context, task.Delivery) task.Result { return task.Complete() },
	)); err != nil {
		t.Fatal(err)
	}
	err = execute(t.Context(), registry, libheadgate.Claim{Envelope: envelope})
	var undecodable *libheadgate.UndecodableError
	if !errors.As(err, &undecodable) {
		t.Fatalf("unsupported version error = %v, want UndecodableError", err)
	}
}

func TestOpenMigratorRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	if _, err := OpenMigrator(t.Context(), "", "headgate", time.Second, time.Second); !errors.Is(err, task.ErrInvalid) {
		t.Fatalf("OpenMigrator() error = %v, want ErrInvalid", err)
	}
}

func TestCheckPoolSchemaRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()
	if _, err := CheckPoolSchema(t.Context(), nil, "headgate"); !errors.Is(err, task.ErrInvalid) {
		t.Fatalf("CheckPoolSchema() error = %v, want ErrInvalid", err)
	}
}
