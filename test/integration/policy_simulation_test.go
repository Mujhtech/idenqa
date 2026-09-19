//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	bootstrapapi "github.com/Mujhtech/idenqa/internal/bootstrap/api"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/platform/health"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// TestPolicySimulationDiffAndRegressionPublicRoutes proves the read-only
// public computation routes against a runnable API, two real stored revisions,
// and the existing policies:read permission.
func TestPolicySimulationDiffAndRegressionPublicRoutes(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := pg.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	headgateMigrator, err := taskheadgate.OpenMigrator(ctx, database.url, "headgate", 5*time.Second, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := headgateMigrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := headgateMigrator.Close(ctx); err != nil {
		t.Fatal(err)
	}
	adminPool, err := pg.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	owner, _ := seedExecutionVerification(t, adminPool, generator, now)
	scope, err := tenant.NewScope(owner)
	if err != nil {
		t.Fatal(err)
	}
	runtimeRole := database.createRuntimeRole(t)
	database.grantHeadgateRuntime(t, runtimeRole)
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = runtimeRole
	runtimePool, err := pg.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimePool.Close()
	pepperMaterial := bytes.Repeat([]byte{0x63}, 32)
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: pepperMaterial})
	if err != nil {
		t.Fatal(err)
	}
	key, presented := newFullScopeIntegrationCredential(t, generator, owner, now, peppers)
	accessStore, err := accesspostgres.New(runtimePool)
	if err != nil {
		t.Fatal(err)
	}
	if err := accessStore.Create(ctx, scope, key); err != nil {
		t.Fatal(err)
	}
	keyringFile := t.TempDir() + "/keyring.json"
	keyring, err := localkms.Create(keyringFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := keyring.Close(); err != nil {
		t.Fatal(err)
	}
	port := reserveLoopbackPort(t)
	configuration := loadPublicFlowConfiguration(t, database.url, runtimeRole, port, t.TempDir(), keyringFile, pepperMaterial)
	process, err := bootstrapapi.NewProcess(
		ctx,
		configuration,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
		&health.State{},
		buildinfo.Info{Version: "integration", Commit: "integration", Date: "integration"},
	)
	if err != nil {
		t.Fatalf("compose runnable API: %v", err)
	}
	processContext, stopProcess := context.WithCancel(ctx)
	running := startPublicFlowProcess(
		func() error { stopProcess(); return nil },
		func() error { stopProcess(); return nil },
		func() error { return process.Run(processContext) },
	)
	t.Cleanup(func() { stopPublicFlowProcess(t, running) })
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	waitForPublicAPIReadiness(t, baseURL, running)

	client := &http.Client{Timeout: 10 * time.Second}
	credential := presented.Reveal()
	definition := publicSyntheticPolicyDefinition()
	var created policy.ManagementResult
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/policies", Bearer: credential,
		IdempotencyKey: "policy-computation-create", Body: map[string]any{"definition": definition},
		WantStatus: http.StatusOK, Result: &created,
	})
	if created.Policy.ID == "" || created.Revision == nil {
		t.Fatalf("created policy = %+v", created)
	}
	policyPath := "/v1/policies/" + created.Policy.ID
	changed := publicSyntheticPolicyDefinition()
	changed.VerifiedAssurance = "synthetic.second"
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + policyPath + "/revisions", Bearer: credential,
		IdempotencyKey: "policy-computation-revision-two",
		Body:           map[string]any{"definition": changed, "expected_revision": 1},
		WantStatus:     http.StatusOK,
	})

	var diff policy.RevisionDiff
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodGet, URL: baseURL + policyPath + "/diff?from_revision=1&to_revision=2",
		Bearer: credential, WantStatus: http.StatusOK, Result: &diff,
	})
	if diff.PolicyID != created.Policy.ID || diff.Identical || diff.Truncated || diff.Digest == "" {
		t.Fatalf("diff = %+v", diff)
	}
	found := false
	for _, change := range diff.Changes {
		if change.Path == "/verified_assurance" && change.Kind == policy.RevisionDiffChanged {
			found = true
		}
	}
	if !found || diff.ChangeCount != len(diff.Changes) {
		t.Fatalf("diff changes = %+v", diff.Changes)
	}
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodGet, URL: baseURL + policyPath + "/diff?from_revision=1&to_revision=1",
		Bearer: credential, WantStatus: http.StatusOK, Result: &diff,
	})
	if !diff.Identical || diff.ChangeCount != 0 || len(diff.Changes) != 0 {
		t.Fatalf("identical diff = %+v", diff)
	}
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodGet, URL: baseURL + policyPath + "/diff?from_revision=1&to_revision=3",
		Bearer: credential, WantStatus: http.StatusNotFound,
	})
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodGet, URL: baseURL + policyPath + "/diff?from_revision=1",
		Bearer: credential, WantStatus: http.StatusBadRequest,
	})

	var source policy.RevisionDocument
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodGet, URL: baseURL + policyPath + "/revisions/1",
		Bearer: credential, WantStatus: http.StatusOK, Result: &source,
	})
	simulationBody := publicPortableSimulation(t, source.Document, now)
	var report policy.SimulationReport
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/policy-simulations", Bearer: credential,
		Body: json.RawMessage(simulationBody), WantStatus: http.StatusOK, Result: &report,
	})
	if report.PolicyID != created.Policy.ID || report.PolicyRevision != 1 ||
		report.Directive != policy.DirectiveCompleteVerified || report.Outcome != policy.OutcomeVerified ||
		!report.AuthorisesCompletion || report.FactCount != 2 || report.BundleDigest == "" {
		t.Fatalf("simulation report = %+v", report)
	}
	var suite policy.ScenarioSuiteReport
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/policy-regressions", Bearer: credential,
		Body: json.RawMessage(publicPortableRegression(t, simulationBody, true)), WantStatus: http.StatusOK, Result: &suite,
	})
	if !suite.Passed || len(suite.Cases) != 1 || !suite.Cases[0].Matches {
		t.Fatalf("regression suite = %+v", suite)
	}
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/policy-regressions", Bearer: credential,
		Body: json.RawMessage(publicPortableRegression(t, simulationBody, false)), WantStatus: http.StatusOK, Result: &suite,
	})
	if suite.Passed || suite.Cases[0].Matches {
		t.Fatalf("mismatching regression suite = %+v", suite)
	}
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/policy-simulations", Bearer: credential,
		Body: map[string]any{"schema_major": 1}, WantStatus: http.StatusBadRequest,
	})
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/policy-simulations", Bearer: credential,
		Body: map[string]any{"oversized": strings.Repeat("a", 2<<20)}, WantStatus: http.StatusRequestEntityTooLarge,
	})

	readPattern, err := access.ParsePattern("policies:read")
	if err != nil {
		t.Fatal(err)
	}
	readKey, readPresented := newFullScopeIntegrationCredential(t, generator, owner, now, peppers)
	readRecord := access.KeyRecord{
		ID: readKey.ID(), TenantID: readKey.TenantID(), Label: readKey.Label(),
		Digest: readKey.Digest(), PepperVersion: readKey.PepperVersion(), Version: 1,
		CreatedAt: readKey.CreatedAt(), UpdatedAt: readKey.UpdatedAt(),
	}
	readRecord.Grant, err = access.TenantRegistry().Resolve(readPattern)
	if err != nil {
		t.Fatal(err)
	}
	readKey, err = access.RestoreKey(readRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := accessStore.Create(ctx, scope, readKey); err != nil {
		t.Fatal(err)
	}
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/policy-simulations", Bearer: readPresented.Reveal(),
		Body: json.RawMessage(simulationBody), WantStatus: http.StatusOK, Result: &report,
	})
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodGet, URL: baseURL + policyPath + "/diff?from_revision=1&to_revision=2",
		Bearer: readPresented.Reveal(), WantStatus: http.StatusOK, Result: &diff,
	})
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/policies", Bearer: readPresented.Reveal(),
		IdempotencyKey: "policy-computation-denied-create", Body: map[string]any{"definition": definition},
		WantStatus: http.StatusForbidden,
	})
}

func publicPortableSimulation(t *testing.T, policyDocument json.RawMessage, evaluatedAt time.Time) []byte {
	t.Helper()
	authorityID := "aut_01K3P4NQF00000000000000003"
	acknowledgementID := "ack_01K3P4NQF00000000000000004"
	observedAt := evaluatedAt.Format(time.RFC3339)
	encoded, err := json.Marshal(map[string]any{
		"schema_major": 1, "schema_minor": 0,
		"policy":             json.RawMessage(policyDocument),
		"tenant_id":          "ten_01K3P4NQF00000000000000001",
		"verification_id":    "ver_01K3P4NQF00000000000000002",
		"authority_id":       authorityID,
		"acknowledgement_id": acknowledgementID,
		"region":             "tenant_home",
		"evaluated_at":       observedAt,
		"facts": []map[string]any{
			{
				"key": "synthetic.document", "state": "satisfied",
				"source":      map[string]any{"kind": "processing_authority", "authority_id": authorityID},
				"observed_at": observedAt, "reason_codes": []string{"synthetic_document"},
			},
			{
				"key": "synthetic.liveness", "state": "satisfied",
				"source":      map[string]any{"kind": "subject_response", "acknowledgement_id": acknowledgementID},
				"observed_at": observedAt, "reason_codes": []string{"synthetic_liveness"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func publicPortableRegression(t *testing.T, simulationInput []byte, match bool) []byte {
	t.Helper()
	state := policy.RequirementSatisfied
	directive := policy.DirectiveCompleteVerified
	if !match {
		state = policy.RequirementUnavailable
		directive = policy.DirectiveRequestInput
	}
	encoded, err := json.Marshal(map[string]any{
		"schema_major": 1, "schema_minor": 0,
		"scenarios": []map[string]any{{
			"name":  "synthetic_verified",
			"input": json.RawMessage(simulationInput),
			"expectation": map[string]any{
				"results": []map[string]any{{
					"name": "synthetic_success", "state": string(state),
					"contributing_facts": []string{"synthetic.document", "synthetic.liveness"},
					"candidate":          string(directive), "priority": 1,
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
