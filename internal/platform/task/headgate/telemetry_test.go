package headgate

import (
	"testing"
	"time"

	libheadgate "github.com/mujhtech/headgate/go"
	metricnoop "go.opentelemetry.io/otel/metric/noop"
	tracenoop "go.opentelemetry.io/otel/trace/noop"
)

func TestTelemetryAcceptsBoundedLifecycleEvents(t *testing.T) {
	t.Parallel()
	bridge, err := NewTelemetry(tracenoop.NewTracerProvider(), metricnoop.NewMeterProvider(), "idenqa-test")
	if err != nil {
		t.Fatal(err)
	}
	bridge.OnEvent(libheadgate.Event{
		Type: "job_span", Queue: QueueVerification, Outcome: "success",
		StartedAtMs: time.Now().Add(-time.Second).UnixMilli(), Duration: time.Second,
		Trace: libheadgate.TraceContext{
			TraceID: "0123456789abcdef0123456789abcdef", SpanID: "0123456789abcdef", TraceFlags: 1,
		},
	})
	bridge.OnEvent(libheadgate.Event{Type: "worker_saturation", Inflight: 2, Capacity: 8})
	bridge.OnEvent(libheadgate.Event{Type: "worker_memory", MemoryBytes: 1024})
}
