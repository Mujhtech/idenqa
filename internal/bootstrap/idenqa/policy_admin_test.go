package idenqa_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	bootstrap "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func TestPolicyCLIUsesPublicAPIAndConfirmsActivation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-policy-key")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/policies/pol_01ARZ3NDEKTSV4RRFFQ69G5FAV/activate" || r.Header.Get("Authorization") != "Bearer test-policy-key" || r.Header.Get("Idempotency-Key") != `"activation-key"` {
			t.Error("wrong public request")
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if input["expected_version"] != float64(0) || input["revision"] != float64(1) {
			t.Error("initial activation version changed")
		}
		_, _ = fmt.Fprint(w, `{"policy":{"activation_version":1},"replayed":false}`)
	}))
	defer server.Close()
	args := []string{"policy", "activate", "--api-url", server.URL, "--id", "pol_01ARZ3NDEKTSV4RRFFQ69G5FAV", "--revision", "1", "--expected-version", "0", "--reason", "tenant_requested", "--idempotency-key", "activation-key"}
	var out, diagnostics bytes.Buffer
	if code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{}); code != 2 || calls.Load() != 0 {
		t.Fatalf("unconfirmed activation exit=%d calls=%d", code, calls.Load())
	}
	out.Reset()
	diagnostics.Reset()
	args = append(args, "--confirm")
	if code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{}); code != 0 || calls.Load() != 1 {
		t.Fatalf("activation exit=%d calls=%d err=%s", code, calls.Load(), diagnostics.String())
	}
	if strings.Contains(out.String()+diagnostics.String(), "test-policy-key") {
		t.Fatal("credential leaked")
	}
}
func TestPolicyCLICreateReadsDefinitionAndSanitizesErrors(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-policy-key")
	definition := `{"schema_major":1,"schema_minor":0,"verified_assurance":"","rules":[]}`
	file := filepath.Join(t.TempDir(), "definition.json")
	if err := os.WriteFile(file, []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		if string(input["definition"]) != definition || r.URL.Path != "/v1/policies" {
			t.Error("definition wrapper changed")
		}
		w.WriteHeader(400)
		_, _ = fmt.Fprint(w, "private compiler diagnostic")
	}))
	defer server.Close()
	var out, diagnostics bytes.Buffer
	code := bootstrap.Run([]string{"policy", "create", "--api-url", server.URL, "--file", file, "--idempotency-key", "create-key"}, &out, &diagnostics, buildinfo.Info{})
	if code != 1 || strings.Contains(diagnostics.String(), "Usage:") || strings.Contains(diagnostics.String(), "private compiler diagnostic") {
		t.Fatalf("unsafe runtime failure code=%d err=%s", code, diagnostics.String())
	}
}
