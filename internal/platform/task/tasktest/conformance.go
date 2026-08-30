// Package tasktest provides a reusable behavioral contract for task drivers.
package tasktest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/task"
)

// Harness exposes only portable owned task behavior. Production adapters can
// implement it with isolated integration storage.
type Harness interface {
	Enqueue(context.Context, ...task.Intent) error
	Claim(context.Context, time.Duration) (task.Delivery, error)
	Heartbeat(context.Context, id.Task, uint64, time.Duration) error
	Resolve(context.Context, task.Delivery, task.Result) error
	Cancel(context.Context, id.Task) error
	Snapshot(id.Task) (task.Snapshot, error)
	Snapshots() []task.Snapshot
}

// DrainHarness is the single-process lifecycle extension. A split API/worker
// deployment intentionally continues accepting durable work while one worker drains.
type DrainHarness interface {
	Harness
	BeginDrain()
	Drained() bool
}

// Fixture supplies fresh isolated state and deterministic time for each case.
type Fixture struct {
	Harness Harness
	Intent  task.Intent
	Advance func(time.Duration)
	Lease   time.Duration
}

// Factory constructs a fresh fixture for one conformance case.
type Factory func(*testing.T) Fixture

// Run executes the portable task state-machine contract.
func Run(t *testing.T, factory Factory) {
	t.Helper()

	t.Run("equivalent replay is idempotent", func(t *testing.T) {
		fixture := factory(t)
		if err := fixture.Harness.Enqueue(t.Context(), fixture.Intent); err != nil {
			t.Fatalf("first Enqueue() error = %v", err)
		}
		if err := fixture.Harness.Enqueue(t.Context(), fixture.Intent); err != nil {
			t.Fatalf("replay Enqueue() error = %v", err)
		}
		if snapshots := len(fixture.Harness.Snapshots()); snapshots != 1 {
			t.Fatalf("durable task count = %d, want 1", snapshots)
		}
	})

	t.Run("stale worker is fenced after lease loss", func(t *testing.T) {
		fixture := factory(t)
		mustEnqueue(t, fixture)
		lease := leaseDuration(fixture)
		first, err := fixture.Harness.Claim(t.Context(), lease)
		if err != nil {
			t.Fatalf("first Claim() error = %v", err)
		}
		fixture.Advance(lease)
		second, err := fixture.Harness.Claim(t.Context(), lease)
		if err != nil {
			t.Fatalf("second Claim() error = %v", err)
		}
		if second.Fence <= first.Fence || second.CrashAttempt != 1 {
			t.Fatalf("reclaimed delivery = %+v, first = %+v", second, first)
		}
		if err := fixture.Harness.Resolve(t.Context(), first, task.Complete()); !errors.Is(err, task.ErrLeaseLost) {
			t.Fatalf("stale Resolve() error = %v, want ErrLeaseLost", err)
		}
	})

	t.Run("heartbeat extends current lease", func(t *testing.T) {
		fixture := factory(t)
		mustEnqueue(t, fixture)
		lease := leaseDuration(fixture)
		delivery, err := fixture.Harness.Claim(t.Context(), lease)
		if err != nil {
			t.Fatalf("Claim() error = %v", err)
		}
		fixture.Advance(lease / 2)
		if err := fixture.Harness.Heartbeat(
			t.Context(), delivery.Intent.ID(), delivery.Fence, lease,
		); err != nil {
			t.Fatalf("Heartbeat() error = %v", err)
		}
		fixture.Advance(lease/2 + lease/20)
		if _, err := fixture.Harness.Claim(t.Context(), lease); !errors.Is(err, task.ErrNotFound) {
			t.Fatalf("Claim() during renewed lease error = %v, want ErrNotFound", err)
		}
	})

	t.Run("retry budget ends in quarantine", func(t *testing.T) {
		fixture := factory(t)
		mustEnqueue(t, fixture)
		for attempt := uint32(1); attempt <= fixture.Intent.Retry().MaxAttempts; attempt++ {
			delivery, err := fixture.Harness.Claim(t.Context(), leaseDuration(fixture))
			if err != nil {
				t.Fatalf("Claim(attempt %d) error = %v", attempt, err)
			}
			if err := fixture.Harness.Resolve(
				t.Context(), delivery,
				task.Retry(task.RetryClassTransient, errors.New("temporary failure")),
			); err != nil {
				t.Fatalf("Resolve(attempt %d) error = %v", attempt, err)
			}
			if attempt < fixture.Intent.Retry().MaxAttempts {
				fixture.Advance(fixture.Intent.Retry().Backoff(attempt, fixture.Intent.ID().String()))
			}
		}
		snapshot, err := fixture.Harness.Snapshot(fixture.Intent.ID())
		if err != nil {
			t.Fatalf("Snapshot() error = %v", err)
		}
		if snapshot.State != task.StateQuarantined {
			t.Fatalf("state = %v, want quarantined", snapshot.State)
		}
	})

	t.Run("cancellation is terminal", func(t *testing.T) {
		fixture := factory(t)
		mustEnqueue(t, fixture)
		if err := fixture.Harness.Cancel(t.Context(), fixture.Intent.ID()); err != nil {
			t.Fatalf("Cancel() error = %v", err)
		}
		if _, err := fixture.Harness.Claim(t.Context(), leaseDuration(fixture)); !errors.Is(err, task.ErrNotFound) {
			t.Fatalf("Claim() error = %v, want ErrNotFound", err)
		}
		snapshot, err := fixture.Harness.Snapshot(fixture.Intent.ID())
		if err != nil || snapshot.State != task.StateCancelled {
			t.Fatalf("Snapshot() = %+v, %v", snapshot, err)
		}
	})

}

// RunDrain executes the lifecycle extension used by an in-process driver.
func RunDrain(t *testing.T, factory Factory) {
	t.Helper()
	t.Run("drain rejects new work and finishes active work", func(t *testing.T) {
		fixture := factory(t)
		harness, ok := fixture.Harness.(DrainHarness)
		if !ok {
			t.Fatal("fixture does not implement DrainHarness")
		}
		mustEnqueue(t, fixture)
		delivery, err := harness.Claim(t.Context(), leaseDuration(fixture))
		if err != nil {
			t.Fatalf("Claim() error = %v", err)
		}
		harness.BeginDrain()
		if harness.Drained() {
			t.Fatal("Drained() = true with an active attempt")
		}
		if err := harness.Enqueue(t.Context(), fixture.Intent); !errors.Is(err, task.ErrDraining) {
			t.Fatalf("Enqueue() error = %v, want ErrDraining", err)
		}
		if err := harness.Resolve(t.Context(), delivery, task.Complete()); err != nil {
			t.Fatalf("Resolve() error = %v", err)
		}
		if !harness.Drained() {
			t.Fatal("Drained() = false after active completion")
		}
	})
}

func leaseDuration(fixture Fixture) time.Duration {
	if fixture.Lease > 0 {
		return fixture.Lease
	}
	return time.Minute
}

func mustEnqueue(t *testing.T, fixture Fixture) {
	t.Helper()
	if err := fixture.Harness.Enqueue(t.Context(), fixture.Intent); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
}
