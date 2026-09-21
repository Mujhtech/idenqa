package provider

import (
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
)

// Metrics is the bounded provider metric receiver owned by this boundary.
type Metrics interface {
	RecordProviderDispatch(observability.ProviderDispatch)
	RecordProviderCallbackDelay(observability.ProviderCallbackDelay)
	RecordProviderHealth(observability.ProviderHealth)
	RecordProviderThrottle(observability.ProviderThrottle)
}

// providerLabel prefers the deployment adapter identity so the label set stays
// a bounded deployment vocabulary instead of tenant registration identifiers.
func providerLabel(request providerv1.Request) observability.Provider {
	if request.Adapter.AdapterID != "" {
		return observability.Provider(request.Adapter.AdapterID)
	}
	return observability.Provider(request.ProviderID)
}

func providerDispatchOutcome(value providerv1.Result) observability.DispatchOutcome {
	switch {
	case value.Outcome == providerv1.ResultOutcomeCompleted:
		return observability.DispatchCompleted
	case value.Failure != nil && value.Failure.Code == "dispatch_requires_reconciliation":
		return observability.DispatchUncertain
	case value.Outcome == providerv1.ResultOutcomeFailed:
		return observability.DispatchFailed
	default:
		return observability.DispatchOther
	}
}

func providerFailureClass(value providerv1.Result) observability.FailureClass {
	if value.Failure == nil {
		return observability.FailureNone
	}
	switch value.Failure.Class {
	case providerv1.FailureInvalidRequest:
		return observability.FailureValidation
	case providerv1.FailureUnauthenticated, providerv1.FailureUnauthorized:
		return observability.FailureAuthority
	case providerv1.FailureUnsupported:
		return observability.FailureValidation
	case providerv1.FailureUnavailable:
		return observability.FailureUnavailable
	case providerv1.FailureRateLimited:
		return observability.FailureUnavailable
	case providerv1.FailureDeadline:
		return observability.FailureTimeout
	case providerv1.FailureCancelled:
		return observability.FailureTimeout
	case providerv1.FailureProviderRejected:
		return observability.FailureProvider
	case providerv1.FailureInternal:
		return observability.FailureInternal
	default:
		return observability.FailureOther
	}
}
