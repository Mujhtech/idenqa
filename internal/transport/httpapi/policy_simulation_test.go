package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
	"github.com/go-chi/chi/v5"
)

const (
	simulationPolicyID   = "pol_01K3P4NQF00000000000000001"
	simulationTenantID   = "ten_01K3P4NQF00000000000000001"
	simulationVerifID    = "ver_01K3P4NQF00000000000000002"
	simulationAuthority  = "aut_01K3P4NQF00000000000000003"
	simulationAckID      = "ack_01K3P4NQF00000000000000004"
	simulationEvaluated  = "2026-08-31T12:00:00Z"
	simulationDocumentFC = `facts["synthetic.document"] == "satisfied" && facts["synthetic.liveness"] == "satisfied"`
)

type policyDiffStub struct {
	diff     policy.RevisionDiff
	err      error
	policyID id.Policy
	from     uint32
	to       uint32
	tenantID id.Tenant
	calls    int
}

func (stub *policyDiffStub) Diff(
	_ context.Context,
	actor access.Context,
	identifier id.Policy,
	from, to uint32,
) (policy.RevisionDiff, error) {
	stub.calls++
	stub.policyID, stub.from, stub.to = identifier, from, to
	stub.tenantID = actor.TenantScope().ID()
	if stub.err != nil {
		return policy.RevisionDiff{}, stub.err
	}
	return stub.diff, nil
}

func TestPolicySimulationRoutesComputeAndDiff(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, policyReadPattern(t))
	differ := &policyDiffStub{diff: policy.RevisionDiff{
		SchemaMajor: 1, SchemaMinor: 0, PolicyID: simulationPolicyID,
		From: policy.RevisionDiffRevision{Revision: 1, Digest: "a"}, To: policy.RevisionDiffRevision{Revision: 2, Digest: "b"},
		Identical: false, ChangeCount: 1, Changes: []policy.RevisionDiffChange{{Path: "/verified_assurance", Kind: policy.RevisionDiffChanged}},
		Digest: "c",
	}}
	router := newPolicySimulationRouter(t, fixture, differ)

	simulationBody := policySimulationBody(t)
	response := performPolicySimulationRequest(t, router, fixture, http.MethodPost, "/v1/policy-simulations", simulationBody)
	if response.Code != http.StatusOK {
		t.Fatalf("simulation status = %d body=%s", response.Code, response.Body)
	}
	var report policy.SimulationReport
	if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.PolicyID != simulationPolicyID || report.PolicyRevision != 3 ||
		report.Directive != policy.DirectiveCompleteVerified || report.Outcome != policy.OutcomeVerified ||
		!report.AuthorisesCompletion || report.EvaluatorDigest == "" || report.FactCount != 2 {
		t.Fatalf("report = %+v", report)
	}

	regressionBody := policyRegressionBody(t, false)
	response = performPolicySimulationRequest(t, router, fixture, http.MethodPost, "/v1/policy-regressions", regressionBody)
	if response.Code != http.StatusOK {
		t.Fatalf("regression status = %d body=%s", response.Code, response.Body)
	}
	var suite policy.ScenarioSuiteReport
	if err := json.Unmarshal(response.Body.Bytes(), &suite); err != nil {
		t.Fatal(err)
	}
	if !suite.Passed || len(suite.Cases) != 1 || !suite.Cases[0].Matches || suite.Cases[0].Name != "verified" {
		t.Fatalf("suite = %+v", suite)
	}
	response = performPolicySimulationRequest(t, router, fixture, http.MethodPost, "/v1/policy-regressions", policyRegressionBody(t, true))
	if response.Code != http.StatusOK {
		t.Fatalf("mismatch regression status = %d body=%s", response.Code, response.Body)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &suite); err != nil {
		t.Fatal(err)
	}
	if suite.Passed || suite.Cases[0].Matches {
		t.Fatalf("mismatching suite = %+v", suite)
	}

	response = performPolicySimulationRequest(t, router, fixture, http.MethodGet,
		"/v1/policies/"+simulationPolicyID+"/diff?from_revision=1&to_revision=2", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("diff status = %d body=%s", response.Code, response.Body)
	}
	if differ.calls != 1 || differ.policyID.String() != simulationPolicyID || differ.from != 1 || differ.to != 2 ||
		differ.tenantID.String() == "" {
		t.Fatalf("differ call = %+v", differ)
	}
	var diff policy.RevisionDiff
	if err := json.Unmarshal(response.Body.Bytes(), &diff); err != nil || diff.ChangeCount != 1 {
		t.Fatalf("diff = %+v, %v", diff, err)
	}
}

func TestPolicySimulationRoutesRequireExistingReadPermission(t *testing.T) {
	t.Parallel()
	pattern, err := access.ParsePattern("tenant:read")
	if err != nil {
		t.Fatal(err)
	}
	fixture := newHTTPAccessFixture(t, nil, pattern)
	router := newPolicySimulationRouter(t, fixture, &policyDiffStub{})
	for _, request := range []struct {
		method, path string
		body         []byte
	}{
		{method: http.MethodPost, path: "/v1/policy-simulations", body: policySimulationBody(t)},
		{method: http.MethodPost, path: "/v1/policy-regressions", body: policyRegressionBody(t, false)},
		{method: http.MethodGet, path: "/v1/policies/" + simulationPolicyID + "/diff?from_revision=1&to_revision=2"},
	} {
		response := performPolicySimulationRequest(t, router, fixture, request.method, request.path, request.body)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d body=%s", request.method, request.path, response.Code, response.Body)
		}
	}
}

func TestPolicySimulationRoutesRejectMalformedAndOversizedInput(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, policyReadPattern(t))
	router := newPolicySimulationRouter(t, fixture, &policyDiffStub{})
	for _, test := range []struct {
		name   string
		path   string
		body   []byte
		length int64
		status int
	}{
		{name: "malformed simulation", path: "/v1/policy-simulations", body: []byte(`{"schema_major":`), status: http.StatusBadRequest},
		{name: "unknown simulation field", path: "/v1/policy-simulations", body: bytes.Replace(policySimulationBody(t), []byte(`"region"`), []byte(`"unknown":true,"region"`), 1), status: http.StatusBadRequest},
		{name: "oversized simulation content length", path: "/v1/policy-simulations", body: []byte(`{}`), length: policy.MaximumSimulationInputBytes + 1, status: http.StatusRequestEntityTooLarge},
		{name: "malformed regression", path: "/v1/policy-regressions", body: []byte(`{"scenarios":`), status: http.StatusBadRequest},
		{name: "oversized regression content length", path: "/v1/policy-regressions", body: []byte(`{}`), length: policy.MaximumScenarioSuiteBytes + 1, status: http.StatusRequestEntityTooLarge},
		{name: "missing diff parameters", path: "/v1/policies/" + simulationPolicyID + "/diff", status: http.StatusBadRequest},
		{name: "zero diff revision", path: "/v1/policies/" + simulationPolicyID + "/diff?from_revision=0&to_revision=2", status: http.StatusBadRequest},
		{name: "bad policy identifier", path: "/v1/policies/not-a-policy/diff?from_revision=1&to_revision=2", status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			method := http.MethodPost
			if test.path[len(test.path)-4:] == "diff" || bytes.Contains([]byte(test.path), []byte("/diff?")) {
				method = http.MethodGet
			}
			response := performPolicySimulationRequestWithLength(t, router, fixture, method, test.path, test.body, test.length)
			if response.Code != test.status {
				t.Fatalf("status = %d, want %d, body=%s", response.Code, test.status, response.Body)
			}
		})
	}
}

func TestPolicySimulationRoutesMapMissingRevision(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, policyReadPattern(t))
	differ := &policyDiffStub{err: policy.ErrRevisionNotFound}
	router := newPolicySimulationRouter(t, fixture, differ)
	response := performPolicySimulationRequest(t, router, fixture, http.MethodGet,
		"/v1/policies/"+simulationPolicyID+"/diff?from_revision=1&to_revision=2", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d body=%s", response.Code, response.Body)
	}
}

func newPolicySimulationRouter(t *testing.T, fixture *httpAccessFixture, differ policyRevisionDiffer) chi.Router {
	t.Helper()
	simulator, err := policy.NewSimulator(policycel.Compiler{})
	if err != nil {
		t.Fatal(err)
	}
	suite, err := policy.NewScenarioSuite(simulator)
	if err != nil {
		t.Fatal(err)
	}
	routes, err := NewPolicySimulationRoutes(fixture.middleware, differ, simulator, suite, fixture.logger)
	if err != nil {
		t.Fatal(err)
	}
	return versionedRouter(t, routes)
}

func policyReadPattern(t testing.TB) access.Pattern {
	t.Helper()
	pattern, err := access.ParsePattern("policies:read")
	if err != nil {
		t.Fatal(err)
	}
	return pattern
}

func performPolicySimulationRequest(
	t *testing.T,
	router chi.Router,
	fixture *httpAccessFixture,
	method, path string,
	body []byte,
) *httptest.ResponseRecorder {
	t.Helper()
	return performPolicySimulationRequestWithLength(t, router, fixture, method, path, body, 0)
}

func performPolicySimulationRequestWithLength(
	t *testing.T,
	router chi.Router,
	fixture *httpAccessFixture,
	method, path string,
	body []byte,
	length int64,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, path, bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	request.Header.Set("Content-Type", "application/json")
	if length > 0 {
		request.ContentLength = length
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

type portableSimulationBody struct {
	SchemaMajor       uint16           `json:"schema_major"`
	SchemaMinor       uint16           `json:"schema_minor"`
	Policy            json.RawMessage  `json:"policy"`
	TenantID          string           `json:"tenant_id"`
	VerificationID    string           `json:"verification_id"`
	AuthorityID       string           `json:"authority_id"`
	AcknowledgementID string           `json:"acknowledgement_id"`
	Region            string           `json:"region"`
	EvaluatedAt       time.Time        `json:"evaluated_at"`
	Facts             []map[string]any `json:"facts"`
}

func policySimulationBody(t testing.TB) []byte {
	t.Helper()
	document, err := policyv1.Canonical(policyv1.Document{
		SchemaMajor: 1, SchemaMinor: 0, PolicyID: simulationPolicyID, Revision: 3,
		VerifiedAssurance: "synthetic.fixture",
		Rules: []policyv1.Rule{{
			Name: "synthetic_success", When: simulationDocumentFC,
			Result: policyv1.Result{
				State: policyv1.RequirementSatisfied, Directive: policyv1.DirectiveCompleteVerified,
				Priority: 1, ContributingFacts: []string{"synthetic.document", "synthetic.liveness"},
				ReasonCodes: []string{},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluatedAt, err := time.Parse(time.RFC3339, simulationEvaluated)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(portableSimulationBody{
		SchemaMajor: 1, SchemaMinor: 0, Policy: document,
		TenantID: simulationTenantID, VerificationID: simulationVerifID,
		AuthorityID: simulationAuthority, AcknowledgementID: simulationAckID,
		Region: "tenant_home", EvaluatedAt: evaluatedAt,
		Facts: []map[string]any{
			{
				"key": "synthetic.document", "state": "satisfied",
				"source":      map[string]any{"kind": "processing_authority", "authority_id": simulationAuthority},
				"observed_at": simulationEvaluated, "reason_codes": []string{"synthetic_document"},
			},
			{
				"key": "synthetic.liveness", "state": "satisfied",
				"source":      map[string]any{"kind": "subject_response", "acknowledgement_id": simulationAckID},
				"observed_at": simulationEvaluated, "reason_codes": []string{"synthetic_liveness"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func policyRegressionBody(t testing.TB, mismatched bool) []byte {
	t.Helper()
	expectedState := policy.RequirementSatisfied
	expectedDirective := policy.DirectiveCompleteVerified
	if mismatched {
		expectedState = policy.RequirementUnavailable
		expectedDirective = policy.DirectiveRequestInput
	}
	encoded, err := json.Marshal(map[string]any{
		"schema_major": 1, "schema_minor": 0,
		"scenarios": []map[string]any{{
			"name":  "verified",
			"input": json.RawMessage(policySimulationBody(t)),
			"expectation": map[string]any{
				"results": []map[string]any{{
					"name": "synthetic_success", "state": string(expectedState),
					"contributing_facts": []string{"synthetic.document", "synthetic.liveness"},
					"candidate":          string(expectedDirective), "priority": 1,
					"reason_codes": []string{},
				}},
				"assurance": "synthetic.fixture",
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
