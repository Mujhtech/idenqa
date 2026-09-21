package postgres

import "github.com/Mujhtech/idenqa/internal/platform/observability"

// Metrics is the bounded capture metric receiver owned by this boundary.
type Metrics interface {
	RecordCaptureStep(observability.CaptureEvent)
}
