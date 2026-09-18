package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Mujhtech/idenqa/internal/access"
)

func TestCancellationLateCredentialFailureIsUnauthenticated(t *testing.T) {
	routes := CancellationRoutes{logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/v1/capture/cancel", nil)
	routes.problem(response, request, access.ErrInvalidCaptureToken)
	if response.Code != http.StatusUnauthorized || response.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("late invalid credential status=%d", response.Code)
	}
}
