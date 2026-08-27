package outbox_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/outbox"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestNewIntentRequiresObjectPayloadAndStableMetadata(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	generator, err := id.NewGenerator(fixedClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{1}, 64)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	eventID, err := generator.NewEvent()
	if err != nil {
		t.Fatalf("NewEvent() error = %v", err)
	}
	intent, err := outbox.NewIntent(
		eventID,
		"verification",
		"ver_01K00000000000000000000000",
		1,
		"verification.created",
		1,
		struct {
			ProfileID string `json:"profile_id"`
		}{ProfileID: "prf_01K00000000000000000000000"},
		now,
	)
	if err != nil {
		t.Fatalf("NewIntent() error = %v", err)
	}
	if intent.EventType != "verification.created" || len(intent.Payload) == 0 {
		t.Fatalf("intent = %+v", intent)
	}

	if _, err := outbox.NewIntent(eventID, "Verification", "ver_1", 1, "verification.created", 1, struct{}{}, now); err == nil {
		t.Fatal("NewIntent() accepted invalid aggregate type")
	}
	if _, err := outbox.NewIntent(eventID, "verification", "ver_1", 1, "verification.created", 1, "secret", now); err == nil {
		t.Fatal("NewIntent() accepted a scalar payload")
	}
}
