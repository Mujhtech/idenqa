package idenqa

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func TestExportTenantCommandCancellation(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "export-credential")
	handlerDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer close(handlerDone)
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte("{\"record\":\"header\"}\n"))
		if flusher, ok := writer.(http.Flusher); ok {
			flusher.Flush()
		}
		<-request.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	command := newRootCommand(buildinfo.Info{})
	command.SetArgs([]string{"export", "tenant", "--output", "-", "--api-url", server.URL})
	command.SetContext(ctx)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	done := make(chan error, 1)
	go func() { done <- command.ExecuteContext(ctx) }()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled export returned nil")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled export did not stop")
	}
	select {
	case <-handlerDone:
	case <-time.After(5 * time.Second):
		t.Fatal("server handler did not observe cancellation")
	}
}
