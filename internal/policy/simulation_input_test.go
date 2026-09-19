package policy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
)

type portableInputDocument struct {
	SchemaMajor       uint16            `json:"schema_major"`
	SchemaMinor       uint16            `json:"schema_minor"`
	Policy            json.RawMessage   `json:"policy"`
	TenantID          string            `json:"tenant_id"`
	VerificationID    string            `json:"verification_id"`
	AuthorityID       string            `json:"authority_id"`
	AcknowledgementID string            `json:"acknowledgement_id"`
	Region            string            `json:"region"`
	EvaluatedAt       time.Time         `json:"evaluated_at"`
	Facts             []json.RawMessage `json:"facts"`
}

func TestParseSimulationInputMatchesDirectSimulation(t *testing.T) {
	t.Parallel()
	input := simulationFixture(t, policy.RequirementSatisfied, policy.DirectiveCompleteVerified)
	parsed, err := policy.ParseSimulationInput(portableInputJSON(t, input))
	if err != nil {
		t.Fatal(err)
	}
	simulator, err := policy.NewSimulator(policycel.Compiler{})
	if err != nil {
		t.Fatal(err)
	}
	direct, err := simulator.Run(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := simulator.Run(t.Context(), parsed)
	if err != nil {
		t.Fatal(err)
	}
	if direct.Snapshot().Digest() != restored.Snapshot().Digest() ||
		direct.Evaluation().Digest() != restored.Evaluation().Digest() ||
		!bytes.Equal(direct.CanonicalPolicy(), restored.CanonicalPolicy()) {
		t.Fatal("portable input changed simulation meaning")
	}
}

func TestParseSimulationInputNormalisesNonCanonicalPolicy(t *testing.T) {
	t.Parallel()
	input := simulationFixture(t, policy.RequirementSatisfied, policy.DirectiveCompleteVerified)
	var encoded map[string]any
	if err := json.Unmarshal(input.CanonicalPolicy, &encoded); err != nil {
		t.Fatal(err)
	}
	rules := encoded["rules"].([]any)
	result := rules[0].(map[string]any)["result"].(map[string]any)
	result["contributing_facts"] = []any{"selfie.liveness", "document.authenticity"}
	nonCanonical, err := json.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := policy.ParseSimulationInput(replacePortablePolicy(t, portableInputJSON(t, input), nonCanonical))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(parsed.CanonicalPolicy, []byte(`"document.authenticity","selfie.liveness"`)) {
		t.Fatalf("policy was not normalised: %s", parsed.CanonicalPolicy)
	}
}

func TestParseSimulationInputRejectsAmbiguousAndUnboundedDocuments(t *testing.T) {
	t.Parallel()
	base := portableInputJSON(t, simulationFixture(t, policy.RequirementSatisfied, policy.DirectiveCompleteVerified))
	tests := []struct {
		name    string
		encoded []byte
	}{
		{name: "empty"},
		{name: "malformed", encoded: []byte(`{"schema_major":`)},
		{name: "trailing", encoded: append(bytes.Clone(base), []byte(`{}`)...)},
		{name: "duplicate field", encoded: bytes.Replace(base, []byte(`{"schema_major":1,`), []byte(`{"schema_major":1,"schema_major":1,`), 1)},
		{name: "missing schema minor", encoded: portableMutation(t, base, func(document map[string]any) { delete(document, "schema_minor") })},
		{name: "null schema major", encoded: portableMutation(t, base, func(document map[string]any) { document["schema_major"] = nil })},
		{name: "unknown field", encoded: portableMutation(t, base, func(document map[string]any) { document["unknown"] = true })},
		{name: "unsupported schema", encoded: portableMutation(t, base, func(document map[string]any) { document["schema_major"] = 2 })},
		{name: "invalid tenant", encoded: portableMutation(t, base, func(document map[string]any) { document["tenant_id"] = "not-a-tenant" })},
		{name: "invalid authority", encoded: portableMutation(t, base, func(document map[string]any) { document["authority_id"] = "" })},
		{name: "non UTC evaluation time", encoded: portableMutation(t, base, func(document map[string]any) { document["evaluated_at"] = "2026-08-31T13:00:00+01:00" })},
		{name: "empty facts", encoded: portableMutation(t, base, func(document map[string]any) { document["facts"] = []any{} })},
		{name: "missing policy", encoded: portableMutation(t, base, func(document map[string]any) { delete(document, "policy") })},
		{name: "invalid policy", encoded: portableMutation(t, base, func(document map[string]any) { document["policy"] = map[string]any{"rules": []any{}} })},
		{name: "invalid fact source", encoded: portableMutation(t, base, func(document map[string]any) {
			document["facts"].([]any)[0].(map[string]any)["source"] = map[string]any{"kind": "processing_authority"}
		})},
		{name: "oversized", encoded: bytes.Repeat([]byte{'x'}, policy.MaximumSimulationInputBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := policy.ParseSimulationInput(test.encoded); !errors.Is(err, policy.ErrInvalid) {
				t.Fatalf("ParseSimulationInput() error = %v", err)
			}
		})
	}
}

func TestParseScenarioSuiteRunsAndRejectsInvalidBoundaries(t *testing.T) {
	t.Parallel()
	input := simulationFixture(t, policy.RequirementSatisfied, policy.DirectiveCompleteVerified)
	expectation := map[string]any{
		"results": []map[string]any{{
			"name": "scenario", "state": string(policy.RequirementSatisfied),
			"contributing_facts": []string{"document.authenticity", "selfie.liveness"},
			"candidate":          string(policy.DirectiveCompleteVerified), "priority": 1,
			"reason_codes": []string{"synthetic_scenario"},
		}},
		"assurance": "global_individual_substantial.1",
	}
	scenario := map[string]any{"name": "verified", "input": json.RawMessage(portableInputJSON(t, input)), "expectation": expectation}
	suiteJSON := portableSuiteJSON(t, []map[string]any{scenario})
	scenarios, err := policy.ParseScenarioSuite(suiteJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 1 || scenarios[0].Name != "verified" {
		t.Fatalf("scenarios = %+v", scenarios)
	}
	simulator, err := policy.NewSimulator(policycel.Compiler{})
	if err != nil {
		t.Fatal(err)
	}
	suite, err := policy.NewScenarioSuite(simulator)
	if err != nil {
		t.Fatal(err)
	}
	_, report, err := suite.Run(t.Context(), scenarios)
	if err != nil || !report.Passed {
		t.Fatalf("suite report = %+v, %v", report, err)
	}

	tooMany := make([]map[string]any, 0, policy.MaximumScenarios+1)
	for index := range policy.MaximumScenarios + 1 {
		copied := map[string]any{"name": fmt.Sprintf("case_%03d", index), "input": json.RawMessage(portableInputJSON(t, input)), "expectation": expectation}
		tooMany = append(tooMany, copied)
	}
	tests := []struct {
		name    string
		encoded []byte
		is      error
	}{
		{name: "empty suite", encoded: []byte(`{}`), is: policy.ErrInvalid},
		{name: "no scenarios", encoded: portableSuiteJSON(t, []map[string]any{}), is: policy.ErrInvalid},
		{name: "too many scenarios", encoded: portableSuiteJSON(t, tooMany), is: policy.ErrInvalid},
		{name: "duplicate names", encoded: portableSuiteJSON(t, []map[string]any{scenario, scenario})},
		{name: "malformed scenario input", encoded: portableSuiteJSON(t, []map[string]any{
			{"name": "broken", "input": map[string]any{"schema_major": 1}, "expectation": expectation},
		}), is: policy.ErrInvalid},
		{name: "oversized scenario input", encoded: portableSuiteJSON(t, []map[string]any{
			{"name": "huge", "input": strings.Repeat("a", policy.MaximumSimulationInputBytes), "expectation": expectation},
		}), is: policy.ErrInvalid},
		{name: "unknown expectation field", encoded: bytes.Replace(suiteJSON, []byte(`"assurance"`), []byte(`"unknown":true,"assurance"`), 1), is: policy.ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			parsed, err := policy.ParseScenarioSuite(test.encoded)
			if test.is != nil && !errors.Is(err, test.is) {
				t.Fatalf("ParseScenarioSuite() error = %v", err)
			}
			if test.is == nil && err != nil {
				t.Fatal(err)
			}
			if test.is == nil {
				if _, _, err := suite.Run(t.Context(), parsed); !errors.Is(err, policy.ErrConflict) {
					t.Fatalf("duplicate scenario Run() error = %v", err)
				}
			}
		})
	}
}

func portableInputJSON(t testing.TB, input policy.SimulationInput) []byte {
	t.Helper()
	facts := make([]json.RawMessage, len(input.Facts))
	for index, fact := range input.Facts {
		facts[index] = portableFactJSON(t, fact)
	}
	encoded, err := json.Marshal(portableInputDocument{
		SchemaMajor: policy.SimulationInputSchemaMajor, SchemaMinor: policy.SimulationInputSchemaMinor,
		Policy: input.CanonicalPolicy, TenantID: input.TenantID.String(),
		VerificationID: input.VerificationID.String(), AuthorityID: input.AuthorityID.String(),
		AcknowledgementID: input.AcknowledgementID.String(), Region: input.Region,
		EvaluatedAt: input.EvaluatedAt, Facts: facts,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func portableFactJSON(t testing.TB, fact policy.Fact) json.RawMessage {
	t.Helper()
	source := map[string]any{"kind": string(fact.Source.Kind)}
	switch {
	case fact.Source.Check != nil:
		observations := make([]string, len(fact.Source.Check.ObservationIDs))
		for index, observationID := range fact.Source.Check.ObservationIDs {
			observations[index] = observationID.String()
		}
		source["check_id"] = fact.Source.Check.CheckID.String()
		source["check_version"] = fact.Source.Check.CheckVersion
		source["attempt_id"] = fact.Source.Check.AttemptID.String()
		source["observation_ids"] = observations
		source["contract_digest"] = fact.Source.Check.ContractDigest
		source["implementation_digest"] = fact.Source.Check.ImplementationDigest
	case fact.Source.Authority != nil:
		source["authority_id"] = fact.Source.Authority.AuthorityID.String()
	case fact.Source.SubjectResponse != nil:
		source["acknowledgement_id"] = fact.Source.SubjectResponse.AcknowledgementID.String()
	case fact.Source.ReviewFinding != nil:
		source["review_finding"] = fact.Source.ReviewFinding.Reference
	case fact.Source.Identity != nil:
		source["identity_receipt"] = fact.Source.Identity.ReceiptDigest
	case fact.Source.Fraud != nil:
		source["fraud_receipt"] = fact.Source.Fraud.ReceiptDigest
	}
	value := map[string]any{
		"key": string(fact.Key), "state": string(fact.State), "source": source,
		"observed_at": fact.ObservedAt, "reason_codes": fact.ReasonCodes,
	}
	if fact.ExpiresAt != nil {
		value["expires_at"] = *fact.ExpiresAt
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func portableSuiteJSON(t testing.TB, scenarios []map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(map[string]any{
		"schema_major": policy.SimulationInputSchemaMajor,
		"schema_minor": policy.SimulationInputSchemaMinor,
		"scenarios":    scenarios,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func portableMutation(t testing.TB, encoded []byte, change func(map[string]any)) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	change(document)
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return mutated
}

func replacePortablePolicy(t testing.TB, encoded, replacement []byte) []byte {
	t.Helper()
	return portableMutation(t, encoded, func(document map[string]any) {
		document["policy"] = json.RawMessage(replacement)
	})
}
