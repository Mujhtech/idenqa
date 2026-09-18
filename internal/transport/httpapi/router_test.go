package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/telemetry"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type routerClock struct{}

func (routerClock) Now() time.Time {
	return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
}

// versionedRouter mounts route groups below the public API version prefix,
// matching process composition.
func versionedRouter(t *testing.T, registrars ...interface{ Register(chi.Router) }) chi.Router {
	t.Helper()

	router := chi.NewRouter()
	router.Route(VersionPrefix, func(versioned chi.Router) {
		for _, registrar := range registrars {
			registrar.Register(versioned)
		}
	})

	return router
}

type routerHarness struct {
	handler   http.Handler
	logs      *bytes.Buffer
	spans     *tracetest.SpanRecorder
	metrics   *sdkmetric.ManualReader
	cancelled chan struct{}
	uploadTTL chan time.Duration
}

func newRouterHarness(t *testing.T, timeout time.Duration, origins []string) routerHarness {
	t.Helper()

	return newRouterHarnessWithUploadPolicy(t, timeout, origins, nil)
}

func newRouterHarnessWithUploadPolicy(
	t *testing.T,
	timeout time.Duration,
	origins []string,
	uploadPolicy *evidence.UploadPolicy,
) routerHarness {
	t.Helper()

	identifiers, err := id.NewGenerator(routerClock{}, bytes.NewReader(bytes.Repeat([]byte{1}, 4_096)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	spanRecorder := tracetest.NewSpanRecorder()
	tracerProvider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	reader := sdkmetric.NewManualReader()
	meterProvider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	t.Cleanup(func() {
		if err := meterProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown meter provider: %v", err)
		}
		if err := tracerProvider.Shutdown(context.Background()); err != nil {
			t.Errorf("shutdown tracer provider: %v", err)
		}
	})

	logs := &bytes.Buffer{}
	cancelled := make(chan struct{})
	uploadTTL := make(chan time.Duration, 1)
	handler, err := New(Dependencies{
		Logger: slog.New(slog.NewJSONHandler(logs, nil)),
		IDs:    identifiers,
		TracerProvider: telemetry.NewAttributeFilteringTracerProvider(
			tracerProvider,
			"http.method",
			"http.scheme",
			"http.route",
			"http.status_code",
			"net.protocol.name",
			"net.protocol.version",
		),
		MeterProvider:        meterProvider,
		RequestTimeout:       timeout,
		MaxBodyBytes:         32,
		EvidenceUploadPolicy: uploadPolicy,
		AllowedOrigins:       origins,
	}, func(router chi.Router) {
		router.Get("/v1/items/{id}", func(writer http.ResponseWriter, request *http.Request) {
			if err := respond.JSON(writer, request, http.StatusOK, map[string]string{"status": "ok"}); err != nil {
				panic(err)
			}
		})
		router.Get("/error", func(writer http.ResponseWriter, request *http.Request) {
			_ = respond.WriteProblem(writer, request, errors.New("database password=secret"), requestIDString(request.Context()))
		})
		router.Get("/panic", func(http.ResponseWriter, *http.Request) {
			panic("subject-secret")
		})
		router.Get("/timeout", func(_ http.ResponseWriter, request *http.Request) {
			<-request.Context().Done()
			close(cancelled)
		})
		router.Get("/ws-probe", func(writer http.ResponseWriter, request *http.Request) {
			_, hasDeadline := request.Context().Deadline()
			if err := respond.JSON(writer, request, http.StatusOK, map[string]bool{"has_deadline": hasDeadline}); err != nil {
				panic(err)
			}
		})
		router.Get("/limited", func(writer http.ResponseWriter, request *http.Request) {
			failure := apierror.New(http.StatusTooManyRequests, "RATE_LIMITED", "Rate limited", "Try again later.", nil).
				WithRetryAfter(1500 * time.Millisecond)
			_ = respond.WriteProblem(writer, request, failure, requestIDString(request.Context()))
		})
		router.Get("/auth", func(writer http.ResponseWriter, request *http.Request) {
			failure := apierror.New(http.StatusUnauthorized, "UNAUTHENTICATED", "Unauthenticated", "Authentication is required.", nil).
				WithChallenge("Bearer realm=\"idenqa\"")
			_ = respond.WriteProblem(writer, request, failure, requestIDString(request.Context()))
		})
		router.Post("/body", func(writer http.ResponseWriter, request *http.Request) {
			if _, err := io.ReadAll(request.Body); err != nil {
				_ = respond.WriteProblem(writer, request, err, requestIDString(request.Context()))

				return
			}
			respond.NoContent(writer)
		})
		router.Put("/v1/evidence-uploads/{uploadID}", func(writer http.ResponseWriter, request *http.Request) {
			deadline, ok := request.Context().Deadline()
			if !ok {
				panic("evidence upload request has no deadline")
			}
			uploadTTL <- time.Until(deadline)
			if _, err := io.ReadAll(request.Body); err != nil {
				_ = respond.WriteProblem(writer, request, err, requestIDString(request.Context()))

				return
			}
			respond.NoContent(writer)
		})
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return routerHarness{
		handler:   handler,
		logs:      logs,
		spans:     spanRecorder,
		metrics:   reader,
		cancelled: cancelled,
		uploadTTL: uploadTTL,
	}
}

func TestRouterSuccessRequestIDAndTelemetryUseRouteTemplates(t *testing.T) {
	t.Parallel()

	harness := newRouterHarness(t, time.Second, nil)
	rawID := "sub_01M11HEQG00000000000000000"
	request := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		"/v1/items/"+rawID+"?token=secret",
		nil,
	)
	response := httptest.NewRecorder()
	request.Header.Set(requestIDHeader, "req_01M11HEQG00000000000000000")
	harness.handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	requestID := response.Header().Get(requestIDHeader)
	if _, err := id.ParseRequest(requestID); err != nil {
		t.Fatalf("request ID %q is invalid: %v", requestID, err)
	}
	if requestID == "req_01M11HEQG00000000000000000" {
		t.Fatal("server trusted the caller-supplied request ID")
	}
	if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}

	spans := harness.spans.Ended()
	if len(spans) != 1 {
		t.Fatalf("ended span count = %d, want 1", len(spans))
	}
	if got, want := spans[0].Name(), "/v1/items/{id}"; got != want {
		t.Errorf("span name = %q, want %q", got, want)
	}
	assertSafeAttributes(t, spans[0].Attributes(), rawID)

	var collected metricdata.ResourceMetrics
	if err := harness.metrics.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}
	foundRoute := false
	for _, scope := range collected.ScopeMetrics {
		for _, measurement := range scope.Metrics {
			visitMetricAttributes(measurement.Data, func(attributes attribute.Set) {
				values := attributes.ToSlice()
				assertSafeAttributes(t, values, rawID)
				for _, keyValue := range values {
					if keyValue.Key == "http.route" && keyValue.Value.AsString() == "/v1/items/{id}" {
						foundRoute = true
					}
				}
			})
		}
	}
	if !foundRoute {
		t.Error("HTTP metrics did not contain the route template")
	}
}

func TestRouterDoesNotApplyOrdinaryRequestDeadlineToWebSocketUpgrade(t *testing.T) {
	t.Parallel()

	harness := newRouterHarness(t, 5*time.Millisecond, nil)
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/ws-probe", nil)
	request.Header.Set("Connection", "keep-alive, Upgrade")
	request.Header.Set("Upgrade", "websocket")
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, request)

	if got, want := response.Code, http.StatusOK; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	var body map[string]bool
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["has_deadline"] {
		t.Fatal("WebSocket upgrade inherited the ordinary request deadline")
	}
}

func TestRouterRedactsInternalErrorsAndPanics(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		path       string
		prohibited string
	}{
		{name: "ordinary error", path: "/error", prohibited: "password=secret"},
		{name: "panic", path: "/panic", prohibited: "subject-secret"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newRouterHarness(t, time.Second, nil)
			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, test.path, nil)
			harness.handler.ServeHTTP(response, request)

			if got, want := response.Code, http.StatusInternalServerError; got != want {
				t.Fatalf("status = %d, want %d", got, want)
			}
			combined := response.Body.String() + harness.logs.String()
			if strings.Contains(combined, test.prohibited) {
				t.Fatalf("response or logs leak %q: %s", test.prohibited, combined)
			}
			problem := decodeProblem(t, response)
			if problem.Code != apierror.CodeInternalError || problem.RequestID == "" {
				t.Fatalf("problem = %+v", problem)
			}
		})
	}
}

func TestRouterDeadlineCancelsHandler(t *testing.T) {
	t.Parallel()

	harness := newRouterHarness(t, 5*time.Millisecond, nil)
	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/timeout", nil)
	harness.handler.ServeHTTP(response, request)

	select {
	case <-harness.cancelled:
	default:
		t.Fatal("handler did not observe request cancellation")
	}
	if got, want := response.Code, http.StatusGatewayTimeout; got != want {
		t.Fatalf("status = %d, want %d", got, want)
	}
	if got := decodeProblem(t, response).Code; got != apierror.CodeRequestTimeout {
		t.Fatalf("problem code = %q, want %q", got, apierror.CodeRequestTimeout)
	}
}

func TestRouterProtocolHeadersAndBodyLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		wantStatus int
		header     string
		wantHeader string
	}{
		{name: "retry after", method: http.MethodGet, path: "/limited", wantStatus: http.StatusTooManyRequests, header: "Retry-After", wantHeader: "2"},
		{name: "authentication challenge", method: http.MethodGet, path: "/auth", wantStatus: http.StatusUnauthorized, header: "WWW-Authenticate", wantHeader: "Bearer realm=\"idenqa\""},
		{name: "body limit", method: http.MethodPost, path: "/body", body: strings.Repeat("x", 33), wantStatus: http.StatusRequestEntityTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			harness := newRouterHarness(t, time.Second, nil)
			response := httptest.NewRecorder()
			request := httptest.NewRequestWithContext(context.Background(), test.method, test.path, strings.NewReader(test.body))
			harness.handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", response.Code, test.wantStatus)
			}
			if test.header != "" && response.Header().Get(test.header) != test.wantHeader {
				t.Errorf("%s = %q, want %q", test.header, response.Header().Get(test.header), test.wantHeader)
			}
		})
	}
}

func TestRouterAppliesLargerLimitsOnlyToCanonicalEvidenceUploadBodies(t *testing.T) {
	t.Parallel()

	policy := evidence.DefaultUploadPolicy()
	harness := newRouterHarnessWithUploadPolicy(t, 25*time.Millisecond, nil, &policy)
	uploadID := "upl_01ARZ3NDEKTSV4RRFFQ69G5FAZ"

	tests := []struct {
		name       string
		method     string
		path       string
		length     int64
		wantStatus int
		wantUpload bool
	}{
		{
			name:       "canonical upload PUT",
			method:     http.MethodPut,
			path:       "/v1/evidence-uploads/" + uploadID,
			wantStatus: http.StatusNoContent,
			wantUpload: true,
		},
		{
			name:       "canonical upload still has the deployment ceiling",
			method:     http.MethodPut,
			path:       "/v1/evidence-uploads/" + uploadID,
			length:     evidence.DefaultUploadMaximumBytes + 1,
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "upload collection POST remains ordinary JSON",
			method:     http.MethodPost,
			path:       "/v1/evidence-uploads/" + uploadID,
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "malformed upload identifier remains ordinary",
			method:     http.MethodPut,
			path:       "/v1/evidence-uploads/not-an-upload",
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name:       "nested upload path remains ordinary",
			method:     http.MethodPut,
			path:       "/v1/evidence-uploads/" + uploadID + "/part",
			wantStatus: http.StatusRequestEntityTooLarge,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequestWithContext(
				t.Context(),
				test.method,
				test.path,
				strings.NewReader(strings.Repeat("x", 33)),
			)
			if test.length != 0 {
				request.ContentLength = test.length
			}
			response := httptest.NewRecorder()
			harness.handler.ServeHTTP(response, request)

			if response.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.wantStatus, response.Body)
			}
			if !test.wantUpload {
				return
			}
			select {
			case ttl := <-harness.uploadTTL:
				if ttl < evidence.DefaultUploadAttemptTimeout-time.Second {
					t.Fatalf("upload deadline TTL = %s, want approximately %s", ttl, evidence.DefaultUploadAttemptTimeout)
				}
			default:
				t.Fatal("upload handler did not observe its dedicated deadline")
			}
		})
	}
}

func TestRouterCORSDeniesByDefaultAndAllowsExactOrigin(t *testing.T) {
	t.Parallel()

	denied := newRouterHarness(t, time.Second, nil)
	deniedRequest := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/items/example", nil)
	deniedRequest.Header.Set("Origin", "https://capture.example")
	deniedResponse := httptest.NewRecorder()
	denied.handler.ServeHTTP(deniedResponse, deniedRequest)
	if got := deniedResponse.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("denied Access-Control-Allow-Origin = %q, want empty", got)
	}
	deniedPreflight := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/v1/items/example", nil)
	deniedPreflight.Header.Set("Origin", "https://capture.example")
	deniedPreflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	deniedPreflightResponse := httptest.NewRecorder()
	denied.handler.ServeHTTP(deniedPreflightResponse, deniedPreflight)
	if got := deniedPreflightResponse.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("denied preflight Access-Control-Allow-Origin = %q, want empty", got)
	}

	allowed := newRouterHarness(t, time.Second, []string{"https://capture.example"})
	preflight := httptest.NewRequestWithContext(context.Background(), http.MethodOptions, "/v1/items/example", nil)
	preflight.Header.Set("Origin", "https://capture.example")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodGet)
	preflight.Header.Set("Access-Control-Request-Headers", "Authorization, Traceparent")
	preflightResponse := httptest.NewRecorder()
	allowed.handler.ServeHTTP(preflightResponse, preflight)
	if got, want := preflightResponse.Code, http.StatusOK; got != want {
		t.Fatalf("preflight status = %d, want %d", got, want)
	}
	if got, want := preflightResponse.Header().Get("Access-Control-Allow-Origin"), "https://capture.example"; got != want {
		t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, want)
	}
	if credentials := preflightResponse.Header().Get("Access-Control-Allow-Credentials"); credentials != "" {
		t.Errorf("Access-Control-Allow-Credentials = %q, want empty", credentials)
	}

	actual := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/v1/items/example", nil)
	actual.Header.Set("Origin", "https://capture.example")
	actualResponse := httptest.NewRecorder()
	allowed.handler.ServeHTTP(actualResponse, actual)
	exposed := strings.ToLower(actualResponse.Header().Get("Access-Control-Expose-Headers"))
	for _, header := range []string{
		"Deprecation",
		"ETag",
		"Link",
		"Location",
		"RateLimit",
		"RateLimit-Policy",
		"Retry-After",
		"Sunset",
		requestIDHeader,
	} {
		if !strings.Contains(exposed, strings.ToLower(header)) {
			t.Errorf("Access-Control-Expose-Headers = %q, want it to expose %s", exposed, header)
		}
	}
}

func decodeProblem(t *testing.T, response *httptest.ResponseRecorder) respond.Problem {
	t.Helper()

	var problem respond.Problem
	if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v; body=%q", err, response.Body.String())
	}

	return problem
}

func assertSafeAttributes(t *testing.T, attributes []attribute.KeyValue, prohibited string) {
	t.Helper()

	allowed := map[attribute.Key]struct{}{
		"http.method":          {},
		"http.scheme":          {},
		"http.route":           {},
		"http.status_code":     {},
		"net.protocol.name":    {},
		"net.protocol.version": {},
	}
	for _, keyValue := range attributes {
		if _, ok := allowed[keyValue.Key]; !ok {
			t.Errorf("attribute %q is outside the allow-list", keyValue.Key)
		}
		value := keyValue.Value.String()
		if strings.Contains(value, prohibited) || strings.Contains(value, "token=secret") {
			t.Errorf("attribute %q leaks raw request data %q", keyValue.Key, value)
		}
	}
}

func visitMetricAttributes(data metricdata.Aggregation, visit func(attribute.Set)) {
	switch aggregation := data.(type) {
	case metricdata.Gauge[int64]:
		for _, point := range aggregation.DataPoints {
			visit(point.Attributes)
		}
	case metricdata.Gauge[float64]:
		for _, point := range aggregation.DataPoints {
			visit(point.Attributes)
		}
	case metricdata.Sum[int64]:
		for _, point := range aggregation.DataPoints {
			visit(point.Attributes)
		}
	case metricdata.Sum[float64]:
		for _, point := range aggregation.DataPoints {
			visit(point.Attributes)
		}
	case metricdata.Histogram[int64]:
		for _, point := range aggregation.DataPoints {
			visit(point.Attributes)
		}
	case metricdata.Histogram[float64]:
		for _, point := range aggregation.DataPoints {
			visit(point.Attributes)
		}
	}
}
