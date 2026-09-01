package policy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// MaximumCatalogPageSize bounds internal catalog inspection reads.
const MaximumCatalogPageSize = 100

// RevisionMetadata identifies immutable compiled policy meaning without
// loading or exposing the canonical policy document.
type RevisionMetadata struct {
	reference Reference
	evaluator EvaluatorReference
	createdAt time.Time
}

// RestoreRevisionMetadata validates one durable metadata-only revision row.
func RestoreRevisionMetadata(
	reference Reference,
	evaluator EvaluatorReference,
	createdAt time.Time,
) (RevisionMetadata, error) {
	if err := reference.validate(); err != nil {
		return RevisionMetadata{}, ErrRevisionConflict
	}
	if err := evaluator.validate(); err != nil || !validUTC(createdAt) {
		return RevisionMetadata{}, ErrRevisionConflict
	}
	return RevisionMetadata{
		reference: reference, evaluator: evaluator, createdAt: createdAt,
	}, nil
}

// Reference returns the exact immutable policy identity.
func (metadata RevisionMetadata) Reference() Reference { return metadata.reference }

// Evaluator returns the exact implementation identity used for compilation.
func (metadata RevisionMetadata) Evaluator() EvaluatorReference { return metadata.evaluator }

// CreatedAt returns the explicit UTC registration time.
func (metadata RevisionMetadata) CreatedAt() time.Time { return metadata.createdAt }

// ActivationMetadata describes one immutable active-pointer switch without
// loading the canonical revision document.
type ActivationMetadata struct {
	revision         RevisionMetadata
	version          int64
	previousRevision uint32
	actor            id.APIKey
	activatedAt      time.Time
}

// RestoreActivationMetadata validates one durable metadata-only history row.
func RestoreActivationMetadata(
	revision RevisionMetadata,
	version int64,
	previousRevision uint32,
	actor id.APIKey,
	activatedAt time.Time,
) (ActivationMetadata, error) {
	if revision.Reference().ID.IsZero() || version < 1 || actor.IsZero() ||
		!validUTC(activatedAt) || activatedAt.Before(revision.CreatedAt()) ||
		previousRevision == revision.Reference().Revision {
		return ActivationMetadata{}, ErrActivationConflict
	}
	return ActivationMetadata{
		revision: revision, version: version, previousRevision: previousRevision,
		actor: actor, activatedAt: activatedAt,
	}, nil
}

// Revision returns the exact revision metadata selected by the switch.
func (metadata ActivationMetadata) Revision() RevisionMetadata { return metadata.revision }

// Version returns the policy's monotonic activation version.
func (metadata ActivationMetadata) Version() int64 { return metadata.version }

// PreviousRevision returns zero only for the first activation.
func (metadata ActivationMetadata) PreviousRevision() uint32 {
	return metadata.previousRevision
}

// Actor returns the API-key record that requested the switch.
func (metadata ActivationMetadata) Actor() id.APIKey { return metadata.actor }

// ActivatedAt returns the explicit UTC switch time.
func (metadata ActivationMetadata) ActivatedAt() time.Time { return metadata.activatedAt }

// RevisionInspectionRepository is the metadata-only revision-list boundary.
type RevisionInspectionRepository interface {
	ListRevisionMetadata(
		context.Context,
		tenant.Scope,
		id.Policy,
		uint32,
		int,
	) ([]RevisionMetadata, error)
}

// ActivationInspectionRepository is the metadata-only activation-list boundary.
type ActivationInspectionRepository interface {
	ListActivationMetadata(
		context.Context,
		tenant.Scope,
		id.Policy,
		int64,
		int,
	) ([]ActivationMetadata, error)
}

// CatalogInspectionRepository composes the two narrow inspection ports.
type CatalogInspectionRepository interface {
	RevisionInspectionRepository
	ActivationInspectionRepository
}

// RevisionPage is one immutable newest-first internal inspection page.
type RevisionPage struct {
	items      []RevisionMetadata
	hasMore    bool
	nextBefore uint32
}

// Items returns a defensive copy of the revision metadata page.
func (page RevisionPage) Items() []RevisionMetadata { return slices.Clone(page.items) }

// HasMore reports whether older revisions remain.
func (page RevisionPage) HasMore() bool { return page.hasMore }

// NextBefore returns the exclusive boundary for the next page, or zero.
func (page RevisionPage) NextBefore() uint32 { return page.nextBefore }

// ActivationPage is one immutable newest-first internal inspection page.
type ActivationPage struct {
	items      []ActivationMetadata
	hasMore    bool
	nextBefore int64
}

// Items returns a defensive copy of the activation metadata page.
func (page ActivationPage) Items() []ActivationMetadata { return slices.Clone(page.items) }

// HasMore reports whether older activations remain.
func (page ActivationPage) HasMore() bool { return page.hasMore }

// NextBefore returns the exclusive boundary for the next page, or zero.
func (page ActivationPage) NextBefore() int64 { return page.nextBefore }

// CatalogInspector provides read-only bounded internal policy history.
type CatalogInspector struct{ repository CatalogInspectionRepository }

// NewCatalogInspector constructs the metadata-only inspection service.
func NewCatalogInspector(repository CatalogInspectionRepository) (*CatalogInspector, error) {
	if repository == nil {
		return nil, errors.New("policy catalog inspector: repository is required")
	}
	return &CatalogInspector{repository: repository}, nil
}

// ListRevisions returns a validated newest-first page using an exclusive
// numeric boundary. Public cursor encoding remains intentionally undecided.
func (inspector *CatalogInspector) ListRevisions(
	ctx context.Context,
	scope tenant.Scope,
	policyID id.Policy,
	before uint32,
	limit int,
) (RevisionPage, error) {
	if inspector == nil || inspector.repository == nil || scope.ID().IsZero() ||
		policyID.IsZero() || limit < 1 || limit > MaximumCatalogPageSize {
		return RevisionPage{}, fmt.Errorf("%w: revision inspection request", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return RevisionPage{}, err
	}
	items, err := inspector.repository.ListRevisionMetadata(
		ctx, scope, policyID, before, limit+1,
	)
	if err != nil {
		return RevisionPage{}, fmt.Errorf("list policy revision metadata: %w", err)
	}
	if err := validateRevisionMetadataPage(items, policyID, before, limit+1); err != nil {
		return RevisionPage{}, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	nextBefore := uint32(0)
	if hasMore {
		nextBefore = items[len(items)-1].Reference().Revision
	}
	return RevisionPage{
		items: slices.Clone(items), hasMore: hasMore, nextBefore: nextBefore,
	}, nil
}

// ListActivations returns a validated newest-first page using an exclusive
// monotonic activation-version boundary.
func (inspector *CatalogInspector) ListActivations(
	ctx context.Context,
	scope tenant.Scope,
	policyID id.Policy,
	before int64,
	limit int,
) (ActivationPage, error) {
	if inspector == nil || inspector.repository == nil || scope.ID().IsZero() ||
		policyID.IsZero() || before < 0 || limit < 1 || limit > MaximumCatalogPageSize {
		return ActivationPage{}, fmt.Errorf("%w: activation inspection request", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return ActivationPage{}, err
	}
	items, err := inspector.repository.ListActivationMetadata(
		ctx, scope, policyID, before, limit+1,
	)
	if err != nil {
		return ActivationPage{}, fmt.Errorf("list policy activation metadata: %w", err)
	}
	if err := validateActivationMetadataPage(items, policyID, before, limit+1); err != nil {
		return ActivationPage{}, err
	}
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	nextBefore := int64(0)
	if hasMore {
		nextBefore = items[len(items)-1].Version()
	}
	return ActivationPage{
		items: slices.Clone(items), hasMore: hasMore, nextBefore: nextBefore,
	}, nil
}

func validateRevisionMetadataPage(
	items []RevisionMetadata,
	policyID id.Policy,
	before uint32,
	maximum int,
) error {
	if len(items) > maximum {
		return ErrRevisionConflict
	}
	previous := uint32(0)
	for index, item := range items {
		reference := item.Reference()
		if reference.ID != policyID || reference.validate() != nil ||
			item.Evaluator().validate() != nil || !validUTC(item.CreatedAt()) ||
			(before != 0 && reference.Revision >= before) ||
			(index > 0 && reference.Revision >= previous) {
			return ErrRevisionConflict
		}
		previous = reference.Revision
	}
	return nil
}

func validateActivationMetadataPage(
	items []ActivationMetadata,
	policyID id.Policy,
	before int64,
	maximum int,
) error {
	if len(items) > maximum {
		return ErrActivationConflict
	}
	previous := int64(0)
	for index, item := range items {
		if item.Revision().Reference().ID != policyID || item.Version() < 1 ||
			item.Actor().IsZero() || !validUTC(item.ActivatedAt()) ||
			item.ActivatedAt().Before(item.Revision().CreatedAt()) ||
			item.PreviousRevision() == item.Revision().Reference().Revision ||
			(before != 0 && item.Version() >= before) ||
			(index > 0 && item.Version() >= previous) {
			return ErrActivationConflict
		}
		previous = item.Version()
	}
	return nil
}
