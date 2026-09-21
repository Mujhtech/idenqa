// Package id generates and parses opaque, prefixed identifiers.
package id

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/oklog/ulid/v2"
)

const prefixLength = 3

// Prefix identifies one resource type at operational boundaries.
type Prefix string

// NewPrefix validates a public resource prefix.
func NewPrefix(value string) (Prefix, error) {
	if len(value) != prefixLength {
		return "", fmt.Errorf("identifier prefix must contain exactly %d characters", prefixLength)
	}
	for _, character := range value {
		if character < 'a' || character > 'z' {
			return "", errors.New("identifier prefix must contain only lowercase ASCII letters")
		}
	}

	return Prefix(value), nil
}

// Value is a validated prefixed ULID. Domain packages should wrap Value in
// their own resource-specific types rather than exposing it directly.
type Value struct {
	prefix Prefix
	ulid   ulid.ULID
}

// Parse validates an identifier against its expected resource prefix.
func Parse(prefix Prefix, encoded string) (Value, error) {
	if _, err := NewPrefix(string(prefix)); err != nil {
		return Value{}, fmt.Errorf("validate identifier prefix: %w", err)
	}
	want := string(prefix) + "_"
	if !strings.HasPrefix(encoded, want) {
		return Value{}, fmt.Errorf("identifier does not have the expected %s_ prefix", prefix)
	}

	parsed, err := ulid.ParseStrict(strings.TrimPrefix(encoded, want))
	if err != nil {
		return Value{}, fmt.Errorf("parse %s identifier: %w", prefix, err)
	}

	return Value{prefix: prefix, ulid: parsed}, nil
}

// String returns the stable public representation of the identifier.
func (value Value) String() string {
	if value.prefix == "" {
		return ""
	}

	return string(value.prefix) + "_" + value.ulid.String()
}

// IsZero reports whether the value has not been initialised.
func (value Value) IsZero() bool {
	return value.prefix == ""
}

// Generator creates sortable identifiers from an injected clock and entropy
// source. Its entropy reader is safe for concurrent use.
type Generator struct {
	clock   clock.Clock
	entropy io.Reader
}

// NewGenerator builds a generator. Nil dependencies are rejected rather than
// replaced implicitly so tests and process composition remain explicit.
func NewGenerator(source clock.Clock, entropy io.Reader) (*Generator, error) {
	if source == nil {
		return nil, errors.New("identifier clock is required")
	}
	if entropy == nil {
		return nil, errors.New("identifier entropy is required")
	}

	return &Generator{
		clock: source,
		entropy: &ulid.LockedMonotonicReader{
			MonotonicReader: ulid.Monotonic(entropy, 0),
		},
	}, nil
}

// NewSystemGenerator builds the production generator using the system clock
// and cryptographic entropy.
func NewSystemGenerator() (*Generator, error) {
	return NewGenerator(clock.System{}, rand.Reader)
}

// New creates an identifier for prefix.
func (generator *Generator) New(prefix Prefix) (Value, error) {
	if _, err := NewPrefix(string(prefix)); err != nil {
		return Value{}, fmt.Errorf("validate identifier prefix: %w", err)
	}
	generated, err := ulid.New(ulid.Timestamp(generator.clock.Now()), generator.entropy)
	if err != nil {
		return Value{}, fmt.Errorf("generate %s identifier: %w", prefix, err)
	}

	return Value{prefix: prefix, ulid: generated}, nil
}

// RequestPrefix is the public prefix for request correlation identifiers.
const RequestPrefix Prefix = "req"

// Request is a typed request correlation identifier.
type Request struct {
	value Value
}

// NewRequest generates a request identifier.
func (generator *Generator) NewRequest() (Request, error) {
	value, err := generator.New(RequestPrefix)
	if err != nil {
		return Request{}, err
	}

	return Request{value: value}, nil
}

// ParseRequest parses a request identifier.
func ParseRequest(encoded string) (Request, error) {
	value, err := Parse(RequestPrefix, encoded)
	if err != nil {
		return Request{}, err
	}

	return Request{value: value}, nil
}

// String returns the stable public representation of the request identifier.
func (request Request) String() string {
	return request.value.String()
}

// IsZero reports whether the request identifier has not been initialised.
func (request Request) IsZero() bool {
	return request.value.IsZero()
}

// TenantPrefix is the public prefix for tenant identifiers.
const TenantPrefix Prefix = "ten"

// Tenant is a typed tenant identifier.
type Tenant struct {
	value Value
}

// NewTenant generates a tenant identifier.
func (generator *Generator) NewTenant() (Tenant, error) {
	value, err := generator.New(TenantPrefix)
	if err != nil {
		return Tenant{}, err
	}

	return Tenant{value: value}, nil
}

// ParseTenant parses a tenant identifier.
func ParseTenant(encoded string) (Tenant, error) {
	value, err := Parse(TenantPrefix, encoded)
	if err != nil {
		return Tenant{}, err
	}

	return Tenant{value: value}, nil
}

// String returns the stable public tenant identifier.
func (tenant Tenant) String() string {
	return tenant.value.String()
}

// IsZero reports whether the tenant identifier has not been initialised.
func (tenant Tenant) IsZero() bool {
	return tenant.value.IsZero()
}

// APIKeyPrefix is the public prefix for API-key record identifiers.
const APIKeyPrefix Prefix = "key"

// APIKey is a typed API-key record identifier. It is not a credential and is
// safe to persist and display.
type APIKey struct {
	value Value
}

// NewAPIKey generates an API-key record identifier.
func (generator *Generator) NewAPIKey() (APIKey, error) {
	value, err := generator.New(APIKeyPrefix)
	if err != nil {
		return APIKey{}, err
	}

	return APIKey{value: value}, nil
}

// ParseAPIKey parses an API-key record identifier.
func ParseAPIKey(encoded string) (APIKey, error) {
	value, err := Parse(APIKeyPrefix, encoded)
	if err != nil {
		return APIKey{}, err
	}

	return APIKey{value: value}, nil
}

// String returns the stable public API-key record identifier.
func (key APIKey) String() string {
	return key.value.String()
}

// IsZero reports whether the API-key record identifier has not been initialised.
func (key APIKey) IsZero() bool {
	return key.value.IsZero()
}

// ProfilePrefix is the public prefix for capture-profile identifiers.
const ProfilePrefix Prefix = "prf"

// Profile is a typed capture-profile identifier.
type Profile struct {
	value Value
}

// NewProfile generates a capture-profile identifier.
func (generator *Generator) NewProfile() (Profile, error) {
	value, err := generator.New(ProfilePrefix)
	if err != nil {
		return Profile{}, err
	}

	return Profile{value: value}, nil
}

// ParseProfile parses a capture-profile identifier.
func ParseProfile(encoded string) (Profile, error) {
	value, err := Parse(ProfilePrefix, encoded)
	if err != nil {
		return Profile{}, err
	}

	return Profile{value: value}, nil
}

// String returns the stable public capture-profile identifier.
func (profile Profile) String() string {
	return profile.value.String()
}

// IsZero reports whether the capture-profile identifier has not been initialised.
func (profile Profile) IsZero() bool {
	return profile.value.IsZero()
}

// VerificationPrefix is the public prefix for verification-session identifiers.
const VerificationPrefix Prefix = "ver"

// Verification is a typed verification-session identifier.
type Verification struct {
	value Value
}

// NewVerification generates a verification-session identifier.
func (generator *Generator) NewVerification() (Verification, error) {
	value, err := generator.New(VerificationPrefix)
	if err != nil {
		return Verification{}, err
	}

	return Verification{value: value}, nil
}

// ParseVerification parses a verification-session identifier.
func ParseVerification(encoded string) (Verification, error) {
	value, err := Parse(VerificationPrefix, encoded)
	if err != nil {
		return Verification{}, err
	}

	return Verification{value: value}, nil
}

// String returns the stable public verification-session identifier.
func (verification Verification) String() string { return verification.value.String() }

// IsZero reports whether the verification-session identifier is uninitialised.
func (verification Verification) IsZero() bool { return verification.value.IsZero() }

// EvidencePrefix is the public prefix for evidence records.
const EvidencePrefix Prefix = "evd"

// Evidence is a typed evidence-record identifier.
type Evidence struct {
	value Value
}

// NewEvidence generates an evidence-record identifier.
func (generator *Generator) NewEvidence() (Evidence, error) {
	value, err := generator.New(EvidencePrefix)
	if err != nil {
		return Evidence{}, err
	}

	return Evidence{value: value}, nil
}

// ParseEvidence parses an evidence-record identifier.
func ParseEvidence(encoded string) (Evidence, error) {
	value, err := Parse(EvidencePrefix, encoded)
	if err != nil {
		return Evidence{}, err
	}

	return Evidence{value: value}, nil
}

// String returns the stable public evidence-record identifier.
func (evidence Evidence) String() string { return evidence.value.String() }

// IsZero reports whether the evidence-record identifier is uninitialised.
func (evidence Evidence) IsZero() bool { return evidence.value.IsZero() }

// UploadPrefix is the public prefix for durable evidence-upload intents.
const UploadPrefix Prefix = "upl"

// Upload is a typed evidence-upload-intent identifier.
type Upload struct{ value Value }

// NewUpload generates an evidence-upload-intent identifier.
func (generator *Generator) NewUpload() (Upload, error) {
	value, err := generator.New(UploadPrefix)
	return Upload{value: value}, err
}

// ParseUpload parses an evidence-upload-intent identifier.
func ParseUpload(encoded string) (Upload, error) {
	value, err := Parse(UploadPrefix, encoded)
	return Upload{value: value}, err
}

// String returns the stable public evidence-upload-intent identifier.
func (upload Upload) String() string { return upload.value.String() }

// IsZero reports whether the evidence-upload-intent identifier is uninitialised.
func (upload Upload) IsZero() bool { return upload.value.IsZero() }

// CaptureTokenPrefix is the public prefix for capture-token record identifiers.
const CaptureTokenPrefix Prefix = "ctk"

// CaptureToken is a typed capture-token record identifier.
type CaptureToken struct {
	value Value
}

// NewCaptureToken generates a capture-token record identifier.
func (generator *Generator) NewCaptureToken() (CaptureToken, error) {
	value, err := generator.New(CaptureTokenPrefix)
	if err != nil {
		return CaptureToken{}, err
	}

	return CaptureToken{value: value}, nil
}

// ParseCaptureToken parses a capture-token record identifier.
func ParseCaptureToken(encoded string) (CaptureToken, error) {
	value, err := Parse(CaptureTokenPrefix, encoded)
	if err != nil {
		return CaptureToken{}, err
	}

	return CaptureToken{value: value}, nil
}

// String returns the stable public capture-token record identifier.
func (token CaptureToken) String() string { return token.value.String() }

// IsZero reports whether the capture-token record identifier is uninitialised.
func (token CaptureToken) IsZero() bool { return token.value.IsZero() }

// OutcomeTokenPrefix is the public prefix for outcome-token record identifiers.
const OutcomeTokenPrefix Prefix = "otk"

// OutcomeToken is a typed outcome-token record identifier.
type OutcomeToken struct {
	value Value
}

// NewOutcomeToken generates an outcome-token record identifier.
func (generator *Generator) NewOutcomeToken() (OutcomeToken, error) {
	value, err := generator.New(OutcomeTokenPrefix)
	if err != nil {
		return OutcomeToken{}, err
	}

	return OutcomeToken{value: value}, nil
}

// ParseOutcomeToken parses an outcome-token record identifier.
func ParseOutcomeToken(encoded string) (OutcomeToken, error) {
	value, err := Parse(OutcomeTokenPrefix, encoded)
	if err != nil {
		return OutcomeToken{}, err
	}

	return OutcomeToken{value: value}, nil
}

// String returns the stable public outcome-token record identifier.
func (token OutcomeToken) String() string { return token.value.String() }

// IsZero reports whether the outcome-token record identifier is uninitialised.
func (token OutcomeToken) IsZero() bool { return token.value.IsZero() }

// EventPrefix is the public prefix for durable event identifiers.
const EventPrefix Prefix = "evt"

// Event is a typed durable event identifier.
type Event struct {
	value Value
}

// NewEvent generates a durable event identifier.
func (generator *Generator) NewEvent() (Event, error) {
	value, err := generator.New(EventPrefix)
	if err != nil {
		return Event{}, err
	}

	return Event{value: value}, nil
}

// ParseEvent parses a durable event identifier.
func ParseEvent(encoded string) (Event, error) {
	value, err := Parse(EventPrefix, encoded)
	if err != nil {
		return Event{}, err
	}

	return Event{value: value}, nil
}

// String returns the stable public durable event identifier.
func (event Event) String() string { return event.value.String() }

// IsZero reports whether the durable event identifier is uninitialised.
func (event Event) IsZero() bool { return event.value.IsZero() }

// AuthorityPrefix is the public prefix for processing-authority records.
const AuthorityPrefix Prefix = "aut"

// Authority is a typed processing-authority identifier.
type Authority struct{ value Value }

// NewAuthority generates a processing-authority identifier.
func (generator *Generator) NewAuthority() (Authority, error) {
	value, err := generator.New(AuthorityPrefix)
	return Authority{value: value}, err
}

// ParseAuthority parses a processing-authority identifier.
func ParseAuthority(encoded string) (Authority, error) {
	value, err := Parse(AuthorityPrefix, encoded)
	return Authority{value: value}, err
}

// String returns the stable public processing-authority identifier.
func (authority Authority) String() string { return authority.value.String() }

// IsZero reports whether the processing-authority identifier is uninitialised.
func (authority Authority) IsZero() bool { return authority.value.IsZero() }

// NoticePrefix is the public prefix for immutable notice-version records.
const NoticePrefix Prefix = "ntc"

// Notice is a typed immutable notice-version identifier.
type Notice struct{ value Value }

// NewNotice generates an immutable notice-version identifier.
func (generator *Generator) NewNotice() (Notice, error) {
	value, err := generator.New(NoticePrefix)
	return Notice{value: value}, err
}

// ParseNotice parses an immutable notice-version identifier.
func ParseNotice(encoded string) (Notice, error) {
	value, err := Parse(NoticePrefix, encoded)
	return Notice{value: value}, err
}

// String returns the stable public notice-version identifier.
func (notice Notice) String() string { return notice.value.String() }

// IsZero reports whether the notice-version identifier is uninitialised.
func (notice Notice) IsZero() bool { return notice.value.IsZero() }

// SubjectPrefix is the public prefix for tenant-scoped subject records.
const SubjectPrefix Prefix = "sub"

// Subject is a typed tenant-scoped subject identifier.
type Subject struct{ value Value }

// NewSubject generates a tenant-scoped subject identifier.
func (generator *Generator) NewSubject() (Subject, error) {
	value, err := generator.New(SubjectPrefix)
	return Subject{value: value}, err
}

// ParseSubject parses a tenant-scoped subject identifier.
func ParseSubject(encoded string) (Subject, error) {
	value, err := Parse(SubjectPrefix, encoded)
	return Subject{value: value}, err
}

// String returns the stable public subject identifier.
func (subject Subject) String() string { return subject.value.String() }

// IsZero reports whether the subject identifier is uninitialised.
func (subject Subject) IsZero() bool { return subject.value.IsZero() }

// AcknowledgementPrefix is the public prefix for subject-response records.
const AcknowledgementPrefix Prefix = "ack"

// Acknowledgement is a typed append-only subject-response identifier.
type Acknowledgement struct{ value Value }

// NewAcknowledgement generates a subject-response identifier.
func (generator *Generator) NewAcknowledgement() (Acknowledgement, error) {
	value, err := generator.New(AcknowledgementPrefix)
	return Acknowledgement{value: value}, err
}

// ParseAcknowledgement parses a subject-response identifier.
func ParseAcknowledgement(encoded string) (Acknowledgement, error) {
	value, err := Parse(AcknowledgementPrefix, encoded)
	return Acknowledgement{value: value}, err
}

// String returns the stable public subject-response identifier.
func (acknowledgement Acknowledgement) String() string { return acknowledgement.value.String() }

// IsZero reports whether the subject-response identifier is uninitialised.
func (acknowledgement Acknowledgement) IsZero() bool { return acknowledgement.value.IsZero() }

// GrantPrefix is the public prefix for scoped evidence-processing grants.
const GrantPrefix Prefix = "grt"

// Grant is a typed evidence-processing-grant identifier.
type Grant struct{ value Value }

// NewGrant generates an evidence-processing-grant identifier.
func (generator *Generator) NewGrant() (Grant, error) {
	value, err := generator.New(GrantPrefix)
	return Grant{value: value}, err
}

// ParseGrant parses an evidence-processing-grant identifier.
func ParseGrant(encoded string) (Grant, error) {
	value, err := Parse(GrantPrefix, encoded)
	return Grant{value: value}, err
}

// String returns the stable public evidence-processing-grant identifier.
func (grant Grant) String() string { return grant.value.String() }

// IsZero reports whether the evidence-processing-grant identifier is uninitialised.
func (grant Grant) IsZero() bool { return grant.value.IsZero() }

// RedemptionPrefix is the public prefix for one idempotent grant-use attempt.
const RedemptionPrefix Prefix = "rdm"

// Redemption identifies one retry-safe processing-grant use.
type Redemption struct{ value Value }

// NewRedemption generates a grant-redemption identifier.
func (generator *Generator) NewRedemption() (Redemption, error) {
	value, err := generator.New(RedemptionPrefix)
	if err != nil {
		return Redemption{}, err
	}
	return Redemption{value: value}, nil
}

// ParseRedemption parses a grant-redemption identifier.
func ParseRedemption(encoded string) (Redemption, error) {
	value, err := Parse(RedemptionPrefix, encoded)
	if err != nil {
		return Redemption{}, err
	}
	return Redemption{value: value}, nil
}

// String returns the stable public redemption identifier.
func (redemption Redemption) String() string { return redemption.value.String() }

// IsZero reports whether the redemption identifier has not been initialised.
func (redemption Redemption) IsZero() bool { return redemption.value.IsZero() }

// ConnectionPrefix is the public prefix for ephemeral realtime connections.
const ConnectionPrefix Prefix = "con"

// Connection identifies one accepted realtime connection.
type Connection struct{ value Value }

// NewConnection generates a realtime connection identifier.
func (generator *Generator) NewConnection() (Connection, error) {
	value, err := generator.New(ConnectionPrefix)
	return Connection{value: value}, err
}

// ParseConnection parses a realtime connection identifier.
func ParseConnection(encoded string) (Connection, error) {
	value, err := Parse(ConnectionPrefix, encoded)
	return Connection{value: value}, err
}

// String returns the stable public realtime connection identifier.
func (connection Connection) String() string { return connection.value.String() }

// IsZero reports whether the realtime connection identifier is uninitialised.
func (connection Connection) IsZero() bool { return connection.value.IsZero() }

// MessagePrefix is the public prefix for realtime message identifiers.
const MessagePrefix Prefix = "msg"

// Message identifies one realtime protocol message.
type Message struct{ value Value }

// NewMessage generates a realtime message identifier.
func (generator *Generator) NewMessage() (Message, error) {
	value, err := generator.New(MessagePrefix)
	return Message{value: value}, err
}

// ParseMessage parses a realtime message identifier.
func ParseMessage(encoded string) (Message, error) {
	value, err := Parse(MessagePrefix, encoded)
	return Message{value: value}, err
}

// String returns the stable public realtime message identifier.
func (message Message) String() string { return message.value.String() }

// IsZero reports whether the realtime message identifier is uninitialised.
func (message Message) IsZero() bool { return message.value.IsZero() }

// CommandPrefix is the public prefix for idempotent realtime commands.
const CommandPrefix Prefix = "cmd"

// Command identifies one consequential realtime command.
type Command struct{ value Value }

// NewCommand generates a realtime command identifier.
func (generator *Generator) NewCommand() (Command, error) {
	value, err := generator.New(CommandPrefix)
	return Command{value: value}, err
}

// ParseCommand parses a realtime command identifier.
func ParseCommand(encoded string) (Command, error) {
	value, err := Parse(CommandPrefix, encoded)
	return Command{value: value}, err
}

// String returns the stable public realtime command identifier.
func (command Command) String() string { return command.value.String() }

// IsZero reports whether the realtime command identifier is uninitialised.
func (command Command) IsZero() bool { return command.value.IsZero() }

// ChallengePrefix is the public prefix for capture challenge identifiers.
const ChallengePrefix Prefix = "chl"

// Challenge identifies one bounded capture challenge.
type Challenge struct{ value Value }

// NewChallenge generates a capture challenge identifier.
func (generator *Generator) NewChallenge() (Challenge, error) {
	value, err := generator.New(ChallengePrefix)
	return Challenge{value: value}, err
}

// ParseChallenge parses a capture challenge identifier.
func ParseChallenge(encoded string) (Challenge, error) {
	value, err := Parse(ChallengePrefix, encoded)
	return Challenge{value: value}, err
}

// String returns the stable public capture challenge identifier.
func (challenge Challenge) String() string { return challenge.value.String() }

// IsZero reports whether the capture challenge identifier is uninitialised.
func (challenge Challenge) IsZero() bool { return challenge.value.IsZero() }

// WebSocketTicketPrefix is the public prefix for single-use connection-ticket records.
const WebSocketTicketPrefix Prefix = "wst"

// WebSocketTicket identifies a connection-ticket record, not its secret.
type WebSocketTicket struct{ value Value }

// NewWebSocketTicket generates a connection-ticket record identifier.
func (generator *Generator) NewWebSocketTicket() (WebSocketTicket, error) {
	value, err := generator.New(WebSocketTicketPrefix)
	return WebSocketTicket{value: value}, err
}

// ParseWebSocketTicket parses a connection-ticket record identifier.
func ParseWebSocketTicket(encoded string) (WebSocketTicket, error) {
	value, err := Parse(WebSocketTicketPrefix, encoded)
	return WebSocketTicket{value: value}, err
}

// String returns the stable public connection-ticket record identifier.
func (ticket WebSocketTicket) String() string { return ticket.value.String() }

// IsZero reports whether the connection-ticket record identifier is uninitialised.
func (ticket WebSocketTicket) IsZero() bool { return ticket.value.IsZero() }

// TaskPrefix is the public prefix for durable background-task identifiers.
const TaskPrefix Prefix = "tsk"

// Task identifies one durable background task without exposing a queue identifier.
type Task struct{ value Value }

// NewTask generates a background-task identifier.
func (generator *Generator) NewTask() (Task, error) {
	value, err := generator.New(TaskPrefix)
	return Task{value: value}, err
}

// ParseTask parses a background-task identifier.
func ParseTask(encoded string) (Task, error) {
	value, err := Parse(TaskPrefix, encoded)
	return Task{value: value}, err
}

// String returns the stable public background-task identifier.
func (task Task) String() string { return task.value.String() }

// IsZero reports whether the background-task identifier is uninitialised.
func (task Task) IsZero() bool { return task.value.IsZero() }

// ProviderPrefix is the public prefix for configured provider registrations.
const ProviderPrefix Prefix = "pvd"

// Provider identifies one configured provider registration.
type Provider struct{ value Value }

// NewProvider generates a provider-registration identifier.
func (generator *Generator) NewProvider() (Provider, error) {
	value, err := generator.New(ProviderPrefix)
	return Provider{value: value}, err
}

// ParseProvider parses a provider-registration identifier.
func ParseProvider(encoded string) (Provider, error) {
	value, err := Parse(ProviderPrefix, encoded)
	return Provider{value: value}, err
}

// String returns the stable public provider-registration identifier.
func (provider Provider) String() string { return provider.value.String() }

// IsZero reports whether the provider-registration identifier is uninitialised.
func (provider Provider) IsZero() bool { return provider.value.IsZero() }

// ProviderRegistrationPrefix is the public prefix for tenant provider registrations.
const ProviderRegistrationPrefix Prefix = "pvr"

// ProviderRegistration identifies one tenant-owned secret-free provider route registration.
type ProviderRegistration struct{ value Value }

// NewProviderRegistration generates a tenant provider-registration identifier.
func (generator *Generator) NewProviderRegistration() (ProviderRegistration, error) {
	value, err := generator.New(ProviderRegistrationPrefix)
	return ProviderRegistration{value: value}, err
}

// ParseProviderRegistration parses a tenant provider-registration identifier.
func ParseProviderRegistration(encoded string) (ProviderRegistration, error) {
	value, err := Parse(ProviderRegistrationPrefix, encoded)
	return ProviderRegistration{value: value}, err
}

// String returns the stable public tenant provider-registration identifier.
func (registration ProviderRegistration) String() string { return registration.value.String() }

// IsZero reports whether the tenant provider-registration identifier is uninitialised.
func (registration ProviderRegistration) IsZero() bool { return registration.value.IsZero() }

// ModelPrefix is the public prefix for configured model registrations.
const ModelPrefix Prefix = "mdl"

// Model identifies one configured model registration.
type Model struct{ value Value }

// NewModel generates a model-registration identifier.
func (generator *Generator) NewModel() (Model, error) {
	value, err := generator.New(ModelPrefix)
	return Model{value: value}, err
}

// ParseModel parses a model-registration identifier.
func ParseModel(encoded string) (Model, error) {
	value, err := Parse(ModelPrefix, encoded)
	return Model{value: value}, err
}

// String returns the stable public model-registration identifier.
func (model Model) String() string { return model.value.String() }

// IsZero reports whether the model-registration identifier is uninitialised.
func (model Model) IsZero() bool { return model.value.IsZero() }

// AttemptPrefix is the public prefix for provider and model execution attempts.
const AttemptPrefix Prefix = "atm"

// Attempt identifies one retry-safe provider or model execution attempt.
type Attempt struct{ value Value }

// NewAttempt generates an execution-attempt identifier.
func (generator *Generator) NewAttempt() (Attempt, error) {
	value, err := generator.New(AttemptPrefix)
	return Attempt{value: value}, err
}

// ParseAttempt parses an execution-attempt identifier.
func ParseAttempt(encoded string) (Attempt, error) {
	value, err := Parse(AttemptPrefix, encoded)
	return Attempt{value: value}, err
}

// String returns the stable public execution-attempt identifier.
func (attempt Attempt) String() string { return attempt.value.String() }

// IsZero reports whether the execution-attempt identifier is uninitialised.
func (attempt Attempt) IsZero() bool { return attempt.value.IsZero() }

// CheckPrefix is the public prefix for verification-check aggregates.
const CheckPrefix Prefix = "chk"

// Check identifies one policy-addressable verification check.
type Check struct{ value Value }

// NewCheck generates a verification-check identifier.
func (generator *Generator) NewCheck() (Check, error) {
	value, err := generator.New(CheckPrefix)
	return Check{value: value}, err
}

// ParseCheck parses a verification-check identifier.
func ParseCheck(encoded string) (Check, error) {
	value, err := Parse(CheckPrefix, encoded)
	return Check{value: value}, err
}

// String returns the stable verification-check identifier.
func (check Check) String() string { return check.value.String() }

// IsZero reports whether the verification-check identifier is uninitialised.
func (check Check) IsZero() bool { return check.value.IsZero() }

// ObservationPrefix is the public prefix for normalised observations.
const ObservationPrefix Prefix = "obs"

// Observation identifies one immutable normalised runner observation.
type Observation struct{ value Value }

// NewObservation generates an observation identifier.
func (generator *Generator) NewObservation() (Observation, error) {
	value, err := generator.New(ObservationPrefix)
	return Observation{value: value}, err
}

// ParseObservation parses an observation identifier.
func ParseObservation(encoded string) (Observation, error) {
	value, err := Parse(ObservationPrefix, encoded)
	return Observation{value: value}, err
}

// String returns the stable observation identifier.
func (observation Observation) String() string { return observation.value.String() }

// IsZero reports whether the observation identifier is uninitialised.
func (observation Observation) IsZero() bool { return observation.value.IsZero() }

// PolicyPrefix is the public prefix for tenant-owned policy aggregates.
const PolicyPrefix Prefix = "pol"

// Policy identifies one versioned policy aggregate without exposing ULID.
type Policy struct{ value Value }

// NewPolicy generates a policy identifier.
func (generator *Generator) NewPolicy() (Policy, error) {
	value, err := generator.New(PolicyPrefix)
	return Policy{value: value}, err
}

// ParsePolicy parses a policy identifier.
func ParsePolicy(encoded string) (Policy, error) {
	value, err := Parse(PolicyPrefix, encoded)
	return Policy{value: value}, err
}

// String returns the stable policy identifier.
func (policy Policy) String() string { return policy.value.String() }

// IsZero reports whether the policy identifier is uninitialised.
func (policy Policy) IsZero() bool { return policy.value.IsZero() }

// DecisionPrefix is the public prefix for immutable policy decisions.
const DecisionPrefix Prefix = "dec"

// Decision identifies one immutable decision and its exact snapshot.
type Decision struct{ value Value }

// NewDecision generates a decision identifier.
func (generator *Generator) NewDecision() (Decision, error) {
	value, err := generator.New(DecisionPrefix)
	return Decision{value: value}, err
}

// ParseDecision parses a decision identifier.
func ParseDecision(encoded string) (Decision, error) {
	value, err := Parse(DecisionPrefix, encoded)
	return Decision{value: value}, err
}

// String returns the stable decision identifier.
func (decision Decision) String() string { return decision.value.String() }

// IsZero reports whether the decision identifier is uninitialised.
func (decision Decision) IsZero() bool { return decision.value.IsZero() }

// WebhookEndpointPrefix is the public prefix for tenant webhook endpoints.
const WebhookEndpointPrefix Prefix = "whk"

// WebhookEndpoint identifies one tenant webhook endpoint.
type WebhookEndpoint struct{ value Value }

// NewWebhookEndpoint generates a webhook endpoint identifier.
func (generator *Generator) NewWebhookEndpoint() (WebhookEndpoint, error) {
	value, err := generator.New(WebhookEndpointPrefix)
	return WebhookEndpoint{value: value}, err
}

// ParseWebhookEndpoint parses a webhook endpoint identifier.
func ParseWebhookEndpoint(encoded string) (WebhookEndpoint, error) {
	value, err := Parse(WebhookEndpointPrefix, encoded)
	return WebhookEndpoint{value: value}, err
}

// String returns the stable webhook endpoint identifier.
func (endpoint WebhookEndpoint) String() string { return endpoint.value.String() }

// IsZero reports whether the webhook endpoint identifier is uninitialised.
func (endpoint WebhookEndpoint) IsZero() bool { return endpoint.value.IsZero() }

// DeliveryPrefix is the public prefix for durable webhook deliveries.
const DeliveryPrefix Prefix = "dlv"

// Delivery identifies one durable webhook delivery intent.
type Delivery struct{ value Value }

// NewDelivery generates a delivery identifier.
func (generator *Generator) NewDelivery() (Delivery, error) {
	value, err := generator.New(DeliveryPrefix)
	return Delivery{value: value}, err
}

// ParseDelivery parses a delivery identifier.
func ParseDelivery(encoded string) (Delivery, error) {
	value, err := Parse(DeliveryPrefix, encoded)
	return Delivery{value: value}, err
}

// String returns the stable delivery identifier.
func (delivery Delivery) String() string { return delivery.value.String() }

// IsZero reports whether the delivery identifier is uninitialised.
func (delivery Delivery) IsZero() bool { return delivery.value.IsZero() }

// DeletionPrefix is the public prefix for deletion workflows.
const DeletionPrefix Prefix = "del"

// Deletion identifies one durable deletion workflow.
type Deletion struct{ value Value }

// NewDeletion generates a deletion identifier.
func (generator *Generator) NewDeletion() (Deletion, error) {
	value, err := generator.New(DeletionPrefix)
	return Deletion{value: value}, err
}

// ParseDeletion parses a deletion identifier.
func ParseDeletion(encoded string) (Deletion, error) {
	value, err := Parse(DeletionPrefix, encoded)
	return Deletion{value: value}, err
}

// String returns the stable deletion identifier.
func (deletion Deletion) String() string { return deletion.value.String() }

// IsZero reports whether the deletion identifier is uninitialised.
func (deletion Deletion) IsZero() bool { return deletion.value.IsZero() }

// LegalHoldPrefix is the public prefix for legal holds.
const LegalHoldPrefix Prefix = "hld"

// LegalHold identifies one durable legal hold.
type LegalHold struct{ value Value }

// NewLegalHold generates a legal-hold identifier.
func (generator *Generator) NewLegalHold() (LegalHold, error) {
	value, err := generator.New(LegalHoldPrefix)
	return LegalHold{value: value}, err
}

// ParseLegalHold parses a legal-hold identifier.
func ParseLegalHold(encoded string) (LegalHold, error) {
	value, err := Parse(LegalHoldPrefix, encoded)
	return LegalHold{value: value}, err
}

// String returns the stable legal-hold identifier.
func (hold LegalHold) String() string { return hold.value.String() }

// IsZero reports whether the legal-hold identifier is uninitialised.
func (hold LegalHold) IsZero() bool { return hold.value.IsZero() }

// ReviewCasePrefix is the public prefix for review cases.
const ReviewCasePrefix Prefix = "rvc"

// ReviewCase identifies one durable manual-review case.
type ReviewCase struct{ value Value }

// NewReviewCase generates a review-case identifier.
func (generator *Generator) NewReviewCase() (ReviewCase, error) {
	value, err := generator.New(ReviewCasePrefix)
	return ReviewCase{value: value}, err
}

// ParseReviewCase parses a review-case identifier.
func ParseReviewCase(encoded string) (ReviewCase, error) {
	value, err := Parse(ReviewCasePrefix, encoded)
	return ReviewCase{value: value}, err
}

// String returns the stable review-case identifier.
func (review ReviewCase) String() string { return review.value.String() }

// IsZero reports whether the review-case identifier is uninitialised.
func (review ReviewCase) IsZero() bool { return review.value.IsZero() }

// FindingPrefix is the public prefix for immutable reviewer findings.
const FindingPrefix Prefix = "fnd"

// Finding identifies one immutable reviewer finding.
type Finding struct{ value Value }

// NewFinding generates a finding identifier.
func (generator *Generator) NewFinding() (Finding, error) {
	value, err := generator.New(FindingPrefix)
	return Finding{value: value}, err
}

// ParseFinding parses a finding identifier.
func ParseFinding(encoded string) (Finding, error) {
	value, err := Parse(FindingPrefix, encoded)
	return Finding{value: value}, err
}

// String returns the stable finding identifier.
func (finding Finding) String() string { return finding.value.String() }

// IsZero reports whether the finding identifier is uninitialised.
func (finding Finding) IsZero() bool { return finding.value.IsZero() }

// ProviderCallbackPrefix is the public prefix for opaque per-attempt provider
// callback references. The reference is a capability presented by the provider
// adapter and never a credential itself.
const ProviderCallbackPrefix Prefix = "pcb"

// ProviderCallback is a typed provider callback reference.
type ProviderCallback struct{ value Value }

// NewProviderCallback generates a provider callback reference.
func (generator *Generator) NewProviderCallback() (ProviderCallback, error) {
	value, err := generator.New(ProviderCallbackPrefix)
	return ProviderCallback{value: value}, err
}

// ParseProviderCallback parses a provider callback reference.
func ParseProviderCallback(encoded string) (ProviderCallback, error) {
	value, err := Parse(ProviderCallbackPrefix, encoded)
	return ProviderCallback{value: value}, err
}

// String returns the stable public provider callback reference.
func (callback ProviderCallback) String() string { return callback.value.String() }

// IsZero reports whether the callback reference has not been initialised.
func (callback ProviderCallback) IsZero() bool { return callback.value.IsZero() }

// InputRequestPrefix is the public prefix for policy-authored subject-input requests.
const InputRequestPrefix Prefix = "inp"

// InputRequest identifies one immutable policy-authored request for further subject input.
type InputRequest struct{ value Value }

// NewInputRequest generates a subject-input request identifier.
func (generator *Generator) NewInputRequest() (InputRequest, error) {
	value, err := generator.New(InputRequestPrefix)
	return InputRequest{value: value}, err
}

// ParseInputRequest parses a subject-input request identifier.
func ParseInputRequest(encoded string) (InputRequest, error) {
	value, err := Parse(InputRequestPrefix, encoded)
	return InputRequest{value: value}, err
}

// String returns the stable subject-input request identifier.
func (request InputRequest) String() string { return request.value.String() }

// IsZero reports whether the subject-input request identifier is uninitialised.
func (request InputRequest) IsZero() bool { return request.value.IsZero() }

// ProposalPrefix is the public prefix for AI proposal records.
const ProposalPrefix Prefix = "prp"

// Proposal identifies one immutable AI proposal.
type Proposal struct{ value Value }

// NewProposal generates a proposal identifier.
func (generator *Generator) NewProposal() (Proposal, error) {
	value, err := generator.New(ProposalPrefix)
	return Proposal{value: value}, err
}

// ParseProposal parses a proposal identifier.
func ParseProposal(encoded string) (Proposal, error) {
	value, err := Parse(ProposalPrefix, encoded)
	return Proposal{value: value}, err
}

// String returns the stable proposal identifier.
func (proposal Proposal) String() string { return proposal.value.String() }

// IsZero reports whether the proposal identifier is uninitialised.
func (proposal Proposal) IsZero() bool { return proposal.value.IsZero() }

// AcceptedCommandPrefix is the public prefix for deterministic accepted commands.
const AcceptedCommandPrefix Prefix = "acc"

// AcceptedCommand identifies one deterministic command derived from a proposal.
type AcceptedCommand struct{ value Value }

// NewAcceptedCommand generates an accepted-command identifier.
func (generator *Generator) NewAcceptedCommand() (AcceptedCommand, error) {
	value, err := generator.New(AcceptedCommandPrefix)
	return AcceptedCommand{value: value}, err
}

// ParseAcceptedCommand parses an accepted-command identifier.
func ParseAcceptedCommand(encoded string) (AcceptedCommand, error) {
	value, err := Parse(AcceptedCommandPrefix, encoded)
	return AcceptedCommand{value: value}, err
}

// String returns the stable accepted-command identifier.
func (command AcceptedCommand) String() string { return command.value.String() }

// IsZero reports whether the accepted-command identifier is uninitialised.
func (command AcceptedCommand) IsZero() bool { return command.value.IsZero() }

// PromptPrefix is the public prefix for prompt registry records.
const PromptPrefix Prefix = "prm"

// Prompt identifies one versioned prompt.
type Prompt struct{ value Value }

// NewPrompt generates a prompt identifier.
func (generator *Generator) NewPrompt() (Prompt, error) {
	value, err := generator.New(PromptPrefix)
	return Prompt{value: value}, err
}

// ParsePrompt parses a prompt identifier.
func ParsePrompt(encoded string) (Prompt, error) {
	value, err := Parse(PromptPrefix, encoded)
	return Prompt{value: value}, err
}

// String returns the stable prompt identifier.
func (prompt Prompt) String() string { return prompt.value.String() }

// IsZero reports whether the prompt identifier is uninitialised.
func (prompt Prompt) IsZero() bool { return prompt.value.IsZero() }

// AppealPrefix is the public prefix for appeals and reconsiderations.
const AppealPrefix Prefix = "apl"

// Appeal identifies one durable appeal.
type Appeal struct{ value Value }

// NewAppeal generates an appeal identifier.
func (generator *Generator) NewAppeal() (Appeal, error) {
	value, err := generator.New(AppealPrefix)
	return Appeal{value: value}, err
}

// ParseAppeal parses an appeal identifier.
func ParseAppeal(encoded string) (Appeal, error) {
	value, err := Parse(AppealPrefix, encoded)
	return Appeal{value: value}, err
}

// String returns the stable appeal identifier.
func (appeal Appeal) String() string { return appeal.value.String() }

// IsZero reports whether the appeal identifier is uninitialised.
func (appeal Appeal) IsZero() bool { return appeal.value.IsZero() }

// PrivacyRequestPrefix is the public prefix for data-subject privacy requests.
const PrivacyRequestPrefix Prefix = "prq"

// PrivacyRequest identifies one durable privacy-request workflow.
type PrivacyRequest struct{ value Value }

// NewPrivacyRequest generates a privacy-request identifier.
func (generator *Generator) NewPrivacyRequest() (PrivacyRequest, error) {
	value, err := generator.New(PrivacyRequestPrefix)
	return PrivacyRequest{value: value}, err
}

// ParsePrivacyRequest parses a privacy-request identifier.
func ParsePrivacyRequest(encoded string) (PrivacyRequest, error) {
	value, err := Parse(PrivacyRequestPrefix, encoded)
	return PrivacyRequest{value: value}, err
}

// String returns the stable privacy-request identifier.
func (request PrivacyRequest) String() string { return request.value.String() }

// IsZero reports whether the privacy-request identifier is uninitialised.
func (request PrivacyRequest) IsZero() bool { return request.value.IsZero() }

// PrivacyDecisionPrefix is the public prefix for privacy-request decisions.
const PrivacyDecisionPrefix Prefix = "prd"

// PrivacyDecision identifies one immutable privacy-request decision.
type PrivacyDecision struct{ value Value }

// NewPrivacyDecision generates a privacy-request decision identifier.
func (generator *Generator) NewPrivacyDecision() (PrivacyDecision, error) {
	value, err := generator.New(PrivacyDecisionPrefix)
	return PrivacyDecision{value: value}, err
}

// ParsePrivacyDecision parses a privacy-request decision identifier.
func ParsePrivacyDecision(encoded string) (PrivacyDecision, error) {
	value, err := Parse(PrivacyDecisionPrefix, encoded)
	return PrivacyDecision{value: value}, err
}

// String returns the stable privacy-request decision identifier.
func (decision PrivacyDecision) String() string { return decision.value.String() }

// IsZero reports whether the privacy-request decision identifier is uninitialised.
func (decision PrivacyDecision) IsZero() bool { return decision.value.IsZero() }

// PrivacyRestrictionPrefix is the public prefix for processing restrictions.
const PrivacyRestrictionPrefix Prefix = "prs"

// PrivacyRestriction identifies one durable processing restriction.
type PrivacyRestriction struct{ value Value }

// NewPrivacyRestriction generates a processing-restriction identifier.
func (generator *Generator) NewPrivacyRestriction() (PrivacyRestriction, error) {
	value, err := generator.New(PrivacyRestrictionPrefix)
	return PrivacyRestriction{value: value}, err
}

// ParsePrivacyRestriction parses a processing-restriction identifier.
func ParsePrivacyRestriction(encoded string) (PrivacyRestriction, error) {
	value, err := Parse(PrivacyRestrictionPrefix, encoded)
	return PrivacyRestriction{value: value}, err
}

// String returns the stable processing-restriction identifier.
func (restriction PrivacyRestriction) String() string { return restriction.value.String() }

// IsZero reports whether the processing-restriction identifier is uninitialised.
func (restriction PrivacyRestriction) IsZero() bool { return restriction.value.IsZero() }

// PrivacyDisclosurePrefix is the public prefix for transfer and disclosure records.
const PrivacyDisclosurePrefix Prefix = "pdc"

// PrivacyDisclosure identifies one durable transfer or disclosure record.
type PrivacyDisclosure struct{ value Value }

// NewPrivacyDisclosure generates a disclosure identifier.
func (generator *Generator) NewPrivacyDisclosure() (PrivacyDisclosure, error) {
	value, err := generator.New(PrivacyDisclosurePrefix)
	return PrivacyDisclosure{value: value}, err
}

// ParsePrivacyDisclosure parses a disclosure identifier.
func ParsePrivacyDisclosure(encoded string) (PrivacyDisclosure, error) {
	value, err := Parse(PrivacyDisclosurePrefix, encoded)
	return PrivacyDisclosure{value: value}, err
}

// String returns the stable disclosure identifier.
func (disclosure PrivacyDisclosure) String() string { return disclosure.value.String() }

// IsZero reports whether the disclosure identifier is uninitialised.
func (disclosure PrivacyDisclosure) IsZero() bool { return disclosure.value.IsZero() }

// ProcessorPrefix is the public prefix for processor and subprocessor inventory entries.
const ProcessorPrefix Prefix = "prc"

// Processor identifies one versioned processor-inventory entry.
type Processor struct{ value Value }

// NewProcessor generates a processor-inventory identifier.
func (generator *Generator) NewProcessor() (Processor, error) {
	value, err := generator.New(ProcessorPrefix)
	return Processor{value: value}, err
}

// ParseProcessor parses a processor-inventory identifier.
func ParseProcessor(encoded string) (Processor, error) {
	value, err := Parse(ProcessorPrefix, encoded)
	return Processor{value: value}, err
}

// String returns the stable processor-inventory identifier.
func (processor Processor) String() string { return processor.value.String() }

// IsZero reports whether the processor-inventory identifier is uninitialised.
func (processor Processor) IsZero() bool { return processor.value.IsZero() }

// ExperiencePrefix is the public prefix for portable capture-experience identifiers.
const ExperiencePrefix Prefix = "exp"

// Experience identifies one tenant-owned portable capture-experience aggregate.
type Experience struct{ value Value }

// NewExperience generates a portable capture-experience identifier.
func (generator *Generator) NewExperience() (Experience, error) {
	value, err := generator.New(ExperiencePrefix)
	return Experience{value: value}, err
}

// ParseExperience parses a portable capture-experience identifier.
func ParseExperience(encoded string) (Experience, error) {
	value, err := Parse(ExperiencePrefix, encoded)
	return Experience{value: value}, err
}

// String returns the stable capture-experience identifier.
func (experience Experience) String() string { return experience.value.String() }

// IsZero reports whether the capture-experience identifier is uninitialised.
func (experience Experience) IsZero() bool { return experience.value.IsZero() }
