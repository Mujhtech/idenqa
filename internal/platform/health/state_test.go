package health_test

import (
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/health"
)

func TestStateLifecycle(t *testing.T) {
	t.Parallel()

	var state health.State
	if state.Started() {
		t.Error("zero state is started")
	}
	if state.Ready() {
		t.Error("zero state is ready")
	}

	state.MarkStarted()
	if !state.Started() {
		t.Error("state did not become started")
	}
	if state.Ready() {
		t.Error("started state became ready without MarkReady")
	}

	state.MarkReady()
	if !state.Ready() {
		t.Error("state did not become ready")
	}

	state.BeginDrain()
	if state.Ready() {
		t.Error("draining state remained ready")
	}
	if !state.Started() {
		t.Error("draining state lost startup status")
	}
}
