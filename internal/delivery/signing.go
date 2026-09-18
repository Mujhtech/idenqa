// Package delivery owns tenant-facing webhook delivery semantics.
package delivery

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// ErrInvalidSignatureInput means signing material violates the v1 contract.
var ErrInvalidSignatureInput = errors.New("delivery: invalid signature input")

// Signature is the canonical v1 tenant-facing signature metadata.
type Signature struct {
	Timestamp string
	EventID   string
	Value     string
}

// Sign authenticates exact body bytes and replay metadata.
func Sign(secret []byte, eventID id.Event, occurredAt time.Time, body []byte) (Signature, error) {
	if len(secret) < 32 || eventID.IsZero() || occurredAt.IsZero() || occurredAt.Location() != time.UTC || len(body) > 1<<20 {
		return Signature{}, ErrInvalidSignatureInput
	}
	timestamp := strconv.FormatInt(occurredAt.Unix(), 10)
	message := canonical(timestamp, eventID.String(), body)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(message)
	return Signature{Timestamp: timestamp, EventID: eventID.String(), Value: "v1=" + hex.EncodeToString(mac.Sum(nil))}, nil
}

func canonical(timestamp, eventID string, body []byte) []byte {
	result := make([]byte, 0, len(timestamp)+len(eventID)+len(body)+2)
	result = append(result, timestamp...)
	result = append(result, '\n')
	result = append(result, eventID...)
	result = append(result, '\n')
	return append(result, body...)
}

// MaximumResponseExcerptBytes bounds stored callback response content.
const MaximumResponseExcerptBytes = 4096

// SafeDiagnostic reduces response material to bounded operational metadata.
// The excerpt, when present, remains untrusted receiver content: it is length
// bounded, sanitised to valid UTF-8 and never parsed as structured data.
type SafeDiagnostic struct {
	StatusCode int
	ErrorClass string
	RetryAfter time.Duration
	Excerpt    string
	Truncated  bool
}

// NewSafeDiagnostic validates operational delivery metadata without a response excerpt.
func NewSafeDiagnostic(statusCode int, class string, retryAfter time.Duration) (SafeDiagnostic, error) {
	diagnostic := SafeDiagnostic{StatusCode: statusCode, ErrorClass: class, RetryAfter: retryAfter}
	if err := diagnostic.Validate(); err != nil {
		return SafeDiagnostic{}, err
	}
	return diagnostic, nil
}

// WithResponse attaches a bounded, sanitised excerpt of receiver response bytes.
// Invalid UTF-8 becomes the Unicode replacement character and any overflow is
// dropped with the truncation flag set.
func (diagnostic SafeDiagnostic) WithResponse(body []byte) SafeDiagnostic {
	if len(body) == 0 {
		return diagnostic
	}
	if len(body) > MaximumResponseExcerptBytes {
		body, diagnostic.Truncated = body[:MaximumResponseExcerptBytes], true
	}
	diagnostic.Excerpt = strings.ToValidUTF8(string(body), "\uFFFD")
	if len(diagnostic.Excerpt) > MaximumResponseExcerptBytes {
		cut := MaximumResponseExcerptBytes
		for cut > 0 && !utf8.RuneStart(diagnostic.Excerpt[cut]) {
			cut--
		}
		diagnostic.Excerpt, diagnostic.Truncated = diagnostic.Excerpt[:cut], true
	}
	return diagnostic
}

// Validate checks bounded response metadata and excerpt invariants.
func (diagnostic SafeDiagnostic) Validate() error {
	if diagnostic.StatusCode < 0 || diagnostic.StatusCode > 599 || len(diagnostic.ErrorClass) == 0 || len(diagnostic.ErrorClass) > 64 || strings.ContainsAny(diagnostic.ErrorClass, " \t\r\n") || diagnostic.RetryAfter < 0 || diagnostic.RetryAfter > 24*time.Hour ||
		len(diagnostic.Excerpt) > MaximumResponseExcerptBytes || !utf8.ValidString(diagnostic.Excerpt) || (diagnostic.Truncated && len(diagnostic.Excerpt) == 0) {
		return errors.New("delivery: invalid diagnostic")
	}
	return nil
}
