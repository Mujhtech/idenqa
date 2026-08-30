package task_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/platform/task/tasktest"
)

type manualClock struct {
	mutex sync.Mutex
	now   time.Time
}

func (clock *manualClock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.now
}

func (clock *manualClock) Advance(duration time.Duration) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.now = clock.now.Add(duration)
}

func TestDriverConformance(t *testing.T) {
	t.Parallel()
	factory := func(t *testing.T) tasktest.Fixture {
		t.Helper()
		clock := &manualClock{now: time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)}
		driver, err := task.NewDriver(task.DriverOptions{Clock: clock, CrashLimit: 3})
		if err != nil {
			t.Fatalf("NewDriver() error = %v", err)
		}
		return tasktest.Fixture{Harness: driver, Intent: newIntent(t, clock.Now()), Advance: clock.Advance}
	}
	tasktest.Run(t, factory)
	tasktest.RunDrain(t, factory)
}

func TestTransactionRollbackAndAtomicConflict(t *testing.T) {
	t.Parallel()
	clock := &manualClock{now: time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)}
	driver, err := task.NewDriver(task.DriverOptions{Clock: clock, CrashLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	intent := newIntent(t, clock.Now())
	transaction := driver.Begin()
	if err := transaction.Enqueue(intent); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Snapshot(intent.ID()); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("Snapshot() error = %v, want ErrNotFound", err)
	}

	transaction = driver.Begin()
	if err := transaction.Enqueue(intent); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := driver.Snapshot(intent.ID()); err != nil {
		t.Fatalf("Snapshot() error = %v", err)
	}
}

func TestEnqueueRejectsChangedIDAndDuplicateSemanticKey(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	clock := &manualClock{now: now}
	driver, err := task.NewDriver(task.DriverOptions{Clock: clock, CrashLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	intent := newIntent(t, now)
	if err := driver.Enqueue(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	changed := rebuildIntent(t, intent, intent.ID(), "verification-one", struct {
		VerificationID string `json:"verification_id"`
	}{VerificationID: "ver_changed"})
	if err := driver.Enqueue(t.Context(), changed); !errors.Is(err, task.ErrConflict) {
		t.Fatalf("changed-id Enqueue() error = %v, want ErrConflict", err)
	}

	generator, err := id.NewGenerator(fixedClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{12}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := generator.NewTask()
	if err != nil {
		t.Fatal(err)
	}
	duplicate := rebuildIntent(t, intent, otherID, intent.IdempotencyKey(), struct {
		VerificationID string `json:"verification_id"`
	}{VerificationID: "ver_other"})
	if err := driver.Enqueue(t.Context(), duplicate); !errors.Is(err, task.ErrDuplicate) {
		t.Fatalf("duplicate-key Enqueue() error = %v, want ErrDuplicate", err)
	}
	if snapshots := driver.Snapshots(); len(snapshots) != 1 {
		t.Fatalf("Snapshots() count = %d, want atomic count 1", len(snapshots))
	}
}

func TestCrashLimitQuarantinesPoisonWork(t *testing.T) {
	t.Parallel()
	clock := &manualClock{now: time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)}
	driver, err := task.NewDriver(task.DriverOptions{Clock: clock, CrashLimit: 2})
	if err != nil {
		t.Fatal(err)
	}
	intent := newIntent(t, clock.Now())
	if err := driver.Enqueue(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		delivery, claimErr := driver.Claim(t.Context(), time.Minute)
		if claimErr != nil {
			t.Fatalf("Claim() error = %v", claimErr)
		}
		if crashErr := driver.Crash(t.Context(), delivery); crashErr != nil {
			t.Fatalf("Crash() error = %v", crashErr)
		}
		clock.Advance(time.Minute)
	}
	if _, err := driver.Claim(t.Context(), time.Minute); !errors.Is(err, task.ErrNotFound) {
		t.Fatalf("Claim() error = %v, want ErrNotFound", err)
	}
	snapshot, err := driver.Snapshot(intent.ID())
	if err != nil || snapshot.State != task.StateQuarantined || snapshot.CrashAttempt != 2 {
		t.Fatalf("Snapshot() = %+v, %v", snapshot, err)
	}
}

func TestDrainReclaimsExpiredCrashedAttempt(t *testing.T) {
	t.Parallel()
	clock := &manualClock{now: time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)}
	driver, err := task.NewDriver(task.DriverOptions{Clock: clock, CrashLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	intent := newIntent(t, clock.Now())
	if err := driver.Enqueue(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	delivery, err := driver.Claim(t.Context(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := driver.Crash(t.Context(), delivery); err != nil {
		t.Fatal(err)
	}
	driver.BeginDrain()
	if driver.Drained() {
		t.Fatal("Drained() = true before crashed lease expiry")
	}
	clock.Advance(time.Minute)
	if !driver.Drained() {
		t.Fatal("Drained() = false after crashed lease reclaim")
	}
	snapshot, err := driver.Snapshot(intent.ID())
	if err != nil || snapshot.State != task.StateScheduled || snapshot.CrashAttempt != 1 {
		t.Fatalf("Snapshot() = %+v, %v", snapshot, err)
	}
}

func TestRunOneQuarantinesUnsupportedPayloadVersion(t *testing.T) {
	t.Parallel()
	clock := &manualClock{now: time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)}
	driver, err := task.NewDriver(task.DriverOptions{Clock: clock, CrashLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	intent := newIntent(t, clock.Now())
	if err := driver.Enqueue(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	if err := driver.RunOne(t.Context(), task.NewRegistry(), time.Minute); err != nil {
		t.Fatalf("RunOne() error = %v", err)
	}
	snapshot, err := driver.Snapshot(intent.ID())
	if err != nil || snapshot.State != task.StateQuarantined {
		t.Fatalf("Snapshot() = %+v, %v", snapshot, err)
	}
}

func TestRunOneDispatchesExactVersion(t *testing.T) {
	t.Parallel()
	clock := &manualClock{now: time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)}
	driver, err := task.NewDriver(task.DriverOptions{Clock: clock, CrashLimit: 3})
	if err != nil {
		t.Fatal(err)
	}
	intent := newIntent(t, clock.Now())
	registry := task.NewRegistry()
	called := false
	if err := registry.Register(intent.Key(), task.HandlerFunc(func(_ context.Context, delivery task.Delivery) task.Result {
		called = delivery.Intent.Key() == intent.Key()
		return task.Complete()
	})); err != nil {
		t.Fatal(err)
	}
	if err := driver.Enqueue(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	if err := driver.RunOne(t.Context(), registry, time.Minute); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("exact version handler was not called")
	}
}

func TestIntentPayloadIsDefensivelyCopied(t *testing.T) {
	t.Parallel()
	intent := newIntent(t, time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC))
	payload := intent.Payload()
	payload[2] = 'x'
	if !json.Valid(intent.Payload()) {
		t.Fatal("mutating returned payload changed the intent")
	}
}

func TestMemoryInboxDeduplicatesEffectAndRejectsStaleFence(t *testing.T) {
	t.Parallel()
	intent := newIntent(t, time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC))
	inbox := task.NewMemoryInbox()
	claimed, err := inbox.Claim(t.Context(), task.Effect{TaskID: intent.ID(), Key: "verification.decision", Fence: 2})
	if err != nil || !claimed {
		t.Fatalf("first Claim() = %v, %v", claimed, err)
	}
	claimed, err = inbox.Claim(t.Context(), task.Effect{TaskID: intent.ID(), Key: "verification.decision", Fence: 2})
	if err != nil || claimed {
		t.Fatalf("replay Claim() = %v, %v", claimed, err)
	}
	if _, err := inbox.Claim(
		t.Context(), task.Effect{TaskID: intent.ID(), Key: "verification.decision", Fence: 1},
	); !errors.Is(err, task.ErrLeaseLost) {
		t.Fatalf("stale Claim() error = %v, want ErrLeaseLost", err)
	}
}

func newIntent(t *testing.T, now time.Time) task.Intent {
	t.Helper()
	generator, err := id.NewGenerator(fixedClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{7}, 128)))
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
	intent, err := task.NewIntent(task.IntentSpec{
		ID: taskID, TenantID: tenantID, Key: task.Key{Name: name, Version: 1},
		Queue: "verification", PartitionKey: tenantID.String(), IdempotencyKey: "verification-one",
		Payload: struct {
			VerificationID string `json:"verification_id"`
		}{VerificationID: "ver_01K00000000000000000000000"},
		ScheduledAt: now, Retry: task.RetryPolicy{
			MaxAttempts: 3, InitialBackoff: time.Second, MaximumBackoff: time.Minute, JitterPercent: 10,
		}, Retention: 24 * time.Hour, CorrelationID: "req_01K00000000000000000000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func rebuildIntent(t *testing.T, original task.Intent, taskID id.Task, idempotencyKey string, payload any) task.Intent {
	t.Helper()
	intent, err := task.NewIntent(task.IntentSpec{
		ID: taskID, TenantID: original.TenantID(), Key: original.Key(), Queue: original.Queue(),
		PartitionKey: original.PartitionKey(), IdempotencyKey: idempotencyKey, Payload: payload,
		ScheduledAt: original.ScheduledAt(), Deadline: original.Deadline(), Retry: original.Retry(),
		Retention: original.Retention(), CorrelationID: original.CorrelationID(),
		CausationID: original.CausationID(), Traceparent: original.Traceparent(), Tracestate: original.Tracestate(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }
