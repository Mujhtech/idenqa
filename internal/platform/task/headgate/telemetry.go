package headgate

import (
	"context"
	"time"

	libheadgate "github.com/mujhtech/headgate/go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

// Telemetry bridges Headgate's exporter-free lifecycle facade into the
// process-owned OpenTelemetry providers.
type Telemetry struct {
	tracer    trace.Tracer
	events    metric.Int64Counter
	duration  metric.Float64Histogram
	inflight  metric.Int64Gauge
	capacity  metric.Int64Gauge
	memory    metric.Int64Gauge
	installID string
}

const maximumMetricInt64 = uint64(1<<63 - 1)

// NewTelemetry creates bounded task instruments. Installation identity is a
// deployment attribute; tenant, task ID, fingerprint, and payload never become
// metric labels.
func NewTelemetry(
	tracerProvider trace.TracerProvider,
	meterProvider metric.MeterProvider,
	installationID string,
) (*Telemetry, error) {
	meter := meterProvider.Meter("github.com/Mujhtech/idenqa/internal/platform/task/headgate")
	events, err := meter.Int64Counter("idenqa.task.events")
	if err != nil {
		return nil, err
	}
	duration, err := meter.Float64Histogram("idenqa.task.attempt.duration", metric.WithUnit("s"))
	if err != nil {
		return nil, err
	}
	inflight, err := meter.Int64Gauge("idenqa.task.worker.inflight")
	if err != nil {
		return nil, err
	}
	capacity, err := meter.Int64Gauge("idenqa.task.worker.capacity")
	if err != nil {
		return nil, err
	}
	memory, err := meter.Int64Gauge("idenqa.task.worker.memory", metric.WithUnit("By"))
	if err != nil {
		return nil, err
	}
	return &Telemetry{
		tracer: tracerProvider.Tracer("github.com/Mujhtech/idenqa/internal/platform/task/headgate"),
		events: events, duration: duration, inflight: inflight, capacity: capacity,
		memory: memory, installID: installationID,
	}, nil
}

// OnEvent implements Headgate's non-blocking telemetry facade.
func (telemetry *Telemetry) OnEvent(event libheadgate.Event) {
	if telemetry == nil {
		return
	}
	ctx := context.Background()
	attributes := []attribute.KeyValue{
		attribute.String("task.system", "headgate"),
		attribute.String("task.installation", telemetry.installID),
		attribute.String("task.event", event.Type),
	}
	if event.Queue != "" {
		attributes = append(attributes, attribute.String("task.queue", event.Queue))
	}
	if event.Outcome != "" {
		attributes = append(attributes, attribute.String("task.outcome", event.Outcome))
	}
	if event.Policy != "" {
		attributes = append(attributes, attribute.String("task.policy", event.Policy))
	}
	count := int64(event.Count)
	if count == 0 {
		count = 1
	}
	telemetry.events.Add(ctx, count, metric.WithAttributes(attributes...))
	switch event.Type {
	case "job_span":
		telemetry.recordAttempt(event, attributes)
	case "worker_saturation":
		workerAttributes := metric.WithAttributes(
			attribute.String("task.system", "headgate"),
			attribute.String("task.installation", telemetry.installID),
		)
		telemetry.inflight.Record(ctx, int64(event.Inflight), workerAttributes)
		telemetry.capacity.Record(ctx, int64(event.Capacity), workerAttributes)
	case "worker_memory":
		memoryBytes := event.MemoryBytes
		if memoryBytes > maximumMetricInt64 {
			memoryBytes = maximumMetricInt64
		}
		telemetry.memory.Record(ctx, int64(memoryBytes), metric.WithAttributes(
			attribute.String("task.system", "headgate"),
			attribute.String("task.installation", telemetry.installID),
		))
	}
}

func (telemetry *Telemetry) recordAttempt(event libheadgate.Event, attributes []attribute.KeyValue) {
	ctx := context.Background()
	if event.Trace.Valid() {
		traceID, traceErr := trace.TraceIDFromHex(event.Trace.TraceID)
		spanID, spanErr := trace.SpanIDFromHex(event.Trace.SpanID)
		if traceErr == nil && spanErr == nil {
			flags := trace.TraceFlags(0)
			if event.Trace.Sampled() {
				flags = trace.FlagsSampled
			}
			ctx = trace.ContextWithRemoteSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
				TraceID: traceID, SpanID: spanID, TraceFlags: flags, Remote: true,
			}))
		}
	}
	started := time.UnixMilli(event.StartedAtMs)
	ctx, span := telemetry.tracer.Start(ctx, "task.attempt",
		trace.WithTimestamp(started), trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attributes...))
	_ = ctx
	if event.Outcome != "success" {
		span.SetStatus(codes.Error, event.Outcome)
	}
	span.End(trace.WithTimestamp(started.Add(event.Duration)))
	telemetry.duration.Record(context.Background(), event.Duration.Seconds(), metric.WithAttributes(attributes...))
}

var _ libheadgate.Telemetry = (*Telemetry)(nil)
