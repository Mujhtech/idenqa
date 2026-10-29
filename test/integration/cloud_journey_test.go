//go:build integration && cloudjourney

package integration_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	bootstrapapi "github.com/Mujhtech/idenqa/internal/bootstrap/api"
	bootstrapworker "github.com/Mujhtech/idenqa/internal/bootstrap/worker"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/config"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/managedlaunch"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/health"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
)

// This opt-in cross-repository proof keeps Core's ordinary distribution and CI
// independent of Cloud. Both applications run their real HTTP boundaries; only
// the workforce/deployment catalogue and normalized check results are fixtures.
func TestCloudVerificationBrowserJourney(t *testing.T) {
	cloudRoot := os.Getenv("IDENQA_CLOUD_JOURNEY_REPOSITORY")
	if cloudRoot == "" {
		t.Skip("IDENQA_CLOUD_JOURNEY_REPOSITORY is required for the opt-in Cloud browser journey")
	}
	database := createIsolatedDatabase(t)
	migrator, err := pg.OpenMigrator(t.Context(), migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = migrator.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = migrator.Close(); err != nil {
		t.Fatal(err)
	}
	queueMigration, err := taskheadgate.OpenMigrator(t.Context(), database.url, "headgate", 5*time.Second, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = queueMigration.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err = queueMigration.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	admin, err := pg.Open(t.Context(), poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	tenants, err := tenantpostgres.New(admin)
	if err != nil {
		t.Fatal(err)
	}
	administrator, err := tenant.NewAdmin(tenants, ids, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := administrator.Create(t.Context(), tenant.AdminAction{Actor: "cloud-journey-fixture", Reason: "synthetic end-to-end proof"}, "Verification journey fixture")
	if err != nil {
		t.Fatal(err)
	}
	scope, _ := tenant.NewScope(owner.ID())
	role := database.createRuntimeRole(t)
	database.grantHeadgateRuntime(t, role)
	runtimeConfiguration := poolConfig(database.url)
	runtimeConfiguration.Role = role
	runtime, err := pg.Open(t.Context(), runtimeConfiguration)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	pepper := bytes.Repeat([]byte{0x31}, 32)
	peppers, _ := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: pepper})
	key, presented := newFullScopeIntegrationCredential(t, ids, owner.ID(), time.Now().UTC(), peppers)
	keys, err := accesspostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if err = keys.Create(t.Context(), scope, key); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(t.TempDir(), "private")
	if err = os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	keyringPath := filepath.Join(directory, "evidence-keys.json")
	keyring, err := localkms.Create(keyringPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = keyring.Close(); err != nil {
		t.Fatal(err)
	}
	port := reserveLoopbackPort(t)
	configuration := loadPublicFlowConfiguration(t, database.url, role, port, directory, keyringPath, pepper)
	configuration.Region = "eu-west-1"
	configuration.ExperienceSigningVersion = 1
	if err = configuration.ExperienceSigningKeys.Decode("1=" + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x42}, ed25519.SeedSize))); err != nil {
		t.Fatal(err)
	}
	t.Setenv("IDENQA_REGION", configuration.Region)
	configuration.HTTPCORSAllowedOrigins = []string{"http://127.0.0.1:43187"}
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	startAPI := func() publicFlowProcess {
		process, err := bootstrapapi.NewProcess(t.Context(), configuration, logger, &health.State{}, buildinfo.Info{Version: "cloud-journey"})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		stop := func() error { cancel(); return nil }
		return startPublicFlowProcess(stop, stop, func() error { return process.Run(ctx) })
	}
	process := startAPI()
	waitForPublicAPIReadiness(t, base, process)
	client := &http.Client{Timeout: 10 * time.Second}
	call := func(method, path, key string, body, result any, status int) http.Header {
		t.Helper()
		return performPublicJSONRequest(t, client, publicJSONRequest{Method: method, URL: base + path, Bearer: presented.Reveal(), IdempotencyKey: key, Body: body, Result: result, WantStatus: status})
	}
	var policyResult policy.ManagementResult
	call("POST", "/v1/policies", "cloud-journey-policy", map[string]any{"definition": publicSyntheticPolicyDefinition()}, &policyResult, 200)
	call("POST", "/v1/policies/"+policyResult.Policy.ID+"/activate", "cloud-journey-policy-active", map[string]any{"revision": 1, "expected_version": 0, "reason": "synthetic_fixture"}, &policyResult, 200)
	profileMaterial, err := os.ReadFile("../../examples/synthetic/capture-profile.json")
	if err != nil {
		t.Fatal(err)
	}
	var profileDocument map[string]any
	if err = json.Unmarshal(profileMaterial, &profileDocument); err != nil {
		t.Fatal(err)
	}
	// This browser proof exercises a truthful upload-only acquisition branch.
	requirements := profileDocument["requirements"].([]any)
	requirement := requirements[0].(map[string]any)
	requirement["acquisition"] = map[string]any{"strategy": "any_of", "methods": []string{"idenqa.method.file_upload"}}
	requirement["fallbacks"] = []any{}
	var profile openapiv1.CaptureProfileMutation
	headers := call("POST", "/v1/capture-profiles", "cloud-journey-profile", map[string]any{"name": "Synthetic verification", "document": profileDocument}, &profile, 201)
	performPublicJSONRequest(t, client, publicJSONRequest{Method: "POST", URL: base + "/v1/capture-profiles/" + profile.ProfileID + "/publish", Bearer: presented.Reveal(), IdempotencyKey: "cloud-journey-publish", IfMatch: headers.Get("ETag"), WantStatus: 200, Result: &profile})
	var notice openapiv1.NoticeVersion
	call("POST", "/v1/notices", "cloud-journey-notice", openapiv1.NoticeVersionCreate{Key: "tenant.notice.cloud_journey", Locale: "en", Controller: "Synthetic tenant", Recipient: "Synthetic tenant", Copy: openapiv1.NoticeCopy{Title: "Synthetic verification notice", Summary: "Use synthetic evidence only.", Purpose: "Exercise this test verification journey.", Consequences: "Refusing stops this test."}, EffectiveAt: time.Now().UTC().Add(-time.Minute)}, &notice, 201)
	receiver := newJourneyReceiver(t, client, base, presented.Reveal())
	stopPublicFlowProcess(t, process)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	preparation := managedlaunch.PreparationConfiguration{Routes: []managedlaunch.PreparationRoute{{TenantID: owner.ID().String(), ProfileID: profile.ProfileID, PolicyID: policyResult.Policy.ID, NoticeID: notice.ID, Locale: "en", Category: "tenant.authority.customer_declared", Purpose: "idenqa.purpose.identity_verification", Jurisdiction: "tenant.jurisdiction.synthetic", PolicyPack: "tenant.policy.synthetic", RecipientReference: "tenant.recipient.synthetic", RetentionReference: "tenant.retention.synthetic"}}}
	preparationBytes, _ := json.Marshal(preparation)
	configuration.ManagedLaunchPreparationFile = filepath.Join(directory, "launch-preparation.json")
	if err = os.WriteFile(configuration.ManagedLaunchPreparationFile, preparationBytes, 0600); err != nil {
		t.Fatal(err)
	}
	configuration.ManagedLaunchPublicKeys = config.ManagedLaunchAuthorityKeys{"journey": base64.StdEncoding.EncodeToString(publicKey)}
	configuration.ManagedLaunchOrganisationID, configuration.ManagedLaunchEnvironmentID, configuration.ManagedLaunchDeploymentID = "org_journey", "env_journey", "dep_journey"
	configuration.ManagedLaunchIssuer, configuration.ManagedLaunchAuthorityTTL = "http://127.0.0.1:43187", time.Minute
	process = startAPI()
	t.Cleanup(func() { stopPublicFlowProcess(t, process) })
	waitForPublicAPIReadiness(t, base, process)
	workerConfiguration, err := config.LoadWorker("")
	if err != nil {
		t.Fatal(err)
	}
	workerConfiguration.SyntheticProcessing, workerConfiguration.ProgressPollInterval = true, 20*time.Millisecond
	startWorker := func() func() {
		worker, err := bootstrapworker.NewProcessWithInfrastructure(t.Context(), workerConfiguration, logger, buildinfo.Info{Version: "cloud-journey"}, task.NewRegistry(), bootstrapworker.EvidenceInfrastructure{}, receiver.infrastructure(t, keyringPath))
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- worker.Run(ctx) }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
				if err := worker.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
		}
		t.Cleanup(stop)
		return stop
	}
	stopWorker := startWorker()
	fixture := map[string]string{"coreUrl": base, "coreTenantId": owner.ID().String(), "credential": presented.Reveal(), "profileId": profile.ProfileID, "region": configuration.Region, "privateKey": base64.StdEncoding.EncodeToString(privateKey), "assetDirectory": directory}
	material, _ := json.Marshal(fixture)
	fixturePath := filepath.Join(directory, "cloud-journey-private.json")
	if err = os.WriteFile(fixturePath, material, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "go", "test", "-tags=integration", "./internal/transport/httpserver", "-run", "^TestLiveVerificationBrowserJourney$", "-count=1", "-timeout=3m")
	command.Dir = cloudRoot
	command.Env = append(os.Environ(), "IDENQA_VERIFICATION_JOURNEY_FILE="+fixturePath)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	restarted := false
	for {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Cloud browser journey: %v\n%s", err, output.String())
			}
			if !restarted {
				t.Fatal("browser finished without proving worker replacement after webhook failure")
			}
			receiver.mu.Lock()
			defer receiver.mu.Unlock()
			if len(receiver.failures) != 0 {
				t.Fatal("webhook signature or tamper verification failed")
			}
			bodies := make(map[string][]byte)
			retried := false
			for index, event := range receiver.events {
				if original, exists := bodies[event]; exists {
					if !bytes.Equal(original, receiver.bodies[index]) {
						t.Fatal("worker replacement changed the signed webhook body")
					}
					retried = true
				} else {
					bodies[event] = receiver.bodies[index]
				}
			}
			if len(bodies) != 2 || !retried {
				t.Fatal("missing completion webhook or preserved-identity retry")
			}
			return
		case <-ticker.C:
			if restarted {
				continue
			}
			var attempts int
			if err := admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.webhook_delivery_attempts WHERE tenant_id=$1`, scope.ID().String()).Scan(&attempts); err != nil {
				t.Fatal(err)
			}
			if attempts > 0 {
				stopWorker()
				receiver.enableSuccess()
				startWorker()
				restarted = true
			}
		case <-deadline.C:
			_ = command.Process.Kill()
			<-done
			t.Fatalf("Cloud browser journey timed out\n%s", output.String())
		}
	}
}
