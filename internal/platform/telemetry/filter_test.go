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
