package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestOpenRejectsInvalidConfigurationWithoutLeakingURL(t *testing.T) {
	t.Parallel()

	// This deliberately fake credential verifies that connection errors redact URLs.
	secretURL := "postgres://idenqa:subject-secret@127.0.0.1:1/idenqa?sslmode=disable" //nolint:gosec // Fake secret exercises redaction.
	_, err := Open(context.Background(), Config{URL: secretURL})
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Open() error = %v, want ErrInvalidConfiguration", err)
	}
	if strings.Contains(err.Error(), "subject-secret") {
		t.Fatalf("Open() error leaks database credential: %v", err)
	}
}

func TestOpenReportsUnavailableWithoutLeakingURL(t *testing.T) {
	t.Parallel()

	// This deliberately fake credential verifies that connection errors redact URLs.
	secretURL := "postgres://idenqa:subject-secret@127.0.0.1:1/idenqa?sslmode=disable" //nolint:gosec // Fake secret exercises redaction.
	_, err := Open(context.Background(), Config{
		URL:                 secretURL,
		MaxConnections:      1,
		MinConnections:      0,
		MaxConnectionAge:    time.Minute,
		MaxConnectionIdle:   time.Minute,
		HealthCheckInterval: time.Minute,
		ConnectTimeout:      100 * time.Millisecond,
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open() error = %v, want ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), "subject-secret") {
		t.Fatalf("Open() error leaks database credential: %v", err)
	}
}

func TestOpenRejectsUnsafeRole(t *testing.T) {
	t.Parallel()

	_, err := Open(context.Background(), Config{URL: "postgres://localhost/idenqa", Role: "runtime; RESET ROLE"})
	if !errors.Is(err, ErrInvalidConfiguration) {
		t.Fatalf("Open() error = %v, want ErrInvalidConfiguration", err)
	}
}
