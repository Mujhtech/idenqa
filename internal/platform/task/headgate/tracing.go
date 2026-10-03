package headgate

import (
	"context"
	"errors"

	"github.com/Mujhtech/idenqa/internal/platform/task"
	libheadgate "github.com/mujhtech/headgate/go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

func enqueueParent(ctx context.Context, intents []task.Intent) (context.Context, bool) {
	if trace.SpanContextFromContext(ctx).IsValid() {
		return ctx, true
	}
	if len(intents) == 0 {
		return ctx, false
	}
	parent, state := intents[0].Traceparent(), intents[0].Tracestate()
	for _, intent := range intents[1:] {
		if intent.Traceparent() != parent || intent.Tracestate() != state {
			return ctx, false
		}
	}
	extracted := propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{"traceparent": parent, "tracestate": state})
	return extracted, trace.SpanContextFromContext(extracted).IsValid()
}

func injectTaskContext(ctx context.Context, envelopes []libheadgate.Envelope, sharedParent bool) {
	if !trace.SpanContextFromContext(ctx).IsValid() {
		return
	}
	for index := range envelopes {
		if envelopes[index].Headers == nil {
			envelopes[index].Headers = make(map[string]string)
		}
		// A mixed-parent batch must retain each explicit intent's lineage.
		if !sharedParent && envelopes[index].Headers[libheadgate.TraceparentHeader] != "" {
			continue
		}
		delete(envelopes[index].Headers, libheadgate.TraceparentHeader)
		delete(envelopes[index].Headers, libheadgate.TracestateHeader)
		// Only W3C trace context is propagated; arbitrary baggage never travels
		// in task headers. The shared producer supersedes stale intent metadata.
		propagation.TraceContext{}.Inject(ctx, propagation.MapCarrier(envelopes[index].Headers))
	}
}

func startTaskSpan(ctx context.Context, intent task.Intent, attempt uint32, tracers ...trace.Tracer) (context.Context, trace.Span) {
	tracer := noop.NewTracerProvider().Tracer("github.com/Mujhtech/idenqa/internal/platform/task/headgate")
	if len(tracers) > 0 && tracers[0] != nil {
		tracer = tracers[0]
	}
	ctx = trace.ContextWithSpanContext(ctx, trace.SpanContext{})
	ctx = propagation.TraceContext{}.Extract(ctx, propagation.MapCarrier{
		"traceparent": intent.Traceparent(), "tracestate": intent.Tracestate(),
	})
	return tracer.Start(ctx, "task.execute", trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(
		attribute.Int64("task.attempt", int64(attempt)),
		attribute.String("task.name", intent.Key().Name.String()),
		attribute.String("task.queue", intent.Queue()),
		attribute.Int("task.version", int(intent.Key().Version)),
	))
}

func finishTaskSpan(span trace.Span, err error) {
	if err != nil {
		span.RecordError(errors.New("task operation failed"))
		span.SetStatus(codes.Error, "")
	}
	span.End()
}

func endTaskSpan(span trace.Span, result *error) {
	if recovered := recover(); recovered != nil {
		finishTaskSpan(span, errors.New("task panicked"))
		panic(recovered)
	}
	finishTaskSpan(span, *result)
}
