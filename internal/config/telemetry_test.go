package config_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/config"
)

func TestLoadAPITelemetryConfiguration(t *testing.T) {
	clearIDENQAEnvironment(t)
	t.Setenv("IDENQA_DATABASE_URL", testDatabaseURL)
	t.Setenv("IDENQA_TELEMETRY_PROTOCOL", "GRPC")
	t.Setenv("IDENQA_TELEMETRY_ENDPOINT", "127.0.0.1:4317")
	t.Setenv("IDENQA_TELEMETRY_INSECURE", "true")
	t.Setenv("IDENQA_TELEMETRY_HEADERS", "authorization=Bearer%20secret,x-scope=core")
	t.Setenv("IDENQA_TELEMETRY_TRACE_SAMPLE_RATIO", "0.25")
	t.Setenv("IDENQA_TELEMETRY_METRIC_INTERVAL", "30s")
	t.Setenv("IDENQA_TELEMETRY_EXPORT_TIMEOUT", "4s")

	configuration, err := config.LoadAPI("")
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if configuration.TelemetryProtocol != "grpc" ||
		configuration.TelemetryTraceSampleRatio != 0.25 ||
		configuration.TelemetryMetricInterval != 30*time.Second ||
		configuration.TelemetryExportTimeout != 4*time.Second {
		t.Fatalf("telemetry configuration = %+v", configuration)
	}
	wantHeaders := map[string]string{"authorization": "Bearer secret", "x-scope": "core"}
	for name, want := range wantHeaders {
		if got := configuration.TelemetryHeaders.Values()[name]; got != want {
			t.Fatalf("telemetry header %q = %q, want %q", name, got, want)
		}
	}
	if formatted := fmt.Sprintf("%+v", configuration.TelemetryHeaders); strings.Contains(formatted, "secret") {
		t.Fatalf("telemetry headers leaked through formatting: %s", formatted)
	}
}

func TestLoadAPIRejectsUnsafeTelemetryConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		variables map[string]string
		want      string
	}{
		{
			name: "unsupported protocol",
			variables: map[string]string{
				"IDENQA_TELEMETRY_PROTOCOL": "json/http",
			},
			want: "protocol",
		},
		{
			name: "non-loopback insecure endpoint",
			variables: map[string]string{
				"IDENQA_TELEMETRY_PROTOCOL": "http/protobuf",
				"IDENQA_TELEMETRY_ENDPOINT": "collector.example:4318",
				"IDENQA_TELEMETRY_INSECURE": "true",
			},
			want: "loopback",
		},
		{
			name: "scheme in endpoint",
			variables: map[string]string{
				"IDENQA_TELEMETRY_PROTOCOL": "grpc",
				"IDENQA_TELEMETRY_ENDPOINT": "https://collector.example:4317",
			},
			want: "host and port",
		},
		{
			name: "invalid endpoint port",
			variables: map[string]string{
				"IDENQA_TELEMETRY_PROTOCOL": "grpc",
				"IDENQA_TELEMETRY_ENDPOINT": "collector.example:otlp",
			},
			want: "port must be an integer",
		},
		{
			name: "orphan client certificate",
			variables: map[string]string{
				"IDENQA_TELEMETRY_PROTOCOL":      "grpc",
				"IDENQA_TELEMETRY_ENDPOINT":      "collector.example:4317",
				"IDENQA_TELEMETRY_TLS_CERT_FILE": "client.pem",
			},
			want: "configured together",
		},
		{
			name: "credentials while disabled",
			variables: map[string]string{
				"IDENQA_TELEMETRY_HEADERS": "authorization=secret",
			},
			want: "disabled telemetry",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearIDENQAEnvironment(t)
			t.Setenv("IDENQA_DATABASE_URL", testDatabaseURL)
			for name, value := range test.variables {
				t.Setenv(name, value)
			}
			if _, err := config.LoadAPI(""); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadAPI() error = %v, want it to contain %q", err, test.want)
			}
		})
	}
}
