// Package httpapi owns shared public HTTP routing and middleware contracts.
package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/telemetry"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
	"github.com/riandyrn/otelchi"
	otelchimetric "github.com/riandyrn/otelchi/metric"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// Dependencies are the process-owned facilities used by the HTTP boundary.
type Dependencies struct {
	Logger         *slog.Logger
	IDs            RequestIDGenerator
	TracerProvider trace.TracerProvider
	MeterProvider  metric.MeterProvider
	RequestTimeout time.Duration
	MaxBodyBytes   int64
	// EvidenceUploadPolicy enables the narrowly matched raw evidence-ingress
	// body and attempt limits. Nil keeps the larger upload exception disabled.
	EvidenceUploadPolicy *evidence.UploadPolicy
	AllowedOrigins       []string
}

// VersionPrefix is the public API major-version path boundary. Public route
// groups register paths relative to it; process composition mounts it exactly
// once so the version appears in a single place.
const VersionPrefix = "/v1"

// RegisterRoutes adds application routes to router.
type RegisterRoutes func(router chi.Router)

// New constructs the shared HTTP boundary and registers routes.
func New(dependencies Dependencies, register RegisterRoutes) (http.Handler, error) {
	if dependencies.Logger == nil || dependencies.IDs == nil || dependencies.TracerProvider == nil ||
		dependencies.MeterProvider == nil {
		return nil, errors.New("HTTP logger, identifier generator, and telemetry providers are required")
	}
	if dependencies.RequestTimeout <= 0 || dependencies.MaxBodyBytes <= 0 {
		return nil, errors.New("HTTP request timeout and maximum body bytes must be greater than zero")
	}
	if dependencies.EvidenceUploadPolicy != nil &&
		(dependencies.EvidenceUploadPolicy.MaximumBytes() == 0 ||
			dependencies.EvidenceUploadPolicy.AttemptTimeout() == 0) {
		return nil, errors.New("HTTP evidence upload policy is invalid")
	}
	if register == nil {
		return nil, errors.New("HTTP route registrar is required")
	}

	router := chi.NewRouter()
	router.Use(requestIDs(dependencies.IDs, dependencies.Logger))
	router.Use(connectionDeadlines(
		dependencies.RequestTimeout,
		dependencies.EvidenceUploadPolicy,
		dependencies.Logger,
	))
	router.Use(otelchi.Middleware(
		"idenqa-api",
		otelchi.WithChiRoutes(router),
		otelchi.WithTracerProvider(dependencies.TracerProvider),
		otelchi.WithPropagators(propagation.TraceContext{}),
		otelchi.WithPublicEndpoint(),
	))

	metricConfig := otelchimetric.NewBaseConfig(
		"idenqa-api",
		otelchimetric.WithMeterProvider(dependencies.MeterProvider),
		otelchimetric.WithAttributesFunc(telemetry.HTTPMetricAttributes),
	)
	router.Use(otelchimetric.NewServerActiveRequests(metricConfig))
	router.Use(otelchimetric.NewServerRequestDuration(metricConfig))
	router.Use(otelchimetric.NewServerRequestBodySize(metricConfig))
	router.Use(otelchimetric.NewServerResponseBodySize(metricConfig))
	router.Use(recoverPanics(dependencies.Logger))
	router.Use(secureResponseHeaders)
	router.Use(cors(dependencies.AllowedOrigins))
	router.Use(limitBody(
		dependencies.MaxBodyBytes,
		dependencies.EvidenceUploadPolicy,
		dependencies.Logger,
	))
	router.Use(defaultDeadline(
		dependencies.RequestTimeout,
		dependencies.EvidenceUploadPolicy,
		dependencies.Logger,
	))

	router.NotFound(problemHandler(dependencies.Logger, apierror.New(
		http.StatusNotFound,
		apierror.CodeNotFound,
		"Not found",
		"The requested resource was not found.",
		nil,
	)))
	router.MethodNotAllowed(problemHandler(dependencies.Logger, apierror.New(
		http.StatusMethodNotAllowed,
		apierror.CodeMethodNotAllowed,
		"Method not allowed",
		"The request method is not supported for this resource.",
		nil,
	)))
	register(router)

	return router, nil
}

func problemHandler(logger *slog.Logger, failure error) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		if err := respond.WriteProblem(
			writer,
			request,
			failure,
			requestIDString(request.Context()),
		); err != nil {
			logger.ErrorContext(request.Context(), "write HTTP problem response")
		}
	}
}
