package idenqa

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"
)

// Public v1 signature headers and the bounded webhook body limit.
const (
	WebhookSignatureHeader = "Idenqa-Signature"
	WebhookTimestampHeader = "Idenqa-Timestamp"
	WebhookEventIDHeader   = "Idenqa-Event-ID"
	MaximumWebhookBody     = 1 << 20
)

// ErrInvalidWebhook reports an invalid signature, envelope or verifier configuration.
var ErrInvalidWebhook = errors.New("idenqa: invalid webhook signature")

// WebhookVerifier accepts active and overlapping rotation secrets.
type WebhookVerifier struct {
	secrets   [][]byte
	tolerance time.Duration
	now       func() time.Time
}

// NewWebhookVerifier constructs a verifier with active and optional overlap secrets.
func NewWebhookVerifier(secrets [][]byte, tolerance time.Duration) (*WebhookVerifier, error) {
	if len(secrets) == 0 || len(secrets) > 3 || tolerance <= 0 || tolerance > 24*time.Hour {
		return nil, ErrInvalidWebhook
	}
	cloned := make([][]byte, len(secrets))
	for index, secret := range secrets {
		if len(secret) < 32 {
			return nil, ErrInvalidWebhook
		}
		cloned[index] = append([]byte(nil), secret...)
	}
	return &WebhookVerifier{secrets: cloned, tolerance: tolerance, now: time.Now}, nil
}

// Verify validates timestamp window and any v1 signature in constant time.
func (verifier *WebhookVerifier) Verify(timestamp, eventID, signature string, body []byte) error {
	if verifier == nil || len(body) > MaximumWebhookBody || eventID == "" || strings.ContainsAny(eventID, "\r\n") {
		return ErrInvalidWebhook
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrInvalidWebhook
	}
	observed := time.Unix(seconds, 0)
	delta := verifier.now().Sub(observed)
	if delta < 0 {
		delta = -delta
	}
	if delta > verifier.tolerance {
		return ErrInvalidWebhook
	}
	provided, err := parseSignatures(signature)
	if err != nil {
		return ErrInvalidWebhook
	}
	message := canonicalWebhook(timestamp, eventID, body)
	valid := false
	for _, secret := range verifier.secrets {
		mac := hmac.New(sha256.New, secret)
		_, _ = mac.Write(message)
		expected := mac.Sum(nil)
		for _, candidate := range provided {
			valid = hmac.Equal(expected, candidate) || valid
		}
	}
	if !valid {
		return ErrInvalidWebhook
	}
	return nil
}

func canonicalWebhook(timestamp, eventID string, body []byte) []byte {
	result := make([]byte, 0, len(timestamp)+len(eventID)+len(body)+2)
	result = append(result, timestamp...)
	result = append(result, '\n')
	result = append(result, eventID...)
	result = append(result, '\n')
	return append(result, body...)
}

func parseSignatures(value string) ([][]byte, error) {
	var result [][]byte
	for _, part := range strings.Split(value, ",") {
		version, encoded, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || version != "v1" {
			continue
		}
		decoded, err := hex.DecodeString(encoded)
		if err != nil || len(decoded) != sha256.Size {
			return nil, ErrInvalidWebhook
		}
		result = append(result, decoded)
	}
	if len(result) == 0 {
		return nil, ErrInvalidWebhook
	}
	return result, nil
}
