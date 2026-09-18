package delivery

import (
	"bytes"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestSignIsByteExactAndDeterministic(t *testing.T) {
	eventID, _ := id.ParseEvent("evt_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	first, err := Sign([]byte("01234567890123456789012345678901"), eventID, now, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Sign([]byte("01234567890123456789012345678901"), eventID, now, []byte("{}"))
	if err != nil || first != second {
		t.Fatalf("signature changed: %#v %#v", first, second)
	}
	changed, _ := Sign([]byte("01234567890123456789012345678901"), eventID, now, []byte("{ }"))
	if changed.Value == first.Value {
		t.Fatal("changed body retained signature")
	}
}

func TestSafeDiagnosticResponseExcerptIsBoundedAndSanitised(t *testing.T) {
	t.Parallel()

	base, err := NewSafeDiagnostic(503, "retryable_status", 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name      string
		body      []byte
		wantExact string
		wantLen   int
		wantTrunc bool
	}{
		{name: "no body"},
		{name: "exact", body: []byte(`{"error":"unavailable"}`), wantExact: `{"error":"unavailable"}`},
		{name: "at limit", body: bytes.Repeat([]byte{'a'}, MaximumResponseExcerptBytes), wantLen: MaximumResponseExcerptBytes},
		{name: "over limit", body: bytes.Repeat([]byte{'a'}, MaximumResponseExcerptBytes+1), wantLen: MaximumResponseExcerptBytes, wantTrunc: true},
		{name: "invalid utf8 replaced", body: []byte{0xFF}, wantExact: "\uFFFD"},
		{
			name:      "replacement expansion trimmed",
			body:      bytes.Repeat([]byte{'a', 0xFF}, MaximumResponseExcerptBytes/2),
			wantLen:   MaximumResponseExcerptBytes,
			wantTrunc: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			diagnostic := base.WithResponse(test.body)
			if err := diagnostic.Validate(); err != nil {
				t.Fatalf("Validate() = %v", err)
			}
			if !utf8.ValidString(diagnostic.Excerpt) || len(diagnostic.Excerpt) > MaximumResponseExcerptBytes {
				t.Fatalf("excerpt = %q", diagnostic.Excerpt)
			}
			if test.wantExact != "" && diagnostic.Excerpt != test.wantExact {
				t.Fatalf("excerpt = %q, want %q", diagnostic.Excerpt, test.wantExact)
			}
			if test.wantLen > 0 && len(diagnostic.Excerpt) != test.wantLen {
				t.Fatalf("excerpt length = %d, want %d", len(diagnostic.Excerpt), test.wantLen)
			}
			if diagnostic.Truncated != test.wantTrunc {
				t.Fatalf("truncated = %t, want %t", diagnostic.Truncated, test.wantTrunc)
			}
		})
	}
}

func TestSafeDiagnosticRejectsInvalidExcerpt(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		diagnostic SafeDiagnostic
	}{
		{name: "oversized", diagnostic: SafeDiagnostic{StatusCode: 200, ErrorClass: "delivered", Excerpt: strings.Repeat("a", MaximumResponseExcerptBytes+1)}},
		{name: "invalid utf8", diagnostic: SafeDiagnostic{StatusCode: 200, ErrorClass: "delivered", Excerpt: "\xFF"}},
		{name: "truncated without content", diagnostic: SafeDiagnostic{StatusCode: 200, ErrorClass: "delivered", Truncated: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.diagnostic.Validate(); err == nil {
				t.Fatal("Validate() = nil")
			}
		})
	}
}
