package realtime

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// ClientHello advertises client version and safe capture capabilities.
type ClientHello struct {
	SDKVersion                     string
	SupportedVersions              []uint16
	LastAcknowledgedServerSequence uint64
	LastAcknowledgedEventCursor    EventCursor
	Capabilities                   []string
}

func (ClientHello) messageType() MessageType { return MessageClientHello }
func (payload ClientHello) validate() error {
	if !safeToken(payload.SDKVersion, 64) || len(payload.SupportedVersions) == 0 ||
		len(payload.SupportedVersions) > 8 || !slices.Contains(payload.SupportedVersions, ProtocolVersionV1) ||
		!uniqueVersions(payload.SupportedVersions) || len(payload.Capabilities) > 32 ||
		!uniqueSafeTokens(payload.Capabilities, 64) {
		return errors.New("client hello is invalid")
	}

	return nil
}

// ServerWelcome confirms the selected protocol and heartbeat policy.
type ServerWelcome struct {
	SelectedVersion uint16
	SessionVersion  int64
	PingInterval    time.Duration
	PongTimeout     time.Duration
	IdleTimeout     time.Duration
}

func (ServerWelcome) messageType() MessageType { return MessageServerWelcome }
func (payload ServerWelcome) validate() error {
	if payload.SelectedVersion != ProtocolVersionV1 || payload.SessionVersion < 1 ||
		payload.PingInterval < 5*time.Second || payload.PingInterval > 30*time.Second ||
		payload.PongTimeout < 5*time.Second || payload.PongTimeout > 15*time.Second ||
		payload.IdleTimeout < payload.PingInterval+payload.PongTimeout || payload.IdleTimeout > 2*time.Minute {
		return errors.New("server welcome is invalid")
	}

	return nil
}

// ServerEventAck acknowledges a connection-local server sequence and, when
// present, the durable event represented by that message.
type ServerEventAck struct {
	ServerSequence uint64
	EventCursor    EventCursor
}

func (ServerEventAck) messageType() MessageType { return MessageServerEventAck }
func (payload ServerEventAck) validate() error {
	if payload.ServerSequence == 0 {
		return errors.New("server event acknowledgement is invalid")
	}

	return nil
}

// StepState identifies a client-reported capture-step transition.
type StepState string

const (
	// StepStarted begins the closed set of capture-step states.
	StepStarted StepState = "started"
	// StepFailed reports a failed capture step.
	StepFailed StepState = "failed"
	// StepCancelled reports a cancelled capture step.
	StepCancelled StepState = "cancelled"
)

// CaptureStepUpdate reports a policy-bound local capture-step transition.
type CaptureStepUpdate struct {
	State             StepState
	RequirementKey    string
	Artefact          string
	AcquisitionMethod string
	Code              string
}

func (payload CaptureStepUpdate) messageType() MessageType {
	switch payload.State {
	case StepStarted:
		return MessageStepStarted
	case StepFailed:
		return MessageStepFailed
	case StepCancelled:
		return MessageStepCancelled
	default:
		return ""
	}
}

func (payload CaptureStepUpdate) validate() error {
	if payload.messageType() == "" || !requirementKey(payload.RequirementKey) ||
		!safeName(payload.Artefact) || !safeName(payload.AcquisitionMethod) {
		return errors.New("capture step update is invalid")
	}
	if payload.State == StepStarted && payload.Code != "" {
		return errors.New("started capture step cannot contain a result code")
	}
	if payload.State != StepStarted && !safeToken(payload.Code, 100) {
		return errors.New("terminal capture step requires a safe code")
	}

	return nil
}

// CommandAction identifies a server-requested capture action.
type CommandAction string

const (
	// CommandStart begins the closed set of capture command actions.
	CommandStart CommandAction = "start"
	// CommandCancel asks the client to cancel capture.
	CommandCancel CommandAction = "cancel"
	// CommandRetry asks the client to retry capture.
	CommandRetry CommandAction = "retry"
)

// CaptureCommand requests a policy-bound action from the capture client.
type CaptureCommand struct {
	Action            CommandAction
	RequirementKey    string
	Artefact          string
	AcquisitionMethod string
}

func (CaptureCommand) messageType() MessageType { return MessageCaptureCommand }
func (payload CaptureCommand) validate() error {
	if payload.Action != CommandStart && payload.Action != CommandCancel && payload.Action != CommandRetry {
		return errors.New("capture command action is invalid")
	}
	if !requirementKey(payload.RequirementKey) || !safeName(payload.Artefact) ||
		(payload.AcquisitionMethod != "" && !safeName(payload.AcquisitionMethod)) {
		return errors.New("capture command binding is invalid")
	}

	return nil
}

// CommandOutcome identifies a client command result.
type CommandOutcome string

const (
	// CommandCompleted begins the closed set of command outcomes.
	CommandCompleted CommandOutcome = "completed"
	// CommandFailed reports unsuccessful command execution.
	CommandFailed CommandOutcome = "failed"
	// CommandCancelled reports cancelled command execution.
	CommandCancelled CommandOutcome = "cancelled"
)

// CaptureCommandResult reports the terminal result of a capture command.
type CaptureCommandResult struct {
	Outcome CommandOutcome
	Code    string
}

func (CaptureCommandResult) messageType() MessageType { return MessageCommandResult }
func (payload CaptureCommandResult) validate() error {
	if payload.Outcome != CommandCompleted && payload.Outcome != CommandFailed &&
		payload.Outcome != CommandCancelled {
		return errors.New("capture command outcome is invalid")
	}
	if payload.Outcome == CommandCompleted && payload.Code != "" {
		return errors.New("completed command cannot contain a failure code")
	}
	if payload.Outcome != CommandCompleted && !safeToken(payload.Code, 100) {
		return errors.New("unsuccessful command requires a safe code")
	}

	return nil
}

// ChallengeAction identifies one challenge lifecycle transition.
type ChallengeAction string

const (
	// ChallengeRequested begins the closed set of challenge actions.
	ChallengeRequested ChallengeAction = "requested"
	// ChallengeResponded reports a client challenge response.
	ChallengeResponded ChallengeAction = "responded"
	// ChallengeCancelled reports challenge cancellation.
	ChallengeCancelled ChallengeAction = "cancelled"
)

// ChallengeUpdate carries safe challenge control metadata without evidence.
type ChallengeUpdate struct {
	Action      ChallengeAction
	ChallengeID id.Challenge
	Kind        string
	Outcome     string
	Code        string
	ExpiresAt   time.Time
}

func (payload ChallengeUpdate) messageType() MessageType {
	switch payload.Action {
	case ChallengeRequested:
		return MessageChallengeRequest
	case ChallengeResponded:
		return MessageChallengeResponse
	case ChallengeCancelled:
		return MessageChallengeCancel
	default:
		return ""
	}
}

func (payload ChallengeUpdate) validate() error {
	if payload.ChallengeID.IsZero() || payload.messageType() == "" {
		return errors.New("challenge update identity is invalid")
	}
	switch payload.Action {
	case ChallengeRequested:
		if !safeToken(payload.Kind, 100) || payload.ExpiresAt.IsZero() ||
			payload.ExpiresAt.Location() != time.UTC || payload.Outcome != "" || payload.Code != "" {
			return errors.New("challenge request is invalid")
		}
	case ChallengeResponded:
		if (payload.Outcome != "completed" && payload.Outcome != "failed") ||
			payload.Kind != "" || !payload.ExpiresAt.IsZero() ||
			(payload.Outcome == "failed" && !safeToken(payload.Code, 100)) ||
			(payload.Outcome == "completed" && payload.Code != "") {
			return errors.New("challenge response is invalid")
		}
	case ChallengeCancelled:
		if !safeToken(payload.Code, 100) || payload.Kind != "" || payload.Outcome != "" ||
			!payload.ExpiresAt.IsZero() {
			return errors.New("challenge cancellation is invalid")
		}
	}

	return nil
}

// CaptureProgress reports safe aggregate capture-step counts.
type CaptureProgress struct {
	CompletedSteps uint32
	TotalSteps     uint32
}

func (CaptureProgress) messageType() MessageType { return MessageCaptureProgress }
func (payload CaptureProgress) validate() error {
	if payload.TotalSteps == 0 || payload.TotalSteps > 256 || payload.CompletedSteps > payload.TotalSteps {
		return errors.New("capture progress is invalid")
	}

	return nil
}

// VerificationCheckProgress reports capture-safe check execution state. It
// deliberately excludes outcomes, reason codes, signals, and provider data.
type VerificationCheckProgress struct {
	CheckID      id.Check
	State        string
	CheckVersion int64
}

func (VerificationCheckProgress) messageType() MessageType {
	return MessageVerificationCheckProgress
}

func (payload VerificationCheckProgress) validate() error {
	if payload.CheckID.IsZero() || payload.CheckVersion < 1 || !verificationCheckState(payload.State) {
		return errors.New("verification check progress is invalid")
	}

	return nil
}

func verificationCheckState(value string) bool {
	switch value {
	case "queued", "running", "awaiting_input", "awaiting_provider", "completed",
		"skipped_by_policy", "timed_out", "cancelled", "failed":
		return true
	default:
		return false
	}
}

// SessionStateChanged reports a safe authoritative session transition.
type SessionStateChanged struct {
	State          string
	SessionVersion int64
}

func (SessionStateChanged) messageType() MessageType { return MessageSessionState }
func (payload SessionStateChanged) validate() error {
	if !safeToken(payload.State, 64) || payload.SessionVersion < 1 {
		return errors.New("session state change is invalid")
	}

	return nil
}

// ResyncRequired instructs the client to recover authoritative REST state.
type ResyncRequired struct{ Reason string }

func (ResyncRequired) messageType() MessageType { return MessageResyncRequired }
func (payload ResyncRequired) validate() error {
	if !safeToken(payload.Reason, 100) {
		return errors.New("resync reason is invalid")
	}

	return nil
}

// ServerDraining asks the client to reconnect after the bounded delay.
type ServerDraining struct{ RetryAfter time.Duration }

func (ServerDraining) messageType() MessageType { return MessageServerDraining }
func (payload ServerDraining) validate() error {
	if payload.RetryAfter < time.Second || payload.RetryAfter > time.Minute {
		return errors.New("server drain retry interval is invalid")
	}

	return nil
}

// CommandDisposition identifies server acceptance of a client command.
type CommandDisposition string

const (
	// CommandDispositionAccepted begins the closed set of dispositions.
	CommandDispositionAccepted CommandDisposition = "accepted"
	// CommandDispositionRejected reports command rejection.
	CommandDispositionRejected CommandDisposition = "rejected"
)

// CommandAcknowledgement confirms or rejects a consequential command.
type CommandAcknowledgement struct {
	Disposition CommandDisposition
	Code        string
}

func (payload CommandAcknowledgement) messageType() MessageType {
	if payload.Disposition == CommandDispositionAccepted {
		return MessageCommandAccepted
	}
	if payload.Disposition == CommandDispositionRejected {
		return MessageCommandRejected
	}

	return ""
}

func (payload CommandAcknowledgement) validate() error {
	if payload.messageType() == "" ||
		(payload.Disposition == CommandDispositionAccepted && payload.Code != "") ||
		(payload.Disposition == CommandDispositionRejected && !safeToken(payload.Code, 100)) {
		return errors.New("command acknowledgement is invalid")
	}

	return nil
}

func requirementKey(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}

	return true
}

func safeName(value string) bool {
	if len(value) == 0 || len(value) > 200 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
			character != '_' && character != '.' {
			return false
		}
	}

	return true
}

func safeToken(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}

	return true
}

func uniqueSafeTokens(values []string, maximum int) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !safeToken(value, maximum) {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}

	return true
}

func uniqueVersions(values []uint16) bool {
	seen := make(map[uint16]struct{}, len(values))
	for _, value := range values {
		if value == 0 {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}

	return true
}
