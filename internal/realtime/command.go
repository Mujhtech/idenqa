package realtime

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const commandFingerprintDomain = "idq-realtime-command\x00v1\x00"

// CommandApplication identifies the stable result of applying a client command.
type CommandApplication string

const (
	// CommandApplied means the command effect was durably recorded.
	CommandApplied CommandApplication = "accepted"
	// CommandStateConflict means the command ID was previously bound to different input.
	CommandStateConflict CommandApplication = "state_conflict"
	// CommandAuthorityUnavailable means current capture authority did not permit the command.
	CommandAuthorityUnavailable CommandApplication = "authority_unavailable"
	// CommandPolicyConflict means the immutable capture profile does not permit the step binding.
	CommandPolicyConflict CommandApplication = "policy_conflict"
)

// CommandFingerprint is the connection-independent input identity retained for replay.
type CommandFingerprint [sha256.Size]byte

// ParseCommandFingerprint validates stored command fingerprint material.
func ParseCommandFingerprint(value []byte) (CommandFingerprint, error) {
	if len(value) != sha256.Size {
		return CommandFingerprint{}, errors.New("realtime: command fingerprint must contain 32 bytes")
	}
	var fingerprint CommandFingerprint
	copy(fingerprint[:], value)

	return fingerprint, nil
}

// Bytes returns a defensive persistence copy.
func (fingerprint CommandFingerprint) Bytes() []byte {
	return append([]byte(nil), fingerprint[:]...)
}

// CaptureStepCommand is one validated, connection-independent capture-step command.
type CaptureStepCommand struct {
	tenantID       id.Tenant
	verificationID id.Verification
	captureTokenID id.CaptureToken
	commandID      id.Command
	messageType    MessageType
	payload        CaptureStepUpdate
	fingerprint    CommandFingerprint
	occurredAt     time.Time
	receivedAt     time.Time
}

// NewCaptureStepCommand constructs the durable identity of a client step update.
func NewCaptureStepCommand(ticket Ticket, message Message, receivedAt time.Time) (CaptureStepCommand, error) {
	payload, ok := message.Payload().(CaptureStepUpdate)
	if !ok || ticket.TenantID().IsZero() || ticket.VerificationID().IsZero() ||
		ticket.CaptureTokenID().IsZero() || ticket.ConnectionID().IsZero() ||
		message.CommandID().IsZero() || message.VerificationID() != ticket.VerificationID() ||
		message.ConnectionID() != ticket.ConnectionID() || receivedAt.IsZero() ||
		receivedAt.Location() != time.UTC {
		return CaptureStepCommand{}, errors.New("realtime: capture step command binding is invalid")
	}
	canonical, err := json.Marshal(struct {
		VerificationID    string      `json:"verification_id"`
		CaptureTokenID    string      `json:"capture_token_id"`
		MessageType       MessageType `json:"message_type"`
		State             StepState   `json:"state"`
		RequirementKey    string      `json:"requirement_key"`
		Artefact          string      `json:"artefact"`
		AcquisitionMethod string      `json:"acquisition_method"`
		Code              string      `json:"code"`
	}{
		VerificationID: ticket.VerificationID().String(), CaptureTokenID: ticket.CaptureTokenID().String(),
		MessageType: message.Type(), State: payload.State, RequirementKey: payload.RequirementKey,
		Artefact: payload.Artefact, AcquisitionMethod: payload.AcquisitionMethod, Code: payload.Code,
	})
	if err != nil {
		return CaptureStepCommand{}, fmt.Errorf("realtime: encode command fingerprint input: %w", err)
	}
	fingerprint := sha256.Sum256(append([]byte(commandFingerprintDomain), canonical...))

	return CaptureStepCommand{
		tenantID: ticket.TenantID(), verificationID: ticket.VerificationID(),
		captureTokenID: ticket.CaptureTokenID(), commandID: message.CommandID(),
		messageType: message.Type(), payload: payload, fingerprint: fingerprint,
		occurredAt: message.OccurredAt(), receivedAt: receivedAt,
	}, nil
}

// TenantID returns the authenticated tenant binding.
func (command CaptureStepCommand) TenantID() id.Tenant { return command.tenantID }

// VerificationID returns the immutable session binding.
func (command CaptureStepCommand) VerificationID() id.Verification { return command.verificationID }

// CaptureTokenID returns the non-secret capture-principal binding.
func (command CaptureStepCommand) CaptureTokenID() id.CaptureToken { return command.captureTokenID }

// CommandID returns the stable client-generated command identity.
func (command CaptureStepCommand) CommandID() id.Command { return command.commandID }

// MessageType returns the exact v1 step transition type.
func (command CaptureStepCommand) MessageType() MessageType { return command.messageType }

// Payload returns the safe capture-step metadata.
func (command CaptureStepCommand) Payload() CaptureStepUpdate { return command.payload }

// Fingerprint returns the connection-independent command input identity.
func (command CaptureStepCommand) Fingerprint() CommandFingerprint { return command.fingerprint }

// OccurredAt returns the client-declared UTC occurrence time.
func (command CaptureStepCommand) OccurredAt() time.Time { return command.occurredAt }

// ReceivedAt returns the server receipt time used for authority decisions.
func (command CaptureStepCommand) ReceivedAt() time.Time { return command.receivedAt }

// CaptureStepCommandRepository atomically deduplicates and applies step activity.
type CaptureStepCommandRepository interface {
	ApplyCaptureStep(context.Context, Ticket, CaptureStepCommand) (CommandApplication, error)
}

// CommandService applies the supported client command catalogue.
type CommandService struct {
	repository CaptureStepCommandRepository
	clock      clock.Clock
}

// NewCommandService constructs the realtime client-command application service.
func NewCommandService(repository CaptureStepCommandRepository, source clock.Clock) (*CommandService, error) {
	if repository == nil || source == nil {
		return nil, errors.New("realtime: command service dependencies are required")
	}

	return &CommandService{repository: repository, clock: source}, nil
}

// HandleClientCommand durably applies supported commands and rejects all others.
func (service *CommandService) HandleClientCommand(
	ctx context.Context,
	ticket Ticket,
	message Message,
) (CommandAcknowledgement, error) {
	if service == nil || service.repository == nil || service.clock == nil {
		return CommandAcknowledgement{}, errors.New("realtime: command service is not initialised")
	}
	if _, ok := message.Payload().(CaptureStepUpdate); !ok {
		return rejectedCommand("not_supported"), nil
	}
	command, err := NewCaptureStepCommand(ticket, message, service.clock.Now().UTC())
	if err != nil {
		return CommandAcknowledgement{}, err
	}
	application, err := service.repository.ApplyCaptureStep(ctx, ticket, command)
	if err != nil {
		return CommandAcknowledgement{}, fmt.Errorf("apply capture step command: %w", err)
	}
	switch application {
	case CommandApplied:
		return CommandAcknowledgement{Disposition: CommandDispositionAccepted}, nil
	case CommandStateConflict, CommandAuthorityUnavailable, CommandPolicyConflict:
		return rejectedCommand(string(application)), nil
	default:
		return CommandAcknowledgement{}, errors.New("realtime: command repository returned an invalid application result")
	}
}

func rejectedCommand(code string) CommandAcknowledgement {
	return CommandAcknowledgement{Disposition: CommandDispositionRejected, Code: code}
}
