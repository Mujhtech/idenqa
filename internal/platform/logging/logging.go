// Package logging configures structured, redacting application logs.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"strings"
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

	return slog.New(handler), nil
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
