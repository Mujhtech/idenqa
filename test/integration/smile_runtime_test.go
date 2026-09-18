//go:build integration

package integration_test

import (
	"archive/zip"
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/evidence"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func smileDocumentProfile(t *testing.T, registry evidence.Registry) verification.Profile {
	t.Helper()
	profile, err := verification.NewProfile(registry, []verification.Requirement{
		{Key: "document", Purpose: evidence.PurposeIdentityVerification, EvidenceType: evidence.EvidenceDocumentImage, Artefacts: []evidence.Name{evidence.ArtefactDocumentFront}, Acquisition: verification.Acquisition{Strategy: verification.StrategyAnyOf, Methods: []evidence.Name{evidence.MethodLiveCamera}}},
		{Key: "selfie", Purpose: evidence.PurposeIdentityVerification, EvidenceType: evidence.EvidenceSelfieImage, Artefacts: []evidence.Name{evidence.ArtefactSelfieImage}, Acquisition: verification.Acquisition{Strategy: verification.StrategyAnyOf, Methods: []evidence.Name{evidence.MethodLiveCamera}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return profile
}
func uploadSmileSelfie(t *testing.T, client *http.Client, base, token string, body []byte) {
	t.Helper()
	var upload openapiv1.EvidenceUpload
	headers := performPublicJSONRequest(t, client, publicJSONRequest{Method: "POST", URL: base + "/v1/evidence-uploads", Bearer: token, IdempotencyKey: "smile-selfie-upload", Body: openapiv1.EvidenceUploadCreate{RequirementKey: "selfie", Artefact: string(evidence.ArtefactSelfieImage), AcquisitionMethod: string(evidence.MethodLiveCamera), ExpectedBytes: int64(len(body)), ExpectedDigest: string(platformcrypto.Sum(body)), MediaType: "image/jpeg", Region: "tenant.region.ng"}, WantStatus: 201, Result: &upload})
	request, err := http.NewRequestWithContext(t.Context(), "PUT", base+"/v1/evidence-uploads/"+upload.ID, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "image/jpeg")
	request.Header.Set("Content-Digest", contentDigest(body))
	request.Header.Set("If-Match", headers.Get("ETag"))
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 {
		t.Fatalf("selfie upload status %d", response.StatusCode)
	}
}

type smileJourneyFixture struct {
	server                      *httptest.Server
	mu                          sync.Mutex
	job, user                   string
	submitted, uploaded, polled atomic.Int32
	complete                    atomic.Bool
}

func newSmileJourneyFixture(t *testing.T, plaintext []byte) *smileJourneyFixture {
	t.Helper()
	fixture := &smileJourneyFixture{}
	fixture.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fixture.mu.Lock()
		defer fixture.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/upload":
			fixture.submitted.Add(1)
			var body struct {
				PartnerParams struct {
					JobID   string `json:"job_id"`
					UserID  string `json:"user_id"`
					JobType int    `json:"job_type"`
				} `json:"partner_params"`
			}
			if r.Method != "POST" || json.NewDecoder(r.Body).Decode(&body) != nil || body.PartnerParams.JobType != 6 {
				t.Error("invalid Smile preparation")
				w.WriteHeader(400)
				return
			}
			fixture.job = body.PartnerParams.JobID
			fixture.user = body.PartnerParams.UserID
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "2202", "smile_job_id": "job-123", "upload_url": fixture.server.URL + "/videos/085/085-job-123-fixture/attachments.zip"})
		case "/videos/085/085-job-123-fixture/attachments.zip":
			fixture.uploaded.Add(1)
			raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
			if err != nil {
				t.Error(err)
				return
			}
			archive, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
			if err != nil || len(archive.File) != 1 {
				t.Error("invalid Smile zip")
				return
			}
			entry, err := archive.File[0].Open()
			if err != nil {
				t.Error(err)
				return
			}
			defer func() { _ = entry.Close() }()
			var info struct {
				Images []struct {
					Type  int    `json:"image_type_id"`
					Image string `json:"image"`
				} `json:"images"`
				IDInfo map[string]any `json:"id_info"`
			}
			if json.NewDecoder(entry).Decode(&info) != nil || len(info.Images) != 2 || info.IDInfo["country"] != "NG" || info.IDInfo["id_type"] != "PASSPORT" {
				t.Error("invalid Smile document/selfie package")
				return
			}
			kinds := map[int]bool{}
			for _, image := range info.Images {
				decoded, err := base64.StdEncoding.DecodeString(image.Image)
				if err != nil || !bytes.Equal(decoded, plaintext) {
					t.Error("Smile upload evidence mismatch")
				}
				kinds[image.Type] = true
			}
			if !kinds[2] || !kinds[3] {
				t.Error("missing selfie or front")
			}
			w.WriteHeader(200)
		case "/v1/job_status":
			fixture.polled.Add(1)
			var request map[string]any
			if json.NewDecoder(r.Body).Decode(&request) != nil || request["job_id"] != fixture.job || request["user_id"] != fixture.user || request["history"] != false || request["image_links"] != false {
				t.Error("status identity/options mismatch")
				w.WriteHeader(400)
				return
			}
			timestamp := time.Now().UTC().Format(time.RFC3339Nano)
			mac := hmac.New(sha256.New, []byte("fixture-key"))
			_, _ = mac.Write([]byte(timestamp + "085sid_request"))
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "2302", "timestamp": timestamp, "signature": base64.StdEncoding.EncodeToString(mac.Sum(nil)), "job_id": fixture.job, "user_id": fixture.user, "job_complete": fixture.complete.Load(), "job_success": true, "result": map[string]any{"SmileJobID": "job-123", "PartnerParams": map[string]any{"job_id": fixture.job, "user_id": fixture.user, "job_type": 6}, "Actions": map[string]any{"Selfie_Check": "Passed", "Liveness_Check": "Passed", "Verify_Document": "Passed", "Selfie_To_ID_Card_Compare": "Completed"}, "FullName": "discard-sensitive-output"}})
		default:
			t.Error("unexpected Smile endpoint")
			w.WriteHeader(404)
		}
	}))
	return fixture
}
func (fixture *smileJourneyFixture) waitPending(t *testing.T, admin *pg.Pool) {
	t.Helper()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var count int
		if err := admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.provider_async_operations o JOIN idenqa.provider_dispatches d USING(tenant_id,attempt_id) WHERE o.provider_job_id='job-123' AND o.lease_expires_at IS NULL AND d.result_body IS NULL`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 && fixture.polled.Load() > 0 {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("Smile job never reached durable pending state")
		case <-tick.C:
		}
	}
}
func (fixture *smileJourneyFixture) assertCompleted(t *testing.T, admin *pg.Pool) {
	t.Helper()
	if fixture.submitted.Load() != 1 || fixture.uploaded.Load() != 1 || fixture.polled.Load() < 2 {
		t.Fatalf("Smile duplicated submission/upload: %d/%d polls=%d", fixture.submitted.Load(), fixture.uploaded.Load(), fixture.polled.Load())
	}
	var grants int
	var result []byte
	if err := admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.evidence_processing_grants WHERE uses=1),(SELECT result_body FROM idenqa.provider_dispatches LIMIT 1)`).Scan(&grants, &result); err != nil {
		t.Fatal(err)
	}
	if grants != 2 {
		t.Fatalf("Smile grants redeemed %d times", grants)
	}
	var body struct {
		Signals []struct {
			Name    string `json:"name"`
			Outcome string `json:"outcome"`
		} `json:"signals"`
	}
	if json.Unmarshal(result, &body) != nil {
		t.Fatal("invalid result")
	}
	for _, signal := range body.Signals {
		if signal.Name == "idenqa.signal.liveness" && signal.Outcome != "inconclusive" {
			t.Fatal("static selfie claimed liveness")
		}
	}
	if _, err := admin.Native().Exec(t.Context(), `UPDATE idenqa.provider_async_operations SET provider_job_id='different'`); err == nil {
		t.Fatal("provider job reference mutable")
	}
}

func assertSmilePollingFences(t *testing.T, admin, runtime *pg.Pool) {
	t.Helper()
	var encoded []byte
	if err := admin.Native().QueryRow(t.Context(), `SELECT request_body FROM idenqa.provider_requests LIMIT 1`).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var request providerv1.Request
	if json.Unmarshal(encoded, &request) != nil {
		t.Fatal("invalid durable request")
	}
	now := time.Now().UTC().Add(15 * time.Second)
	first, err := providerpostgres.NewRequestStore(runtime, fixedIntegrationClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := first.ClaimAsync(t.Context(), request)
	if err != nil || !claim.Acquired || claim.Initial {
		t.Fatalf("resume claim: %+v %v", claim, err)
	}
	duplicate, err := first.ClaimAsync(t.Context(), request)
	if err != nil || duplicate.Acquired {
		t.Fatalf("overlapping poll acquired: %+v %v", duplicate, err)
	}
	second, err := providerpostgres.NewRequestStore(runtime, fixedIntegrationClock{now: now.Add(46 * time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	newer, err := second.ClaimAsync(t.Context(), request)
	if err != nil || !newer.Acquired || newer.Initial || newer.Fence <= claim.Fence {
		t.Fatalf("expired lease recovery: %+v %v", newer, err)
	}
	if err := first.SaveProgress(t.Context(), request, claim, providerv1.Progress{ProviderJobID: "job-123"}); !errors.Is(err, provider.ErrDispatchPending) {
		t.Fatalf("stale fence accepted: %v", err)
	}
	if err := second.SaveProgress(t.Context(), request, newer, providerv1.Progress{ProviderJobID: "other-job"}); !errors.Is(err, provider.ErrDispatchPending) {
		t.Fatalf("job substitution accepted: %v", err)
	}
	if err := second.SaveProgress(t.Context(), request, newer, providerv1.Progress{ProviderJobID: "job-123"}); err != nil {
		t.Fatal(err)
	}
	// Return only the fixture's scheduling clock to wall time before restart.
	if _, err := admin.Native().Exec(t.Context(), `UPDATE idenqa.provider_async_operations SET next_poll_at=$1`, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
}
