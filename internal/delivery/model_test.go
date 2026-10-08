package delivery_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
)

func TestNewIntentRequiresWrappedBody(t *testing.T) {
	t.Parallel()
	_, err := delivery.NewIntent(
		mustDelivery(t, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV"),
		mustEndpoint(t),
		mustEvent(t, "evt_01ARZ3NDEKTSV4RRFFQ69G5FAV"),
		"verification.completed", kms.WrappedKey{}, 8,
		time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
	)
	if !errors.Is(err, delivery.ErrInvalid) {
		t.Fatalf("unwrapped intent: %v", err)
	}
}

func TestIntentRejectsMissingOrMismatchedWrapping(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*delivery.Intent)
	}{
		{name: "missing metadata", mutate: func(intent *delivery.Intent) { intent.BodyWrapping = nil }},
		{name: "empty metadata", mutate: func(intent *delivery.Intent) { intent.BodyWrapping = &kms.WrappedKey{} }},
		{name: "different ciphertext with valid digest", mutate: func(intent *delivery.Intent) {
			intent.Body = []byte("different ciphertext")
			digest := sha256.Sum256(intent.Body)
			intent.BodyDigest = hex.EncodeToString(digest[:])
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			intent, err := delivery.NewIntent(
				mustDelivery(t, "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV"),
				mustEndpoint(t),
				mustEvent(t, "evt_01ARZ3NDEKTSV4RRFFQ69G5FAV"),
				"verification.completed", wrappedBody(t, []byte("ciphertext")), 8,
				time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC),
			)
			if err != nil || intent.Validate() != nil {
				t.Fatalf("valid wrapped intent: %v", err)
			}
			test.mutate(&intent)
			if !errors.Is(intent.Validate(), delivery.ErrInvalid) {
				t.Fatal("accepted invalid wrapping")
			}
		})
	}
}
