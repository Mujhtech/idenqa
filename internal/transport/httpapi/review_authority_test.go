package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReviewRequestsRejectAssertedAuthority(t *testing.T) {
	for _, body := range []string{`{"expected_version":1,"reviewer_id":"other"}`, `{"expected_version":1,"certifications":["document.level2"]}`} {
		request := httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		if _, err := decodeJSONBody[reviewerRequest](request); err == nil {
			t.Fatal("accepted caller-supplied reviewer authority")
		}
	}
	request := httptest.NewRequestWithContext(t.Context(), "POST", "/", strings.NewReader(`{"expected_version":1}`))
	request.Header.Set("Content-Type", "application/json")
	if _, err := decodeJSONBody[reviewerRequest](request); err != nil {
		t.Fatal(err)
	}
}
