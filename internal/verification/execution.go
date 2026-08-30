package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	maximumSignals     = 64
	maximumReasonCodes = 16
)

var (
	// ErrInvalidCheck means check or attempt state failed closed validation.
	ErrInvalidCheck = errors.New("verification: invalid check")
	// ErrAttemptConflict means an attempt received incompatible terminal content.
	ErrAttemptConflict = errors.New("verification: attempt result conflict")
	// ErrStaleAttempt means a worker no longer owns the current fenced attempt.
	ErrStaleAttempt = errors.New("verification: stale attempt")
)

// CheckState is operational state and never an identity-proofing conclusion.
type CheckState string

const (
	// CheckQueued begins the closed set of verification-check execution states.
	CheckQueued CheckState = "queued"
	// CheckRunning means the current attempt is executing.
	CheckRunning CheckState = "running"
	// CheckAwaitingInput means bounded subject input is required.
	CheckAwaitingInput CheckState = "awaiting_input"
	// CheckAwaitingProvider means an authenticated external result is pending.
	CheckAwaitingProvider CheckState = "awaiting_provider"
	// CheckCompleted means the check has a normalised result.
	CheckCompleted CheckState = "completed"
	// CheckSkippedByPolicy means pinned policy did not require execution.
	CheckSkippedByPolicy CheckState = "skipped_by_policy"
	// CheckTimedOut is an operational deadline outcome.
	CheckTimedOut CheckState = "timed_out"
	// CheckCancelled is an operational cancellation outcome.
	CheckCancelled CheckState = "cancelled"
	// CheckFailed is an operational execution failure.
	CheckFailed CheckState = "failed"
)

// CheckOutcome is meaningful only when a check is completed.
type CheckOutcome string

const (
	// CheckPassed begins the closed set of completed check outcomes.
	CheckPassed CheckOutcome = "passed"
	// CheckNotPassed means defined evidence did not satisfy the check.
	CheckNotPassed CheckOutcome = "not_passed"
	// CheckInconclusive means evidence supports neither completed conclusion.
	CheckInconclusive CheckOutcome = "inconclusive"
)

// RunnerKind identifies the owned execution boundary without adapter types.
type RunnerKind string

const (
	// RunnerProvider begins the closed set of runner kinds.
	RunnerProvider RunnerKind = "provider"
	// RunnerModel identifies an Idenqa model contract execution.
	RunnerModel RunnerKind = "model"
)

// SignalOutcome is a normalised observation, not a final policy decision.
type SignalOutcome string

const (
	// SignalSatisfied begins the closed set of normalised signal outcomes.
	SignalSatisfied SignalOutcome = "satisfied"
	// SignalNotSatisfied is a defined evidence result, not an execution error.
	SignalNotSatisfied SignalOutcome = "not_satisfied"
	// SignalInconclusive supports neither satisfied nor not-satisfied.
	SignalInconclusive SignalOutcome = "inconclusive"
)

// RetryDisposition controls execution mechanics only.
type RetryDisposition string

const (
	// RetryNever begins the closed set of runner retry dispositions.
	RetryNever RetryDisposition = "never"
	// RetryBackoff permits another attempt after bounded backoff.
	RetryBackoff RetryDisposition = "backoff"
	// RetryReconcile requests authoritative external reconciliation.
	RetryReconcile RetryDisposition = "reconcile"
)

// AttemptState records every execution outcome immutably.
type AttemptState string

const (
	// AttemptRunning begins the closed set of immutable attempt states.
	AttemptRunning AttemptState = "running"
	// AttemptCompleted contains normalised observations.
	AttemptCompleted AttemptState = "completed"
	// AttemptFailed contains an operational failure.
	AttemptFailed AttemptState = "failed"
	// AttemptTimedOut records deadline exhaustion.
	AttemptTimedOut AttemptState = "timed_out"
	// AttemptCancelled records cancellation.
	AttemptCancelled AttemptState = "cancelled"
)

// Provenance pins runner and request meaning for one attempt.
type Provenance struct {
	RunnerID      string
	RunnerVersion string
	PackageDigest string
	ContractMajor uint16
	ContractMinor uint16
	RequestDigest string
	Configuration string
}

// Signal is one bounded runner-independent observation.
type Signal struct {
	Name        string
	Outcome     SignalOutcome
	ReasonCodes []string
}

// Observation gives one signal immutable attempt provenance.
type Observation struct {
	ID         id.Observation
	AttemptID  id.Attempt
	RunnerKind RunnerKind
	Provenance Provenance
	Signal     Signal
	RecordedAt time.Time
}

// Failure is redacted operational state. It does not say anything about the subject.
type Failure struct {
	Class      string
	Code       string
	Retry      RetryDisposition
	RetryAfter time.Duration
}

// Attempt is an immutable-history entry. Terminal fields are set at most once.
type Attempt struct {
	ID           id.Attempt
	Number       uint32
	Fence        uint64
	RunnerKind   RunnerKind
	Provenance   Provenance
	State        AttemptState
	StartedAt    time.Time
	Deadline     time.Time
	FinishedAt   time.Time
	Failure      *Failure
	Observations []Observation
	resultDigest string
}

// Check is one versioned verification-check aggregate.
type Check struct {
	ID             id.Check
	TenantID       id.Tenant
	VerificationID id.Verification
	Name           string
	State          CheckState
	Outcome        CheckOutcome
	Version        int64
	CreatedAt      time.Time
	UpdatedAt      time.Time
	attempts       []Attempt
	diagnostics    []Diagnostic
}

// Diagnostic retains safe duplicate, stale, and conflicting delivery facts.
type Diagnostic struct {
	AttemptID  id.Attempt
	Kind       string
	Code       string
	RecordedAt time.Time
}

// NewCheck creates a queued check without inferring policy meaning.
func NewCheck(identifier id.Check, tenantID id.Tenant, verificationID id.Verification, name string, now time.Time) (Check, error) {
	if identifier.IsZero() || tenantID.IsZero() || verificationID.IsZero() ||
		!safeExecutionToken(name, 128) || !utcNonZero(now) {
		return Check{}, ErrInvalidCheck
	}
	return Check{ID: identifier, TenantID: tenantID, VerificationID: verificationID, Name: name,
		State: CheckQueued, Version: 1, CreatedAt: now, UpdatedAt: now}, nil
}

// RestoreCheck validates one complete aggregate loaded from durable state.
func RestoreCheck(
	identifier id.Check,
	tenantID id.Tenant,
	verificationID id.Verification,
	name string,
	state CheckState,
	outcome CheckOutcome,
	version int64,
	createdAt time.Time,
	updatedAt time.Time,
	attempts []Attempt,
	diagnostics []Diagnostic,
) (Check, error) {
	if identifier.IsZero() || tenantID.IsZero() || verificationID.IsZero() ||
		!safeExecutionToken(name, 128) || !validCheckState(state) || version < 1 ||
		!utcNonZero(createdAt) || !utcNonZero(updatedAt) || updatedAt.Before(createdAt) ||
		(state == CheckCompleted) != validCheckOutcome(outcome) {
		return Check{}, ErrInvalidCheck
	}
	copyOfAttempts := make([]Attempt, len(attempts))
	for index, attempt := range attempts {
		if err := validateStoredAttempt(attempt, uint32(index+1)); err != nil {
			return Check{}, err
		}
		copyOfAttempts[index] = cloneAttempt(attempt)
	}
	if err := validateCheckAttemptState(state, copyOfAttempts); err != nil {
		return Check{}, err
	}
	copyOfDiagnostics := slices.Clone(diagnostics)
	for _, diagnostic := range copyOfDiagnostics {
		if diagnostic.AttemptID.IsZero() || (diagnostic.Kind != "duplicate" && diagnostic.Kind != "conflict" && diagnostic.Kind != "stale") ||
			!safeExecutionToken(diagnostic.Code, 100) || !utcNonZero(diagnostic.RecordedAt) {
			return Check{}, ErrInvalidCheck
		}
	}
	return Check{ID: identifier, TenantID: tenantID, VerificationID: verificationID, Name: name,
		State: state, Outcome: outcome, Version: version, CreatedAt: createdAt,
		UpdatedAt: updatedAt, attempts: copyOfAttempts, diagnostics: copyOfDiagnostics}, nil
}

// Validate checks a complete aggregate before it crosses a persistence boundary.
func (check Check) Validate() error {
	_, err := RestoreCheck(check.ID, check.TenantID, check.VerificationID, check.Name, check.State,
		check.Outcome, check.Version, check.CreatedAt, check.UpdatedAt, check.Attempts(), check.Diagnostics())
	return err
}

// BeginAttempt appends a new running attempt. Earlier attempts are never replaced.
func (check *Check) BeginAttempt(attempt Attempt) error {
	if check == nil || (check.State != CheckQueued && check.State != CheckFailed && check.State != CheckTimedOut) ||
		attempt.ID.IsZero() || attempt.Number != uint32(len(check.attempts)+1) || //nolint:gosec // V-02 attempts are bounded far below uint32.
		attempt.Fence == 0 ||
		(attempt.RunnerKind != RunnerProvider && attempt.RunnerKind != RunnerModel) ||
		attempt.State != AttemptRunning || !utcNonZero(attempt.StartedAt) || !utcNonZero(attempt.Deadline) ||
		!attempt.Deadline.After(attempt.StartedAt) || validateProvenance(attempt.Provenance) != nil {
		return ErrInvalidCheck
	}
	check.attempts = append(check.attempts, cloneAttempt(attempt))
	check.State = CheckRunning
	check.Outcome = ""
	check.UpdatedAt = attempt.StartedAt
	check.Version++
	return nil
}

// CompleteAttempt applies one exact terminal result or classifies its replay.
func (check *Check) CompleteAttempt(attemptID id.Attempt, fence uint64, observations []Observation, at time.Time) (string, error) {
	attempt, disposition, err := check.currentAttempt(attemptID, fence, at)
	if err != nil {
		return disposition, err
	}
	if len(observations) == 0 || len(observations) > maximumSignals {
		return "", ErrInvalidCheck
	}
	copyOfObservations := make([]Observation, len(observations))
	for index, observation := range observations {
		if err := validateObservation(observation, *attempt, at); err != nil {
			return "", err
		}
		copyOfObservations[index] = cloneObservation(observation)
	}
	digest, err := digestResult(copyOfObservations, nil)
	if err != nil {
		return "", err
	}
	if disposition == "duplicate" {
		if attempt.resultDigest != digest {
			check.diagnostics[len(check.diagnostics)-1].Kind = "conflict"
			return "conflict", ErrAttemptConflict
		}
		return "duplicate", nil
	}
	attempt.State, attempt.FinishedAt = AttemptCompleted, at
	attempt.Observations, attempt.resultDigest = copyOfObservations, digest
	check.State, check.Outcome = CheckCompleted, aggregateOutcome(copyOfObservations)
	check.UpdatedAt, check.Version = at, check.Version+1
	return "applied", nil
}

// FailAttempt records an operational failure without producing not_passed.
func (check *Check) FailAttempt(attemptID id.Attempt, fence uint64, failure Failure, at time.Time) (string, error) {
	attempt, disposition, err := check.currentAttempt(attemptID, fence, at)
	if err != nil {
		return disposition, err
	}
	if !safeExecutionToken(failure.Class, 100) || !safeExecutionToken(failure.Code, 100) ||
		(failure.Retry != RetryNever && failure.Retry != RetryBackoff && failure.Retry != RetryReconcile) || failure.RetryAfter < 0 {
		return "", ErrInvalidCheck
	}
	copyOfFailure := failure
	digest, err := digestResult(nil, &copyOfFailure)
	if err != nil {
		return "", err
	}
	if disposition == "duplicate" {
		if attempt.resultDigest != digest {
			check.diagnostics[len(check.diagnostics)-1].Kind = "conflict"
			return "conflict", ErrAttemptConflict
		}
		return "duplicate", nil
	}
	attempt.Failure, attempt.FinishedAt, attempt.resultDigest = &copyOfFailure, at, digest
	switch failure.Class {
	case "deadline_exceeded":
		attempt.State, check.State = AttemptTimedOut, CheckTimedOut
	case "cancelled":
		attempt.State, check.State = AttemptCancelled, CheckCancelled
	default:
		attempt.State, check.State = AttemptFailed, CheckFailed
	}
	check.Outcome = ""
	check.UpdatedAt, check.Version = at, check.Version+1
	return "applied", nil
}

// Cancel marks the current running attempt cancelled with an owned stable code.
func (check *Check) Cancel(at time.Time) error {
	if check == nil || check.State != CheckRunning || !utcNonZero(at) || at.Before(check.UpdatedAt) {
		return ErrInvalidCheck
	}
	_, err := check.FailAttempt(check.attempts[len(check.attempts)-1].ID,
		check.attempts[len(check.attempts)-1].Fence,
		Failure{Class: "cancelled", Code: "verification_cancelled", Retry: RetryNever}, at)
	return err
}

// Attempts returns a defensive immutable-history copy.
func (check Check) Attempts() []Attempt {
	result := make([]Attempt, len(check.attempts))
	for index := range check.attempts {
		result[index] = cloneAttempt(check.attempts[index])
	}
	return result
}

// Diagnostics returns safe reconciliation facts in append order.
func (check Check) Diagnostics() []Diagnostic { return slices.Clone(check.diagnostics) }

// ResultDigest returns the canonical semantic terminal digest, when present.
func (attempt Attempt) ResultDigest() string { return attempt.resultDigest }

// RestoreAttempt validates one attempt loaded from durable state, including its semantic digest.
func RestoreAttempt(attempt Attempt, resultDigest string) (Attempt, error) {
	attempt.resultDigest = resultDigest
	if err := validateStoredAttempt(attempt, attempt.Number); err != nil {
		return Attempt{}, err
	}
	return cloneAttempt(attempt), nil
}

func (check *Check) currentAttempt(attemptID id.Attempt, fence uint64, at time.Time) (*Attempt, string, error) {
	if check == nil || attemptID.IsZero() || fence == 0 || !utcNonZero(at) || len(check.attempts) == 0 {
		return nil, "", ErrInvalidCheck
	}
	current := &check.attempts[len(check.attempts)-1]
	if current.ID.String() != attemptID.String() || current.Fence != fence || check.State != CheckRunning {
		kind := "stale"
		if current.ID.String() == attemptID.String() && current.Fence == fence && current.State != AttemptRunning {
			kind = "duplicate"
		}
		check.diagnostics = append(check.diagnostics, Diagnostic{AttemptID: attemptID, Kind: kind, Code: "terminal_delivery", RecordedAt: at})
		check.UpdatedAt, check.Version = at, check.Version+1
		if kind == "duplicate" {
			return current, kind, nil
		}
		return nil, kind, ErrStaleAttempt
	}
	if at.Before(current.StartedAt) {
		return nil, "", ErrInvalidCheck
	}
	return current, "apply", nil
}

func validateObservation(value Observation, attempt Attempt, at time.Time) error {
	if value.ID.IsZero() || value.AttemptID.String() != attempt.ID.String() || value.RunnerKind != attempt.RunnerKind ||
		value.Provenance != attempt.Provenance || !utcNonZero(value.RecordedAt) || value.RecordedAt.After(at) ||
		!safeExecutionToken(value.Signal.Name, 128) || (value.Signal.Outcome != SignalSatisfied &&
		value.Signal.Outcome != SignalNotSatisfied && value.Signal.Outcome != SignalInconclusive) ||
		len(value.Signal.ReasonCodes) > maximumReasonCodes {
		return ErrInvalidCheck
	}
	seen := make(map[string]struct{}, len(value.Signal.ReasonCodes))
	for _, code := range value.Signal.ReasonCodes {
		if !safeExecutionToken(code, 100) {
			return ErrInvalidCheck
		}
		if _, exists := seen[code]; exists {
			return ErrInvalidCheck
		}
		seen[code] = struct{}{}
	}
	return nil
}

func validateProvenance(value Provenance) error {
	if !safeExecutionToken(value.RunnerID, 128) || !safeExecutionToken(value.RunnerVersion, 64) ||
		!digestToken(value.PackageDigest) || value.ContractMajor == 0 || !digestToken(value.RequestDigest) ||
		!digestToken(value.Configuration) {
		return ErrInvalidCheck
	}
	return nil
}

func validCheckState(state CheckState) bool {
	switch state {
	case CheckQueued, CheckRunning, CheckAwaitingInput, CheckAwaitingProvider, CheckCompleted,
		CheckSkippedByPolicy, CheckTimedOut, CheckCancelled, CheckFailed:
		return true
	default:
		return false
	}
}

func validCheckOutcome(outcome CheckOutcome) bool {
	return outcome == CheckPassed || outcome == CheckNotPassed || outcome == CheckInconclusive
}

func validateStoredAttempt(attempt Attempt, expectedNumber uint32) error {
	if attempt.ID.IsZero() || attempt.Number != expectedNumber || attempt.Fence == 0 ||
		(attempt.RunnerKind != RunnerProvider && attempt.RunnerKind != RunnerModel) ||
		validateProvenance(attempt.Provenance) != nil || !utcNonZero(attempt.StartedAt) ||
		!utcNonZero(attempt.Deadline) || !attempt.Deadline.After(attempt.StartedAt) {
		return ErrInvalidCheck
	}
	if attempt.State == AttemptRunning {
		if !attempt.FinishedAt.IsZero() || attempt.Failure != nil || len(attempt.Observations) != 0 || attempt.resultDigest != "" {
			return ErrInvalidCheck
		}
		return nil
	}
	if attempt.State != AttemptCompleted && attempt.State != AttemptFailed && attempt.State != AttemptTimedOut && attempt.State != AttemptCancelled {
		return ErrInvalidCheck
	}
	if !utcNonZero(attempt.FinishedAt) || attempt.FinishedAt.Before(attempt.StartedAt) || !digestToken(attempt.resultDigest) {
		return ErrInvalidCheck
	}
	if attempt.State == AttemptCompleted {
		if attempt.Failure != nil || len(attempt.Observations) == 0 {
			return ErrInvalidCheck
		}
		for _, observation := range attempt.Observations {
			if err := validateObservation(observation, attempt, attempt.FinishedAt); err != nil {
				return err
			}
		}
	} else if attempt.Failure == nil || len(attempt.Observations) != 0 ||
		!safeExecutionToken(attempt.Failure.Class, 100) || !safeExecutionToken(attempt.Failure.Code, 100) ||
		(attempt.Failure.Retry != RetryNever && attempt.Failure.Retry != RetryBackoff && attempt.Failure.Retry != RetryReconcile) ||
		attempt.Failure.RetryAfter < 0 {
		return ErrInvalidCheck
	}
	digest, err := digestResult(attempt.Observations, attempt.Failure)
	if err != nil || digest != attempt.resultDigest {
		return ErrInvalidCheck
	}
	return nil
}

func validateCheckAttemptState(state CheckState, attempts []Attempt) error {
	if len(attempts) == 0 {
		if state == CheckQueued || state == CheckSkippedByPolicy {
			return nil
		}
		return ErrInvalidCheck
	}
	latest := attempts[len(attempts)-1]
	for index := range attempts[:len(attempts)-1] {
		if attempts[index].State == AttemptRunning {
			return ErrInvalidCheck
		}
	}
	switch state {
	case CheckRunning, CheckAwaitingProvider:
		if latest.State != AttemptRunning {
			return ErrInvalidCheck
		}
	case CheckCompleted:
		if latest.State != AttemptCompleted {
			return ErrInvalidCheck
		}
	case CheckFailed:
		if latest.State != AttemptFailed {
			return ErrInvalidCheck
		}
	case CheckTimedOut:
		if latest.State != AttemptTimedOut {
			return ErrInvalidCheck
		}
	case CheckCancelled:
		if latest.State != AttemptCancelled {
			return ErrInvalidCheck
		}
	}
	return nil
}

func aggregateOutcome(observations []Observation) CheckOutcome {
	result := CheckPassed
	for _, observation := range observations {
		if observation.Signal.Outcome == SignalNotSatisfied {
			return CheckNotPassed
		}
		if observation.Signal.Outcome == SignalInconclusive {
			result = CheckInconclusive
		}
	}
	return result
}

func cloneAttempt(value Attempt) Attempt {
	copyOf := value
	if value.Failure != nil {
		failure := *value.Failure
		copyOf.Failure = &failure
	}
	copyOf.Observations = make([]Observation, len(value.Observations))
	for index := range value.Observations {
		copyOf.Observations[index] = cloneObservation(value.Observations[index])
	}
	return copyOf
}

func cloneObservation(value Observation) Observation {
	copyOf := value
	if value.Signal.ReasonCodes == nil {
		copyOf.Signal.ReasonCodes = []string{}
	} else {
		copyOf.Signal.ReasonCodes = slices.Clone(value.Signal.ReasonCodes)
	}
	return copyOf
}

func digestResult(observations []Observation, failure *Failure) (string, error) {
	type semanticObservation struct {
		AttemptID  string
		RunnerKind RunnerKind
		Provenance Provenance
		Signal     Signal
		RecordedAt time.Time
	}
	semantic := make([]semanticObservation, len(observations))
	for index, observation := range observations {
		semantic[index] = semanticObservation{AttemptID: observation.AttemptID.String(), RunnerKind: observation.RunnerKind,
			Provenance: observation.Provenance, Signal: observation.Signal,
			RecordedAt: observation.RecordedAt.UTC().Truncate(time.Microsecond)}
	}
	encoded, err := json.Marshal(struct {
		Observations []semanticObservation
		Failure      *Failure
	}{semantic, failure})
	if err != nil {
		return "", fmt.Errorf("verification: encode result: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func utcNonZero(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

func digestToken(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil && strings.ToLower(value) == value
}

func safeExecutionToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') ||
			character == '.' || character == '_' || character == '-' || character == ':' {
			continue
		}
		return false
	}
	return true
}
