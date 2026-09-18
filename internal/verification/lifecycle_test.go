package verification

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestAdvanceLifecycleGraph(t *testing.T) {
	t.Parallel()
	// This fixture is the architecture's transition table, including the absence
	// of any terminal outgoing edge and of a created-to-expired edge.
	allowed := map[SessionState][]SessionState{
		SessionStateCreated:          {SessionStateCollecting, SessionStateCancelled, SessionStateFailed},
		SessionStateCollecting:       {SessionStateAwaitingInput, SessionStateProcessing, SessionStateCancelled, SessionStateExpired, SessionStateFailed},
		SessionStateAwaitingInput:    {SessionStateCollecting, SessionStateManualReview, SessionStateCancelled, SessionStateExpired, SessionStateFailed},
		SessionStateProcessing:       {SessionStateAwaitingExternal, SessionStateAwaitingInput, SessionStateManualReview, SessionStateCompleted, SessionStateCancelled, SessionStateExpired, SessionStateFailed},
		SessionStateAwaitingExternal: {SessionStateProcessing, SessionStateCancelled, SessionStateExpired, SessionStateFailed},
		SessionStateManualReview:     {SessionStateAwaitingInput, SessionStateCompleted, SessionStateCancelled, SessionStateExpired, SessionStateFailed},
	}
	states := []SessionState{SessionStateCreated, SessionStateCollecting, SessionStateAwaitingInput,
		SessionStateProcessing, SessionStateAwaitingExternal, SessionStateManualReview,
		SessionStateCompleted, SessionStateCancelled, SessionStateExpired, SessionStateFailed, "", "verified"}
	for _, from := range states {
		for _, to := range states {
			t.Run(string(from)+"/"+string(to), func(t *testing.T) {
				current, command := lifecycleFixture(t)
				current.State, command.Target = from, to
				wantAllowed := false
				for _, permitted := range allowed[from] {
					wantAllowed = wantAllowed || permitted == to
				}
				if from == SessionStateCompleted {
					current.DecisionID = lifecycleDecision(t)
				}
				if to == SessionStateCompleted {
					command.DecisionID = lifecycleDecision(t)
				}
				if to == SessionStateExpired {
					command.OccurredAt = current.ExpiresAt
				}
				before := current
				next, err := AdvanceLifecycle(current, command)
				if !wantAllowed {
					if !errors.Is(err, ErrSessionConflict) || next != (Lifecycle{}) {
						t.Fatalf("invalid edge returned %+v, %v", next, err)
					}
					return
				}
				if err != nil || next.State != to || next.Version != before.Version+1 ||
					next.UpdatedAt != command.OccurredAt || next.ExpiresAt != before.ExpiresAt ||
					next.CreatedAt != before.CreatedAt || next.DecisionID != command.DecisionID || current != before {
					t.Fatalf("transition = %+v, %v; original = %+v", next, err, current)
				}
			})
		}
	}
}

func TestAdvanceLifecycleRejectsInvalidMeaning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		change func(*Lifecycle, *LifecycleCommand)
	}{
		{"stale version", func(_ *Lifecycle, c *LifecycleCommand) { c.ExpectedVersion++ }},
		{"version overflow", func(s *Lifecycle, c *LifecycleCommand) { s.Version = math.MaxInt64; c.ExpectedVersion = math.MaxInt64 }},
		{"clock regression", func(s *Lifecycle, c *LifecycleCommand) { c.OccurredAt = s.UpdatedAt.Add(-time.Second) }},
		{"deadline equality", func(s *Lifecycle, c *LifecycleCommand) { c.OccurredAt = s.ExpiresAt }},
		{"after deadline", func(s *Lifecycle, c *LifecycleCommand) { c.OccurredAt = s.ExpiresAt.Add(time.Second) }},
		{"premature expiry", func(_ *Lifecycle, c *LifecycleCommand) { c.Target = SessionStateExpired }},
		{"completion without decision", func(s *Lifecycle, c *LifecycleCommand) {
			s.State = SessionStateProcessing
			c.Target = SessionStateCompleted
		}},
		{"failure with identity decision", func(_ *Lifecycle, c *LifecycleCommand) {
			c.Target = SessionStateFailed
			c.DecisionID = lifecycleDecision(t)
		}},
		{"nonterminal with decision", func(s *Lifecycle, _ *LifecycleCommand) { s.DecisionID = lifecycleDecision(t) }},
		{"missing event", func(_ *Lifecycle, c *LifecycleCommand) { c.EventID = id.Event{} }},
		{"missing verification", func(_ *Lifecycle, c *LifecycleCommand) { c.VerificationID = id.Verification{} }},
		{"unbounded actor", func(_ *Lifecycle, c *LifecycleCommand) { c.ActorID = "person@example.test" }},
		{"nonprincipal actor", func(_ *Lifecycle, c *LifecycleCommand) { c.ActorID = c.VerificationID.String() }},
		{"invalid current lifetime", func(s *Lifecycle, _ *LifecycleCommand) { s.ExpiresAt = s.CreatedAt }},
		{"sub-microsecond time", func(_ *Lifecycle, c *LifecycleCommand) { c.OccurredAt = c.OccurredAt.Add(1900 * time.Nanosecond) }},
		{"missing time", func(_ *Lifecycle, c *LifecycleCommand) { c.OccurredAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, command := lifecycleFixture(t)
			test.change(&current, &command)
			if _, err := AdvanceLifecycle(current, command); !errors.Is(err, ErrSessionConflict) {
				t.Fatalf("AdvanceLifecycle() = %v, want conflict", err)
			}
		})
	}
}

func TestAdvanceLifecycleSyntheticDecisionPath(t *testing.T) {
	t.Parallel()
	current, command := lifecycleFixture(t)
	for _, target := range []SessionState{SessionStateProcessing, SessionStateAwaitingExternal,
		SessionStateProcessing, SessionStateManualReview, SessionStateAwaitingInput,
		SessionStateManualReview, SessionStateCompleted} {
		command.Target, command.ExpectedVersion = target, current.Version
		command.OccurredAt = command.OccurredAt.Add(time.Second)
		if target == SessionStateCompleted {
			command.DecisionID = lifecycleDecision(t)
		}
		var err error
		current, err = AdvanceLifecycle(current, command)
		if err != nil {
			t.Fatalf("advance to %s: %v", target, err)
		}
	}
	if current.State != SessionStateCompleted || current.DecisionID.IsZero() || current.Version != 8 {
		t.Fatalf("completed lifecycle = %+v", current)
	}
}

func lifecycleFixture(t *testing.T) (Lifecycle, LifecycleCommand) {
	t.Helper()
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	eventID, err := id.ParseEvent("evt_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	verificationID, err := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	return Lifecycle{State: SessionStateCollecting, Version: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)},
		LifecycleCommand{EventID: eventID, VerificationID: verificationID, ExpectedVersion: 1,
			Target: SessionStateProcessing, ActorID: "tsk_01ARZ3NDEKTSV4RRFFQ69G5FAV", OccurredAt: now.Add(time.Minute)}
}

func lifecycleDecision(t *testing.T) id.Decision {
	t.Helper()
	decision, err := id.ParseDecision("dec_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	return decision
}
