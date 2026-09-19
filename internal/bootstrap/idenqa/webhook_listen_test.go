package idenqa

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdk "github.com/Mujhtech/idenqa/sdk/go"
)

const (
	listenEventOne  = `{"id":"evt_01M11HEQG00000000000000001","type":"verification.completed","schema_version":"1.0","created_at":"2026-09-19T10:00:00Z","tenant_id":"ten_01M11HEQG00000000000000000","region":"ng-lagos","data":{"verification_id":"ver_1","subject_id":"sub_1","decision_id":"dec_1"}}`
	listenEventTwo  = `{"id":"evt_01M11HEQG00000000000000002","type":"verification.completed","schema_version":"1.0","created_at":"2026-09-19T10:00:01Z","tenant_id":"ten_01M11HEQG00000000000000000","region":"ng-lagos","data":{"verification_id":"ver_2","subject_id":"sub_2","decision_id":"dec_2"}}`
	listenEventThin = `{"id":"evt_01M11HEQG00000000000000003","type":"verification.completed","schema_version":"1.0","created_at":"2026-09-19T10:00:02Z","tenant_id":"ten_01M11HEQG00000000000000000","region":"ng-lagos","data":{"id":"data_1","type":"verification","verification_id":"ver_3","subject_id":"sub_3","decision_id":"dec_3","status":"approved","score":92}}`
)

func TestWebhookListenForwardsAndResumes(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	secret := bytes.Repeat([]byte{7}, 32)
	secretFile := filepath.Join(t.TempDir(), "webhook-secret")
	if err := os.WriteFile(secretFile, []byte(base64.RawURLEncoding.EncodeToString(secret)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	verifier, err := sdk.NewWebhookVerifier([][]byte{secret}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var receiverMutex sync.Mutex
	var received [][]byte
	forwarded := make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(io.LimitReader(request.Body, sdk.MaximumWebhookBody+1))
		if err != nil {
			t.Errorf("read forwarded body: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := verifier.Verify(request.Header.Get(sdk.WebhookTimestampHeader), request.Header.Get(sdk.WebhookEventIDHeader), request.Header.Get(sdk.WebhookSignatureHeader), body); err != nil {
			t.Errorf("verify forwarded signature: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		receiverMutex.Lock()
		received = append(received, body)
		count := len(received)
		receiverMutex.Unlock()
		if count == 2 {
			select {
			case <-forwarded:
			default:
				close(forwarded)
			}
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	var streamMutex sync.Mutex
	streamCalls := 0
	resumed := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/webhook-events/stream" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		if request.Header.Get("Authorization") != "Bearer test-credential" || request.URL.Query().Get("event_types") != "verification.completed" {
			writer.WriteHeader(http.StatusUnauthorized)
			return
		}
		streamMutex.Lock()
		streamCalls++
		call := streamCalls
		streamMutex.Unlock()
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		flusher, _ := writer.(http.Flusher)
		if call == 1 {
			for _, event := range []string{listenEventOne, listenEventTwo} {
				_, _ = io.WriteString(writer, "id: "+eventID(t, event)+"\nevent: webhook.event\ndata: "+event+"\n\n")
				flusher.Flush()
			}
			select {
			case <-forwarded:
			case <-request.Context().Done():
			}
			return
		}
		if got := request.Header.Get("Last-Event-ID"); got != eventID(t, listenEventTwo) {
			t.Errorf("resume last event id=%q", got)
		}
		select {
		case <-resumed:
		default:
			close(resumed)
		}
		<-request.Context().Done()
	}))
	defer api.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := newWebhookListenCommand()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{
		"--api-url", api.URL,
		"--api-key-file", keyFile,
		"--event-types", "verification.completed",
		"--forward-to", receiver.URL,
		"--secret-file", secretFile,
	})
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	select {
	case <-resumed:
	case <-time.After(10 * time.Second):
		t.Fatalf("listener did not resume: %s", stderr.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("listen exited: %v: %s", err, stderr.String())
	}
	receiverMutex.Lock()
	defer receiverMutex.Unlock()
	if len(received) != 2 || string(received[0]) != listenEventOne || string(received[1]) != listenEventTwo {
		t.Fatalf("forwarded bodies=%d", len(received))
	}
	if !strings.Contains(stdout.String(), "verification.completed") || !strings.Contains(stdout.String(), eventID(t, listenEventOne)) {
		t.Fatalf("event summary missing: %s", stdout.String())
	}
}

func TestWebhookListenForwardsCustomHeaders(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	secret := bytes.Repeat([]byte{11}, 32)
	secretFile := filepath.Join(t.TempDir(), "webhook-secret")
	if err := os.WriteFile(secretFile, []byte(base64.RawURLEncoding.EncodeToString(secret)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	verifier, err := sdk.NewWebhookVerifier([][]byte{secret}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	forwarded := make(chan http.Header, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(io.LimitReader(request.Body, sdk.MaximumWebhookBody+1))
		if err != nil {
			t.Errorf("read forwarded body: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := verifier.Verify(request.Header.Get(sdk.WebhookTimestampHeader), request.Header.Get(sdk.WebhookEventIDHeader), request.Header.Get(sdk.WebhookSignatureHeader), body); err != nil {
			t.Errorf("verify forwarded signature: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case forwarded <- request.Header.Clone():
		default:
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/webhook-events/stream" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		flusher, _ := writer.(http.Flusher)
		_, _ = io.WriteString(writer, "id: "+eventID(t, listenEventOne)+"\nevent: webhook.event\ndata: "+listenEventOne+"\n\n")
		flusher.Flush()
		<-request.Context().Done()
	}))
	defer api.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := newWebhookListenCommand()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{
		"--api-url", api.URL,
		"--api-key-file", keyFile,
		"--event-types", "verification.completed",
		"--forward-to", receiver.URL,
		"--secret-file", secretFile,
		"--forward-header", "X-Test: abc",
	})
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	select {
	case header := <-forwarded:
		if got := header.Get("X-Test"); got != "abc" {
			t.Fatalf("forwarded X-Test=%q", got)
		}
		if header.Get(sdk.WebhookSignatureHeader) == "" || header.Get(sdk.WebhookTimestampHeader) == "" || header.Get(sdk.WebhookEventIDHeader) == "" {
			t.Fatal("mandatory signature headers missing")
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no forward received: %s", stderr.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("listen exited: %v: %s", err, stderr.String())
	}
}

func TestWebhookListenSkipsVerifyForSelfSignedForward(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	secret := bytes.Repeat([]byte{13}, 32)
	secretFile := filepath.Join(t.TempDir(), "webhook-secret")
	if err := os.WriteFile(secretFile, []byte(base64.RawURLEncoding.EncodeToString(secret)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	verifier, err := sdk.NewWebhookVerifier([][]byte{secret}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	forwarded := make(chan struct{}, 1)
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(io.LimitReader(request.Body, sdk.MaximumWebhookBody+1))
		if err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := verifier.Verify(request.Header.Get(sdk.WebhookTimestampHeader), request.Header.Get(sdk.WebhookEventIDHeader), request.Header.Get(sdk.WebhookSignatureHeader), body); err != nil {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case forwarded <- struct{}{}:
		default:
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/webhook-events/stream" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		flusher, _ := writer.(http.Flusher)
		_, _ = io.WriteString(writer, "id: "+eventID(t, listenEventOne)+"\nevent: webhook.event\ndata: "+listenEventOne+"\n\n")
		flusher.Flush()
		<-request.Context().Done()
	}))
	defer api.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := newWebhookListenCommand()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{
		"--api-url", api.URL,
		"--api-key-file", keyFile,
		"--event-types", "verification.completed",
		"--forward-to", receiver.URL,
		"--secret-file", secretFile,
		"--skip-verify",
	})
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	select {
	case <-forwarded:
	case <-time.After(10 * time.Second):
		t.Fatalf("no self-signed forward received: %s", stderr.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("listen exited: %v: %s", err, stderr.String())
	}
}

func TestWebhookListenThinForwarding(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	secret := bytes.Repeat([]byte{12}, 32)
	secretFile := filepath.Join(t.TempDir(), "webhook-secret")
	if err := os.WriteFile(secretFile, []byte(base64.RawURLEncoding.EncodeToString(secret)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	verifier, err := sdk.NewWebhookVerifier([][]byte{secret}, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	forwarded := make(chan []byte, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := io.ReadAll(io.LimitReader(request.Body, sdk.MaximumWebhookBody+1))
		if err != nil {
			t.Errorf("read forwarded body: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := verifier.Verify(request.Header.Get(sdk.WebhookTimestampHeader), request.Header.Get(sdk.WebhookEventIDHeader), request.Header.Get(sdk.WebhookSignatureHeader), body); err != nil {
			t.Errorf("verify forwarded signature: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case forwarded <- body:
		default:
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/webhook-events/stream" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		writer.WriteHeader(http.StatusOK)
		flusher, _ := writer.(http.Flusher)
		_, _ = io.WriteString(writer, "id: "+eventID(t, listenEventThin)+"\nevent: webhook.event\ndata: "+listenEventThin+"\n\n")
		flusher.Flush()
		<-request.Context().Done()
	}))
	defer api.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := newWebhookListenCommand()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{
		"--api-url", api.URL,
		"--api-key-file", keyFile,
		"--event-types", "verification.completed",
		"--forward-to", receiver.URL,
		"--secret-file", secretFile,
		"--thin",
	})
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	select {
	case body := <-forwarded:
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatalf("decode forwarded body: %v", err)
		}
		for _, key := range []string{"id", "type", "schema_version", "created_at", "tenant_id", "region"} {
			if _, ok := envelope[key]; !ok {
				t.Fatalf("projected envelope missing %s: %s", key, body)
			}
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(envelope["data"], &data); err != nil {
			t.Fatalf("decode projected data: %v", err)
		}
		want := []string{"id", "type", "verification_id", "subject_id", "decision_id"}
		if len(data) != len(want) {
			t.Fatalf("projected data keys=%v", data)
		}
		for _, key := range want {
			if _, ok := data[key]; !ok {
				t.Fatalf("projected data missing %s: %s", key, body)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("no forward received: %s", stderr.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("listen exited: %v: %s", err, stderr.String())
	}
}

func TestWebhookListenDiscoversEventTypes(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var mutex sync.Mutex
	var cursors []string
	filter := ""
	streamed := make(chan struct{})
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/v1/webhook-endpoints":
			if request.Header.Get("Authorization") != "Bearer test-credential" || request.URL.Query().Get("limit") != "100" {
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			mutex.Lock()
			cursors = append(cursors, request.URL.Query().Get("cursor"))
			mutex.Unlock()
			writer.Header().Set("Content-Type", "application/json")
			if request.URL.Query().Get("cursor") == "" {
				_, _ = io.WriteString(writer, `{"data":[{"id":"whk_1","url":"https://one.example.com","event_types":["verification.completed"],"schema_version":"1.0","version":1,"disabled_at":null},{"id":"whk_2","url":"https://two.example.com","event_types":["document.verified"],"schema_version":"1.0","version":1,"disabled_at":null},{"id":"whk_3","url":"https://three.example.com","event_types":["identity.deleted"],"schema_version":"1.0","version":1,"disabled_at":"2026-09-18T10:00:00Z"}],"page":{"has_more":true,"next_cursor":"cursor-1"}}`)
				return
			}
			if request.URL.Query().Get("cursor") != "cursor-1" {
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			_, _ = io.WriteString(writer, `{"data":[{"id":"whk_4","url":"https://four.example.com","event_types":["evidence.uploaded"],"schema_version":"1.0","version":1}],"page":{"has_more":false,"next_cursor":""}}`)
		case "/v1/webhook-events/stream":
			mutex.Lock()
			filter = request.URL.Query().Get("event_types")
			mutex.Unlock()
			writer.Header().Set("Content-Type", "text/event-stream")
			writer.WriteHeader(http.StatusOK)
			select {
			case <-streamed:
			default:
				close(streamed)
			}
			<-request.Context().Done()
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer api.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := newWebhookListenCommand()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{
		"--api-url", api.URL,
		"--api-key-file", keyFile,
		"--load-from-webhooks-api",
	})
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	select {
	case <-streamed:
	case <-time.After(10 * time.Second):
		t.Fatalf("stream did not connect: %s", stderr.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("listen exited: %v: %s", err, stderr.String())
	}
	mutex.Lock()
	defer mutex.Unlock()
	if filter != "document.verified,evidence.uploaded,verification.completed" {
		t.Fatalf("stream filter=%q", filter)
	}
	if len(cursors) != 2 || cursors[0] != "" || cursors[1] != "cursor-1" {
		t.Fatalf("discovery cursors=%v", cursors)
	}
}

func TestWebhookListenBackfillStartsFromBeginning(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	keyFile := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(keyFile, []byte("test-credential\n"), 0600); err != nil {
		t.Fatal(err)
	}
	lastEventIDs := make(chan string, 4)
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/webhook-events/stream" {
			writer.WriteHeader(http.StatusNotFound)
			return
		}
		select {
		case lastEventIDs <- request.Header.Get("Last-Event-ID"):
		default:
		}
		<-request.Context().Done()
	}))
	defer api.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	command := newWebhookListenCommand()
	var stdout, stderr bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(&stderr)
	command.SetArgs([]string{
		"--api-url", api.URL,
		"--api-key-file", keyFile,
		"--event-types", "verification.completed",
		"--backfill",
	})
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	select {
	case got := <-lastEventIDs:
		if got != "0" {
			t.Fatalf("Last-Event-ID=%q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("stream did not connect: %s", stderr.String())
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("listen exited: %v: %s", err, stderr.String())
	}
}

func TestWebhookListenRejectsInvalidOptions(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "test-credential")
	base := []string{"--api-url", "https://core.example.com", "--event-types", "verification.completed"}
	withBase := func(extra ...string) []string {
		return append(append([]string(nil), base...), extra...)
	}
	tooMany := withBase()
	for index := 0; index < 33; index++ {
		tooMany = append(tooMany, "--forward-header", fmt.Sprintf("X-Test-%d: value", index))
	}
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"reserved header", withBase("--forward-header", "Host: evil.example.com"), "reserved header"},
		{"reserved signature header", withBase("--forward-header", "idenqa-signature: v1=deadbeef"), "reserved header"},
		{"duplicate header", withBase("--forward-header", "X-Test: a", "--forward-header", "x-test: b"), "repeats a header name"},
		{"invalid header name", withBase("--forward-header", "Bad Header: value"), "valid HTTP header name"},
		{"empty header value", withBase("--forward-header", "X-Test:"), "single-line value"},
		{"multiline header value", withBase("--forward-header", "X-Test: a\r\nInjected: b"), "single-line value"},
		{"too many headers", tooMany, "at most 32 entries"},
		{"exclusive event types", withBase("--load-from-webhooks-api"), "mutually exclusive"},
		{"skip verify without forward", withBase("--skip-verify"), "requires forward-to"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := newWebhookListenCommand()
			command.SetOut(io.Discard)
			command.SetErr(io.Discard)
			command.SetArgs(test.args)
			if err := command.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want substring %q", err, test.want)
			}
		})
	}
}

func TestWebhookListenSecretLifecycle(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	dir := t.TempDir()
	secretOut := filepath.Join(dir, "webhook-secret")
	command := newWebhookListenCommand()
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	command.SetErr(io.Discard)
	command.SetArgs([]string{"--print-secret", "--secret-out", secretOut})
	if err := command.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	printed := strings.TrimSpace(stdout.String())
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(printed)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("printed secret=%q", printed)
	}
	material, err := os.ReadFile(secretOut) // #nosec G304 -- test-owned path.
	if err != nil || strings.TrimSpace(string(material)) != printed {
		t.Fatal("secret file and printed secret diverged")
	}
	info, err := os.Stat(secretOut)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("secret file is not owner-only")
	}
	replay := newWebhookListenCommand()
	replay.SetOut(io.Discard)
	replay.SetErr(io.Discard)
	replay.SetArgs([]string{"--print-secret", "--secret-out", secretOut})
	if err := replay.ExecuteContext(context.Background()); err == nil {
		t.Fatal("existing secret file permitted replacement")
	}
	read := newWebhookListenCommand()
	var second bytes.Buffer
	read.SetOut(&second)
	read.SetErr(io.Discard)
	read.SetArgs([]string{"--print-secret", "--secret-file", secretOut})
	if err := read.ExecuteContext(context.Background()); err != nil || strings.TrimSpace(second.String()) != printed {
		t.Fatalf("secret-file replay=%q err=%v", second.String(), err)
	}
	missing := newWebhookListenCommand()
	missing.SetOut(io.Discard)
	missing.SetErr(io.Discard)
	missing.SetArgs([]string{"--forward-to", "http://localhost:4242/webhook", "--api-url", "https://core.example.com"})
	if err := missing.ExecuteContext(context.Background()); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("missing secret error=%v", err)
	}
}

func eventID(t *testing.T, event string) string {
	t.Helper()
	start := strings.Index(event, `"id":"`)
	if start < 0 {
		t.Errorf("event has no id: %s", event)
		return ""
	}
	rest := event[start+len(`"id":"`):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Errorf("event id is unterminated: %s", event)
		return ""
	}
	return rest[:end]
}
