package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/audit"
)

type auditReaderStub struct {
	before uint64
	limit  int
}

func (reader *auditReaderStub) List(_ context.Context, authority access.Context, before uint64, limit int) ([]audit.Record, error) {
	if err := authority.Require(access.PermissionAuditExport); err != nil {
		return nil, err
	}
	reader.before, reader.limit = before, limit
	return []audit.Record{{Sequence: 9, EventID: "evt_example", EventType: "review.case.assigned", AggregateID: "rvc_example", ActorID: "key_example", OccurredAt: time.Date(2026, 9, 29, 8, 0, 0, 0, time.UTC), EventDigest: strings.Repeat("a", 64), PreviousHash: strings.Repeat("b", 64), Hash: strings.Repeat("c", 64)}}, nil
}

func TestAuditRoutesReturnReferenceOnlyMetadata(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("audit:export"))
	reader := &auditReaderStub{}
	routes, err := NewAuditRoutes(fixture.middleware, reader, fixture.logger)
	if err != nil {
		t.Fatalf("NewAuditRoutes() error = %v", err)
	}
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/audit-records?before=10&limit=1", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()
	versionedRouter(t, routes).ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d: %s", response.Code, http.StatusOK, response.Body.String())
	}
	if reader.before != 10 || reader.limit != 1 {
		t.Fatalf("query = before %d limit %d", reader.before, reader.limit)
	}
	var body struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0]["event_id"] != "evt_example" {
		t.Fatalf("data = %#v", body.Data)
	}
	for _, forbidden := range []string{"event_digest", "previous_hash", "hash"} {
		if _, exists := body.Data[0][forbidden]; exists {
			t.Fatalf("response disclosed %s", forbidden)
		}
	}
}
