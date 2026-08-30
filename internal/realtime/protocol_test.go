package realtime_test

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/realtime"
)

type fixedClock struct{ now time.Time }

func (source fixedClock) Now() time.Time { return source.now }

type protocolFixture struct {
	input          realtime.MessageInput
	verificationID id.Verification
	connectionID   id.Connection
	commandID      id.Command
	challengeID    id.Challenge
	checkID        id.Check
}

func newProtocolFixture(t *testing.T) protocolFixture {
	t.Helper()

	now := time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)
	generator, err := id.NewGenerator(fixedClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{7}, 256)))
	if err != nil {
		t.Fatal(err)
	}
	messageID, err := generator.NewMessage()
	if err != nil {
		t.Fatal(err)
	}
	verificationID, err := generator.NewVerification()
	if err != nil {
		t.Fatal(err)
	}
	connectionID, err := generator.NewConnection()
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := generator.NewCommand()
	if err != nil {
		t.Fatal(err)
	}
	challengeID, err := generator.NewChallenge()
	if err != nil {
		t.Fatal(err)
	}
	checkID, err := generator.NewCheck()
	if err != nil {
		t.Fatal(err)
	}

	return protocolFixture{
		input: realtime.MessageInput{
			Version: realtime.ProtocolVersionV1, ID: messageID, VerificationID: verificationID,
			ConnectionID: connectionID, Sequence: 1, OccurredAt: now,
		},
		verificationID: verificationID, connectionID: connectionID,
		commandID: commandID, challengeID: challengeID, checkID: checkID,
	}
}

func TestProtocolAcceptsEveryV1Payload(t *testing.T) {
	t.Parallel()

	fixture := newProtocolFixture(t)
	payloads := []realtime.Payload{
		realtime.ClientHello{SDKVersion: "typescript-0.1.0", SupportedVersions: []uint16{1}, Capabilities: []string{"camera"}},
		realtime.ServerWelcome{SelectedVersion: 1, SessionVersion: 1, PingInterval: 15 * time.Second, PongTimeout: 10 * time.Second, IdleTimeout: 45 * time.Second},
		realtime.ServerEventAck{ServerSequence: 1},
		realtime.CaptureStepUpdate{State: realtime.StepStarted, RequirementKey: "selfie", Artefact: "idenqa.artefact.selfie", AcquisitionMethod: "idenqa.method.live_camera"},
		realtime.CaptureStepUpdate{State: realtime.StepFailed, RequirementKey: "selfie", Artefact: "idenqa.artefact.selfie", AcquisitionMethod: "idenqa.method.live_camera", Code: "camera_unavailable"},
		realtime.CaptureStepUpdate{State: realtime.StepCancelled, RequirementKey: "selfie", Artefact: "idenqa.artefact.selfie", AcquisitionMethod: "idenqa.method.live_camera", Code: "subject_cancelled"},
		realtime.CaptureCommand{Action: realtime.CommandStart, RequirementKey: "selfie", Artefact: "idenqa.artefact.selfie", AcquisitionMethod: "idenqa.method.live_camera"},
		realtime.CaptureCommandResult{Outcome: realtime.CommandCompleted},
		realtime.ChallengeUpdate{Action: realtime.ChallengeRequested, ChallengeID: fixture.challengeID, Kind: "turn_head", ExpiresAt: fixture.input.OccurredAt.Add(time.Minute)},
		realtime.ChallengeUpdate{Action: realtime.ChallengeResponded, ChallengeID: fixture.challengeID, Outcome: "completed"},
		realtime.ChallengeUpdate{Action: realtime.ChallengeCancelled, ChallengeID: fixture.challengeID, Code: "expired"},
		realtime.CaptureProgress{CompletedSteps: 1, TotalSteps: 2},
		realtime.VerificationCheckProgress{CheckID: fixture.checkID, State: "running", CheckVersion: 2},
		realtime.SessionStateChanged{State: "capture_in_progress", SessionVersion: 2},
		realtime.ResyncRequired{Reason: "cursor_unavailable"},
		realtime.ServerDraining{RetryAfter: 5 * time.Second},
		realtime.CommandAcknowledgement{Disposition: realtime.CommandDispositionAccepted},
		realtime.CommandAcknowledgement{Disposition: realtime.CommandDispositionRejected, Code: "state_conflict"},
	}
	for _, payload := range payloads {
		payload := payload
		t.Run(reflect.TypeOf(payload).String(), func(t *testing.T) {
			input := fixture.input
			input.Payload = payload
			if consequential(payload) {
				input.CommandID = fixture.commandID
			}
			message, err := realtime.NewMessage(input)
			if err != nil {
				t.Fatalf("NewMessage() error = %v", err)
			}
			if message.VerificationID() != fixture.verificationID || message.Sequence() != 1 {
				t.Fatalf("NewMessage() lost envelope binding")
			}
		})
	}
}

func TestMessageRejectsInvalidEnvelopeAndPayload(t *testing.T) {
	t.Parallel()

	fixture := newProtocolFixture(t)
	tests := []struct {
		name   string
		mutate func(*realtime.MessageInput)
	}{
		{"wrong version", func(input *realtime.MessageInput) { input.Version = 2 }},
		{"zero sequence", func(input *realtime.MessageInput) { input.Sequence = 0 }},
		{"non UTC timestamp", func(input *realtime.MessageInput) {
			input.OccurredAt = input.OccurredAt.In(time.FixedZone("test", 3600))
		}},
		{"typed pointer payload", func(input *realtime.MessageInput) {
			input.Payload = &realtime.CaptureProgress{CompletedSteps: 1, TotalSteps: 1}
		}},
		{"invalid payload", func(input *realtime.MessageInput) {
			input.Payload = realtime.CaptureProgress{CompletedSteps: 2, TotalSteps: 1}
		}},
		{"duplicate hello version", func(input *realtime.MessageInput) {
			input.ConnectionID = id.Connection{}
			input.Payload = realtime.ClientHello{SDKVersion: "typescript-0.1.0", SupportedVersions: []uint16{1, 1}}
		}},
		{"heartbeat outside bounds", func(input *realtime.MessageInput) {
			input.Payload = realtime.ServerWelcome{SelectedVersion: 1, SessionVersion: 1, PingInterval: time.Second, PongTimeout: 10 * time.Second, IdleTimeout: 45 * time.Second}
		}},
		{"missing command", func(input *realtime.MessageInput) {
			input.Payload = realtime.CaptureCommandResult{Outcome: realtime.CommandCompleted}
		}},
		{"extra command", func(input *realtime.MessageInput) {
			input.Payload = realtime.CaptureProgress{CompletedSteps: 1, TotalSteps: 1}
			input.CommandID = fixture.commandID
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := fixture.input
			input.Payload = realtime.CaptureProgress{CompletedSteps: 1, TotalSteps: 1}
			test.mutate(&input)
			if _, err := realtime.NewMessage(input); !errors.Is(err, realtime.ErrInvalidMessage) {
				t.Fatalf("NewMessage() error = %v", err)
			}
		})
	}
}

func TestMessageDefensivelyCopiesClientHello(t *testing.T) {
	t.Parallel()

	fixture := newProtocolFixture(t)
	versions := []uint16{1}
	capabilities := []string{"camera"}
	fixture.input.Payload = realtime.ClientHello{SDKVersion: "typescript-0.1.0", SupportedVersions: versions, Capabilities: capabilities}
	fixture.input.ConnectionID = id.Connection{}
	message, err := realtime.NewMessage(fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	versions[0] = 9
	capabilities[0] = "mutated"
	first := message.Payload().(realtime.ClientHello)
	first.Capabilities[0] = "also-mutated"
	second := message.Payload().(realtime.ClientHello)
	if second.SupportedVersions[0] != 1 || second.Capabilities[0] != "camera" {
		t.Fatalf("Payload() = %#v", second)
	}
}

func TestV1PayloadsContainNoRawByteFields(t *testing.T) {
	t.Parallel()

	types := []reflect.Type{
		reflect.TypeFor[realtime.ClientHello](), reflect.TypeFor[realtime.ServerWelcome](),
		reflect.TypeFor[realtime.ServerEventAck](), reflect.TypeFor[realtime.CaptureStepUpdate](),
		reflect.TypeFor[realtime.CaptureCommand](), reflect.TypeFor[realtime.CaptureCommandResult](),
		reflect.TypeFor[realtime.ChallengeUpdate](), reflect.TypeFor[realtime.CaptureProgress](),
		reflect.TypeFor[realtime.SessionStateChanged](), reflect.TypeFor[realtime.ResyncRequired](),
		reflect.TypeFor[realtime.ServerDraining](), reflect.TypeFor[realtime.CommandAcknowledgement](),
	}
	byteSlice := reflect.TypeFor[[]byte]()
	for _, payloadType := range types {
		for index := range payloadType.NumField() {
			if payloadType.Field(index).Type == byteSlice {
				t.Fatalf("%s.%s carries raw bytes", payloadType, payloadType.Field(index).Name)
			}
		}
	}
}

func consequential(payload realtime.Payload) bool {
	switch payload.(type) {
	case realtime.CaptureStepUpdate, realtime.CaptureCommand, realtime.CaptureCommandResult,
		realtime.ChallengeUpdate, realtime.CommandAcknowledgement:
		return true
	default:
		return false
	}
}
