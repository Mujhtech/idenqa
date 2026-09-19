package idenqa_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	bootstrap "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type syntheticRecordedRequest struct {
	step           string
	method         string
	path           string
	authorization  string
	idempotencyKey string
	ifMatch        string
	contentDigest  string
	body           []byte
}

type syntheticFixture struct {
	t             *testing.T
	mu            sync.Mutex
	requests      []syntheticRecordedRequest
	failStep      string
	neverComplete bool
	completeAfter int
	polls         int
}

func newSyntheticFixture(t *testing.T) (*syntheticFixture, *httptest.Server) {
	t.Helper()
	fixture := &syntheticFixture{t: t, completeAfter: 1}
	server := httptest.NewServer(http.HandlerFunc(fixture.handle))
	t.Cleanup(server.Close)
	return fixture, server
}

func syntheticRequestStep(method, path string) string {
	switch {
	case method == http.MethodPost && path == "/v1/capture-profiles":
		return "profile.create"
	case method == http.MethodPost && strings.HasPrefix(path, "/v1/capture-profiles/") && strings.HasSuffix(path, "/publish"):
		return "profile.publish"
	case method == http.MethodPost && path == "/v1/policies":
		return "policy.create"
	case method == http.MethodPost && strings.HasPrefix(path, "/v1/policies/") && strings.HasSuffix(path, "/activate"):
		return "policy.activate"
	case method == http.MethodPost && path == "/v1/verifications":
		return "verification.create"
	case method == http.MethodPost && path == "/v1/notices":
		return "notice.create"
	case method == http.MethodPost && strings.HasPrefix(path, "/v1/verifications/") && strings.HasSuffix(path, "/authority"):
		return "authority.declare"
	case method == http.MethodGet && path == "/v1/capture/authority":
		return "authority.snapshot"
	case method == http.MethodPost && path == "/v1/capture/authority/responses":
		return "authority.consent"
	case method == http.MethodPost && path == "/v1/evidence-uploads":
		return "evidence.issue"
	case method == http.MethodPut && strings.HasPrefix(path, "/v1/evidence-uploads/"):
		return "evidence.upload"
	case method == http.MethodGet && path == "/v1/capture/progress":
		return "capture.progress"
	case method == http.MethodGet && strings.HasPrefix(path, "/v1/verifications/"):
		return "verification.await"
	case method == http.MethodGet && strings.HasPrefix(path, "/v1/decisions/"):
		return "decision.read"
	default:
		return ""
	}
}

func (fixture *syntheticFixture) handle(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		fixture.t.Errorf("read synthetic request: %v", err)
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	step := syntheticRequestStep(request.Method, request.URL.Path)
	fixture.mu.Lock()
	fixture.requests = append(fixture.requests, syntheticRecordedRequest{
		step: step, method: request.Method, path: request.URL.Path,
		authorization:  request.Header.Get("Authorization"),
		idempotencyKey: request.Header.Get("Idempotency-Key"),
		ifMatch:        request.Header.Get("If-Match"),
		contentDigest:  request.Header.Get("Content-Digest"),
		body:           append([]byte(nil), body...),
	})
	fail := fixture.failStep != "" && fixture.failStep == step
	fixture.mu.Unlock()
	if step == "" {
		fixture.t.Errorf("unexpected synthetic request %s %s", request.Method, request.URL.Path)
		writeSyntheticJSON(writer, http.StatusNotFound, map[string]any{"code": "not_found"})
		return
	}
	if fail {
		writeSyntheticJSON(writer, http.StatusInternalServerError, map[string]any{
			"type": "about:blank", "title": "Internal error", "status": 500,
			"detail": "injected step failure", "code": "internal_error",
		})
		return
	}
	switch step {
	case "profile.create":
		writer.Header().Set("ETag", `"1"`)
		writeSyntheticJSON(writer, http.StatusCreated, map[string]any{
			"profile_id": "cpp_01ARZ3NDEKTSV4RRFFQ69G5FAV", "name": "Synthetic selfie verification",
			"state": "draft", "version": 1, "latest_revision": 1, "revision": 1,
			"digest": "sha256:fixture", "updated_at": "2030-01-01T00:00:00Z",
		})
	case "profile.publish":
		writeSyntheticJSON(writer, http.StatusOK, map[string]any{
			"profile_id": "cpp_01ARZ3NDEKTSV4RRFFQ69G5FAV", "name": "Synthetic selfie verification",
			"state": "active", "version": 2, "latest_revision": 1, "published_revision": 1,
			"revision": 1, "digest": "sha256:fixture", "updated_at": "2030-01-01T00:00:01Z",
		})
	case "policy.create":
		writeSyntheticJSON(writer, http.StatusOK, map[string]any{
			"policy": map[string]any{
				"id": "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV", "latest_revision": 1,
				"activation_version": 0, "created_at": "2030-01-01T00:00:00Z",
			},
			"revision": map[string]any{
				"policy_id": "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV", "revision": 1,
				"schema_major": 1, "schema_minor": 0, "digest": "sha256:fixture",
				"evaluator_major": 1, "evaluator_minor": 0, "evaluator_digest": "sha256:fixture",
				"created_at": "2030-01-01T00:00:00Z",
			},
			"replayed": false,
		})
	case "policy.activate":
		writeSyntheticJSON(writer, http.StatusOK, map[string]any{
			"policy": map[string]any{
				"id": "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV", "latest_revision": 1,
				"active_revision": 1, "activation_version": 1,
				"created_at": "2030-01-01T00:00:00Z", "activated_at": "2030-01-01T00:00:01Z",
			},
			"activation": map[string]any{
				"policy_id": "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV", "revision": 1,
				"previous_revision": 0, "version": 1, "actor_id": "key_fixture",
				"activated_at": "2030-01-01T00:00:01Z",
			},
			"replayed": false,
		})
	case "verification.create":
		writeSyntheticJSON(writer, http.StatusCreated, map[string]any{ //nolint:gosec // deterministic test fixture identifiers
			"session": map[string]any{
				"id": "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV", "state": "collecting", "version": 1,
				"created_at": "2030-01-01T00:00:00Z", "updated_at": "2030-01-01T00:00:00Z",
				"expires_at": "2030-01-02T00:00:00Z", "profile_id": "cpp_01ARZ3NDEKTSV4RRFFQ69G5FAV",
				"policy_id": "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV", "region": "tenant-local",
				"profile_revision": 1, "profile_digest": "sha256:fixture", "requirements": map[string]any{},
			},
			"capture_token":            "ct_capture_token_fixture",
			"outcome_token":            "ot_outcome_token_fixture",
			"outcome_token_expires_at": "2030-01-03T00:00:00Z",
		})
	case "notice.create":
		writeSyntheticJSON(writer, http.StatusCreated, map[string]any{
			"id": "ntc_01ARZ3NDEKTSV4RRFFQ69G5FAV", "key": "tenant.notice.synthetic_demo",
			"locale": "en-NG", "controller": "Idenqa synthetic demonstration",
			"recipient": "Synthetic subject", "digest": "sha256:fixture",
			"effective_at": "2030-01-01T00:00:00Z", "created_at": "2030-01-01T00:00:00Z",
		})
	case "authority.declare":
		writeSyntheticJSON(writer, http.StatusCreated, map[string]any{
			"id": "auth_01ARZ3NDEKTSV4RRFFQ69G5FAV", "notice_id": "ntc_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"verification_id": "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV", "state": "active", "version": 1,
		})
	case "authority.snapshot":
		writeSyntheticJSON(writer, http.StatusOK, map[string]any{
			"authority": map[string]any{
				"id": "auth_01ARZ3NDEKTSV4RRFFQ69G5FAV", "notice_id": "ntc_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			},
			"notice": map[string]any{
				"id": "ntc_01ARZ3NDEKTSV4RRFFQ69G5FAV", "recipient": "Synthetic subject",
			},
		})
	case "authority.consent":
		writeSyntheticJSON(writer, http.StatusCreated, map[string]any{
			"id": "res_01ARZ3NDEKTSV4RRFFQ69G5FAV", "action": "consent",
			"authority_id": "auth_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"notice_id":    "ntc_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"subject_id":   "sub_fixture", "verification_id": "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"locale": "en-NG", "recorded_at": "2030-01-01T00:00:02Z",
		})
	case "evidence.issue":
		writer.Header().Set("ETag", `"1"`)
		writeSyntheticJSON(writer, http.StatusCreated, map[string]any{
			"id": "upl_01ARZ3NDEKTSV4RRFFQ69G5FAV", "evidence_id": "evd_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"state": "issued", "version": 1, "attempt": 1, "requirement_key": "selfie",
			"evidence_type": "idenqa.evidence.selfie_image", "artefact": "idenqa.artefact.selfie_image",
			"acquisition_method": "idenqa.method.live_camera", "assurances": []string{},
			"allowed_media_types": []string{"image/jpeg"}, "maximum_bytes": 16777216,
			"expected_bytes": 176, "media_type": "image/jpeg", "region": "tenant.region.ng",
			"created_at": "2030-01-01T00:00:03Z", "updated_at": "2030-01-01T00:00:03Z",
			"expires_at": "2030-01-01T00:15:00Z",
		})
	case "evidence.upload":
		writeSyntheticJSON(writer, http.StatusOK, map[string]any{
			"id": "upl_01ARZ3NDEKTSV4RRFFQ69G5FAV", "evidence_id": "evd_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"state": "accepted", "version": 2, "attempt": 1, "requirement_key": "selfie",
			"evidence_type": "idenqa.evidence.selfie_image", "artefact": "idenqa.artefact.selfie_image",
			"acquisition_method": "idenqa.method.live_camera", "assurances": []string{},
			"allowed_media_types": []string{"image/jpeg"}, "maximum_bytes": 16777216,
			"expected_bytes": 176, "media_type": "image/jpeg", "region": "tenant.region.ng",
			"created_at": "2030-01-01T00:00:03Z", "updated_at": "2030-01-01T00:00:04Z",
			"expires_at": "2030-01-01T00:15:00Z", "accepted_at": "2030-01-01T00:00:04Z",
		})
	case "capture.progress":
		writeSyntheticJSON(writer, http.StatusOK, map[string]any{
			"verification_id": "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"completions": []map[string]any{{
				"upload_id": "upl_01ARZ3NDEKTSV4RRFFQ69G5FAV", "evidence_id": "evd_01ARZ3NDEKTSV4RRFFQ69G5FAV",
				"requirement_key": "selfie", "evidence_type": "idenqa.evidence.selfie_image",
				"artefact": "idenqa.artefact.selfie_image", "acquisition_method": "idenqa.method.live_camera",
			}},
		})
	case "verification.await":
		fixture.mu.Lock()
		fixture.polls++
		completed := !fixture.neverComplete && fixture.polls >= fixture.completeAfter
		fixture.mu.Unlock()
		session := map[string]any{
			"id": "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV", "state": "processing", "version": 2,
			"expires_at": "2030-01-02T00:00:00Z",
		}
		if completed {
			session["state"] = "completed"
			session["version"] = 3
			session["current_decision"] = map[string]any{
				"decision_id": "dec_01ARZ3NDEKTSV4RRFFQ69G5FAV",
				"outcome":     "satisfied", "directive": "complete_verified",
				"decided_at": "2030-01-01T00:00:05Z",
			}
		}
		writeSyntheticJSON(writer, http.StatusOK, session)
	case "decision.read":
		writeSyntheticJSON(writer, http.StatusOK, map[string]any{
			"decision_id":     "dec_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"verification_id": "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"policy_id":       "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV",
			"outcome":         "satisfied", "directive": "complete_verified",
			"assurance": "synthetic.fixture", "schema_major": 1, "schema_minor": 0,
			"policy_revision": 1, "decided_at": "2030-01-01T00:00:05Z",
			"evaluated_at": "2030-01-01T00:00:05Z", "reproduced": true,
		})
	default:
		fixture.t.Errorf("unhandled synthetic step %q", step)
		writeSyntheticJSON(writer, http.StatusInternalServerError, map[string]any{"code": "unhandled"})
	}
}

func writeSyntheticJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func examplePath(name string) string {
	return filepath.Join("..", "..", "..", "examples", "synthetic", name)
}

func writeSyntheticKeyFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "api-key")
	if err := os.WriteFile(path, []byte("test-synthetic-credential\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func runSyntheticCommand(t *testing.T, server *httptest.Server, keyFile string, extra ...string) (int, string, string) {
	t.Helper()
	t.Setenv("IDENQA_API_KEY", "")
	args := []string{
		"synthetic", "run",
		"--api-url", server.URL,
		"--api-key-file", keyFile,
		"--profile-file", examplePath("capture-profile.json"),
		"--policy-file", examplePath("policy.json"),
		"--timeout", "2s",
		"--poll-interval", "10ms",
	}
	args = append(args, extra...)
	var stdout, stderr bytes.Buffer
	code := bootstrap.Run(args, &stdout, &stderr, buildinfo.Info{})
	return code, stdout.String(), stderr.String()
}

func recordedRequest(t *testing.T, fixture *syntheticFixture, step string) syntheticRecordedRequest {
	t.Helper()
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	for _, request := range fixture.requests {
		if request.step == step {
			return request
		}
	}
	t.Fatalf("no %q request was recorded", step)
	return syntheticRecordedRequest{}
}

func TestSyntheticRunCompletesEveryJourneyStep(t *testing.T) {
	fixture, server := newSyntheticFixture(t)
	code, stdout, stderr := runSyntheticCommand(t, server, writeSyntheticKeyFile(t))
	if code != 0 {
		t.Fatalf("exit code = %d, stderr = %q", code, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	expected := []string{
		"profile.create", "profile.publish", "policy.create", "policy.activate",
		"verification.create", "notice.create", "authority.declare", "authority.snapshot",
		"authority.consent", "evidence.issue", "evidence.upload", "capture.progress",
		"verification.await", "decision.read",
	}
	fixture.mu.Lock()
	steps := make([]string, 0, len(fixture.requests))
	for _, request := range fixture.requests {
		steps = append(steps, request.step)
	}
	fixture.mu.Unlock()
	if strings.Join(steps, ",") != strings.Join(expected, ",") {
		t.Fatalf("journey steps = %v, want %v", steps, expected)
	}
	for _, line := range expected {
		if !strings.Contains(stdout, "step="+line+" status=ok") {
			t.Fatalf("stdout omitted step %q: %q", line, stdout)
		}
	}
	if !strings.Contains(stdout, "outcome=satisfied") || !strings.Contains(stdout, "directive=complete_verified") {
		t.Fatalf("stdout omitted the final decision: %q", stdout)
	}
	for _, secret := range []string{"test-synthetic-credential", "ct_capture_token_fixture", "ot_outcome_token_fixture"} {
		if strings.Contains(stdout+stderr, secret) {
			t.Fatalf("journey output leaked %q", secret)
		}
	}

	profileDocument, err := os.ReadFile(examplePath("capture-profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	policyDocument, err := os.ReadFile(examplePath("policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	createProfile := recordedRequest(t, fixture, "profile.create")
	var profileBody struct {
		Name     string          `json:"name"`
		Document json.RawMessage `json:"document"`
	}
	if err := json.Unmarshal(createProfile.body, &profileBody); err != nil {
		t.Fatal(err)
	}
	if profileBody.Name != "Synthetic selfie verification" || !equalJSON(t, profileBody.Document, profileDocument) {
		t.Fatalf("profile create body = %s", createProfile.body)
	}
	publishProfile := recordedRequest(t, fixture, "profile.publish")
	if publishProfile.ifMatch != `"1"` || publishProfile.idempotencyKey != `"synthetic-profile-publish"` {
		t.Fatalf("profile publish headers = %+v", publishProfile)
	}
	createPolicy := recordedRequest(t, fixture, "policy.create")
	var policyBody struct {
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(createPolicy.body, &policyBody); err != nil {
		t.Fatal(err)
	}
	if createPolicy.idempotencyKey != `"synthetic-policy-create"` || !equalJSON(t, policyBody.Definition, policyDocument) {
		t.Fatalf("policy create body = %s headers = %+v", createPolicy.body, createPolicy)
	}
	activatePolicy := recordedRequest(t, fixture, "policy.activate")
	var activation struct {
		Revision        int64  `json:"revision"`
		ExpectedVersion int64  `json:"expected_version"`
		Reason          string `json:"reason"`
	}
	if err := json.Unmarshal(activatePolicy.body, &activation); err != nil {
		t.Fatal(err)
	}
	if activation.Revision != 1 || activation.ExpectedVersion != 0 || activation.Reason != "synthetic_journey" {
		t.Fatalf("policy activation body = %s", activatePolicy.body)
	}
	createVerification := recordedRequest(t, fixture, "verification.create")
	if !strings.HasPrefix(createVerification.idempotencyKey, `"synthetic-verification-`) {
		t.Fatalf("verification idempotency key = %s", createVerification.idempotencyKey)
	}
	issue := recordedRequest(t, fixture, "evidence.issue")
	var issueBody struct {
		RequirementKey    string `json:"requirement_key"`
		Artefact          string `json:"artefact"`
		AcquisitionMethod string `json:"acquisition_method"`
		ExpectedBytes     int64  `json:"expected_bytes"`
		ExpectedDigest    string `json:"expected_digest"`
		MediaType         string `json:"media_type"`
		Region            string `json:"region"`
	}
	if err := json.Unmarshal(issue.body, &issueBody); err != nil {
		t.Fatal(err)
	}
	if issueBody.RequirementKey != "selfie" || issueBody.Artefact != "idenqa.artefact.selfie_image" ||
		issueBody.AcquisitionMethod != "idenqa.method.live_camera" || issueBody.MediaType != "image/jpeg" ||
		issueBody.Region != "tenant.region.ng" {
		t.Fatalf("evidence issue body = %s", issue.body)
	}
	upload := recordedRequest(t, fixture, "evidence.upload")
	if int64(len(upload.body)) != issueBody.ExpectedBytes {
		t.Fatalf("uploaded bytes = %d, expected = %d", len(upload.body), issueBody.ExpectedBytes)
	}
	sum := sha256.Sum256(upload.body)
	if issueBody.ExpectedDigest != "sha256:"+hex.EncodeToString(sum[:]) {
		t.Fatalf("expected digest = %q", issueBody.ExpectedDigest)
	}
	if upload.contentDigest != "sha-256=:"+base64.StdEncoding.EncodeToString(sum[:])+":" || upload.ifMatch != `"1"` {
		t.Fatalf("upload headers = %+v", upload)
	}
	for _, step := range []string{"authority.snapshot", "authority.consent", "evidence.issue", "evidence.upload", "capture.progress"} {
		if request := recordedRequest(t, fixture, step); request.authorization != "Bearer ct_capture_token_fixture" {
			t.Fatalf("%s authorization = %q", step, request.authorization)
		}
	}
	for _, step := range []string{"profile.create", "policy.create", "verification.create", "notice.create", "authority.declare", "verification.await", "decision.read"} {
		if request := recordedRequest(t, fixture, step); request.authorization != "Bearer test-synthetic-credential" {
			t.Fatalf("%s authorization = %q", step, request.authorization)
		}
	}
}

func equalJSON(t *testing.T, left, right []byte) bool {
	t.Helper()
	var leftValue, rightValue any
	if err := json.Unmarshal(left, &leftValue); err != nil {
		t.Fatalf("decode left JSON: %v", err)
	}
	if err := json.Unmarshal(right, &rightValue); err != nil {
		t.Fatalf("decode right JSON: %v", err)
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func TestSyntheticRunFailsAtEveryJourneyStep(t *testing.T) {
	steps := []string{
		"profile.create", "profile.publish", "policy.create", "policy.activate",
		"verification.create", "notice.create", "authority.declare", "authority.snapshot",
		"authority.consent", "evidence.issue", "evidence.upload", "capture.progress",
		"verification.await", "decision.read",
	}
	for _, step := range steps {
		t.Run(step, func(t *testing.T) {
			fixture := &syntheticFixture{t: t, failStep: step, completeAfter: 1}
			server := httptest.NewServer(http.HandlerFunc(fixture.handle))
			defer server.Close()
			code, _, stderr := runSyntheticCommand(t, server, writeSyntheticKeyFile(t))
			if code != 1 {
				t.Fatalf("exit code = %d, want 1; stderr = %q", code, stderr)
			}
			if !strings.Contains(stderr, "step "+step) {
				t.Fatalf("stderr = %q, want actionable step %q", stderr, step)
			}
		})
	}
}

func TestSyntheticRunBoundsPollingAndReportsTimeout(t *testing.T) {
	fixture := &syntheticFixture{t: t, neverComplete: true}
	server := httptest.NewServer(http.HandlerFunc(fixture.handle))
	defer server.Close()
	keyFile := writeSyntheticKeyFile(t)
	t.Setenv("IDENQA_API_KEY", "")
	started := time.Now()
	var stdout, stderr bytes.Buffer
	code := bootstrap.Run([]string{
		"synthetic", "run",
		"--api-url", server.URL, "--api-key-file", keyFile,
		"--profile-file", examplePath("capture-profile.json"),
		"--policy-file", examplePath("policy.json"),
		"--timeout", "60ms", "--poll-interval", "10ms",
	}, &stdout, &stderr, buildinfo.Info{})
	elapsed := time.Since(started)
	if code != 1 {
		t.Fatalf("exit code = %d, want 1; stdout = %q", code, stdout.String())
	}
	if elapsed > 5*time.Second {
		t.Fatalf("bounded poll took %s", elapsed)
	}
	if !strings.Contains(stderr.String(), "did not reach a completed decision within") ||
		!strings.Contains(stderr.String(), "IDENQA_WORKER_SYNTHETIC_PROCESSING") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	fixture.mu.Lock()
	polls := fixture.polls
	fixture.mu.Unlock()
	if polls == 0 || polls > 30 {
		t.Fatalf("poll count = %d", polls)
	}
}

func TestSyntheticRunJSONOutputIsDeterministic(t *testing.T) {
	_, server := newSyntheticFixture(t)
	keyFile := writeSyntheticKeyFile(t)
	firstCode, first, firstStderr := runSyntheticCommand(t, server, keyFile, "--json")
	if firstCode != 0 {
		t.Fatalf("first exit code = %d, stderr = %q", firstCode, firstStderr)
	}
	secondCode, second, secondStderr := runSyntheticCommand(t, server, keyFile, "--json")
	if secondCode != 0 {
		t.Fatalf("second exit code = %d, stderr = %q", secondCode, secondStderr)
	}
	if first != second {
		t.Fatalf("deterministic JSON differed:\nfirst:  %s\nsecond: %s", first, second)
	}
	lines := strings.Split(strings.TrimSpace(first), "\n")
	if len(lines) != 14 {
		t.Fatalf("JSON lines = %d, want 14", len(lines))
	}
	for _, line := range lines {
		var step struct {
			Step   string            `json:"step"`
			Status string            `json:"status"`
			Fields map[string]string `json:"fields"`
		}
		if err := json.Unmarshal([]byte(line), &step); err != nil {
			t.Fatalf("decode JSON step %q: %v", line, err)
		}
		if step.Step == "" || step.Status != "ok" {
			t.Fatalf("unexpected JSON step %q", line)
		}
	}
}

func TestSyntheticRunRejectsUnusableOptions(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	keyFile := writeSyntheticKeyFile(t)
	base := []string{
		"synthetic", "run",
		"--api-url", "http://127.0.0.1:1",
		"--api-key-file", keyFile,
		"--profile-file", examplePath("capture-profile.json"),
		"--policy-file", examplePath("policy.json"),
	}
	tests := []struct {
		name     string
		extra    []string
		wantCode int
		wantText string
	}{
		{name: "missing credential", extra: []string{"--api-key-file", ""}, wantCode: 2, wantText: "IDENQA_API_KEY is required"},
		{name: "invalid prefix", extra: []string{"--idempotency-prefix", "not a prefix"}, wantCode: 2, wantText: "idempotency-prefix"},
		{name: "poll exceeds timeout", extra: []string{"--timeout", "10ms", "--poll-interval", "1s"}, wantCode: 2, wantText: "one poll interval"},
		{name: "missing profile file", extra: []string{"--profile-file", "does-not-exist.json"}, wantCode: 1, wantText: "open capture profile file"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := append(append([]string(nil), base...), test.extra...)
			var stdout, stderr bytes.Buffer
			code := bootstrap.Run(args, &stdout, &stderr, buildinfo.Info{})
			if code != test.wantCode {
				t.Fatalf("exit code = %d, want %d; stderr = %q", code, test.wantCode, stderr.String())
			}
			if !strings.Contains(stderr.String(), test.wantText) {
				t.Fatalf("stderr = %q, want %q", stderr.String(), test.wantText)
			}
		})
	}
}

func TestSyntheticExamplesValidateAgainstPublicContracts(t *testing.T) {
	catalog, err := evidence.BuiltInCatalog()
	if err != nil {
		t.Fatal(err)
	}
	profileDocument, err := os.ReadFile(examplePath("capture-profile.json"))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := verification.ParseProfileFromCatalog(profileDocument, catalog)
	if err != nil {
		t.Fatalf("packaged capture profile is invalid: %v", err)
	}
	registry, err := catalog.Resolve(profile.Registry)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := verification.CanonicalJSON(profile, registry)
	if err != nil {
		t.Fatalf("packaged capture profile is not canonicalisable: %v", err)
	}
	if !json.Valid(canonical) || len(profile.Requirements) != 1 || profile.Requirements[0].Key != "selfie" {
		t.Fatalf("packaged capture profile = %s", canonical)
	}
	policyDocument, err := os.ReadFile(examplePath("policy.json"))
	if err != nil {
		t.Fatal(err)
	}
	var definition struct {
		SchemaMajor       uint16          `json:"schema_major"`
		SchemaMinor       uint16          `json:"schema_minor"`
		VerifiedAssurance string          `json:"verified_assurance"`
		Rules             []policyv1.Rule `json:"rules"`
	}
	decoder := json.NewDecoder(bytes.NewReader(policyDocument))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		t.Fatalf("packaged policy definition is invalid: %v", err)
	}
	policyCanonical, err := policyv1.Canonical(policyv1.Document{
		SchemaMajor: definition.SchemaMajor, SchemaMinor: definition.SchemaMinor,
		PolicyID: "pol_00000000000000000000000000", Revision: 1,
		VerifiedAssurance: definition.VerifiedAssurance, Rules: definition.Rules,
	})
	if err != nil {
		t.Fatalf("packaged policy definition does not compile: %v", err)
	}
	parsed, err := policyv1.ParseCanonical(policyCanonical)
	if err != nil {
		t.Fatalf("packaged policy definition is not canonical: %v", err)
	}
	if len(parsed.Rules) != 1 || parsed.Rules[0].Result.Directive != policyv1.DirectiveCompleteVerified {
		t.Fatalf("packaged policy rules = %+v", parsed.Rules)
	}
}
