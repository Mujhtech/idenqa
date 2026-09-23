package config_test

import (
	"os"
	"strings"
	"testing"
	"time"

	distributionconfig "github.com/Mujhtech/idenqa/distributions/s3/internal/config"
)

const testDatabaseURL = "postgres://idenqa@127.0.0.1:5432/idenqa?sslmode=disable"

func TestLoad(t *testing.T) {
	clearIDENQAEnvironment(t)
	t.Setenv("IDENQA_DATABASE_URL", testDatabaseURL)
	setRequiredCoreEnvironment(t)
	t.Setenv("IDENQA_S3_BUCKET", "idenqa-evidence")
	t.Setenv("IDENQA_S3_REGION", "eu-west-1")
	t.Setenv("IDENQA_S3_PREFIX", "production/evidence")
	t.Setenv("IDENQA_S3_ENDPOINT", "https://objects.example.test")
	t.Setenv("IDENQA_S3_FORCE_PATH_STYLE", "true")
	t.Setenv("IDENQA_S3_CLEANUP_TIMEOUT", "45s")
	t.Setenv("IDENQA_EVIDENCE_LOCAL_KEYRING_FILE", "/run/secrets/idenqa-keyring.json")

	configuration, err := distributionconfig.Load("")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if configuration.DatabaseURL != testDatabaseURL || configuration.Bucket != "idenqa-evidence" ||
		configuration.S3Region != "eu-west-1" || configuration.Prefix != "production/evidence" ||
		configuration.Endpoint != "https://objects.example.test" || !configuration.ForcePathStyle ||
		configuration.AllowHTTP || configuration.CleanupTimeout != 45*time.Second ||
		configuration.EvidenceLocalKeyringFile != "/run/secrets/idenqa-keyring.json" {
		t.Fatalf("Load() = %+v", configuration)
	}
	provider := configuration.ObjectStoreConfig(configuration.EvidenceUploadMaximumBytes)
	if provider.MaxObjectBytes != 2*configuration.EvidenceUploadMaximumBytes {
		t.Errorf("MaxObjectBytes = %d, want %d", provider.MaxObjectBytes, 2*configuration.EvidenceUploadMaximumBytes)
	}
}

func TestLoadRejectsInvalidDistributionConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		environment map[string]string
		wantError   string
	}{
		{
			name: "missing bucket",
			environment: map[string]string{
				"IDENQA_S3_REGION":                   "eu-west-1",
				"IDENQA_EVIDENCE_LOCAL_KEYRING_FILE": "/run/secrets/keyring.json",
			},
			wantError: "object store settings are invalid",
		},
		{
			name: "plain HTTP is explicit",
			environment: map[string]string{
				"IDENQA_S3_BUCKET":                   "idenqa-evidence",
				"IDENQA_S3_REGION":                   "eu-west-1",
				"IDENQA_S3_ENDPOINT":                 "http://objects.example.test",
				"IDENQA_EVIDENCE_LOCAL_KEYRING_FILE": "/run/secrets/keyring.json",
			},
			wantError: "object store settings are invalid",
		},
		{
			name: "missing keyring",
			environment: map[string]string{
				"IDENQA_S3_BUCKET": "idenqa-evidence",
				"IDENQA_S3_REGION": "eu-west-1",
			},
			wantError: "local keyring file is required",
		},
		{
			name: "unknown variable",
			environment: map[string]string{
				"IDENQA_S3_BUCKET":                   "idenqa-evidence",
				"IDENQA_S3_REGION":                   "eu-west-1",
				"IDENQA_EVIDENCE_LOCAL_KEYRING_FILE": "/run/secrets/keyring.json",
				"IDENQA_S3_BUKCET":                   "typo",
			},
			wantError: "check API environment",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearIDENQAEnvironment(t)
			t.Setenv("IDENQA_DATABASE_URL", testDatabaseURL)
			setRequiredCoreEnvironment(t)
			for key, value := range test.environment {
				t.Setenv(key, value)
			}

			_, err := distributionconfig.Load("")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("Load() error = %v, want it to contain %q", err, test.wantError)
			}
		})
	}
}

func setRequiredCoreEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("IDENQA_REGION", "eu-west-1")
	t.Setenv("IDENQA_REALTIME_WEBSOCKET_URL", "wss://core.example/v1/capture/socket")
	t.Setenv("IDENQA_HTTP_CORS_ALLOWED_ORIGINS", "https://capture.example")
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
