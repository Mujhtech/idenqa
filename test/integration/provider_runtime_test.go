//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	"github.com/Mujhtech/idenqa/adapters/providers/smileid"
	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/bootstrap/adapterrunner"
	bootstrapapi "github.com/Mujhtech/idenqa/internal/bootstrap/api"
	bootstrapworker "github.com/Mujhtech/idenqa/internal/bootstrap/worker"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/health"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	objectlocal "github.com/Mujhtech/idenqa/internal/platform/objectstore/local"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

type providerPublicJourney struct {
	runtimeFile  string
	smile        bool
	model        bool
	matching     bool
	composed     bool
	providerFile string
}

func TestDojahRuntimePublicCaptureThroughDecisionAndWebhook(t *testing.T) {
	runProviderPublicJourney(t, false, false)
}
func TestSmileRuntimePublicCaptureThroughRestartDecisionAndWebhook(t *testing.T) {
	runProviderPublicJourney(t, true, false)
}
func runProviderPublicJourney(t *testing.T, smile, pad bool, matching ...bool) {
	directory := t.TempDir()
	objects, err := objectlocal.Open(objectlocal.Config{Directory: directory, MaxObjectBytes: 2 * evidence.MaximumUploadMaximumBytes})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := objects.Close(); err != nil {
			t.Error(err)
		}
	})
	journey := &providerPublicJourney{smile: smile, model: pad, matching: len(matching) > 0 && matching[0], composed: len(matching) > 1 && matching[1]}
	backend := publicFlowBackend{providerJourney: journey,
		start: func(t *testing.T, databaseURL, runtimeRole string, port int, keyringFile string, pepper []byte) publicFlowProcess {
			t.Helper()
			configuration := loadPublicFlowConfiguration(t, databaseURL, runtimeRole, port, directory, keyringFile, pepper)
			if journey.model {
				configuration.ModelRuntimeFile = journey.runtimeFile
				configuration.ProviderRuntimeFile = journey.providerFile
			} else {
				configuration.ProviderRuntimeFile = journey.runtimeFile
			}
			process, err := bootstrapapi.NewProcess(t.Context(), configuration, slog.New(slog.NewJSONHandler(io.Discard, nil)), &health.State{}, buildinfo.Info{Version: "integration"})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			stop := func() error { cancel(); return nil }
			return startPublicFlowProcess(stop, stop, func() error { return process.Run(ctx) })
		}, readCiphertext: func(t *testing.T, object objectstore.Object) []byte {
			t.Helper()
			reader, err := objects.Open(t.Context(), object)
			if err != nil {
				t.Fatal(err)
			}
			return readAndCloseCiphertext(t, reader)
		}}
	runPublicEvidenceUploadFlow(t, backend)
}

func providerDocumentProfile(t *testing.T, registry evidence.Registry) verification.Profile {
	t.Helper()
	document, err := verification.NewProfile(registry, []verification.Requirement{{Key: "document", Purpose: evidence.PurposeIdentityVerification, EvidenceType: evidence.EvidenceDocumentImage, Artefacts: []evidence.Name{evidence.ArtefactDocumentFront}, Acquisition: verification.Acquisition{Strategy: verification.StrategyAnyOf, Methods: []evidence.Name{evidence.MethodLiveCamera}}}})
	if err != nil {
		t.Fatal(err)
	}
	return document
}
func createProviderFixturePolicy(t *testing.T, client *http.Client, base, credential string, smile, pad bool, matching bool, composed bool) id.Policy {
	t.Helper()
	definition := policy.Definition{SchemaMajor: 1, VerifiedAssurance: "synthetic.fixture", Rules: []policyv1.Rule{{Name: "fixture_document", When: `facts["idenqa.signal.document_quality"] == "satisfied"`, Result: policyv1.Result{State: policyv1.RequirementSatisfied, Directive: policyv1.DirectiveCompleteVerified, Priority: 1, ContributingFacts: []string{"idenqa.signal.document_quality"}, ReasonCodes: []string{}}}}}
	if smile {
		definition.Rules[0].When = `facts["idenqa.signal.provider_job"] == "satisfied" && facts["idenqa.signal.face_match_1to1"] == "satisfied" && facts["idenqa.signal.document_authenticity"] == "satisfied"`
		definition.Rules[0].Result.ContributingFacts = []string{"idenqa.signal.provider_job", "idenqa.signal.face_match_1to1", "idenqa.signal.document_authenticity"}
	}
	if pad {
		definition.Rules[0].When = `facts["idenqa.signal.passive_pad"] == "inconclusive"`
		definition.Rules[0].Result.ContributingFacts = []string{"idenqa.signal.passive_pad"}
	}
	if matching {
		definition.Rules[0].When = `facts["idenqa.signal.face_match_1to1"] == "inconclusive"`
		definition.Rules[0].Result.ContributingFacts = []string{"idenqa.signal.face_match_1to1"}
	}

	if composed {
		definition.Rules[0].When = `facts["idenqa.signal.document_quality"] == "satisfied" && facts["idenqa.signal.passive_pad"] == "inconclusive" && facts["idenqa.signal.face_match_1to1"] == "inconclusive"`
		definition.Rules[0].Result.ContributingFacts = []string{"idenqa.signal.document_quality", "idenqa.signal.passive_pad", "idenqa.signal.face_match_1to1"}
	}
	var result policy.ManagementResult
	performPublicJSONRequest(t, client, publicJSONRequest{Method: "POST", URL: base + "/v1/policies", Bearer: credential, IdempotencyKey: "provider-policy-create", Body: map[string]any{"definition": definition}, WantStatus: 200, Result: &result})
	identifier, err := id.ParsePolicy(result.Policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	performPublicJSONRequest(t, client, publicJSONRequest{Method: "POST", URL: base + "/v1/policies/" + identifier.String() + "/activate", Bearer: credential, IdempotencyKey: "provider-policy-activate", Body: map[string]any{"revision": 1, "expected_version": 0, "reason": "synthetic_fixture"}, WantStatus: 200, Result: &result})
	return identifier
}

func (journey *providerPublicJourney) run(t *testing.T, admin, runtime *pg.Pool, scope tenant.Scope, verificationValue, policyID, profileDigest, base, credential, keyring string, restartAPI func(), plaintext []byte) {
	t.Helper()
	if journey.model {
		journey.runModel(t, admin, runtime, scope, verificationValue, policyID, profileDigest, base, credential, keyring, restartAPI)
		return
	}
	directory := t.TempDir()
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	var calls atomic.Int32
	fixture := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var body map[string]string
		err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body)
		image, decodeErr := base64.StdEncoding.DecodeString(body["imagefrontside"])
		if err != nil || decodeErr != nil || !bytes.Equal(image, plaintext) || body["input_type"] != "base64" || len(body) != 2 || r.URL.Path != "/api/v1/document/analysis" || r.Method != "POST" || r.Header.Get("AppId") != "fixture-app" || r.Header.Get("Authorization") != "fixture-key" {
			t.Error("provider request did not match documented wire contract and accepted evidence")
			w.WriteHeader(400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"entity":{"status":{"overall_status":1},"text_data":[{"value":"discard-sensitive-provider-output"}]}}`)
	}))
	var smileFixture *smileJourneyFixture
	if journey.smile {
		fixture.Close()
		smileFixture = newSmileJourneyFixture(t, plaintext)
		fixture = smileFixture.server
	}
	defer fixture.Close()
	ca := write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fixture.TLS.Certificates[0].Certificate[0]}))
	rawKey, err := x509.MarshalPKCS8PrivateKey(fixture.TLS.Certificates[0].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	key := write("tls-key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rawKey}))
	target, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		_ = r.Body.Close()
		r.Body = io.NopCloser(bytes.NewReader(raw))
		var altered map[string]string
		if err := json.Unmarshal(raw, &altered); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		altered["grant_id"] = "grt_01K4AR9V8FQ2G7ZXCPNM5T6JWH"
		body, _ := json.Marshal(altered)
		denied, err := http.NewRequestWithContext(r.Context(), "POST", base+"/internal/v1/provider-evidence", bytes.NewReader(body))
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		denied.Header.Set("Authorization", r.Header.Get("Authorization"))
		response, err := http.DefaultClient.Do(denied)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		_ = response.Body.Close()
		if response.StatusCode != 403 {
			t.Error("swapped grant was not denied")
		}
		proxy.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	gatewayCA := write("gateway-ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: gateway.TLS.Certificates[0].Certificate[0]}))
	runnerKey := write("runner.key", []byte("idq_wrk_v1_"+base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x61}, 32))))
	gatewaySecret := "idq_wrk_v1_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x62}, 32))
	gatewayKey := write("gateway.key", []byte(gatewaySecret))
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	providerID, err := ids.NewProvider()
	if err != nil {
		t.Fatal(err)
	}
	description := dojah.Description()
	if journey.smile {
		description = smileid.Description()
	}
	reference := providerv1.ConfigurationReference{ProviderID: providerID.String(), SchemaDigest: description.Configuration.Digest, SecretReference: "secret://provider/dojah/fixture", CredentialVersion: "v1"}
	runnerSettings := adapterrunner.Settings{ListenAddress: "127.0.0.1:0", CertificateFile: ca, PrivateKeyFile: key, CredentialFile: runnerKey, TenantID: scope.ID().String(), Configuration: reference, BaseURL: fixture.URL, ProviderCAFile: ca, AppIDFile: write("app.key", []byte("fixture-app")), APIKeyFile: write("api.key", []byte("fixture-key")), GatewayURL: gateway.URL, GatewayCAFile: gatewayCA, GatewayCredentialFile: gatewayKey, Fixture: true}
	inputs := []providerv1.InputReference{{Name: "idenqa.input.country", Reference: "secret://input/country"}, {Name: "idenqa.input.id_type", Reference: "secret://input/id-type"}}
	if journey.smile {
		runnerSettings.Adapter = "smileid"
		runnerSettings.PartnerIDFile = write("partner.key", []byte("085"))
		runnerSettings.CallbackURL = "https://tenant.example/smile-callback"
		runnerSettings.UploadOrigin = fixture.URL
		runnerSettings.UploadCAFile = ca
		runnerSettings.InputsFile = write("inputs.json", []byte(`[{"name":"idenqa.input.country","reference":"secret://input/country","value":"NG"},{"name":"idenqa.input.id_type","reference":"secret://input/id-type","value":"PASSPORT"}]`))
	}
	runnerProcess, err := adapterrunner.NewProcess(t.Context(), runnerSettings)
	if err != nil {
		t.Fatal(err)
	}
	runnerContext, runnerCancel := context.WithCancel(t.Context())
	runnerDone := make(chan error, 1)
	go func() { runnerDone <- runnerProcess.Run(runnerContext) }()
	defer func() {
		runnerCancel()
		if err := <-runnerDone; err != nil && !errors.Is(err, context.Canceled) {
			t.Error(err)
		}
		runnerProcess.Close()
	}()
	settings := config.ProviderRuntime{Binding: provider.Binding{TenantID: scope.ID().String(), PolicyID: policyID, ProfileDigest: profileDigest, Requirement: "document", Region: "tenant.region.ng", Purpose: string(evidence.PurposeIdentityVerification), Recipient: "tenant.recipient.primary", Configuration: reference}, RunnerAddress: runnerProcess.Address(), RunnerCAFile: ca, RunnerServerName: "127.0.0.1", RunnerCredentialFile: runnerKey, GatewayCredentialFile: gatewayKey}
	if journey.smile {
		settings.Adapter = "smileid"
		settings.Binding.SelfieRequirement = "selfie"
		settings.Binding.Inputs = inputs
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	journey.runtimeFile = write("runtime.json", raw)
	restartAPI()
	verificationID, err := id.ParseVerification(verificationValue)
	if err != nil {
		t.Fatal(err)
	}
	// A failed queue insertion must roll back the grant, envelope, check and lifecycle together.
	plan, err := provider.NewPlan(settings.Binding, description)
	if err != nil {
		t.Fatal(err)
	}
	requests, err := providerpostgres.NewRequestStore(runtime, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := taskheadgate.NewPostgres(runtime.Native(), taskheadgate.DefaultConfig("idenqa-test"))
	if err != nil {
		t.Fatal(err)
	}
	planner, err := verificationpostgres.NewProcessingStore(runtime, plan, ids, failingProcessingEnqueuer{adapter}, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	if err := planner.WithPreparation(&providerpostgres.Preparation{Plan: plan, Requests: requests, IDs: ids, Catalog: catalog, Clock: clock.System{}}); err != nil {
		t.Fatal(err)
	}
	var grantsBefore int
	if err := admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.evidence_processing_grants`).Scan(&grantsBefore); err != nil {
		t.Fatal(err)
	}
	if started, err := planner.StartProcessing(t.Context(), scope, verificationID); started || !errors.Is(err, errPlannedQueueFailure) {
		t.Fatalf("provider rollback: %v %v", started, err)
	}
	var prepared, grantsAfter int
	if err := admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.provider_requests),(SELECT count(*) FROM idenqa.evidence_processing_grants)`).Scan(&prepared, &grantsAfter); err != nil || prepared != 0 || grantsAfter != grantsBefore {
		t.Fatalf("provider envelope escaped rollback: %d %v", prepared, err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	receiver := newJourneyReceiver(t, client, base, credential)
	t.Setenv("IDENQA_HEADGATE_INSTALLATION_ID", "idenqa-test")
	configuration, err := config.LoadWorker("")
	if err != nil {
		t.Fatal(err)
	}
	configuration.ProviderRuntimeFile = journey.runtimeFile
	configuration.SyntheticProcessing = false
	configuration.ProgressPollInterval = 20 * time.Millisecond
	startWorker := func() func() {
		t.Helper()
		process, err := bootstrapworker.NewProcessWithInfrastructure(t.Context(), configuration, slog.New(slog.NewJSONHandler(io.Discard, nil)), buildinfo.Info{Version: "integration"}, task.NewRegistry(), bootstrapworker.EvidenceInfrastructure{}, receiver.infrastructure(t, keyring))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- process.Run(ctx) }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
				if err := process.Close(context.WithoutCancel(t.Context())); err != nil {
					t.Error(err)
				}
			})
		}
		t.Cleanup(stop)
		return stop
	}
	stop := startWorker()
	if journey.smile {
		smileFixture.waitPending(t, admin)
		stop()
		assertSmilePollingFences(t, admin, runtime)
		smileFixture.complete.Store(true)
		stop = startWorker()
	}
	waitProviderAttempt(t, admin, verificationValue)
	stop()
	receiver.enableSuccess()
	stopRestart := startWorker()
	receiver.waitForDelivery(t, admin, scope, verificationID)
	stopRestart()
	var checks, dispatches, observations int
	var saved []byte
	if err := admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.verification_checks),(SELECT count(*) FROM idenqa.provider_dispatches WHERE result_body IS NOT NULL),(SELECT count(*) FROM idenqa.verification_observations),(SELECT request_body FROM idenqa.provider_requests LIMIT 1)`).Scan(&checks, &dispatches, &observations, &saved); err != nil {
		t.Fatal(err)
	}
	if checks != 1 || dispatches != 1 || observations != map[bool]int{false: 1, true: 4}[journey.smile] || (!journey.smile && calls.Load() != 1) {
		t.Fatalf("provider execution duplicated or incomplete: %d %d %d calls=%d", checks, dispatches, observations, calls.Load())
	}
	if journey.smile {
		smileFixture.assertCompleted(t, admin)
	}
	// Database guards preserve immutable request and completed receipt meaning.
	if _, err := admin.Native().Exec(t.Context(), `UPDATE idenqa.provider_requests SET request_body='{}'::jsonb`); err == nil {
		t.Fatal("request snapshot was mutable")
	}
	if _, err := admin.Native().Exec(t.Context(), `UPDATE idenqa.provider_dispatches SET result_body='{}'::jsonb`); err == nil {
		t.Fatal("completed dispatch was mutable")
	}
	var request providerv1.Request
	if json.Unmarshal(saved, &request) != nil || request.Validate() != nil {
		t.Fatal("invalid saved provider envelope")
	}
	otherTenant, err := ids.NewTenant()
	if err != nil {
		t.Fatal(err)
	}
	swapped := request
	swapped.TenantID = otherTenant.String()
	if claimed, result, err := requests.Claim(t.Context(), swapped); err == nil || claimed || result != nil {
		t.Fatal("cross-tenant dispatch lookup was not denied")
	}
	claimed, result, err := requests.Claim(t.Context(), request)
	if err != nil || claimed || result == nil || result.Signals[0].Name != map[bool]string{false: "idenqa.signal.document_quality", true: "idenqa.signal.provider_job"}[journey.smile] {
		t.Fatalf("stored dispatch replay failed: %v %v", claimed, err)
	}
	encoded, _ := json.Marshal(result)
	if bytes.Contains(encoded, []byte("discard-sensitive")) {
		t.Fatal("raw provider output escaped normalization")
	}
	// Completed dispatches cannot redeem or leak their evidence, including exact replay.
	payload, _ := json.Marshal(map[string]string{"attempt_id": request.AttemptID, "grant_id": request.Evidence[0].GrantID, "redemption_id": request.Evidence[0].RedemptionID})
	req, err := http.NewRequestWithContext(t.Context(), "POST", base+"/internal/v1/provider-evidence", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+gatewaySecret)
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if response.StatusCode != 403 {
		t.Fatalf("completed grant replay status=%d", response.StatusCode)
	}
}
func waitProviderAttempt(t *testing.T, admin *pg.Pool, verificationID string, expected ...int) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var completed, attempted int
		if err := admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.verification_checks WHERE verification_id=$1 AND state='completed'),(SELECT count(*) FROM idenqa.webhook_deliveries WHERE attempt_count=1)`, verificationID).Scan(&completed, &attempted); err != nil {
			t.Fatal(err)
		}
		want := 1
		if len(expected) > 0 {
			want = expected[0]
		}
		if completed == want && attempted == 1 {
			return
		}
		select {
		case <-deadline.C:
			var diagnostic string
			_ = admin.Native().QueryRow(t.Context(), `SELECT json_build_object('jobs',(SELECT json_agg(json_build_object('kind',kind,'state',state,'errors',errors)) FROM headgate.headgate_job),'checks',(SELECT json_agg(row_to_json(c)) FROM idenqa.verification_checks c))::text`).Scan(&diagnostic)
			t.Fatalf("provider journey timeout: %s", diagnostic)
		case <-tick.C:
		}
	}
}

type providerDiscoveryRoute struct{ tenant, policy, profile string }

func (route providerDiscoveryRoute) CaptureRoute() (string, string, string) {
	return route.tenant, route.policy, route.profile
}
func (providerDiscoveryRoute) Plan(verification.PlanInput) ([]verification.PlannedCheck, error) {
	return nil, verification.ErrPlanUnavailable
}

func TestProviderDiscoveryEnforcesExactTenantPolicyAndProfile(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		store, mutations := f.prepare(t)
		for _, mutation := range mutations {
			if _, err := store.AcceptUpload(t.Context(), f.scope, mutation); err != nil {
				t.Fatal(err)
			}
		}
		var policyID string
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT policy_id FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.creation.Session.ID().String()).Scan(&policyID); err != nil {
			t.Fatal(err)
		}
		otherTenant, err := f.ids.NewTenant()
		if err != nil {
			t.Fatal(err)
		}
		otherPolicy, err := f.ids.NewPolicy()
		if err != nil {
			t.Fatal(err)
		}
		exact := providerDiscoveryRoute{f.scope.ID().String(), policyID, f.creation.Session.ProfileDigest()}
		for _, test := range []struct {
			name  string
			route providerDiscoveryRoute
			want  int
		}{
			{"exact", exact, 1}, {"other tenant", providerDiscoveryRoute{otherTenant.String(), exact.policy, exact.profile}, 0}, {"other policy", providerDiscoveryRoute{exact.tenant, otherPolicy.String(), exact.profile}, 0}, {"other profile", providerDiscoveryRoute{exact.tenant, exact.policy, "sha256:other"}, 0},
		} {
			t.Run(test.name, func(t *testing.T) {
				planner, err := verificationpostgres.NewProcessingStore(f.runtime, test.route, f.ids, processingProbe{}, fixedIntegrationClock{now: f.now})
				if err != nil {
					t.Fatal(err)
				}
				targets, err := planner.ListReadyCaptures(t.Context(), f.now, 1)
				if err != nil || len(targets) != test.want {
					t.Fatalf("route targets=%d want=%d err=%v", len(targets), test.want, err)
				}
			})
		}
	})
}
