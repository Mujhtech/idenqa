package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
)

type accessContextKey struct{}

// CredentialAuthenticator is the narrow authentication capability consumed by
// the HTTP boundary.
type CredentialAuthenticator interface {
	Authenticate(context.Context, string) (access.Context, error)
}

// AccessMiddleware adapts transport credentials to access.Context and performs
// optional early route permission rejection. Application services must still
// authorise the operation themselves.
type AccessMiddleware struct {
	authenticator CredentialAuthenticator
	logger        *slog.Logger
}

// NewAccessMiddleware constructs the tenant API-key HTTP boundary.
func NewAccessMiddleware(authenticator CredentialAuthenticator, logger *slog.Logger) (*AccessMiddleware, error) {
	if authenticator == nil || logger == nil {
		return nil, errors.New("HTTP access middleware dependencies are required")
	}

	return &AccessMiddleware{authenticator: authenticator, logger: logger}, nil
}

// AccessContext returns authenticated authority carried by the request.
func AccessContext(ctx context.Context) (access.Context, bool) {
	accessContext, ok := ctx.Value(accessContextKey{}).(access.Context)
	if !ok || accessContext.TenantScope().ID().IsZero() || accessContext.Principal().KeyID().IsZero() {
		return access.Context{}, false
	}

	return accessContext, true
}

// Authenticate accepts only one Authorization header using the Bearer scheme.
func (middleware *AccessMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if middleware == nil || middleware.authenticator == nil || middleware.logger == nil {
			writeAccessProblem(writer, request, nil, errors.New("HTTP access middleware is not initialised"), false)

			return
		}

		encoded, ok := bearerCredential(request.Header.Values("Authorization"))
		if !ok {
			writeAccessProblem(writer, request, middleware.logger, access.ErrInvalidCredential, true)

			return
		}
		accessContext, err := middleware.authenticator.Authenticate(request.Context(), encoded)
		if err != nil {
			if errors.Is(err, access.ErrInvalidCredential) {
				writeAccessProblem(writer, request, middleware.logger, err, true)

				return
			}

			middleware.logger.ErrorContext(
				request.Context(),
				"API key authentication failed",
				"request_id", requestIDString(request.Context()),
			)
			writeAccessProblem(writer, request, middleware.logger, err, false)

			return
		}

		ctx := context.WithValue(request.Context(), accessContextKey{}, accessContext)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

// Require rejects a successfully authenticated request that lacks permission.
// It is defence in depth and does not replace application-service checks.
// Routes normally use Authorize instead; Require remains for callers that have
// already authenticated in an outer middleware.
func (middleware *AccessMiddleware) Require(permission access.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			if middleware == nil || middleware.logger == nil {
				writeAccessProblem(writer, request, nil, errors.New("HTTP access middleware is not initialised"), false)

				return
			}
			accessContext, ok := AccessContext(request.Context())
			if !ok {
				writeAccessProblem(writer, request, middleware.logger, access.ErrInvalidCredential, true)

				return
			}
			if err := accessContext.Require(permission); err != nil {
				writeAccessProblem(writer, request, middleware.logger, err, false)

				return
			}

			next.ServeHTTP(writer, request)
		})
	}
}

// Authorize authenticates the request and rejects a credential that lacks
// permission before the handler runs. Pairing both checks in one middleware
// makes it impossible to register a permission check without authentication.
func (middleware *AccessMiddleware) Authorize(permission access.Permission) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return middleware.Authenticate(middleware.Require(permission)(next))
	}
}

func bearerCredential(values []string) (string, bool) {
	if len(values) != 1 {
		return "", false
	}
	header := values[0]
	scheme, encoded, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "Bearer") || encoded == "" ||
		strings.ContainsAny(encoded, " \t\r\n") {
		return "", false
	}

	return encoded, true
}

func writeAccessProblem(
	writer http.ResponseWriter,
	request *http.Request,
	logger *slog.Logger,
	err error,
	challenge bool,
) {
	if challenge {
		err = apierror.New(
			http.StatusUnauthorized,
			apierror.CodeUnauthenticated,
			"Unauthenticated",
			"Authentication is required.",
			err,
		).WithChallenge(`Bearer realm="idenqa"`)
	}
	if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil && logger != nil {
		logger.ErrorContext(request.Context(), "write access failure response")
	}
}
