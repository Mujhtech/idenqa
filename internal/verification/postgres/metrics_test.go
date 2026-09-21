package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type recordingVerificationMetrics struct {
	transitions []observability.Transition
	completions []observability.WorkflowCompletion
	failures    []observability.OperationalFailure
	starts      []observability.SessionStart
	recaptures  []observability.Recapture
}

func (metrics *recordingVerificationMetrics) RecordVerificationTransition(transition observability.Transition) {
	metrics.transitions = append(metrics.transitions, transition)
}

func (metrics *recordingVerificationMetrics) RecordVerificationCompletion(completion observability.WorkflowCompletion) {
	metrics.completions = append(metrics.completions, completion)
}

func (metrics *recordingVerificationMetrics) RecordVerificationOperationalFailure(failure observability.OperationalFailure) {
	metrics.failures = append(metrics.failures, failure)
}

func (metrics *recordingVerificationMetrics) RecordVerificationSessionStart(start observability.SessionStart) {
	metrics.starts = append(metrics.starts, start)
}

func (metrics *recordingVerificationMetrics) RecordVerificationRecapture(recapture observability.Recapture) {
	metrics.recaptures = append(metrics.recaptures, recapture)
}

func TestObserveLifecycleRecordsBoundedTransitionFailureAndCompletion(t *testing.T) {
	t.Parallel()
	created := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	metrics := &recordingVerificationMetrics{}
	store := (&LifecycleStore{}).WithMetrics(metrics)
	store.observeLifecycle(
		verification.LifecycleCommand{OccurredAt: created.Add(time.Minute), Failure: verification.SessionFailure{Class: "provider", Code: "sub_01ARZ3NDEKTSV4RRFFQ69G5FAV"}},
		verification.Lifecycle{State: verification.SessionStateCreated, CreatedAt: created},
		verification.Lifecycle{State: verification.SessionStateFailed},
		"eu-1",
	)
	if len(metrics.transitions) != 1 {
		t.Fatalf("transitions = %d, want 1", len(metrics.transitions))
	}
	transition := metrics.transitions[0]
	if transition.From != observability.StateCreated || transition.To != observability.StateFailed ||
		transition.FailureClass != observability.FailureProvider || transition.Region != "eu-1" {
		t.Fatalf("transition = %+v", transition)
	}
	if len(metrics.failures) != 1 || metrics.failures[0].FailureClass != observability.FailureProvider {
		t.Fatalf("failures = %+v", metrics.failures)
	}
	if len(metrics.completions) != 1 || metrics.completions[0].Outcome != observability.OutcomeFailed ||
		metrics.completions[0].Duration != time.Minute || metrics.completions[0].Region != "eu-1" {
		t.Fatalf("completions = %+v", metrics.completions)
	}
	for _, recorded := range []string{
		transition.From.Safe(), transition.To.Safe(), transition.FailureClass.Safe(), transition.Region.Safe(),
		metrics.failures[0].FailureClass.Safe(), metrics.completions[0].Outcome.Safe(), metrics.completions[0].Region.Safe(),
	} {
		if strings.Contains(recorded, "sub_") || strings.Contains(recorded, "01ARZ3") {
			t.Fatalf("lifecycle metric leaked failure code: %q", recorded)
		}
	}
}

func TestObserveLifecycleSkipsCompletionForNonTerminalTransition(t *testing.T) {
	t.Parallel()
	metrics := &recordingVerificationMetrics{}
	store := (&LifecycleStore{}).WithMetrics(metrics)
	store.observeLifecycle(
		verification.LifecycleCommand{},
		verification.Lifecycle{State: verification.SessionStateCollecting, CreatedAt: time.Now().UTC()},
		verification.Lifecycle{State: verification.SessionStateProcessing},
		"",
	)
	if len(metrics.transitions) != 1 || len(metrics.failures) != 0 || len(metrics.completions) != 0 {
		t.Fatalf("unexpected observations: transitions=%d failures=%d completions=%d", len(metrics.transitions), len(metrics.failures), len(metrics.completions))
	}
	if metrics.transitions[0].FailureClass != observability.FailureNone {
		t.Fatalf("failure class = %q, want none", metrics.transitions[0].FailureClass.Safe())
	}
	if metrics.transitions[0].Region.Safe() != "unknown" {
		t.Fatalf("region = %q, want unknown", metrics.transitions[0].Region.Safe())
	}
}

func TestVerificationMetricVocabularyMappingsAreBounded(t *testing.T) {
	t.Parallel()
	if got := verificationState(verification.SessionState("sub_01ARZ3NDEKTSV4RRFFQ69G5FAV")); got != observability.StateOther {
		t.Fatalf("state = %q, want other", got)
	}
	if got := sessionFailureClass("sub_01ARZ3NDEKTSV4RRFFQ69G5FAV"); got != observability.FailureOther {
		t.Fatalf("failure class = %q, want other", got)
	}
	if got := recaptureReason("verifications.create"); got != observability.RecaptureOther {
		t.Fatalf("recapture reason = %q, want other", got)
	}
	if got := policyOutcome("not_an_outcome"); got != observability.OutcomeOther {
		t.Fatalf("outcome = %q, want other", got)
	}
}
