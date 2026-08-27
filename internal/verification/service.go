package verification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	// OperationCreateProfile scopes idempotency records for draft creation.
	OperationCreateProfile = "capture_profiles.create"
	// OperationPublishProfile scopes idempotency records for publication.
	OperationPublishProfile = "capture_profiles.publish"
	// OperationSupersedeProfile scopes idempotency records for supersession.
	OperationSupersedeProfile = "capture_profiles.supersede"
	// OperationDeactivateProfile scopes idempotency records for deactivation.
	OperationDeactivateProfile = "capture_profiles.deactivate"
)

// ProfileIDGenerator is the identifier capability consumed by Service.
type ProfileIDGenerator interface {
	NewProfile() (id.Profile, error)
}

// ListPosition is the persisted descending list position carried by a cursor.
type ListPosition struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

// Page is one bounded profile list page. The HTTP boundary signs Next.
type Page struct {
	Profiles []CaptureProfile
	Next     *ListPosition
}

// MutationKind selects one atomic persistence transition.
type MutationKind string

const (
	// MutationCreate persists a new root and revision-one draft.
	MutationCreate MutationKind = "create_draft"
	// MutationUpdateDraft replaces the only mutable revision.
	MutationUpdateDraft MutationKind = "update_draft"
	// MutationPublish makes a draft immutable and current.
	MutationPublish MutationKind = "publish"
	// MutationSupersede begins a new draft while the current revision stays active.
	MutationSupersede MutationKind = "begin_supersession"
	// MutationDeactivate disables a profile and withdraws any open draft.
	MutationDeactivate MutationKind = "deactivate"
)

// MutationResult is the safe application snapshot retained for exact replay.
type MutationResult struct {
	ProfileID         string       `json:"profile_id"`
	Name              string       `json:"name"`
	State             ProfileState `json:"state"`
	Version           int64        `json:"version"`
	LatestRevision    uint32       `json:"latest_revision"`
	DraftRevision     *uint32      `json:"draft_revision"`
	PublishedRevision *uint32      `json:"published_revision"`
	Revision          uint32       `json:"revision"`
	Digest            string       `json:"digest"`
	UpdatedAt         time.Time    `json:"updated_at"`
}

// Mutation contains domain-validated state for one atomic idempotent write.
type Mutation struct {
	Kind             MutationKind
	Profile          CaptureProfile
	Revision         Revision
	PreviousRevision *Revision
	ExpectedVersion  int64
	Actor            id.APIKey
	Idempotency      idempotency.Request
	Result           MutationResult
	Status           int
}

// Repository is the tenant-scoped persistence boundary consumed by Service.
type Repository interface {
	FindProfile(context.Context, tenant.Scope, id.Profile) (CaptureProfile, error)
	FindRevision(context.Context, tenant.Scope, id.Profile, uint32) (Revision, error)
	ListProfiles(context.Context, tenant.Scope, *ListPosition, int) (Page, error)
	Replay(context.Context, tenant.Scope, idempotency.Request) (MutationResult, bool, error)
	SaveDraft(context.Context, tenant.Scope, id.APIKey, CaptureProfile, Revision, int64) error
	Apply(context.Context, tenant.Scope, Mutation) (MutationResult, error)
}

// Service authorises and coordinates capture-profile lifecycle use cases.
type Service struct {
	repository  Repository
	identifiers ProfileIDGenerator
	catalog     evidence.Catalog
	clock       clock.Clock
	retention   time.Duration
}

// NewService constructs the capture-profile application service.
func NewService(
	repository Repository,
	identifiers ProfileIDGenerator,
	catalog evidence.Catalog,
	source clock.Clock,
	retention time.Duration,
) (*Service, error) {
	if repository == nil || identifiers == nil || source == nil || retention <= 0 {
		return nil, errors.New("verification: profile service dependencies are required")
	}
	if catalog.IsZero() {
		return nil, errors.New("verification: profile service catalog is empty")
	}

	return &Service{
		repository:  repository,
		identifiers: identifiers,
		catalog:     catalog,
		clock:       source,
		retention:   retention,
	}, nil
}

// Create creates revision one as a mutable draft with durable retry safety.
func (service *Service) Create(
	ctx context.Context,
	authority access.Context,
	idempotencyKey string,
	name string,
	document Profile,
) (MutationResult, error) {
	if err := authority.Require(access.PermissionCaptureProfilesWrite); err != nil {
		return MutationResult{}, err
	}
	registry, err := service.catalog.Resolve(document.Registry)
	if err != nil {
		return MutationResult{}, err
	}
	canonical, err := canonicalCreate(name, document, registry)
	if err != nil {
		return MutationResult{}, err
	}
	now := service.clock.Now().UTC()
	retry, err := idempotency.NewRequest(
		authority.TenantScope().ID(),
		authority.Principal().KeyID(),
		OperationCreateProfile,
		idempotencyKey,
		canonical,
		now,
		service.retention,
	)
	if err != nil {
		return MutationResult{}, err
	}
	if replay, found, err := service.repository.Replay(ctx, authority.TenantScope(), retry); err != nil || found {
		return replay, err
	}
	identifier, err := service.identifiers.NewProfile()
	if err != nil {
		return MutationResult{}, fmt.Errorf("generate capture profile id: %w", err)
	}
	profile, revision, err := NewCaptureProfile(
		identifier,
		authority.TenantScope().ID(),
		name,
		document,
		registry,
		now,
	)
	if err != nil {
		return MutationResult{}, err
	}
	result := mutationResult(profile, revision)

	return service.repository.Apply(ctx, authority.TenantScope(), Mutation{
		Kind:        MutationCreate,
		Profile:     profile,
		Revision:    revision,
		Actor:       authority.Principal().KeyID(),
		Idempotency: retry,
		Result:      result,
		Status:      201,
	})
}

// UpdateDraft replaces the only mutable revision using optimistic concurrency.
func (service *Service) UpdateDraft(
	ctx context.Context,
	authority access.Context,
	identifier id.Profile,
	expectedVersion int64,
	name string,
	document Profile,
) (MutationResult, error) {
	if err := authority.Require(access.PermissionCaptureProfilesWrite); err != nil {
		return MutationResult{}, err
	}
	profile, err := service.repository.FindProfile(ctx, authority.TenantScope(), identifier)
	if err != nil {
		return MutationResult{}, err
	}
	draftNumber := profile.DraftRevision()
	if draftNumber == nil {
		return MutationResult{}, ErrPublishedRevisionImmutable
	}
	draft, err := service.repository.FindRevision(ctx, authority.TenantScope(), identifier, *draftNumber)
	if err != nil {
		return MutationResult{}, err
	}
	registry, err := service.catalog.Resolve(document.Registry)
	if err != nil {
		return MutationResult{}, err
	}
	updated, revision, err := profile.UpdateDraft(
		expectedVersion,
		name,
		draft,
		document,
		registry,
		service.clock.Now().UTC(),
	)
	if err != nil {
		return MutationResult{}, err
	}
	if err := service.repository.SaveDraft(
		ctx,
		authority.TenantScope(),
		authority.Principal().KeyID(),
		updated,
		revision,
		expectedVersion,
	); err != nil {
		return MutationResult{}, err
	}

	return mutationResult(updated, revision), nil
}

// Publish validates and publishes the current draft.
func (service *Service) Publish(
	ctx context.Context,
	authority access.Context,
	identifier id.Profile,
	expectedVersion int64,
	idempotencyKey string,
) (MutationResult, error) {
	return service.publish(ctx, authority, identifier, expectedVersion, idempotencyKey)
}

// Supersede creates the next draft without disturbing the active revision.
func (service *Service) Supersede(
	ctx context.Context,
	authority access.Context,
	identifier id.Profile,
	expectedVersion int64,
	idempotencyKey string,
	document Profile,
) (MutationResult, error) {
	if err := authority.Require(access.PermissionCaptureProfilesWrite); err != nil {
		return MutationResult{}, err
	}
	registry, err := service.catalog.Resolve(document.Registry)
	if err != nil {
		return MutationResult{}, err
	}
	canonicalDocument, err := CanonicalJSON(document, registry)
	if err != nil {
		return MutationResult{}, err
	}
	canonical, err := json.Marshal(struct {
		ProfileID       string          `json:"profile_id"`
		ExpectedVersion int64           `json:"expected_version"`
		Document        json.RawMessage `json:"document"`
	}{identifier.String(), expectedVersion, canonicalDocument})
	if err != nil {
		return MutationResult{}, fmt.Errorf("serialise supersede command: %w", err)
	}
	now := service.clock.Now().UTC()
	retry, err := service.retry(authority, OperationSupersedeProfile, idempotencyKey, canonical, now)
	if err != nil {
		return MutationResult{}, err
	}
	if replay, found, err := service.repository.Replay(ctx, authority.TenantScope(), retry); err != nil || found {
		return replay, err
	}
	profile, err := service.repository.FindProfile(ctx, authority.TenantScope(), identifier)
	if err != nil {
		return MutationResult{}, err
	}
	updated, revision, err := profile.BeginSupersession(expectedVersion, document, registry, now)
	if err != nil {
		return MutationResult{}, err
	}
	result := mutationResult(updated, revision)

	return service.repository.Apply(ctx, authority.TenantScope(), Mutation{
		Kind:            MutationSupersede,
		Profile:         updated,
		Revision:        revision,
		ExpectedVersion: expectedVersion,
		Actor:           authority.Principal().KeyID(),
		Idempotency:     retry,
		Result:          result,
		Status:          200,
	})
}

// Deactivate irreversibly disables the resource and withdraws any open draft.
func (service *Service) Deactivate(
	ctx context.Context,
	authority access.Context,
	identifier id.Profile,
	expectedVersion int64,
	idempotencyKey string,
) (MutationResult, error) {
	if err := authority.Require(access.PermissionCaptureProfilesWrite); err != nil {
		return MutationResult{}, err
	}
	canonical, err := json.Marshal(struct {
		ProfileID       string `json:"profile_id"`
		ExpectedVersion int64  `json:"expected_version"`
	}{identifier.String(), expectedVersion})
	if err != nil {
		return MutationResult{}, fmt.Errorf("serialise deactivate command: %w", err)
	}
	now := service.clock.Now().UTC()
	retry, err := service.retry(authority, OperationDeactivateProfile, idempotencyKey, canonical, now)
	if err != nil {
		return MutationResult{}, err
	}
	if replay, found, err := service.repository.Replay(ctx, authority.TenantScope(), retry); err != nil || found {
		return replay, err
	}
	profile, err := service.repository.FindProfile(ctx, authority.TenantScope(), identifier)
	if err != nil {
		return MutationResult{}, err
	}
	var draft *Revision
	if number := profile.DraftRevision(); number != nil {
		value, err := service.repository.FindRevision(ctx, authority.TenantScope(), identifier, *number)
		if err != nil {
			return MutationResult{}, err
		}
		draft = &value
	}
	updated, withdrawn, err := profile.Deactivate(expectedVersion, draft, now)
	if err != nil {
		return MutationResult{}, err
	}
	revision := Revision{}
	if withdrawn != nil {
		revision = *withdrawn
	} else if number := updated.PublishedRevision(); number != nil {
		revision, err = service.repository.FindRevision(ctx, authority.TenantScope(), identifier, *number)
		if err != nil {
			return MutationResult{}, err
		}
	}
	result := mutationResult(updated, revision)

	return service.repository.Apply(ctx, authority.TenantScope(), Mutation{
		Kind:            MutationDeactivate,
		Profile:         updated,
		Revision:        revision,
		ExpectedVersion: expectedVersion,
		Actor:           authority.Principal().KeyID(),
		Idempotency:     retry,
		Result:          result,
		Status:          200,
	})
}

// Find returns one tenant-scoped stable profile resource.
func (service *Service) Find(
	ctx context.Context,
	authority access.Context,
	identifier id.Profile,
) (CaptureProfile, error) {
	if err := authority.Require(access.PermissionCaptureProfilesRead); err != nil {
		return CaptureProfile{}, err
	}

	return service.repository.FindProfile(ctx, authority.TenantScope(), identifier)
}

// FindRevision returns one immutable or current draft revision.
func (service *Service) FindRevision(
	ctx context.Context,
	authority access.Context,
	identifier id.Profile,
	revision uint32,
) (Revision, error) {
	if err := authority.Require(access.PermissionCaptureProfilesRead); err != nil {
		return Revision{}, err
	}

	return service.repository.FindRevision(ctx, authority.TenantScope(), identifier, revision)
}

// List returns a bounded tenant-scoped page.
func (service *Service) List(
	ctx context.Context,
	authority access.Context,
	after *ListPosition,
	limit int,
) (Page, error) {
	if err := authority.Require(access.PermissionCaptureProfilesRead); err != nil {
		return Page{}, err
	}
	if limit < 1 || limit > 100 {
		return Page{}, errors.New("verification: profile list limit must be from 1 to 100")
	}

	return service.repository.ListProfiles(ctx, authority.TenantScope(), after, limit)
}

// ValidateDraft applies the publication gate without changing state.
func (service *Service) ValidateDraft(
	ctx context.Context,
	authority access.Context,
	identifier id.Profile,
) (string, error) {
	if err := authority.Require(access.PermissionCaptureProfilesWrite); err != nil {
		return "", err
	}
	profile, err := service.repository.FindProfile(ctx, authority.TenantScope(), identifier)
	if err != nil {
		return "", err
	}
	number := profile.DraftRevision()
	if number == nil {
		return "", ErrPublishedRevisionImmutable
	}
	revision, err := service.repository.FindRevision(ctx, authority.TenantScope(), identifier, *number)
	if err != nil {
		return "", err
	}
	registry, err := service.catalog.Resolve(revision.Document().Registry)
	if err != nil {
		return "", err
	}

	return ValidateForActivation(revision.Document(), registry)
}

func (service *Service) publish(
	ctx context.Context,
	authority access.Context,
	identifier id.Profile,
	expectedVersion int64,
	idempotencyKey string,
) (MutationResult, error) {
	if err := authority.Require(access.PermissionCaptureProfilesWrite); err != nil {
		return MutationResult{}, err
	}
	canonical, err := json.Marshal(struct {
		ProfileID       string `json:"profile_id"`
		ExpectedVersion int64  `json:"expected_version"`
	}{identifier.String(), expectedVersion})
	if err != nil {
		return MutationResult{}, fmt.Errorf("serialise publish command: %w", err)
	}
	now := service.clock.Now().UTC()
	retry, err := service.retry(authority, OperationPublishProfile, idempotencyKey, canonical, now)
	if err != nil {
		return MutationResult{}, err
	}
	if replay, found, err := service.repository.Replay(ctx, authority.TenantScope(), retry); err != nil || found {
		return replay, err
	}
	profile, err := service.repository.FindProfile(ctx, authority.TenantScope(), identifier)
	if err != nil {
		return MutationResult{}, err
	}
	draftNumber := profile.DraftRevision()
	if draftNumber == nil {
		return MutationResult{}, ErrPublishedRevisionImmutable
	}
	draft, err := service.repository.FindRevision(ctx, authority.TenantScope(), identifier, *draftNumber)
	if err != nil {
		return MutationResult{}, err
	}
	registry, err := service.catalog.Resolve(draft.Document().Registry)
	if err != nil {
		return MutationResult{}, err
	}
	if _, err := ValidateForActivation(draft.Document(), registry); err != nil {
		return MutationResult{}, err
	}
	var previous *Revision
	if number := profile.PublishedRevision(); number != nil {
		value, err := service.repository.FindRevision(ctx, authority.TenantScope(), identifier, *number)
		if err != nil {
			return MutationResult{}, err
		}
		previous = &value
	}
	updated, published, superseded, err := profile.PublishDraft(expectedVersion, draft, previous, now)
	if err != nil {
		return MutationResult{}, err
	}
	result := mutationResult(updated, published)

	return service.repository.Apply(ctx, authority.TenantScope(), Mutation{
		Kind:             MutationPublish,
		Profile:          updated,
		Revision:         published,
		PreviousRevision: superseded,
		ExpectedVersion:  expectedVersion,
		Actor:            authority.Principal().KeyID(),
		Idempotency:      retry,
		Result:           result,
		Status:           200,
	})
}

func (service *Service) retry(
	authority access.Context,
	operation string,
	key string,
	canonical []byte,
	now time.Time,
) (idempotency.Request, error) {
	return idempotency.NewRequest(
		authority.TenantScope().ID(),
		authority.Principal().KeyID(),
		operation,
		key,
		canonical,
		now,
		service.retention,
	)
}

func canonicalCreate(name string, document Profile, registry evidence.Registry) ([]byte, error) {
	canonicalDocument, err := CanonicalJSON(document, registry)
	if err != nil {
		return nil, err
	}

	return json.Marshal(struct {
		Name     string          `json:"name"`
		Document json.RawMessage `json:"document"`
	}{name, canonicalDocument})
}

func mutationResult(profile CaptureProfile, revision Revision) MutationResult {
	return MutationResult{
		ProfileID:         profile.ID().String(),
		Name:              profile.Name(),
		State:             profile.State(),
		Version:           profile.Version(),
		LatestRevision:    profile.LatestRevision(),
		DraftRevision:     profile.DraftRevision(),
		PublishedRevision: profile.PublishedRevision(),
		Revision:          revision.Number(),
		Digest:            revision.Digest(),
		UpdatedAt:         profile.UpdatedAt(),
	}
}
