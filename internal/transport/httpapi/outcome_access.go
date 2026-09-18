package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type outcomeContextKey struct{}

// OutcomeCredentialAuthenticator is the outcome-token capability consumed by HTTP.
type OutcomeCredentialAuthenticator interface {
	Authenticate(context.Context, string) (verification.OutcomeContext, error)
}

// OutcomeAccessMiddleware authenticates session-bound read-only outcome tokens.
type OutcomeAccessMiddleware struct {
	authenticator OutcomeCredentialAuthenticator
	logger        *slog.Logger
}

// NewOutcomeAccessMiddleware constructs the outcome-token HTTP boundary.
func NewOutcomeAccessMiddleware(
	authenticator OutcomeCredentialAuthenticator,
	logger *slog.Logger,
) (*OutcomeAccessMiddleware, error) {
	if authenticator == nil || logger == nil {
		return nil, errors.New("HTTP outcome access middleware dependencies are required")
	}

	return &OutcomeAccessMiddleware{authenticator: authenticator, logger: logger}, nil
}

// OutcomeContext returns authenticated read-only outcome authority carried by the request.
func OutcomeContext(ctx context.Context) (verification.OutcomeContext, bool) {
	outcomeContext, ok := ctx.Value(outcomeContextKey{}).(verification.OutcomeContext)
	if !ok || outcomeContext.TenantScope().ID().IsZero() || outcomeContext.VerificationID().IsZero() {
		return verification.OutcomeContext{}, false
	}

	return outcomeContext, true
}

// Authenticate accepts exactly one Authorization Bearer outcome credential.
func (middleware *OutcomeAccessMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if middleware == nil || middleware.authenticator == nil || middleware.logger == nil {
			writeAccessProblem(writer, request, nil, errors.New("HTTP outcome access middleware is not initialised"), false)

			return
		}
		encoded, ok := bearerCredential(request.Header.Values("Authorization"))
		if !ok {
			writeOutcomeProblem(writer, request, middleware.logger, access.ErrInvalidOutcomeToken, true)

			return
		}
		outcomeContext, err := middleware.authenticator.Authenticate(request.Context(), encoded)
		if err != nil {
			if errors.Is(err, access.ErrInvalidOutcomeToken) {
				writeOutcomeProblem(writer, request, middleware.logger, err, true)

				return
			}
			middleware.logger.ErrorContext(
				request.Context(),
				"outcome-token authentication failed",
				"request_id", requestIDString(request.Context()),
			)
			writeOutcomeProblem(writer, request, middleware.logger, err, false)

			return
		}
		ctx := context.WithValue(request.Context(), outcomeContextKey{}, outcomeContext)
		next.ServeHTTP(writer, request.WithContext(ctx))
	})
}

func writeOutcomeProblem(
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
		).WithChallenge(`Bearer realm="idenqa-outcome"`)
	}
	writeAccessProblem(writer, request, logger, err, false)
}
