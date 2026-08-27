package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
)

const requestIDHeader = "X-Request-ID"

type requestIDContextKey struct{}

// RequestIDGenerator is the narrow identifier capability consumed by the HTTP
// boundary.
type RequestIDGenerator interface {
	NewRequest() (id.Request, error)
}

// RequestID returns the server-issued request identifier from ctx.
func RequestID(ctx context.Context) (id.Request, bool) {
	requestID, ok := ctx.Value(requestIDContextKey{}).(id.Request)

	return requestID, ok
}

func requestIDString(ctx context.Context) string {
	requestID, ok := RequestID(ctx)
	if !ok {
		return ""
	}

	return requestID.String()
}

func requestIDs(generator RequestIDGenerator, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			requestID, err := generator.NewRequest()
			if err != nil {
				logger.ErrorContext(request.Context(), "request ID generation failed")
				if writeErr := respond.WriteProblem(writer, request, err, ""); writeErr != nil {
					logger.ErrorContext(request.Context(), "write request ID failure response")
				}

				return
			}

			writer.Header().Set(requestIDHeader, requestID.String())
			ctx := context.WithValue(request.Context(), requestIDContextKey{}, requestID)
			next.ServeHTTP(writer, request.WithContext(ctx))
		})
	}
}
