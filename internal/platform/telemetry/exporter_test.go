package telemetry

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

type recordingCollector struct {
	tracecollector.UnimplementedTraceServiceServer
	metriccollector.UnimplementedMetricsServiceServer
	traceExports  atomic.Int64
	metricExports atomic.Int64
}

func (collector *recordingCollector) Export(
	context.Context,
	*tracecollector.ExportTraceServiceRequest,
) (*tracecollector.ExportTraceServiceResponse, error) {
	collector.traceExports.Add(1)
	return &tracecollector.ExportTraceServiceResponse{}, nil
}

type recordingMetricCollector recordingCollector

func (collector *recordingMetricCollector) Export(
	context.Context,
	*metriccollector.ExportMetricsServiceRequest,
) (*metriccollector.ExportMetricsServiceResponse, error) {
	collector.metricExports.Add(1)
	return &metriccollector.ExportMetricsServiceResponse{}, nil
}

func TestConfiguredProvidersExportBothOTLPTransports(t *testing.T) {
	tests := []struct {
		name  string
		start func(*testing.T) (string, *atomic.Int64, *atomic.Int64)
		proto string
	}{
		{name: "gRPC", start: startGRPCCollector, proto: ProtocolGRPC},
		{name: "HTTP protobuf", start: startHTTPCollector, proto: ProtocolHTTPProtobuf},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			endpoint, traceExports, metricExports := test.start(t)
			providers, err := NewConfiguredProviders(context.Background(), "idenqa-test", "test", Config{
				Protocol:           test.proto,
				Endpoint:           endpoint,
				Insecure:           true,
				Headers:            map[string]string{"authorization": "Bearer test"},
				TraceSamplingRatio: 1,
				MetricInterval:     10 * time.Second,
				ExportTimeout:      2 * time.Second,
			})
			if err != nil {
				t.Fatalf("NewConfiguredProviders() error = %v", err)
			}

			_, span := providers.TracerProvider().Tracer("test").Start(context.Background(), "export")
			span.End()
			counter, err := providers.MeterProvider().Meter("test").Int64Counter("test.exports")
			if err != nil {
				t.Fatalf("Int64Counter() error = %v", err)
			}
			counter.Add(context.Background(), 1)

			shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := providers.Shutdown(shutdownContext); err != nil {
				t.Fatalf("Shutdown() error = %v", err)
			}
			if traceExports.Load() == 0 || metricExports.Load() == 0 {
				t.Fatalf(
					"exports = trace %d, metric %d; want both signals",
					traceExports.Load(),
					metricExports.Load(),
				)
			}
		})
	}
}

func TestConfigRejectsInsecureNonLoopbackEndpoint(t *testing.T) {
	_, err := NewConfiguredProviders(context.Background(), "idenqa-test", "test", Config{
		Protocol:           ProtocolGRPC,
		Endpoint:           "collector.example:4317",
		Insecure:           true,
		TraceSamplingRatio: 0.1,
		MetricInterval:     time.Minute,
		ExportTimeout:      10 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("NewConfiguredProviders() error = %v, want loopback rejection", err)
	}
}

func startGRPCCollector(t *testing.T) (string, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	collector := &recordingCollector{}
	metricCollector := (*recordingMetricCollector)(collector)
	server := grpc.NewServer()
	tracecollector.RegisterTraceServiceServer(server, collector)
	metriccollector.RegisterMetricsServiceServer(server, metricCollector)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener.Addr().String(), &collector.traceExports, &collector.metricExports
}

func startHTTPCollector(t *testing.T) (string, *atomic.Int64, *atomic.Int64) {
	t.Helper()
	var traceExports atomic.Int64
	var metricExports atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer test" {
			http.Error(writer, "unauthorised", http.StatusUnauthorized)
			return
		}
		switch request.URL.Path {
		case "/v1/traces":
			traceExports.Add(1)
		case "/v1/metrics":
			metricExports.Add(1)
		default:
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/x-protobuf")
		writer.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return strings.TrimPrefix(server.URL, "http://"), &traceExports, &metricExports
}
