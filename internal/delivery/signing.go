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

// SafeDiagnostic reduces response material to bounded operational metadata.
type SafeDiagnostic struct {
	StatusCode int
	ErrorClass string
	RetryAfter time.Duration
}

// NewSafeDiagnostic validates response-free operational delivery metadata.
func NewSafeDiagnostic(statusCode int, class string, retryAfter time.Duration) (SafeDiagnostic, error) {
	if statusCode < 0 || statusCode > 599 || len(class) == 0 || len(class) > 64 || strings.ContainsAny(class, " \t\r\n") || retryAfter < 0 || retryAfter > 24*time.Hour {
		return SafeDiagnostic{}, errors.New("delivery: invalid diagnostic")
	}
	return SafeDiagnostic{StatusCode: statusCode, ErrorClass: class, RetryAfter: retryAfter}, nil
}
