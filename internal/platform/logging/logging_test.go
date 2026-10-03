package logging_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/trace"

	"github.com/Mujhtech/idenqa/internal/platform/logging"
)

func TestNewRedactsSensitiveAttributes(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := logging.New(&output, "info", "json")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Info(
		"configured",
		"api_token", "token-value",
		"authorization", "bearer-value",
		"safe", "visible",
		slog.Group("nested", "client_secret", "secret-value"),
	)

	got := output.String()
	for _, forbidden := range []string{"token-value", "bearer-value", "secret-value"} {
		if strings.Contains(got, forbidden) {
			t.Errorf("log contains sensitive value %q: %s", forbidden, got)
		}
	}
	if gotCount := strings.Count(got, "[REDACTED]"); gotCount != 3 {
		t.Errorf("redaction count = %d, want 3: %s", gotCount, got)
	}
	if !strings.Contains(got, "visible") {
		t.Errorf("safe value was removed: %s", got)
	}
}

func TestNewHonoursLevel(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	logger, err := logging.New(&output, "warn", "text")
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger.Info("hidden")
	logger.Warn("visible")

	if got := output.String(); strings.Contains(got, "hidden") || !strings.Contains(got, "visible") {
		t.Fatalf("unexpected filtered output: %q", got)
	}
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		level     string
		format    string
		wantError string
	}{
		{name: "level", level: "verbose", format: "json", wantError: "parse log level"},
		{name: "format", level: "info", format: "xml", wantError: "log format"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := logging.New(&bytes.Buffer{}, test.level, test.format)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("New() error = %v, want it to contain %q", err, test.wantError)
			}
		})
	}
}

func TestContextLogsIncludeActiveTraceAndSpan(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"json", "text"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			logger, err := logging.New(&output, "info", format)
			if err != nil {
				t.Fatal(err)
			}
			traceID, _ := trace.TraceIDFromHex("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
			spanID, _ := trace.SpanIDFromHex("bbbbbbbbbbbbbbbb")
			ctx := trace.ContextWithSpanContext(t.Context(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: traceID, SpanID: spanID}))
			logger.With("component", "worker").WithGroup("operation").InfoContext(ctx, "attempt", "secret", "private")
			got := output.String()
			if !strings.Contains(got, traceID.String()) || !strings.Contains(got, spanID.String()) || strings.Contains(got, "private") {
				t.Fatal("missing correlation or redaction")
			}
			output.Reset()
			logger.InfoContext(t.Context(), "untraced")
			if strings.Contains(output.String(), "trace_id") || strings.Contains(output.String(), "span_id") {
				t.Fatal("untraced log has fabricated IDs")
			}
		})
	}
}
