package openapiv1_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestLifecycleStateContractMatchesDomain(t *testing.T) {
	t.Parallel()
	for _, state := range []string{
		"created", "collecting", "awaiting_input", "processing", "awaiting_external",
		"manual_review", "completed", "cancelled", "expired", "failed",
	} {
		t.Run(state, func(t *testing.T) {
			if !verification.SessionState(state).Valid() || !openapiv1.VerificationSessionState(state).Valid() {
				t.Fatalf("workflow state %q is missing from a contract", state)
			}
			payload, err := json.Marshal(map[string]any{"state": state, "version": 2})
			if err != nil {
				t.Fatal(err)
			}
			response, err := openapiv1.ParseGetVerificationResponse(&http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}},
				Body: io.NopCloser(bytes.NewReader(payload)),
			})
			if err != nil || response.JSON200 == nil || string(response.JSON200.State) != state {
				t.Fatalf("client changed workflow state: %+v, %v", response, err)
			}
		})
	}
	for _, outcome := range []string{"verified", "not_verified", "inconclusive", "unknown"} {
		if openapiv1.VerificationSessionState(outcome).Valid() {
			t.Errorf("identity outcome or unknown value %q was accepted as a workflow state", outcome)
		}
	}
}

func TestContractFixturesDecodeGeneratedModels(t *testing.T) {
	t.Parallel()

	var tenant openapiv1.Tenant
	decodeFixture(t, "tenant-active.json", &tenant)
	if _, err := id.ParseTenant(tenant.ID); err != nil {
		t.Fatalf("tenant ID = %q: %v", tenant.ID, err)
	}
	if !tenant.State.Valid() {
		t.Fatalf("tenant state = %q, want known state", tenant.State)
	}

	var page openapiv1.Page
	decodeFixture(t, "page.json", &page)
	if !page.HasMore || page.NextCursor == nil || *page.NextCursor == "" {
		t.Fatalf("page = %+v, want a continuation cursor", page)
	}
}

func TestProblemFixturesCoverStableClasses(t *testing.T) {
	t.Parallel()

	var problems []openapiv1.Problem
	decodeFixture(t, "problems.json", &problems)
	if got, want := len(problems), 15; got != want {
		t.Fatalf("problem count = %d, want %d", got, want)
	}

	seen := make(map[openapiv1.ProblemCode]struct{}, len(problems))
	for _, problem := range problems {
		if !problem.Code.Valid() {
			t.Errorf("problem code = %q, want generated enum member", problem.Code)
		}
		if problem.Status < http.StatusBadRequest || problem.Status > 599 {
			t.Errorf("problem %q status = %d, want 4xx or 5xx", problem.Code, problem.Status)
		}
		if _, err := id.ParseRequest(problem.RequestID); err != nil {
			t.Errorf("problem %q request ID = %q: %v", problem.Code, problem.RequestID, err)
		}
		if _, duplicate := seen[problem.Code]; duplicate {
			t.Errorf("duplicate problem code %q", problem.Code)
		}
		seen[problem.Code] = struct{}{}
	}
}

func TestGeneratedClientDecodesDocumentedTenantResponses(t *testing.T) {
	t.Parallel()

	tenantPayload := fixture(t, "tenant-active.json")
	requestID := "req_01M11HEQG00000000000000000"
	success, err := openapiv1.ParseGetCurrentTenantResponse(&http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": {"application/json"},
			"X-Request-Id": {requestID},
			"Etag":         {`"1"`},
		},
		Body: io.NopCloser(bytes.NewReader(tenantPayload)),
	})
	if err != nil {
		t.Fatalf("ParseGetCurrentTenantResponse(200) error = %v", err)
	}
	if success.JSON200 == nil || success.JSON200.State != openapiv1.TenantStateActive {
		t.Fatalf("200 response = %+v, want active tenant", success.JSON200)
	}

	var fixtures []openapiv1.Problem
	decodeFixture(t, "problems.json", &fixtures)
	byCode := make(map[openapiv1.ProblemCode]openapiv1.Problem, len(fixtures))
	for _, problem := range fixtures {
		byCode[problem.Code] = problem
	}
	tests := []struct {
		name   string
		status int
		code   openapiv1.ProblemCode
	}{
		{name: "unauthenticated", status: http.StatusUnauthorized, code: openapiv1.UNAUTHENTICATED},
		{name: "insufficient scope", status: http.StatusForbidden, code: openapiv1.INSUFFICIENTSCOPE},
		{name: "not found", status: http.StatusNotFound, code: openapiv1.NOTFOUND},
		{name: "rate limited", status: http.StatusTooManyRequests, code: openapiv1.RATELIMITED},
		{name: "internal error", status: http.StatusInternalServerError, code: openapiv1.INTERNALERROR},
		{name: "service unavailable", status: http.StatusServiceUnavailable, code: openapiv1.SERVICEUNAVAILABLE},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload, marshalErr := json.Marshal(byCode[test.code])
			if marshalErr != nil {
				t.Fatalf("marshal problem: %v", marshalErr)
			}
			response, parseErr := openapiv1.ParseGetCurrentTenantResponse(&http.Response{
				StatusCode: test.status,
				Header: http.Header{
					"Content-Type": {"application/problem+json"},
					"X-Request-Id": {requestID},
				},
				Body: io.NopCloser(bytes.NewReader(payload)),
			})
			if parseErr != nil {
				t.Fatalf("ParseGetCurrentTenantResponse(%d) error = %v", test.status, parseErr)
			}
			problem := tenantResponseProblem(response)
			if problem == nil || problem.Code != test.code {
				t.Fatalf("problem = %+v, want code %q", problem, test.code)
			}
		})
	}
}

func tenantResponseProblem(response *openapiv1.GetCurrentTenantResponse) *openapiv1.Problem {
	switch response.StatusCode() {
	case http.StatusUnauthorized:
		return response.ApplicationProblemJSON401
	case http.StatusForbidden:
		return response.ApplicationProblemJSON403
	case http.StatusNotFound:
		return response.ApplicationProblemJSON404
	case http.StatusTooManyRequests:
		return response.ApplicationProblemJSON429
	case http.StatusInternalServerError:
		return response.ApplicationProblemJSON500
	case http.StatusServiceUnavailable:
		return response.ApplicationProblemJSON503
	default:
		return nil
	}
}

func decodeFixture(t *testing.T, name string, destination any) {
	t.Helper()

	if err := json.Unmarshal(fixture(t, name), destination); err != nil {
		t.Fatalf("decode fixture %q: %v", name, err)
	}
}

func fixture(t *testing.T, name string) []byte {
	t.Helper()

	directory := filepath.Join(
		"..",
		"..",
		"..",
		"..",
		"contracts",
		"api",
		"openapi",
		"v1",
		"fixtures",
	)
	root, err := os.OpenRoot(directory)
	if err != nil {
		t.Fatalf("open fixture root: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := root.Close(); closeErr != nil {
			t.Errorf("close fixture root: %v", closeErr)
		}
	})
	payload, err := root.ReadFile(name)
	if err != nil {
		t.Fatalf("read fixture %q: %v", name, err)
	}

	return payload
}
