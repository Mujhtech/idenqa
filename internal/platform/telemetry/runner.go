package telemetry

import (
	"context"

	"github.com/Mujhtech/idenqa/internal/config"
)

// NewRunnerProviders uses the same validated telemetry environment as API and
// worker without requiring their database or application configuration.
func NewRunnerProviders(ctx context.Context, serviceName, serviceVersion string) (*Providers, error) {
	configuration, err := config.LoadTelemetry()
	if err != nil {
		return nil, err
	}
	return NewConfiguredProviders(ctx, serviceName, serviceVersion, Config{
		Protocol:           configuration.TelemetryProtocol,
		Endpoint:           configuration.TelemetryEndpoint,
		Insecure:           configuration.TelemetryInsecure,
		Headers:            configuration.TelemetryHeaders.Values(),
		TLSCAFile:          configuration.TelemetryTLSCAFile,
		TLSCertificateFile: configuration.TelemetryTLSCertFile,
		TLSKeyFile:         configuration.TelemetryTLSKeyFile,
		TLSServerName:      configuration.TelemetryTLSServerName,
		TraceSamplingRatio: configuration.TelemetryTraceSampleRatio,
		MetricInterval:     configuration.TelemetryMetricInterval,
		ExportTimeout:      configuration.TelemetryExportTimeout,
	})
}
