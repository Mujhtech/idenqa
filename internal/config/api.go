// Package config loads and validates process-specific Idenqa configuration.
package config

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/joho/godotenv"
	"github.com/kelseyhightower/envconfig"
)

const prefix = "IDENQA"

const secretByteLength = 32

// Peppers is redacting, versioned API-key HMAC configuration. Its environment
// form is a comma-separated list of version=unpadded-base64url entries.
type Peppers struct {
	values map[uint16][]byte
}

// Decode implements envconfig.Decoder and replaces the value only after the
// complete configuration has been validated.
func (peppers *Peppers) Decode(value string) error {
	if peppers == nil {
		return errors.New("API key pepper destination is required")
	}
	if value == "" || strings.TrimSpace(value) != value {
		return errors.New("API key peppers must be non-empty and contain no surrounding whitespace")
	}

	decoded := make(map[uint16][]byte)
	for _, entry := range strings.Split(value, ",") {
		versionText, encoded, found := strings.Cut(entry, "=")
		if !found || versionText == "" || encoded == "" || strings.TrimSpace(entry) != entry {
			return errors.New("API key peppers must use version=unpadded-base64url entries")
		}
		versionValue, err := strconv.ParseUint(versionText, 10, 16)
		if err != nil || versionValue == 0 {
			return errors.New("API key pepper versions must be integers from 1 to 65535")
		}
		version := uint16(versionValue)
		if _, exists := decoded[version]; exists {
			return fmt.Errorf("API key pepper version %d is duplicated", version)
		}
		material, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(material) != secretByteLength {
			return fmt.Errorf("API key pepper version %d must be 32 unpadded-Base64URL bytes", version)
		}
		decoded[version] = material
	}

	peppers.values = decoded

	return nil
}

// CursorKeys is redacting, versioned cursor HMAC configuration. Cursor keys
// are purpose-separated from credential peppers even when deployed together.
type CursorKeys struct {
	values map[uint16][]byte
}

// Decode implements envconfig.Decoder.
func (keys *CursorKeys) Decode(value string) error {
	if keys == nil {
		return errors.New("cursor key destination is required")
	}

	decoded, err := decodeVersionedSecrets(value, "cursor keys")
	if err != nil {
		return err
	}
	keys.values = decoded

	return nil
}

// Values returns a defensive copy for constructing the cursor keyring.
func (keys CursorKeys) Values() map[uint16][]byte {
	result := make(map[uint16][]byte, len(keys.values))
	for version, material := range keys.values {
		result[version] = append([]byte(nil), material...)
	}

	return result
}

// IsZero reports whether no cursor keys were supplied.
func (keys CursorKeys) IsZero() bool { return len(keys.values) == 0 }

// String redacts all cursor key material.
func (CursorKeys) String() string { return "[REDACTED]" }

// GoString redacts all cursor key material in %#v formatting.
func (CursorKeys) GoString() string { return "[REDACTED]" }

// MarshalJSON prevents configuration diagnostics from serialising cursor keys.
func (CursorKeys) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED]") }

// CaptureTokenKeys is redacting, versioned capture-token HMAC configuration.
// It is purpose-separated from API-key peppers and cursor keys.
type CaptureTokenKeys struct {
	values map[uint16][]byte
}

// Decode implements envconfig.Decoder.
func (keys *CaptureTokenKeys) Decode(value string) error {
	if keys == nil {
		return errors.New("capture-token key destination is required")
	}
	decoded, err := decodeVersionedSecrets(value, "capture-token keys")
	if err != nil {
		return err
	}
	keys.values = decoded

	return nil
}

// Values returns a defensive copy for constructing the capture-token keyring.
func (keys CaptureTokenKeys) Values() map[uint16][]byte {
	result := make(map[uint16][]byte, len(keys.values))
	for version, material := range keys.values {
		result[version] = append([]byte(nil), material...)
	}

	return result
}

// IsZero reports whether no capture-token keys were supplied.
func (keys CaptureTokenKeys) IsZero() bool { return len(keys.values) == 0 }

// String redacts all capture-token key material.
func (CaptureTokenKeys) String() string { return "[REDACTED]" }

// GoString redacts all capture-token key material in %#v formatting.
func (CaptureTokenKeys) GoString() string { return "[REDACTED]" }

// MarshalJSON prevents diagnostics from serialising capture-token keys.
func (CaptureTokenKeys) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED]") }

// OutcomeTokenKeys is redacting, versioned outcome-token HMAC configuration.
// It is purpose-separated from capture-token keys and all other credentials.
type OutcomeTokenKeys struct {
	values map[uint16][]byte
}

// Decode implements envconfig.Decoder.
func (keys *OutcomeTokenKeys) Decode(value string) error {
	if keys == nil {
		return errors.New("outcome-token key destination is required")
	}
	decoded, err := decodeVersionedSecrets(value, "outcome-token keys")
	if err != nil {
		return err
	}
	keys.values = decoded

	return nil
}

// Values returns a defensive copy for constructing the outcome-token keyring.
func (keys OutcomeTokenKeys) Values() map[uint16][]byte {
	result := make(map[uint16][]byte, len(keys.values))
	for version, material := range keys.values {
		result[version] = append([]byte(nil), material...)
	}

	return result
}

// IsZero reports whether no outcome-token keys were supplied.
func (keys OutcomeTokenKeys) IsZero() bool { return len(keys.values) == 0 }

// String redacts all outcome-token key material.
func (OutcomeTokenKeys) String() string { return "[REDACTED]" }

// GoString redacts all outcome-token key material in %#v formatting.
func (OutcomeTokenKeys) GoString() string { return "[REDACTED]" }

// MarshalJSON prevents diagnostics from serialising outcome-token keys.
func (OutcomeTokenKeys) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED]") }

// ExperienceSigningKeys is redacting, versioned Ed25519 experience-signing key
// configuration. Values are 32-byte seeds; the owned signing adapter derives
// stable key ids from versions and never accepts caller-supplied key ids.
type ExperienceSigningKeys struct {
	values map[uint16][]byte
}

// Decode implements envconfig.Decoder.
func (keys *ExperienceSigningKeys) Decode(value string) error {
	if keys == nil {
		return errors.New("experience signing key destination is required")
	}
	decoded, err := decodeVersionedSecrets(value, "experience signing keys")
	if err != nil {
		return err
	}
	keys.values = decoded

	return nil
}

// Values returns a defensive copy for constructing the signing keyring.
func (keys ExperienceSigningKeys) Values() map[uint16][]byte {
	result := make(map[uint16][]byte, len(keys.values))
	for version, material := range keys.values {
		result[version] = append([]byte(nil), material...)
	}

	return result
}

// IsZero reports whether no experience signing keys were supplied.
func (keys ExperienceSigningKeys) IsZero() bool { return len(keys.values) == 0 }

// String redacts all experience signing key material.
func (ExperienceSigningKeys) String() string { return "[REDACTED]" }

// GoString redacts all experience signing key material in %#v formatting.
func (ExperienceSigningKeys) GoString() string { return "[REDACTED]" }

// MarshalJSON prevents diagnostics from serialising experience signing keys.
func (ExperienceSigningKeys) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED]") }

func decodeVersionedSecrets(value, label string) (map[uint16][]byte, error) {
	if value == "" || strings.TrimSpace(value) != value {
		return nil, fmt.Errorf("%s must be non-empty and contain no surrounding whitespace", label)
	}

	decoded := make(map[uint16][]byte)
	for _, entry := range strings.Split(value, ",") {
		versionText, encoded, found := strings.Cut(entry, "=")
		if !found || versionText == "" || encoded == "" || strings.TrimSpace(entry) != entry {
			return nil, fmt.Errorf("%s must use version=unpadded-base64url entries", label)
		}
		versionValue, err := strconv.ParseUint(versionText, 10, 16)
		if err != nil || versionValue == 0 {
			return nil, fmt.Errorf("%s versions must be integers from 1 to 65535", label)
		}
		version := uint16(versionValue)
		if _, exists := decoded[version]; exists {
			return nil, fmt.Errorf("%s version %d is duplicated", label, version)
		}
		material, err := base64.RawURLEncoding.DecodeString(encoded)
		if err != nil || len(material) != secretByteLength {
			return nil, fmt.Errorf("%s version %d must be 32 unpadded-Base64URL bytes", label, version)
		}
		decoded[version] = material
	}

	return decoded, nil
}

// Values returns a defensive copy for constructing the access-layer pepper set.
func (peppers Peppers) Values() map[uint16][]byte {
	result := make(map[uint16][]byte, len(peppers.values))
	for version, material := range peppers.values {
		result[version] = append([]byte(nil), material...)
	}

	return result
}

// IsZero reports whether no pepper configuration was supplied.
func (peppers Peppers) IsZero() bool {
	return len(peppers.values) == 0
}

// String redacts all pepper material.
func (Peppers) String() string {
	return "[REDACTED]"
}

// GoString redacts all pepper material in %#v formatting.
func (Peppers) GoString() string {
	return "[REDACTED]"
}

// MarshalJSON prevents configuration diagnostics from serialising peppers.
func (Peppers) MarshalJSON() ([]byte, error) {
	return json.Marshal("[REDACTED]")
}

// TelemetryHeaders is redacting OTLP collector authentication metadata. Its
// environment form is a comma-separated list of percent-encoded name=value
// entries, matching the OpenTelemetry header convention.
type TelemetryHeaders struct {
	values map[string]string
}

// Decode implements envconfig.Decoder.
func (headers *TelemetryHeaders) Decode(value string) error {
	if headers == nil {
		return errors.New("telemetry header destination is required")
	}
	if value == "" || strings.TrimSpace(value) != value {
		return errors.New("telemetry headers must be non-empty and contain no surrounding whitespace")
	}

	decoded := make(map[string]string)
	for _, entry := range strings.Split(value, ",") {
		encodedName, encodedValue, found := strings.Cut(entry, "=")
		if !found || encodedName == "" || encodedValue == "" {
			return errors.New("telemetry headers must use percent-encoded name=value entries")
		}
		name, err := url.QueryUnescape(encodedName)
		if err != nil || name == "" {
			return errors.New("telemetry header name is not valid percent-encoding")
		}
		value, err := url.QueryUnescape(encodedValue)
		if err != nil {
			return fmt.Errorf("telemetry header %q value is not valid percent-encoding", name)
		}
		name = strings.ToLower(name)
		if _, exists := decoded[name]; exists {
			return fmt.Errorf("telemetry header %q is duplicated", name)
		}
		decoded[name] = value
	}
	headers.values = decoded

	return nil
}

// Values returns a defensive copy for exporter construction.
func (headers TelemetryHeaders) Values() map[string]string {
	result := make(map[string]string, len(headers.values))
	for name, value := range headers.values {
		result[name] = value
	}
	return result
}

// IsZero reports whether no collector headers were supplied.
func (headers TelemetryHeaders) IsZero() bool { return len(headers.values) == 0 }

// String redacts all collector header values.
func (TelemetryHeaders) String() string { return "[REDACTED]" }

// GoString redacts all collector header values in %#v formatting.
func (TelemetryHeaders) GoString() string { return "[REDACTED]" }

// MarshalJSON prevents diagnostics from serialising collector credentials.
func (TelemetryHeaders) MarshalJSON() ([]byte, error) { return json.Marshal("[REDACTED]") }

// API is the configuration required by the public API process.
type API struct {
	ReviewAuthorityFile string `envconfig:"REVIEW_AUTHORITY_FILE"`
	ProviderRuntimeFile string `envconfig:"PROVIDER_RUNTIME_FILE"`
	ModelRuntimeFile    string `envconfig:"MODEL_RUNTIME_FILE"`
	EvidenceUploadConfiguration
	EvidenceLocalDirectory     string                `envconfig:"EVIDENCE_LOCAL_DIRECTORY"`
	EvidenceLocalKeyringFile   string                `envconfig:"EVIDENCE_LOCAL_KEYRING_FILE"`
	EvidenceProtectionCleanup  time.Duration         `envconfig:"EVIDENCE_PROTECTION_CLEANUP_TIMEOUT" default:"5s"`
	Environment                string                `envconfig:"ENVIRONMENT" default:"production"`
	DatabaseURL                string                `envconfig:"DATABASE_URL"`
	DatabaseAdminURL           string                `envconfig:"DATABASE_ADMIN_URL"`
	DatabaseRole               string                `envconfig:"DATABASE_ROLE"`
	DatabaseMaxConnections     int32                 `envconfig:"DATABASE_MAX_CONNECTIONS" default:"20"`
	DatabaseMinConnections     int32                 `envconfig:"DATABASE_MIN_CONNECTIONS" default:"2"`
	DatabaseMaxLifetime        time.Duration         `envconfig:"DATABASE_MAX_LIFETIME" default:"1h"`
	DatabaseMaxIdleTime        time.Duration         `envconfig:"DATABASE_MAX_IDLE_TIME" default:"15m"`
	DatabaseConnectTimeout     time.Duration         `envconfig:"DATABASE_CONNECT_TIMEOUT" default:"5s"`
	DatabaseMigrationTimeout   time.Duration         `envconfig:"DATABASE_MIGRATION_TIMEOUT" default:"5m"`
	DatabaseHealthInterval     time.Duration         `envconfig:"DATABASE_HEALTH_INTERVAL" default:"10s"`
	DatabaseHealthTimeout      time.Duration         `envconfig:"DATABASE_HEALTH_TIMEOUT" default:"2s"`
	HeadgateInstallationID     string                `envconfig:"HEADGATE_INSTALLATION_ID"`
	HeadgateSchema             string                `envconfig:"HEADGATE_SCHEMA" default:"headgate"`
	APIKeyActivePepperVersion  uint16                `envconfig:"API_KEY_ACTIVE_PEPPER_VERSION"`
	APIKeyPeppers              Peppers               `envconfig:"API_KEY_PEPPERS"`
	APIKeyAllowNoExpiry        bool                  `envconfig:"API_KEY_ALLOW_NO_EXPIRY" default:"false"`
	APIKeyMaximumLifetime      time.Duration         `envconfig:"API_KEY_MAXIMUM_LIFETIME"`
	APIKeyMaximumOverlap       time.Duration         `envconfig:"API_KEY_MAXIMUM_ROTATION_OVERLAP"`
	CursorActiveKeyVersion     uint16                `envconfig:"CURSOR_ACTIVE_KEY_VERSION"`
	CursorKeys                 CursorKeys            `envconfig:"CURSOR_KEYS"`
	CursorTTL                  time.Duration         `envconfig:"CURSOR_TTL" default:"15m"`
	ProfileIdempotencyTTL      time.Duration         `envconfig:"PROFILE_IDEMPOTENCY_RETENTION" default:"24h"`
	CaptureTokenActiveVersion  uint16                `envconfig:"CAPTURE_TOKEN_ACTIVE_KEY_VERSION"`
	CaptureTokenKeys           CaptureTokenKeys      `envconfig:"CAPTURE_TOKEN_KEYS"`
	OutcomeTokenActiveVersion  uint16                `envconfig:"OUTCOME_TOKEN_ACTIVE_KEY_VERSION"`
	OutcomeTokenKeys           OutcomeTokenKeys      `envconfig:"OUTCOME_TOKEN_KEYS"`
	ExperienceSigningVersion   uint16                `envconfig:"EXPERIENCE_SIGNING_ACTIVE_KEY_VERSION"`
	ExperienceSigningKeys      ExperienceSigningKeys `envconfig:"EXPERIENCE_SIGNING_KEYS"`
	VerificationDefaultTTL     time.Duration         `envconfig:"VERIFICATION_DEFAULT_TTL" default:"24h"`
	VerificationMaximumTTL     time.Duration         `envconfig:"VERIFICATION_MAXIMUM_TTL" default:"168h"`
	CaptureTokenDefaultTTL     time.Duration         `envconfig:"CAPTURE_TOKEN_DEFAULT_TTL" default:"30m"`
	CaptureTokenMaximumTTL     time.Duration         `envconfig:"CAPTURE_TOKEN_MAXIMUM_TTL" default:"2h"`
	OutcomeTokenDefaultPostTTL time.Duration         `envconfig:"OUTCOME_TOKEN_DEFAULT_POST_EXPIRY_TTL" default:"24h"`
	OutcomeTokenMaximumPostTTL time.Duration         `envconfig:"OUTCOME_TOKEN_MAXIMUM_POST_EXPIRY_TTL" default:"168h"`
	NativeApplicationIDs       []string              `envconfig:"NATIVE_APPLICATION_IDS"`
	VerificationIdempotencyTTL time.Duration         `envconfig:"VERIFICATION_IDEMPOTENCY_RETENTION" default:"24h"`
	Region                     string                `envconfig:"REGION"`
	RealtimeWebSocketURL       string                `envconfig:"REALTIME_WEBSOCKET_URL"`
	RealtimeTicketLifetime     time.Duration         `envconfig:"REALTIME_TICKET_LIFETIME" default:"30s"`
	HTTPHost                   string                `envconfig:"HTTP_HOST" default:"127.0.0.1"`
	HTTPPort                   uint16                `envconfig:"HTTP_PORT" default:"8080"`
	HTTPTLSMode                string                `envconfig:"HTTP_TLS_MODE" default:"disabled"`
	HTTPTLSCertFile            string                `envconfig:"HTTP_TLS_CERT_FILE"`
	HTTPTLSKeyFile             string                `envconfig:"HTTP_TLS_KEY_FILE"`
	HTTPMaxBodyBytes           int64                 `envconfig:"HTTP_MAX_BODY_BYTES" default:"1048576"`
	HTTPRequestTimeout         time.Duration         `envconfig:"HTTP_REQUEST_TIMEOUT" default:"10s"`
	HTTPCORSAllowedOrigins     []string              `envconfig:"HTTP_CORS_ALLOWED_ORIGINS"`
	ShutdownTimeout            time.Duration         `envconfig:"SHUTDOWN_TIMEOUT" default:"10s"`
	LogLevel                   string                `envconfig:"LOG_LEVEL" default:"info"`
	LogFormat                  string                `envconfig:"LOG_FORMAT" default:"json"`
	TelemetryProtocol          string                `envconfig:"TELEMETRY_PROTOCOL" default:"disabled"`
	TelemetryEndpoint          string                `envconfig:"TELEMETRY_ENDPOINT"`
	TelemetryInsecure          bool                  `envconfig:"TELEMETRY_INSECURE" default:"false"`
	TelemetryHeaders           TelemetryHeaders      `envconfig:"TELEMETRY_HEADERS"`
	TelemetryTLSCAFile         string                `envconfig:"TELEMETRY_TLS_CA_FILE"`
	TelemetryTLSCertFile       string                `envconfig:"TELEMETRY_TLS_CERT_FILE"`
	TelemetryTLSKeyFile        string                `envconfig:"TELEMETRY_TLS_KEY_FILE"`
	TelemetryTLSServerName     string                `envconfig:"TELEMETRY_TLS_SERVER_NAME"`
	TelemetryTraceSampleRatio  float64               `envconfig:"TELEMETRY_TRACE_SAMPLE_RATIO" default:"0.10"`
	TelemetryMetricInterval    time.Duration         `envconfig:"TELEMETRY_METRIC_INTERVAL" default:"1m"`
	TelemetryExportTimeout     time.Duration         `envconfig:"TELEMETRY_EXPORT_TIMEOUT" default:"10s"`
}

// EvidenceUploadConfiguration is the reusable deployment configuration for
// every process that composes evidence-upload issuance or ingress.
type EvidenceUploadConfiguration struct {
	EvidenceUploadMaximumBytes   int64         `envconfig:"EVIDENCE_UPLOAD_MAXIMUM_BYTES" default:"16777216"`
	EvidenceUploadIntentLifetime time.Duration `envconfig:"EVIDENCE_UPLOAD_INTENT_LIFETIME" default:"15m"`
	EvidenceUploadAttemptTimeout time.Duration `envconfig:"EVIDENCE_UPLOAD_ATTEMPT_TIMEOUT" default:"10m"`
	EvidenceUploadMediaTypes     []string      `envconfig:"EVIDENCE_UPLOAD_ALLOWED_MEDIA_TYPES" default:"image/jpeg,image/png"`
}

// HTTPAddress returns the validated host and port in net/http listen format.
func (configuration API) HTTPAddress() string {
	return net.JoinHostPort(configuration.HTTPHost, strconv.Itoa(int(configuration.HTTPPort)))
}

// OperationalDatabaseURL returns the explicitly separated administrative
// credential when configured, preserving the development-only fallback used
// by the initial migration CLI.
func (configuration API) OperationalDatabaseURL() string {
	if configuration.DatabaseAdminURL != "" {
		return configuration.DatabaseAdminURL
	}

	return configuration.DatabaseURL
}

// EvidenceUploadPolicy returns the validated deployment upload policy shared
// by issuance, ingress, and HTTP transport composition.
func (configuration EvidenceUploadConfiguration) EvidenceUploadPolicy() (evidence.UploadPolicy, error) {
	policy, err := evidence.NewUploadPolicy(evidence.UploadPolicyConfig{
		MaximumBytes:      configuration.EvidenceUploadMaximumBytes,
		IntentLifetime:    configuration.EvidenceUploadIntentLifetime,
		AttemptTimeout:    configuration.EvidenceUploadAttemptTimeout,
		AllowedMediaTypes: configuration.EvidenceUploadMediaTypes,
	})
	if err != nil {
		return evidence.UploadPolicy{}, fmt.Errorf("evidence upload policy: %w", err)
	}

	return policy, nil
}

// LocalEvidenceEnabled reports whether the root API should compose the
// filesystem ciphertext store and mounted file keyring. Provider-backed
// distributions inject the same owned ports without setting these paths.
func (configuration API) LocalEvidenceEnabled() bool {
	return configuration.EvidenceLocalDirectory != "" && configuration.EvidenceLocalKeyringFile != ""
}

// LoadAPI loads an optional, explicitly named dotenv file and then processes
// IDENQA_* variables. Existing process variables always take precedence.
func LoadAPI(envFile string) (API, error) {
	var configuration API
	if err := loadAPIInto(envFile, &configuration, &configuration, false); err != nil {
		return API{}, err
	}

	return configuration, nil
}

// LoadAPIInto loads and validates the core API fields embedded in a larger
// deployment-owned configuration. Processing one combined schema preserves
// typo detection while allowing independent distributions to own additional
// IDENQA_* settings without adding provider concerns to the root API type.
func LoadAPIInto(envFile string, target any, configuration *API) error {
	return loadAPIInto(envFile, target, configuration, true)
}

func loadAPIInto(envFile string, target any, configuration *API, providerEvidence bool) error {
	if target == nil || configuration == nil {
		return errors.New("API configuration target is required")
	}

	err := withEnvFile(envFile, func() error {
		if err := checkDisallowedCoreEnvironment(target); err != nil {
			return fmt.Errorf("check API environment: %w", err)
		}
		if err := envconfig.Process(prefix, target); err != nil {
			return fmt.Errorf("decode API environment: %w", err)
		}

		return nil
	})
	if err != nil {
		return err
	}
	configuration.HTTPTLSMode = strings.ToLower(configuration.HTTPTLSMode)
	configuration.TelemetryProtocol = strings.ToLower(configuration.TelemetryProtocol)
	if err := configuration.validate(providerEvidence); err != nil {
		return fmt.Errorf("validate API configuration: %w", err)
	}

	return nil
}

// cliEnvironmentKeys are command-line credential variables that are not part of
// any process configuration schema. Commands such as doctor and synthetic read
// them directly, and operators commonly export them alongside the shared dotenv
// file, so they must not be rejected as misspelled process settings.
var cliEnvironmentKeys = map[string]struct{}{
	"IDENQA_API_KEY": {},
}

// checkDisallowedCoreEnvironment validates the complete open-source core
// namespace. API processes and administrative commands commonly share one
// dotenv file with the worker, so documented worker settings must not be
// mistaken for misspelled API settings. Deployment-owned targets remain part
// of the checked schema, preserving strict validation for their extensions.
func checkDisallowedCoreEnvironment(target any) error {
	targetType := reflect.TypeOf(target)
	if targetType == nil || targetType.Kind() != reflect.Pointer ||
		targetType.Elem().Kind() != reflect.Struct {
		return errors.New("API configuration target must be a pointer to a struct")
	}

	allowed := make(map[string]struct{})
	collectEnvironmentKeys(targetType.Elem(), prefix, allowed)
	collectEnvironmentKeys(reflect.TypeFor[Worker](), prefix, allowed)

	environmentPrefix := prefix + "_"
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, environmentPrefix) {
			continue
		}
		if _, exists := cliEnvironmentKeys[name]; exists {
			continue
		}
		if _, exists := allowed[name]; !exists {
			return fmt.Errorf("unknown environment variable %s", name)
		}
	}

	return nil
}

func collectEnvironmentKeys(configurationType reflect.Type, keyPrefix string, allowed map[string]struct{}) {
	for configurationType.Kind() == reflect.Pointer {
		configurationType = configurationType.Elem()
	}
	for index := range configurationType.NumField() {
		field := configurationType.Field(index)
		if field.PkgPath != "" || strings.EqualFold(field.Tag.Get("ignored"), "true") {
			continue
		}

		name := field.Tag.Get("envconfig")
		if name == "" {
			name = field.Name
		}
		key := strings.ToUpper(keyPrefix + "_" + name)
		fieldType := field.Type
		for fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		if fieldType.Kind() == reflect.Struct && field.Tag.Get("envconfig") == "" {
			nestedPrefix := key
			if field.Anonymous {
				nestedPrefix = keyPrefix
			}
			collectEnvironmentKeys(fieldType, nestedPrefix, allowed)
			continue
		}

		allowed[key] = struct{}{}
	}
}

func (configuration API) validate(providerEvidence bool) error {
	switch configuration.Environment {
	case "production", "development", "test":
	default:
		return errors.New("environment must be production, development, or test")
	}
	if err := validateDatabaseURL(configuration.DatabaseURL); err != nil {
		return err
	}
	if configuration.DatabaseAdminURL != "" {
		if err := validateDatabaseURL(configuration.DatabaseAdminURL); err != nil {
			return fmt.Errorf("admin %w", err)
		}
	}
	if configuration.DatabaseMaxConnections <= 0 || configuration.DatabaseMinConnections < 0 ||
		configuration.DatabaseMinConnections > configuration.DatabaseMaxConnections {
		return errors.New("database connection limits are invalid")
	}
	if configuration.DatabaseMaxLifetime <= 0 || configuration.DatabaseMaxIdleTime <= 0 ||
		configuration.DatabaseConnectTimeout <= 0 || configuration.DatabaseMigrationTimeout <= 0 ||
		configuration.DatabaseHealthInterval <= 0 ||
		configuration.DatabaseHealthTimeout <= 0 {
		return errors.New("database durations must be greater than zero")
	}
	if !validDeploymentRegion(configuration.HeadgateSchema) ||
		(configuration.HeadgateInstallationID != "" &&
			!validDeploymentRegion(configuration.HeadgateInstallationID)) {
		return errors.New("headgate schema and installation ID must be lowercase deployment identifiers")
	}
	if (configuration.APIKeyActivePepperVersion == 0) != configuration.APIKeyPeppers.IsZero() {
		return errors.New("API key active pepper version and peppers must be configured together")
	}
	if configuration.APIKeyActivePepperVersion != 0 {
		if _, exists := configuration.APIKeyPeppers.values[configuration.APIKeyActivePepperVersion]; !exists {
			return errors.New("API key active pepper version is not configured")
		}
	}
	if configuration.APIKeyMaximumLifetime < 0 {
		return errors.New("API key maximum lifetime must not be negative")
	}
	if configuration.APIKeyMaximumOverlap < 0 {
		return errors.New("API key maximum rotation overlap must not be negative")
	}
	if (configuration.CursorActiveKeyVersion == 0) != configuration.CursorKeys.IsZero() {
		return errors.New("cursor active key version and keys must be configured together")
	}
	if configuration.CursorActiveKeyVersion != 0 {
		if _, exists := configuration.CursorKeys.values[configuration.CursorActiveKeyVersion]; !exists {
			return errors.New("cursor active key version is not configured")
		}
	}
	if configuration.CursorTTL <= 0 {
		return errors.New("cursor TTL must be greater than zero")
	}
	if configuration.ProfileIdempotencyTTL <= 0 {
		return errors.New("profile idempotency retention must be greater than zero")
	}
	if (configuration.CaptureTokenActiveVersion == 0) != configuration.CaptureTokenKeys.IsZero() {
		return errors.New("capture-token active key version and keys must be configured together")
	}
	if configuration.CaptureTokenActiveVersion != 0 {
		if _, exists := configuration.CaptureTokenKeys.values[configuration.CaptureTokenActiveVersion]; !exists {
			return errors.New("capture-token active key version is not configured")
		}
	}
	if (configuration.OutcomeTokenActiveVersion == 0) != configuration.OutcomeTokenKeys.IsZero() {
		return errors.New("outcome-token active key version and keys must be configured together")
	}
	if configuration.OutcomeTokenActiveVersion != 0 {
		if _, exists := configuration.OutcomeTokenKeys.values[configuration.OutcomeTokenActiveVersion]; !exists {
			return errors.New("outcome-token active key version is not configured")
		}
	}
	if (configuration.ExperienceSigningVersion == 0) != configuration.ExperienceSigningKeys.IsZero() {
		return errors.New("experience signing active key version and keys must be configured together")
	}
	if configuration.ExperienceSigningVersion != 0 {
		if _, exists := configuration.ExperienceSigningKeys.values[configuration.ExperienceSigningVersion]; !exists {
			return errors.New("experience signing active key version is not configured")
		}
	}
	if configuration.VerificationDefaultTTL <= 0 ||
		configuration.VerificationMaximumTTL < configuration.VerificationDefaultTTL {
		return errors.New("verification default lifetime must be positive and not exceed its maximum")
	}
	if configuration.CaptureTokenDefaultTTL <= 0 ||
		configuration.CaptureTokenMaximumTTL < configuration.CaptureTokenDefaultTTL ||
		configuration.CaptureTokenMaximumTTL > configuration.VerificationMaximumTTL {
		return errors.New("capture-token lifetimes must be positive, ordered, and within the verification maximum")
	}
	if configuration.OutcomeTokenDefaultPostTTL <= 0 ||
		configuration.OutcomeTokenMaximumPostTTL < configuration.OutcomeTokenDefaultPostTTL ||
		configuration.OutcomeTokenMaximumPostTTL > 30*24*time.Hour {
		return errors.New("outcome-token post-expiry lifetimes must be positive, ordered, and no more than 30 days")
	}
	if configuration.VerificationIdempotencyTTL <= 0 {
		return errors.New("verification idempotency retention must be greater than zero")
	}
	if configuration.RealtimeTicketLifetime < 10*time.Second ||
		configuration.RealtimeTicketLifetime > 60*time.Second {
		return errors.New("realtime ticket lifetime must be between 10 and 60 seconds")
	}
	if configuration.HTTPHost == "" || strings.TrimSpace(configuration.HTTPHost) != configuration.HTTPHost {
		return errors.New("HTTP host must be non-empty and contain no surrounding whitespace")
	}
	if configuration.HTTPPort == 0 {
		return errors.New("HTTP port must be greater than zero")
	}

	switch configuration.HTTPTLSMode {
	case "disabled":
		if configuration.HTTPTLSCertFile != "" || configuration.HTTPTLSKeyFile != "" {
			return errors.New("HTTP TLS certificate and key files require HTTP TLS mode file")
		}
	case "file":
		if configuration.HTTPTLSCertFile == "" || configuration.HTTPTLSKeyFile == "" {
			return errors.New("HTTP TLS mode file requires both certificate and key files")
		}
	default:
		return fmt.Errorf("HTTP TLS mode %q is not supported", configuration.HTTPTLSMode)
	}
	if configuration.ShutdownTimeout <= 0 {
		return errors.New("shutdown timeout must be greater than zero")
	}
	if configuration.HTTPMaxBodyBytes <= 0 {
		return errors.New("HTTP maximum body bytes must be greater than zero")
	}
	if configuration.HTTPRequestTimeout <= 0 {
		return errors.New("HTTP request timeout must be greater than zero")
	}
	if err := validateOrigins(configuration.HTTPCORSAllowedOrigins); err != nil {
		return err
	}
	if err := validateNativeApplicationIDs(configuration.NativeApplicationIDs); err != nil {
		return err
	}
	if err := configuration.validateRealtimeBootstrap(false); err != nil {
		return err
	}
	if _, err := configuration.EvidenceUploadPolicy(); err != nil {
		return err
	}
	if !providerEvidence &&
		(configuration.EvidenceLocalDirectory == "") != (configuration.EvidenceLocalKeyringFile == "") {
		return errors.New("local evidence directory and keyring file must be configured together")
	}
	if (configuration.EvidenceLocalDirectory != "" &&
		strings.TrimSpace(configuration.EvidenceLocalDirectory) != configuration.EvidenceLocalDirectory) ||
		(configuration.EvidenceLocalKeyringFile != "" &&
			strings.TrimSpace(configuration.EvidenceLocalKeyringFile) != configuration.EvidenceLocalKeyringFile) {
		return errors.New("local evidence paths must not contain surrounding whitespace")
	}
	if configuration.EvidenceProtectionCleanup <= 0 {
		return errors.New("evidence protection cleanup timeout must be greater than zero")
	}
	if err := configuration.validateTelemetry(); err != nil {
		return err
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(configuration.LogLevel)); err != nil {
		return fmt.Errorf("log level: %w", err)
	}

	switch strings.ToLower(configuration.LogFormat) {
	case "json", "text":
		return nil
	default:
		return fmt.Errorf("log format %q is not supported", configuration.LogFormat)
	}
}

func validateNativeApplicationIDs(values []string) error {
	if len(values) > 256 {
		return errors.New("native application identity list is too large")
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" || len(value) > 255 || strings.TrimSpace(value) != value {
			return errors.New("native application identity is invalid")
		}
		for _, character := range value {
			if (character < 'A' || character > 'Z') && (character < 'a' || character > 'z') &&
				(character < '0' || character > '9') && !strings.ContainsRune("._-", character) {
				return errors.New("native application identity is invalid")
			}
		}
		if _, duplicate := seen[value]; duplicate {
			return errors.New("native application identities contain duplicates")
		}
		seen[value] = struct{}{}
	}
	return nil
}

func (configuration API) validateTelemetry() error {
	if configuration.TelemetryTraceSampleRatio < 0 || configuration.TelemetryTraceSampleRatio > 1 {
		return errors.New("telemetry trace sample ratio must be between zero and one")
	}
	if configuration.TelemetryMetricInterval < 10*time.Second ||
		configuration.TelemetryMetricInterval > 10*time.Minute {
		return errors.New("telemetry metric interval must be between 10 seconds and 10 minutes")
	}
	if configuration.TelemetryExportTimeout <= 0 || configuration.TelemetryExportTimeout > time.Minute {
		return errors.New("telemetry export timeout must be greater than zero and at most one minute")
	}

	switch configuration.TelemetryProtocol {
	case "disabled":
		if configuration.TelemetryEndpoint != "" || configuration.TelemetryInsecure ||
			!configuration.TelemetryHeaders.IsZero() || configuration.TelemetryTLSCAFile != "" ||
			configuration.TelemetryTLSCertFile != "" || configuration.TelemetryTLSKeyFile != "" ||
			configuration.TelemetryTLSServerName != "" {
			return errors.New("disabled telemetry must not configure an exporter endpoint or credentials")
		}
		return nil
	case "grpc", "http/protobuf":
	default:
		return fmt.Errorf("telemetry protocol %q is not supported", configuration.TelemetryProtocol)
	}

	if strings.TrimSpace(configuration.TelemetryEndpoint) != configuration.TelemetryEndpoint ||
		configuration.TelemetryEndpoint == "" {
		return errors.New("telemetry endpoint must be provided without surrounding whitespace")
	}
	host, portText, err := net.SplitHostPort(configuration.TelemetryEndpoint)
	if err != nil || host == "" {
		return errors.New("telemetry endpoint must be a host and port without a scheme or path")
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return errors.New("telemetry endpoint port must be an integer from 1 to 65535")
	}
	if configuration.TelemetryInsecure && !telemetryLoopbackHost(host) {
		return errors.New("insecure telemetry export is allowed only to a loopback endpoint")
	}
	if configuration.TelemetryInsecure && (configuration.TelemetryTLSCAFile != "" ||
		configuration.TelemetryTLSCertFile != "" || configuration.TelemetryTLSKeyFile != "" ||
		configuration.TelemetryTLSServerName != "") {
		return errors.New("insecure telemetry export must not configure TLS credentials")
	}
	if (configuration.TelemetryTLSCertFile == "") != (configuration.TelemetryTLSKeyFile == "") {
		return errors.New("telemetry TLS client certificate and key files must be configured together")
	}
	for _, value := range []string{
		configuration.TelemetryTLSCAFile,
		configuration.TelemetryTLSCertFile,
		configuration.TelemetryTLSKeyFile,
		configuration.TelemetryTLSServerName,
	} {
		if strings.TrimSpace(value) != value {
			return errors.New("telemetry TLS values must not contain surrounding whitespace")
		}
	}

	return nil
}

func telemetryLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// ValidateRealtimeBootstrap requires the API-process-only browser realtime
// settings. Administrative CLI commands may reuse database configuration
// without being forced to provide public HTTP deployment values.
func (configuration API) ValidateRealtimeBootstrap() error {
	return configuration.validateRealtimeBootstrap(true)
}

func (configuration API) validateRealtimeBootstrap(required bool) error {
	isConfigured := configuration.Region != "" || configuration.RealtimeWebSocketURL != "" ||
		len(configuration.HTTPCORSAllowedOrigins) != 0
	if !required && !isConfigured {
		return nil
	}
	if !validDeploymentRegion(configuration.Region) {
		return errors.New("region must be a lowercase deployment identifier of at most 63 characters")
	}
	if err := validateWebSocketURL(configuration.RealtimeWebSocketURL); err != nil {
		return err
	}
	if len(configuration.HTTPCORSAllowedOrigins) == 0 {
		return errors.New("at least one HTTP CORS allowed origin is required for browser capture")
	}

	return nil
}

func validDeploymentRegion(region string) bool {
	if len(region) == 0 || len(region) > 63 || region[0] < 'a' || region[0] > 'z' {
		return false
	}
	for _, character := range region[1:] {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}

	return true
}

func validateWebSocketURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" ||
		parsed.User != nil || parsed.Path != "/v1/capture/socket" || parsed.RawQuery != "" ||
		parsed.Fragment != "" || parsed.String() != value {
		return errors.New("realtime websocket URL must be an exact ws or wss /v1/capture/socket endpoint")
	}

	return nil
}

func validateDatabaseURL(value string) error {
	if value == "" || strings.TrimSpace(value) != value {
		return errors.New("database URL must be provided without surrounding whitespace")
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.Host == "" ||
		strings.Trim(parsed.Path, "/") == "" || parsed.Fragment != "" {
		return errors.New("database URL must identify a PostgreSQL database")
	}

	return nil
}

func validateOrigins(origins []string) error {
	seen := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		parsed, err := url.Parse(origin)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" ||
			parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return fmt.Errorf("HTTP CORS allowed origin %q must be an exact HTTP or HTTPS origin", origin)
		}
		if _, exists := seen[origin]; exists {
			return fmt.Errorf("HTTP CORS allowed origin %q is duplicated", origin)
		}
		seen[origin] = struct{}{}
	}

	return nil
}

func withEnvFile(path string, process func() error) (err error) {
	if path == "" {
		return process()
	}

	values, err := godotenv.Read(path)
	if err != nil {
		return fmt.Errorf("read dotenv file: %w", err)
	}

	added := make([]string, 0, len(values))
	defer func() {
		for _, key := range added {
			if unsetErr := os.Unsetenv(key); unsetErr != nil {
				err = errors.Join(err, fmt.Errorf("restore environment variable %q: %w", key, unsetErr))
			}
		}
	}()

	for key, value := range values {
		if !strings.HasPrefix(key, prefix+"_") {
			continue
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("load environment variable %q: %w", key, err)
		}
		added = append(added, key)
	}

	return process()
}
