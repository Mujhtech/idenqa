package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type captureContextKey struct{}

// CaptureCredentialAuthenticator is the capture-token capability consumed by HTTP.
type CaptureCredentialAuthenticator interface {
	Authenticate(context.Context, string) (verification.CaptureContext, error)
}

// CaptureAccessMiddleware authenticates session-bound capture tokens.
type CaptureAccessMiddleware struct {
	authenticator CaptureCredentialAuthenticator
	logger        *slog.Logger
}

// NewCaptureAccessMiddleware constructs the capture-token HTTP boundary.
func NewCaptureAccessMiddleware(
	authenticator CaptureCredentialAuthenticator,
	logger *slog.Logger,
) (*CaptureAccessMiddleware, error) {
	if authenticator == nil || logger == nil {
		return nil, errors.New("HTTP capture access middleware dependencies are required")
	}

	return &CaptureAccessMiddleware{authenticator: authenticator, logger: logger}, nil
}

// CaptureContext returns authenticated capture authority carried by the request.
func CaptureContext(ctx context.Context) (verification.CaptureContext, bool) {
	captureContext, ok := ctx.Value(captureContextKey{}).(verification.CaptureContext)
	if !ok || captureContext.TenantScope().ID().IsZero() || captureContext.Session().ID().IsZero() {
		return verification.CaptureContext{}, false
	}

	return captureContext, true
}

// Authenticate accepts exactly one Authorization Bearer credential.
func (middleware *CaptureAccessMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if middleware == nil || middleware.authenticator == nil || middleware.logger == nil {
			writeAccessProblem(writer, request, nil, errors.New("HTTP capture access middleware is not initialised"), false)

			return
		}
		encoded, ok := bearerCredential(request.Header.Values("Authorization"))
		if !ok {
			writeCaptureProblem(writer, request, middleware.logger, access.ErrInvalidCaptureToken, true)

			return
		}
		captureContext, err := middleware.authenticator.Authenticate(request.Context(), encoded)
		if err != nil {
			if errors.Is(err, access.ErrInvalidCaptureToken) {
				writeCaptureProblem(writer, request, middleware.logger, err, true)

				return
			}
			middleware.logger.ErrorContext(
				request.Context(),
				"capture-token authentication failed",
				"request_id", requestIDString(request.Context()),
			)
			writeCaptureProblem(writer, request, middleware.logger, err, false)

			return
		}
		ctx := context.WithValue(request.Context(), captureContextKey{}, captureContext)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func writeCaptureProblem(
	writer http.ResponseWriter,
	request *http.Request,
	logger *slog.Logger,
	err error,
	challenge bool,
) {
	if challenge {
		err = captureUnauthenticated(err)
	}
	writeAccessProblem(writer, request, logger, err, false)
}
