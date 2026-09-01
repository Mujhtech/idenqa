package policy

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"
)

// DecisionBundleSchemaMajor and DecisionBundleSchemaMinor version the portable
// reproduction envelope independently of a future public policy language.
const (
	DecisionBundleSchemaMajor = 1
	DecisionBundleSchemaMinor = 0
)

// DecisionBundle is an immutable, self-checking export of one authoritative
// decision and every canonical input required to reproduce it offline.
type DecisionBundle struct {
	decision  Decision
	canonical []byte
	digest    string
}

type canonicalDecisionBundle struct {
	SchemaMajor      uint16          `json:"schema_major"`
	SchemaMinor      uint16          `json:"schema_minor"`
	BundleDigest     string          `json:"bundle_digest"`
	DecisionID       string          `json:"decision_id"`
	TenantID         string          `json:"tenant_id"`
	VerificationID   string          `json:"verification_id"`
	SnapshotDigest   string          `json:"snapshot_digest"`
	EvaluationDigest string          `json:"evaluation_digest"`
	DecisionDigest   string          `json:"decision_digest"`
	Snapshot         json.RawMessage `json:"snapshot"`
	Evaluation       json.RawMessage `json:"evaluation"`
	Decision         json.RawMessage `json:"decision"`
}

type canonicalDecisionBundlePayload struct {
	SchemaMajor      uint16          `json:"schema_major"`
	SchemaMinor      uint16          `json:"schema_minor"`
	DecisionID       string          `json:"decision_id"`
	TenantID         string          `json:"tenant_id"`
	VerificationID   string          `json:"verification_id"`
	SnapshotDigest   string          `json:"snapshot_digest"`
	EvaluationDigest string          `json:"evaluation_digest"`
	DecisionDigest   string          `json:"decision_digest"`
	Snapshot         json.RawMessage `json:"snapshot"`
	Evaluation       json.RawMessage `json:"evaluation"`
	Decision         json.RawMessage `json:"decision"`
}

// ReproductionReport is a bounded diagnostic summary. It deliberately omits
// facts, reason codes, canonical JSON, evidence references, and raw evidence.
type ReproductionReport struct {
	SchemaMajor      uint16     `json:"schema_major"`
	SchemaMinor      uint16     `json:"schema_minor"`
	DecisionID       string     `json:"decision_id"`
	TenantID         string     `json:"tenant_id"`
	VerificationID   string     `json:"verification_id"`
	PolicyID         string     `json:"policy_id"`
	PolicyRevision   uint32     `json:"policy_revision"`
	PolicyDigest     string     `json:"policy_digest"`
	EvaluatorMajor   uint16     `json:"evaluator_major"`
	EvaluatorMinor   uint16     `json:"evaluator_minor"`
	EvaluatorDigest  string     `json:"evaluator_digest"`
	SnapshotDigest   string     `json:"snapshot_digest"`
	EvaluationDigest string     `json:"evaluation_digest"`
	DecisionDigest   string     `json:"decision_digest"`
	BundleDigest     string     `json:"bundle_digest"`
	Directive        Directive  `json:"directive"`
	Outcome          Outcome    `json:"outcome"`
	Assurance        string     `json:"assurance"`
	Actor            ActorClass `json:"actor"`
	Supersedes       string     `json:"supersedes,omitempty"`
	FactCount        int        `json:"fact_count"`
	RequirementCount int        `json:"requirement_count"`
	EvaluatedAt      time.Time  `json:"evaluated_at"`
	DecidedAt        time.Time  `json:"decided_at"`
	Reproduced       bool       `json:"reproduced"`
}

// NewDecisionBundle strictly reproduces a decision before exporting it.
func NewDecisionBundle(decision Decision) (DecisionBundle, ReproductionReport, error) {
	if _, err := Reproduce(decision); err != nil {
		return DecisionBundle{}, ReproductionReport{}, err
	}
	snapshot, evaluation := decision.Snapshot(), decision.Evaluation()
	if _, err := RestoreDecisionCanonical(snapshot, evaluation, decision.Canonical(), decision.Digest()); err != nil {
		return DecisionBundle{}, ReproductionReport{}, ErrReproduction
	}
	payload := bundlePayload(decision)
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return DecisionBundle{}, ReproductionReport{}, fmt.Errorf("encode decision bundle payload: %w", err)
	}
	sum := sha256.Sum256(payloadBytes)
	digest := hex.EncodeToString(sum[:])
	envelope := canonicalDecisionBundle{
		SchemaMajor: payload.SchemaMajor, SchemaMinor: payload.SchemaMinor, BundleDigest: digest,
		DecisionID: payload.DecisionID, TenantID: payload.TenantID, VerificationID: payload.VerificationID,
		SnapshotDigest: payload.SnapshotDigest, EvaluationDigest: payload.EvaluationDigest,
		DecisionDigest: payload.DecisionDigest, Snapshot: payload.Snapshot,
		Evaluation: payload.Evaluation, Decision: payload.Decision,
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return DecisionBundle{}, ReproductionReport{}, fmt.Errorf("encode decision bundle: %w", err)
	}
	if len(encoded) > MaximumBundleBytes {
		return DecisionBundle{}, ReproductionReport{}, ErrInvalid
	}
	bundle := DecisionBundle{decision: decision, canonical: slices.Clone(encoded), digest: digest}
	return bundle, reportOf(bundle), nil
}

// RestoreDecisionBundle closes over the envelope, verifies all nested digests,
// and reruns policy resolution without a database, provider, model, or network.
func RestoreDecisionBundle(encoded []byte) (DecisionBundle, ReproductionReport, error) {
	if len(encoded) == 0 || len(encoded) > MaximumBundleBytes {
		return DecisionBundle{}, ReproductionReport{}, ErrReproduction
	}
	var stored canonicalDecisionBundle
	if err := decodeClosed(encoded, &stored); err != nil ||
		stored.SchemaMajor != DecisionBundleSchemaMajor || stored.SchemaMinor != DecisionBundleSchemaMinor ||
		!validDigest(stored.BundleDigest) {
		return DecisionBundle{}, ReproductionReport{}, ErrReproduction
	}
	payload := canonicalDecisionBundlePayload{
		SchemaMajor: stored.SchemaMajor, SchemaMinor: stored.SchemaMinor,
		DecisionID: stored.DecisionID, TenantID: stored.TenantID, VerificationID: stored.VerificationID,
		SnapshotDigest: stored.SnapshotDigest, EvaluationDigest: stored.EvaluationDigest,
		DecisionDigest: stored.DecisionDigest, Snapshot: stored.Snapshot,
		Evaluation: stored.Evaluation, Decision: stored.Decision,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return DecisionBundle{}, ReproductionReport{}, ErrReproduction
	}
	sum := sha256.Sum256(payloadBytes)
	if hex.EncodeToString(sum[:]) != stored.BundleDigest {
		return DecisionBundle{}, ReproductionReport{}, ErrReproduction
	}
	snapshot, err := RestoreSnapshotCanonical(stored.Snapshot, stored.SnapshotDigest)
	if err != nil || snapshot.TenantID().String() != stored.TenantID ||
		snapshot.VerificationID().String() != stored.VerificationID {
		return DecisionBundle{}, ReproductionReport{}, ErrReproduction
	}
	evaluation, err := RestoreEvaluationCanonical(snapshot, stored.Evaluation, stored.EvaluationDigest)
	if err != nil {
		return DecisionBundle{}, ReproductionReport{}, ErrReproduction
	}
	decision, err := RestoreDecisionCanonical(snapshot, evaluation, stored.Decision, stored.DecisionDigest)
	if err != nil || decision.ID().String() != stored.DecisionID {
		return DecisionBundle{}, ReproductionReport{}, ErrReproduction
	}
	bundle, report, err := NewDecisionBundle(decision)
	if err != nil || bundle.digest != stored.BundleDigest || !bytes.Equal(bundle.canonical, encoded) {
		return DecisionBundle{}, ReproductionReport{}, ErrReproduction
	}
	return bundle, report, nil
}

// Decision returns a defensive copy of the verified decision.
func (bundle DecisionBundle) Decision() Decision {
	decision := bundle.decision
	decision.snapshot = cloneSnapshot(decision.snapshot)
	decision.evaluation = cloneEvaluation(decision.evaluation)
	decision.canonical = slices.Clone(decision.canonical)
	return decision
}

// Canonical returns a defensive copy of the portable canonical envelope.
func (bundle DecisionBundle) Canonical() []byte { return slices.Clone(bundle.canonical) }

// Digest returns the SHA-256 digest of the envelope payload.
func (bundle DecisionBundle) Digest() string { return bundle.digest }

func bundlePayload(decision Decision) canonicalDecisionBundlePayload {
	snapshot, evaluation := decision.Snapshot(), decision.Evaluation()
	return canonicalDecisionBundlePayload{
		SchemaMajor: DecisionBundleSchemaMajor, SchemaMinor: DecisionBundleSchemaMinor,
		DecisionID: decision.ID().String(), TenantID: snapshot.TenantID().String(),
		VerificationID: snapshot.VerificationID().String(), SnapshotDigest: snapshot.Digest(),
		EvaluationDigest: evaluation.Digest(), DecisionDigest: decision.Digest(),
		Snapshot: snapshot.Canonical(), Evaluation: evaluation.Canonical(), Decision: decision.Canonical(),
	}
}

func reportOf(bundle DecisionBundle) ReproductionReport {
	decision := bundle.decision
	snapshot, evaluation := decision.Snapshot(), decision.Evaluation()
	reference, evaluator := snapshot.Policy(), snapshot.Evaluator()
	supersedes := ""
	if !decision.Supersedes().IsZero() {
		supersedes = decision.Supersedes().String()
	}
	return ReproductionReport{
		SchemaMajor: DecisionBundleSchemaMajor, SchemaMinor: DecisionBundleSchemaMinor,
		DecisionID: decision.ID().String(), TenantID: snapshot.TenantID().String(),
		VerificationID: snapshot.VerificationID().String(), PolicyID: reference.ID.String(),
		PolicyRevision: reference.Revision, PolicyDigest: reference.Digest,
		EvaluatorMajor: evaluator.Major, EvaluatorMinor: evaluator.Minor, EvaluatorDigest: evaluator.Digest,
		SnapshotDigest: snapshot.Digest(), EvaluationDigest: evaluation.Digest(),
		DecisionDigest: decision.Digest(), BundleDigest: bundle.digest,
		Directive: evaluation.Selected(), Outcome: evaluation.Outcome(), Assurance: evaluation.Assurance(),
		Actor: decision.Actor(), Supersedes: supersedes, FactCount: len(snapshot.Facts()),
		RequirementCount: len(evaluation.Results()), EvaluatedAt: snapshot.EvaluatedAt(),
		DecidedAt: decision.DecidedAt(), Reproduced: true,
	}
}
