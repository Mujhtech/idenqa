package httpapi

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
)

type failedProblemWriter struct{ header http.Header }

func (writer failedProblemWriter) Header() http.Header { return writer.header }
func (failedProblemWriter) WriteHeader(int)            {}
func (failedProblemWriter) Write([]byte) (int, error) {
	return 0, errors.New("sensitive response data\r\nforged write failure\x1b[2J")
}

func TestHandlerProblemLogsOnlySafeClassification(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusBadRequest, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			base := newHandlerBase(slog.New(slog.NewTextHandler(&logs, nil)), "capture profile")
			cause := errors.New("sensitive request content\r\nforged entry\x1b[2J\u2028")
			failure := apierror.New(status, apierror.CodeInvalidRequest, "Invalid request", "Safe detail.", cause)
			base.problem(failedProblemWriter{header: make(http.Header)}, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil), failure)
			output := logs.String()
			if !strings.Contains(output, "code=INVALID_REQUEST") || strings.Count(output, "\n") != 2 {
				t.Fatalf("expected one classification and one content-free write failure: %q", output)
			}
			for _, forbidden := range []string{"sensitive", "forged", "error=", "\r", "\x1b", "\u2028"} {
				if strings.Contains(output, forbidden) {
					t.Fatalf("problem log disclosed unsafe content %q: %q", forbidden, output)
				}
			}
		})
	}
}

func TestHandlerProblemRejectsUnsafeLogCodes(t *testing.T) {
	t.Parallel()
	for _, code := range []string{"", "bad\r\nFORGED", "BAD\x00CODE", "BAD\x1b[2J", "BAD\u2028CODE", "BAD\u202eCODE", "<script>", strings.Repeat("A", 65)} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			base := newHandlerBase(slog.New(slog.NewTextHandler(&logs, nil)), "verification")
			failure := apierror.New(http.StatusBadRequest, code, "Invalid request", "Safe detail.", nil)
			response := httptest.NewRecorder()
			base.problem(response, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", nil), failure)
			if !strings.Contains(logs.String(), "code=INTERNAL_ERROR") || strings.Count(logs.String(), "\n") != 1 {
				t.Fatalf("unsafe public code entered logs: %q", logs.String())
			}
			if response.Code != http.StatusBadRequest {
				t.Fatalf("classification sanitization changed response status: %d", response.Code)
			}
		})
	}
}

func TestHandlerProblemOmitsUnclassifiedErrorContent(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	base := newHandlerBase(slog.New(slog.NewTextHandler(&logs, nil)), "verification")
	base.problem(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil), errors.New("credential=secret\nforged"))
	if strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "forged") || !strings.Contains(logs.String(), "code=INTERNAL_ERROR") {
		t.Fatalf("unclassified failure content entered logs: %q", logs.String())
	}
}
