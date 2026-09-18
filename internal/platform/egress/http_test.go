package egress_test

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/egress"
)

func TestEgressOriginAndRedirectRestrictions(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "https://example.com/private", http.StatusFound)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.TLS.Certificates[0].Certificate[0]}), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := egress.NewClient(server.URL, ca, true)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	for _, test := range []struct {
		url       string
		wantError bool
	}{{server.URL, false}, {server.URL + "/redirect", true}, {"https://example.com/private", true}} {
		req, err := http.NewRequestWithContext(context.Background(), "GET", test.url, nil)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Do(req)
		if response != nil {
			_ = response.Body.Close()
		}
		if (err != nil) != test.wantError {
			t.Fatalf("%s error=%v", test.url, err)
		}
	}
	public, err := egress.NewClient(server.URL, ca, false)
	if err != nil {
		t.Fatal(err)
	}
	defer public.CloseIdleConnections()
	req, _ := http.NewRequestWithContext(t.Context(), "GET", server.URL, nil)
	response, err := public.Do(req)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("non-fixture egress reached loopback")
	}
	for _, origin := range []string{"http://127.0.0.1", "https://user:password@example.com", "https://example.com/path", "https://example.com?secret=value"} {
		if _, err := egress.NewClient(origin, "", false); err == nil {
			t.Fatalf("accepted unsafe origin: %s", origin)
		}
	}
}
