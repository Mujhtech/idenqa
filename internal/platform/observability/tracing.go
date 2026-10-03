package observability

import (
	"context"
	"errors"
)

// ErrOperationPanic is a safe diagnostic classification, never a panic value.
var ErrOperationPanic = errors.New("operation panicked")

// Tracer starts an operation and returns its context and completion callback.
// Implementations must not export error text, arguments, credentials, or evidence.
// Operation names are static code-defined names, never request-derived values.
type Tracer interface {
	Start(context.Context, string) (context.Context, func(error))
}

// StartSpan uses an optional, explicitly injected tracer. The returned context
// must be passed to downstream calls. Complete must run on every return path.
func StartSpan(ctx context.Context, tracer Tracer, operation string) (context.Context, func(error)) {
	if tracer == nil {
		return ctx, func(error) {}
	}
	return tracer.Start(ctx, operation)
}

// EndSpan must be deferred directly. It closes the operation on return or
// panic, preserving the original panic for the process recovery boundary.
func EndSpan(complete func(error), result *error) {
	if recovered := recover(); recovered != nil {
		complete(ErrOperationPanic)
		panic(recovered)
	}
	complete(*result)
}
