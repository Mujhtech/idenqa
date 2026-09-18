//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/models/onnx"
	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/bootstrap/modelrunner"
	bootstrapworker "github.com/Mujhtech/idenqa/internal/bootstrap/worker"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/model"
	modelpostgres "github.com/Mujhtech/idenqa/internal/model/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/tenant"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestONNXRuntimePublicCaptureThroughDecisionAndWebhook(t *testing.T) {
	if os.Getenv("ONNX_TEST_PYTHON") == "" {
		t.Skip("set ONNX_TEST_PYTHON to the hash-locked Python environment")
	}
	runProviderPublicJourney(t, false, true)
}
func TestONNXFaceMatchingPublicCaptureThroughDecisionAndWebhook(t *testing.T) {
	if os.Getenv("ONNX_TEST_PYTHON") == "" {
		t.Skip("native ONNX environment required")
	}
	runProviderPublicJourney(t, false, true, true)
}
func TestComposedDocumentPADMatchingPublicJourney(t *testing.T) {
	if os.Getenv("ONNX_TEST_PYTHON") == "" {
		t.Skip("native ONNX environment required")
	}
	runProviderPublicJourney(t, false, true, true, true)
}
func padFixtureJPEG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 128, 128))
	for y := 0; y < 128; y++ {
		for x := 0; x < 128; x++ {
			picture.Set(x, y, color.RGBA{R: 128, G: 64, B: 32, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := jpeg.Encode(&encoded, picture, nil); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}
func (journey *providerPublicJourney) runModel(t *testing.T, admin, runtime *pg.Pool, scope tenant.Scope, verificationValue, policyID, profileDigest, base, credential, keyring string, restartAPI func()) {
	t.Helper()
	directory := t.TempDir()
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	target, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	var modelCredentials []string
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
		denied, err := http.NewRequestWithContext(r.Context(), "POST", base+"/internal/v1/model-evidence", bytes.NewReader(body))
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
		for _, credential := range modelCredentials {
			if r.Header.Get("Authorization") == "Bearer "+credential {
				continue
			}
			cross, err := http.NewRequestWithContext(r.Context(), "POST", base+"/internal/v1/model-evidence", bytes.NewReader(raw))
			if err != nil {
				t.Error(err)
				return
			}
			cross.Header.Set("Authorization", "Bearer "+credential)
			response, err := http.DefaultClient.Do(cross)
			if err != nil {
				t.Error(err)
				return
			}
			_ = response.Body.Close()
			if response.StatusCode != 403 {
				t.Error("another model credential redeemed this attempt")
			}
		}
		proxy.ServeHTTP(w, r)
	}))
	defer gateway.Close()
	ca := write("ca.pem", pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: gateway.TLS.Certificates[0].Certificate[0]}))
	rawKey, err := x509.MarshalPKCS8PrivateKey(gateway.TLS.Certificates[0].PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	key := write("tls-key.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: rawKey}))
	runnerKey := write("runner.key", []byte("idq_wrk_v1_"+base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x61}, 32))))
	gatewaySecret := "idq_wrk_v1_" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x62}, 32))
	gatewayKey := write("gateway.key", []byte(gatewaySecret))
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	modelID, err := ids.NewModel()
	if err != nil {
		t.Fatal(err)
	}
	python := os.Getenv("ONNX_TEST_PYTHON")
	runtimeDigest, err := onnx.RuntimeDigest(t.Context(), python)
	if err != nil {
		t.Fatal(err)
	}
	fixture := "../../adapters/models/onnx/testdata/pad_fixture.onnx"
	if journey.matching {
		fixture = "../../adapters/models/onnx/testdata/embedding_fixture.onnx"
	}
	modelFile, err := filepath.Abs(fixture)
	if err != nil {
		t.Fatal(err)
	}
	modelBytes, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(modelBytes)
	configurationModel := onnx.Configuration{TenantID: scope.ID().String(), Width: 128, Height: 128, EvaluationOnly: true,
		Registration: modelv1.ConfigurationReference{ModelID: modelID.String(), ConfigurationRef: "configuration://model/pad-fixture"},
		Manifest: modelv1.Manifest{Provenance: modelv1.Provenance{ModelID: modelID.String(), ModelVersion: "0.1.0", ModelDigest: "sha256:" + hex.EncodeToString(sum[:]), RuntimeDigest: runtimeDigest, PreprocessingDigest: onnx.PreprocessingDigest(128, 128), OutputSchemaDigest: onnx.OutputSchemaDigest(), Contract: modelv1.CurrentVersion},
			Capabilities: []modelv1.Capability{{Evaluation: "idenqa.check.passive_pad", AcceptedEvidence: []string{"idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.passive_pad"}}},
			Restrictions: modelv1.Restrictions{MaximumGrants: 1, MaximumInputBytes: 10 << 20, MaximumResultSize: 4096, MaximumDuration: 30 * time.Second}}}
	detectorFile, err := filepath.Abs("../../adapters/models/onnx/testdata/detector_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	detectorBytes, err := os.ReadFile("../../adapters/models/onnx/testdata/detector_fixture.onnx")
	if err != nil {
		t.Fatal(err)
	}
	detectorSum := sha256.Sum256(detectorBytes)
	configurationModel.FacePreparation = &onnx.FacePreparation{Algorithm: "yunet-context-v1", DetectorDigest: "sha256:" + hex.EncodeToString(detectorSum[:])}
	configurationModel.Manifest.Provenance.PreprocessingDigest = onnx.FacePreprocessingDigest(*configurationModel.FacePreparation)
	if journey.matching {
		configurationModel.FaceMatching = true
		configurationModel.Width, configurationModel.Height = 112, 112
		configurationModel.Manifest.Restrictions.MaximumGrants = 2
		configurationModel.Manifest.Provenance.PreprocessingDigest = onnx.MatchingPreprocessingDigest(*configurationModel.FacePreparation)
		configurationModel.Manifest.Provenance.OutputSchemaDigest = onnx.MatchingOutputSchemaDigest()
		configurationModel.Manifest.Capabilities = []modelv1.Capability{{Evaluation: "idenqa.check.face_match_1to1", AcceptedEvidence: []string{"idenqa.evidence.document_image", "idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.face_match_1to1"}}}
	}

	configurationModel.Registration.ConfigurationDigest = onnx.ConfigurationDigest(configurationModel)
	if !journey.matching {

		// The same native preparation path is available to the explicitly offline evaluator.
		offlineImage := padFixtureJPEG(t)
		offlineHash := sha256.Sum256(offlineImage)
		write("offline.png", offlineImage)
		threshold := 0.8
		dataset := modelrunner.Dataset{Version: 1, Reference: "synthetic.fixture", ApprovalReference: "fixture.only", ThresholdReference: "fixture.operating-point", RealScoreThreshold: &threshold, Samples: []modelrunner.Sample{{ID: "sample-1", SubjectID: "fixture-1", Split: "evaluation", Path: "offline.png", SHA256: hex.EncodeToString(offlineHash[:]), Label: "attack", AttackType: "synthetic.fixture", DeviceClass: "fixture", CaptureCondition: "fixture"}}}
		datasetJSON, err := json.Marshal(dataset)
		if err != nil {
			t.Fatal(err)
		}
		datasetFile := write("dataset.json", datasetJSON)
		report, err := modelrunner.Evaluate(t.Context(), modelrunner.Settings{Python: python, ModelFile: modelFile, DetectorFile: detectorFile, Model: configurationModel}, datasetFile)
		if err != nil || report.ProductionAccepted || report.Coverage != "attack_only" || report.Total.AttacksScored != 1 || report.Total.BPCER != nil {
			t.Fatalf("native offline evaluation failed: %+v %v", report, err)
		}
	}
	runnerProcess, err := modelrunner.NewProcess(t.Context(), modelrunner.Settings{ListenAddress: "127.0.0.1:0", CertificateFile: ca, PrivateKeyFile: key, CredentialFile: runnerKey, Python: python, ModelFile: modelFile, DetectorFile: detectorFile, Model: configurationModel, GatewayURL: gateway.URL, GatewayCAFile: ca, GatewayCredentialFile: gatewayKey})
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
	settings := config.ModelRuntime{Manifest: configurationModel.Manifest, Binding: model.Binding{TenantID: scope.ID().String(), PolicyID: policyID, ProfileDigest: profileDigest, Requirement: "selfie", Region: "tenant.region.ng", Purpose: string(evidence.PurposeIdentityVerification), Recipient: "tenant.recipient.primary", Configuration: configurationModel.Registration}, RunnerAddress: runnerProcess.Address(), RunnerCAFile: ca, RunnerServerName: "127.0.0.1", RunnerCredentialFile: runnerKey, GatewayCredentialFile: gatewayKey}
	if journey.matching {
		settings.Binding.DocumentRequirement = "document"
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	journey.runtimeFile = write("runtime.json", raw)
	if journey.composed {
		extra, providerFile := startComposedRunners(t, settings, modelrunner.Settings{Python: python, DetectorFile: detectorFile, ModelFile: modelFile, Model: configurationModel}, base, ca, key, gateway.URL, runnerKey, scope, policyID, profileDigest)
		raw, err = json.Marshal(struct {
			Models []config.ModelRuntime `json:"models"`
		}{[]config.ModelRuntime{settings, extra}})
		if err != nil {
			t.Fatal(err)
		}
		journey.runtimeFile = write("runtime.json", raw)
		journey.providerFile = providerFile
		extraSecret, err := config.ReadCredentialFile(extra.GatewayCredentialFile)
		if err != nil {
			t.Fatal(err)
		}
		modelCredentials = []string{gatewaySecret, extraSecret}

	}

	restartAPI()
	verificationID, err := id.ParseVerification(verificationValue)
	if err != nil {
		t.Fatal(err)
	}
	// A failed queue insertion must roll back the grant, envelope, check and lifecycle together.
	plan, err := model.NewPlan(settings.Binding, settings.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	requests, err := modelpostgres.NewRequestStore(runtime, clock.System{})
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
	if err := planner.WithPreparation(&modelpostgres.Preparation{Plan: plan, Requests: requests, IDs: ids, Catalog: catalog, Clock: clock.System{}}); err != nil {
		t.Fatal(err)
	}
	var grantsBefore int
	if err := admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.evidence_processing_grants`).Scan(&grantsBefore); err != nil {
		t.Fatal(err)
	}
	if started, err := planner.StartProcessing(t.Context(), scope, verificationID); started || !errors.Is(err, errPlannedQueueFailure) {
		t.Fatalf("model rollback: %v %v", started, err)
	}
	var prepared, grantsAfter int
	if err := admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.model_requests),(SELECT count(*) FROM idenqa.evidence_processing_grants)`).Scan(&prepared, &grantsAfter); err != nil || prepared != 0 || grantsAfter != grantsBefore {
		t.Fatalf("model envelope escaped rollback: %d %v", prepared, err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	receiver := newJourneyReceiver(t, client, base, credential)
	t.Setenv("IDENQA_HEADGATE_INSTALLATION_ID", "idenqa-test")
	configuration, err := config.LoadWorker("")
	if err != nil {
		t.Fatal(err)
	}
	configuration.ModelRuntimeFile = journey.runtimeFile
	configuration.ProviderRuntimeFile = journey.providerFile
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
	if journey.composed {
		// A late model-request failure must roll back the provider and earlier model
		// grants/checks too. The sequence is only a nontransactional test observation.
		for _, statement := range []string{
			`CREATE SEQUENCE idenqa.test_composed_failure`,
			`CREATE FUNCTION idenqa.test_reject_composed() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$ BEGIN IF NEW.request_body->>'evaluation'='idenqa.check.passive_pad' THEN PERFORM nextval('idenqa.test_composed_failure'); RAISE EXCEPTION 'synthetic composition failure'; END IF; RETURN NEW; END $$`,
			`CREATE TRIGGER test_reject_composed BEFORE INSERT ON idenqa.model_requests FOR EACH ROW EXECUTE FUNCTION idenqa.test_reject_composed()`,
		} {
			if _, err := admin.Native().Exec(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
		stopBlocked := startWorker()
		deadline := time.NewTimer(15 * time.Second)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer deadline.Stop()
		defer ticker.Stop()
		observed := false
		for !observed {
			if err := admin.Native().QueryRow(t.Context(), `SELECT is_called FROM idenqa.test_composed_failure`).Scan(&observed); err != nil {
				t.Fatal(err)
			}
			if observed {
				break
			}
			select {
			case <-deadline.C:
				t.Fatal("composed preparation failure was not exercised")
			case <-ticker.C:
			}
		}
		stopBlocked()
		var requestsCount, checksCount, grantCount int
		if err := admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.model_requests)+(SELECT count(*) FROM idenqa.provider_requests),(SELECT count(*) FROM idenqa.verification_checks),(SELECT count(*) FROM idenqa.evidence_processing_grants)`).Scan(&requestsCount, &checksCount, &grantCount); err != nil || requestsCount != 0 || checksCount != 0 || grantCount != grantsBefore {
			t.Fatalf("partial composition escaped rollback: %d %d %d %v", requestsCount, checksCount, grantCount, err)
		}
		for _, statement := range []string{`DROP TRIGGER test_reject_composed ON idenqa.model_requests`, `DROP FUNCTION idenqa.test_reject_composed()`, `DROP SEQUENCE idenqa.test_composed_failure`} {
			if _, err := admin.Native().Exec(t.Context(), statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	stop := startWorker()
	expectedChecks := 1
	if journey.composed {
		expectedChecks = 3
	}
	waitProviderAttempt(t, admin, verificationValue, expectedChecks)
	stop()
	receiver.enableSuccess()
	stopRestart := startWorker()
	receiver.waitForDelivery(t, admin, scope, verificationID)
	stopRestart()
	var checks, dispatches, observations int
	var saved []byte
	if err := admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.verification_checks),(SELECT count(*) FROM idenqa.model_dispatches WHERE result_body IS NOT NULL),(SELECT count(*) FROM idenqa.verification_observations),(SELECT request_body FROM idenqa.model_requests WHERE request_body->>'model_registration_id'=$1 LIMIT 1)`, modelID.String()).Scan(&checks, &dispatches, &observations, &saved); err != nil {
		t.Fatal(err)
	}
	expectedModels := 1
	if journey.composed {
		expectedModels = 2
	}
	if checks != expectedChecks || dispatches != expectedModels || observations != expectedChecks {
		t.Fatalf("model execution duplicated or incomplete: %d %d %d", checks, dispatches, observations)
	}
	if journey.composed {
		var grants, used int
		if err := admin.Native().QueryRow(t.Context(), `SELECT count(*),count(*) FILTER (WHERE uses=1) FROM idenqa.evidence_processing_grants WHERE tenant_id=$1`, scope.ID().String()).Scan(&grants, &used); err != nil || grants != 4 || used != 4 {
			t.Fatalf("composed grants: %d %d %v", grants, used, err)
		}
	}
	// Database guards preserve immutable request and completed receipt meaning.
	if _, err := admin.Native().Exec(t.Context(), `UPDATE idenqa.model_requests SET request_body='{}'::jsonb`); err == nil {
		t.Fatal("request snapshot was mutable")
	}
	if _, err := admin.Native().Exec(t.Context(), `UPDATE idenqa.model_dispatches SET result_body='{}'::jsonb`); err == nil {
		t.Fatal("completed dispatch was mutable")
	}
	var request modelv1.Request
	if json.Unmarshal(saved, &request) != nil || request.Validate() != nil {
		t.Fatal("invalid saved model envelope")
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
	if claimed, result, err := requests.Claim(t.Context(), request); err == nil || claimed || result != nil {
		t.Fatal("completed authority accepted a dispatch")
	}
	for _, ref := range request.Evidence {

		var uses int
		if err := admin.Native().QueryRow(t.Context(), `SELECT uses FROM idenqa.evidence_processing_grants WHERE tenant_id=$1 AND id=$2`, request.TenantID, ref.GrantID).Scan(&uses); err != nil || uses != 1 {
			t.Fatalf("evidence was not consumed exactly once: %d %v", uses, err)
		}
	}
	if len(request.Evidence) != int(configurationModel.Manifest.Restrictions.MaximumGrants) {
		t.Fatal("incorrect pair grant count")
	}
	if request.Provenance != configurationModel.Manifest.Provenance {
		t.Fatal("model provenance changed")
	}
	var resultBody []byte
	if err := admin.Native().QueryRow(t.Context(), `SELECT result_body FROM idenqa.model_dispatches WHERE attempt_id=$1`, request.AttemptID).Scan(&resultBody); err != nil {
		t.Fatal(err)
	}
	var result modelv1.Result
	if json.Unmarshal(resultBody, &result) != nil || result.ValidateForRequest(request) != nil || len(result.Signals) != 1 || result.Signals[0].Outcome != modelv1.SignalOutcomeInconclusive {
		t.Fatalf("candidate asserted assurance: %s", resultBody)
	}
	// Completed dispatches cannot redeem or leak their evidence, including exact replay.
	payload, _ := json.Marshal(map[string]string{"attempt_id": request.AttemptID, "grant_id": request.Evidence[0].GrantID, "redemption_id": request.Evidence[0].RedemptionID})
	req, err := http.NewRequestWithContext(t.Context(), "POST", base+"/internal/v1/model-evidence", bytes.NewReader(payload))
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
