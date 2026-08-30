package realtime_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/realtime"
)

func TestWireRoundTripsEveryV1Payload(t *testing.T) {
	t.Parallel()

	fixture := newProtocolFixture(t)
	payloads := []realtime.Payload{
		realtime.ClientHello{SDKVersion: "typescript-0.1.0", SupportedVersions: []uint16{1},
			LastAcknowledgedServerSequence: 4, Capabilities: []string{"camera", "nfc"}},
		realtime.ServerWelcome{SelectedVersion: 1, SessionVersion: 2, PingInterval: 15 * time.Second,
			PongTimeout: 10 * time.Second, IdleTimeout: 45 * time.Second},
		realtime.ServerEventAck{ServerSequence: 3},
		realtime.CaptureStepUpdate{State: realtime.StepStarted, RequirementKey: "selfie",
			Artefact: "idenqa.artefact.selfie", AcquisitionMethod: "idenqa.method.live_camera"},
		realtime.CaptureStepUpdate{State: realtime.StepFailed, RequirementKey: "selfie",
			Artefact: "idenqa.artefact.selfie", AcquisitionMethod: "idenqa.method.live_camera", Code: "camera_unavailable"},
		realtime.CaptureStepUpdate{State: realtime.StepCancelled, RequirementKey: "selfie",
			Artefact: "idenqa.artefact.selfie", AcquisitionMethod: "idenqa.method.live_camera", Code: "subject_cancelled"},
		realtime.CaptureCommand{Action: realtime.CommandStart, RequirementKey: "selfie",
			Artefact: "idenqa.artefact.selfie", AcquisitionMethod: "idenqa.method.live_camera"},
		realtime.CaptureCommandResult{Outcome: realtime.CommandCompleted},
		realtime.CaptureCommandResult{Outcome: realtime.CommandFailed, Code: "camera_unavailable"},
		realtime.ChallengeUpdate{Action: realtime.ChallengeRequested, ChallengeID: fixture.challengeID,
			Kind: "turn_head", ExpiresAt: fixture.input.OccurredAt.Add(time.Minute)},
		realtime.ChallengeUpdate{Action: realtime.ChallengeResponded, ChallengeID: fixture.challengeID,
			Outcome: "completed"},
		realtime.ChallengeUpdate{Action: realtime.ChallengeResponded, ChallengeID: fixture.challengeID,
			Outcome: "failed", Code: "challenge_failed"},
		realtime.ChallengeUpdate{Action: realtime.ChallengeCancelled, ChallengeID: fixture.challengeID,
			Code: "expired"},
		realtime.CaptureProgress{CompletedSteps: 1, TotalSteps: 2},
		realtime.VerificationCheckProgress{
			CheckID: fixture.checkID, State: "awaiting_provider", CheckVersion: 3,
		},
		realtime.SessionStateChanged{State: "capture_in_progress", SessionVersion: 2},
		realtime.ResyncRequired{Reason: "cursor_unavailable"},
		realtime.ServerDraining{RetryAfter: 5 * time.Second},
		realtime.CommandAcknowledgement{Disposition: realtime.CommandDispositionAccepted},
		realtime.CommandAcknowledgement{Disposition: realtime.CommandDispositionRejected, Code: "state_conflict"},
	}
	for index, payload := range payloads {
		payload := payload
		t.Run(fmt.Sprintf("%02d_%s", index, reflect.TypeOf(payload)), func(t *testing.T) {
			input := fixture.input
			input.Payload = payload
			if _, hello := payload.(realtime.ClientHello); hello {
				input.ConnectionID = id.Connection{}
			}
			if consequential(payload) {
				input.CommandID = fixture.commandID
			}
			message, err := realtime.NewMessage(input)
			if err != nil {
				t.Fatal(err)
			}

			var encoded []byte
			var decoded realtime.Message
			if clientPayload(payload) {
				encoded, err = realtime.EncodeClientMessage(message)
				if err == nil {
					decoded, err = realtime.DecodeClientMessage(encoded)
				}
			} else {
				encoded, err = realtime.EncodeServerMessage(message)
				if err == nil {
					decoded, err = realtime.DecodeServerMessage(encoded)
				}
			}
			if err != nil {
				t.Fatalf("round trip error = %v; JSON = %s", err, encoded)
			}
			if decoded.Type() != message.Type() || decoded.ID() != message.ID() ||
				decoded.VerificationID() != message.VerificationID() || decoded.ConnectionID() != message.ConnectionID() ||
				decoded.CommandID() != message.CommandID() || !reflect.DeepEqual(decoded.Payload(), message.Payload()) {
				t.Fatalf("decoded message does not match input: %#v", decoded.Payload())
			}
		})
	}
}

func TestWireStrictlyRejectsMalformedOrWrongDirectionJSON(t *testing.T) {
	t.Parallel()

	fixture := newProtocolFixture(t)
	fixture.input.ConnectionID = id.Connection{}
	fixture.input.Payload = realtime.ClientHello{SDKVersion: "typescript-0.1.0", SupportedVersions: []uint16{1}}
	message, err := realtime.NewMessage(fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	valid, err := realtime.EncodeClientMessage(message)
	if err != nil {
		t.Fatal(err)
	}

	tests := map[string][]byte{
		"unknown envelope field":   append(valid[:len(valid)-1], []byte(`,"unexpected":true}`)...),
		"duplicate envelope field": []byte(strings.Replace(string(valid), `"version":1`, `"version":1,"version":1`, 1)),
		"trailing value":           append(append([]byte(nil), valid...), []byte(` {}`)...),
		"null payload":             []byte(strings.Replace(string(valid), `"payload":{"sdk_version":"typescript-0.1.0","supported_versions":[1]}`, `"payload":null`, 1)),
		"array payload":            []byte(strings.Replace(string(valid), `"payload":{"sdk_version":"typescript-0.1.0","supported_versions":[1]}`, `"payload":[]`, 1)),
		"unknown payload field":    []byte(strings.Replace(string(valid), `"supported_versions":[1]`, `"supported_versions":[1],"unexpected":true`, 1)),
		"duplicate payload field":  []byte(strings.Replace(string(valid), `"supported_versions":[1]`, `"supported_versions":[1],"supported_versions":[1]`, 1)),
		"null optional field":      []byte(strings.Replace(string(valid), `"supported_versions":[1]`, `"supported_versions":[1],"capabilities":null`, 1)),
		"non UTC timestamp":        []byte(strings.Replace(string(valid), "Z\"", "+00:00\"", 1)),
	}
	for name, encoded := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := realtime.DecodeClientMessage(encoded); !errors.Is(err, realtime.ErrInvalidWireMessage) {
				t.Fatalf("DecodeClientMessage() error = %v", err)
			}
		})
	}
	if _, err := realtime.DecodeServerMessage(valid); !errors.Is(err, realtime.ErrInvalidWireMessage) {
		t.Fatalf("DecodeServerMessage(client) error = %v", err)
	}
	if _, err := realtime.EncodeServerMessage(message); !errors.Is(err, realtime.ErrInvalidWireMessage) {
		t.Fatalf("EncodeServerMessage(client) error = %v", err)
	}
}

func TestWireEnforcesTypeSpecificOptionalFieldPresence(t *testing.T) {
	t.Parallel()

	fixture := newProtocolFixture(t)
	tests := []struct {
		name        string
		messageType realtime.MessageType
		payload     string
	}{
		{"started code present", realtime.MessageStepStarted,
			`{"requirement_key":"selfie","artefact":"idenqa.artefact.selfie","acquisition_method":"idenqa.method.live_camera","code":""}`},
		{"completed result code present", realtime.MessageCommandResult, `{"outcome":"completed","code":""}`},
		{"challenge response has request field", realtime.MessageChallengeResponse,
			fmt.Sprintf(`{"challenge_id":%q,"outcome":"completed","kind":"turn_head"}`, fixture.challengeID.String())},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := ""
			if consequentialWireType(test.messageType) {
				command = fmt.Sprintf(`,"command_id":%q`, fixture.commandID.String())
			}
			encoded := fmt.Sprintf(`{"version":1,"message_id":%q,"verification_id":%q,"connection_id":%q,"sequence":1%s,"occurred_at":"2026-08-30T12:00:00Z","type":%q,"payload":%s}`,
				fixture.input.ID.String(), fixture.verificationID.String(), fixture.connectionID.String(), command,
				test.messageType, test.payload)
			if _, err := realtime.DecodeClientMessage([]byte(encoded)); !errors.Is(err, realtime.ErrInvalidWireMessage) {
				t.Fatalf("DecodeClientMessage() error = %v", err)
			}
		})
	}
}

func clientPayload(payload realtime.Payload) bool {
	switch value := payload.(type) {
	case realtime.ClientHello, realtime.ServerEventAck, realtime.CaptureStepUpdate,
		realtime.CaptureCommandResult:
		return true
	case realtime.ChallengeUpdate:
		return value.Action == realtime.ChallengeResponded
	default:
		return false
	}
}

func consequentialWireType(messageType realtime.MessageType) bool {
	switch messageType {
	case realtime.MessageStepStarted, realtime.MessageStepFailed, realtime.MessageStepCancelled,
		realtime.MessageCommandResult, realtime.MessageChallengeResponse:
		return true
	default:
		return false
	}
}
