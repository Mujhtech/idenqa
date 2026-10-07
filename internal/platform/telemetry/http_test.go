package telemetry

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (roundTripper roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTripper(request)
}

func TestHTTPTransportPropagationPolicies(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		internal  bool
		wantTrace bool
	}{
		{name: "external provider"},
		{name: "trusted internal service", internal: true, wantTrace: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			ctx, parent := provider.Tracer("test").Start(t.Context(), "parent")
			defer parent.End()
			member, err := baggage.NewMember("subject", "private")
			if err != nil {
				t.Fatal(err)
			}
			bag, err := baggage.New(member)
			if err != nil {
				t.Fatal(err)
			}
			ctx = baggage.ContextWithBaggage(ctx, bag)

			var traceparent, outgoingBaggage string
			base := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				traceparent = request.Header.Get("traceparent")
				outgoingBaggage = request.Header.Get("baggage")
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}, nil
			})
			transport := NewExternalHTTPTransport(base, provider)
			if test.internal {
				transport = NewInternalHTTPTransport(base, provider)
			}
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://example.com", nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			if (traceparent != "") != test.wantTrace || outgoingBaggage != "" {
				t.Fatalf("unexpected propagated headers: traceparent=%t baggage=%q", traceparent != "", outgoingBaggage)
			}
			if request.Header.Get("traceparent") != "" {
				t.Fatal("original request was mutated")
			}
			if _, err := io.Copy(io.Discard, response.Body); err != nil {
				t.Fatal(err)
			}
			if err := response.Body.Close(); err != nil {
				t.Fatal(err)
			}
			spans := recorder.Ended()
			if len(spans) != 1 || spans[0].Status().Code != codes.Unset {
				t.Fatalf("expected one successful client span, got %d", len(spans))
			}
		})
	}
}
