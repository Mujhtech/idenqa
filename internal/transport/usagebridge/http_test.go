package usagebridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	usagev1 "github.com/Mujhtech/idenqa/contracts/usage/v1"
)

type testStore struct{ calls int }

func (s *testStore) Read(context.Context, string, int) ([]usagev1.Receipt, error) {
	s.calls++
	return []usagev1.Receipt{}, nil
}
func (s *testStore) Acknowledge(context.Context, string, string, string, time.Time) error {
	s.calls++
	return nil
}
func TestReceiptBridgeRejectsUnboundedOrUnrelatedInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, method, path, body string
		status                   int
	}{
		{"valid", http.MethodPost, "/local/v1/usage/receipts/read", `{"coreTenantId":"ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH","limit":10}`, http.StatusOK},
		{"unknown-field", http.MethodPost, "/local/v1/usage/receipts/read", `{"coreTenantId":"ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH","secret":"hidden"}`, http.StatusBadRequest},
		{"trailing-json", http.MethodPost, "/local/v1/usage/receipts/read", `{"coreTenantId":"ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}{}`, http.StatusBadRequest},
		{"bad-tenant", http.MethodPost, "/local/v1/usage/receipts/read", `{"coreTenantId":"other","limit":10}`, http.StatusBadRequest},
		{"wrong-method", http.MethodGet, "/local/v1/usage/receipts/read", `{}`, http.StatusNotFound},
		{"arbitrary-route", http.MethodPost, "/local/v1/usage/receipts/delete", `{}`, http.StatusNotFound},
		{"oversize", http.MethodPost, "/local/v1/usage/receipts/read", strings.Repeat(" ", 9000) + `{}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &testStore{}
			handler, err := NewHandler(store, time.Now)
			if err != nil {
				t.Fatal(err)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body)))
			if response.Code != tc.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if tc.status != http.StatusOK && store.calls != 0 {
				t.Fatal("rejected request reached receipt store")
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("receipt response may be cached")
			}
		})
	}
}
