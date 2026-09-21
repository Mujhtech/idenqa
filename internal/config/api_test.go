package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/evidence"
)

const testDatabaseURL = "postgres://idenqa@127.0.0.1:5432/idenqa?sslmode=disable"

func TestLoadAPI(t *testing.T) {
	clearIDENQAEnvironment(t)

	tests := []struct {
		name        string
		environment map[string]string
		envFile     string
		want        config.API
		wantError   string
	}{
		{
			name: "defaults",
			want: config.API{
				ProviderHealthConfiguration: defaultProviderHealthConfiguration(),
				ProviderLimitConfiguration:  defaultProviderLimitConfiguration(),
				EvidenceUploadConfiguration: defaultEvidenceUploadConfiguration(),
				EvidenceProtectionCleanup:   5 * time.Second,
				KMSProvider:                 "local",
				KMSAWSMaxPlaintextBytes:     4096,
				SecretsProvider:             "file",
				SecretsCacheTTL:             30 * time.Second,
				SecretsReloadInterval:       5 * time.Minute,
				Environment:                 "production",
				DatabaseURL:                 testDatabaseURL,
				DatabaseMaxConnections:      20,
				DatabaseMinConnections:      2,
				DatabaseMaxLifetime:         time.Hour,
				DatabaseMaxIdleTime:         15 * time.Minute,
				DatabaseConnectTimeout:      5 * time.Second,
				DatabaseMigrationTimeout:    5 * time.Minute,
				DatabaseHealthInterval:      10 * time.Second,
				DatabaseHealthTimeout:       2 * time.Second,
				HeadgateSchema:              "headgate",
				CursorTTL:                   15 * time.Minute,
				ProfileIdempotencyTTL:       24 * time.Hour,
				VerificationDefaultTTL:      24 * time.Hour,
				VerificationMaximumTTL:      168 * time.Hour,
				CaptureTokenDefaultTTL:      30 * time.Minute,
				CaptureTokenMaximumTTL:      2 * time.Hour,
				OutcomeTokenDefaultPostTTL:  24 * time.Hour,
				OutcomeTokenMaximumPostTTL:  168 * time.Hour,
				VerificationIdempotencyTTL:  24 * time.Hour,
				Region:                      "local",
				RealtimeWebSocketURL:        "ws://127.0.0.1:8080/v1/capture/socket",
				RealtimeTicketLifetime:      30 * time.Second,
				HTTPHost:                    "127.0.0.1",
				HTTPPort:                    8080,
				HTTPTLSMode:                 "disabled",
				HTTPMaxBodyBytes:            1_048_576,
				HTTPRequestTimeout:          10 * time.Second,
				HTTPCORSAllowedOrigins:      []string{"http://localhost:3000"},
				ShutdownTimeout:             10 * time.Second,
				LogLevel:                    "info",
				LogFormat:                   "json",
				TelemetryProtocol:           "disabled",
				TelemetryTraceSampleRatio:   0.10,
				TelemetryMetricInterval:     time.Minute,
				TelemetryExportTimeout:      10 * time.Second,
			},
		},
		{
			name: "process environment",
			environment: map[string]string{
				"IDENQA_HTTP_HOST":                           "localhost",
				"IDENQA_HTTP_PORT":                           "9090",
				"IDENQA_HTTP_MAX_BODY_BYTES":                 "2048",
				"IDENQA_HTTP_REQUEST_TIMEOUT":                "3s",
				"IDENQA_HTTP_CORS_ALLOWED_ORIGINS":           "https://capture.example,http://localhost:3000",
				"IDENQA_EVIDENCE_UPLOAD_MAXIMUM_BYTES":       "8388608",
				"IDENQA_EVIDENCE_UPLOAD_INTENT_LIFETIME":     "20m",
				"IDENQA_EVIDENCE_UPLOAD_ATTEMPT_TIMEOUT":     "5m",
				"IDENQA_EVIDENCE_UPLOAD_ALLOWED_MEDIA_TYPES": "image/jpeg",
				"IDENQA_EVIDENCE_LOCAL_DIRECTORY":            "/var/lib/idenqa/evidence",
				"IDENQA_EVIDENCE_LOCAL_KEYRING_FILE":         "/run/secrets/evidence-keyring.json",
				"IDENQA_EVIDENCE_PROTECTION_CLEANUP_TIMEOUT": "7s",
				"IDENQA_SHUTDOWN_TIMEOUT":                    "25s",
				"IDENQA_LOG_LEVEL":                           "debug",
				"IDENQA_LOG_FORMAT":                          "text",
				"IDENQA_REGION":                              "eu-west-1",
				"IDENQA_REALTIME_WEBSOCKET_URL":              "wss://core.example/v1/capture/socket",
				"IDENQA_REALTIME_TICKET_LIFETIME":            "45s",
			},
			want: config.API{
				ProviderHealthConfiguration: defaultProviderHealthConfiguration(),
				ProviderLimitConfiguration:  defaultProviderLimitConfiguration(),
				EvidenceUploadConfiguration: config.EvidenceUploadConfiguration{
					EvidenceUploadMaximumBytes: 8 << 20, EvidenceUploadIntentLifetime: 20 * time.Minute,
					EvidenceUploadAttemptTimeout: 5 * time.Minute,
					EvidenceUploadMediaTypes:     []string{evidence.MediaTypeJPEG},
				},
				EvidenceLocalDirectory:     "/var/lib/idenqa/evidence",
				EvidenceLocalKeyringFile:   "/run/secrets/evidence-keyring.json",
				EvidenceProtectionCleanup:  7 * time.Second,
				KMSProvider:                "local",
				KMSAWSMaxPlaintextBytes:    4096,
				SecretsProvider:            "file",
				SecretsCacheTTL:            30 * time.Second,
				SecretsReloadInterval:      5 * time.Minute,
				Environment:                "production",
				DatabaseURL:                testDatabaseURL,
				DatabaseMaxConnections:     20,
				DatabaseMinConnections:     2,
				DatabaseMaxLifetime:        time.Hour,
				DatabaseMaxIdleTime:        15 * time.Minute,
				DatabaseConnectTimeout:     5 * time.Second,
				DatabaseMigrationTimeout:   5 * time.Minute,
				DatabaseHealthInterval:     10 * time.Second,
				DatabaseHealthTimeout:      2 * time.Second,
				HeadgateSchema:             "headgate",
				CursorTTL:                  15 * time.Minute,
				ProfileIdempotencyTTL:      24 * time.Hour,
				VerificationDefaultTTL:     24 * time.Hour,
				VerificationMaximumTTL:     168 * time.Hour,
				CaptureTokenDefaultTTL:     30 * time.Minute,
				CaptureTokenMaximumTTL:     2 * time.Hour,
				OutcomeTokenDefaultPostTTL: 24 * time.Hour,
				OutcomeTokenMaximumPostTTL: 168 * time.Hour,
				VerificationIdempotencyTTL: 24 * time.Hour,
				Region:                     "eu-west-1",
				RealtimeWebSocketURL:       "wss://core.example/v1/capture/socket",
				RealtimeTicketLifetime:     45 * time.Second,
				HTTPHost:                   "localhost",
				HTTPPort:                   9090,
				HTTPTLSMode:                "disabled",
				HTTPMaxBodyBytes:           2_048,
				HTTPRequestTimeout:         3 * time.Second,
				HTTPCORSAllowedOrigins:     []string{"https://capture.example", "http://localhost:3000"},
				ShutdownTimeout:            25 * time.Second,
				LogLevel:                   "debug",
				LogFormat:                  "text",
				TelemetryProtocol:          "disabled",
				TelemetryTraceSampleRatio:  0.10,
				TelemetryMetricInterval:    time.Minute,
				TelemetryExportTimeout:     10 * time.Second,
			},
		},
		{
			name: "missing browser origin allow-list",
			environment: map[string]string{
				"IDENQA_HTTP_CORS_ALLOWED_ORIGINS": "",
			},
			wantError: "at least one HTTP CORS allowed origin",
		},
		{
			name: "missing region",
			environment: map[string]string{
				"IDENQA_REGION": "",
			},
			wantError: "region must be",
		},
		{
			name: "invalid region",
			environment: map[string]string{
				"IDENQA_REGION": "idenqa.region.eu",
			},
			wantError: "region must be",
		},
		{
			name: "invalid realtime websocket URL",
			environment: map[string]string{
				"IDENQA_REALTIME_WEBSOCKET_URL": "https://core.example/v1/capture/socket",
			},
			wantError: "realtime websocket URL",
		},
		{
			name: "realtime websocket URL rejects existing query",
			environment: map[string]string{
				"IDENQA_REALTIME_WEBSOCKET_URL": "wss://core.example/v1/capture/socket?ticket=unsafe",
			},
			wantError: "realtime websocket URL",
		},
		{
			name: "invalid realtime ticket lifetime",
			environment: map[string]string{
				"IDENQA_REALTIME_TICKET_LIFETIME": "61s",
			},
			wantError: "realtime ticket lifetime",
		},
		{
			name: "local evidence paths must be paired",
			environment: map[string]string{
				"IDENQA_EVIDENCE_LOCAL_DIRECTORY": "/var/lib/idenqa/evidence",
			},
			wantError: "local evidence directory and keyring file",
		},
		{
			name: "evidence cleanup timeout must be positive",
			environment: map[string]string{
				"IDENQA_EVIDENCE_PROTECTION_CLEANUP_TIMEOUT": "0s",
			},
			wantError: "evidence protection cleanup timeout",
		},
		{
			name: "local evidence paths reject surrounding whitespace",
			environment: map[string]string{
				"IDENQA_EVIDENCE_LOCAL_DIRECTORY":    " /var/lib/idenqa/evidence",
				"IDENQA_EVIDENCE_LOCAL_KEYRING_FILE": "/run/secrets/evidence-keyring.json",
			},
			wantError: "local evidence paths",
		},
		{
			name: "missing database URL",
			environment: map[string]string{
				"IDENQA_DATABASE_URL": "",
			},
			wantError: "database URL",
		},
		{
			name: "invalid database URL",
			environment: map[string]string{
				"IDENQA_DATABASE_URL": "mysql://idenqa@localhost/idenqa",
			},
			wantError: "PostgreSQL database",
		},
		{
			name: "invalid provider health thresholds",
			environment: map[string]string{
				"IDENQA_PROVIDER_HEALTH_NOT_READY_FAILURE_RATIO": "0.1",
			},
			wantError: "provider health policy",
		},
		{
			name: "invalid database pool limits",
			environment: map[string]string{
				"IDENQA_DATABASE_MIN_CONNECTIONS": "21",
			},
			wantError: "connection limits",
		},
		{
			name: "invalid environment",
			environment: map[string]string{
				"IDENQA_ENVIRONMENT": "staging",
			},
			wantError: "environment",
		},
		{
			name: "invalid host",
			environment: map[string]string{
				"IDENQA_HTTP_HOST": " ",
			},
			wantError: "HTTP host",
		},
		{
			name: "invalid port",
			environment: map[string]string{
				"IDENQA_HTTP_PORT": "0",
			},
			wantError: "HTTP port",
		},
		{
			name: "invalid TLS mode",
			environment: map[string]string{
				"IDENQA_HTTP_TLS_MODE": "automatic",
			},
			wantError: "HTTP TLS mode",
		},
		{
			name: "file TLS requires certificate",
			environment: map[string]string{
				"IDENQA_HTTP_TLS_MODE":     "file",
				"IDENQA_HTTP_TLS_KEY_FILE": "server.key",
			},
			wantError: "requires both certificate and key",
		},
		{
			name: "disabled TLS rejects certificate",
			environment: map[string]string{
				"IDENQA_HTTP_TLS_CERT_FILE": "server.crt",
			},
			wantError: "require HTTP TLS mode file",
		},
		{
			name: "invalid maximum body bytes",
			environment: map[string]string{
				"IDENQA_HTTP_MAX_BODY_BYTES": "0",
			},
			wantError: "maximum body bytes",
		},
		{
			name: "invalid request timeout",
			environment: map[string]string{
				"IDENQA_HTTP_REQUEST_TIMEOUT": "0s",
			},
			wantError: "request timeout",
		},
		{
			name: "invalid CORS origin",
			environment: map[string]string{
				"IDENQA_HTTP_CORS_ALLOWED_ORIGINS": "https://capture.example/path",
			},
			wantError: "exact HTTP or HTTPS origin",
		},
		{
			name: "upload maximum below deployment minimum",
			environment: map[string]string{
				"IDENQA_EVIDENCE_UPLOAD_MAXIMUM_BYTES": "1048575",
			},
			wantError: "evidence upload policy",
		},
		{
			name: "upload intent lifetime below minimum",
			environment: map[string]string{
				"IDENQA_EVIDENCE_UPLOAD_INTENT_LIFETIME": "4m",
			},
			wantError: "evidence upload policy",
		},
		{
			name: "upload attempt timeout above maximum",
			environment: map[string]string{
				"IDENQA_EVIDENCE_UPLOAD_ATTEMPT_TIMEOUT": "16m",
			},
			wantError: "evidence upload policy",
		},
		{
			name: "unsupported upload media type",
			environment: map[string]string{
				"IDENQA_EVIDENCE_UPLOAD_ALLOWED_MEDIA_TYPES": "image/webp",
			},
			wantError: "upload media type is unsupported",
		},
		{
			name: "empty upload media allow-list",
			environment: map[string]string{
				"IDENQA_EVIDENCE_UPLOAD_ALLOWED_MEDIA_TYPES": "",
			},
			wantError: "upload media types are invalid",
		},
		{
			name: "duplicate upload media type",
			environment: map[string]string{
				"IDENQA_EVIDENCE_UPLOAD_ALLOWED_MEDIA_TYPES": "image/jpeg,image/jpeg",
			},
			wantError: "upload media types contain duplicates",
		},
		{
			name: "duplicate CORS origin",
			environment: map[string]string{
				"IDENQA_HTTP_CORS_ALLOWED_ORIGINS": "https://capture.example,https://capture.example",
			},
			wantError: "duplicated",
		},
		{
			name: "file TLS pair",
			environment: map[string]string{
				"IDENQA_HTTP_TLS_MODE":      "FILE",
				"IDENQA_HTTP_TLS_CERT_FILE": "server.crt",
				"IDENQA_HTTP_TLS_KEY_FILE":  "server.key",
			},
			want: config.API{
				ProviderHealthConfiguration: defaultProviderHealthConfiguration(),
				ProviderLimitConfiguration:  defaultProviderLimitConfiguration(),
				EvidenceUploadConfiguration: defaultEvidenceUploadConfiguration(),
				EvidenceProtectionCleanup:   5 * time.Second,
				KMSProvider:                 "local",
				KMSAWSMaxPlaintextBytes:     4096,
				SecretsProvider:             "file",
				SecretsCacheTTL:             30 * time.Second,
				SecretsReloadInterval:       5 * time.Minute,
				Environment:                 "production",
				DatabaseURL:                 testDatabaseURL,
				DatabaseMaxConnections:      20,
				DatabaseMinConnections:      2,
				DatabaseMaxLifetime:         time.Hour,
				DatabaseMaxIdleTime:         15 * time.Minute,
				DatabaseConnectTimeout:      5 * time.Second,
				DatabaseMigrationTimeout:    5 * time.Minute,
				DatabaseHealthInterval:      10 * time.Second,
				DatabaseHealthTimeout:       2 * time.Second,
				CursorTTL:                   15 * time.Minute,
				ProfileIdempotencyTTL:       24 * time.Hour,
				VerificationDefaultTTL:      24 * time.Hour,
				VerificationMaximumTTL:      168 * time.Hour,
				CaptureTokenDefaultTTL:      30 * time.Minute,
				CaptureTokenMaximumTTL:      2 * time.Hour,
				OutcomeTokenDefaultPostTTL:  24 * time.Hour,
				OutcomeTokenMaximumPostTTL:  168 * time.Hour,
				VerificationIdempotencyTTL:  24 * time.Hour,
				Region:                      "local",
				RealtimeWebSocketURL:        "ws://127.0.0.1:8080/v1/capture/socket",
				RealtimeTicketLifetime:      30 * time.Second,
				HTTPHost:                    "127.0.0.1",
				HTTPPort:                    8080,
				HTTPTLSMode:                 "file",
				HeadgateSchema:              "headgate",
				HTTPTLSCertFile:             "server.crt",
				HTTPTLSKeyFile:              "server.key",
				HTTPMaxBodyBytes:            1_048_576,
				HTTPRequestTimeout:          10 * time.Second,
				HTTPCORSAllowedOrigins:      []string{"http://localhost:3000"},
				ShutdownTimeout:             10 * time.Second,
				LogLevel:                    "info",
				LogFormat:                   "json",
				TelemetryProtocol:           "disabled",
				TelemetryTraceSampleRatio:   0.10,
				TelemetryMetricInterval:     time.Minute,
				TelemetryExportTimeout:      10 * time.Second,
			},
		},
		{
			name: "invalid shutdown timeout",
			environment: map[string]string{
				"IDENQA_SHUTDOWN_TIMEOUT": "0s",
			},
			wantError: "shutdown timeout",
		},
		{
			name: "invalid log level",
			environment: map[string]string{
				"IDENQA_LOG_LEVEL": "verbose",
			},
			wantError: "log level",
		},
		{
			name: "invalid log format",
			environment: map[string]string{
				"IDENQA_LOG_FORMAT": "xml",
			},
			wantError: "log format",
		},
		{
			name: "unknown prefixed variable",
			environment: map[string]string{
				"IDENQA_HTP_ADDRESS": "127.0.0.1:9090",
			},
			wantError: "check API environment",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearIDENQAEnvironment(t)
			if _, exists := test.environment["IDENQA_DATABASE_URL"]; !exists {
				t.Setenv("IDENQA_DATABASE_URL", testDatabaseURL)
			}
			if _, exists := test.environment["IDENQA_REGION"]; !exists {
				t.Setenv("IDENQA_REGION", "local")
			}
			if _, exists := test.environment["IDENQA_REALTIME_WEBSOCKET_URL"]; !exists {
				t.Setenv("IDENQA_REALTIME_WEBSOCKET_URL", "ws://127.0.0.1:8080/v1/capture/socket")
			}
			if _, exists := test.environment["IDENQA_HTTP_CORS_ALLOWED_ORIGINS"]; !exists {
				t.Setenv("IDENQA_HTTP_CORS_ALLOWED_ORIGINS", "http://localhost:3000")
			}
			for key, value := range test.environment {
				t.Setenv(key, value)
			}

			got, err := config.LoadAPI(test.envFile)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("LoadAPI() error = %v, want it to contain %q", err, test.wantError)
				}

				return
			}
			if err != nil {
				t.Fatalf("LoadAPI() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("LoadAPI() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestLoadAPIProcessEnvironmentWinsOverDotenv(t *testing.T) {
	clearIDENQAEnvironment(t)
	t.Setenv("IDENQA_DATABASE_URL", testDatabaseURL)
	t.Setenv("IDENQA_REGION", "local")
	t.Setenv("IDENQA_REALTIME_WEBSOCKET_URL", "ws://127.0.0.1:8080/v1/capture/socket")
	t.Setenv("IDENQA_HTTP_CORS_ALLOWED_ORIGINS", "http://localhost:3000")
	t.Setenv("IDENQA_HTTP_PORT", "9090")

	envFile := writeEnvFile(t, "IDENQA_HTTP_PORT=7070\nIDENQA_LOG_LEVEL=debug\n")
	configuration, err := config.LoadAPI(envFile)
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}

	if got, want := configuration.HTTPPort, uint16(9090); got != want {
		t.Errorf("HTTPPort = %d, want %d", got, want)
	}
	if got, want := configuration.LogLevel, "debug"; got != want {
		t.Errorf("LogLevel = %q, want %q", got, want)
	}
	if _, exists := os.LookupEnv("IDENQA_LOG_LEVEL"); exists {
		t.Error("dotenv-only value persisted in the process environment")
	}
}

func TestLoadAPIAcceptsWorkerSettingsFromSharedEnvironment(t *testing.T) {
	clearIDENQAEnvironment(t)
	setRequiredAPIEnvironment(t)
	t.Setenv("IDENQA_WORKER_VERIFICATION_CONCURRENCY", "12")

	if _, err := config.LoadAPI(""); err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
}

func TestLoadAPIRejectsUnknownWorkerSettingFromSharedEnvironment(t *testing.T) {
	clearIDENQAEnvironment(t)
	setRequiredAPIEnvironment(t)
	t.Setenv("IDENQA_WORKER_VERIFICATON_CONCURRENCY", "12")

	_, err := config.LoadAPI("")
	if err == nil || !strings.Contains(err.Error(), "unknown environment variable") {
		t.Fatalf("LoadAPI() error = %v, want unknown environment variable", err)
	}
}

func TestLoadAPILeavesAPIOnlyRealtimeSettingsOptionalForAdministrativeCallers(t *testing.T) {
	clearIDENQAEnvironment(t)
	t.Setenv("IDENQA_DATABASE_URL", testDatabaseURL)

	configuration, err := config.LoadAPI("")
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if err := configuration.ValidateRealtimeBootstrap(); err == nil ||
		!strings.Contains(err.Error(), "region must be") {
		t.Fatalf("ValidateRealtimeBootstrap() error = %v", err)
	}
}

func TestAPIHTTPAddress(t *testing.T) {
	t.Parallel()

	configuration := config.API{HTTPHost: "::1", HTTPPort: 8443}
	if got, want := configuration.HTTPAddress(), "[::1]:8443"; got != want {
		t.Fatalf("HTTPAddress() = %q, want %q", got, want)
	}
}

func TestEvidenceUploadConfigurationPolicy(t *testing.T) {
	t.Parallel()

	configuration := config.EvidenceUploadConfiguration{
		EvidenceUploadMaximumBytes:   8 << 20,
		EvidenceUploadIntentLifetime: 20 * time.Minute,
		EvidenceUploadAttemptTimeout: 5 * time.Minute,
		EvidenceUploadMediaTypes:     []string{evidence.MediaTypeJPEG},
	}
	policy, err := configuration.EvidenceUploadPolicy()
	if err != nil {
		t.Fatalf("EvidenceUploadPolicy() error = %v", err)
	}
	configuration.EvidenceUploadMediaTypes[0] = evidence.MediaTypePNG
	if policy.MaximumBytes() != 8<<20 || policy.IntentLifetime() != 20*time.Minute ||
		policy.AttemptTimeout() != 5*time.Minute ||
		!reflect.DeepEqual(policy.AllowedMediaTypes(), []string{evidence.MediaTypeJPEG}) {
		t.Fatalf(
			"EvidenceUploadPolicy() = maximum %d, intent %s, attempt %s, media %v",
			policy.MaximumBytes(),
			policy.IntentLifetime(),
			policy.AttemptTimeout(),
			policy.AllowedMediaTypes(),
		)
	}
}

func defaultProviderHealthConfiguration() config.ProviderHealthConfiguration {
	return config.ProviderHealthConfiguration{
		ProviderHealthWindow: 5 * time.Minute, ProviderHealthMinimumSamples: 5,
		ProviderHealthDegradedRatio: 0.2, ProviderHealthNotReadyRatio: 0.5, ProviderHealthAsyncBacklog: 16,
		ProviderHealthStaleAfter: 15 * time.Minute, ProviderHealthCacheTTL: 10 * time.Second, ProviderHealthProbeTimeout: 2 * time.Second,
		ProviderBreakerWindow: time.Minute, ProviderBreakerMinimumSamples: 4, ProviderBreakerFailureRatio: 0.5,
		ProviderBreakerOpenDuration: 30 * time.Second, ProviderBreakerHalfOpenProbes: 1,
	}
}

func defaultProviderLimitConfiguration() config.ProviderLimitConfiguration {
	return config.ProviderLimitConfiguration{
		ProviderMaxConcurrent: 4, ProviderRateLimit: 60, ProviderRatePeriod: time.Minute, ProviderRateBurst: 10, ProviderLeaseTTL: 10 * time.Minute,
	}
}

func defaultEvidenceUploadConfiguration() config.EvidenceUploadConfiguration {
	return config.EvidenceUploadConfiguration{
		EvidenceUploadMaximumBytes:   evidence.DefaultUploadMaximumBytes,
		EvidenceUploadIntentLifetime: evidence.DefaultUploadIntentLifetime,
		EvidenceUploadAttemptTimeout: evidence.DefaultUploadAttemptTimeout,
		EvidenceUploadMediaTypes:     []string{evidence.MediaTypeJPEG, evidence.MediaTypePNG},
	}
}

func TestLoadAPIMissingDotenv(t *testing.T) {
	clearIDENQAEnvironment(t)

	_, err := config.LoadAPI(filepath.Join(t.TempDir(), "missing.env"))
	if err == nil || !strings.Contains(err.Error(), "read dotenv file") {
		t.Fatalf("LoadAPI() error = %v, want missing dotenv error", err)
	}
}

func writeEnvFile(t *testing.T, contents string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "idenqa.env")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write dotenv fixture: %v", err)
	}

	return path
}

func clearIDENQAEnvironment(t *testing.T) {
	t.Helper()

	for _, entry := range os.Environ() {
		key, value, found := strings.Cut(entry, "=")
		if !found || !strings.HasPrefix(key, "IDENQA_") {
			continue
		}
		if err := os.Unsetenv(key); err != nil {
			t.Fatalf("unset %s: %v", key, err)
		}
		t.Cleanup(func() {
			if err := os.Setenv(key, value); err != nil {
				t.Errorf("restore %s: %v", key, err)
			}
		})
	}
}

func setRequiredAPIEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("IDENQA_DATABASE_URL", testDatabaseURL)
	t.Setenv("IDENQA_REGION", "local")
	t.Setenv("IDENQA_REALTIME_WEBSOCKET_URL", "ws://127.0.0.1:8080/v1/capture/socket")
	t.Setenv("IDENQA_HTTP_CORS_ALLOWED_ORIGINS", "http://localhost:3000")
}
