package realtime_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/realtime"
)

func TestEventIntentAllowsOnlySafeReplayPayloads(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 30, 14, 0, 0, 0, time.UTC)
	eventID, _ := id.ParseEvent("evt_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	verificationID, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAX")
	commandID, _ := id.ParseCommand("cmd_01ARZ3NDEKTSV4RRFFQ69G5FAY")

	tests := []struct {
		name      string
		payload   realtime.Payload
		commandID id.Command
		wantError bool
	}{
		{name: "progress", payload: realtime.CaptureProgress{CompletedSteps: 1, TotalSteps: 2}},
		{name: "state", payload: realtime.SessionStateChanged{State: "processing", SessionVersion: 2}},
		{name: "server command", payload: realtime.CaptureCommand{Action: realtime.CommandRetry, RequirementKey: "selfie", Artefact: "idenqa.artefact.selfie_image"}, commandID: commandID},
		{name: "challenge request", payload: realtime.ChallengeUpdate{Action: realtime.ChallengeRequested, ChallengeID: mustChallenge(t), Kind: "turn_head", ExpiresAt: now.Add(time.Minute)}, commandID: commandID},
		{name: "transport welcome", payload: realtime.ServerWelcome{SelectedVersion: 1, SessionVersion: 1, PingInterval: 15 * time.Second, PongTimeout: 5 * time.Second, IdleTimeout: time.Minute}, wantError: true},
		{name: "client result", payload: realtime.CaptureStepUpdate{State: realtime.StepStarted, RequirementKey: "selfie", Artefact: "idenqa.artefact.selfie_image", AcquisitionMethod: "idenqa.method.live_camera"}, commandID: commandID, wantError: true},
		{name: "command without identity", payload: realtime.CaptureCommand{Action: realtime.CommandRetry, RequirementKey: "selfie", Artefact: "idenqa.artefact.selfie_image"}, wantError: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			intent, err := realtime.NewEventIntent(
				eventID, tenantID, verificationID, test.commandID, id.Message{}, id.Message{},
				now, now.Add(time.Hour), test.payload,
			)
			if test.wantError {
				if !errors.Is(err, realtime.ErrInvalidDurableEvent) {
					t.Fatalf("NewEventIntent() error = %v", err)
				}
				return
			}
			if err != nil || intent.Type() != payloadMessageType(test.payload) {
				t.Fatalf("NewEventIntent() = %#v, %v", intent, err)
			}
		})
	}
}

func TestReplayWindowEnforcesContiguousPagesAndGaps(t *testing.T) {
	t.Parallel()

	events := []realtime.DurableEvent{durableEvent(t, 3), durableEvent(t, 4)}
	window, err := realtime.NewReplayWindow(2, 5, 1, events, true)
	if err != nil || window.Gap() || !window.HasMore() || len(window.Events()) != 2 {
		t.Fatalf("NewReplayWindow() = %#v, %v", window, err)
	}
	events[0] = realtime.DurableEvent{}
	if window.Events()[0].Cursor() != 3 {
		t.Fatal("replay window retained caller-owned slice")
	}

	gap, err := realtime.NewReplayWindow(1, 7, 4, nil, false)
	if err != nil || !gap.Gap() || gap.RetainedFrom() != 4 {
		t.Fatalf("gap window = %#v, %v", gap, err)
	}
	if _, err := realtime.NewReplayWindow(2, 5, 1, []realtime.DurableEvent{durableEvent(t, 4)}, false); !errors.Is(err, realtime.ErrInvalidDurableEvent) {
		t.Fatalf("non-contiguous error = %v", err)
	}
	if _, err := realtime.NewReplayWindow(6, 5, 1, nil, false); !errors.Is(err, realtime.ErrReplayCursorAhead) {
		t.Fatalf("ahead error = %v", err)
	}
}

func TestValidateReplayLimit(t *testing.T) {
	t.Parallel()

	for _, limit := range []uint16{1, 64, 256} {
		if err := realtime.ValidateReplayLimit(limit); err != nil {
			t.Fatalf("ValidateReplayLimit(%d) = %v", limit, err)
		}
	}
	for _, limit := range []uint16{0, 257} {
		if err := realtime.ValidateReplayLimit(limit); err == nil {
			t.Fatalf("ValidateReplayLimit(%d) accepted", limit)
		}
	}
}

func durableEvent(t *testing.T, cursor realtime.EventCursor) realtime.DurableEvent {
	t.Helper()
	now := time.Date(2026, time.August, 30, 14, 0, 0, 0, time.UTC)
	eventID, _ := id.ParseEvent("evt_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	verificationID, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAX")
	intent, err := realtime.NewEventIntent(eventID, tenantID, verificationID, id.Command{}, id.Message{}, id.Message{}, now, now.Add(time.Hour), realtime.CaptureProgress{TotalSteps: 1})
	if err != nil {
		t.Fatal(err)
	}
	event, err := realtime.RestoreDurableEvent(intent, cursor)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func mustChallenge(t *testing.T) id.Challenge {
	t.Helper()
	value, err := id.ParseChallenge("chl_01ARZ3NDEKTSV4RRFFQ69G5FAZ")
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func payloadMessageType(payload realtime.Payload) realtime.MessageType {
	switch payload.(type) {
	case realtime.CaptureProgress:
		return realtime.MessageCaptureProgress
	case realtime.SessionStateChanged:
		return realtime.MessageSessionState
	case realtime.CaptureCommand:
		return realtime.MessageCaptureCommand
	case realtime.ChallengeUpdate:
		return realtime.MessageChallengeRequest
	default:
		return ""
	}
}
