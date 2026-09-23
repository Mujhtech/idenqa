package idenqa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientRejectsCredentialRedirectAndAbsolutePaths(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("redirect followed") }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	caller := server.Client()
	client, err := NewClient(server.URL, "fixture", caller)
	if err != nil {
		t.Fatal(err)
	}
	if caller.CheckRedirect != nil {
		t.Fatal("modified caller's client")
	}
	err = client.DoJSON(t.Context(), "GET", "v1/test", nil, nil)
	var problem *APIError
	if !errors.As(err, &problem) || problem.StatusCode != 307 {
		t.Fatalf("redirect result = %v", err)
	}
	for _, path := range []string{target.URL, "//example.test/path", "v1/%2e%2e/private"} {
		if err := client.DoJSON(t.Context(), "GET", path, nil, nil); err == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
}

func TestClientCancellationAndBoundedDecoding(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			_, _ = io.WriteString(w, strings.Repeat("x", maximumResponseBytes+1))
		case "/trailing":
			_, _ = io.WriteString(w, `{} {}`)
		default:
			_, _ = io.WriteString(w, `{"id":"evd_fixture","future":true}`)
		}
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "fixture", server.Client())
	if err := client.DoJSON(ctx, "GET", "cancelled", nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation = %v", err)
	}
	for _, path := range []string{"large", "trailing"} {
		var result EvidenceMetadata
		if err := client.DoJSON(t.Context(), "GET", path, nil, &result); err == nil {
			t.Fatalf("accepted %s", path)
		}
	}
	var result EvidenceMetadata
	if err := client.DoJSON(t.Context(), "GET", "valid", nil, &result); err != nil || result.ID != "evd_fixture" {
		t.Fatalf("additive response = %+v, %v", result, err)
	}
}
