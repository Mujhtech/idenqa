package telemetry

import (
	"context"
	"errors"

	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// OperationTracer implements the owned application tracing port with the
// process-owned provider. It exports no application error text or payloads.
type OperationTracer struct{ tracer trace.Tracer }

// NewOperationTracer constructs an explicitly injected application tracer.
func NewOperationTracer(provider trace.TracerProvider) *OperationTracer {
	return &OperationTracer{tracer: provider.Tracer("github.com/Mujhtech/idenqa/internal/application")}
}

// Start opens a child span and preserves the caller's cancellation and deadline.
func (tracer *OperationTracer) Start(ctx context.Context, operation string) (context.Context, func(error)) {
	ctx, span := tracer.tracer.Start(ctx, operation)
	return ctx, func(err error) {
		if err != nil {
			// Use a fixed error rather than potentially sensitive wrapped causes,
			// even if the supplied provider has no attribute filter.
			message := "operation failed"
			if errors.Is(err, observability.ErrOperationPanic) {
				message = "operation panicked"
			}
			span.RecordError(errors.New(message))
			span.SetStatus(codes.Error, "")
		}
		span.End()
	}
}

var _ observability.Tracer = (*OperationTracer)(nil)
