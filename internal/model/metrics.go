package model

import "github.com/Mujhtech/idenqa/internal/platform/observability"

// Metrics is the bounded model metric receiver owned by this boundary.
type Metrics interface {
	RecordModelDispatch(observability.ModelDispatch)
	RecordModelHealth(observability.ModelHealth)
}
