package verification

import (
	"math"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	// SessionStateCreated means policy and regional placement have been resolved.
	SessionStateCreated SessionState = "created"
	// SessionStateAwaitingInput means the workflow needs further subject input.
	SessionStateAwaitingInput SessionState = "awaiting_input"
	// SessionStateProcessing means checks and deterministic policy are executing.
	SessionStateProcessing SessionState = "processing"
	// SessionStateAwaitingExternal means an asynchronous dependency is pending.
	SessionStateAwaitingExternal SessionState = "awaiting_external"
	// SessionStateManualReview means a bounded human finding is required.
	SessionStateManualReview SessionState = "manual_review"
	// SessionStateCompleted means an immutable identity decision has been appended.
	SessionStateCompleted SessionState = "completed"
	// SessionStateCancelled means the tenant or subject ended the workflow.
	SessionStateCancelled SessionState = "cancelled"
	// SessionStateExpired means the completion window elapsed.
	SessionStateExpired SessionState = "expired"
	// SessionStateFailed means an operational failure prevented completion.
	SessionStateFailed SessionState = "failed"
)

// Valid reports whether state belongs to the version-one lifecycle vocabulary.
func (state SessionState) Valid() bool {
	switch state {
	case SessionStateCreated, SessionStateCollecting, SessionStateAwaitingInput,
		SessionStateProcessing, SessionStateAwaitingExternal, SessionStateManualReview,
		SessionStateCompleted, SessionStateCancelled, SessionStateExpired, SessionStateFailed:
		return true
	default:
		return false
	}
}

// Terminal reports whether further workflow transitions are prohibited.
func (state SessionState) Terminal() bool {
	return state == SessionStateCompleted || state == SessionStateCancelled ||
		state == SessionStateExpired || state == SessionStateFailed
}

// Lifecycle is the reference-only state needed to validate a session transition.
// It carries no identity outcome. Decision meaning remains owned by policy.
// Failure is set only while State is failed.
type Lifecycle struct {
	State      SessionState
	Version    int64
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ExpiresAt  time.Time
	DecisionID id.Decision
	Failure    SessionFailure
}

// LifecycleCommand is one immutable transition request. EventID is its durable
// replay identity; callers must reuse the entire command after uncertain commit.
// ActorID is a verified principal reference, not a free-form name or credential.
// Supplying it does not replace authorisation by the owning application service.
// OccurredAt must be UTC with microsecond precision, matching PostgreSQL storage.
// Failure is required exactly when Target is failed and is part of replay
// identity: repeating an event with a different reason is a different command.
type LifecycleCommand struct {
	EventID         id.Event
	VerificationID  id.Verification
	ExpectedVersion int64
	Target          SessionState
	DecisionID      id.Decision
	ActorID         string
	OccurredAt      time.Time
	Failure         SessionFailure
}

// LifecycleReceipt is the original committed transition, not the latest session.
// Replaying a command returns this same receipt after subsequent transitions.
type LifecycleReceipt struct {
	EventID        id.Event
	VerificationID id.Verification
	From           SessionState
	To             SessionState
	Version        int64
	DecisionID     id.Decision
	OccurredAt     time.Time
}

// Validate rejects unbounded attribution, invalid references, decision
// references attached to anything other than decision-authored completion, and
// a failure attached to anything other than the terminal failed state.
func (command LifecycleCommand) Validate() error {
	failureRequired := command.Target == SessionStateFailed
	if command.EventID.IsZero() || command.VerificationID.IsZero() ||
		command.ExpectedVersion < 1 || command.ExpectedVersion == math.MaxInt64 ||
		!command.Target.Valid() || !utcNonZero(command.OccurredAt) || command.OccurredAt.Nanosecond()%1000 != 0 ||
		(command.Target == SessionStateCompleted) == command.DecisionID.IsZero() ||
		failureRequired != (command.Failure.Validate() == nil) ||
		!lifecycleActor(command.ActorID) {
		return ErrSessionConflict
	}
	return nil
}

// AdvanceLifecycle applies the v0.6 transition graph without performing effects.
// Owning applications validate the cause (capture, callback, policy, review,
// cancellation or failure) and current authority before calling persistence.
// A decision reference is necessary for completion but is not proof of authority;
// persistence must verify the referenced decision belongs to this verification.
func AdvanceLifecycle(current Lifecycle, command LifecycleCommand) (Lifecycle, error) {
	if err := command.Validate(); err != nil {
		return Lifecycle{}, err
	}
	if !current.State.Valid() || current.State.Terminal() || current.Failure != (SessionFailure{}) ||
		current.Version != command.ExpectedVersion || !current.DecisionID.IsZero() ||
		!utcNonZero(current.CreatedAt) || !utcNonZero(current.UpdatedAt) || !utcNonZero(current.ExpiresAt) ||
		current.UpdatedAt.Before(current.CreatedAt) || !current.ExpiresAt.After(current.CreatedAt) ||
		command.OccurredAt.Before(current.UpdatedAt) || !lifecycleEdge(current.State, command.Target) {
		return Lifecycle{}, ErrSessionConflict
	}
	if command.Target == SessionStateExpired {
		if command.OccurredAt.Before(current.ExpiresAt) {
			return Lifecycle{}, ErrSessionConflict
		}
	} else if !command.OccurredAt.Before(current.ExpiresAt) {
		return Lifecycle{}, ErrSessionConflict
	}
	current.State = command.Target
	current.Version++
	current.UpdatedAt = command.OccurredAt
	current.DecisionID = command.DecisionID
	current.Failure = command.Failure
	return current, nil
}

func lifecycleEdge(from, to SessionState) bool {
	if to == SessionStateCancelled || to == SessionStateFailed {
		return from.Valid() && !from.Terminal()
	}
	if to == SessionStateExpired {
		return from.Valid() && !from.Terminal() && from != SessionStateCreated
	}
	switch from {
	case SessionStateCreated:
		return to == SessionStateCollecting
	case SessionStateCollecting:
		return to == SessionStateAwaitingInput || to == SessionStateProcessing
	case SessionStateAwaitingInput:
		return to == SessionStateCollecting || to == SessionStateManualReview
	case SessionStateProcessing:
		return to == SessionStateAwaitingExternal || to == SessionStateAwaitingInput ||
			to == SessionStateManualReview || to == SessionStateCompleted
	case SessionStateAwaitingExternal:
		return to == SessionStateProcessing
	case SessionStateManualReview:
		return to == SessionStateAwaitingInput || to == SessionStateCompleted
	default:
		return false
	}
}

func lifecycleActor(value string) bool {
	if _, err := id.ParseAPIKey(value); err == nil {
		return true
	}
	if _, err := id.ParseCaptureToken(value); err == nil {
		return true
	}
	_, err := id.ParseTask(value)
	return err == nil
}
