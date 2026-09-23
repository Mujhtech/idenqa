package dojah_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

type sideReader struct {
	fail    string
	cancel  context.CancelFunc
	reads   []string
	buffers [][]byte
}

func TestDocumentCheckRejectsPreviousManifestPin(t *testing.T) {
	t.Parallel()
	reader := &sideReader{}
	transport := &client{}
	adapter, err := dojah.New(secrets{}, inputs{}, reader, transport, func() time.Time { return fixedNow })
	if err != nil {
		t.Fatal(err)
	}
	request, _ := fixture(t, adapter, "idenqa.check.document_analysis")
	request.Evidence[0].Variant = "document.front"
	request.Adapter.AdapterVersion = "0.1.1"
	request.Adapter.PackageDigest = "sha256:7fb58659eab38742110fb3193890a8ca90ee5930bf47207bfb39bcb8acac10d6"
	result, err := adapter.Execute(t.Context(), request)
	if err != nil || result.Failure == nil || result.Failure.Code != "manifest_mismatch" {
		t.Fatalf("expected manifest rejection, got %+v, %v", result.Failure, err)
	}
	if len(reader.reads) != 0 || transport.request != nil {
		t.Fatal("old pin reached evidence or provider")
	}
}

func (reader *sideReader) ReadProviderEvidence(ctx context.Context, grant providerv1.EvidenceGrantReference, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit != 10<<20 {
		return nil, errors.New("unexpected evidence limit")
	}
	reader.reads = append(reader.reads, grant.Variant)
	value := []byte("fixture-" + grant.Variant)
	reader.buffers = append(reader.buffers, value)
	if reader.cancel != nil {
		reader.cancel()
	}
	if grant.Variant == reader.fail {
		return value, errors.New("fixture unavailable")
	}
	return value, nil
}

func TestDocumentCheckExplicitSides(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		variants  []string
		fail      string
		cancel    bool
		wantReads []string
		wantSend  bool
	}{
		{"front only", []string{"document.front"}, "", false, []string{"document.front"}, true},
		{"both sides", []string{"document.front", "document.back"}, "", false, []string{"document.front", "document.back"}, true},
		{"reversed grants", []string{"document.back", "document.front"}, "", false, []string{"document.front", "document.back"}, true},
		{"back unavailable", []string{"document.front", "document.back"}, "document.back", false, []string{"document.front", "document.back"}, false},
		{"front unavailable", []string{"document.front", "document.back"}, "document.front", false, []string{"document.front"}, false},
		{"cancel after front", []string{"document.front", "document.back"}, "", true, []string{"document.front"}, false},
		{"missing front", []string{"document.back"}, "", false, nil, false},
		{"duplicate front", []string{"document.front", "document.front"}, "", false, nil, false},
		{"duplicate back", []string{"document.front", "document.back", "document.back"}, "", false, nil, false},
		{"unrelated evidence", []string{"document.front", "selfie"}, "", false, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reader := &sideReader{fail: tt.fail}
			if tt.cancel {
				reader.cancel = cancel
			}
			transport := &client{status: 200, body: `{"entity":{"status":{"overall_status":1}}}`}
			adapter, err := dojah.New(secrets{}, inputs{}, reader, transport, func() time.Time { return fixedNow })
			if err != nil {
				t.Fatal(err)
			}
			request, _ := fixture(t, adapter, "idenqa.check.document_analysis")
			template := request.Evidence[0]
			request.Evidence = nil
			for i, variant := range tt.variants {
				grant := template
				grant.GrantID, grant.RedemptionID, grant.EvidenceID = id("grt", byte(i+1)), id("rdm", byte(i+5)), id("evd", byte(i+9))
				grant.Variant = variant
				request.Evidence = append(request.Evidence, grant)
			}
			result, err := adapter.Execute(ctx, request)
			if tt.cancel {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("expected cancellation, got %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				if err := result.ValidateForRequest(request); err != nil {
					t.Fatal(err)
				}
				if !tt.wantSend && (result.Failure == nil || result.Failure.Retry != providerv1.RetryNever) {
					t.Fatal("invalid evidence was not rejected")
				}
			}
			if !reflect.DeepEqual(reader.reads, tt.wantReads) {
				t.Fatalf("reads = %v, want %v", reader.reads, tt.wantReads)
			}
			if (transport.request != nil) != tt.wantSend {
				t.Fatal("unexpected external submission")
			}
			for _, buffer := range reader.buffers {
				for _, b := range buffer {
					if b != 0 {
						t.Fatal("raw evidence buffer not wiped")
					}
				}
			}
			if !tt.wantSend {
				return
			}
			var body map[string]string
			if err := json.Unmarshal(transport.requestBody, &body); err != nil {
				t.Fatal(err)
			}
			if body["input_type"] != "base64" || len(body) != len(tt.wantReads)+1 {
				t.Fatal("unexpected provider body")
			}
			for _, side := range tt.wantReads {
				field := "imagefrontside"
				if side == "document.back" {
					field = "imagebackside"
				}
				value, err := base64.StdEncoding.DecodeString(body[field])
				if err != nil || string(value) != "fixture-"+side {
					t.Fatal("wrong image for document side")
				}
			}
		})
	}
}
