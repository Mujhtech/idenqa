package policy

import (
	"bytes"
	"fmt"
	"slices"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// Revision is one immutable, canonical, successfully compiled policy document.
type Revision struct {
	reference Reference
	evaluator EvaluatorReference
	canonical []byte
	createdAt time.Time
}

// NewRevisionCanonical restores and validates byte-exact public policy meaning.
func NewRevisionCanonical(
	canonical []byte,
	evaluator EvaluatorReference,
	createdAt time.Time,
) (Revision, error) {
	document, err := policyv1.ParseCanonical(canonical)
	if err != nil {
		return Revision{}, fmt.Errorf("%w: canonical revision: %w", ErrInvalid, err)
	}
	identifier, err := id.ParsePolicy(document.PolicyID)
	if err != nil {
		return Revision{}, fmt.Errorf("%w: policy identifier", ErrInvalid)
	}
	digest, err := policyv1.Digest(document)
	if err != nil {
		return Revision{}, fmt.Errorf("digest policy revision: %w", err)
	}
	reference := Reference{
		ID: identifier, Revision: document.Revision,
		SchemaMajor: document.SchemaMajor, SchemaMinor: document.SchemaMinor,
		Digest: digest,
	}
	if err := reference.validate(); err != nil {
		return Revision{}, err
	}
	if err := evaluator.validate(); err != nil {
		return Revision{}, err
	}
	if !validUTC(createdAt) {
		return Revision{}, fmt.Errorf("%w: revision creation time", ErrInvalid)
	}

	return Revision{
		reference: reference,
		evaluator: evaluator,
		canonical: slices.Clone(canonical),
		createdAt: createdAt,
	}, nil
}

// RestoreRevision additionally verifies persisted identity fields.
func RestoreRevision(
	reference Reference,
	evaluator EvaluatorReference,
	canonical []byte,
	createdAt time.Time,
) (Revision, error) {
	restored, err := NewRevisionCanonical(canonical, evaluator, createdAt)
	if err != nil {
		return Revision{}, err
	}
	if restored.reference != reference || !bytes.Equal(restored.canonical, canonical) {
		return Revision{}, ErrRevisionConflict
	}
	return restored, nil
}

// Reference returns the exact public policy identity.
func (revision Revision) Reference() Reference { return revision.reference }

// Evaluator returns the implementation identity used to compile the revision.
func (revision Revision) Evaluator() EvaluatorReference { return revision.evaluator }

// Canonical returns a defensive copy of the byte-exact public document.
func (revision Revision) Canonical() []byte { return slices.Clone(revision.canonical) }

// CreatedAt returns the explicit UTC registration time.
func (revision Revision) CreatedAt() time.Time { return revision.createdAt }

// Activation is one immutable switch of a policy's active revision.
type Activation struct {
	revision         Revision
	version          int64
	previousRevision uint32
	actor            id.APIKey
	activatedAt      time.Time
}

// RestoreActivation validates one durable activation record.
func RestoreActivation(
	revision Revision,
	version int64,
	previousRevision uint32,
	actor id.APIKey,
	activatedAt time.Time,
) (Activation, error) {
	if revision.Reference().ID.IsZero() || version < 1 || actor.IsZero() || !validUTC(activatedAt) ||
		activatedAt.Before(revision.CreatedAt()) || previousRevision == revision.Reference().Revision {
		return Activation{}, ErrActivationConflict
	}
	return Activation{
		revision: revision, version: version, previousRevision: previousRevision,
		actor: actor, activatedAt: activatedAt,
	}, nil
}

// Revision returns the immutable active revision.
func (activation Activation) Revision() Revision { return activation.revision }

// Version returns the policy's monotonic optimistic activation version.
func (activation Activation) Version() int64 { return activation.version }

// PreviousRevision returns zero only for the first activation.
func (activation Activation) PreviousRevision() uint32 { return activation.previousRevision }

// Actor returns the API-key record that requested the switch.
func (activation Activation) Actor() id.APIKey { return activation.actor }

// ActivatedAt returns the explicit UTC switch time.
func (activation Activation) ActivatedAt() time.Time { return activation.activatedAt }
