package verification

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
)

func TestProjectCaptureOutcomeUsesClosedSubjectSafeStates(t *testing.T) {
	t.Parallel()

	verificationID, err := id.ParseVerification("ver_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseVerification() error = %v", err)
	}
	now := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name    string
		session SessionState
		outcome policy.Outcome
		want    CaptureOutcomeState
	}{
		{name: "collecting", session: SessionStateCollecting, want: CaptureOutcomeCaptureRequired},
		{name: "awaiting input", session: SessionStateAwaitingInput, want: CaptureOutcomeActionRequired},
		{name: "processing", session: SessionStateProcessing, want: CaptureOutcomeProcessing},
		{name: "external", session: SessionStateAwaitingExternal, want: CaptureOutcomeProcessing},
		{name: "review", session: SessionStateManualReview, want: CaptureOutcomeProcessing},
		{name: "verified", session: SessionStateCompleted, outcome: policy.OutcomeVerified, want: CaptureOutcomeVerified},
		{name: "not verified", session: SessionStateCompleted, outcome: policy.OutcomeNotVerified, want: CaptureOutcomeNotVerified},
		{name: "inconclusive", session: SessionStateCompleted, outcome: policy.OutcomeInconclusive, want: CaptureOutcomeInconclusive},
		{name: "cancelled", session: SessionStateCancelled, want: CaptureOutcomeCancelled},
		{name: "expired", session: SessionStateExpired, want: CaptureOutcomeExpired},
		{name: "failed", session: SessionStateFailed, want: CaptureOutcomeFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := projectCaptureOutcome(CaptureOutcomeRecord{
				VerificationID:  verificationID,
				SessionState:    test.session,
				SessionVersion:  3,
				UpdatedAt:       now,
				DecisionOutcome: test.outcome,
			})
			if err != nil {
				t.Fatalf("projectCaptureOutcome() error = %v", err)
			}
			if got.State != test.want || got.VerificationID.String() != verificationID.String() ||
				got.SessionVersion != 3 || !got.UpdatedAt.Equal(now) {
				t.Fatalf("projectCaptureOutcome() = %+v, want state %q", got, test.want)
			}
		})
	}
}

func TestProjectCaptureOutcomeRejectsDecisionOnNonCompletedState(t *testing.T) {
	t.Parallel()

	verificationID, _ := id.ParseVerification("ver_01K3P4NQF00000000000000000")
	_, err := projectCaptureOutcome(CaptureOutcomeRecord{
		VerificationID:  verificationID,
		SessionState:    SessionStateProcessing,
		SessionVersion:  2,
		UpdatedAt:       time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC),
		DecisionOutcome: policy.OutcomeVerified,
	})
	if !errors.Is(err, ErrSessionConflict) {
		t.Fatalf("projectCaptureOutcome() error = %v, want ErrSessionConflict", err)
	}
}
