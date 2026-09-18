package verification

import (
	"context"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// CaptureOutcomeState is the subject-safe projection of workflow and decision state.
// It deliberately excludes policy reasons, assurance, provider data, and evidence data.
type CaptureOutcomeState string

const (
	// CaptureOutcomeCaptureRequired means the session still accepts subject capture.
	CaptureOutcomeCaptureRequired CaptureOutcomeState = "capture_required"
	// CaptureOutcomeProcessing means Core is still evaluating the verification.
	CaptureOutcomeProcessing CaptureOutcomeState = "processing"
	// CaptureOutcomeActionRequired means Core requires another subject action.
	CaptureOutcomeActionRequired CaptureOutcomeState = "action_required"
	// CaptureOutcomeVerified means Core completed with a verified decision.
	CaptureOutcomeVerified CaptureOutcomeState = "verified"
	// CaptureOutcomeNotVerified means Core completed with a not-verified decision.
	CaptureOutcomeNotVerified CaptureOutcomeState = "not_verified"
	// CaptureOutcomeInconclusive means Core completed without a conclusive decision.
	CaptureOutcomeInconclusive CaptureOutcomeState = "inconclusive"
	// CaptureOutcomeCancelled means the verification was cancelled.
	CaptureOutcomeCancelled CaptureOutcomeState = "cancelled"
	// CaptureOutcomeExpired means the verification expired.
	CaptureOutcomeExpired CaptureOutcomeState = "expired"
	// CaptureOutcomeFailed means the verification failed.
	CaptureOutcomeFailed CaptureOutcomeState = "failed"
)

// CaptureOutcomeRecord is the persistence result used to build a safe subject projection.
// DecisionOutcome is populated only for a completed session.
type CaptureOutcomeRecord struct {
	VerificationID  id.Verification
	SessionState    SessionState
	SessionVersion  int64
	UpdatedAt       time.Time
	DecisionOutcome policy.Outcome
}

// CaptureOutcome is the minimum authoritative state Capture Web may present to a subject.
type CaptureOutcome struct {
	VerificationID id.Verification
	State          CaptureOutcomeState
	SessionVersion int64
	UpdatedAt      time.Time
}

// CaptureOutcomeRepository is the narrow subject-outcome read boundary.
type CaptureOutcomeRepository interface {
	FindCaptureOutcome(context.Context, tenant.Scope, id.Verification) (CaptureOutcomeRecord, error)
}

// CaptureOutcomeService correlates an authenticated outcome context with its safe outcome.
type CaptureOutcomeService struct{ repository CaptureOutcomeRepository }

// NewCaptureOutcomeService constructs the read-only subject-outcome application service.
func NewCaptureOutcomeService(repository CaptureOutcomeRepository) (*CaptureOutcomeService, error) {
	if repository == nil {
		return nil, errors.New("verification: capture outcome repository is required")
	}
	return &CaptureOutcomeService{repository: repository}, nil
}

// Find returns only the authenticated session's subject-safe authoritative state.
func (service *CaptureOutcomeService) Find(
	ctx context.Context,
	authority OutcomeContext,
) (CaptureOutcome, error) {
	record, err := service.repository.FindCaptureOutcome(
		ctx,
		authority.TenantScope(),
		authority.VerificationID(),
	)
	if err != nil {
		return CaptureOutcome{}, err
	}
	if record.VerificationID.String() != authority.VerificationID().String() {
		return CaptureOutcome{}, ErrSessionNotFound
	}
	return projectCaptureOutcome(record)
}

func projectCaptureOutcome(record CaptureOutcomeRecord) (CaptureOutcome, error) {
	if record.VerificationID.IsZero() || !record.SessionState.Valid() ||
		record.SessionVersion < 1 || !utcNonZero(record.UpdatedAt) {
		return CaptureOutcome{}, ErrSessionConflict
	}
	state, err := captureOutcomeState(record.SessionState, record.DecisionOutcome)
	if err != nil {
		return CaptureOutcome{}, err
	}
	return CaptureOutcome{
		VerificationID: record.VerificationID,
		State:          state,
		SessionVersion: record.SessionVersion,
		UpdatedAt:      record.UpdatedAt,
	}, nil
}

func captureOutcomeState(state SessionState, outcome policy.Outcome) (CaptureOutcomeState, error) {
	switch state {
	case SessionStateCreated, SessionStateCollecting:
		if outcome == "" {
			return CaptureOutcomeCaptureRequired, nil
		}
	case SessionStateAwaitingInput:
		if outcome == "" {
			return CaptureOutcomeActionRequired, nil
		}
	case SessionStateProcessing, SessionStateAwaitingExternal, SessionStateManualReview:
		if outcome == "" {
			return CaptureOutcomeProcessing, nil
		}
	case SessionStateCompleted:
		switch outcome {
		case policy.OutcomeVerified:
			return CaptureOutcomeVerified, nil
		case policy.OutcomeNotVerified:
			return CaptureOutcomeNotVerified, nil
		case policy.OutcomeInconclusive:
			return CaptureOutcomeInconclusive, nil
		}
	case SessionStateCancelled:
		if outcome == "" {
			return CaptureOutcomeCancelled, nil
		}
	case SessionStateExpired:
		if outcome == "" {
			return CaptureOutcomeExpired, nil
		}
	case SessionStateFailed:
		if outcome == "" {
			return CaptureOutcomeFailed, nil
		}
	}
	return "", ErrSessionConflict
}
