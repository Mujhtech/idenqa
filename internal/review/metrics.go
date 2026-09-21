package review

import "github.com/Mujhtech/idenqa/internal/platform/observability"

// Metrics is the bounded review metric receiver owned by this boundary.
type Metrics interface {
	RecordReviewResolution(observability.ReviewResolution)
}

func reviewOutcome(resolution Resolution) observability.ReviewOutcome {
	switch resolution {
	case ResolutionSatisfy:
		return observability.ReviewSatisfied
	case ResolutionNotSatisfy:
		return observability.ReviewNotSatisfied
	case ResolutionRequestInput:
		return observability.ReviewInputRequired
	default:
		return observability.ReviewOther
	}
}

func reviewOversight(oversight Oversight) observability.Oversight {
	switch oversight {
	case OversightSingle:
		return observability.OversightSingle
	case OversightDual:
		return observability.OversightDual
	default:
		return observability.OversightOther
	}
}
