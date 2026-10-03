// Package logging configures structured, redacting application logs.
package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

const redacted = "[REDACTED]"

// New constructs a structured logger with the configured level and format.
func New(writer io.Writer, levelText, format string) (*slog.Logger, error) {
	var level slog.Level
	if err := level.UnmarshalText([]byte(levelText)); err != nil {
		return nil, fmt.Errorf("parse log level: %w", err)
	}

	options := &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, attribute slog.Attr) slog.Attr {
			if sensitiveKey(attribute.Key) {
				return slog.String(attribute.Key, redacted)
			}

			return attribute
		},
	}

	var handler slog.Handler
	switch strings.ToLower(format) {
	case "json":
		handler = slog.NewJSONHandler(writer, options)
	case "text":
		handler = slog.NewTextHandler(writer, options)
	default:
		return nil, fmt.Errorf("log format %q is not supported", format)
	}

	return slog.New(correlatingHandler{Handler: handler}), nil
}

type correlatingHandler struct{ slog.Handler }

func (handler correlatingHandler) Handle(ctx context.Context, record slog.Record) error {
	if span := trace.SpanContextFromContext(ctx); span.IsValid() {
		record = record.Clone()
		record.AddAttrs(slog.String("trace_id", span.TraceID().String()), slog.String("span_id", span.SpanID().String()))
	}
	return handler.Handler.Handle(ctx, record)
}

func (handler correlatingHandler) WithAttrs(attributes []slog.Attr) slog.Handler {
	return correlatingHandler{Handler: handler.Handler.WithAttrs(attributes)}
}

func (handler correlatingHandler) WithGroup(name string) slog.Handler {
	return correlatingHandler{Handler: handler.Handler.WithGroup(name)}
}

func sensitiveKey(key string) bool {
	normalized := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(strings.ToLower(key))
	if normalized == "authorization" || normalized == "cookie" || normalized == "set_cookie" {
		return true
	}

	for _, part := range strings.Split(normalized, "_") {
		switch part {
		case "credential", "credentials", "password", "secret", "token":
			return true
		}
	}

	return false
}
