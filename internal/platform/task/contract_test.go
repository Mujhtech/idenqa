package task_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/task"
)

func TestNewNameAndKeyRejectUnstableWireNames(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		value string
	}{
		{name: "empty"},
		{name: "uppercase", value: "Verification.Evaluate"},
		{name: "leading digit", value: "1verification"},
		{name: "whitespace", value: "verification evaluate"},
		{name: "surrounding whitespace", value: " verification.evaluate"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := task.NewName(test.value); !errors.Is(err, task.ErrInvalid) {
				t.Fatalf("NewName(%q) error = %v, want ErrInvalid", test.value, err)
			}
		})
	}
}

func TestNewIntentRejectsScalarPayloadAndUnboundedPolicies(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	generator, err := id.NewGenerator(fixedClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{11}, 96)))
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := generator.NewTask()
	if err != nil {
		t.Fatal(err)
	}
	tenantID, err := generator.NewTenant()
	if err != nil {
		t.Fatal(err)
	}
	name, err := task.NewName("verification.evaluate")
	if err != nil {
		t.Fatal(err)
	}
	base := task.IntentSpec{
		ID: taskID, TenantID: tenantID, Key: task.Key{Name: name, Version: 1},
		Queue: "verification", IdempotencyKey: "one", Payload: struct{}{}, ScheduledAt: now,
		Retry:     task.RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Second, MaximumBackoff: time.Minute},
		Retention: time.Hour,
	}
	tests := []struct {
		name   string
		mutate func(*task.IntentSpec)
	}{
		{name: "scalar payload", mutate: func(spec *task.IntentSpec) { spec.Payload = "raw evidence" }},
		{name: "zero task version", mutate: func(spec *task.IntentSpec) { spec.Key.Version = 0 }},
		{name: "unbounded attempts", mutate: func(spec *task.IntentSpec) { spec.Retry.MaxAttempts = 101 }},
		{name: "sub-millisecond retention", mutate: func(spec *task.IntentSpec) { spec.Retention = time.Nanosecond }},
		{name: "deadline before schedule", mutate: func(spec *task.IntentSpec) { spec.Deadline = now.Add(-time.Second) }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			spec := base
			test.mutate(&spec)
			if _, err := task.NewIntent(spec); !errors.Is(err, task.ErrInvalid) {
				t.Fatalf("NewIntent() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestResultRequiresExplicitClassification(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		result task.Result
		valid  bool
	}{
		{name: "complete", result: task.Complete(), valid: true},
		{name: "retry", result: task.Retry(task.RetryClassTransient, errors.New("temporary")), valid: true},
		{name: "retry without class", result: task.Retry(task.RetryClassUnknown, errors.New("temporary"))},
		{name: "retry without error", result: task.Retry(task.RetryClassTransient, nil)},
		{name: "cancel", result: task.Cancel(errors.New("operator request")), valid: true},
		{name: "quarantine", result: task.Quarantine(errors.New("poison work")), valid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.result.Validate()
			if test.valid && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if !test.valid && !errors.Is(err, task.ErrInvalid) {
				t.Fatalf("Validate() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestRegistrySupportsExactRollingVersions(t *testing.T) {
	t.Parallel()
	name, err := task.NewName("verification.evaluate")
	if err != nil {
		t.Fatal(err)
	}
	registry := task.NewRegistry()
	handler := task.HandlerFunc(func(context.Context, task.Delivery) task.Result { return task.Complete() })
	for _, version := range []uint32{2, 1} {
		if err := registry.Register(task.Key{Name: name, Version: version}, handler); err != nil {
			t.Fatalf("Register(v%d) error = %v", version, err)
		}
	}
	keys := registry.Keys()
	if len(keys) != 2 || keys[0].Version != 1 || keys[1].Version != 2 {
		t.Fatalf("Keys() = %+v", keys)
	}
	if _, err := registry.Resolve(task.Key{Name: name, Version: 3}); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("Resolve(v3) error = %v, want ErrNotFound", err)
	}
	if err := registry.Register(task.Key{Name: name, Version: 1}, handler); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("duplicate Register() error = %v, want ErrConflict", err)
	}
}

func TestRetryBackoffIsBoundedAndDeterministic(t *testing.T) {
	t.Parallel()
	policy := task.RetryPolicy{
		MaxAttempts: 5, InitialBackoff: time.Second, MaximumBackoff: 10 * time.Second, JitterPercent: 20,
	}
	first := policy.Backoff(3, "tsk_one")
	if first != policy.Backoff(3, "tsk_one") {
		t.Fatal("Backoff() is not deterministic")
	}
	if first <= 0 || first > policy.MaximumBackoff {
		t.Fatalf("Backoff() = %v, want (0, %v]", first, policy.MaximumBackoff)
	}
	if got := policy.Backoff(100, "tsk_one"); got <= 0 || got > policy.MaximumBackoff {
		t.Fatalf("bounded Backoff() = %v", got)
	}
}
