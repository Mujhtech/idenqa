package idempotency_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
)

func TestNewRequestScopesFingerprintAndRetention(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	request, err := idempotency.NewRequest(
		mustTenant(t),
		mustKey(t),
		"capture_profiles.create",
		"customer-attempt-1",
		[]byte(`{"name":"Standard"}`),
		now,
		24*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if request.Operation() != "capture_profiles.create" || request.Key() != "customer-attempt-1" ||
		!request.ExpiresAt().Equal(now.Add(24*time.Hour)) || len(request.Fingerprint().Bytes()) != 32 {
		t.Fatalf("NewRequest() = %#v", request)
	}
	if request.String() != "[REDACTED]" {
		t.Fatalf("Request.String() = %q", request.String())
	}
}

func TestNewRequestAcceptsCapturePrincipal(t *testing.T) {
	t.Parallel()

	token, err := id.ParseCaptureToken("ctk_01K3P4NQF00000000000000001")
	if err != nil {
		t.Fatalf("ParseCaptureToken() error = %v", err)
	}
	request, err := idempotency.NewRequest(
		mustTenant(t), token, "authorities.respond", "response-1",
		[]byte(`{"action":"consent"}`), time.Now(), time.Hour,
	)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	if request.Principal().String() != token.String() {
		t.Fatalf("Principal() = %q, want %q", request.Principal(), token)
	}
}

func TestNewRequestRejectsInvalidScope(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		operation string
		key       string
		canonical []byte
		retention time.Duration
	}{
		{name: "uppercase operation", operation: "Profiles.Create", key: "one", canonical: []byte(`{}`), retention: time.Hour},
		{name: "control in key", operation: "profiles.create", key: "one\n", canonical: []byte(`{}`), retention: time.Hour},
		{name: "empty body", operation: "profiles.create", key: "one", retention: time.Hour},
		{name: "zero retention", operation: "profiles.create", key: "one", canonical: []byte(`{}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := idempotency.NewRequest(
				mustTenant(t),
				mustKey(t),
				test.operation,
				test.key,
				test.canonical,
				time.Now(),
				test.retention,
			)
			if err == nil {
				t.Fatal("NewRequest() error = nil")
			}
		})
	}
}

func TestResultRequiresJSONObjectAndDefensiveCopy(t *testing.T) {
	t.Parallel()

	body := []byte(`{"profile_id":"prf_one"}`)
	result, err := idempotency.NewResult(201, body)
	if err != nil {
		t.Fatalf("NewResult() error = %v", err)
	}
	body[2] = 'x'
	copyOfBody := result.Body()
	copyOfBody[2] = 'y'
	if string(result.Body()) != `{"profile_id":"prf_one"}` {
		t.Fatal("Result exposed its body")
	}
	for _, invalid := range [][]byte{[]byte(`[]`), []byte(`null`), []byte(`not-json`)} {
		if _, err := idempotency.NewResult(200, invalid); err == nil {
			t.Fatalf("NewResult(%s) error = nil", invalid)
		}
	}
	if !errors.Is(idempotency.ErrConflict, idempotency.ErrConflict) {
		t.Fatal("ErrConflict is not stable")
	}
}

func mustTenant(t *testing.T) id.Tenant {
	t.Helper()

	value, err := id.ParseTenant("ten_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}

	return value
}

func mustKey(t *testing.T) id.APIKey {
	t.Helper()

	value, err := id.ParseAPIKey("key_01K3P4NQF00000000000000000")
	if err != nil {
		t.Fatalf("ParseAPIKey() error = %v", err)
	}

	return value
}
