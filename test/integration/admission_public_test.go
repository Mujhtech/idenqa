//go:build integration

package integration_test

import (
	"bytes"
	"cmp"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
	"github.com/Mujhtech/idenqa/internal/admission"
	admissionpg "github.com/Mujhtech/idenqa/internal/admission/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/admissionbridge"
)

type admissionJourney struct {
	runtimeFile     string
	key             ed25519.PrivateKey
	snapshot        admissionv1.Snapshot
	control         *admissionpg.Store
	input           openapiv1.VerificationCreate
	cloudRepository string
	providerID      string
}

func TestOperationalAdmissionPublicCaptureContinuesAfterRevocation(t *testing.T) {
	runAdmissionPublicJourney(t, "")
}

func runAdmissionPublicJourney(t *testing.T, cloudRepository string) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{46}, ed25519.SeedSize))
	config := map[string]any{"deploymentId": "dep_public_fixture", "region": "local", "maximumLifetime": "30m", "issuers": map[string]string{"fixture": base64.StdEncoding.EncodeToString(key.Public().(ed25519.PublicKey))}}
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "admission.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	runProviderPublicJourneyWithOptions(t, &providerPublicJourney{admission: &admissionJourney{runtimeFile: path, key: key, cloudRepository: cloudRepository}})
}

func (a *admissionJourney) prepare(t *testing.T, admin *pg.Pool, scope tenant.Scope, profile openapiv1.CaptureProfileMutation, policyID id.Policy, client *http.Client, base, credential string) {
	t.Helper()
	a.input = openapiv1.VerificationCreate{CaptureProfileID: profile.ProfileID, PolicyID: policyID.String()}
	var problem openapiv1.Problem
	performPublicJSONRequest(t, client, publicJSONRequest{Method: http.MethodPost, URL: base + "/v1/verifications", Bearer: credential, IdempotencyKey: "missing-policy", Body: a.input, WantStatus: http.StatusServiceUnavailable, Result: &problem})
	if string(problem.Code) != "ADMISSION_UNAVAILABLE" {
		t.Fatalf("missing policy public code=%s", problem.Code)
	}
	policy, err := admission.NewPolicy(admission.Config{DeploymentID: "dep_public_fixture", Region: "local", MaximumLifetime: 30 * time.Minute, Issuers: map[string]ed25519.PublicKey{"fixture": a.key.Public().(ed25519.PublicKey)}})
	if err != nil {
		t.Fatal(err)
	}
	a.control, err = admissionpg.New(admin, policy)
	if err != nil {
		t.Fatal(err)
	}
	operation, err := admissionv1.VerificationOperation(profile.ProfileID, 1, policyID.String())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	a.snapshot = admissionv1.Snapshot{Schema: admissionv1.Schema, ID: "public_snapshot_1", Scope: admissionv1.Scope{TenantID: scope.ID().String(), DeploymentID: "dep_public_fixture", Region: "local"}, Revision: 1, Issuer: "fixture", IssuedAt: now, ValidFrom: now, ValidUntil: now.Add(10 * time.Minute), Status: "active", Capabilities: map[string]int64{"fixture": 1}, Limits: []admissionv1.Limit{{Kind: "concurrency", Key: "work", Maximum: 1}, {Kind: "usage", Key: "work", Maximum: 1, Window: time.Hour}}, Rules: []admissionv1.Rule{{Operation: operation, Capabilities: map[string]int64{"fixture": 1}, Budgets: []admissionv1.Budget{{Kind: "concurrency", Key: "work", Quantity: 1, Lifetime: "terminal"}, {Kind: "usage", Key: "work", Quantity: 1, Lifetime: "window"}}}}}
	description := dojah.Description()
	providerOperation, err := admissionv1.ResourceOperation("provider", a.providerID, description.Package.AdapterVersion, description.Package.PackageDigest, "idenqa.check.document_analysis")
	if err != nil {
		t.Fatal(err)
	}
	evidenceOperation, err := admissionv1.ResourceOperation("evidence", string(evidence.EvidenceDocumentImage), string(evidence.ArtefactDocumentFront), string(evidence.MethodLiveCamera), evidence.MediaTypeJPEG, "tenant.retention.identity_v1")
	if err != nil {
		t.Fatal(err)
	}
	providerOperation, err = admissionv1.DependentOperation(operation, providerOperation)
	if err != nil {
		t.Fatal(err)
	}
	evidenceOperation, err = admissionv1.DependentOperation(operation, evidenceOperation)
	if err != nil {
		t.Fatal(err)
	}
	a.snapshot.Limits = append(a.snapshot.Limits, admissionv1.Limit{Kind: "concurrency", Key: "provider", Maximum: 1}, admissionv1.Limit{Kind: "storage", Key: "evidence", Maximum: 8 << 20})
	a.snapshot.Rules = append(a.snapshot.Rules,
		admissionv1.Rule{Operation: providerOperation, Capabilities: map[string]int64{"fixture": 1}, Budgets: []admissionv1.Budget{{Kind: "concurrency", Key: "provider", Quantity: 1, Lifetime: "terminal"}}},
		admissionv1.Rule{Operation: evidenceOperation, Capabilities: map[string]int64{"fixture": 1}, Budgets: []admissionv1.Budget{{Kind: "storage", Key: "evidence", Quantity: 8 << 20, Lifetime: "delete"}}},
	)
	slices.SortFunc(a.snapshot.Limits, func(a, b admissionv1.Limit) int { return cmp.Compare(a.Kind+":"+a.Key, b.Kind+":"+b.Key) })
	slices.SortFunc(a.snapshot.Rules, func(a, b admissionv1.Rule) int { return cmp.Compare(a.Operation, b.Operation) })
	a.snapshot.Proof = ed25519.Sign(a.key, a.snapshot.SigningBytes())
	handler, err := admissionbridge.NewHandler(a.control)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(a.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, path, body, origin, cookie string
		status                           int
	}{
		{name: "unknown field", path: "/local/v1/admission/snapshots/install", body: `{"unknown":true}`, status: http.StatusBadRequest},
		{name: "trailing JSON", path: "/local/v1/admission/snapshots/install", body: string(raw) + `{}`, status: http.StatusBadRequest},
		{name: "oversized", path: "/local/v1/admission/snapshots/install", body: strings.Repeat(" ", 200<<10) + string(raw), status: http.StatusBadRequest},
		{name: "browser origin", path: "/local/v1/admission/snapshots/install", body: string(raw), origin: "https://tenant.example", status: http.StatusBadRequest},
		{name: "browser cookie", path: "/local/v1/admission/snapshots/install", body: string(raw), cookie: "session=blocked", status: http.StatusBadRequest},
		{name: "query", path: "/local/v1/admission/snapshots/install?policy=blocked", body: string(raw), status: http.StatusBadRequest},
		{name: "committed", path: "/local/v1/admission/snapshots/install", body: string(raw), status: http.StatusOK},
		{name: "lost acknowledgement", path: "/local/v1/admission/snapshots/install", body: string(raw), status: http.StatusOK},
	} {
		t.Run(test.name, func(t *testing.T) {
			if a.cloudRepository != "" && test.status == http.StatusOK {
				t.Skip("the companion conformance runner installs through its durable queue")
			}
			request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, test.path, strings.NewReader(test.body))
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Cookie", test.cookie)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("install status=%d want=%d", response.Code, test.status)
			}
		})
	}
	if a.cloudRepository != "" {
		a.deliverThroughCloud(t, a.snapshot)
	}
	// Authentication remains required even with an active signed entitlement.
	performPublicJSONRequest(t, client, publicJSONRequest{Method: http.MethodPost, URL: base + "/v1/verifications", IdempotencyKey: "no-principal", Body: a.input, WantStatus: http.StatusUnauthorized})
}

func (a *admissionJourney) revoke(t *testing.T, client *http.Client, base, credential string, created openapiv1.VerificationCreated) {
	t.Helper()
	revoked := a.snapshot
	revoked.ID = "public_snapshot_2"
	revoked.Revision = 2
	revoked.Status = "revoked"
	revoked.Proof = ed25519.Sign(a.key, revoked.SigningBytes())
	if a.cloudRepository != "" {
		a.deliverThroughCloud(t, revoked)
	} else {
		if err := a.control.Install(t.Context(), revoked); err != nil {
			t.Fatal(err)
		}
	}
	var replay openapiv1.VerificationCreated
	performPublicJSONRequest(t, client, publicJSONRequest{Method: http.MethodPost, URL: base + "/v1/verifications", Bearer: credential, IdempotencyKey: "public-flow-verification-create", Body: a.input, WantStatus: http.StatusCreated, Result: &replay})
	if replay.Session.ID != created.Session.ID {
		t.Fatal("accepted replay changed after revocation")
	}
	var problem openapiv1.Problem
	performPublicJSONRequest(t, client, publicJSONRequest{Method: http.MethodPost, URL: base + "/v1/verifications", Bearer: credential, IdempotencyKey: "revoked-new-work", Body: a.input, WantStatus: http.StatusForbidden, Result: &problem})
	if string(problem.Code) != "CAPABILITY_NOT_ENTITLED" {
		t.Fatalf("revoked public code=%s", problem.Code)
	}
}

func (a *admissionJourney) assertCompleted(t *testing.T, admin *pg.Pool, verificationID string) {
	t.Helper()
	var state string
	var concurrency, usage int64
	if err := admin.Native().QueryRow(t.Context(), `SELECT o.state,(SELECT used FROM idenqa.admission_counters WHERE tenant_id=o.tenant_id AND kind='concurrency' AND key='work'),(SELECT used FROM idenqa.admission_counters WHERE tenant_id=o.tenant_id AND kind='usage') FROM idenqa.admission_operations o WHERE id=$1`, verificationID).Scan(&state, &concurrency, &usage); err != nil || state != "terminal" || concurrency != 0 || usage != 1 {
		t.Fatalf("completed admission state=%s active=%d usage=%d: %v", state, concurrency, usage, err)
	}
	var children int
	if err := admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.admission_resource_sources WHERE verification_id=$1`, verificationID).Scan(&children); err != nil || children != 2 {
		t.Fatalf("actual dependent admissions=%d: %v", children, err)
	}
	var providerUsed, storageUsed int64
	if err := admin.Native().QueryRow(t.Context(), `SELECT (SELECT used FROM idenqa.admission_counters WHERE kind='concurrency' AND key='provider'),(SELECT used FROM idenqa.admission_counters WHERE kind='storage' AND key='evidence')`).Scan(&providerUsed, &storageUsed); err != nil || providerUsed != 0 || storageUsed != 8<<20 {
		t.Fatalf("dependent lifetime provider=%d evidence=%d: %v", providerUsed, storageUsed, err)
	}
}
