package admissionbridge

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
)

type summaryFixture struct{ calls int }

func (*summaryFixture) Install(context.Context, admissionv1.Snapshot) error { return nil }
func (s *summaryFixture) Summary(_ context.Context, _ admissionv1.Scope) (admissionv1.Summary, error) {
	s.calls++
	return admissionv1.Summary{Revision: 3, Status: "suspended", Limits: []admissionv1.CounterSummary{{Kind: "count", Key: "work", Maximum: 2, Used: 1, Remaining: 1}}}, nil
}

func TestSummaryBoundary(t *testing.T) {
	fixture := &summaryFixture{}
	h, err := NewHandler(fixture)
	if err != nil {
		t.Fatal(err)
	}
	h.WithSummary(fixture)
	for _, test := range []struct {
		name, body, query, header string
		status                    int
	}{
		{"read during suspension", `{"tenantId":"tenant","deploymentId":"dep","region":"region"}`, "", "", 200},
		{"unknown field", `{"tenantId":"tenant","deploymentId":"dep","region":"region","operationId":"forbidden"}`, "", "", 400},
		{"trailing", `{"tenantId":"tenant","deploymentId":"dep","region":"region"}{}`, "", "", 400},
		{"query", `{}`, "?tenantId=tenant", "", 400},
		{"browser", `{}`, "", "Origin", 400},
		{"cookie", `{}`, "", "Cookie", 400},
		{"bearer", `{}`, "", "Authorization", 400},
		{"oversize", strings.Repeat(" ", 4097), "", "", 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/local/v1/admission/summary"+test.query, strings.NewReader(test.body))
			if test.header != "" {
				r.Header.Set(test.header, "test")
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != test.status {
				t.Fatalf("%d: %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("cache permitted")
			}
		})
	}
	if fixture.calls != 1 {
		t.Fatalf("unexpected service calls %d", fixture.calls)
	}
}
