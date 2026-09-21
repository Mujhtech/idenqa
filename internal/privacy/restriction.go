package privacy

import (
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// RestrictionState is the closed restriction lifecycle vocabulary.
type RestrictionState string

// The selected restriction states.
const (
	RestrictionActive RestrictionState = "active"
	RestrictionLifted RestrictionState = "lifted"
)

// Valid reports whether the restriction state is in the selected vocabulary.
func (value RestrictionState) Valid() bool {
	return value == RestrictionActive || value == RestrictionLifted
}

// RestrictionReason is a bounded, auditable lift or creation reason.
type RestrictionReason string

// The selected restriction reason codes.
const (
	RestrictionRequestedBySubject RestrictionReason = "subject_request"
	RestrictionAccuracyDispute    RestrictionReason = "accuracy_dispute"
	RestrictionLiftedByTenant     RestrictionReason = "lifted_by_tenant"
	RestrictionLiftedBySubject    RestrictionReason = "lifted_by_subject"
	RestrictionExpired            RestrictionReason = "restriction_expired"
)

// Valid reports whether the reason code is in the selected vocabulary.
func (value RestrictionReason) Valid() bool {
	switch value {
	case RestrictionRequestedBySubject, RestrictionAccuracyDispute, RestrictionLiftedByTenant, RestrictionLiftedBySubject, RestrictionExpired:
		return true
	default:
		return false
	}
}

// RestrictionScope distinguishes a subject-blocking restriction from a
// purpose-scoped objection record consumed as policy input.
type RestrictionScope string

// The selected restriction scopes.
const (
	RestrictionScopeSubject RestrictionScope = "subject"
	RestrictionScopePurpose RestrictionScope = "purpose"
)

// Valid reports whether the restriction scope is in the selected vocabulary.
func (value RestrictionScope) Valid() bool {
	return value == RestrictionScopeSubject || value == RestrictionScopePurpose
}

// Restriction blocks new subject-scoped processing until explicitly lifted.
// In-flight external operations and completed decisions are untouched.
type Restriction struct {
	ID             id.PrivacyRestriction
	RequestID      id.PrivacyRequest
	SubjectID      string
	Purpose        string
	Scope          RestrictionScope
	ReasonCode     RestrictionReason
	Region         string
	State          RestrictionState
	StartsAt       time.Time
	LiftedAt       time.Time
	LiftReasonCode RestrictionReason
	Version        int64
}

// NewRestriction creates one active subject-scoped restriction for an approved request.
func NewRestriction(identifier id.PrivacyRestriction, requestID id.PrivacyRequest, subjectID, purpose string, reason RestrictionReason, region string, startsAt time.Time) (Restriction, error) {
	restriction := Restriction{
		ID: identifier, RequestID: requestID, SubjectID: subjectID, Purpose: purpose,
		Scope: RestrictionScopeSubject, ReasonCode: reason, Region: region, State: RestrictionActive,
		StartsAt: startsAt, Version: 1,
	}
	if restriction.Validate() != nil {
		return Restriction{}, ErrInvalid
	}
	return restriction, nil
}

// NewObjection creates one active purpose-scoped objection record. It is
// consumed as policy input for new processing and never blocks existing work.
func NewObjection(identifier id.PrivacyRestriction, requestID id.PrivacyRequest, subjectID, purpose string, reason RestrictionReason, region string, startsAt time.Time) (Restriction, error) {
	objection := Restriction{
		ID: identifier, RequestID: requestID, SubjectID: subjectID, Purpose: purpose,
		Scope: RestrictionScopePurpose, ReasonCode: reason, Region: region, State: RestrictionActive,
		StartsAt: startsAt, Version: 1,
	}
	if objection.Validate() != nil {
		return Restriction{}, ErrInvalid
	}
	return objection, nil
}

// Validate checks bounded restriction meaning and UTC times.
func (restriction Restriction) Validate() error {
	if restriction.ID.IsZero() || restriction.RequestID.IsZero() || !token(restriction.SubjectID, 200) ||
		(restriction.Purpose != "" && !token(restriction.Purpose, 128)) || !restriction.Scope.Valid() ||
		!restriction.ReasonCode.Valid() ||
		!validRegion(restriction.Region) || !restriction.State.Valid() || restriction.Version < 1 ||
		restriction.StartsAt.IsZero() || restriction.StartsAt.Location() != time.UTC {
		return ErrInvalid
	}
	if restriction.Scope == RestrictionScopePurpose && restriction.Purpose == "" {
		return ErrInvalid
	}
	if restriction.Scope == RestrictionScopeSubject && restriction.Purpose != "" {
		return ErrInvalid
	}
	if restriction.State == RestrictionLifted {
		if !restriction.LiftReasonCode.Valid() || restriction.LiftedAt.IsZero() || restriction.LiftedAt.Location() != time.UTC ||
			restriction.LiftedAt.Before(restriction.StartsAt) {
			return ErrInvalid
		}
	}
	return nil
}

// Lift ends the restriction exactly once with an audited reason. Replay with
// the same reason is idempotent; a conflicting replay fails.
func (restriction Restriction) Lift(reason RestrictionReason, at time.Time) (Restriction, error) {
	if restriction.Validate() != nil || !reason.Valid() || at.IsZero() || at.Location() != time.UTC || at.Before(restriction.StartsAt) {
		return Restriction{}, ErrInvalid
	}
	if restriction.State == RestrictionLifted {
		if restriction.LiftReasonCode == reason {
			return restriction, nil
		}
		return Restriction{}, ErrConflict
	}
	next := restriction
	next.State, next.LiftReasonCode, next.LiftedAt, next.Version = RestrictionLifted, reason, at, restriction.Version+1
	return next, nil
}

// ActiveAt reports whether the restriction blocks new processing at an instant.
func (restriction Restriction) ActiveAt(at time.Time) bool {
	return restriction.Validate() == nil && restriction.State == RestrictionActive && !at.Before(restriction.StartsAt)
}

// AuditTime returns the most recent lifecycle instant for audit envelopes.
func (restriction Restriction) AuditTime() time.Time {
	if !restriction.LiftedAt.IsZero() {
		return restriction.LiftedAt
	}
	return restriction.StartsAt
}
