package privacy

import "github.com/Mujhtech/idenqa/internal/platform/observability"

// RequestMetrics is the bounded privacy-request metric receiver owned by this
// boundary. Every label is collapsed through the observability allow-lists.
type RequestMetrics interface {
	RecordPrivacyRequestTransition(observability.PrivacyRequestTransition)
	RecordPrivacyRequestAge(observability.PrivacyRequestAge)
}

func requestType(request RequestType) observability.RequestType {
	switch request {
	case RequestAccess:
		return observability.RequestAccess
	case RequestPortability:
		return observability.RequestPortability
	case RequestCorrection:
		return observability.RequestCorrection
	case RequestRestriction:
		return observability.RequestRestriction
	case RequestObjection:
		return observability.RequestObjection
	case RequestErasure:
		return observability.RequestErasure
	default:
		return observability.RequestOther
	}
}

func requestState(state RequestState) observability.RequestState {
	switch state {
	case RequestStateRequested:
		return observability.RequestStateRequested
	case RequestStateInReview:
		return observability.RequestStateInReview
	case RequestStateApproved:
		return observability.RequestStateApproved
	case RequestStatePartiallyApproved:
		return observability.RequestStatePartiallyApproved
	case RequestStateDenied:
		return observability.RequestStateDenied
	case RequestStateExecuting:
		return observability.RequestStateExecuting
	case RequestStateCompleted:
		return observability.RequestStateCompleted
	case RequestStateFailed:
		return observability.RequestStateFailed
	case RequestStateWithdrawn:
		return observability.RequestStateWithdrawn
	case RequestStateExpired:
		return observability.RequestStateExpired
	default:
		return observability.RequestStateOther
	}
}
