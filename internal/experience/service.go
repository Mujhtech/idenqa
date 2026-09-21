package experience

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Deps are the owned ports the experience service consumes. All are required.
type Deps struct {
	Repository Repository
	Pins       PinRepository
	Signer     Signer
	Verifier   Verifier
	Assets     AssetStore
	Mandatory  MandatoryCatalogue
	IDs        IDGenerator
	Clock      Clock
}

// Service authorises and coordinates portable-experience use cases. It never
// exposes private keys or writes raw evidence.
type Service struct {
	repository  Repository
	pins        PinRepository
	signer      Signer
	verifier    Verifier
	assets      AssetStore
	mandatory   MandatoryCatalogue
	identifiers IDGenerator
	clock       Clock

	defaultDocument contract.Document
	defaultManifest contract.Manifest
}

// DraftRequest is the bounded tenant-owned document input.
type DraftRequest struct {
	Name                 string
	Copy                 contract.Copy
	MandatoryCopyVersion string
	DefaultLocale        string
	Targeting            []contract.Target
	Assets               []contract.Asset
	Links                contract.Links
	Theme                contract.Theme
	AllowedOrigins       []string
}

// NewService constructs the experience application service and self-checks the
// signed safe default before accepting traffic.
func NewService(deps Deps) (*Service, error) {
	if deps.Repository == nil || deps.Pins == nil || deps.Signer == nil || deps.Verifier == nil || deps.Assets == nil ||
		deps.Mandatory == nil || deps.IDs == nil || deps.Clock == nil {
		return nil, fmt.Errorf("%w: dependencies", ErrInvalid)
	}
	service := &Service{
		repository: deps.Repository, pins: deps.Pins, signer: deps.Signer, verifier: deps.Verifier,
		assets: deps.Assets, mandatory: deps.Mandatory, identifiers: deps.IDs, clock: deps.Clock,
		defaultDocument: SafeDefaultDocument(),
	}
	if !service.mandatory.Has(service.defaultDocument.MandatoryCopyVersion) {
		return nil, fmt.Errorf("%w: safe default references %s", ErrUnknownMandatoryCopy, service.defaultDocument.MandatoryCopyVersion)
	}
	manifest, err := service.signDocument(context.Background(), service.defaultDocument)
	if err != nil {
		return nil, fmt.Errorf("experience: sign safe default: %w", err)
	}
	canonical, err := contract.CanonicalDocument(service.defaultDocument)
	if err != nil {
		return nil, fmt.Errorf("experience: canonical safe default: %w", err)
	}
	signature, err := hex.DecodeString(manifest.Signature)
	if err != nil || service.verifier.Verify(context.Background(), manifest.KeyID, canonical, signature) != nil {
		return nil, fmt.Errorf("experience: verify safe default: %w", ErrSignature)
	}
	service.defaultManifest = manifest
	return service, nil
}

// ActiveKeyID reports the signing key advertised to clients for the newest
// Core-authored documents.
func (service *Service) ActiveKeyID() string { return service.defaultManifest.KeyID }

// Create creates one draft experience and its signed first revision.
func (service *Service) Create(ctx context.Context, authority access.Context, request DraftRequest) (Experience, error) {
	if err := authority.Require(access.PermissionExperiencesWrite); err != nil {
		return Experience{}, err
	}
	identifier, err := service.identifiers.NewExperience()
	if err != nil {
		return Experience{}, fmt.Errorf("experience: generate id: %w", err)
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return Experience{}, fmt.Errorf("experience: generate event id: %w", err)
	}
	document, err := service.buildDocument(identifier, 1, request)
	if err != nil {
		return Experience{}, err
	}
	if err := service.verifyAssets(ctx, authority.TenantScope(), document.Assets); err != nil {
		return Experience{}, err
	}
	manifest, err := service.signDocument(ctx, document)
	if err != nil {
		return Experience{}, err
	}
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	return service.repository.Create(ctx, authority.TenantScope(), CreateMutation{
		Manifest: manifest, Document: document, Actor: authority.Principal().KeyID().String(), EventID: eventID, At: now,
	})
}

// UpdateDraft saves one new immutable draft revision. Saving invalidates an
// existing approval because approval binds to the exact latest version.
func (service *Service) UpdateDraft(
	ctx context.Context,
	authority access.Context,
	identifier id.Experience,
	expectedRevision int64,
	request DraftRequest,
) (Experience, error) {
	if err := authority.Require(access.PermissionExperiencesWrite); err != nil {
		return Experience{}, err
	}
	current, err := service.repository.Find(ctx, authority.TenantScope(), identifier)
	if err != nil {
		return Experience{}, err
	}
	if current.Revision != expectedRevision {
		return Experience{}, fmt.Errorf("%w: expected revision", ErrConflict)
	}
	plan, applied, err := PlanTransition(current, OperationUpdate, current.LatestVersion+1)
	if err != nil {
		return Experience{}, err
	}
	if !applied {
		return current, nil
	}
	document, err := service.buildDocument(identifier, plan.TargetVersion, request)
	if err != nil {
		return Experience{}, err
	}
	if err := service.verifyAssets(ctx, authority.TenantScope(), document.Assets); err != nil {
		return Experience{}, err
	}
	manifest, err := service.signDocument(ctx, document)
	if err != nil {
		return Experience{}, err
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return Experience{}, fmt.Errorf("experience: generate event id: %w", err)
	}
	return service.repository.ApplyRevision(ctx, authority.TenantScope(), RevisionMutation{
		ExperienceID: identifier, ExpectedRevision: expectedRevision, Manifest: manifest, Document: document,
		Actor: authority.Principal().KeyID().String(), EventID: eventID, At: service.clock.Now().UTC().Truncate(time.Microsecond),
	})
}

// Approve records the explicit approval publication requires.
func (service *Service) Approve(
	ctx context.Context,
	authority access.Context,
	identifier id.Experience,
	expectedRevision int64,
) (Experience, error) {
	return service.transition(ctx, authority, identifier, expectedRevision, OperationApprove, 0, "")
}

// Publish publishes the exactly approved latest revision. A previous published
// revision becomes superseded; its history is preserved.
func (service *Service) Publish(
	ctx context.Context,
	authority access.Context,
	identifier id.Experience,
	expectedRevision int64,
) (Experience, error) {
	return service.transition(ctx, authority, identifier, expectedRevision, OperationPublish, 0, "")
}

// Revoke is the kill switch. Resolution stops serving the aggregate and falls
// back to the signed safe default.
func (service *Service) Revoke(
	ctx context.Context,
	authority access.Context,
	identifier id.Experience,
	expectedRevision int64,
	reason string,
) (Experience, error) {
	return service.transition(ctx, authority, identifier, expectedRevision, OperationRevoke, 0, reason)
}

// Rollback republishes a prior immutable approved revision without editing it.
func (service *Service) Rollback(
	ctx context.Context,
	authority access.Context,
	identifier id.Experience,
	expectedRevision int64,
	targetVersion uint32,
	reason string,
) (Experience, error) {
	return service.transition(ctx, authority, identifier, expectedRevision, OperationRollback, targetVersion, reason)
}

func (service *Service) transition(
	ctx context.Context,
	authority access.Context,
	identifier id.Experience,
	expectedRevision int64,
	operation TransitionOperation,
	targetVersion uint32,
	reason string,
) (Experience, error) {
	if err := authority.Require(access.PermissionExperiencesPublish); err != nil {
		return Experience{}, err
	}
	current, err := service.repository.Find(ctx, authority.TenantScope(), identifier)
	if err != nil {
		return Experience{}, err
	}
	if current.Revision != expectedRevision {
		return Experience{}, fmt.Errorf("%w: expected revision", ErrConflict)
	}
	if operation == OperationRollback {
		revision, revisionErr := service.repository.Revision(ctx, authority.TenantScope(), identifier, targetVersion)
		if revisionErr != nil {
			return Experience{}, revisionErr
		}
		if revision.State == RevisionDraft || revision.State == RevisionRevoked {
			return Experience{}, fmt.Errorf("%w: rollback target was never approved", ErrConflict)
		}
	}
	if operation == OperationPublish {
		if err := service.verifyAssets(ctx, authority.TenantScope(), current.Document.Assets); err != nil {
			return Experience{}, err
		}
	}
	plan, applied, err := PlanTransition(current, operation, targetVersion)
	if err != nil {
		return Experience{}, err
	}
	if !applied {
		return current, nil
	}
	digest, err := service.transitionDigest(ctx, authority.TenantScope(), current, plan)
	if err != nil {
		return Experience{}, err
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return Experience{}, fmt.Errorf("experience: generate event id: %w", err)
	}
	if len(reason) > 512 {
		return Experience{}, fmt.Errorf("%w: reason", ErrInvalid)
	}
	return service.repository.ApplyTransition(ctx, authority.TenantScope(), TransitionMutation{
		ExperienceID: identifier, ExpectedRevision: expectedRevision, Operation: operation,
		TargetVersion: plan.TargetVersion, Digest: digest, Plan: plan, Applied: true,
		Actor: authority.Principal().KeyID().String(), Reason: reason, EventID: eventID,
		At: service.clock.Now().UTC().Truncate(time.Microsecond),
	})
}

func (service *Service) transitionDigest(
	ctx context.Context,
	scope tenant.Scope,
	current Experience,
	plan Plan,
) (string, error) {
	version := plan.TargetVersion
	if version == 0 {
		version = current.LatestVersion
	}
	revision, err := service.repository.Revision(ctx, scope, current.ID, version)
	if err != nil {
		return "", err
	}
	return revision.Manifest.Digest, nil
}

// Get returns one tenant-owned experience.
func (service *Service) Get(ctx context.Context, authority access.Context, identifier id.Experience) (Experience, error) {
	if err := authority.Require(access.PermissionExperiencesRead); err != nil {
		return Experience{}, err
	}
	return service.repository.Find(ctx, authority.TenantScope(), identifier)
}

// List returns one bounded page of tenant-owned experiences.
func (service *Service) List(ctx context.Context, authority access.Context, position *Position, limit int) (Page, error) {
	if err := authority.Require(access.PermissionExperiencesRead); err != nil {
		return Page{}, err
	}
	if limit <= 0 || limit > 100 {
		return Page{}, fmt.Errorf("%w: limit", ErrInvalid)
	}
	return service.repository.List(ctx, authority.TenantScope(), position, limit)
}

// Changes returns the append-only lifecycle history of one experience.
func (service *Service) Changes(ctx context.Context, authority access.Context, identifier id.Experience) ([]Change, error) {
	if err := authority.Require(access.PermissionExperiencesRead); err != nil {
		return nil, err
	}
	return service.repository.Changes(ctx, authority.TenantScope(), identifier)
}

// Export returns the signed canonical manifest of the live published revision,
// or the latest revision when nothing is published.
func (service *Service) Export(ctx context.Context, authority access.Context, identifier id.Experience) (contract.Manifest, error) {
	if err := authority.Require(access.PermissionExperiencesRead); err != nil {
		return contract.Manifest{}, err
	}
	current, err := service.repository.Find(ctx, authority.TenantScope(), identifier)
	if err != nil {
		return contract.Manifest{}, err
	}
	version := current.PublishedVersion
	if version == 0 {
		version = current.LatestVersion
	}
	revision, err := service.repository.Revision(ctx, authority.TenantScope(), identifier, version)
	if err != nil {
		return contract.Manifest{}, err
	}
	return revision.Manifest, nil
}

// Import validates one exported manifest, verifies its digest and signature,
// and creates a draft revision without Console or Cloud.
func (service *Service) Import(ctx context.Context, authority access.Context, raw []byte) (Experience, error) {
	if err := authority.Require(access.PermissionExperiencesWrite); err != nil {
		return Experience{}, err
	}
	manifest, err := contract.ParseManifest(raw)
	if err != nil {
		return Experience{}, err
	}
	canonical, err := contract.CanonicalDocument(manifest.Document)
	if err != nil {
		return Experience{}, err
	}
	signature, err := hex.DecodeString(manifest.Signature)
	if err != nil || service.verifier.Verify(ctx, manifest.KeyID, canonical, signature) != nil {
		return Experience{}, fmt.Errorf("%w: imported manifest", ErrSignature)
	}
	if !service.mandatory.Has(manifest.Document.MandatoryCopyVersion) {
		return Experience{}, fmt.Errorf("%w: %s", ErrUnknownMandatoryCopy, manifest.Document.MandatoryCopyVersion)
	}
	if err := service.verifyAssets(ctx, authority.TenantScope(), manifest.Document.Assets); err != nil {
		return Experience{}, err
	}
	identifier, err := id.ParseExperience(manifest.Document.ExperienceID)
	if err != nil {
		return Experience{}, fmt.Errorf("%w: imported experience id", ErrInvalid)
	}
	current, findErr := service.repository.Find(ctx, authority.TenantScope(), identifier)
	if findErr != nil && !isNotFound(findErr) {
		return Experience{}, findErr
	}
	document := manifest.Document
	if findErr != nil {
		document.Version = 1
		eventID, idErr := service.identifiers.NewEvent()
		if idErr != nil {
			return Experience{}, fmt.Errorf("experience: generate event id: %w", idErr)
		}
		signed, signErr := service.signDocument(ctx, document)
		if signErr != nil {
			return Experience{}, signErr
		}
		return service.repository.Create(ctx, authority.TenantScope(), CreateMutation{
			Manifest: signed, Document: document, Actor: authority.Principal().KeyID().String(), EventID: eventID,
			At: service.clock.Now().UTC().Truncate(time.Microsecond),
		})
	}
	plan, applied, err := PlanTransition(current, OperationUpdate, current.LatestVersion+1)
	if err != nil {
		return Experience{}, err
	}
	if !applied {
		return current, nil
	}
	document.Version = plan.TargetVersion
	signed, err := service.signDocument(ctx, document)
	if err != nil {
		return Experience{}, err
	}
	eventID, err := service.identifiers.NewEvent()
	if err != nil {
		return Experience{}, fmt.Errorf("experience: generate event id: %w", err)
	}
	return service.repository.ApplyRevision(ctx, authority.TenantScope(), RevisionMutation{
		ExperienceID: identifier, ExpectedRevision: current.Revision, Manifest: signed, Document: document,
		Actor: authority.Principal().KeyID().String(), EventID: eventID,
		At: service.clock.Now().UTC().Truncate(time.Microsecond),
	})
}

// Resolve returns the newest published most-specific match or the signed safe
// default. It never returns unsigned or unbranded tenant content.
func (service *Service) Resolve(ctx context.Context, scope tenant.Scope, request ResolutionRequest) (contract.Resolution, error) {
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	candidates, err := service.repository.LoadPublished(ctx, scope)
	if err != nil {
		return service.defaultResolution(now), nil //nolint:nilerr // Resolution failure always falls back to the signed safe default.
	}
	candidate, matched, err := Resolve(candidates, request)
	if err != nil || !matched {
		return service.defaultResolution(now), nil //nolint:nilerr // Resolution failure always falls back to the signed safe default.
	}
	return service.resolutionFor(candidate, request.Locale, PinSourcePublished, false, now)
}

// ResolveForSession pins the resolved experience and copy versions on first use
// and preserves them on resume. Revocation, removal, tampering, or any
// resolution failure always falls back to the signed safe default.
func (service *Service) ResolveForSession(
	ctx context.Context,
	scope tenant.Scope,
	verificationID id.Verification,
	request ResolutionRequest,
) (contract.Resolution, error) {
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	if verificationID.IsZero() {
		return service.defaultResolution(now), nil
	}
	pinned, err := service.pins.FindPin(ctx, scope, verificationID)
	if err == nil {
		return service.resumePinned(ctx, scope, pinned, now)
	}
	if !isNotFound(err) {
		return service.defaultResolution(now), nil
	}
	candidates, err := service.repository.LoadPublished(ctx, scope)
	if err != nil {
		return service.defaultResolution(now), nil //nolint:nilerr // Resolution failure always falls back to the signed safe default.
	}
	candidate, matched, err := Resolve(candidates, request)
	if err != nil || !matched {
		return service.defaultResolution(now), nil //nolint:nilerr // Resolution failure always falls back to the signed safe default.
	}
	resolution, err := service.resolutionFor(candidate, request.Locale, PinSourcePublished, false, now)
	if err != nil {
		return service.defaultResolution(now), nil //nolint:nilerr // Resolution failure always falls back to the signed safe default.
	}
	pin := pinFromResolution(scope, verificationID, resolution, PinSourcePublished, now)
	stored, saveErr := service.pins.SavePin(ctx, scope, pin)
	if saveErr == nil && stored.ExperienceID.String() != pin.ExperienceID.String() {
		return service.resumePinned(ctx, scope, stored, now)
	}
	return resolution, nil
}

func (service *Service) resumePinned(ctx context.Context, scope tenant.Scope, pin Pin, now time.Time) (contract.Resolution, error) {
	if pin.Source == PinSourceDefault || pin.ExperienceID.String() == SafeDefaultExperienceID {
		return service.defaultResolution(now), nil
	}
	current, err := service.repository.Find(ctx, scope, pin.ExperienceID)
	if err != nil || current.State == StateRevoked {
		return service.defaultResolution(now), nil //nolint:nilerr // Resolution failure always falls back to the signed safe default.
	}
	revision, err := service.repository.Revision(ctx, scope, pin.ExperienceID, pin.Version)
	if err != nil || revision.State == RevisionDraft || revision.State == RevisionRevoked {
		return service.defaultResolution(now), nil //nolint:nilerr // Resolution failure always falls back to the signed safe default.
	}
	resolution, err := service.resolutionFor(Published{
		ExperienceID: pin.ExperienceID, Version: pin.Version, Manifest: revision.Manifest, PublishedAt: revision.CreatedAt,
	}, pin.Locale, PinSourcePinned, false, pin.PinnedAt)
	if err != nil {
		return service.defaultResolution(now), nil //nolint:nilerr // Resolution failure always falls back to the signed safe default.
	}
	resolution.Pinned = pin.Contract()
	if pin.Source != PinSourceDefault {
		resolution.Pinned.Source = PinSourcePinned
	}
	return resolution, nil
}

// PinForSession resolves and pins the session experience. It is idempotent:
// the first effective resolution wins and resume never moves the pin.
func (service *Service) PinForSession(
	ctx context.Context,
	scope tenant.Scope,
	verificationID id.Verification,
	request ResolutionRequest,
) (Pin, error) {
	resolution, err := service.ResolveForSession(ctx, scope, verificationID, request)
	if err != nil {
		return Pin{}, err
	}
	if pin, findErr := service.pins.FindPin(ctx, scope, verificationID); findErr == nil {
		return pin, nil
	}
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	return pinFromResolution(scope, verificationID, resolution, resolution.Pinned.Source, now), nil
}

// SafeDefault returns the signed accessible safe default.
func (service *Service) SafeDefault(now time.Time) contract.Resolution {
	return service.defaultResolution(now.UTC().Truncate(time.Microsecond))
}

// DefaultManifest exposes the signed safe default for bootstrap distribution.
func (service *Service) DefaultManifest() contract.Manifest { return service.defaultManifest }

func (service *Service) defaultResolution(now time.Time) contract.Resolution {
	entries, _ := service.mandatory.Entries(service.defaultDocument.MandatoryCopyVersion)
	digest, _ := service.mandatory.Digest(service.defaultDocument.MandatoryCopyVersion)
	pin := Pin{
		ExperienceID: mustExperienceID(service.defaultDocument.ExperienceID), Version: service.defaultDocument.Version,
		Locale: SafeDefaultLocale, TenantCopyVersion: service.defaultDocument.Copy.Version,
		MandatoryCopyVersion: service.defaultDocument.MandatoryCopyVersion, Source: PinSourceDefault,
		Digest: service.defaultManifest.Digest, KeyID: service.defaultManifest.KeyID, PinnedAt: now,
	}
	return contract.Resolution{
		Manifest:      service.defaultManifest,
		MandatoryCopy: contract.MandatoryCopy{Version: service.defaultDocument.MandatoryCopyVersion, Digest: digest, Entries: entries},
		Pinned:        pin.Contract(),
		Fallback:      true,
	}
}

func (service *Service) resolutionFor(
	candidate Published,
	locale string,
	source string,
	fallback bool,
	now time.Time,
) (contract.Resolution, error) {
	document := candidate.Manifest.Document
	chosen := document.DefaultLocale
	if locale != "" {
		for _, candidateLocale := range document.Copy.Locales {
			if candidateLocale.Locale == locale {
				chosen = locale
				break
			}
		}
	}
	entries, ok := service.mandatory.Entries(document.MandatoryCopyVersion)
	if !ok {
		return contract.Resolution{}, fmt.Errorf("%w: %s", ErrUnknownMandatoryCopy, document.MandatoryCopyVersion)
	}
	digest, ok := service.mandatory.Digest(document.MandatoryCopyVersion)
	if !ok {
		return contract.Resolution{}, fmt.Errorf("%w: %s", ErrUnknownMandatoryCopy, document.MandatoryCopyVersion)
	}
	pin := Pin{
		ExperienceID: candidate.ExperienceID, Version: candidate.Version, Locale: chosen,
		TenantCopyVersion: document.Copy.Version, MandatoryCopyVersion: document.MandatoryCopyVersion,
		Source: source, Digest: candidate.Manifest.Digest, KeyID: candidate.Manifest.KeyID, PinnedAt: now,
	}
	return contract.Resolution{
		Manifest:      candidate.Manifest,
		MandatoryCopy: contract.MandatoryCopy{Version: document.MandatoryCopyVersion, Digest: digest, Entries: entries},
		Pinned:        pin.Contract(),
		Fallback:      fallback,
	}, nil
}

func (service *Service) buildDocument(identifier id.Experience, version uint32, request DraftRequest) (contract.Document, error) {
	if !service.mandatory.Has(request.MandatoryCopyVersion) {
		return contract.Document{}, fmt.Errorf("%w: %s", ErrUnknownMandatoryCopy, request.MandatoryCopyVersion)
	}
	document := contract.Document{
		SchemaVersion: contract.SchemaVersion, ExperienceID: identifier.String(), Version: version,
		Name: request.Name, Copy: request.Copy, MandatoryCopyVersion: request.MandatoryCopyVersion,
		DefaultLocale: request.DefaultLocale, Targeting: request.Targeting, Assets: request.Assets,
		Links: request.Links, Theme: request.Theme, AllowedOrigins: request.AllowedOrigins,
	}
	if err := contract.ValidateDocument(document); err != nil {
		return contract.Document{}, err
	}
	return document, nil
}

func (service *Service) verifyAssets(ctx context.Context, scope tenant.Scope, assets []contract.Asset) error {
	for _, asset := range assets {
		if err := service.assets.Verify(ctx, scope, asset); err != nil {
			return fmt.Errorf("%w: %s", ErrAssetUnavailable, asset.Key)
		}
	}
	return nil
}

func (service *Service) signDocument(ctx context.Context, document contract.Document) (contract.Manifest, error) {
	canonical, err := contract.CanonicalDocument(document)
	if err != nil {
		return contract.Manifest{}, err
	}
	signature, err := service.signer.Sign(ctx, canonical)
	if err != nil {
		return contract.Manifest{}, fmt.Errorf("experience: sign document: %w", err)
	}
	if signature.Algorithm != contract.SignatureAlgorithm || len(signature.Bytes) != ed25519.SignatureSize {
		return contract.Manifest{}, ErrInvalid
	}
	manifest := contract.Manifest{
		Document: document, Digest: digestOf(canonical), KeyID: signature.KeyID,
		Algorithm: signature.Algorithm, Signature: hex.EncodeToString(signature.Bytes),
	}
	if err := contract.ValidateManifest(manifest); err != nil {
		return contract.Manifest{}, err
	}
	return manifest, nil
}

func pinFromResolution(scope tenant.Scope, verificationID id.Verification, resolution contract.Resolution, source string, now time.Time) Pin {
	return Pin{
		TenantID: scope.ID(), VerificationID: verificationID,
		ExperienceID: mustExperienceID(resolution.Pinned.ExperienceID), Version: resolution.Pinned.Version,
		Locale: resolution.Pinned.Locale, TenantCopyVersion: resolution.Pinned.TenantCopyVersion,
		MandatoryCopyVersion: resolution.Pinned.MandatoryCopyVersion, Source: source,
		Digest: resolution.Pinned.Digest, KeyID: resolution.Pinned.KeyID, PinnedAt: now,
	}
}

func mustExperienceID(value string) id.Experience {
	parsed, err := id.ParseExperience(value)
	if err != nil {
		return id.Experience{}
	}
	return parsed
}

func isNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

func digestOf(canonical []byte) string {
	return contract.DigestCanonical(canonical)
}
