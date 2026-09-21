package postgres

import (
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// Metrics is the bounded verification metric receiver owned by this boundary.
// It carries no tenant, subject, evidence, or workflow identifiers.
type Metrics interface {
	RecordVerificationTransition(observability.Transition)
	RecordVerificationCompletion(observability.WorkflowCompletion)
	RecordVerificationOperationalFailure(observability.OperationalFailure)
	RecordVerificationSessionStart(observability.SessionStart)
	RecordVerificationRecapture(observability.Recapture)
}

func verificationState(state verification.SessionState) observability.State {
	switch state {
	case verification.SessionStateCreated:
		return observability.StateCreated
	case verification.SessionStateCollecting:
		return observability.StateCollecting
	case verification.SessionStateProcessing:
		return observability.StateProcessing
	case verification.SessionStateAwaitingInput:
		return observability.StateAwaitingInput
	case verification.SessionStateAwaitingExternal:
		return observability.StateAwaitingExt
	case verification.SessionStateManualReview:
		return observability.StateManualReview
	case verification.SessionStateCompleted:
		return observability.StateCompleted
	case verification.SessionStateCancelled:
		return observability.StateCancelled
	case verification.SessionStateFailed:
		return observability.StateFailed
	case verification.SessionStateExpired:
		return observability.StateExpired
	default:
		return observability.StateOther
	}
}

func policyOutcome(outcome policy.Outcome) observability.Outcome {
	switch outcome {
	case policy.OutcomeVerified:
		return observability.OutcomeVerified
	case policy.OutcomeNotVerified:
		return observability.OutcomeNotVerified
	case policy.OutcomeInconclusive:
		return observability.OutcomeInconclusive
	default:
		return observability.OutcomeOther
	}
}

// terminalOutcome maps a terminal non-decision lifecycle state to its outcome.
func terminalOutcome(state verification.SessionState) observability.Outcome {
	switch state {
	case verification.SessionStateCancelled:
		return observability.OutcomeCancelled
	case verification.SessionStateExpired:
		return observability.OutcomeExpired
	case verification.SessionStateFailed:
		return observability.OutcomeFailed
	default:
		return observability.OutcomeOther
	}
}

func sessionFailureClass(class string) observability.FailureClass {
	switch class {
	case "":
		return observability.FailureNone
	case "policy":
		return observability.FailurePolicy
	case "provider":
		return observability.FailureProvider
	case "model":
		return observability.FailureModel
	case "timeout", "deadline":
		return observability.FailureTimeout
	case "transport", "network":
		return observability.FailureTransport
	case "validation":
		return observability.FailureValidation
	case "authority", "permission":
		return observability.FailureAuthority
	case "unavailable":
		return observability.FailureUnavailable
	case "internal":
		return observability.FailureInternal
	default:
		return observability.FailureOther
	}
}

func recaptureReason(operation string) observability.RecaptureReason {
	switch operation {
	case "reviews.recapture":
		return observability.RecaptureRequested
	case "reviews.recapture.renew":
		return observability.RecaptureRenewed
	default:
		return observability.RecaptureOther
	}
}
