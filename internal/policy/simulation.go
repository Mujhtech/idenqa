package policy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// SimulationProgram is one compiled canonical policy usable only through the
// owned deterministic evaluator contract.
type SimulationProgram interface {
	Evaluator
	PolicyDigest() string
}

// SimulationCompiler compiles supplied canonical policy meaning without
// registering or activating it.
type SimulationCompiler interface {
	CompileSimulation(context.Context, []byte) (SimulationProgram, error)
}

// SimulationInput contains explicit synthetic inputs. It cannot load tenant
// production state, read a clock, persist a decision, or activate a revision.
type SimulationInput struct {
	CanonicalPolicy   []byte
	TenantID          id.Tenant
	VerificationID    id.Verification
	AuthorityID       id.Authority
	AcknowledgementID id.Acknowledgement
	Region            string
	EvaluatedAt       time.Time
	Facts             []Fact
}

// Simulation is one immutable deterministic policy evaluation without a
// durable decision or workflow effect.
type Simulation struct {
	canonicalPolicy []byte
	snapshot        Snapshot
	evaluation      Evaluation
}

// Simulator compiles and evaluates caller-supplied synthetic policy inputs.
type Simulator struct{ compiler SimulationCompiler }

// NewSimulator constructs the CEL-neutral simulation application service.
func NewSimulator(compiler SimulationCompiler) (*Simulator, error) {
	if compiler == nil {
		return nil, errors.New("policy simulator: compiler is required")
	}
	return &Simulator{compiler: compiler}, nil
}

// Run compiles supplied canonical meaning and evaluates one immutable
// synthetic snapshot. It never creates an authoritative decision.
func (simulator *Simulator) Run(
	ctx context.Context,
	input SimulationInput,
) (Simulation, error) {
	if simulator == nil || simulator.compiler == nil {
		return Simulation{}, fmt.Errorf("%w: policy simulator", ErrInvalid)
	}
	if err := ctx.Err(); err != nil {
		return Simulation{}, err
	}
	if len(input.CanonicalPolicy) == 0 || len(input.CanonicalPolicy) > policyv1.MaximumDocumentBytes {
		return Simulation{}, fmt.Errorf("%w: simulation policy", ErrInvalid)
	}
	document, err := policyv1.ParseCanonical(input.CanonicalPolicy)
	if err != nil {
		return Simulation{}, fmt.Errorf("%w: simulation policy: %w", ErrInvalid, err)
	}
	policyID, err := id.ParsePolicy(document.PolicyID)
	if err != nil {
		return Simulation{}, fmt.Errorf("%w: simulation policy identifier", ErrInvalid)
	}
	digest, err := policyv1.Digest(document)
	if err != nil {
		return Simulation{}, fmt.Errorf("digest simulation policy: %w", err)
	}

	program, err := simulator.compiler.CompileSimulation(ctx, slices.Clone(input.CanonicalPolicy))
	if err != nil {
		return Simulation{}, fmt.Errorf("compile simulation policy: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Simulation{}, err
	}
	if program == nil || program.PolicyDigest() != digest {
		return Simulation{}, ErrReproduction
	}
	reference := Reference{
		ID: policyID, Revision: document.Revision,
		SchemaMajor: document.SchemaMajor, SchemaMinor: document.SchemaMinor,
		Digest: digest,
	}
	snapshot, err := NewSnapshot(SnapshotInput{
		TenantID: input.TenantID, VerificationID: input.VerificationID,
		AuthorityID: input.AuthorityID, AcknowledgementID: input.AcknowledgementID,
		Region: input.Region, Policy: reference, Evaluator: program.Reference(),
		EvaluatedAt: input.EvaluatedAt, Facts: input.Facts,
	})
	if err != nil {
		return Simulation{}, fmt.Errorf("construct simulation snapshot: %w", err)
	}
	output, err := program.Evaluate(ctx, snapshot)
	if err != nil {
		return Simulation{}, fmt.Errorf("evaluate simulation policy: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Simulation{}, err
	}
	evaluation, err := Resolve(snapshot, output.Results, output.Assurance)
	if err != nil {
		return Simulation{}, fmt.Errorf("resolve simulation policy: %w", err)
	}
	simulation := Simulation{
		canonicalPolicy: slices.Clone(input.CanonicalPolicy),
		snapshot:        snapshot,
		evaluation:      evaluation,
	}
	if err := validateSimulation(simulation); err != nil {
		return Simulation{}, err
	}
	return simulation, nil
}

// CanonicalPolicy returns a defensive copy of the exact simulated document.
func (simulation Simulation) CanonicalPolicy() []byte {
	return slices.Clone(simulation.canonicalPolicy)
}

// Snapshot returns the immutable synthetic fact snapshot.
func (simulation Simulation) Snapshot() Snapshot { return cloneSnapshot(simulation.snapshot) }

// Evaluation returns the immutable deterministic simulation result.
func (simulation Simulation) Evaluation() Evaluation { return cloneEvaluation(simulation.evaluation) }

func validateSimulation(simulation Simulation) error {
	if len(simulation.canonicalPolicy) == 0 ||
		len(simulation.canonicalPolicy) > policyv1.MaximumDocumentBytes ||
		validateSnapshot(simulation.snapshot) != nil ||
		simulation.evaluation.snapshotDigest != simulation.snapshot.digest {
		return ErrReproduction
	}
	document, err := policyv1.ParseCanonical(simulation.canonicalPolicy)
	if err != nil {
		return ErrReproduction
	}
	digest, err := policyv1.Digest(document)
	if err != nil || digest != simulation.snapshot.policy.Digest ||
		document.PolicyID != simulation.snapshot.policy.ID.String() ||
		document.Revision != simulation.snapshot.policy.Revision ||
		document.SchemaMajor != simulation.snapshot.policy.SchemaMajor ||
		document.SchemaMinor != simulation.snapshot.policy.SchemaMinor {
		return ErrReproduction
	}
	restored, err := RestoreEvaluationCanonical(
		simulation.snapshot,
		simulation.evaluation.canonical,
		simulation.evaluation.digest,
	)
	if err != nil || !bytes.Equal(restored.canonical, simulation.evaluation.canonical) {
		return ErrReproduction
	}
	return nil
}
