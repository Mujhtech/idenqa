package smileid_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/smileid"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

type countedEvidence struct{ calls int }

func (reader *countedEvidence) ReadProviderEvidence(context.Context, providerv1.EvidenceGrantReference, int64) ([]byte, error) {
	reader.calls++
	return []byte{0xff, 0xd8, 0xff, 0xe0, 1, 2, 3}, nil
}
func asyncStatus(t *testing.T, request providerv1.Request) map[string]any {
	t.Helper()
	timestamp := fixedNow.Format("2006-01-02T15:04:05.000Z")
	return map[string]any{"code": "2302", "timestamp": timestamp, "signature": signature("test-key", timestamp, "085"), "job_id": request.AttemptID, "user_id": request.VerificationID, "job_complete": true, "job_success": true, "result": map[string]any{"SmileJobID": "job-123", "PartnerParams": map[string]any{"job_id": request.AttemptID, "user_id": request.VerificationID, "job_type": 6}, "Actions": map[string]any{"Selfie_Check": "Passed", "Liveness_Check": "Passed", "Verify_Document": "Passed", "Selfie_To_ID_Card_Compare": "Completed"}}}
}
func TestAdvanceResumeNeverReadsEvidenceAndRejectsUnboundResults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		mutate        func(map[string]any)
		fail, pending bool
	}{
		{name: "complete"},
		{name: "pending", mutate: func(m map[string]any) { m["job_complete"] = false }, pending: true},
		{name: "not found", mutate: func(m map[string]any) { m["code"] = "2304" }, pending: true},
		{name: "missing upload", mutate: func(m map[string]any) { m["code"] = "2314" }, pending: true},
		{name: "wrong job", mutate: func(m map[string]any) { m["job_id"] = "other" }, fail: true},
		{name: "wrong subject", mutate: func(m map[string]any) { m["user_id"] = "other" }, fail: true},
		{name: "wrong product", mutate: func(m map[string]any) { m["result"].(map[string]any)["PartnerParams"].(map[string]any)["job_type"] = 1 }, fail: true},
		{name: "bad signature", mutate: func(m map[string]any) { m["signature"] = "invalid" }, fail: true},
		{name: "stale signature", mutate: func(m map[string]any) {
			m["timestamp"] = "2020-01-01T00:00:00Z"
			m["signature"] = signature("test-key", m["timestamp"].(string), "085")
		}, fail: true},
		{name: "malformed completion", mutate: func(m map[string]any) { m["job_complete"] = "true" }, fail: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, _ := fixture(t, &smileid.Adapter{})
			request.Evidence = request.Evidence[:2]
			body := asyncStatus(t, request)
			if test.mutate != nil {
				test.mutate(body)
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			transport := &client{responses: []response{{status: 200, body: string(raw)}}}
			reader := &countedEvidence{}
			adapter, err := smileid.New(secrets{}, inputs{}, reader, transport, nil, func() time.Time { return fixedNow })
			if err != nil {
				t.Fatal(err)
			}
			progress, err := adapter.Advance(t.Context(), request, true)
			if (err != nil) != test.fail {
				t.Fatalf("error=%v wanted failure=%v", err, test.fail)
			}
			if reader.calls != 0 || len(transport.requests) != 1 || transport.requests[0].URL.Path != "/v1/job_status" {
				t.Fatal("resume redeemed evidence or resubmitted")
			}
			if test.fail {
				return
			}
			if (progress.Result == nil) != test.pending {
				t.Fatalf("pending=%v", progress.Result == nil)
			}
			if progress.Result != nil {
				for _, signal := range progress.Result.Signals {
					if signal.Name == "idenqa.signal.liveness" && signal.Outcome != providerv1.SignalOutcomeInconclusive {
						t.Fatal("static selfie implies liveness")
					}
				}
			}
		})
	}
}
func TestAdvanceInitialUploadBindsPartnerAndProviderJob(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, url string
		upload    bool
	}{
		{"valid", "https://uploads.example/videos/085/085-job-123-random/attachments.zip", true},
		{"wrong partner", "https://uploads.example/videos/999/999-job-123-random/attachments.zip", false},
		{"wrong job", "https://uploads.example/videos/085/085-other-random/attachments.zip", false},
		{"encoded path", "https://uploads.example/videos/085/085-job-123-random/%61ttachments.zip", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, _ := fixture(t, &smileid.Adapter{})
			request.Evidence = request.Evidence[:2]
			transport := &client{responses: []response{{status: 200, body: fmt.Sprintf(`{"code":"2202","smile_job_id":"job-123","upload_url":%q}`, test.url)}, {status: 200, body: ""}}}
			reader := &countedEvidence{}
			adapter, err := smileid.New(secrets{}, inputs{}, reader, transport, nil, func() time.Time { return fixedNow })
			if err != nil {
				t.Fatal(err)
			}
			progress, err := adapter.Advance(t.Context(), request, false)
			if err != nil || progress.Result != nil || progress.ProviderJobID != "job-123" {
				t.Fatalf("progress=%v err=%v", progress, err)
			}
			wanted := 1
			if test.upload {
				wanted = 2
			}
			if len(transport.requests) != wanted || reader.calls != 2 {
				t.Fatal("incorrect submission/upload count")
			}
			for _, req := range transport.requests {
				if req.Method == http.MethodPut && !strings.Contains(req.URL.Path, "job-123") {
					t.Fatal("unbound upload")
				}
			}
		})
	}
}
