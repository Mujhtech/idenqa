package experience

import (
	"time"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// State is the aggregate lifecycle state of one experience. The published
// version remains live across draft work; only an explicit publish replaces it.
type State string

const (
	// StateDraft means the latest revision is not approved.
	StateDraft State = "draft"
	// StateApproved means the latest revision is approved and awaits publication.
	StateApproved State = "approved"
	// StatePublished means the latest approved revision is the live revision.
	StatePublished State = "published"
	// StateRevoked means the kill switch cleared the live revision.
	StateRevoked State = "revoked"
)

// Valid reports whether the state is part of the closed v1 vocabulary.
func (state State) Valid() bool {
	switch state {
	case StateDraft, StateApproved, StatePublished, StateRevoked:
		return true
	default:
		return false
	}
}

// RevisionState is the immutable lifecycle of one revision version.
type RevisionState string

const (
	// RevisionDraft is an unpublished working revision.
	RevisionDraft RevisionState = "draft"
	// RevisionApproved is an approved revision awaiting publication.
	RevisionApproved RevisionState = "approved"
	// RevisionPublished is or was the live revision.
	RevisionPublished RevisionState = "published"
	// RevisionSuperseded is a former live revision replaced by a newer publish.
	RevisionSuperseded RevisionState = "superseded"
	// RevisionRevoked is a live revision removed by kill switch.
	RevisionRevoked RevisionState = "revoked"
)

// Experience is the aggregate projection of one tenant-owned experience.
type Experience struct {
	ID               id.Experience
	TenantID         id.Tenant
	State            State
	Revision         int64
	LatestVersion    uint32
	ApprovedVersion  uint32
	PublishedVersion uint32
	Document         contract.Document
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// IsZero reports whether the aggregate is uninitialised.
func (value Experience) IsZero() bool { return value.ID.IsZero() }

// Revision is one immutable signed revision.
type Revision struct {
	ExperienceID id.Experience
	Version      uint32
	State        RevisionState
	Manifest     contract.Manifest
	CreatedAt    time.Time
}

// Change is one append-only lifecycle event.
type Change struct {
	Sequence      uint64
	Operation     string
	FromState     State
	ToState       State
	Version       uint32
	TargetVersion uint32
	Actor         string
	Reason        string
	Digest        string
	OccurredAt    time.Time
}

// Published is one live published revision consumed by resolution.
type Published struct {
	ExperienceID id.Experience
	Version      uint32
	Manifest     contract.Manifest
	PublishedAt  time.Time
}

// Position is an exclusive list cursor over experience identifiers.
type Position struct {
	Before id.Experience
}

// Page is one bounded ascending page of experiences.
type Page struct {
	Experiences []Experience
	Next        *Position
}

// Pin records the exact experience and copy versions pinned to one session.
type Pin struct {
	TenantID             id.Tenant
	VerificationID       id.Verification
	ExperienceID         id.Experience
	Version              uint32
	Locale               string
	TenantCopyVersion    string
	MandatoryCopyVersion string
	Source               string
	Digest               string
	KeyID                string
	PinnedAt             time.Time
}

// Contract converts the pin to its public representation.
func (pin Pin) Contract() contract.Pinned {
	return contract.Pinned{
		ExperienceID:         pin.ExperienceID.String(),
		Version:              pin.Version,
		Locale:               pin.Locale,
		TenantCopyVersion:    pin.TenantCopyVersion,
		MandatoryCopyVersion: pin.MandatoryCopyVersion,
		Source:               pin.Source,
		Digest:               pin.Digest,
		KeyID:                pin.KeyID,
		PinnedAt:             pin.PinnedAt.UTC(),
	}
}

// Pin sources recorded on every session.
const (
	PinSourcePinned    = "pinned"
	PinSourcePublished = "published"
	PinSourceDefault   = "default"
)
