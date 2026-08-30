package realtime

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestConnectionLifecycleSignalsAndWaitsForActiveAdmissions(t *testing.T) {
	t.Parallel()

	lifecycle := NewConnectionLifecycle()
	admission, ok := lifecycle.admit()
	if !ok {
		t.Fatal("admit() rejected before drain")
	}
	result := make(chan error, 1)
	go func() {
		result <- lifecycle.Drain(t.Context())
	}()
	select {
	case <-admission.drain:
	case <-time.After(time.Second):
		t.Fatal("active admission did not receive drain signal")
	}
	select {
	case err := <-result:
		t.Fatalf("Drain() returned before admission completion: %v", err)
	default:
	}
	if _, accepted := lifecycle.admit(); accepted {
		t.Fatal("admit() accepted during drain")
	}
	admission.done()
	if err := <-result; err != nil {
		t.Fatalf("Drain() error = %v", err)
	}
}

func TestConnectionLifecycleDrainHonoursContext(t *testing.T) {
	t.Parallel()

	lifecycle := NewConnectionLifecycle()
	admission, ok := lifecycle.admit()
	if !ok {
		t.Fatal("admit() rejected before drain")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := lifecycle.Drain(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Drain() error = %v, want context cancellation", err)
	}
	admission.done()
}
