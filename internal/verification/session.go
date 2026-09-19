package verification

import (
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
)

var (
	// ErrSessionNotFound deliberately also covers cross-tenant misses.
	ErrSessionNotFound = errors.New("verification: session not found")
	// ErrSessionConflict identifies an invalid or stale session transition.
	ErrSessionConflict = errors.New("verification: session conflict")
	// ErrProfileUnavailable means a session cannot snapshot the selected profile.
	ErrProfileUnavailable = errors.New("verification: capture profile is unavailable for session creation")
)

// SessionState is the lifecycle state of one verification session.
type SessionState string

const (
	// SessionStateCollecting means the session accepts profile-approved capture activity.
	SessionStateCollecting SessionState = "collecting"

	// OperationCreateVerification is the durable idempotency operation name.
	OperationCreateVerification = "verifications.create"
)

// SessionCreateMutation contains generated identities and bounded lifetimes
// required for one atomic verification-session creation transaction.
type SessionCreateMutation struct {
	SessionID          id.Verification
	CaptureTokenID     id.CaptureToken
	OutcomeTokenID     id.OutcomeToken
	EventID            id.Event
	ProfileID          id.Profile
	PolicyID           id.Policy
	DecisionID         id.Decision
	Region             string
	Actor              id.APIKey
	CaptureKeyVersion  access.CaptureTokenKeyVersion
	OutcomeKeyVersion  access.OutcomeTokenKeyVersion
	CreatedAt          time.Time
	SessionExpiresAt   time.Time
	CaptureTokenExpiry time.Time
	OutcomeTokenExpiry time.Time
	Idempotency        idempotency.Request
}

// SessionCreation is the safe reconstructable result of session creation. It
// contains the non-secret credential record, never the signed bearer token.
type SessionCreation struct {
	Session           Session
	Credential        access.CaptureCredential
	OutcomeCredential access.OutcomeCredential
}

// Session is a tenant-owned verification aggregate with an immutable copy of
// the exact capture requirements selected at creation.
type Session struct {
	id              id.Verification
	tenantID        id.Tenant
	state           SessionState
	version         int64
	profileID       id.Profile
	policyID        id.Policy
	profileRevision uint32
	profileDigest   string
	region          string
	requirements    Profile
	failure         SessionFailure
	inputRequest    *InputRequest
	createdAt       time.Time
	updatedAt       time.Time
	expiresAt       time.Time
}

// NewSession snapshots one active published capture-profile revision.
func NewSession(
	identifier id.Verification,
	tenantID id.Tenant,
	profile CaptureProfile,
	revision Revision,
	registry evidence.Registry,
	region string,
	policyID id.Policy,
	now time.Time,
	expiresAt time.Time,
) (Session, error) {
	if identifier.IsZero() || tenantID.IsZero() || policyID.IsZero() || !validRegion(region) || now.IsZero() || !expiresAt.After(now) {
		return Session{}, errors.New("verification: session identity and lifetime are invalid")
	}
	published := profile.PublishedRevision()
	isProfileUnavailable := profile.TenantID().String() != tenantID.String() ||
		profile.State() != ProfileStateActive || published == nil ||
		revision.TenantID().String() != tenantID.String() ||
		revision.ProfileID().String() != profile.ID().String() ||
		revision.Number() != *published || revision.State() != RevisionStatePublished
	if isProfileUnavailable {
		return Session{}, ErrProfileUnavailable
	}
	if err := ValidateProfile(revision.Document(), registry); err != nil {
		return Session{}, err
	}
	digest, err := Digest(revision.Document(), registry)
	if err != nil {
		return Session{}, err
	}
	if digest != revision.Digest() {
		return Session{}, ErrProfileUnavailable
	}

	return RestoreSession(
		identifier,
		tenantID,
		SessionStateCollecting,
		1,
		profile.ID(),
		revision.Number(),
		revision.Digest(),
		revision.Document(),
		region,
		policyID,
		now,
		now,
		expiresAt,
		registry,
	)
}

// RestoreSession validates a verification session loaded from durable state.
func RestoreSession(
	identifier id.Verification,
	tenantID id.Tenant,
	state SessionState,
	version int64,
	profileID id.Profile,
	profileRevision uint32,
	profileDigest string,
	requirements Profile,
	region string,
	policyID id.Policy,
	createdAt time.Time,
	updatedAt time.Time,
	expiresAt time.Time,
	registry evidence.Registry,
) (Session, error) {
	createdAt = createdAt.UTC()
	updatedAt = updatedAt.UTC()
	expiresAt = expiresAt.UTC()
	isIdentityInvalid := identifier.IsZero() || tenantID.IsZero() || profileID.IsZero() || policyID.IsZero() ||
		profileRevision == 0 || profileDigest == ""
	isLifecycleInvalid := !state.Valid() || version < 1 || !validRegion(region)
	isTimeInvalid := createdAt.IsZero() || updatedAt.Before(createdAt) || !expiresAt.After(createdAt)
	if isIdentityInvalid || isLifecycleInvalid || isTimeInvalid {
		return Session{}, errors.New("verification: stored session is invalid")
	}
	if err := ValidateProfile(requirements, registry); err != nil {
		return Session{}, err
	}
	digest, err := Digest(requirements, registry)
	if err != nil {
		return Session{}, err
	}
	if digest != profileDigest {
		return Session{}, errors.New("verification: session snapshot digest does not match its requirements")
	}

	return Session{
		id:              identifier,
		tenantID:        tenantID,
		state:           state,
		version:         version,
		profileID:       profileID,
		profileRevision: profileRevision,
		profileDigest:   profileDigest,
		region:          region,
		policyID:        policyID,
		requirements:    cloneProfile(requirements),
		createdAt:       createdAt,
		updatedAt:       updatedAt,
		expiresAt:       expiresAt,
	}, nil
}

// WithFailure returns a restored failed session carrying its validated
// operational failure. It is the additive read path that leaves RestoreSession
// stable; it rejects any combination other than a failed session with a
// bounded failure so a non-failed aggregate can never carry one.
func (session Session) WithFailure(failure SessionFailure) (Session, error) {
	if session.state != SessionStateFailed || failure.Validate() != nil {
		return Session{}, errors.New("verification: session failure is invalid")
	}
	session.failure = failure
	return session, nil
}

// ID returns the stable verification-session identifier.
func (session Session) ID() id.Verification { return session.id }

// TenantID returns the owning tenant identifier.
func (session Session) TenantID() id.Tenant { return session.tenantID }

// State returns the current session lifecycle state.
func (session Session) State() SessionState { return session.state }

// Version returns the optimistic aggregate version.
func (session Session) Version() int64 { return session.version }

// ProfileID returns the source capture-profile identifier.
func (session Session) ProfileID() id.Profile { return session.profileID }

// ProfileRevision returns the exact source revision copied at creation.
func (session Session) ProfileRevision() uint32 { return session.profileRevision }

// ProfileDigest returns the canonical immutable requirements digest.
func (session Session) ProfileDigest() string { return session.profileDigest }

// Region returns the immutable regional data-plane assignment.
func (session Session) Region() string { return session.region }

// PolicyID returns the immutable tenant-selected decision policy.
func (session Session) PolicyID() id.Policy { return session.policyID }

// Requirements returns a defensive copy of the immutable profile snapshot.
func (session Session) Requirements() Profile { return cloneProfile(session.requirements) }

// Failure returns the bounded operational failure class and code. It is the
// zero value unless the session is in the terminal failed state.
func (session Session) Failure() SessionFailure { return session.failure }

// CreatedAt returns the session creation time.
func (session Session) CreatedAt() time.Time { return session.createdAt }

// UpdatedAt returns the latest session transition time.
func (session Session) UpdatedAt() time.Time { return session.updatedAt }

// ExpiresAt returns the absolute session expiry.
func (session Session) ExpiresAt() time.Time { return session.expiresAt }

// AcceptsCaptureAt reports whether capture activity is currently permitted.
func (session Session) AcceptsCaptureAt(now time.Time) bool {
	return session.state == SessionStateCollecting && now.UTC().Before(session.expiresAt)
}

func validRegion(region string) bool {
	if len(region) == 0 || len(region) > 63 || region[0] < 'a' || region[0] > 'z' {
		return false
	}
	for _, character := range region[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}
