package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/telemetry"
	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestQueryTracingPreservesParentAndRedactsSQLAndErrors(t *testing.T) {
	t.Parallel()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	ctx, parent := provider.Tracer("test").Start(t.Context(), "service")
	defer parent.End()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	tracer := &queryTracer{}
	pool := &Pool{tracing: tracer}
	pool.WithTracer(telemetry.NewOperationTracer(provider))
	queryContext := tracer.TraceQueryStart(ctx, nil, pgx.TraceQueryStartData{SQL: "select secret from identity where subject = $1", Args: []any{"private-subject"}})
	querySpan := trace.SpanFromContext(queryContext)
	if querySpan.SpanContext().SpanID() == parent.SpanContext().SpanID() {
		t.Fatal("query did not get a child span")
	}
	cancel()
	if queryContext.Err() != context.Canceled {
		t.Fatal("query lost cancellation")
	}
	tracer.TraceQueryEnd(queryContext, nil, pgx.TraceQueryEndData{Err: errors.New("private database cause")})
	spans := recorder.Ended()
	if len(spans) != 1 || spans[0].Name() != "postgres.query" || spans[0].Parent().SpanID() != parent.SpanContext().SpanID() || spans[0].Status().Code != codes.Error {
		t.Fatal("incorrect query span")
	}
	if len(spans[0].Attributes()) != 0 {
		t.Fatal("query exported SQL or arguments")
	}
	for _, event := range spans[0].Events() {
		for _, value := range event.Attributes {
			if value.Key == "exception.message" && value.Value.AsString() != "operation failed" {
				t.Fatal("database error text exported")
			}
		}
	}
}
