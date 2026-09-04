// Package telemetry owns OpenTelemetry providers and safe instrumentation
// policy for Idenqa processes.
package telemetry

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/credentials"
)

const (
	// ProtocolDisabled leaves telemetry providers local and installs no exporter.
	ProtocolDisabled = "disabled"
	// ProtocolGRPC exports OTLP protobuf messages over gRPC.
	ProtocolGRPC = "grpc"
	// ProtocolHTTPProtobuf exports OTLP protobuf messages over HTTP.
	ProtocolHTTPProtobuf = "http/protobuf"

	defaultTraceBatchTimeout  = 5 * time.Second
	defaultTraceExportTimeout = 10 * time.Second
	defaultTraceQueueSize     = 2048
	defaultTraceBatchSize     = 512
	maximumHeaderCount        = 32
)

var httpSpanAttributeAllowList = []attribute.Key{
	"http.method",
	"http.scheme",
	"http.route",
	"http.status_code",
	"net.protocol.name",
	"net.protocol.version",
	"http.request.method",
	"url.scheme",
	"http.response.status_code",
	"network.protocol.name",
	"network.protocol.version",
}

// Config is the process-owned, vendor-neutral OTLP exporter configuration.
// Header values are secrets and must never be logged.
type Config struct {
	Protocol           string
	Endpoint           string
	Insecure           bool
	Headers            map[string]string
	TLSCAFile          string
	TLSCertificateFile string
	TLSKeyFile         string
	TLSServerName      string
	TraceSamplingRatio float64
	MetricInterval     time.Duration
	ExportTimeout      time.Duration
}

// Providers contains the process telemetry providers.
type Providers struct {
	tracer *sdktrace.TracerProvider
	meter  *metric.MeterProvider
	safe   oteltrace.TracerProvider
}

// NewProviders constructs providers with safe resource metadata.
func NewProviders(serviceName, serviceVersion string) *Providers {
	return newProviders(
		serviceName,
		serviceVersion,
		nil,
		nil,
		sdktrace.AlwaysSample(),
		time.Minute,
		defaultTraceExportTimeout,
	)
}

// NewConfiguredProviders constructs providers with a selected OTLP exporter.
// A disabled configuration preserves the local-provider behaviour used by
// tests and self-hosted deployments that do not configure a collector.
func NewConfiguredProviders(
	ctx context.Context,
	serviceName string,
	serviceVersion string,
	configuration Config,
) (*Providers, error) {
	if configuration.Protocol == "" {
		configuration.Protocol = ProtocolDisabled
	}
	if err := configuration.validate(); err != nil {
		return nil, fmt.Errorf("validate telemetry configuration: %w", err)
	}
	if configuration.Protocol == ProtocolDisabled {
		return NewProviders(serviceName, serviceVersion), nil
	}

	tlsConfiguration, err := configuration.tlsConfig()
	if err != nil {
		return nil, fmt.Errorf("load telemetry TLS configuration: %w", err)
	}
	traceExporter, metricExporter, err := newExporters(ctx, configuration, tlsConfiguration)
	if err != nil {
		return nil, err
	}

	return newProviders(
		serviceName,
		serviceVersion,
		traceExporter,
		metricExporter,
		sdktrace.ParentBased(sdktrace.TraceIDRatioBased(configuration.TraceSamplingRatio)),
		configuration.MetricInterval,
		configuration.ExportTimeout,
	), nil
}

func newProviders(
	serviceName string,
	serviceVersion string,
	traceExporter sdktrace.SpanExporter,
	metricExporter metric.Exporter,
	sampler sdktrace.Sampler,
	metricInterval time.Duration,
	traceExportTimeout time.Duration,
) *Providers {
	processResource := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(serviceVersion),
	)
	traceOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(processResource),
		sdktrace.WithSampler(sampler),
	}
	if traceExporter != nil {
		traceOptions = append(traceOptions, sdktrace.WithBatcher(
			traceExporter,
			sdktrace.WithBatchTimeout(defaultTraceBatchTimeout),
			sdktrace.WithExportTimeout(traceExportTimeout),
			sdktrace.WithMaxQueueSize(defaultTraceQueueSize),
			sdktrace.WithMaxExportBatchSize(defaultTraceBatchSize),
		))
	}
	tracer := sdktrace.NewTracerProvider(traceOptions...)

	metricOptions := []metric.Option{metric.WithResource(processResource)}
	if metricExporter != nil {
		metricOptions = append(metricOptions, metric.WithReader(metric.NewPeriodicReader(
			metricExporter,
			metric.WithInterval(metricInterval),
		)))
	}
	meter := metric.NewMeterProvider(metricOptions...)

	return &Providers{
		tracer: tracer,
		meter:  meter,
		safe:   NewAttributeFilteringTracerProvider(tracer, httpSpanAttributeAllowList...),
	}
}

func (configuration Config) validate() error {
	switch configuration.Protocol {
	case ProtocolDisabled:
		if configuration.Endpoint != "" || configuration.Insecure || len(configuration.Headers) != 0 ||
			configuration.TLSCAFile != "" || configuration.TLSCertificateFile != "" ||
			configuration.TLSKeyFile != "" || configuration.TLSServerName != "" {
			return errors.New("disabled telemetry must not configure an exporter endpoint or credentials")
		}
		return nil
	case ProtocolGRPC, ProtocolHTTPProtobuf:
	default:
		return fmt.Errorf("telemetry protocol %q is not supported", configuration.Protocol)
	}

	if configuration.TraceSamplingRatio < 0 || configuration.TraceSamplingRatio > 1 {
		return errors.New("trace sampling ratio must be between zero and one")
	}
	if configuration.MetricInterval < 10*time.Second || configuration.MetricInterval > 10*time.Minute {
		return errors.New("metric interval must be between 10 seconds and 10 minutes")
	}
	if configuration.ExportTimeout <= 0 || configuration.ExportTimeout > time.Minute {
		return errors.New("export timeout must be greater than zero and at most one minute")
	}
	if len(configuration.Headers) > maximumHeaderCount {
		return fmt.Errorf("telemetry headers must not exceed %d entries", maximumHeaderCount)
	}
	for name, value := range configuration.Headers {
		if !validHeaderName(name) || len(name) > 128 {
			return errors.New("telemetry header name is invalid")
		}
		if len(value) > 4096 || strings.IndexFunc(value, invalidHeaderValueRune) >= 0 {
			return fmt.Errorf("telemetry header %q has an invalid value", name)
		}
	}

	if strings.TrimSpace(configuration.Endpoint) != configuration.Endpoint ||
		configuration.Endpoint == "" {
		return errors.New("telemetry endpoint must be provided without surrounding whitespace")
	}
	host, portText, err := net.SplitHostPort(configuration.Endpoint)
	if err != nil || host == "" {
		return errors.New("telemetry endpoint must be a host and port without a scheme or path")
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return errors.New("telemetry endpoint port must be an integer from 1 to 65535")
	}
	if configuration.Insecure && !loopbackHost(host) {
		return errors.New("insecure telemetry export is allowed only to a loopback endpoint")
	}
	if configuration.Insecure && (configuration.TLSCAFile != "" ||
		configuration.TLSCertificateFile != "" || configuration.TLSKeyFile != "" ||
		configuration.TLSServerName != "") {
		return errors.New("insecure telemetry export must not configure TLS credentials")
	}
	if (configuration.TLSCertificateFile == "") != (configuration.TLSKeyFile == "") {
		return errors.New("telemetry TLS client certificate and key files must be configured together")
	}
	for _, path := range []string{
		configuration.TLSCAFile,
		configuration.TLSCertificateFile,
		configuration.TLSKeyFile,
	} {
		if strings.TrimSpace(path) != path {
			return errors.New("telemetry TLS file paths must not contain surrounding whitespace")
		}
	}
	if strings.TrimSpace(configuration.TLSServerName) != configuration.TLSServerName {
		return errors.New("telemetry TLS server name must not contain surrounding whitespace")
	}

	return nil
}

func (configuration Config) tlsConfig() (*tls.Config, error) {
	if configuration.Insecure {
		return nil, nil
	}

	tlsConfiguration := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: configuration.TLSServerName,
	}
	if configuration.TLSCAFile != "" {
		rootCertificates, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load system certificate pool: %w", err)
		}
		certificate, err := os.ReadFile(configuration.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA certificate: %w", err)
		}
		if !rootCertificates.AppendCertsFromPEM(certificate) {
			return nil, errors.New("CA certificate file contains no valid PEM certificates")
		}
		tlsConfiguration.RootCAs = rootCertificates
	}
	if configuration.TLSCertificateFile != "" {
		certificate, err := tls.LoadX509KeyPair(
			configuration.TLSCertificateFile,
			configuration.TLSKeyFile,
		)
		if err != nil {
			return nil, fmt.Errorf("load client certificate and key: %w", err)
		}
		tlsConfiguration.Certificates = []tls.Certificate{certificate}
	}

	return tlsConfiguration, nil
}

func newExporters(
	ctx context.Context,
	configuration Config,
	tlsConfiguration *tls.Config,
) (sdktrace.SpanExporter, metric.Exporter, error) {
	switch configuration.Protocol {
	case ProtocolGRPC:
		traceOptions := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(configuration.Endpoint),
			otlptracegrpc.WithHeaders(cloneHeaders(configuration.Headers)),
			otlptracegrpc.WithTimeout(configuration.ExportTimeout),
		}
		metricOptions := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(configuration.Endpoint),
			otlpmetricgrpc.WithHeaders(cloneHeaders(configuration.Headers)),
			otlpmetricgrpc.WithTimeout(configuration.ExportTimeout),
		}
		if configuration.Insecure {
			traceOptions = append(traceOptions, otlptracegrpc.WithInsecure())
			metricOptions = append(metricOptions, otlpmetricgrpc.WithInsecure())
		} else {
			traceOptions = append(traceOptions, otlptracegrpc.WithTLSCredentials(
				credentials.NewTLS(tlsConfiguration.Clone()),
			))
			metricOptions = append(metricOptions, otlpmetricgrpc.WithTLSCredentials(
				credentials.NewTLS(tlsConfiguration.Clone()),
			))
		}

		traceExporter, err := otlptracegrpc.New(ctx, traceOptions...)
		if err != nil {
			return nil, nil, fmt.Errorf("construct OTLP gRPC trace exporter: %w", err)
		}
		metricExporter, err := otlpmetricgrpc.New(ctx, metricOptions...)
		if err != nil {
			shutdownErr := shutdownTraceExporter(ctx, configuration.ExportTimeout, traceExporter)
			return nil, nil, errors.Join(
				fmt.Errorf("construct OTLP gRPC metric exporter: %w", err),
				shutdownErr,
			)
		}
		return traceExporter, metricExporter, nil

	case ProtocolHTTPProtobuf:
		traceOptions := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(configuration.Endpoint),
			otlptracehttp.WithHeaders(cloneHeaders(configuration.Headers)),
			otlptracehttp.WithTimeout(configuration.ExportTimeout),
		}
		metricOptions := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(configuration.Endpoint),
			otlpmetrichttp.WithHeaders(cloneHeaders(configuration.Headers)),
			otlpmetrichttp.WithTimeout(configuration.ExportTimeout),
		}
		if configuration.Insecure {
			traceOptions = append(traceOptions, otlptracehttp.WithInsecure())
			metricOptions = append(metricOptions, otlpmetrichttp.WithInsecure())
		} else {
			traceOptions = append(traceOptions, otlptracehttp.WithTLSClientConfig(tlsConfiguration.Clone()))
			metricOptions = append(metricOptions, otlpmetrichttp.WithTLSClientConfig(tlsConfiguration.Clone()))
		}

		traceExporter, err := otlptracehttp.New(ctx, traceOptions...)
		if err != nil {
			return nil, nil, fmt.Errorf("construct OTLP HTTP/protobuf trace exporter: %w", err)
		}
		metricExporter, err := otlpmetrichttp.New(ctx, metricOptions...)
		if err != nil {
			shutdownErr := shutdownTraceExporter(ctx, configuration.ExportTimeout, traceExporter)
			return nil, nil, errors.Join(
				fmt.Errorf("construct OTLP HTTP/protobuf metric exporter: %w", err),
				shutdownErr,
			)
		}
		return traceExporter, metricExporter, nil
	default:
		return nil, nil, errors.New("telemetry protocol was not validated")
	}
}

func shutdownTraceExporter(
	ctx context.Context,
	timeout time.Duration,
	exporter sdktrace.SpanExporter,
) error {
	shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return exporter.Shutdown(shutdownContext)
}

func cloneHeaders(source map[string]string) map[string]string {
	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}
	return clone
}

func loopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", character) &&
			(character < '0' || character > '9') &&
			(character < 'A' || character > 'Z') &&
			(character < 'a' || character > 'z') {
			return false
		}
	}
	return true
}

func invalidHeaderValueRune(character rune) bool {
	return character < 0x20 || character > 0x7e
}

// TracerProvider returns the allow-listing provider used by application and
// HTTP instrumentation.
func (providers *Providers) TracerProvider() oteltrace.TracerProvider {
	return providers.safe
}

// MeterProvider returns the process metric provider.
func (providers *Providers) MeterProvider() otelmetric.MeterProvider {
	return providers.meter
}

// Shutdown flushes and closes providers in dependency-safe order.
func (providers *Providers) Shutdown(ctx context.Context) error {
	return errors.Join(
		providers.meter.Shutdown(ctx),
		providers.tracer.Shutdown(ctx),
	)
}

// HTTPMetricAttributes returns the complete metric attribute allow-list for an
// HTTP request. It deliberately excludes raw paths, query strings, hosts,
// client addresses, user agents, and identity-derived values.
func HTTPMetricAttributes(request *http.Request) []attribute.KeyValue {
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}

	attributes := []attribute.KeyValue{
		attribute.String("http.method", request.Method),
		attribute.String("http.scheme", scheme),
	}
	if route := chi.RouteContext(request.Context()).RoutePattern(); route != "" {
		attributes = append(attributes, attribute.String("http.route", route))
	}

	return attributes
}
