package idenqa_test

import (
	"bytes"
	"encoding/base64"
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

func TestWebhookCLIProtectsDisplayOnceSecret(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{42}, 32))
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.URL.Path != "/v1/webhook-endpoints" || r.Header.Get("Authorization") != "Bearer test-credential" || r.Header.Get("Idempotency-Key") != `"create-stable"` {
			t.Error("incorrect API request")
		}
		w.Header().Set("Content-Type", "application/json")
		if calls.Load() == 1 {
			_, _ = fmt.Fprintf(w, `{"endpoint":{"id":"whk_01M11HEQG00000000000000000"},"replayed":false,"signing_secret":%q}`, secret)
		} else {
			_, _ = fmt.Fprint(w, `{"endpoint":{"id":"whk_01M11HEQG00000000000000000"},"replayed":true}`)
		}
	}))
	defer server.Close()
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	secretFile := filepath.Join(t.TempDir(), "secret")
	args := []string{"webhook", "create", "--api-url", server.URL, "--api-key-file", keyFile, "--url", "https://receiver.example.com", "--idempotency-key", "create-stable", "--secret-out", secretFile}
	run := func(arguments []string, want int) (string, string) {
		t.Helper()
		var out, diagnostics bytes.Buffer
		code := bootstrap.Run(arguments, &out, &diagnostics, buildinfo.Info{})
		if code != want {
			t.Fatalf("exit=%d want=%d stderr=%s", code, want, diagnostics.String())
		}
		if strings.Contains(out.String()+diagnostics.String(), secret) || strings.Contains(out.String()+diagnostics.String(), "test-credential") {
			t.Fatal("CLI disclosed a secret")
		}
		return out.String(), diagnostics.String()
	}
	run(args, 0)
	// #nosec G304 -- Read the test-owned secret output inside t.TempDir.
	material, err := os.ReadFile(secretFile)
	if err != nil || string(material) != secret+"\n" {
		t.Fatal("secret file did not receive exact material")
	}
	info, err := os.Stat(secretFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("secret file is not owner-only")
	}
	_, diagnostics := run(args, 1)
	if strings.Contains(diagnostics, "Usage:") || calls.Load() != 1 {
		t.Fatal("existing secret output file permitted mutation or wrong error class")
	}
	retryFile := filepath.Join(t.TempDir(), "retry-secret")
	retryArgs := append([]string(nil), args...)
	retryArgs[len(retryArgs)-1] = retryFile
	out, _ := run(retryArgs, 0)
	if !strings.Contains(out, `"replayed":true`) {
		t.Fatal("retry metadata omitted")
	}
	if _, err := os.Stat(retryFile); !os.IsNotExist(err) {
		t.Fatal("retry retained empty secret file")
	}
}

func TestWebhookCLIConfirmationAndFailures(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-credential")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		_, _ = fmt.Fprint(w, "private-server-response")
	}))
	defer server.Close()
	for _, test := range []struct {
		operation string
		args      []string
		code      int
	}{
		{"disable", []string{"--id", "whk_01M11HEQG00000000000000000", "--expected-version", "1", "--reason", "tenant_requested", "--idempotency-key", "disable"}, 2},
		{"list", nil, 1},
	} {
		var out, diagnostics bytes.Buffer
		args := append([]string{"webhook", test.operation, "--api-url", server.URL}, test.args...)
		code := bootstrap.Run(args, &out, &diagnostics, buildinfo.Info{})
		if code != test.code {
			t.Fatalf("%s exit=%d: %s", test.operation, code, diagnostics.String())
		}
		if strings.Contains(out.String()+diagnostics.String(), "private-server-response") {
			t.Fatal("error body leaked")
		}
		if test.operation == "disable" && calls.Load() != 0 {
			t.Fatal("unconfirmed operation reached API")
		}
		if test.operation == "list" && strings.Contains(diagnostics.String(), "Usage:") {
			t.Fatal("runtime error printed usage")
		}
	}
}
