package idenqa

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"
)

func TestWebhookVerifierRotationAndWindow(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	oldSecret := []byte("01234567890123456789012345678901")
	newSecret := []byte("abcdefghijklmnopqrstuvwxyzABCDEF")
	verifier, err := NewWebhookVerifier([][]byte{newSecret, oldSecret}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }
	timestamp, eventID, body := "1788264000", "evt_01ARZ3NDEKTSV4RRFFQ69G5FAV", []byte(`{"type":"verification.decision.v1"}`)
	mac := hmac.New(sha256.New, oldSecret)
	_, _ = mac.Write(canonicalWebhook(timestamp, eventID, body))
	if err := verifier.Verify(timestamp, eventID, "v1="+hex.EncodeToString(mac.Sum(nil)), body); err != nil {
		t.Fatal(err)
	}
	if err := verifier.Verify("1788263000", eventID, "v1="+hex.EncodeToString(mac.Sum(nil)), body); err == nil {
		t.Fatal("expired timestamp verified")
	}
	body[0] = '['
	if err := verifier.Verify(timestamp, eventID, "v1="+hex.EncodeToString(mac.Sum(nil)), body); err == nil {
		t.Fatal("mutated body verified")
	}
}
