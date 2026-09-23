//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	bootstrapapi "github.com/Mujhtech/idenqa/internal/bootstrap/api"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	tinkcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto/tink"
	"github.com/Mujhtech/idenqa/internal/platform/health"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	objectlocal "github.com/Mujhtech/idenqa/internal/platform/objectstore/local"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/tenant"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestPublicEvidenceUploadFlowThroughRunnableLocalAPI(t *testing.T) {
	objectDirectory := t.TempDir()
	objects, err := objectlocal.Open(objectlocal.Config{
		Directory: objectDirectory, MaxObjectBytes: 2 * evidence.MaximumUploadMaximumBytes,
	})
	if err != nil {
		t.Fatalf("open local ciphertext store: %v", err)
	}
	t.Cleanup(func() {
		if err := objects.Close(); err != nil {
			t.Errorf("close local ciphertext store: %v", err)
		}
	})
	backend := publicFlowBackend{
		verifyProcessing: true,
		start: func(
			t *testing.T,
			databaseURL string,
			runtimeRole string,
			port int,
			keyringFile string,
			pepperMaterial []byte,
		) publicFlowProcess {
			t.Helper()
			configuration := loadPublicFlowConfiguration(
				t, databaseURL, runtimeRole, port, objectDirectory, keyringFile, pepperMaterial,
			)
			state := &health.State{}
			process, processErr := bootstrapapi.NewProcess(
				t.Context(),
				configuration,
				slog.New(slog.NewJSONHandler(io.Discard, nil)),
				state,
				buildinfo.Info{Version: "integration", Commit: "integration", Date: "integration"},
			)
			if processErr != nil {
				t.Fatalf("compose runnable local API: %v", processErr)
			}
			processContext, stopProcess := context.WithCancel(t.Context())

			return startPublicFlowProcess(
				func() error {
					stopProcess()

					return nil
				},
				func() error {
					stopProcess()

					return nil
				},
				func() error { return process.Run(processContext) },
			)
		},
		readCiphertext: func(t *testing.T, object objectstore.Object) []byte {
			t.Helper()
			reader, err := objects.Open(t.Context(), object)
			if err != nil {
				t.Fatalf("open local persisted ciphertext: %v", err)
			}

			return readAndCloseCiphertext(t, reader)
		},
	}

	runPublicEvidenceUploadFlow(t, backend)
}

func TestPublicEvidenceUploadFlowThroughS3Distribution(t *testing.T) {
	if os.Getenv("S3_TEST_ENABLED") != "true" {
		t.Skip("S3_TEST_ENABLED is not true")
	}
	const (
		bucket = "idenqa-evidence"
		prefix = "idenqa/integration"
	)
	provider := newS3PublicFlowFixture(t, bucket)
	endpoint := provider.URL
	backend := publicFlowBackend{
		start: func(
			t *testing.T,
			databaseURL string,
			runtimeRole string,
			port int,
			keyringFile string,
			pepperMaterial []byte,
		) publicFlowProcess {
			t.Helper()
			setPublicFlowCoreEnvironment(t, databaseURL, runtimeRole, port, keyringFile, pepperMaterial)
			for key, value := range map[string]string{
				"IDENQA_S3_BUCKET":           bucket,
				"IDENQA_S3_REGION":           "us-east-1",
				"IDENQA_S3_PREFIX":           prefix,
				"IDENQA_S3_ENDPOINT":         endpoint,
				"IDENQA_S3_FORCE_PATH_STYLE": "true",
				"IDENQA_S3_ALLOW_HTTP":       "true",
				"AWS_ACCESS_KEY_ID":          "idenqa-integration-fixture",
				"AWS_SECRET_ACCESS_KEY":      "idenqa-integration-fixture",
				"AWS_EC2_METADATA_DISABLED":  "true",
			} {
				t.Setenv(key, value)
			}
			processContext, cancelProcess := context.WithCancel(context.Background())
			command := exec.CommandContext(processContext, "../../bin/distributions/s3/api")
			command.Cancel = func() error {
				err := command.Process.Signal(os.Interrupt)
				if errors.Is(err, os.ErrProcessDone) {
					return nil
				}

				return err
			}
			command.WaitDelay = 10 * time.Second
			command.Stdout = io.Discard
			command.Stderr = io.Discard
			if err := command.Start(); err != nil {
				cancelProcess()
				t.Fatalf("start S3 API distribution: %v", err)
			}

			return startPublicFlowProcess(
				func() error {
					cancelProcess()

					return nil
				},
				func() error {
					err := command.Process.Kill()
					if errors.Is(err, os.ErrProcessDone) {
						return nil
					}

					return err
				},
				func() error {
					err := command.Wait()
					if processContext.Err() != nil && errors.Is(err, context.Canceled) {
						return nil
					}

					return err
				},
			)
		},
		readCiphertext: func(t *testing.T, object objectstore.Object) []byte {
			t.Helper()
			record := object.Record()
			objectURL := endpoint + "/" + path.Join(bucket, prefix, string(object.Key()), record.Version)
			request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, objectURL, nil)
			if err != nil {
				t.Fatalf("construct independent S3 ciphertext request: %v", err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatalf("read S3 ciphertext independently: %v", err)
			}
			if response.StatusCode != http.StatusOK {
				_ = response.Body.Close()
				t.Fatalf("independent S3 ciphertext status = %d, want %d", response.StatusCode, http.StatusOK)
			}

			return readAndCloseCiphertext(t, response.Body)
		},
	}

	runPublicEvidenceUploadFlow(t, backend)
}

type publicFlowBackend struct {
	verifyProcessing bool
	providerJourney  *providerPublicJourney
	start            func(
		t *testing.T,
		databaseURL string,
		runtimeRole string,
		port int,
		keyringFile string,
		pepperMaterial []byte,
	) publicFlowProcess
	readCiphertext func(*testing.T, objectstore.Object) []byte
}

type publicFlowProcess struct {
	stop      func() error
	forceStop func() error
	done      <-chan struct{}
	waitError func() error
}

func runPublicEvidenceUploadFlow(t *testing.T, backend publicFlowBackend) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("open migrator: %v", err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("migrate evidence flow database: %v", err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}
	headgateMigrator, err := taskheadgate.OpenMigrator(
		ctx, database.url, "headgate", 5*time.Second, 30*time.Second,
	)
	if err != nil {
		t.Fatalf("open Headgate migrator: %v", err)
	}
	if _, err := headgateMigrator.Up(ctx); err != nil {
		t.Fatalf("migrate Headgate schema: %v", err)
	}
	if err := headgateMigrator.Close(ctx); err != nil {
		t.Fatalf("close Headgate migrator: %v", err)
	}

	adminPool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("open administrative pool: %v", err)
	}
	defer adminPool.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("new identifier generator: %v", err)
	}
	tenantStore, err := tenantpostgres.New(adminPool)
	if err != nil {
		t.Fatalf("new tenant store: %v", err)
	}
	tenantAdmin, err := tenant.NewAdmin(tenantStore, generator, clock.System{})
	if err != nil {
		t.Fatalf("new tenant admin: %v", err)
	}
	owner, err := tenantAdmin.Create(ctx, tenant.AdminAction{
		Actor: "public-flow-integration", Reason: "prove encrypted public evidence flow",
	})
	if err != nil {
		t.Fatalf("create evidence-flow tenant: %v", err)
	}
	scope, err := tenant.NewScope(owner.ID())
	if err != nil {
		t.Fatalf("new tenant scope: %v", err)
	}
	var policyID id.Policy
	if !backend.verifyProcessing && backend.providerJourney == nil {
		policyID = seedIntegrationPolicy(t, adminPool, owner.ID(), generator, time.Now().UTC().Truncate(time.Second))
	}

	runtimeRole := database.createRuntimeRole(t)
	database.grantHeadgateRuntime(t, runtimeRole)
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = runtimeRole
	runtimePool, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer runtimePool.Close()
	pepperMaterial := bytes.Repeat([]byte{0x31}, 32)
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: pepperMaterial})
	if err != nil {
		t.Fatalf("new API key pepper set: %v", err)
	}
	apiKey, presentedKey := newFullScopeIntegrationCredential(
		t, generator, owner.ID(), time.Now().UTC(), peppers,
	)
	accessStore, err := accesspostgres.New(runtimePool)
	if err != nil {
		t.Fatalf("new access store: %v", err)
	}
	if err := accessStore.Create(ctx, scope, apiKey); err != nil {
		t.Fatalf("persist API key: %v", err)
	}

	evidenceDirectory := t.TempDir()
	keyringFile := evidenceDirectory + "/keyring.json"
	keyring, err := localkms.Create(keyringFile)
	if err != nil {
		t.Fatalf("create evidence keyring: %v", err)
	}
	if err := keyring.Close(); err != nil {
		t.Fatalf("close initial evidence keyring: %v", err)
	}

	port := reserveLoopbackPort(t)
	process := backend.start(t, database.url, runtimeRole, port, keyringFile, pepperMaterial)
	t.Cleanup(func() { stopPublicFlowProcess(t, process) })
	baseURL := "http://127.0.0.1:" + strconv.Itoa(port)
	waitForPublicAPIReadiness(t, baseURL, process)

	client := &http.Client{Timeout: 10 * time.Second}
	backendCredential := presentedKey.Reveal()
	if backend.verifyProcessing {
		policyID = assertPublicPolicyAdministration(t, client, baseURL, backendCredential, adminPool, runtimePool, scope, peppers)
	}
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("load built-in evidence registry: %v", err)
	}
	if backend.providerJourney != nil {
		policyID = createProviderFixturePolicy(t, client, baseURL, backendCredential, backend.providerJourney.smile, backend.providerJourney.model, backend.providerJourney.matching, backend.providerJourney.composed)
	}
	document := integrationProfileDocument(t, registry, evidence.MethodLiveCamera)
	requirement, artefact := "selfie", string(evidence.ArtefactSelfieImage)
	if backend.providerJourney != nil && (!backend.providerJourney.model || backend.providerJourney.matching) {
		document = providerDocumentProfile(t, registry, backend.providerJourney.documentBack)
		if backend.providerJourney.smile || backend.providerJourney.matching {
			document = smileDocumentProfile(t, registry)
		}
		requirement, artefact = "document", string(evidence.ArtefactDocumentFront)
	}
	profileDocument, err := verification.CanonicalJSON(
		document,
		registry,
	)
	if err != nil {
		t.Fatalf("encode capture profile: %v", err)
	}
	var profile openapiv1.CaptureProfileMutation
	profileHeaders := performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/capture-profiles",
		Bearer: backendCredential, IdempotencyKey: "public-flow-profile-create",
		Body: openapiv1.CaptureProfileWrite{
			Name: "Public flow profile", Document: json.RawMessage(profileDocument),
		},
		WantStatus: http.StatusCreated, Result: &profile,
	})
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/capture-profiles/" + profile.ProfileID + "/publish",
		Bearer: backendCredential, IdempotencyKey: "public-flow-profile-publish",
		IfMatch: profileHeaders.Get("ETag"), WantStatus: http.StatusOK,
		Result: &profile,
	})

	var creation openapiv1.VerificationCreated
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/verifications",
		Bearer: backendCredential, IdempotencyKey: "public-flow-verification-create",
		Body: openapiv1.VerificationCreate{
			CaptureProfileID: profile.ProfileID,
			PolicyID:         policyID.String(),
		},
		WantStatus: http.StatusCreated, Result: &creation,
	})
	if creation.CaptureToken == nil || *creation.CaptureToken == "" {
		t.Fatal("verification response omitted its display-once capture token")
	}
	captureToken := *creation.CaptureToken

	now := time.Now().UTC()
	var notice openapiv1.NoticeVersion
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/notices",
		Bearer: backendCredential, IdempotencyKey: "public-flow-notice-create",
		Body: openapiv1.NoticeVersionCreate{
			Key: "tenant.notice.public_flow", Locale: "en-NG",
			Controller: "Example Controller", Recipient: "Example Recipient",
			Copy: openapiv1.NoticeCopy{
				Title: "Identity verification", Summary: "We verify identity.",
				Purpose: "Identity verification only.", Consequences: "Collection stops if refused.",
			},
			EffectiveAt: now.Add(-time.Minute),
		},
		WantStatus: http.StatusCreated, Result: &notice,
	})
	var declaration openapiv1.ProcessingAuthority
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost,
		URL:    baseURL + "/v1/verifications/" + creation.Session.ID + "/authority",
		Bearer: backendCredential, IdempotencyKey: "public-flow-authority-declare",
		Body: openapiv1.ProcessingAuthorityDeclare{
			NoticeID: notice.ID, Category: "tenant.authority.customer_declared",
			Purpose: string(evidence.PurposeIdentityVerification), Jurisdiction: "tenant.jurisdiction.ng",
			PolicyPack: "tenant.policy.identity_v1", ConsentRequired: true,
			RecipientReference: "tenant.recipient.primary", RecipientDisplayName: notice.Recipient,
			Regions: []string{"tenant.region.ng"}, RetentionReference: "tenant.retention.identity_v1",
			ValidFrom: now.Add(-time.Minute), ExpiresAt: creation.Session.ExpiresAt,
		},
		WantStatus: http.StatusCreated, Result: &declaration,
	})
	var snapshot openapiv1.CaptureAuthoritySnapshot
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodGet, URL: baseURL + "/v1/capture/authority", Bearer: captureToken,
		WantStatus: http.StatusOK, Result: &snapshot,
	})
	if snapshot.Authority.ID != declaration.ID || snapshot.Notice.ID != notice.ID || snapshot.LatestResponse != nil {
		t.Fatalf("capture authority snapshot does not preserve the declared authority and notice")
	}
	renderedVersion := "capture.notice.v1"
	var subjectResponse openapiv1.SubjectResponse
	performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/capture/authority/responses",
		Bearer: captureToken, IdempotencyKey: "public-flow-subject-consent",
		Body: openapiv1.SubjectResponseCreate{
			Action: openapiv1.SubjectResponseCreateAction("consent"), Locale: "en-NG",
			RenderedExperienceVersion: &renderedVersion,
		},
		WantStatus: http.StatusCreated, Result: &subjectResponse,
	})
	if subjectResponse.AuthorityID != declaration.ID || subjectResponse.VerificationID != creation.Session.ID {
		t.Fatal("subject response is not bound to the declared authority and verification")
	}

	body := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte("private selfie"), 8)...)
	if backend.providerJourney != nil && backend.providerJourney.model {
		body = padFixtureJPEG(t)
	}
	digest := platformcrypto.Sum(body)
	var upload openapiv1.EvidenceUpload
	uploadHeaders := performPublicJSONRequest(t, client, publicJSONRequest{
		Method: http.MethodPost, URL: baseURL + "/v1/evidence-uploads",
		Bearer: captureToken, IdempotencyKey: "public-flow-evidence-upload",
		Body: openapiv1.EvidenceUploadCreate{
			RequirementKey: requirement, Artefact: artefact,
			AcquisitionMethod: string(evidence.MethodLiveCamera), ExpectedBytes: int64(len(body)),
			ExpectedDigest: string(digest), MediaType: openapiv1.EvidenceUploadCreateMediaType("image/jpeg"),
			Region: "tenant.region.ng",
		},
		WantStatus: http.StatusCreated, Result: &upload,
	})
	uploadRequest, err := http.NewRequestWithContext(
		ctx, http.MethodPut, baseURL+"/v1/evidence-uploads/"+upload.ID, bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("construct evidence upload request: %v", err)
	}
	uploadRequest.Header.Set("Authorization", "Bearer "+captureToken)
	uploadRequest.Header.Set("Content-Type", evidence.MediaTypeJPEG)
	uploadRequest.Header.Set("Content-Digest", contentDigest(body))
	uploadRequest.Header.Set("If-Match", uploadHeaders.Get("ETag"))
	uploadResponse, err := client.Do(uploadRequest)
	if err != nil {
		t.Fatalf("perform evidence upload: %v", err)
	}
	defer func() {
		if err := uploadResponse.Body.Close(); err != nil {
			t.Errorf("close evidence upload response: %v", err)
		}
	}()
	if uploadResponse.StatusCode != http.StatusOK {
		var problem openapiv1.Problem
		_ = json.NewDecoder(uploadResponse.Body).Decode(&problem)
		t.Fatalf("evidence upload status = %d, problem code = %q", uploadResponse.StatusCode, problem.Code)
	}
	if err := json.NewDecoder(uploadResponse.Body).Decode(&upload); err != nil {
		t.Fatalf("decode accepted evidence upload: %v", err)
	}
	if upload.State != openapiv1.EvidenceUploadState("accepted") || upload.AcceptedAt == nil ||
		upload.AcquisitionMethod != string(evidence.MethodLiveCamera) {
		t.Fatalf("accepted upload state, timestamp, or acquisition method is invalid")
	}

	if backend.providerJourney != nil && (backend.providerJourney.smile || backend.providerJourney.matching) {
		uploadSmileSelfie(t, client, baseURL, captureToken, body)
	}
	if backend.providerJourney != nil && backend.providerJourney.documentBack {
		back := append(bytes.Clone(body), []byte("synthetic back side")...)
		backend.providerJourney.backPlaintext = back
		uploadProviderArtefact(t, client, baseURL, captureToken, back, "document", evidence.ArtefactDocumentBack, "dojah-back-upload")
	}
	assertPersistedEncryptedEvidence(
		t, adminPool, runtimePool, scope, registry, upload, body, keyringFile, backend.readCiphertext,
	)
	if backend.providerJourney != nil {
		digest, err := verification.Digest(document, registry)
		if err != nil {
			t.Fatal(err)
		}
		restart := func() {
			stopPublicFlowProcess(t, process)
			process = backend.start(t, database.url, runtimeRole, port, keyringFile, pepperMaterial)
			waitForPublicAPIReadiness(t, baseURL, process)
		}
		backend.providerJourney.run(t, adminPool, runtimePool, scope, creation.Session.ID, policyID.String(), digest, baseURL, backendCredential, keyringFile, restart, body)
	}
	if backend.verifyProcessing {
		assertPublicSDKAdministration(t, client, baseURL, backendCredential, upload.EvidenceID, subjectResponse.ID, creation.Session.ID)
		verificationID, err := id.ParseVerification(creation.Session.ID)
		if err != nil {
			t.Fatal(err)
		}
		assertPublicCancellation(t, client, baseURL, backendCredential, profile.ProfileID, policyID.String())
		decisionID := assertCapturedProcessing(t, adminPool, runtimePool, scope, verificationID, client, baseURL, backendCredential)
		var current openapiv1.VerificationSession
		performPublicJSONRequest(t, client, publicJSONRequest{Method: http.MethodGet, URL: baseURL + "/v1/verifications/" + verificationID.String(), Bearer: backendCredential, WantStatus: http.StatusOK, Result: &current})
		if current.State != openapiv1.VerificationSessionStateCompleted || current.Version != 3 {
			t.Fatalf("public completed session = %#v", current)
		}
		var report openapiv1.PolicyDecisionReport
		performPublicJSONRequest(t, client, publicJSONRequest{Method: http.MethodGet, URL: baseURL + "/v1/decisions/" + decisionID.String(), Bearer: backendCredential, WantStatus: http.StatusOK, Result: &report})
		assertPublicSDKDecisionAndConsent(t, client, baseURL, backendCredential, verificationID.String(), decisionID.String(), subjectResponse.ID)
	}
	if backend.verifyProcessing {
		assertPublicWebhookManagement(t, client, baseURL, backendCredential, keyringFile, adminPool, runtimePool, scope, peppers)
	}

}

func newFullScopeIntegrationCredential(
	t *testing.T,
	generator *id.Generator,
	tenantID id.Tenant,
	createdAt time.Time,
	peppers *access.PepperSet,
) (access.Key, access.PresentedKey) {
	t.Helper()
	identifier, err := generator.NewAPIKey()
	if err != nil {
		t.Fatalf("new API key identifier: %v", err)
	}
	secretGenerator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0x41}, 32)))
	if err != nil {
		t.Fatalf("new API key secret generator: %v", err)
	}
	presented, err := secretGenerator.Generate(tenantID, identifier)
	if err != nil {
		t.Fatalf("generate API key: %v", err)
	}
	digest, version, err := peppers.Digest(presented)
	if err != nil {
		t.Fatalf("digest API key: %v", err)
	}
	pattern, err := access.ParsePattern("*:*")
	if err != nil {
		t.Fatalf("parse full tenant scope: %v", err)
	}
	grant, err := access.TenantRegistry().Resolve(pattern)
	if err != nil {
		t.Fatalf("resolve full tenant scope: %v", err)
	}
	key, err := access.RestoreKey(access.KeyRecord{
		ID: identifier, TenantID: tenantID, Label: "public flow integration",
		Digest: digest, PepperVersion: version, Grant: grant, Version: 1,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	})
	if err != nil {
		t.Fatalf("restore API key: %v", err)
	}

	return key, presented
}

func loadPublicFlowConfiguration(
	t *testing.T,
	databaseURL string,
	runtimeRole string,
	port int,
	objectDirectory string,
	keyringFile string,
	pepperMaterial []byte,
) config.API {
	t.Helper()
	setPublicFlowCoreEnvironment(t, databaseURL, runtimeRole, port, keyringFile, pepperMaterial)
	t.Setenv("IDENQA_EVIDENCE_LOCAL_DIRECTORY", objectDirectory)
	configuration, err := config.LoadAPI("")
	if err != nil {
		t.Fatalf("load public flow API configuration: %v", err)
	}

	return configuration
}

func setPublicFlowCoreEnvironment(
	t *testing.T,
	databaseURL string,
	runtimeRole string,
	port int,
	keyringFile string,
	pepperMaterial []byte,
) {
	t.Helper()
	clearIntegrationIDENQAEnvironment(t)
	pepperSecret := base64.RawURLEncoding.EncodeToString(pepperMaterial)
	cursorSecret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x32}, 32))
	captureTokenSecret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x33}, 32))
	outcomeTokenSecret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x34}, 32))
	values := map[string]string{
		"IDENQA_ENVIRONMENT":                      "test",
		"IDENQA_HEADGATE_INSTALLATION_ID":         "idenqa-test",
		"IDENQA_DATABASE_URL":                     databaseURL,
		"IDENQA_DATABASE_ROLE":                    runtimeRole,
		"IDENQA_API_KEY_ACTIVE_PEPPER_VERSION":    "1",
		"IDENQA_API_KEY_PEPPERS":                  "1=" + pepperSecret,
		"IDENQA_CURSOR_ACTIVE_KEY_VERSION":        "1",
		"IDENQA_CURSOR_KEYS":                      "1=" + cursorSecret,
		"IDENQA_CAPTURE_TOKEN_ACTIVE_KEY_VERSION": "1",
		"IDENQA_CAPTURE_TOKEN_KEYS":               "1=" + captureTokenSecret,
		"IDENQA_OUTCOME_TOKEN_ACTIVE_KEY_VERSION": "1",
		"IDENQA_OUTCOME_TOKEN_KEYS":               "1=" + outcomeTokenSecret,
		"IDENQA_REGION":                           "local",
		"IDENQA_REALTIME_WEBSOCKET_URL":           "ws://127.0.0.1:" + strconv.Itoa(port) + "/v1/capture/socket",
		"IDENQA_HTTP_CORS_ALLOWED_ORIGINS":        "http://localhost:3000",
		"IDENQA_HTTP_HOST":                        "127.0.0.1",
		"IDENQA_HTTP_PORT":                        strconv.Itoa(port),
		"IDENQA_HTTP_REQUEST_TIMEOUT":             "5s",
		"IDENQA_SHUTDOWN_TIMEOUT":                 "5s",
		"IDENQA_EVIDENCE_LOCAL_KEYRING_FILE":      keyringFile,
	}
	for key, value := range values {
		t.Setenv(key, value)
	}
}

func clearIntegrationIDENQAEnvironment(t *testing.T) {
	t.Helper()
	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if !found || !strings.HasPrefix(key, "IDENQA_") {
			continue
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if err := os.Setenv(key, value); err != nil {
				t.Errorf("restore %s: %v", key, err)
			}
		})
	}
}

func reserveLoopbackPort(t *testing.T) int {
	t.Helper()
	var listenConfig net.ListenConfig
	listener, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve loopback port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release loopback port: %v", err)
	}

	return port
}

func newS3PublicFlowFixture(t *testing.T, bucket string) *httptest.Server {
	t.Helper()
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("open S3 fixture storage root: %v", err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("close S3 fixture storage root: %v", err)
		}
	})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ownedPrefix := "/" + bucket + "/"
		if !strings.HasPrefix(request.URL.Path, ownedPrefix) {
			http.NotFound(writer, request)

			return
		}
		key := strings.TrimPrefix(request.URL.Path, ownedPrefix)
		if _, err := objectstore.NewKey(key); err != nil {
			http.Error(writer, "invalid object key", http.StatusBadRequest)

			return
		}
		name := filepath.FromSlash(key)
		if !filepath.IsLocal(name) {
			http.Error(writer, "invalid object key", http.StatusBadRequest)

			return
		}
		switch request.Method {
		case http.MethodPut:
			if !strings.HasPrefix(
				request.Header.Get("Authorization"),
				"AWS4-HMAC-SHA256 Credential=idenqa-integration-fixture/",
			) || request.Header.Get("X-Amz-Date") == "" ||
				request.Header.Get("X-Amz-Content-Sha256") != "UNSIGNED-PAYLOAD" {
				http.Error(writer, "signed request required", http.StatusForbidden)

				return
			}
			if request.Header.Get("If-None-Match") != "*" {
				http.Error(writer, "conditional creation required", http.StatusBadRequest)

				return
			}
			if err := root.MkdirAll(filepath.Dir(name), 0o700); err != nil {
				http.Error(writer, "object storage unavailable", http.StatusInternalServerError)

				return
			}
			file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if errors.Is(err, os.ErrExist) {
				http.Error(writer, "object exists", http.StatusPreconditionFailed)

				return
			}
			if err != nil {
				http.Error(writer, "object storage unavailable", http.StatusInternalServerError)

				return
			}
			bounded := http.MaxBytesReader(
				writer, request.Body, 2*evidence.MaximumUploadMaximumBytes,
			)
			_, writeErr := io.Copy(file, bounded)
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				_ = root.Remove(name)
				http.Error(writer, "object storage unavailable", http.StatusInternalServerError)

				return
			}
			writer.Header().Set("ETag", strconv.Quote("idenqa-integration-ciphertext"))
			writer.WriteHeader(http.StatusOK)
		case http.MethodGet:
			file, err := root.Open(name)
			if errors.Is(err, os.ErrNotExist) {
				http.NotFound(writer, request)

				return
			}
			if err != nil {
				http.Error(writer, "object storage unavailable", http.StatusInternalServerError)

				return
			}
			defer func() { _ = file.Close() }()
			if _, err := io.Copy(writer, file); err != nil {
				return
			}
		default:
			writer.Header().Set("Allow", http.MethodPut+", "+http.MethodGet)
			http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(server.Close)

	return server
}

func startPublicFlowProcess(
	stop func() error,
	forceStop func() error,
	run func() error,
) publicFlowProcess {
	done := make(chan struct{})
	var runErr error
	go func() {
		runErr = run()
		close(done)
	}()

	return publicFlowProcess{
		stop: stop, forceStop: forceStop, done: done,
		waitError: func() error { return runErr },
	}
}

func stopPublicFlowProcess(t *testing.T, process publicFlowProcess) {
	t.Helper()
	if err := process.stop(); err != nil {
		t.Errorf("stop public API process: %v", err)
	}
	select {
	case <-process.done:
		if err := process.waitError(); err != nil {
			t.Errorf("run public API process: %v", err)
		}
	case <-time.After(10 * time.Second):
		if err := process.forceStop(); err != nil {
			t.Errorf("force-stop public API process: %v", err)
		}
		select {
		case <-process.done:
		case <-time.After(5 * time.Second):
			t.Error("public API process did not stop after being force-stopped")
		}
	}
}

func waitForPublicAPIReadiness(t *testing.T, baseURL string, process publicFlowProcess) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	client := &http.Client{Timeout: 250 * time.Millisecond}
	for {
		select {
		case <-process.done:
			t.Fatalf("public API stopped before readiness: %v", process.waitError())
		default:
		}
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, baseURL+"/readyz", nil)
		if err != nil {
			t.Fatalf("construct public API readiness request: %v", err)
		}
		response, err := client.Do(request)
		if err == nil {
			closeErr := response.Body.Close()
			if response.StatusCode == http.StatusOK && closeErr == nil {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("public API did not become ready within ten seconds")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readAndCloseCiphertext(t *testing.T, reader io.ReadCloser) []byte {
	t.Helper()
	value, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatalf("read persisted ciphertext: %v", err)
	}

	return value
}

type publicJSONRequest struct {
	Method         string
	URL            string
	Bearer         string
	IdempotencyKey string
	IfMatch        string
	Body           any
	WantStatus     int
	Result         any
}

func performPublicJSONRequest(
	t *testing.T,
	client *http.Client,
	input publicJSONRequest,
) http.Header {
	t.Helper()
	var body io.Reader
	if input.Body != nil {
		encoded, err := json.Marshal(input.Body)
		if err != nil {
			t.Fatalf("encode public API request: %v", err)
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(t.Context(), input.Method, input.URL, body)
	if err != nil {
		t.Fatalf("construct public API request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer "+input.Bearer)
	if input.Body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if input.IdempotencyKey != "" {
		request.Header.Set("Idempotency-Key", strconv.Quote(input.IdempotencyKey))
	}
	if input.IfMatch != "" {
		request.Header.Set("If-Match", input.IfMatch)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("perform public API request: %v", err)
	}
	defer func() {
		if err := response.Body.Close(); err != nil {
			t.Errorf("close public API response: %v", err)
		}
	}()
	if response.StatusCode != input.WantStatus {
		var problem openapiv1.Problem
		_ = json.NewDecoder(response.Body).Decode(&problem)
		t.Fatalf("public API status = %d, want %d, problem code = %q", response.StatusCode, input.WantStatus, problem.Code)
	}
	if input.Result != nil {
		if err := json.NewDecoder(response.Body).Decode(input.Result); err != nil {
			t.Fatalf("decode public API response: %v", err)
		}
	}

	return response.Header.Clone()
}

func contentDigest(body []byte) string {
	digest := sha256.Sum256(body)

	return "sha-256=:" + base64.StdEncoding.EncodeToString(digest[:]) + ":"
}

func assertPersistedEncryptedEvidence(
	t *testing.T,
	adminPool *idenqapostgres.Pool,
	runtimePool *idenqapostgres.Pool,
	scope tenant.Scope,
	registry evidence.Registry,
	upload openapiv1.EvidenceUpload,
	plaintext []byte,
	keyringFile string,
	readCiphertext func(*testing.T, objectstore.Object) []byte,
) {
	t.Helper()
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatalf("new evidence catalog: %v", err)
	}
	store, err := evidencepostgres.New(runtimePool, integrationProtector{}, catalog)
	if err != nil {
		t.Fatalf("new evidence persistence: %v", err)
	}
	evidenceID, err := id.ParseEvidence(upload.EvidenceID)
	if err != nil {
		t.Fatalf("parse accepted evidence identifier: %v", err)
	}
	asset, err := store.Find(t.Context(), scope, evidenceID)
	if err != nil {
		t.Fatalf("find accepted evidence: %v", err)
	}
	if asset.Record().AcquisitionMethod != evidence.MethodLiveCamera || !asset.CanRead() {
		t.Fatal("accepted evidence lost its acquisition method or readable state")
	}
	object := asset.Content().Object()
	ciphertextBytes := readCiphertext(t, object)
	objectRecord := object.Record()
	if int64(len(ciphertextBytes)) != objectRecord.Size ||
		string(platformcrypto.Sum(ciphertextBytes)) != objectRecord.Checksum {
		t.Fatal("independently read ciphertext differs from its durable exact reference")
	}
	if bytes.Equal(ciphertextBytes, plaintext) || bytes.Contains(ciphertextBytes, plaintext) {
		t.Fatal("object storage contains recoverable raw evidence bytes")
	}

	keys, err := localkms.Open(keyringFile)
	if err != nil {
		t.Fatalf("open mounted keyring for independent verification: %v", err)
	}
	defer func() {
		if err := keys.Close(); err != nil {
			t.Errorf("close independent evidence keyring: %v", err)
		}
	}()
	purpose, err := kms.NewPurpose(evidence.ContentEncryptionPurpose)
	if err != nil {
		t.Fatalf("construct evidence encryption purpose: %v", err)
	}
	streaming, err := tinkcrypto.NewStreaming(keys, keys, purpose)
	if err != nil {
		t.Fatalf("construct independent evidence decryptor: %v", err)
	}
	var opened bytes.Buffer
	authenticatedContext, err := evidence.AuthenticatedContext(asset.Record())
	if err != nil {
		t.Fatalf("construct evidence authenticated context: %v", err)
	}
	if err := streaming.Open(
		t.Context(), &opened, bytes.NewReader(ciphertextBytes),
		asset.Content().Envelope(), authenticatedContext,
	); err != nil {
		t.Fatalf("decrypt persisted evidence: %v", err)
	}
	if !bytes.Equal(opened.Bytes(), plaintext) {
		t.Fatal("decrypted evidence does not equal the exact uploaded bytes")
	}

	var acceptedUploads, availableEvidence, retainedObjects, readyEvents int
	err = adminPool.WithinTransaction(
		t.Context(), idenqapostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			queries := []struct {
				statement string
				argument  string
				target    *int
			}{
				{statement: "SELECT count(*) FROM idenqa.evidence_upload_intents WHERE id = $1 AND state = 'accepted'", argument: upload.ID, target: &acceptedUploads},
				{statement: "SELECT count(*) FROM idenqa.evidence_assets WHERE id = $1 AND state = 'available'", argument: upload.EvidenceID, target: &availableEvidence},
				{statement: "SELECT count(*) FROM idenqa.evidence_object_reconciliations WHERE upload_id = $1 AND state = 'retained'", argument: upload.ID, target: &retainedObjects},
				{statement: "SELECT count(*) FROM idenqa.outbox_events WHERE aggregate_id = $1 AND event_type = 'evidence.ready.v1'", argument: upload.EvidenceID, target: &readyEvents},
			}
			for _, query := range queries {
				if err := tx.QueryRow(ctx, query.statement, query.argument).Scan(query.target); err != nil {
					return fmt.Errorf("inspect evidence transaction result: %w", err)
				}
			}

			return nil
		},
	)
	if err != nil {
		t.Fatalf("inspect evidence transaction: %v", err)
	}
	if acceptedUploads != 1 || availableEvidence != 1 || retainedObjects != 1 || readyEvents != 1 {
		t.Fatalf(
			"accepted upload, evidence, retained object, and ready event counts = %d/%d/%d/%d",
			acceptedUploads, availableEvidence, retainedObjects, readyEvents,
		)
	}
}
