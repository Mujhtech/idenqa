package policy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
)

func TestSimulationBundleRoundTripsExactSyntheticMeaning(t *testing.T) {
	t.Parallel()
	simulation := runSimulation(t, policy.RequirementSatisfied, policy.DirectiveCompleteVerified)
	bundle, report, err := policy.NewSimulationBundle(simulation)
	if err != nil {
		t.Fatal(err)
	}
	restored, restoredReport, err := policy.RestoreSimulationBundle(bundle.Canonical())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Digest() != bundle.Digest() || !bytes.Equal(restored.Canonical(), bundle.Canonical()) ||
		restored.Simulation().Snapshot().Digest() != simulation.Snapshot().Digest() ||
		restored.Simulation().Evaluation().Digest() != simulation.Evaluation().Digest() ||
		restoredReport != report || !report.Reproduced || !report.AuthorisesCompletion ||
		report.Directive != policy.DirectiveCompleteVerified || report.Outcome != policy.OutcomeVerified {
		t.Fatalf("restored report = %+v", restoredReport)
	}
}

func TestSimulationReportOmitsSyntheticInputsAndExpressions(t *testing.T) {
	t.Parallel()
	simulation := runSimulation(t, policy.RequirementUnavailable, policy.DirectiveRequestInput)
	_, report, err := policy.NewSimulationBundle(simulation)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		`"facts"`, `"reason_codes"`, `"source"`, `"expression"`,
		"document.authenticity", "synthetic_scenario", "processing_authority",
	} {
		if strings.Contains(string(encoded), forbidden) {
			t.Fatalf("safe report contains %q: %s", forbidden, encoded)
		}
	}
	if report.AuthorisesCompletion || report.Outcome != "" ||
		report.Directive != policy.DirectiveRequestInput {
		t.Fatalf("nonterminal report = %+v", report)
	}
}

func TestSimulationBundleOwnsReturnedBytesAndSimulation(t *testing.T) {
	t.Parallel()
	bundle, _, err := policy.NewSimulationBundle(
		runSimulation(t, policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
	)
	if err != nil {
		t.Fatal(err)
	}
	canonical := bundle.Canonical()
	canonical[0] = ' '
	simulation := bundle.Simulation()
	policyBytes := simulation.CanonicalPolicy()
	policyBytes[0] = ' '
	if bundle.Canonical()[0] == ' ' || bundle.Simulation().CanonicalPolicy()[0] == ' ' {
		t.Fatal("bundle exposed mutable canonical state")
	}
}

func TestRestoreSimulationBundleRejectsEnvelopeAndNestedTampering(t *testing.T) {
	t.Parallel()
	bundle, _, err := policy.NewSimulationBundle(
		runSimulation(t, policy.RequirementSatisfied, policy.DirectiveCompleteVerified),
	)
	if err != nil {
		t.Fatal(err)
	}
	canonical := bundle.Canonical()
	tests := []struct {
		name    string
		encoded []byte
	}{
		{name: "empty"},
		{name: "oversized", encoded: bytes.Repeat([]byte{'x'}, policy.MaximumSimulationBundleBytes+1)},
		{name: "whitespace", encoded: append([]byte{' '}, canonical...)},
		{name: "trailing", encoded: append(append([]byte(nil), canonical...), []byte(`{}`)...)},
		{name: "duplicate field", encoded: []byte(strings.Replace(string(canonical), `{"schema_major":1,`, `{"schema_major":1,"schema_major":1,`, 1))},
		{name: "unknown field", encoded: changedBundleField(t, canonical, "unknown", `true`)},
		{name: "schema major", encoded: changedBundleField(t, canonical, "schema_major", `2`)},
		{name: "bundle digest", encoded: changedBundleField(t, canonical, "bundle_digest", `"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"`)},
		{name: "tenant", encoded: changedBundleField(t, canonical, "tenant_id", `"ten_01K3P4NQF00000000000000009"`)},
		{name: "verification", encoded: changedBundleField(t, canonical, "verification_id", `"ver_01K3P4NQF00000000000000009"`)},
		{name: "policy identity", encoded: changedBundleField(t, canonical, "policy_id", `"pol_01K3P4NQF00000000000000009"`)},
		{name: "policy revision", encoded: changedBundleField(t, canonical, "policy_revision", `4`)},
		{name: "policy digest", encoded: changedBundleField(t, canonical, "policy_digest", `"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"`)},
		{name: "evaluator digest", encoded: changedBundleField(t, canonical, "evaluator_digest", `"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"`)},
		{name: "snapshot digest", encoded: changedBundleField(t, canonical, "snapshot_digest", `"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"`)},
		{name: "evaluation digest", encoded: changedBundleField(t, canonical, "evaluation_digest", `"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"`)},
		{name: "policy", encoded: changedBundleField(t, canonical, "policy", `{}`)},
		{name: "snapshot", encoded: changedBundleField(t, canonical, "snapshot", `{}`)},
		{name: "evaluation", encoded: changedBundleField(t, canonical, "evaluation", `{}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, _, err := policy.RestoreSimulationBundle(test.encoded); !errors.Is(err, policy.ErrReproduction) {
				t.Fatalf("RestoreSimulationBundle() error = %v", err)
			}
		})
	}
}

func TestNewSimulationBundleRejectsZeroSimulation(t *testing.T) {
	t.Parallel()
	if _, _, err := policy.NewSimulationBundle(policy.Simulation{}); !errors.Is(err, policy.ErrReproduction) {
		t.Fatalf("NewSimulationBundle() error = %v", err)
	}
}

func FuzzRestoreSimulationBundle(f *testing.F) {
	simulation := runSimulation(f, policy.RequirementSatisfied, policy.DirectiveCompleteVerified)
	bundle, _, err := policy.NewSimulationBundle(simulation)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(bundle.Canonical())
	f.Add([]byte(`{}`))
	f.Add([]byte("not-json"))
	f.Fuzz(func(t *testing.T, encoded []byte) {
		restored, report, restoreErr := policy.RestoreSimulationBundle(encoded)
		if restoreErr != nil {
			return
		}
		if !report.Reproduced || restored.Digest() == "" ||
			!bytes.Equal(restored.Canonical(), encoded) {
			t.Fatal("accepted bundle did not reproduce byte-exact meaning")
		}
	})
}

func runSimulation(
	t testing.TB,
	state policy.RequirementState,
	directive policy.Directive,
) policy.Simulation {
	t.Helper()
	simulator, err := policy.NewSimulator(policycel.Compiler{})
	if err != nil {
		t.Fatal(err)
	}
	simulation, err := simulator.Run(t.Context(), simulationFixture(t, state, directive))
	if err != nil {
		t.Fatal(err)
	}
	return simulation
}

func changedBundleField(t testing.TB, canonical []byte, field string, value string) []byte {
	t.Helper()
	var object map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &object); err != nil {
		t.Fatal(err)
	}
	object[field] = json.RawMessage(value)
	encoded, err := json.Marshal(object)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
