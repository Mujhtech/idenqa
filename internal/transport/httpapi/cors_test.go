package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCaptureUploadPreflightAllowsRequiredIntegrityHeader(t *testing.T) {
	t.Parallel()
	allowed := newRouterHarness(t, time.Second, []string{"https://capture.example"})
	for _, test := range []struct {
		name, origin string
		permitted    bool
	}{
		{"exact capture origin", "https://capture.example", true},
		{"other origin", "https://other.example", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(t.Context(), http.MethodOptions, "/v1/evidence-uploads/upl_test/content", nil)
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Access-Control-Request-Method", http.MethodPut)
			request.Header.Set("Access-Control-Request-Headers", "Authorization, Content-Type, Content-Digest, If-Match")
			response := httptest.NewRecorder()
			allowed.handler.ServeHTTP(response, request)
			if !test.permitted {
				if response.Header().Get("Access-Control-Allow-Origin") != "" {
					t.Fatal("unapproved capture origin accepted")
				}
				return
			}
			if response.Header().Get("Access-Control-Allow-Origin") != test.origin || !strings.Contains(strings.ToLower(response.Header().Get("Access-Control-Allow-Headers")), "content-digest") {
				t.Fatal("browser cannot send the upload integrity header")
			}
			if response.Header().Get("Access-Control-Allow-Credentials") != "" {
				t.Fatal("capture unexpectedly permits browser cookies")
			}
		})
	}
}
