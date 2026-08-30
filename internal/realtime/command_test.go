package realtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/realtime"
)

type commandRepositoryStub struct {
	application realtime.CommandApplication
	commands    []realtime.CaptureStepCommand
}

func (repository *commandRepositoryStub) ApplyCaptureStep(
	_ context.Context,
	_ realtime.Ticket,
	command realtime.CaptureStepCommand,
) (realtime.CommandApplication, error) {
	repository.commands = append(repository.commands, command)

	return repository.application, nil
}

func TestCommandServiceAppliesOnlyCaptureStepUpdates(t *testing.T) {
	t.Parallel()

	fixture := newTicketFixture(t)
	ticket := redeemedCommandTicket(t, fixture)
	commandID, err := fixture.identifiers.NewCommand()
	if err != nil {
		t.Fatal(err)
	}
	message := captureStepMessage(t, fixture, ticket, commandID, realtime.StepStarted, "")
	repository := &commandRepositoryStub{application: realtime.CommandApplied}
	service, err := realtime.NewCommandService(repository, fixedClock{now: fixture.now})
	if err != nil {
		t.Fatal(err)
	}
	acknowledgement, err := service.HandleClientCommand(t.Context(), ticket, message)
	if err != nil {
		t.Fatal(err)
	}
	if acknowledgement.Disposition != realtime.CommandDispositionAccepted || len(repository.commands) != 1 {
		t.Fatalf("acknowledgement = %#v, commands = %d", acknowledgement, len(repository.commands))
	}
	unsupportedCommandID, err := fixture.identifiers.NewCommand()
	if err != nil {
		t.Fatal(err)
	}
	unsupportedMessageID, err := fixture.identifiers.NewMessage()
	if err != nil {
		t.Fatal(err)
	}
	unsupportedMessage, err := realtime.NewMessage(realtime.MessageInput{
		Version: realtime.ProtocolVersionV1, ID: unsupportedMessageID,
		VerificationID: ticket.VerificationID(), ConnectionID: ticket.ConnectionID(),
		Sequence: 3, CommandID: unsupportedCommandID, OccurredAt: fixture.now,
		Payload: realtime.CaptureCommandResult{Outcome: realtime.CommandCompleted},
	})
	if err != nil {
		t.Fatal(err)
	}
	rejected, err := service.HandleClientCommand(t.Context(), ticket, unsupportedMessage)
	if err != nil {
		t.Fatal(err)
	}
	if rejected.Disposition != realtime.CommandDispositionRejected || rejected.Code != "not_supported" ||
		len(repository.commands) != 1 {
		t.Fatalf("unsupported acknowledgement = %#v, commands = %d", rejected, len(repository.commands))
	}
}

func TestCommandServiceMapsStableRejections(t *testing.T) {
	t.Parallel()

	fixture := newTicketFixture(t)
	ticket := redeemedCommandTicket(t, fixture)
	for _, application := range []realtime.CommandApplication{
		realtime.CommandStateConflict,
		realtime.CommandAuthorityUnavailable,
		realtime.CommandPolicyConflict,
	} {
		t.Run(string(application), func(t *testing.T) {
			commandID, err := fixture.identifiers.NewCommand()
			if err != nil {
				t.Fatal(err)
			}
			repository := &commandRepositoryStub{application: application}
			service, err := realtime.NewCommandService(repository, fixedClock{now: fixture.now})
			if err != nil {
				t.Fatal(err)
			}
			acknowledgement, err := service.HandleClientCommand(
				t.Context(), ticket,
				captureStepMessage(t, fixture, ticket, commandID, realtime.StepFailed, "camera_unavailable"),
			)
			if err != nil {
				t.Fatal(err)
			}
			if acknowledgement.Disposition != realtime.CommandDispositionRejected ||
				acknowledgement.Code != string(application) {
				t.Fatalf("acknowledgement = %#v", acknowledgement)
			}
		})
	}
}

func TestCaptureStepFingerprintSurvivesReconnectButBindsInput(t *testing.T) {
	t.Parallel()

	fixture := newTicketFixture(t)
	firstTicket := redeemedCommandTicket(t, fixture)
	secondConnectionID, err := fixture.identifiers.NewConnection()
	if err != nil {
		t.Fatal(err)
	}
	redeemedAt := fixture.now.Add(time.Second)
	secondTicket, err := realtime.RestoreTicket(
		firstTicket.ID(), firstTicket.TenantID(), firstTicket.VerificationID(), firstTicket.CaptureTokenID(),
		firstTicket.Digest(), firstTicket.Binding(), firstTicket.Region(), firstTicket.Protocol(),
		firstTicket.IssuedAt(), firstTicket.ExpiresAt(), &redeemedAt, secondConnectionID,
	)
	if err != nil {
		t.Fatal(err)
	}
	commandID, err := fixture.identifiers.NewCommand()
	if err != nil {
		t.Fatal(err)
	}
	first, err := realtime.NewCaptureStepCommand(
		firstTicket,
		captureStepMessage(t, fixture, firstTicket, commandID, realtime.StepStarted, ""),
		fixture.now,
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := realtime.NewCaptureStepCommand(
		secondTicket,
		captureStepMessage(t, fixture, secondTicket, commandID, realtime.StepStarted, ""),
		fixture.now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := realtime.NewCaptureStepCommand(
		secondTicket,
		captureStepMessage(t, fixture, secondTicket, commandID, realtime.StepFailed, "capture_failed"),
		fixture.now.Add(time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint() != second.Fingerprint() || first.Fingerprint() == changed.Fingerprint() {
		t.Fatal("command fingerprint does not preserve reconnect replay and input binding")
	}
}

func redeemedCommandTicket(t *testing.T, fixture ticketFixture) realtime.Ticket {
	t.Helper()
	ticket := fixture.newTicket(t, fixture.now.Add(-time.Second), fixture.now.Add(29*time.Second))
	connectionID, err := fixture.identifiers.NewConnection()
	if err != nil {
		t.Fatal(err)
	}
	redeemedAt := fixture.now
	ticket, err = realtime.RestoreTicket(
		ticket.ID(), ticket.TenantID(), ticket.VerificationID(), ticket.CaptureTokenID(), ticket.Digest(),
		ticket.Binding(), ticket.Region(), ticket.Protocol(), ticket.IssuedAt(), ticket.ExpiresAt(),
		&redeemedAt, connectionID,
	)
	if err != nil {
		t.Fatal(err)
	}

	return ticket
}

func captureStepMessage(
	t *testing.T,
	fixture ticketFixture,
	ticket realtime.Ticket,
	commandID id.Command,
	state realtime.StepState,
	code string,
) realtime.Message {
	t.Helper()
	messageID, err := fixture.identifiers.NewMessage()
	if err != nil {
		t.Fatal(err)
	}
	message, err := realtime.NewMessage(realtime.MessageInput{
		Version: realtime.ProtocolVersionV1, ID: messageID, VerificationID: ticket.VerificationID(),
		ConnectionID: ticket.ConnectionID(), Sequence: 2, CommandID: commandID, OccurredAt: fixture.now,
		Payload: realtime.CaptureStepUpdate{
			State: state, RequirementKey: "selfie", Artefact: "idenqa.artefact.selfie",
			AcquisitionMethod: "idenqa.method.live_camera", Code: code,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	return message
}
