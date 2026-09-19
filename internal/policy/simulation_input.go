package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// Portable simulation schema versions and limits are independent of the
// simulation-bundle envelope but share canonical fact and result meaning.
const (
	SimulationInputSchemaMajor = 1
	SimulationInputSchemaMinor = 0

	// MaximumSimulationInputBytes bounds one portable simulation input document.
	MaximumSimulationInputBytes = policyv1.MaximumDocumentBytes + MaximumSnapshotBytes + 16*1024
	// MaximumScenarioSuiteBytes bounds one portable scenario suite document.
	MaximumScenarioSuiteBytes = MaximumScenarios * MaximumSimulationInputBytes
)

type canonicalSimulationInput struct {
	SchemaMajor       *uint16         `json:"schema_major"`
	SchemaMinor       *uint16         `json:"schema_minor"`
	Policy            json.RawMessage `json:"policy"`
	TenantID          string          `json:"tenant_id"`
	VerificationID    string          `json:"verification_id"`
	AuthorityID       string          `json:"authority_id"`
	AcknowledgementID string          `json:"acknowledgement_id"`
	Region            string          `json:"region"`
	EvaluatedAt       time.Time       `json:"evaluated_at"`
	Facts             []canonicalFact `json:"facts"`
}

type canonicalScenarioSuite struct {
	SchemaMajor *uint16             `json:"schema_major"`
	SchemaMinor *uint16             `json:"schema_minor"`
	Scenarios   []canonicalScenario `json:"scenarios"`
}

type canonicalScenario struct {
	Name        string                       `json:"name"`
	Input       json.RawMessage              `json:"input"`
	Expectation canonicalScenarioExpectation `json:"expectation"`
}

type canonicalScenarioExpectation struct {
	Results   []canonicalRequirementResult `json:"results"`
	Assurance string                       `json:"assurance"`
}

// ParseSimulationInput parses and validates one closed portable simulation
// input document. The embedded policy is normalised to canonical meaning; the
// caller supplies only synthetic identities, region, time, and typed facts.
func ParseSimulationInput(encoded []byte) (SimulationInput, error) {
	if len(encoded) == 0 || len(encoded) > MaximumSimulationInputBytes || !utf8.Valid(encoded) {
		return SimulationInput{}, fmt.Errorf("%w: simulation input size", ErrInvalid)
	}
	if err := rejectPortableDuplicates(encoded); err != nil {
		return SimulationInput{}, err
	}
	var stored canonicalSimulationInput
	if err := decodeClosed(encoded, &stored); err != nil {
		return SimulationInput{}, fmt.Errorf("%w: simulation input document", ErrInvalid)
	}
	if stored.SchemaMajor == nil || stored.SchemaMinor == nil ||
		*stored.SchemaMajor != SimulationInputSchemaMajor || *stored.SchemaMinor != SimulationInputSchemaMinor {
		return SimulationInput{}, fmt.Errorf("%w: simulation input schema", ErrInvalid)
	}
	document, err := policyv1.Parse(stored.Policy)
	if err != nil {
		return SimulationInput{}, fmt.Errorf("%w: simulation policy", ErrInvalid)
	}
	canonical, err := policyv1.Canonical(document)
	if err != nil {
		return SimulationInput{}, fmt.Errorf("%w: simulation policy", ErrInvalid)
	}
	tenantID, err := id.ParseTenant(stored.TenantID)
	if err != nil {
		return SimulationInput{}, fmt.Errorf("%w: simulation tenant", ErrInvalid)
	}
	verificationID, err := id.ParseVerification(stored.VerificationID)
	if err != nil {
		return SimulationInput{}, fmt.Errorf("%w: simulation verification", ErrInvalid)
	}
	authorityID, err := id.ParseAuthority(stored.AuthorityID)
	if err != nil {
		return SimulationInput{}, fmt.Errorf("%w: simulation authority", ErrInvalid)
	}
	acknowledgementID, err := id.ParseAcknowledgement(stored.AcknowledgementID)
	if err != nil {
		return SimulationInput{}, fmt.Errorf("%w: simulation acknowledgement", ErrInvalid)
	}
	if !validToken(stored.Region, 64) || !validUTC(stored.EvaluatedAt) {
		return SimulationInput{}, fmt.Errorf("%w: simulation context", ErrInvalid)
	}
	if len(stored.Facts) == 0 || len(stored.Facts) > MaximumFacts {
		return SimulationInput{}, fmt.Errorf("%w: simulation facts", ErrInvalid)
	}
	facts := make([]Fact, len(stored.Facts))
	for index, fact := range stored.Facts {
		facts[index], err = factOf(fact)
		if err != nil {
			return SimulationInput{}, fmt.Errorf("%w: simulation fact", ErrInvalid)
		}
	}
	return SimulationInput{
		CanonicalPolicy: canonical, TenantID: tenantID, VerificationID: verificationID,
		AuthorityID: authorityID, AcknowledgementID: acknowledgementID,
		Region: stored.Region, EvaluatedAt: stored.EvaluatedAt, Facts: facts,
	}, nil
}

// ParseScenarioSuite parses and validates one closed portable scenario suite.
// Each scenario input is accepted through ParseSimulationInput and each
// expectation is expressed in canonical requirement-result meaning.
func ParseScenarioSuite(encoded []byte) ([]Scenario, error) {
	if len(encoded) == 0 || len(encoded) > MaximumScenarioSuiteBytes || !utf8.Valid(encoded) {
		return nil, fmt.Errorf("%w: scenario suite size", ErrInvalid)
	}
	if err := rejectPortableDuplicates(encoded); err != nil {
		return nil, err
	}
	var stored canonicalScenarioSuite
	if err := decodeClosed(encoded, &stored); err != nil {
		return nil, fmt.Errorf("%w: scenario suite document", ErrInvalid)
	}
	if stored.SchemaMajor == nil || stored.SchemaMinor == nil ||
		*stored.SchemaMajor != SimulationInputSchemaMajor || *stored.SchemaMinor != SimulationInputSchemaMinor {
		return nil, fmt.Errorf("%w: scenario suite schema", ErrInvalid)
	}
	if len(stored.Scenarios) == 0 || len(stored.Scenarios) > MaximumScenarios {
		return nil, fmt.Errorf("%w: scenario suite size", ErrInvalid)
	}
	scenarios := make([]Scenario, len(stored.Scenarios))
	for index, scenario := range stored.Scenarios {
		input, err := ParseSimulationInput(scenario.Input)
		if err != nil {
			return nil, fmt.Errorf("%w: scenario input", err)
		}
		results, err := resultsOf(scenario.Expectation.Results)
		if err != nil {
			return nil, fmt.Errorf("%w: scenario expectation", ErrInvalid)
		}
		scenarios[index] = Scenario{
			Name:  scenario.Name,
			Input: input,
			Expectation: ScenarioExpectation{
				Results: results, Assurance: scenario.Expectation.Assurance,
			},
		}
	}
	return scenarios, nil
}

func rejectPortableDuplicates(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	var depth int
	var visit func() error
	visit = func() error {
		depth++
		defer func() { depth-- }()
		if depth > 32 {
			return fmt.Errorf("%w: portable document depth", ErrInvalid)
		}
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("%w: portable document token", ErrInvalid)
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, keyErr := decoder.Token()
				if keyErr != nil {
					return fmt.Errorf("%w: portable document key", ErrInvalid)
				}
				key, keyOK := keyToken.(string)
				if !keyOK {
					return fmt.Errorf("%w: portable document key", ErrInvalid)
				}
				if _, exists := seen[key]; exists {
					return fmt.Errorf("%w: portable duplicate field", ErrInvalid)
				}
				seen[key] = struct{}{}
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return fmt.Errorf("%w: portable document delimiter", ErrInvalid)
		}
	}
	if err := visit(); err != nil {
		return err
	}
	if decoder.More() {
		return fmt.Errorf("%w: portable document trailing content", ErrInvalid)
	}
	return nil
}
