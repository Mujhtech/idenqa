package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/realtime"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// CaptureConnectionIssuer is the browser-ticket capability consumed by HTTP.
type CaptureConnectionIssuer interface {
	Issue(context.Context, realtime.IssueInput) (realtime.IssuedTicket, error)
}

// CaptureConnectionRoutes adapts authenticated browser ticket issuance to HTTP.
type CaptureConnectionRoutes struct {
	capture        *CaptureAccessMiddleware
	issuer         CaptureConnectionIssuer
	endpoint       url.URL
	region         string
	allowedOrigins map[string]struct{}
	logger         *slog.Logger
}

// NewCaptureConnectionRoutes constructs the display-once browser bootstrap surface.
func NewCaptureConnectionRoutes(
	capture *CaptureAccessMiddleware,
	issuer CaptureConnectionIssuer,
	endpoint string,
	region string,
	allowedOrigins []string,
	logger *slog.Logger,
) (*CaptureConnectionRoutes, error) {
	parsed, err := url.Parse(endpoint)
	if capture == nil || issuer == nil || logger == nil || err != nil ||
		(parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" ||
		parsed.User != nil || parsed.Path != "/v1/capture/socket" || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.String() != endpoint || region == "" || len(allowedOrigins) == 0 {
		return nil, errors.New("capture connection route dependencies are invalid")
	}
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if _, duplicate := origins[origin]; duplicate {
			return nil, errors.New("capture connection allowed origins contain duplicates")
		}
		if _, err := realtime.NewBrowserBinding(origin); err != nil {
			return nil, errors.New("capture connection allowed origin is invalid")
		}
		origins[origin] = struct{}{}
	}

	return &CaptureConnectionRoutes{
		capture: capture, issuer: issuer, endpoint: *parsed, region: region,
		allowedOrigins: origins, logger: logger,
	}, nil
}

// Register adds browser connection-ticket issuance.
func (routes *CaptureConnectionRoutes) Register(router chi.Router) {
	router.With(routes.capture.Authenticate).Post("/capture/connections", routes.issue)
}

func (routes *CaptureConnectionRoutes) issue(writer http.ResponseWriter, request *http.Request) {
	captureContext, ok := CaptureContext(request.Context())
	if !ok {
		writeCaptureProblem(writer, request, routes.logger, accessFailure(), true)

		return
	}
	binding, ok := routes.browserBinding(request.Header.Values("Origin"))
	if !ok {
		failure := apierror.New(
			http.StatusForbidden,
			apierror.CodeCaptureOriginNotAllowed,
			"Capture origin not allowed",
			"The browser origin is not permitted for realtime capture.",
			nil,
		)
		if err := respond.WriteProblem(writer, request, failure, requestIDString(request.Context())); err != nil {
			routes.logger.ErrorContext(request.Context(), "write capture connection origin problem response")
		}

		return
	}
	issued, err := routes.issuer.Issue(request.Context(), realtime.IssueInput{
		Scope: captureContext.TenantScope(), VerificationID: captureContext.Session().ID(),
		CaptureTokenID: captureContext.TokenID(), Binding: binding, Region: routes.region,
		SessionExpiresAt: captureContext.Session().ExpiresAt(),
	})
	if errors.Is(err, realtime.ErrTicketUnavailable) {
		writeCaptureProblem(writer, request, routes.logger, err, true)

		return
	}
	if err != nil {
		if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
			routes.logger.ErrorContext(request.Context(), "write capture connection problem response")
		}

		return
	}

	connectionURL := routes.endpoint
	query := make(url.Values, 1)
	query.Set("ticket", issued.Credential.Reveal())
	connectionURL.RawQuery = query.Encode()
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, http.StatusCreated, openapiv1.CaptureConnection{
		WebsocketURL: connectionURL.String(), Protocol: realtime.SubprotocolV1,
		ExpiresAt: issued.Ticket.ExpiresAt().UTC().Truncate(time.Microsecond),
	}); err != nil {
		routes.logger.ErrorContext(request.Context(), "write capture connection response")
	}
}

func (routes *CaptureConnectionRoutes) browserBinding(values []string) (realtime.ClientBinding, bool) {
	if len(values) != 1 {
		return realtime.ClientBinding{}, false
	}
	if _, allowed := routes.allowedOrigins[values[0]]; !allowed {
		return realtime.ClientBinding{}, false
	}
	binding, err := realtime.NewBrowserBinding(values[0])

	return binding, err == nil
}
