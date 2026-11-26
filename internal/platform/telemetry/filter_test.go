package telemetry_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/telemetry"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestAttributeFilteringTracerProviderDropsUnapprovedData(t *testing.T) {
	t.Parallel()

	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	t.Cleanup(func() {
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})

	safe := telemetry.NewAttributeFilteringTracerProvider(provider, "http.route", "http.status_code")
	_, span := safe.Tracer("test").Start(
		context.Background(),
		"GET /v1/items/{id}",
		oteltrace.WithAttributes(
			attribute.String("http.route", "/v1/items/{id}"),
			attribute.String("http.target", "/v1/items/subject-secret?token=secret"),
			attribute.String("tenant.id", "ten_secret"),
		),
	)
	span.SetAttributes(
		attribute.Int("http.status_code", 500),
		attribute.String("subject.id", "sub_secret"),
	)
	span.RecordError(errors.New("database password=secret"), oteltrace.WithAttributes(
		attribute.String("evidence.id", "evd_secret"),
	))
	span.SetStatus(codes.Error, "database password=secret")
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended span count = %d, want 1", len(ended))
	}
	serialized := ended[0].Name() + ended[0].Status().Description
	for _, keyValue := range ended[0].Attributes() {
		serialized += string(keyValue.Key) + keyValue.Value.String()
		if keyValue.Key != "http.route" && keyValue.Key != "http.status_code" {
			t.Errorf("unexpected span attribute %q", keyValue.Key)
		}
	}
	for _, event := range ended[0].Events() {
		serialized += event.Name
		for _, keyValue := range event.Attributes {
			serialized += string(keyValue.Key) + keyValue.Value.String()
		}
	}
	for _, prohibited := range []string{"subject-secret", "token=secret", "ten_secret", "sub_secret", "evd_secret", "password=secret"} {
		if strings.Contains(serialized, prohibited) {
			t.Errorf("span contains prohibited value %q: %s", prohibited, serialized)
		}
	}
}

func TestAttributeFilteringTracerProviderPreservesSDKValueLengthLimit(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{name: "replacement runes count toward limit", value: "\uFFFD\uFFFDoversized", limit: 1, want: "\uFFFD"},
		{name: "zero limit with replacement rune", value: "\uFFFDoversized", limit: 0, want: ""},
		{name: "zero limit with malformed UTF-8", value: "\xffoversized", limit: 0, want: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(
				sdktrace.WithSpanProcessor(recorder),
				sdktrace.WithRawSpanLimits(sdktrace.SpanLimits{
					AttributeValueLengthLimit: test.limit,
					AttributeCountLimit:       1,
				}),
			)
			t.Cleanup(func() {
				if err := provider.Shutdown(context.Background()); err != nil {
					t.Errorf("shutdown tracer provider: %v", err)
				}
			})

			safe := telemetry.NewAttributeFilteringTracerProvider(provider, "http.route")
			_, span := safe.Tracer("length-limit-regression").Start(t.Context(), "bounded attribute")
			span.SetAttributes(attribute.String("http.route", test.value))
			span.End()

			ended := recorder.Ended()
			if len(ended) != 1 {
				t.Fatalf("ended span count = %d, want 1", len(ended))
			}
			attributes := ended[0].Attributes()
			if len(attributes) != 1 || attributes[0].Key != "http.route" {
				t.Fatalf("allowed attributes = %v, want http.route", attributes)
			}
			if got := attributes[0].Value.AsString(); got != test.want {
				t.Errorf("recorded attribute = %q, want %q for length limit %d", got, test.want, test.limit)
			}
		})
	}
}
