package telemetry

import (
	"context"
	"errors"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestOperationTracerParentSamplingAndSafeFailure(t *testing.T) {
	t.Parallel()
	for _, sampled := range []bool{true, false} {
		t.Run(map[bool]string{true: "sampled", false: "unsampled"}[sampled], func(t *testing.T) {
			t.Parallel()
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample())), sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			traceID, _ := trace.TraceIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			spanID, _ := trace.SpanIDFromHex("bbbbbbbbbbbbbbbb")
			flags := trace.TraceFlags(0)
			if sampled {
				flags = trace.FlagsSampled
			}
			parent := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, TraceFlags: flags, Remote: true})
			ctx := trace.ContextWithRemoteSpanContext(t.Context(), parent)
			ctx, complete := NewOperationTracer(provider).Start(ctx, "verification.create")
			child := trace.SpanContextFromContext(ctx)
			if child.TraceID() != traceID || child.SpanID() == spanID || child.IsSampled() != sampled {
				t.Fatal("child lost parent or sampling")
			}
			complete(errors.New("secret credential and identity bytes"))
			spans := recorder.Ended()
			if !sampled {
				if len(spans) != 0 {
					t.Fatal("unsampled span recorded")
				}
				return
			}
			if len(spans) != 1 || spans[0].Parent().SpanID() != spanID || spans[0].Status().Code != codes.Error || spans[0].Status().Description != "" {
				t.Fatal("incorrect failure span")
			}
			for _, event := range spans[0].Events() {
				for _, value := range event.Attributes {
					if value.Key == "exception.message" && value.Value.AsString() != "operation failed" {
						t.Fatal("sensitive error exported")
					}
				}
			}
		})
	}
}

func TestMissingOperationTracerPreservesContext(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	got, complete := observability.StartSpan(ctx, nil, "verification.create")
	if got != ctx {
		t.Fatal("no-op tracer changed context")
	}
	complete(nil)
}

func TestLocalProvidersPreserveIncomingSampling(t *testing.T) {
	t.Parallel()
	providers := NewProviders("test", "test")
	t.Cleanup(func() { _ = providers.Shutdown(context.Background()) })
	traceID, _ := trace.TraceIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	spanID, _ := trace.SpanIDFromHex("bbbbbbbbbbbbbbbb")
	parent := trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID, Remote: true})
	ctx := trace.ContextWithRemoteSpanContext(t.Context(), parent)
	ctx, span := providers.TracerProvider().Tracer("test").Start(ctx, "application")
	defer span.End()
	if child := trace.SpanContextFromContext(ctx); child.IsSampled() || child.TraceID() != traceID {
		t.Fatal("disabled export process changed incoming sampling")
	}
}
