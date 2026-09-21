package task

import "github.com/Mujhtech/idenqa/internal/platform/observability"

// Metrics is the bounded delivery metric receiver owned by this boundary.
type Metrics interface {
	RecordDeliveryAttempt(observability.DeliveryAttempt)
}
