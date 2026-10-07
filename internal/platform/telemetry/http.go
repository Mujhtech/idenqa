package telemetry

import (
	"net/http"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

// NewExternalHTTPTransport uses the official OTel HTTP instrumentation without
// injecting trace headers into external provider requests.
func NewExternalHTTPTransport(base http.RoundTripper, provider trace.TracerProvider) *otelhttp.Transport {
	return otelhttp.NewTransport(base,
		otelhttp.WithTracerProvider(provider),
		otelhttp.WithPropagators(propagation.NewCompositeTextMapPropagator()),
	)
}

// NewInternalHTTPTransport propagates W3C trace context only to trusted Core
// services. Baggage is never injected.
func NewInternalHTTPTransport(base http.RoundTripper, provider trace.TracerProvider) *otelhttp.Transport {
	return otelhttp.NewTransport(base,
		otelhttp.WithTracerProvider(provider),
		otelhttp.WithPropagators(propagation.TraceContext{}),
	)
}
