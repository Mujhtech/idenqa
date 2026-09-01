package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
)

// Simulation bundle versions and limits are independent of policy-document
// and decision-bundle versions.
const (
	SimulationBundleSchemaMajor  = 1
	SimulationBundleSchemaMinor  = 0
	MaximumSimulationBundleBytes = policyv1.MaximumDocumentBytes + MaximumSnapshotBytes +
		MaximumEvaluationBytes + 16*1024
)

// SimulationBundle is a portable self-checking synthetic evaluation. It is
// not an authoritative decision, policy activation, or production-state proof.
type SimulationBundle struct {
	simulation Simulation
	canonical  []byte
	digest     string
}

type canonicalSimulationBundle struct {
	SchemaMajor      uint16          `json:"schema_major"`
	SchemaMinor      uint16          `json:"schema_minor"`
	BundleDigest     string          `json:"bundle_digest"`
	TenantID         string          `json:"tenant_id"`
	VerificationID   string          `json:"verification_id"`
	PolicyID         string          `json:"policy_id"`
	PolicyRevision   uint32          `json:"policy_revision"`
	PolicyDigest     string          `json:"policy_digest"`
	EvaluatorDigest  string          `json:"evaluator_digest"`
	SnapshotDigest   string          `json:"snapshot_digest"`
	EvaluationDigest string          `json:"evaluation_digest"`
	Policy           json.RawMessage `json:"policy"`
	Snapshot         json.RawMessage `json:"snapshot"`
	Evaluation       json.RawMessage `json:"evaluation"`
}

type canonicalSimulationBundlePayload struct {
	SchemaMajor      uint16          `json:"schema_major"`
	SchemaMinor      uint16          `json:"schema_minor"`
	TenantID         string          `json:"tenant_id"`
	VerificationID   string          `json:"verification_id"`
	PolicyID         string          `json:"policy_id"`
	PolicyRevision   uint32          `json:"policy_revision"`
	PolicyDigest     string          `json:"policy_digest"`
	EvaluatorDigest  string          `json:"evaluator_digest"`
	SnapshotDigest   string          `json:"snapshot_digest"`
	EvaluationDigest string          `json:"evaluation_digest"`
	Policy           json.RawMessage `json:"policy"`
	Snapshot         json.RawMessage `json:"snapshot"`
	Evaluation       json.RawMessage `json:"evaluation"`
}

// SimulationReport is a bounded safe summary that omits facts, source
// references, reason codes, expressions, and canonical input bytes.
type SimulationReport struct {
	SchemaMajor          uint16    `json:"schema_major"`
	SchemaMinor          uint16    `json:"schema_minor"`
	TenantID             string    `json:"tenant_id"`
	VerificationID       string    `json:"verification_id"`
	PolicyID             string    `json:"policy_id"`
	PolicyRevision       uint32    `json:"policy_revision"`
	PolicyDigest         string    `json:"policy_digest"`
	EvaluatorMajor       uint16    `json:"evaluator_major"`
	EvaluatorMinor       uint16    `json:"evaluator_minor"`
	EvaluatorDigest      string    `json:"evaluator_digest"`
	SnapshotDigest       string    `json:"snapshot_digest"`
	EvaluationDigest     string    `json:"evaluation_digest"`
	BundleDigest         string    `json:"bundle_digest"`
	Directive            Directive `json:"directive"`
	Outcome              Outcome   `json:"outcome,omitempty"`
	Assurance            string    `json:"assurance,omitempty"`
	AuthorisesCompletion bool      `json:"authorises_completion"`
	FactCount            int       `json:"fact_count"`
	RequirementCount     int       `json:"requirement_count"`
	EvaluatedAt          time.Time `json:"evaluated_at"`
	Reproduced           bool      `json:"reproduced"`
}

// NewSimulationBundle validates and closes over every canonical input needed
// to reproduce one synthetic evaluation without an expression engine.
func NewSimulationBundle(
	simulation Simulation,
) (SimulationBundle, SimulationReport, error) {
	if err := validateSimulation(simulation); err != nil {
		return SimulationBundle{}, SimulationReport{}, err
	}
	payload := simulationBundlePayload(simulation)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return SimulationBundle{}, SimulationReport{}, fmt.Errorf("encode simulation bundle payload: %w", err)
	}
	sum := sha256.Sum256(payloadBytes)
	digest := hex.EncodeToString(sum[:])
	envelope := canonicalSimulationBundle{
		SchemaMajor: payload.SchemaMajor, SchemaMinor: payload.SchemaMinor,
		BundleDigest: digest, TenantID: payload.TenantID,
		VerificationID: payload.VerificationID, PolicyID: payload.PolicyID,
		PolicyRevision: payload.PolicyRevision, PolicyDigest: payload.PolicyDigest,
		EvaluatorDigest: payload.EvaluatorDigest, SnapshotDigest: payload.SnapshotDigest,
		EvaluationDigest: payload.EvaluationDigest, Policy: payload.Policy,
		Snapshot: payload.Snapshot, Evaluation: payload.Evaluation,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return SimulationBundle{}, SimulationReport{}, fmt.Errorf("encode simulation bundle: %w", err)
	}
	if len(encoded) > MaximumSimulationBundleBytes {
		return SimulationBundle{}, SimulationReport{}, ErrInvalid
	}
	bundle := SimulationBundle{
		simulation: cloneSimulation(simulation),
		canonical:  slices.Clone(encoded),
		digest:     digest,
	}
	return bundle, simulationReportOf(bundle), nil
}

// RestoreSimulationBundle verifies the envelope and nested canonical meaning,
// then re-enters deterministic resolution without CEL, persistence, or I/O.
func RestoreSimulationBundle(
	encoded []byte,
) (SimulationBundle, SimulationReport, error) {
	if len(encoded) == 0 || len(encoded) > MaximumSimulationBundleBytes {
		return SimulationBundle{}, SimulationReport{}, ErrReproduction
	}
	var stored canonicalSimulationBundle
	if err := decodeClosed(encoded, &stored); err != nil ||
		stored.SchemaMajor != SimulationBundleSchemaMajor ||
		stored.SchemaMinor != SimulationBundleSchemaMinor ||
		!validDigest(stored.BundleDigest) {
		return SimulationBundle{}, SimulationReport{}, ErrReproduction
	}
	payload := canonicalSimulationBundlePayload{
		SchemaMajor: stored.SchemaMajor, SchemaMinor: stored.SchemaMinor,
		TenantID: stored.TenantID, VerificationID: stored.VerificationID,
		PolicyID: stored.PolicyID, PolicyRevision: stored.PolicyRevision,
		PolicyDigest: stored.PolicyDigest, EvaluatorDigest: stored.EvaluatorDigest,
		SnapshotDigest: stored.SnapshotDigest, EvaluationDigest: stored.EvaluationDigest,
		Policy: stored.Policy, Snapshot: stored.Snapshot, Evaluation: stored.Evaluation,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return SimulationBundle{}, SimulationReport{}, ErrReproduction
	}
	sum := sha256.Sum256(payloadBytes)
	if hex.EncodeToString(sum[:]) != stored.BundleDigest {
		return SimulationBundle{}, SimulationReport{}, ErrReproduction
	}
	if _, err := policyv1.ParseCanonical(stored.Policy); err != nil {
		return SimulationBundle{}, SimulationReport{}, ErrReproduction
	}
	snapshot, err := RestoreSnapshotCanonical(stored.Snapshot, stored.SnapshotDigest)
	if err != nil {
		return SimulationBundle{}, SimulationReport{}, ErrReproduction
	}
	evaluation, err := RestoreEvaluationCanonical(snapshot, stored.Evaluation, stored.EvaluationDigest)
	if err != nil {
		return SimulationBundle{}, SimulationReport{}, ErrReproduction
	}
	simulation := Simulation{
		canonicalPolicy: slices.Clone(stored.Policy), snapshot: snapshot, evaluation: evaluation,
	}
	if snapshot.TenantID().String() != stored.TenantID ||
		snapshot.VerificationID().String() != stored.VerificationID ||
		snapshot.Policy().ID.String() != stored.PolicyID ||
		snapshot.Policy().Revision != stored.PolicyRevision ||
		snapshot.Policy().Digest != stored.PolicyDigest ||
		snapshot.Evaluator().Digest != stored.EvaluatorDigest ||
		validateSimulation(simulation) != nil {
		return SimulationBundle{}, SimulationReport{}, ErrReproduction
	}
	bundle, report, err := NewSimulationBundle(simulation)
	if err != nil || bundle.digest != stored.BundleDigest || !bytes.Equal(bundle.canonical, encoded) {
		return SimulationBundle{}, SimulationReport{}, ErrReproduction
	}
	return bundle, report, nil
}

// Simulation returns a defensive copy of the reproduced synthetic evaluation.
func (bundle SimulationBundle) Simulation() Simulation {
	return cloneSimulation(bundle.simulation)
}

// Canonical returns a defensive copy of the portable canonical envelope.
func (bundle SimulationBundle) Canonical() []byte { return slices.Clone(bundle.canonical) }

// Digest returns the SHA-256 digest of the canonical bundle payload.
func (bundle SimulationBundle) Digest() string { return bundle.digest }

func simulationBundlePayload(simulation Simulation) canonicalSimulationBundlePayload {
	snapshot, evaluation := simulation.snapshot, simulation.evaluation
	reference, evaluator := snapshot.policy, snapshot.evaluator
	return canonicalSimulationBundlePayload{
		SchemaMajor: SimulationBundleSchemaMajor, SchemaMinor: SimulationBundleSchemaMinor,
		TenantID: snapshot.tenantID.String(), VerificationID: snapshot.verificationID.String(),
		PolicyID: reference.ID.String(), PolicyRevision: reference.Revision,
		PolicyDigest: reference.Digest, EvaluatorDigest: evaluator.Digest,
		SnapshotDigest: snapshot.digest, EvaluationDigest: evaluation.digest,
		Policy: slices.Clone(simulation.canonicalPolicy), Snapshot: snapshot.Canonical(),
		Evaluation: evaluation.Canonical(),
	}
}

func simulationReportOf(bundle SimulationBundle) SimulationReport {
	simulation := bundle.simulation
	snapshot, evaluation := simulation.snapshot, simulation.evaluation
	reference, evaluator := snapshot.policy, snapshot.evaluator
	return SimulationReport{
		SchemaMajor: SimulationBundleSchemaMajor, SchemaMinor: SimulationBundleSchemaMinor,
		TenantID: snapshot.tenantID.String(), VerificationID: snapshot.verificationID.String(),
		PolicyID: reference.ID.String(), PolicyRevision: reference.Revision,
		PolicyDigest: reference.Digest, EvaluatorMajor: evaluator.Major,
		EvaluatorMinor: evaluator.Minor, EvaluatorDigest: evaluator.Digest,
		SnapshotDigest: snapshot.digest, EvaluationDigest: evaluation.digest,
		BundleDigest: bundle.digest, Directive: evaluation.selected, Outcome: evaluation.outcome,
		Assurance: evaluation.assurance, AuthorisesCompletion: evaluation.AuthorisesCompletion(),
		FactCount: len(snapshot.facts), RequirementCount: len(evaluation.results),
		EvaluatedAt: snapshot.evaluatedAt, Reproduced: true,
	}
}

func cloneSimulation(simulation Simulation) Simulation {
	return Simulation{
		canonicalPolicy: slices.Clone(simulation.canonicalPolicy),
		snapshot:        cloneSnapshot(simulation.snapshot),
		evaluation:      cloneEvaluation(simulation.evaluation),
	}
}
