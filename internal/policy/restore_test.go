package policy_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
)

func TestRestoreCanonicalPolicyValues(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	snapshot, err := policy.NewSnapshot(base.input)
	if err != nil {
		t.Fatal(err)
	}
	restoredSnapshot, err := policy.RestoreSnapshotCanonical(snapshot.Canonical(), snapshot.Digest())
	if err != nil || restoredSnapshot.Digest() != snapshot.Digest() {
		t.Fatalf("RestoreSnapshotCanonical() digest = %q, error = %v", restoredSnapshot.Digest(), err)
	}
	evaluation, err := policy.Resolve(snapshot, verifiedResults(base), "global_individual_substantial.1")
	if err != nil {
		t.Fatal(err)
	}
	restoredEvaluation, err := policy.RestoreEvaluationCanonical(
		restoredSnapshot,
		evaluation.Canonical(),
		evaluation.Digest(),
	)
	if err != nil || restoredEvaluation.Digest() != evaluation.Digest() {
		t.Fatalf("RestoreEvaluationCanonical() digest = %q, error = %v", restoredEvaluation.Digest(), err)
	}
	decision, err := policy.NewDecision(policy.DecisionInput{
		ID: base.decision, Snapshot: snapshot, Evaluation: evaluation,
		Actor: policy.ActorMachine, DecidedAt: base.now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	restoredDecision, err := policy.RestoreDecisionCanonical(
		restoredSnapshot,
		restoredEvaluation,
		decision.Canonical(),
		decision.Digest(),
	)
	if err != nil || restoredDecision.Digest() != decision.Digest() ||
		restoredDecision.ID().String() != decision.ID().String() {
		t.Fatalf("RestoreDecisionCanonical() digest = %q, error = %v", restoredDecision.Digest(), err)
	}
}

func TestRestoreSnapshotCanonicalRejectsMalformedStoredInput(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	snapshot, err := policy.NewSnapshot(base.input)
	if err != nil {
		t.Fatal(err)
	}
	canonical := snapshot.Canonical()
	tests := []struct {
		name    string
		encoded []byte
		digest  string
	}{
		{name: "empty", encoded: nil, digest: snapshot.Digest()},
		{name: "oversized", encoded: bytes.Repeat([]byte{'x'}, policy.MaximumSnapshotBytes+1), digest: snapshot.Digest()},
		{name: "unknown field", encoded: bytes.Replace(canonical, []byte(`{"schema_major":1`), []byte(`{"unknown":true,"schema_major":1`), 1), digest: snapshot.Digest()},
		{name: "duplicate field", encoded: bytes.Replace(canonical, []byte(`{"schema_major":1`), []byte(`{"schema_major":1,"schema_major":1`), 1), digest: snapshot.Digest()},
		{name: "trailing value", encoded: append(bytes.Clone(canonical), []byte(` {}`)...), digest: snapshot.Digest()},
		{name: "non object", encoded: []byte(`[]`), digest: snapshot.Digest()},
		{name: "noncanonical whitespace", encoded: append([]byte{' '}, canonical...), digest: snapshot.Digest()},
		{name: "unsupported schema", encoded: bytes.Replace(canonical, []byte(`"schema_major":1`), []byte(`"schema_major":2`), 1), digest: snapshot.Digest()},
		{name: "wrong identifier", encoded: bytes.Replace(canonical, []byte(`"tenant_id":"ten_`), []byte(`"tenant_id":"ver_`), 1), digest: snapshot.Digest()},
		{name: "wrong digest", encoded: canonical, digest: strings.Repeat("f", 64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := policy.RestoreSnapshotCanonical(test.encoded, test.digest)
			if !errors.Is(err, policy.ErrReproduction) {
				t.Fatalf("RestoreSnapshotCanonical() error = %v", err)
			}
		})
	}
}

func TestRestoreEvaluationCanonicalRejectsChangedDerivedMeaning(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	snapshot, _ := policy.NewSnapshot(base.input)
	evaluation, _ := policy.Resolve(snapshot, verifiedResults(base), "moderate")
	canonical := evaluation.Canonical()
	tests := []struct {
		name    string
		encoded []byte
		digest  string
	}{
		{name: "oversized", encoded: bytes.Repeat([]byte{'x'}, policy.MaximumEvaluationBytes+1), digest: evaluation.Digest()},
		{name: "unknown", encoded: bytes.Replace(canonical, []byte(`{"schema_major":1`), []byte(`{"unknown":true,"schema_major":1`), 1), digest: evaluation.Digest()},
		{name: "duplicate", encoded: bytes.Replace(canonical, []byte(`{"schema_major":1`), []byte(`{"schema_major":1,"schema_major":1`), 1), digest: evaluation.Digest()},
		{name: "selected", encoded: bytes.Replace(canonical, []byte(`"selected":"complete_verified"`), []byte(`"selected":"route_manual_review"`), 1), digest: evaluation.Digest()},
		{name: "outcome", encoded: bytes.Replace(canonical, []byte(`"outcome":"verified"`), []byte(`"outcome":"not_verified"`), 1), digest: evaluation.Digest()},
		{name: "reason", encoded: bytes.Replace(canonical, []byte(`document_authenticity_satisfied`), []byte(`document_authenticity_changed`), 1), digest: evaluation.Digest()},
		{name: "digest", encoded: canonical, digest: strings.Repeat("f", 64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := policy.RestoreEvaluationCanonical(snapshot, test.encoded, test.digest)
			if !errors.Is(err, policy.ErrReproduction) {
				t.Fatalf("RestoreEvaluationCanonical() error = %v", err)
			}
		})
	}
}

func TestDecisionCanonicalIsImmutableAndRejectsChangedLineage(t *testing.T) {
	t.Parallel()
	base := policyFixture(t)
	snapshot, _ := policy.NewSnapshot(base.input)
	evaluation, _ := policy.Resolve(snapshot, verifiedResults(base), "moderate")
	previous, _ := id.ParseDecision("dec_01K3P4NQF00000000000000001")
	decision, err := policy.NewDecision(policy.DecisionInput{
		ID: base.decision, Snapshot: snapshot, Evaluation: evaluation, Actor: policy.ActorHuman,
		Supersedes: previous, DecidedAt: base.now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	canonical := decision.Canonical()
	canonical[0] = 'X'
	if decision.Canonical()[0] == 'X' {
		t.Fatal("Decision.Canonical() aliases internal bytes")
	}
	tests := []struct {
		name    string
		encoded []byte
		digest  string
	}{
		{name: "identity", encoded: bytes.Replace(decision.Canonical(), []byte(base.decision.String()), []byte(previous.String()), 1), digest: decision.Digest()},
		{name: "actor", encoded: bytes.Replace(decision.Canonical(), []byte(`"actor":"human"`), []byte(`"actor":"machine"`), 1), digest: decision.Digest()},
		{name: "supersession", encoded: bytes.Replace(decision.Canonical(), []byte(previous.String()), []byte(base.decision.String()), 1), digest: decision.Digest()},
		{name: "version", encoded: bytes.Replace(decision.Canonical(), []byte(`"schema_major":1`), []byte(`"schema_major":2`), 1), digest: decision.Digest()},
		{name: "oversized", encoded: bytes.Repeat([]byte{'x'}, policy.MaximumDecisionBytes+1), digest: decision.Digest()},
		{name: "digest", encoded: decision.Canonical(), digest: strings.Repeat("f", 64)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := policy.RestoreDecisionCanonical(snapshot, evaluation, test.encoded, test.digest)
			if !errors.Is(err, policy.ErrReproduction) {
				t.Fatalf("RestoreDecisionCanonical() error = %v", err)
			}
		})
	}
}
