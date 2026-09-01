package policy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/policy"
)

func TestNewDecisionBundleRoundTripsExactMeaning(t *testing.T) {
	t.Parallel()
	decision := verifiedDecision(t)
	bundle, report, err := policy.NewDecisionBundle(decision)
	if err != nil {
		t.Fatal(err)
	}
	restored, restoredReport, err := policy.RestoreDecisionBundle(bundle.Canonical())
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Digest() != restored.Digest() || !slices.Equal(bundle.Canonical(), restored.Canonical()) ||
		restored.Decision().Digest() != decision.Digest() || report != restoredReport || !report.Reproduced {
		t.Fatal("portable bundle did not preserve exact reproduced meaning")
	}
	if report.FactCount != len(decision.Snapshot().Facts()) ||
		report.RequirementCount != len(decision.Evaluation().Results()) {
		t.Fatal("report counts do not describe the decision")
	}
}

func TestRestoreDecisionBundleRejectsClosedEnvelopeViolations(t *testing.T) {
	t.Parallel()
	bundle, _, err := policy.NewDecisionBundle(verifiedDecision(t))
	if err != nil {
		t.Fatal(err)
	}
	canonical := bundle.Canonical()
	tests := []struct {
		name  string
		input []byte
	}{
		{name: "empty"},
		{name: "malformed", input: []byte("{")},
		{name: "unknown field", input: append(canonical[:len(canonical)-1], []byte(`,"extra":true}`)...)},
		{name: "trailing value", input: append(slices.Clone(canonical), []byte(`{}`)...)},
		{name: "non canonical whitespace", input: append([]byte(" "), canonical...)},
		{name: "oversized", input: bytes.Repeat([]byte("x"), policy.MaximumBundleBytes+1)},
		{name: "wrong schema", input: bytes.Replace(canonical, []byte(`"schema_major":1`), []byte(`"schema_major":2`), 1)},
		{name: "wrong bundle digest", input: bytes.Replace(canonical, []byte(bundle.Digest()), []byte(strings.Repeat("f", 64)), 1)},
		{name: "tampered nested snapshot", input: bytes.Replace(canonical, []byte(`"region":"tenant_home"`), []byte(`"region":"tenant_away"`), 1)},
		{name: "tampered decision identity", input: bytes.Replace(canonical, []byte(`"decision_id":"dec_`), []byte(`"decision_id":"bad_`), 1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := policy.RestoreDecisionBundle(test.input); !errors.Is(err, policy.ErrReproduction) {
				t.Fatalf("RestoreDecisionBundle() error = %v", err)
			}
		})
	}
}

func TestDecisionBundleAccessorsDoNotAliasInternalState(t *testing.T) {
	t.Parallel()
	bundle, _, err := policy.NewDecisionBundle(verifiedDecision(t))
	if err != nil {
		t.Fatal(err)
	}
	canonical := bundle.Canonical()
	canonical[0] = 'X'
	decision := bundle.Decision()
	decisionCanonical := decision.Canonical()
	decisionCanonical[0] = 'X'
	if bundle.Canonical()[0] == 'X' || bundle.Decision().Canonical()[0] == 'X' {
		t.Fatal("bundle accessors alias internal state")
	}
}

func TestReproductionReportOmitsCanonicalInputsAndReasons(t *testing.T) {
	t.Parallel()
	decision := verifiedDecision(t)
	_, report, err := policy.NewDecisionBundle(decision)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"facts", "results", "reason_codes", "canonical", "observation_ids"} {
		if bytes.Contains(encoded, []byte(`"`+forbidden+`"`)) {
			t.Fatalf("report contains forbidden field %q", forbidden)
		}
	}
}

func FuzzRestoreDecisionBundleRejectsChangedBytes(f *testing.F) {
	decision := verifiedDecision(f)
	bundle, _, err := policy.NewDecisionBundle(decision)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(bundle.Canonical(), uint16(0))
	f.Fuzz(func(t *testing.T, encoded []byte, offset uint16) {
		if len(encoded) == 0 || len(encoded) > policy.MaximumBundleBytes {
			return
		}
		changed := slices.Clone(encoded)
		index := int(offset) % len(changed)
		changed[index] ^= 1
		if bytes.Equal(changed, bundle.Canonical()) {
			t.Fatal("fuzz mutation did not change input")
		}
		if _, _, err := policy.RestoreDecisionBundle(changed); err == nil {
			t.Fatal("changed portable bundle was accepted")
		}
	})
}

func verifiedDecision(t testing.TB) policy.Decision {
	t.Helper()
	base := policyFixture(t)
	snapshot, err := policy.NewSnapshot(base.input)
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := policy.Resolve(snapshot, verifiedResults(base), "global_individual_substantial.1")
	if err != nil {
		t.Fatal(err)
	}
	decision, err := policy.NewDecision(policy.DecisionInput{
		ID: base.decision, Snapshot: snapshot, Evaluation: evaluation,
		Actor: policy.ActorMachine, DecidedAt: base.now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}
