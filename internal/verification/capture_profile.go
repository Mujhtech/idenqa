package verification

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

var (
	// ErrProfileNotFound is deliberately also used for cross-tenant misses.
	ErrProfileNotFound = errors.New("verification: capture profile not found")
	// ErrProfileConflict identifies a stale aggregate version or invalid transition.
	ErrProfileConflict = errors.New("verification: capture profile conflict")
	// ErrPublishedRevisionImmutable prevents edits to published profile content.
	ErrPublishedRevisionImmutable = errors.New("verification: published capture profile revision is immutable")
)

// ProfileState is the lifecycle of a stable capture-profile resource.
type ProfileState string

const (
	// ProfileStateDraft has one mutable draft and no published revision.
	ProfileStateDraft ProfileState = "draft"
	// ProfileStateActive has one current published revision and may have a newer draft.
	ProfileStateActive ProfileState = "active"
	// ProfileStateDeactivated is an irreversible disabled profile state.
	ProfileStateDeactivated ProfileState = "deactivated"
)

// RevisionState is the lifecycle of one numeric profile revision.
type RevisionState string

const (
	// RevisionStateDraft identifies the profile's only mutable revision.
	RevisionStateDraft RevisionState = "draft"
	// RevisionStatePublished identifies the profile's current immutable revision.
	RevisionStatePublished RevisionState = "published"
	// RevisionStateSuperseded identifies an immutable formerly published revision.
	RevisionStateSuperseded RevisionState = "superseded"
	// RevisionStateWithdrawn identifies a discarded draft preserved for audit.
	RevisionStateWithdrawn RevisionState = "withdrawn"
)

// CaptureProfile is the stable tenant-owned aggregate. Its document content
// lives in numeric revisions so published content never changes in place.
type CaptureProfile struct {
	id                id.Profile
	tenantID          id.Tenant
	name              string
	state             ProfileState
	version           int64
	latestRevision    uint32
	draftRevision     *uint32
	publishedRevision *uint32
	createdAt         time.Time
	updatedAt         time.Time
	deactivatedAt     *time.Time
}

// Revision is one immutable-after-publication capture-profile document.
type Revision struct {
	profileID   id.Profile
	tenantID    id.Tenant
	number      uint32
	state       RevisionState
	document    Profile
	digest      string
	createdAt   time.Time
	updatedAt   time.Time
	publishedAt *time.Time
	endedAt     *time.Time
}

// NewCaptureProfile creates revision one as the aggregate's only mutable draft.
func NewCaptureProfile(
	identifier id.Profile,
	tenantID id.Tenant,
	name string,
	document Profile,
	registry evidence.Registry,
	now time.Time,
) (CaptureProfile, Revision, error) {
	if identifier.IsZero() || tenantID.IsZero() || !validProfileName(name) || now.IsZero() {
		return CaptureProfile{}, Revision{}, errors.New("verification: capture profile identity, name, and time are required")
	}
	revision, err := newRevision(identifier, tenantID, 1, document, registry, now)
	if err != nil {
		return CaptureProfile{}, Revision{}, err
	}
	number := uint32(1)
	profile := CaptureProfile{
		id:             identifier,
		tenantID:       tenantID,
		name:           name,
		state:          ProfileStateDraft,
		version:        1,
		latestRevision: number,
		draftRevision:  &number,
		createdAt:      now.UTC(),
		updatedAt:      now.UTC(),
	}

	return profile, revision, nil
}

// RestoreCaptureProfile validates a stable aggregate loaded from persistence.
func RestoreCaptureProfile(
	identifier id.Profile,
	tenantID id.Tenant,
	name string,
	state ProfileState,
	version int64,
	latestRevision uint32,
	draftRevision *uint32,
	publishedRevision *uint32,
	createdAt time.Time,
	updatedAt time.Time,
	deactivatedAt *time.Time,
) (CaptureProfile, error) {
	profile := CaptureProfile{
		id:                identifier,
		tenantID:          tenantID,
		name:              name,
		state:             state,
		version:           version,
		latestRevision:    latestRevision,
		draftRevision:     cloneUint32(draftRevision),
		publishedRevision: cloneUint32(publishedRevision),
		createdAt:         createdAt,
		updatedAt:         updatedAt,
		deactivatedAt:     cloneTime(deactivatedAt),
	}
	if err := profile.validate(); err != nil {
		return CaptureProfile{}, err
	}

	return profile, nil
}

// RestoreRevision validates a revision loaded from persistence.
func RestoreRevision(
	profileID id.Profile,
	tenantID id.Tenant,
	number uint32,
	state RevisionState,
	document Profile,
	digest string,
	createdAt time.Time,
	updatedAt time.Time,
	publishedAt *time.Time,
	endedAt *time.Time,
	registry evidence.Registry,
) (Revision, error) {
	if err := ValidateProfile(document, registry); err != nil {
		return Revision{}, err
	}
	actualDigest, err := Digest(document, registry)
	if err != nil {
		return Revision{}, err
	}
	revision := Revision{
		profileID:   profileID,
		tenantID:    tenantID,
		number:      number,
		state:       state,
		document:    cloneProfile(document),
		digest:      digest,
		createdAt:   createdAt,
		updatedAt:   updatedAt,
		publishedAt: cloneTime(publishedAt),
		endedAt:     cloneTime(endedAt),
	}
	if err := revision.validate(); err != nil {
		return Revision{}, err
	}
	if actualDigest != digest {
		return Revision{}, errors.New("verification: stored capture profile digest does not match its document")
	}

	return revision, nil
}

// UpdateDraft replaces only the current draft document and advances the root version.
func (profile CaptureProfile) UpdateDraft(
	expectedVersion int64,
	name string,
	current Revision,
	document Profile,
	registry evidence.Registry,
	now time.Time,
) (CaptureProfile, Revision, error) {
	if err := profile.expectMutable(expectedVersion, now); err != nil {
		return CaptureProfile{}, Revision{}, err
	}
	if profile.draftRevision == nil || current.profileID.String() != profile.id.String() ||
		current.number != *profile.draftRevision || current.state != RevisionStateDraft {
		return CaptureProfile{}, Revision{}, ErrPublishedRevisionImmutable
	}
	if !validProfileName(name) {
		return CaptureProfile{}, Revision{}, errors.New("verification: capture profile name is invalid")
	}
	updated, err := newRevision(profile.id, profile.tenantID, current.number, document, registry, current.createdAt)
	if err != nil {
		return CaptureProfile{}, Revision{}, err
	}
	updated.updatedAt = now.UTC()
	profile.name = name
	profile.version++
	profile.updatedAt = now.UTC()

	return profile, updated, nil
}

// PublishDraft makes the current draft immutable and non-destructively
// supersedes the previous published revision, when one exists.
func (profile CaptureProfile) PublishDraft(
	expectedVersion int64,
	draft Revision,
	previous *Revision,
	now time.Time,
) (CaptureProfile, Revision, *Revision, error) {
	if err := profile.expectMutable(expectedVersion, now); err != nil {
		return CaptureProfile{}, Revision{}, nil, err
	}
	if profile.draftRevision == nil || draft.profileID.String() != profile.id.String() ||
		draft.number != *profile.draftRevision || draft.state != RevisionStateDraft {
		return CaptureProfile{}, Revision{}, nil, ErrProfileConflict
	}
	if (profile.publishedRevision == nil) != (previous == nil) {
		return CaptureProfile{}, Revision{}, nil, ErrProfileConflict
	}

	published := draft
	published.state = RevisionStatePublished
	published.updatedAt = now.UTC()
	published.publishedAt = timePointer(now.UTC())
	var superseded *Revision
	if previous != nil {
		if previous.profileID.String() != profile.id.String() || previous.number != *profile.publishedRevision ||
			previous.state != RevisionStatePublished {
			return CaptureProfile{}, Revision{}, nil, ErrProfileConflict
		}
		value := *previous
		value.state = RevisionStateSuperseded
		value.updatedAt = now.UTC()
		value.endedAt = timePointer(now.UTC())
		superseded = &value
	}

	number := draft.number
	profile.state = ProfileStateActive
	profile.version++
	profile.draftRevision = nil
	profile.publishedRevision = &number
	profile.updatedAt = now.UTC()

	return profile, published, superseded, nil
}

// BeginSupersession adds one new draft while the published revision stays active.
func (profile CaptureProfile) BeginSupersession(
	expectedVersion int64,
	document Profile,
	registry evidence.Registry,
	now time.Time,
) (CaptureProfile, Revision, error) {
	if err := profile.expectMutable(expectedVersion, now); err != nil {
		return CaptureProfile{}, Revision{}, err
	}
	if profile.state != ProfileStateActive || profile.publishedRevision == nil || profile.draftRevision != nil {
		return CaptureProfile{}, Revision{}, ErrProfileConflict
	}
	number := profile.latestRevision + 1
	revision, err := newRevision(profile.id, profile.tenantID, number, document, registry, now)
	if err != nil {
		return CaptureProfile{}, Revision{}, err
	}
	profile.latestRevision = number
	profile.draftRevision = &number
	profile.version++
	profile.updatedAt = now.UTC()

	return profile, revision, nil
}

// Deactivate irreversibly disables the profile while preserving all revisions.
func (profile CaptureProfile) Deactivate(
	expectedVersion int64,
	draft *Revision,
	now time.Time,
) (CaptureProfile, *Revision, error) {
	if err := profile.expectMutable(expectedVersion, now); err != nil {
		return CaptureProfile{}, nil, err
	}
	if profile.state != ProfileStateActive || profile.publishedRevision == nil ||
		(profile.draftRevision == nil) != (draft == nil) {
		return CaptureProfile{}, nil, ErrProfileConflict
	}
	var withdrawn *Revision
	if draft != nil {
		if draft.profileID.String() != profile.id.String() || draft.number != *profile.draftRevision ||
			draft.state != RevisionStateDraft {
			return CaptureProfile{}, nil, ErrProfileConflict
		}
		value := *draft
		value.state = RevisionStateWithdrawn
		value.updatedAt = now.UTC()
		value.endedAt = timePointer(now.UTC())
		withdrawn = &value
	}
	profile.state = ProfileStateDeactivated
	profile.version++
	profile.draftRevision = nil
	profile.updatedAt = now.UTC()
	profile.deactivatedAt = timePointer(now.UTC())

	return profile, withdrawn, nil
}

func newRevision(
	profileID id.Profile,
	tenantID id.Tenant,
	number uint32,
	document Profile,
	registry evidence.Registry,
	now time.Time,
) (Revision, error) {
	if profileID.IsZero() || tenantID.IsZero() || number == 0 || now.IsZero() {
		return Revision{}, errors.New("verification: capture profile revision identity and time are required")
	}
	digest, err := Digest(document, registry)
	if err != nil {
		return Revision{}, fmt.Errorf("validate capture profile revision: %w", err)
	}

	return Revision{
		profileID: profileID,
		tenantID:  tenantID,
		number:    number,
		state:     RevisionStateDraft,
		document:  cloneProfile(document),
		digest:    digest,
		createdAt: now.UTC(),
		updatedAt: now.UTC(),
	}, nil
}

func (profile CaptureProfile) validate() error {
	if profile.id.IsZero() || profile.tenantID.IsZero() || !validProfileName(profile.name) || profile.version <= 0 ||
		profile.latestRevision == 0 || profile.createdAt.IsZero() || profile.updatedAt.Before(profile.createdAt) {
		return errors.New("verification: stored capture profile is invalid")
	}
	if profile.draftRevision != nil && *profile.draftRevision != profile.latestRevision {
		return errors.New("verification: stored capture profile draft is not its latest revision")
	}
	if profile.publishedRevision != nil && *profile.publishedRevision > profile.latestRevision {
		return errors.New("verification: stored capture profile publication is invalid")
	}
	switch profile.state {
	case ProfileStateDraft:
		if profile.draftRevision == nil || profile.publishedRevision != nil || profile.deactivatedAt != nil {
			return errors.New("verification: stored draft capture profile is invalid")
		}
	case ProfileStateActive:
		if profile.publishedRevision == nil || profile.deactivatedAt != nil {
			return errors.New("verification: stored active capture profile is invalid")
		}
	case ProfileStateDeactivated:
		if profile.publishedRevision == nil || profile.draftRevision != nil || profile.deactivatedAt == nil {
			return errors.New("verification: stored deactivated capture profile is invalid")
		}
	default:
		return errors.New("verification: stored capture profile state is invalid")
	}

	return nil
}

func (revision Revision) validate() error {
	if revision.profileID.IsZero() || revision.tenantID.IsZero() || revision.number == 0 || revision.digest == "" ||
		revision.createdAt.IsZero() || revision.updatedAt.Before(revision.createdAt) {
		return errors.New("verification: stored capture profile revision is invalid")
	}
	switch revision.state {
	case RevisionStateDraft:
		if revision.publishedAt != nil || revision.endedAt != nil {
			return errors.New("verification: stored draft revision is invalid")
		}
	case RevisionStatePublished:
		if revision.publishedAt == nil || revision.endedAt != nil {
			return errors.New("verification: stored published revision is invalid")
		}
	case RevisionStateSuperseded:
		if revision.publishedAt == nil || revision.endedAt == nil {
			return errors.New("verification: stored superseded revision is invalid")
		}
	case RevisionStateWithdrawn:
		if revision.publishedAt != nil || revision.endedAt == nil {
			return errors.New("verification: stored withdrawn revision is invalid")
		}
	default:
		return errors.New("verification: stored capture profile revision state is invalid")
	}

	return nil
}

func (profile CaptureProfile) expectMutable(expectedVersion int64, now time.Time) error {
	if expectedVersion <= 0 || profile.version != expectedVersion || now.IsZero() || now.Before(profile.updatedAt) ||
		profile.state == ProfileStateDeactivated {
		return ErrProfileConflict
	}

	return nil
}

func validProfileName(value string) bool {
	return len(value) >= 1 && len(value) <= 100 && strings.TrimSpace(value) == value &&
		!strings.ContainsAny(value, "\x00\r\n")
}

func cloneUint32(value *uint32) *uint32 {
	if value == nil {
		return nil
	}
	cloned := *value

	return &cloned
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value

	return &cloned
}

func timePointer(value time.Time) *time.Time { return &value }

// ID returns the stable capture-profile identifier.
func (profile CaptureProfile) ID() id.Profile { return profile.id }

// TenantID returns the owning tenant identifier.
func (profile CaptureProfile) TenantID() id.Tenant { return profile.tenantID }

// Name returns the operator-facing profile name.
func (profile CaptureProfile) Name() string { return profile.name }

// State returns the aggregate lifecycle state.
func (profile CaptureProfile) State() ProfileState { return profile.state }

// Version returns the optimistic aggregate version.
func (profile CaptureProfile) Version() int64 { return profile.version }

// LatestRevision returns the greatest revision number allocated to the profile.
func (profile CaptureProfile) LatestRevision() uint32 { return profile.latestRevision }

// DraftRevision returns a defensive copy of the current draft pointer, if any.
func (profile CaptureProfile) DraftRevision() *uint32 { return cloneUint32(profile.draftRevision) }

// PublishedRevision returns a defensive copy of the current published pointer, if any.
func (profile CaptureProfile) PublishedRevision() *uint32 {
	return cloneUint32(profile.publishedRevision)
}

// CreatedAt returns the aggregate creation time.
func (profile CaptureProfile) CreatedAt() time.Time { return profile.createdAt }

// UpdatedAt returns the latest aggregate transition time.
func (profile CaptureProfile) UpdatedAt() time.Time { return profile.updatedAt }

// DeactivatedAt returns a defensive copy of the terminal transition time, if any.
func (profile CaptureProfile) DeactivatedAt() *time.Time { return cloneTime(profile.deactivatedAt) }

// ProfileID returns the stable profile that owns the revision.
func (revision Revision) ProfileID() id.Profile { return revision.profileID }

// TenantID returns the tenant that owns the revision.
func (revision Revision) TenantID() id.Tenant { return revision.tenantID }

// Number returns the positive profile-local revision number.
func (revision Revision) Number() uint32 { return revision.number }

// State returns the revision lifecycle state.
func (revision Revision) State() RevisionState { return revision.state }

// Document returns a defensive copy of the registry-bound profile document.
func (revision Revision) Document() Profile { return cloneProfile(revision.document) }

// Digest returns the canonical SHA-256 document digest.
func (revision Revision) Digest() string { return revision.digest }

// CreatedAt returns the revision creation time.
func (revision Revision) CreatedAt() time.Time { return revision.createdAt }

// UpdatedAt returns the latest permitted revision transition time.
func (revision Revision) UpdatedAt() time.Time { return revision.updatedAt }

// PublishedAt returns a defensive copy of the publication time, if published.
func (revision Revision) PublishedAt() *time.Time { return cloneTime(revision.publishedAt) }

// EndedAt returns a defensive copy of the supersession or withdrawal time.
func (revision Revision) EndedAt() *time.Time { return cloneTime(revision.endedAt) }
