package policy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// RevisionCompiler proves canonical policy meaning compiles under one evaluator.
type RevisionCompiler interface {
	CompileCanonical(context.Context, []byte) (EvaluatorReference, error)
}

// RevisionRepository is the immutable revision boundary consumed by Catalog.
type RevisionRepository interface {
	AppendRevision(context.Context, tenant.Scope, Revision) error
	FindRevision(context.Context, tenant.Scope, id.Policy, uint32) (Revision, error)
}

// ActivationRepository is the optimistic active-policy boundary consumed by Catalog.
type ActivationRepository interface {
	Activate(
		context.Context,
		tenant.Scope,
		id.Policy,
		uint32,
		int64,
		id.APIKey,
		time.Time,
	) (Activation, error)
	FindActive(context.Context, tenant.Scope, id.Policy) (Activation, error)
}

// CatalogRepository composes the two narrow policy-catalog persistence ports.
type CatalogRepository interface {
	RevisionRepository
	ActivationRepository
}

// Catalog compiles, registers, resolves, and activates immutable policy revisions.
type Catalog struct {
	repository CatalogRepository
	compiler   RevisionCompiler
}

// NewCatalog constructs the policy-catalog application service.
func NewCatalog(repository CatalogRepository, compiler RevisionCompiler) (*Catalog, error) {
	if repository == nil || compiler == nil {
		return nil, errors.New("policy catalog: dependencies are required")
	}
	return &Catalog{repository: repository, compiler: compiler}, nil
}

// RegisterCanonical compiles and appends one byte-exact immutable revision.
func (catalog *Catalog) RegisterCanonical(
	ctx context.Context,
	scope tenant.Scope,
	canonical []byte,
	createdAt time.Time,
) (Revision, error) {
	if scope.ID().IsZero() || !validUTC(createdAt) {
		return Revision{}, fmt.Errorf("%w: register revision request", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return Revision{}, err
	}
	reference, err := catalog.compiler.CompileCanonical(ctx, canonical)
	if err != nil {
		return Revision{}, fmt.Errorf("compile policy revision: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Revision{}, err
	}
	revision, err := NewRevisionCanonical(canonical, reference, createdAt)
	if err != nil {
		return Revision{}, err
	}
	if err := catalog.repository.AppendRevision(ctx, scope, revision); err != nil {
		return Revision{}, fmt.Errorf("append policy revision: %w", err)
	}
	stored, err := catalog.repository.FindRevision(
		ctx, scope, revision.Reference().ID, revision.Reference().Revision,
	)
	if err != nil {
		return Revision{}, fmt.Errorf("read registered policy revision: %w", err)
	}
	if stored.Reference() != revision.Reference() || stored.Evaluator() != revision.Evaluator() ||
		!stored.CreatedAt().Equal(revision.CreatedAt()) ||
		!bytes.Equal(stored.Canonical(), revision.Canonical()) {
		return Revision{}, ErrRevisionConflict
	}
	return stored, nil
}

// Activate switches the active pointer with an explicit optimistic precondition.
func (catalog *Catalog) Activate(
	ctx context.Context,
	scope tenant.Scope,
	policyID id.Policy,
	revision uint32,
	expectedVersion int64,
	actor id.APIKey,
	activatedAt time.Time,
) (Activation, error) {
	if scope.ID().IsZero() || policyID.IsZero() || revision == 0 || expectedVersion < 0 ||
		actor.IsZero() || !validUTC(activatedAt) {
		return Activation{}, ErrActivationConflict
	}
	if err := ctx.Err(); err != nil {
		return Activation{}, err
	}
	activation, err := catalog.repository.Activate(
		ctx, scope, policyID, revision, expectedVersion, actor, activatedAt,
	)
	if err != nil {
		return Activation{}, fmt.Errorf("activate policy revision: %w", err)
	}
	return activation, nil
}

// FindActive resolves one exact tenant-scoped active revision and activation.
func (catalog *Catalog) FindActive(
	ctx context.Context,
	scope tenant.Scope,
	policyID id.Policy,
) (Activation, error) {
	if scope.ID().IsZero() || policyID.IsZero() {
		return Activation{}, ErrActivationNotFound
	}
	activation, err := catalog.repository.FindActive(ctx, scope, policyID)
	if err != nil {
		return Activation{}, fmt.Errorf("find active policy revision: %w", err)
	}
	return activation, nil
}
