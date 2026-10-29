//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
	"github.com/Mujhtech/idenqa/internal/transport/admissionbridge"
)

// The public Core fixture exchanges only its public signed snapshot and local
// protocol endpoint. Private Cloud binding, SQL, queue and TLS fixtures stay in
// the independently built companion repository and are entirely optional.
func TestExternalAdmissionDeliveryAndCaptureContinuation(t *testing.T) {
	repository := os.Getenv("IDENQA_CLOUD_JOURNEY_REPOSITORY")
	if repository == "" {
		t.Skip("optional external conformance runner required")
	}
	absolute, err := filepath.Abs(repository)
	if err != nil {
		t.Fatal(err)
	}
	runAdmissionPublicJourney(t, absolute)
}

func (a *admissionJourney) deliverThroughCloud(t *testing.T, snapshot admissionv1.Snapshot) {
	t.Helper()
	directory, err := os.MkdirTemp("/private/tmp", "idq-admission-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	socket := filepath.Join(directory, "core.sock")
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := admissionbridge.NewHandler(a.control)
	if err != nil {
		t.Fatal(err)
	}
	handler.WithSummary(a.control)
	var loseAcknowledgement atomic.Bool
	loseAcknowledgement.Store(true)
	var installs atomic.Int64
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, r)
		if response.Code == http.StatusOK && r.URL.Path == "/local/v1/admission/snapshots/install" {
			installs.Add(1)
			if loseAcknowledgement.Swap(false) {
				connection, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = connection.Close()
				return
			}
		}
		for name, values := range response.Header() {
			for _, value := range values {
				w.Header().Add(name, value)
			}
		}
		w.WriteHeader(response.Code)
		_, _ = io.Copy(w, response.Body)
	})}
	closed := make(chan struct{})
	go func() { defer close(closed); _ = server.Serve(listener) }()
	defer func() { _ = server.Close(); <-closed }()
	input, err := json.Marshal(struct {
		Snapshot  admissionv1.Snapshot `json:"snapshot"`
		PublicKey string               `json:"publicKey"`
	}{snapshot, base64.StdEncoding.EncodeToString(a.key.Public().(ed25519.PublicKey))})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "public-policy.json")
	if err := os.WriteFile(path, input, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-race", "-tags=integration", "./internal/platform/admissiondeliverystore", "-run", "^TestCorePublicPolicyDeliveryJourney$", "-count=1", "-timeout=75s")
	command.Dir = a.cloudRepository
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOWORK=") && !strings.HasPrefix(entry, "IDENQA_CLOUD_ADMISSION_JOURNEY_FILE=") && !strings.HasPrefix(entry, "IDENQA_CLOUD_ADMISSION_CORE_SOCKET=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GOWORK=off", "IDENQA_CLOUD_ADMISSION_JOURNEY_FILE="+path, "IDENQA_CLOUD_ADMISSION_CORE_SOCKET="+socket)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("external public policy conformance failed: %v\n%s", err, output.String())
	}
	if installs.Load() != 2 {
		t.Fatalf("actual Core install acknowledgements=%d; expected one lost acknowledgement and exact recovery", installs.Load())
	}
}
