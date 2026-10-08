package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/kelseyhightower/envconfig"
)

// Telemetry contains only the shared process telemetry environment settings.
// Runners do not load or require API database, tenant, or credential settings.
type Telemetry struct {
	TelemetryProtocol         string           `envconfig:"TELEMETRY_PROTOCOL" default:"disabled"`
	TelemetryEndpoint         string           `envconfig:"TELEMETRY_ENDPOINT"`
	TelemetryInsecure         bool             `envconfig:"TELEMETRY_INSECURE" default:"false"`
	TelemetryHeaders          TelemetryHeaders `envconfig:"TELEMETRY_HEADERS"`
	TelemetryTLSCAFile        string           `envconfig:"TELEMETRY_TLS_CA_FILE"`
	TelemetryTLSCertFile      string           `envconfig:"TELEMETRY_TLS_CERT_FILE"`
	TelemetryTLSKeyFile       string           `envconfig:"TELEMETRY_TLS_KEY_FILE"`
	TelemetryTLSServerName    string           `envconfig:"TELEMETRY_TLS_SERVER_NAME"`
	TelemetryTraceSampleRatio float64          `envconfig:"TELEMETRY_TRACE_SAMPLE_RATIO" default:"0.10"`
	TelemetryMetricInterval   time.Duration    `envconfig:"TELEMETRY_METRIC_INTERVAL" default:"1m"`
	TelemetryExportTimeout    time.Duration    `envconfig:"TELEMETRY_EXPORT_TIMEOUT" default:"10s"`
}

// LoadTelemetry loads and validates IDENQA_TELEMETRY_* for isolated runners.
func LoadTelemetry() (Telemetry, error) {
	var configuration Telemetry
	if err := envconfig.Process(prefix, &configuration); err != nil {
		return Telemetry{}, fmt.Errorf("decode telemetry environment: %w", err)
	}
	configuration.TelemetryProtocol = strings.ToLower(configuration.TelemetryProtocol)
	validation := API{
		TelemetryProtocol:         configuration.TelemetryProtocol,
		TelemetryEndpoint:         configuration.TelemetryEndpoint,
		TelemetryInsecure:         configuration.TelemetryInsecure,
		TelemetryHeaders:          configuration.TelemetryHeaders,
		TelemetryTLSCAFile:        configuration.TelemetryTLSCAFile,
		TelemetryTLSCertFile:      configuration.TelemetryTLSCertFile,
		TelemetryTLSKeyFile:       configuration.TelemetryTLSKeyFile,
		TelemetryTLSServerName:    configuration.TelemetryTLSServerName,
		TelemetryTraceSampleRatio: configuration.TelemetryTraceSampleRatio,
		TelemetryMetricInterval:   configuration.TelemetryMetricInterval,
		TelemetryExportTimeout:    configuration.TelemetryExportTimeout,
	}
	if err := validation.validateTelemetry(); err != nil {
		return Telemetry{}, err
	}
	return configuration, nil
}
