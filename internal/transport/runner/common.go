package runner

import runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"

func boundedUint16(value uint32) uint16 {
	if value > uint32(^uint16(0)) {
		return 0
	}
	return uint16(value)
}

func versionToProto(major, minor uint16) *runnerv1.Version {
	return &runnerv1.Version{Major: uint32(major), Minor: uint32(minor)}
}

func signalOutcomeToProto(value string) runnerv1.SignalOutcome {
	switch value {
	case "satisfied":
		return runnerv1.SignalOutcome_SIGNAL_OUTCOME_SATISFIED
	case "not_satisfied":
		return runnerv1.SignalOutcome_SIGNAL_OUTCOME_NOT_SATISFIED
	case "inconclusive":
		return runnerv1.SignalOutcome_SIGNAL_OUTCOME_INCONCLUSIVE
	default:
		return runnerv1.SignalOutcome_SIGNAL_OUTCOME_UNSPECIFIED
	}
}

func signalOutcomeFromProto(value runnerv1.SignalOutcome) string {
	switch value {
	case runnerv1.SignalOutcome_SIGNAL_OUTCOME_SATISFIED:
		return "satisfied"
	case runnerv1.SignalOutcome_SIGNAL_OUTCOME_NOT_SATISFIED:
		return "not_satisfied"
	case runnerv1.SignalOutcome_SIGNAL_OUTCOME_INCONCLUSIVE:
		return "inconclusive"
	default:
		return ""
	}
}

func resultOutcomeToProto(value string) runnerv1.ResultOutcome {
	switch value {
	case "completed":
		return runnerv1.ResultOutcome_RESULT_OUTCOME_COMPLETED
	case "failed":
		return runnerv1.ResultOutcome_RESULT_OUTCOME_FAILED
	default:
		return runnerv1.ResultOutcome_RESULT_OUTCOME_UNSPECIFIED
	}
}

func resultOutcomeFromProto(value runnerv1.ResultOutcome) string {
	switch value {
	case runnerv1.ResultOutcome_RESULT_OUTCOME_COMPLETED:
		return "completed"
	case runnerv1.ResultOutcome_RESULT_OUTCOME_FAILED:
		return "failed"
	default:
		return ""
	}
}

func retryToProto(value string) runnerv1.RetryDisposition {
	switch value {
	case "never":
		return runnerv1.RetryDisposition_RETRY_DISPOSITION_NEVER
	case "backoff":
		return runnerv1.RetryDisposition_RETRY_DISPOSITION_BACKOFF
	case "reconcile":
		return runnerv1.RetryDisposition_RETRY_DISPOSITION_RECONCILE
	default:
		return runnerv1.RetryDisposition_RETRY_DISPOSITION_UNSPECIFIED
	}
}

func retryFromProto(value runnerv1.RetryDisposition) string {
	switch value {
	case runnerv1.RetryDisposition_RETRY_DISPOSITION_NEVER:
		return "never"
	case runnerv1.RetryDisposition_RETRY_DISPOSITION_BACKOFF:
		return "backoff"
	case runnerv1.RetryDisposition_RETRY_DISPOSITION_RECONCILE:
		return "reconcile"
	default:
		return ""
	}
}

func healthToProto(value string) runnerv1.HealthState {
	switch value {
	case "ready":
		return runnerv1.HealthState_HEALTH_STATE_READY
	case "degraded":
		return runnerv1.HealthState_HEALTH_STATE_DEGRADED
	case "not_ready":
		return runnerv1.HealthState_HEALTH_STATE_NOT_READY
	default:
		return runnerv1.HealthState_HEALTH_STATE_UNSPECIFIED
	}
}

func healthFromProto(value runnerv1.HealthState) string {
	switch value {
	case runnerv1.HealthState_HEALTH_STATE_READY:
		return "ready"
	case runnerv1.HealthState_HEALTH_STATE_DEGRADED:
		return "degraded"
	case runnerv1.HealthState_HEALTH_STATE_NOT_READY:
		return "not_ready"
	default:
		return ""
	}
}
