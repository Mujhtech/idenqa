package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

const (
	evidenceUploadPathPrefix = "/v1/evidence-uploads/"
	requestIOGrace           = 5 * time.Second
)

func recoverPanics(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			wrapped := chimiddleware.NewWrapResponseWriter(writer, request.ProtoMajor)
			defer func() {
				if recover() == nil {
					return
				}

				logger.ErrorContext(
					request.Context(),
					"request panic recovered",
					"request_id", requestIDString(request.Context()),
				)
				if wrapped.Status() != 0 {
					return
				}
				if err := respond.WriteProblem(
					wrapped,
					request,
					errors.New("handler panic"),
					requestIDString(request.Context()),
				); err != nil {
					logger.ErrorContext(request.Context(), "write panic response")
				}
			}()

			next.ServeHTTP(wrapped, request)
		})
	}
}

func secureResponseHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(writer, request)
	})
}

func connectionDeadlines(
	ordinaryTimeout time.Duration,
	uploadPolicy *evidence.UploadPolicy,
	logger *slog.Logger,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if isLongLivedRequest(request) {
				next.ServeHTTP(writer, request)

				return
			}

			deadline := time.Now().Add(requestTimeout(request, ordinaryTimeout, uploadPolicy) + requestIOGrace)
			controller := http.NewResponseController(writer)
			if err := controller.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
				logger.WarnContext(request.Context(), "set HTTP request read deadline")
			}
			if err := controller.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
				logger.WarnContext(request.Context(), "set HTTP request write deadline")
			}

			next.ServeHTTP(writer, request)
		})
	}
}

func limitBody(
	ordinaryMaximumBytes int64,
	uploadPolicy *evidence.UploadPolicy,
	logger *slog.Logger,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			maxBytes := requestMaximumBytes(request, ordinaryMaximumBytes, uploadPolicy)
			if request.ContentLength > maxBytes {
				failure := apierror.New(
					http.StatusRequestEntityTooLarge,
					apierror.CodeRequestTooLarge,
					"Request too large",
					"The request body exceeds the permitted size.",
					nil,
				)
				if err := respond.WriteProblem(
					writer,
					request,
					failure,
					requestIDString(request.Context()),
				); err != nil {
					logger.ErrorContext(request.Context(), "write request-size response")
				}

				return
			}

			request.Body = http.MaxBytesReader(writer, request.Body, maxBytes)
			next.ServeHTTP(writer, request)
		})
	}
}

func defaultDeadline(
	ordinaryTimeout time.Duration,
	uploadPolicy *evidence.UploadPolicy,
	logger *slog.Logger,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if isLongLivedRequest(request) {
				next.ServeHTTP(writer, request)

				return
			}

			timeout := requestTimeout(request, ordinaryTimeout, uploadPolicy)
			ctx, cancel := context.WithTimeout(request.Context(), timeout)
			defer cancel()

			wrapped := chimiddleware.NewWrapResponseWriter(writer, request.ProtoMajor)
			next.ServeHTTP(wrapped, request.WithContext(ctx))
			if ctx.Err() == nil || wrapped.Status() != 0 {
				return
			}
			if err := respond.WriteProblem(
				wrapped,
				request,
				ctx.Err(),
				requestIDString(request.Context()),
			); err != nil {
				logger.ErrorContext(request.Context(), "write request-deadline response")
			}
		})
	}
}

func requestMaximumBytes(
	request *http.Request,
	ordinaryMaximumBytes int64,
	uploadPolicy *evidence.UploadPolicy,
) int64 {
	if uploadPolicy != nil && isEvidenceUploadBodyRequest(request) {
		return uploadPolicy.MaximumBytes()
	}

	return ordinaryMaximumBytes
}

func requestTimeout(
	request *http.Request,
	ordinaryTimeout time.Duration,
	uploadPolicy *evidence.UploadPolicy,
) time.Duration {
	if uploadPolicy != nil && isEvidenceUploadBodyRequest(request) {
		return uploadPolicy.AttemptTimeout()
	}

	return ordinaryTimeout
}

func isEvidenceUploadBodyRequest(request *http.Request) bool {
	if request.Method != http.MethodPut || !strings.HasPrefix(request.URL.Path, evidenceUploadPathPrefix) {
		return false
	}
	rawID := strings.TrimPrefix(request.URL.Path, evidenceUploadPathPrefix)
	if rawID == "" || strings.Contains(rawID, "/") {
		return false
	}
	_, err := id.ParseUpload(rawID)

	return err == nil
}

// isLongLivedRequest exempts transports that own their own bounded deadline,
// write window, heartbeats, or size guard from the ordinary request timeout.
func isLongLivedRequest(request *http.Request) bool {
	return isWebSocketUpgrade(request) || isEventStreamRequest(request) || isTenantExportRequest(request)
}

func isWebSocketUpgrade(request *http.Request) bool {
	if !strings.EqualFold(request.Header.Get("Upgrade"), "websocket") {
		return false
	}
	for _, token := range strings.Split(request.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(token), "upgrade") {
			return true
		}
	}

	return false
}
