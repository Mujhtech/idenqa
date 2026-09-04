// Package dojah implements the Idenqa provider contract for the reviewed
// Dojah public HTTP API. All sensitive values are resolved inside the runner.
package dojah

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

const (
	configurationDigest = "sha256:1a43389f9f297b42dc8507a353ba5d98f7c7d150527d67c13af117f1e478bae5"
	packageDigest       = "sha256:8e6eb4d7a4780565ce971b0eb3f4298869b54b4fe4ae1c64d5c180214f0dc90b"
	maximumBodyBytes    = 1 << 20
	maximumEvidence     = 10 << 20
)

var (
	// ErrConfiguration identifies an unavailable or invalid tenant configuration.
	ErrConfiguration = errors.New("dojah: configuration unavailable")
	// ErrInput identifies an unavailable structured input reference.
	ErrInput = errors.New("dojah: input unavailable")
	// ErrEvidence identifies an unavailable or oversized evidence grant.
	ErrEvidence = errors.New("dojah: evidence unavailable")
)

// Config is the runner-resolved tenant-owned Dojah configuration.
type Config struct {
	BaseURL string
	AppID   string
	APIKey  string
	Mode    string
	Region  string
}

// SecretResolver resolves exactly one tenant credential reference.
type SecretResolver interface {
	ResolveDojah(context.Context, string, string) (Config, error)
}

// InputResolver resolves a purpose-bound structured input reference.
type InputResolver interface {
	ResolveProviderInput(context.Context, string) (string, error)
}

// EvidenceReader redeems a purpose-bound evidence grant inside the runner.
type EvidenceReader interface {
	ReadProviderEvidence(context.Context, providerv1.EvidenceGrantReference, int64) ([]byte, error)
}

// HTTPClient is the bounded deployment-owned transport used by the adapter.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Adapter implements the public provider boundary without exposing Dojah types.
type Adapter struct {
	secrets  SecretResolver
	inputs   InputResolver
	evidence EvidenceReader
	client   HTTPClient
	now      func() time.Time
}

// New constructs a Dojah adapter around runner-owned resolvers and transport.
func New(secrets SecretResolver, inputs InputResolver, evidence EvidenceReader, client HTTPClient, now func() time.Time) (*Adapter, error) {
	if secrets == nil || inputs == nil || evidence == nil || client == nil || now == nil {
		return nil, ErrConfiguration
	}
	return &Adapter{secrets: secrets, inputs: inputs, evidence: evidence, client: client, now: now}, nil
}

// Manifest returns the frozen provider-neutral Dojah capabilities.
func (adapter *Adapter) Manifest(context.Context) (providerv1.Manifest, error) {
	return manifest(), nil
}

// ValidateConfiguration resolves and validates the exact tenant-owned reference.
func (adapter *Adapter) ValidateConfiguration(ctx context.Context, reference providerv1.ConfigurationReference) error {
	if adapter == nil || ctx == nil || reference.SchemaDigest != configurationDigest || reference.Validate() != nil {
		return ErrConfiguration
	}
	configuration, err := adapter.secrets.ResolveDojah(ctx, reference.SecretReference, reference.CredentialVersion)
	if err != nil || validateConfig(configuration) != nil {
		return ErrConfiguration
	}
	return nil
}

// Health checks configuration-independent adapter readiness without sending PII.
func (adapter *Adapter) Health(ctx context.Context) (providerv1.Health, error) {
	if adapter == nil || ctx == nil {
		return providerv1.Health{}, ErrConfiguration
	}
	if err := ctx.Err(); err != nil {
		return providerv1.Health{}, err
	}
	return providerv1.Health{State: providerv1.HealthReady, Code: "adapter_ready", CheckedAt: adapter.now().UTC()}, nil
}

// Execute performs one synchronous Dojah check and normalises only stable signals.
func (adapter *Adapter) Execute(ctx context.Context, request providerv1.Request) (providerv1.Result, error) {
	if adapter == nil || ctx == nil {
		return providerv1.Result{}, ErrConfiguration
	}
	if err := request.Validate(); err != nil {
		// Provider adapters return contract failures for caller-correctable requests.
		//nolint:nilerr // the validation error is intentionally normalised.
		return failed(request, adapter.now, providerv1.FailureInvalidRequest, "invalid_request", providerv1.RetryNever, 0), nil
	}
	if !pinned(request, manifest()) {
		return failed(request, adapter.now, providerv1.FailureUnsupported, "manifest_mismatch", providerv1.RetryNever, 0), nil
	}
	configuration, err := adapter.secrets.ResolveDojah(ctx, request.Configuration.SecretReference, request.Configuration.CredentialVersion)
	if err != nil || validateConfig(configuration) != nil {
		//nolint:nilerr // secret resolution details must not escape this boundary.
		return failed(request, adapter.now, providerv1.FailureUnauthenticated, "configuration_unavailable", providerv1.RetryNever, 0), nil
	}

	switch request.Check {
	case "idenqa.check.passive_liveness":
		return adapter.imageCheck(ctx, request, configuration, "/api/v1/ml/liveness", "image", "selfie", "idenqa.signal.passive_liveness")
	case "idenqa.check.face_match_1to1":
		return adapter.faceMatch(ctx, request, configuration)
	case "idenqa.check.document_analysis":
		return adapter.imageCheck(ctx, request, configuration, "/api/v1/document/analysis", "document", "document.front", "idenqa.signal.document_quality")
	case "idenqa.check.authority_lookup":
		return adapter.authorityLookup(ctx, request, configuration)
	default:
		return failed(request, adapter.now, providerv1.FailureUnsupported, "check_unsupported", providerv1.RetryNever, 0), nil
	}
}

func (adapter *Adapter) imageCheck(ctx context.Context, request providerv1.Request, configuration Config, path, field, variant, signal string) (providerv1.Result, error) {
	image, err := adapter.evidenceByVariant(ctx, request, map[string]bool{variant: true})
	if err != nil {
		return failed(request, adapter.now, providerv1.FailureInvalidRequest, "evidence_unavailable", providerv1.RetryNever, 0), nil
	}
	defer wipe(image)
	response, status, retryAfter, err := adapter.post(ctx, configuration, path, map[string]string{field: base64.StdEncoding.EncodeToString(image)})
	if err != nil {
		return transportFailure(request, adapter.now, status, retryAfter), nil
	}
	return completed(request, adapter.now, signal, responseOutcome(response)), nil
}

func (adapter *Adapter) faceMatch(ctx context.Context, request providerv1.Request, configuration Config) (providerv1.Result, error) {
	selfie, err := adapter.evidenceByVariant(ctx, request, map[string]bool{"selfie": true})
	if err != nil {
		return failed(request, adapter.now, providerv1.FailureInvalidRequest, "selfie_unavailable", providerv1.RetryNever, 0), nil
	}
	defer wipe(selfie)
	document, err := adapter.evidenceByVariant(ctx, request, map[string]bool{"document.front": true})
	if err != nil {
		return failed(request, adapter.now, providerv1.FailureInvalidRequest, "document_unavailable", providerv1.RetryNever, 0), nil
	}
	defer wipe(document)
	response, status, retryAfter, err := adapter.post(ctx, configuration, "/api/v1/kyc/photoid/verify", map[string]string{
		"selfie_image": base64.StdEncoding.EncodeToString(selfie), "photoid_image": base64.StdEncoding.EncodeToString(document),
	})
	if err != nil {
		return transportFailure(request, adapter.now, status, retryAfter), nil
	}
	return completed(request, adapter.now, "idenqa.signal.face_match_1to1", responseOutcome(response)), nil
}

func (adapter *Adapter) authorityLookup(ctx context.Context, request providerv1.Request, configuration Config) (providerv1.Result, error) {
	values, err := adapter.resolveInputs(ctx, request.Inputs)
	if err != nil {
		//nolint:nilerr // input resolution is exposed as a stable contract failure.
		return failed(request, adapter.now, providerv1.FailureInvalidRequest, "input_unavailable", providerv1.RetryNever, 0), nil
	}
	path, parameter, ok := authorityPath(values["idenqa.input.country"], values["idenqa.input.id_type"])
	if !ok || values["idenqa.input.id_number"] == "" {
		return failed(request, adapter.now, providerv1.FailureUnsupported, "authority_pack_unsupported", providerv1.RetryNever, 0), nil
	}
	endpoint, _ := url.Parse(strings.TrimSuffix(configuration.BaseURL, "/") + path)
	query := endpoint.Query()
	query.Set(parameter, values["idenqa.input.id_number"])
	endpoint.RawQuery = query.Encode()
	response, status, retryAfter, err := adapter.do(ctx, configuration, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		//nolint:nilerr // transport errors are deliberately classified in the result.
		return transportFailure(request, adapter.now, status, retryAfter), nil
	}
	return completed(request, adapter.now, "idenqa.signal.authority_record", responseOutcome(response)), nil
}

func (adapter *Adapter) post(ctx context.Context, configuration Config, path string, body any) (map[string]any, int, time.Duration, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, 0, 0, err
	}
	defer wipe(encoded)
	return adapter.do(ctx, configuration, http.MethodPost, strings.TrimSuffix(configuration.BaseURL, "/")+path, encoded)
}

func (adapter *Adapter) do(ctx context.Context, configuration Config, method, endpoint string, body []byte) (map[string]any, int, time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, 0, err
	}
	request.Header.Set("AppId", configuration.AppID)
	request.Header.Set("Authorization", configuration.APIKey)
	request.Header.Set("Accept", "application/json")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := adapter.client.Do(request)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	retryAfter := parseRetryAfter(response.Header.Get("Retry-After"))
	limited := io.LimitReader(response.Body, maximumBodyBytes+1)
	encoded, readErr := io.ReadAll(limited)
	if readErr != nil || len(encoded) > maximumBodyBytes {
		return nil, response.StatusCode, retryAfter, errors.New("invalid bounded response")
	}
	defer wipe(encoded)
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return nil, response.StatusCode, retryAfter, errors.New("provider rejected request")
	}
	var decoded map[string]any
	if json.Unmarshal(encoded, &decoded) != nil {
		return nil, response.StatusCode, retryAfter, errors.New("invalid provider response")
	}
	return decoded, response.StatusCode, retryAfter, nil
}

func (adapter *Adapter) evidenceByVariant(ctx context.Context, request providerv1.Request, accepted map[string]bool) ([]byte, error) {
	for _, grant := range request.Evidence {
		if accepted[grant.Variant] {
			value, err := adapter.evidence.ReadProviderEvidence(ctx, grant, maximumEvidence)
			if err != nil || len(value) == 0 || len(value) > maximumEvidence {
				wipe(value)
				return nil, ErrEvidence
			}
			return value, nil
		}
	}
	return nil, ErrEvidence
}

func (adapter *Adapter) resolveInputs(ctx context.Context, references []providerv1.InputReference) (map[string]string, error) {
	values := make(map[string]string, len(references))
	for _, reference := range references {
		value, err := adapter.inputs.ResolveProviderInput(ctx, reference.Reference)
		if err != nil || value == "" || len(value) > 512 {
			return nil, ErrInput
		}
		values[reference.Name] = value
	}
	return values, nil
}

func manifest() providerv1.Manifest {
	return providerv1.Manifest{
		Package: providerv1.PackageProvenance{
			AdapterID:      "dojah",
			AdapterVersion: "0.1.0",
			PackageDigest:  packageDigest,
			Contract:       providerv1.CurrentVersion,
		},
		Configuration: providerv1.ConfigurationSchema{
			ID:     "dojah.tenant.v1",
			Digest: configurationDigest,
		},
		Capabilities: []providerv1.Capability{
			capability("idenqa.check.passive_liveness", []string{"idenqa.evidence.selfie_image"}, []string{"idenqa.assurance.passive_liveness"}),
			capability("idenqa.check.face_match_1to1", []string{"idenqa.evidence.selfie_image", "idenqa.evidence.document_image"}, []string{"idenqa.assurance.face_match_1to1"}),
			capability("idenqa.check.document_analysis", []string{"idenqa.evidence.document_image"}, []string{"idenqa.assurance.capture_quality"}),
			{Check: "idenqa.check.authority_lookup", AcceptedEvidence: []string{}, AcceptedInputs: authorityInputs(), AcceptedAssurances: []string{}, ProcessingRegions: []string{"africa"}},
		},
		Restrictions: providerv1.Restrictions{NetworkRequired: true, MaximumGrants: 16, MaximumResultSize: 32 * 1024, MaximumDuration: 2 * time.Minute},
	}
}

func authorityInputs() []string {
	return []string{"idenqa.input.country", "idenqa.input.id_type", "idenqa.input.id_number"}
}

func capability(check string, evidence, assurances []string) providerv1.Capability {
	return providerv1.Capability{Check: check, AcceptedEvidence: evidence, AcceptedAssurances: assurances, ProcessingRegions: []string{"africa"}}
}

func validateConfig(configuration Config) error {
	endpoint, err := url.Parse(configuration.BaseURL)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || configuration.AppID == "" || configuration.APIKey == "" ||
		(configuration.Mode != "sandbox" && configuration.Mode != "production") || configuration.Region != "africa" {
		return ErrConfiguration
	}
	return nil
}

func pinned(request providerv1.Request, advertised providerv1.Manifest) bool {
	if request.Adapter != advertised.Package || request.Restrictions != advertised.Restrictions || request.Configuration.SchemaDigest != advertised.Configuration.Digest {
		return false
	}
	for _, candidate := range advertised.Capabilities {
		if candidate.Check == request.Capability.Check {
			return equalCapability(candidate, request.Capability)
		}
	}
	return false
}

func equalCapability(left, right providerv1.Capability) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return bytes.Equal(leftJSON, rightJSON)
}

func authorityPath(country, idType string) (string, string, bool) {
	paths := map[string][2]string{
		"NG:NIN":             {"/api/v1/kyc/nin", "nin"},
		"NG:VNIN":            {"/api/v1/kyc/vnin", "vnin"},
		"NG:BVN":             {"/api/v1/kyc/bvn", "bvn"},
		"NG:PASSPORT":        {"/api/v1/kyc/passport", "passport_number"},
		"NG:DRIVERS_LICENSE": {"/api/v1/kyc/dl", "license_number"},
		"GH:PASSPORT":        {"/api/v1/gh/kyc/passport", "passport_number"},
		"GH:DRIVERS_LICENSE": {"/api/v1/gh/kyc/dl", "license_number"},
		"KE:NATIONAL_ID":     {"/api/v1/ke/kyc/id", "id_number"},
		"KE:PASSPORT":        {"/api/v1/ke/kyc/passport", "passport_number"},
		"ZA:NATIONAL_ID":     {"/api/v1/za/kyc/id", "id_number"},
	}
	value, ok := paths[country+":"+idType]
	return value[0], value[1], ok
}

func responseOutcome(response map[string]any) providerv1.SignalOutcome {
	for _, key := range []string{"match", "liveness", "valid", "verified", "success"} {
		if value, ok := deepValue(response, key); ok {
			switch typed := value.(type) {
			case bool:
				if typed {
					return providerv1.SignalOutcomeSatisfied
				}
				return providerv1.SignalOutcomeNotSatisfied
			case string:
				switch strings.ToLower(typed) {
				case "true", "passed", "verified", "valid", "match", "matched", "approved":
					return providerv1.SignalOutcomeSatisfied
				case "false", "failed", "not verified", "invalid", "no match", "unmatched", "rejected":
					return providerv1.SignalOutcomeNotSatisfied
				}
			}
		}
	}
	if entity, ok := response["entity"]; ok && entity != nil {
		return providerv1.SignalOutcomeSatisfied
	}
	return providerv1.SignalOutcomeInconclusive
}

func deepValue(value map[string]any, wanted string) (any, bool) {
	for key, item := range value {
		if strings.EqualFold(key, wanted) {
			return item, true
		}
		if nested, ok := item.(map[string]any); ok {
			if found, exists := deepValue(nested, wanted); exists {
				return found, true
			}
		}
	}
	return nil, false
}

func completed(request providerv1.Request, now func() time.Time, name string, outcome providerv1.SignalOutcome) providerv1.Result {
	return providerv1.Result{
		Contract:  request.Contract,
		AttemptID: request.AttemptID,
		Outcome:   providerv1.ResultOutcomeCompleted,
		Signals: []providerv1.Signal{{
			Name:    name,
			Outcome: outcome,
		}},
		CompletedAt: now().UTC(),
	}
}

func failed(request providerv1.Request, now func() time.Time, class providerv1.FailureClass, code string, retry providerv1.RetryDisposition, after time.Duration) providerv1.Result {
	return providerv1.Result{
		Contract:  request.Contract,
		AttemptID: request.AttemptID,
		Outcome:   providerv1.ResultOutcomeFailed,
		Failure: &providerv1.Failure{
			Class:      class,
			Code:       code,
			Retry:      retry,
			RetryAfter: after,
		},
		CompletedAt: now().UTC(),
	}
}

func transportFailure(request providerv1.Request, now func() time.Time, status int, retryAfter time.Duration) providerv1.Result {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return failed(request, now, providerv1.FailureInvalidRequest, "provider_invalid_request", providerv1.RetryNever, 0)
	case http.StatusUnauthorized:
		return failed(request, now, providerv1.FailureUnauthenticated, "provider_unauthenticated", providerv1.RetryNever, 0)
	case http.StatusForbidden:
		return failed(request, now, providerv1.FailureUnauthorized, "provider_unauthorized", providerv1.RetryNever, 0)
	case http.StatusTooManyRequests:
		return failed(request, now, providerv1.FailureRateLimited, "provider_rate_limited", providerv1.RetryBackoff, retryAfter)
	default:
		return failed(request, now, providerv1.FailureUnavailable, "provider_unavailable", providerv1.RetryBackoff, retryAfter)
	}
}

func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}
	seconds, err := time.ParseDuration(value + "s")
	if err != nil || seconds < 0 || seconds > time.Hour {
		return 0
	}
	return seconds
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}

var _ providerv1.Adapter = (*Adapter)(nil)
