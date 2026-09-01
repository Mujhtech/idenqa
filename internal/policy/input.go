package policy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// AuthoritativeState is one atomically read, CEL-neutral projection of the
// durable state that may influence a machine decision. PolicyID is selected by
// the source; ActiveInputLoader deliberately does not invent assignment rules.
type AuthoritativeState struct {
	PolicyID          id.Policy
	AuthorityID       id.Authority
	AcknowledgementID id.Acknowledgement
	Region            string
	Facts             []Fact
}

// AuthoritativeStateSource owns the transactionally consistent projection
// consumed by ActiveInputLoader. Implementations must return only references
// and bounded normalised facts, never raw evidence or provider payloads.
type AuthoritativeStateSource interface {
	LoadAuthoritativeState(
		context.Context,
		tenant.Scope,
		id.Verification,
		time.Time,
	) (AuthoritativeState, error)
}

// ActivePolicyReader is the narrow active-revision boundary consumed by
// ActiveInputLoader.
type ActivePolicyReader interface {
	FindActive(context.Context, tenant.Scope, id.Policy) (Activation, error)
}

// ActiveInputLoader combines an atomic authoritative projection with the
// exact active immutable policy revision used to author a decision snapshot.
type ActiveInputLoader struct {
	source   AuthoritativeStateSource
	policies ActivePolicyReader
}

var _ InputLoader = (*ActiveInputLoader)(nil)

// NewActiveInputLoader constructs the production input coordination seam.
func NewActiveInputLoader(
	source AuthoritativeStateSource,
	policies ActivePolicyReader,
) (*ActiveInputLoader, error) {
	if source == nil || policies == nil {
		return nil, errors.New("policy active input loader: dependencies are required")
	}
	return &ActiveInputLoader{source: source, policies: policies}, nil
}

// LoadPolicyInput pins the policy that is active after the authoritative state
// has been read. The returned reference remains immutable if activation moves
// before evaluation; the evaluator resolves that exact revision, not the new
// active pointer.
func (loader *ActiveInputLoader) LoadPolicyInput(
	ctx context.Context,
	scope tenant.Scope,
	verificationID id.Verification,
	evaluatedAt time.Time,
) (AuthorInput, error) {
	if loader == nil || scope.ID().IsZero() || verificationID.IsZero() || !validUTC(evaluatedAt) {
		return AuthorInput{}, fmt.Errorf("%w: active input request", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return AuthorInput{}, err
	}
	state, err := loader.source.LoadAuthoritativeState(ctx, scope, verificationID, evaluatedAt)
	if err != nil {
		return AuthorInput{}, fmt.Errorf("load authoritative policy state: %w", err)
	}
	if err := validateAuthoritativeState(state, evaluatedAt); err != nil {
		return AuthorInput{}, err
	}
	if err := ctx.Err(); err != nil {
		return AuthorInput{}, err
	}
	activation, err := loader.policies.FindActive(ctx, scope, state.PolicyID)
	if err != nil {
		return AuthorInput{}, fmt.Errorf("find active policy revision: %w", err)
	}
	revision := activation.Revision()
	if revision.Reference().ID.String() != state.PolicyID.String() {
		return AuthorInput{}, ErrRevisionConflict
	}
	if err := ctx.Err(); err != nil {
		return AuthorInput{}, err
	}
	return AuthorInput{
		AuthorityID:       state.AuthorityID,
		AcknowledgementID: state.AcknowledgementID,
		Region:            state.Region,
		Policy:            revision.Reference(),
		Facts:             cloneFacts(state.Facts),
	}, nil
}

func validateAuthoritativeState(state AuthoritativeState, evaluatedAt time.Time) error {
	if state.PolicyID.IsZero() || state.AuthorityID.IsZero() || state.AcknowledgementID.IsZero() ||
		!validToken(state.Region, 64) || len(state.Facts) == 0 || len(state.Facts) > MaximumFacts {
		return fmt.Errorf("%w: authoritative policy state", ErrInvalid)
	}
	seen := make(map[FactKey]struct{}, len(state.Facts))
	reasons := 0
	for _, fact := range state.Facts {
		if _, err := validateFact(fact, evaluatedAt); err != nil {
			return fmt.Errorf("validate authoritative fact: %w", err)
		}
		if _, exists := seen[fact.Key]; exists {
			return fmt.Errorf("%w: duplicate authoritative fact", ErrConflict)
		}
		seen[fact.Key] = struct{}{}
		reasons += len(fact.ReasonCodes)
		if reasons > MaximumSnapshotReasons {
			return fmt.Errorf("%w: authoritative fact reasons", ErrInvalid)
		}
	}
	return nil
}

func cloneFacts(facts []Fact) []Fact {
	cloned := make([]Fact, len(facts))
	for index, fact := range facts {
		cloned[index] = cloneFact(fact)
	}
	return cloned
}
