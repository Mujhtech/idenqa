package verification

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// ProviderResultFingerprint identifies exact safe provider delivery content for
// inbox deduplication. The transient document observation is never bound into
// the digest, so a result with and without it fingerprints identically.
func ProviderResultFingerprint(result providerv1.Result) (string, error) {
	result.Document = nil
	return resultFingerprint(result)
}

// ModelResultFingerprint identifies exact safe model delivery content for inbox deduplication.
func ModelResultFingerprint(result modelv1.Result) (string, error) {
	return resultFingerprint(result)
}

func resultFingerprint(result any) (string, error) {
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", fmt.Errorf("encode runner result fingerprint: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

// ObservationIDGenerator is the narrow identity capability used by normalisation.
type ObservationIDGenerator interface {
	NewObservation() (id.Observation, error)
}

// NormaliseProviderResult validates an exact provider binding and removes
// contract-specific types. Optional deterministic document post-processing
// appends bounded Core signals without changing provider signal meaning.
func NormaliseProviderResult(
	result providerv1.Result,
	attempt Attempt,
	identifiers ObservationIDGenerator,
	options ...ResultOption,
) ([]Observation, *Failure, error) {
	if identifiers == nil || result.AttemptID != attempt.ID.String() ||
		!providerv1.CurrentVersion.Accepts(result.Contract) || !utcNonZero(result.CompletedAt) ||
		result.CompletedAt.Before(attempt.StartedAt) || result.CompletedAt.After(attempt.Deadline) {
		return nil, nil, ErrInvalidCheck
	}
	if result.Outcome == providerv1.ResultOutcomeFailed {
		if result.Failure == nil || len(result.Signals) != 0 {
			return nil, nil, ErrInvalidCheck
		}
		failure := Failure{Class: string(result.Failure.Class), Code: result.Failure.Code,
			Retry: RetryDisposition(result.Failure.Retry), RetryAfter: result.Failure.RetryAfter}
		if failure.Retry == RetryBackoff || failure.Retry == RetryReconcile {
			failure.RetryAfter = min(max(failure.RetryAfter, time.Second), time.Hour)
		}
		return nil, &failure, nil
	}
	if result.Outcome != providerv1.ResultOutcomeCompleted || result.Failure != nil {
		return nil, nil, ErrInvalidCheck
	}
	consumed, err := ConsumeProviderDocument(result)
	if err != nil {
		return nil, nil, err
	}
	result = consumed
	signals := make([]Signal, len(result.Signals))
	for index, signal := range result.Signals {
		signals[index] = Signal{Name: signal.Name, Outcome: SignalOutcome(signal.Outcome), ReasonCodes: signal.ReasonCodes}
	}
	resolved, err := resolveResultOptions(options)
	if err != nil {
		return nil, nil, err
	}
	derived, err := documentSignals(resolved, result.CompletedAt)
	if err != nil {
		return nil, nil, err
	}
	signals = mergeDocumentSignals(signals, derived)
	return observations(attempt, result.CompletedAt, signals, identifiers)
}

// NormaliseModelResult validates an exact model binding and removes
// contract-specific types. Optional deterministic document post-processing
// appends bounded Core signals without changing model signal meaning.
func NormaliseModelResult(
	result modelv1.Result,
	attempt Attempt,
	identifiers ObservationIDGenerator,
	options ...ResultOption,
) ([]Observation, *Failure, error) {
	if identifiers == nil || result.AttemptID != attempt.ID.String() ||
		!modelv1.CurrentVersion.Accepts(result.Contract) || !utcNonZero(result.CompletedAt) ||
		result.CompletedAt.Before(attempt.StartedAt) || result.CompletedAt.After(attempt.Deadline) {
		return nil, nil, ErrInvalidCheck
	}
	if result.Outcome == modelv1.ResultOutcomeFailed {
		if result.Failure == nil || len(result.Signals) != 0 {
			return nil, nil, ErrInvalidCheck
		}
		failure := Failure{Class: string(result.Failure.Class), Code: result.Failure.Code,
			Retry: RetryDisposition(result.Failure.Retry), RetryAfter: result.Failure.RetryAfter}
		return nil, &failure, nil
	}
	if result.Outcome != modelv1.ResultOutcomeCompleted || result.Failure != nil {
		return nil, nil, ErrInvalidCheck
	}
	signals := make([]Signal, len(result.Signals))
	for index, signal := range result.Signals {
		signals[index] = Signal{Name: signal.Name, Outcome: SignalOutcome(signal.Outcome), ReasonCodes: signal.ReasonCodes}
	}
	resolved, err := resolveResultOptions(options)
	if err != nil {
		return nil, nil, err
	}
	derived, err := documentSignals(resolved, result.CompletedAt)
	if err != nil {
		return nil, nil, err
	}
	signals = mergeDocumentSignals(signals, derived)
	return observations(attempt, result.CompletedAt, signals, identifiers)
}

func observations(attempt Attempt, completedAt time.Time, signals []Signal, identifiers ObservationIDGenerator) ([]Observation, *Failure, error) {
	if len(signals) == 0 || len(signals) > maximumSignals {
		return nil, nil, ErrInvalidCheck
	}
	result := make([]Observation, len(signals))
	for index, signal := range signals {
		identifier, err := identifiers.NewObservation()
		if err != nil {
			return nil, nil, fmt.Errorf("generate observation id: %w", err)
		}
		result[index] = Observation{ID: identifier, AttemptID: attempt.ID, RunnerKind: attempt.RunnerKind,
			Provenance: attempt.Provenance, Signal: signal, RecordedAt: completedAt}
		if err := validateObservation(result[index], attempt, completedAt); err != nil {
			return nil, nil, err
		}
	}
	return result, nil, nil
}

// ApplyProviderResult shares the callback, polling, and direct execution result path.
func ApplyProviderResult(check *Check, result providerv1.Result, identifiers ObservationIDGenerator, fence uint64, options ...ResultOption) (string, error) {
	attempt, err := attemptForResult(check, RunnerProvider, result.AttemptID)
	if err != nil {
		return "", err
	}
	observations, failure, err := NormaliseProviderResult(result, attempt, identifiers, options...)
	if err != nil {
		return "", err
	}
	if failure != nil {
		return check.FailAttempt(attempt.ID, fence, *failure, result.CompletedAt)
	}
	return check.CompleteAttempt(attempt.ID, fence, observations, result.CompletedAt)
}

// ApplyModelResult shares the synchronous and asynchronous model result path.
func ApplyModelResult(check *Check, result modelv1.Result, identifiers ObservationIDGenerator, fence uint64, options ...ResultOption) (string, error) {
	attempt, err := attemptForResult(check, RunnerModel, result.AttemptID)
	if err != nil {
		return "", err
	}
	observations, failure, err := NormaliseModelResult(result, attempt, identifiers, options...)
	if err != nil {
		return "", err
	}
	if failure != nil {
		return check.FailAttempt(attempt.ID, fence, *failure, result.CompletedAt)
	}
	return check.CompleteAttempt(attempt.ID, fence, observations, result.CompletedAt)
}

func attemptForResult(check *Check, kind RunnerKind, encodedAttemptID string) (Attempt, error) {
	if check == nil || len(check.attempts) == 0 {
		return Attempt{}, ErrInvalidCheck
	}
	identifier, err := id.ParseAttempt(encodedAttemptID)
	if err != nil {
		return Attempt{}, ErrInvalidCheck
	}
	for index := range check.attempts {
		attempt := cloneAttempt(check.attempts[index])
		if attempt.ID.String() != identifier.String() {
			continue
		}
		if attempt.RunnerKind != kind {
			return Attempt{}, errors.Join(ErrInvalidCheck, errors.New("runner kind mismatch"))
		}
		return attempt, nil
	}
	return Attempt{}, ErrInvalidCheck
}
