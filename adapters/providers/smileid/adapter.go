// Package smileid implements the Idenqa provider contract for Smile ID's
// reviewed REST upload and job-status APIs.
package smileid

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

const (
	configurationDigest = "sha256:6c970ac324797952f7d25b4dd847db9b9b65a86a7fdc9c42e85e36bd72a480a1"
	packageDigest       = "sha256:1cf0a62a1a739943e1ade0ed763ebbf9c20dce9969b4e3b6809bce820e15b46c"
	maximumBodyBytes    = 1 << 20
	maximumEvidence     = 10 << 20
	maximumArchive      = 48 << 20
	timestampLayout     = "2006-01-02T15:04:05.000Z"
)

var (
	// ErrConfiguration identifies an unavailable or invalid tenant configuration.
	ErrConfiguration = errors.New("smileid: configuration unavailable")
	// ErrEvidence identifies unavailable or oversized evidence.
	ErrEvidence = errors.New("smileid: evidence unavailable")
)

// Config is the runner-resolved tenant-owned Smile ID configuration.
type Config struct {
	BaseURL      string
	PartnerID    string
	APIKey       string
	CallbackURL  string
	Mode         string
	Region       string
	PollInterval time.Duration
}

// SecretResolver resolves exactly one tenant credential reference.
type SecretResolver interface {
	ResolveSmileID(context.Context, string, string) (Config, error)
}

// InputResolver resolves one purpose-bound structured input.
type InputResolver interface {
	ResolveProviderInput(context.Context, string) (string, error)
}

// EvidenceReader redeems evidence only inside the isolated runner.
type EvidenceReader interface {
	ReadProviderEvidence(context.Context, providerv1.EvidenceGrantReference, int64) ([]byte, error)
}

// HTTPClient is the deployment-owned bounded transport.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// Waiter provides cancellable polling and deterministic tests.
type Waiter interface {
	Wait(context.Context, time.Duration) error
}

type timerWaiter struct{}

func (timerWaiter) Wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// Adapter implements the provider-neutral boundary.
type Adapter struct {
	secrets  SecretResolver
	inputs   InputResolver
	evidence EvidenceReader
	client   HTTPClient
	waiter   Waiter
	now      func() time.Time
}

// New constructs a Smile ID adapter.
func New(secrets SecretResolver, inputs InputResolver, evidence EvidenceReader, client HTTPClient, waiter Waiter, now func() time.Time) (*Adapter, error) {
	if secrets == nil || inputs == nil || evidence == nil || client == nil || now == nil {
		return nil, ErrConfiguration
	}
	if waiter == nil {
		waiter = timerWaiter{}
	}
	return &Adapter{secrets: secrets, inputs: inputs, evidence: evidence, client: client, waiter: waiter, now: now}, nil
}

// Manifest returns the frozen provider-neutral Smile ID capabilities.
func (*Adapter) Manifest(context.Context) (providerv1.Manifest, error) { return manifest(), nil }

// ValidateConfiguration resolves and validates tenant-owned credentials.
func (adapter *Adapter) ValidateConfiguration(ctx context.Context, reference providerv1.ConfigurationReference) error {
	if adapter == nil || ctx == nil || reference.Validate() != nil || reference.SchemaDigest != configurationDigest {
		return ErrConfiguration
	}
	configuration, err := adapter.secrets.ResolveSmileID(ctx, reference.SecretReference, reference.CredentialVersion)
	if err != nil || validateConfig(configuration) != nil {
		return ErrConfiguration
	}
	return nil
}

// Health reports local readiness without sending subject data.
func (adapter *Adapter) Health(ctx context.Context) (providerv1.Health, error) {
	if adapter == nil || ctx == nil {
		return providerv1.Health{}, ErrConfiguration
	}
	if err := ctx.Err(); err != nil {
		return providerv1.Health{}, err
	}
	return providerv1.Health{State: providerv1.HealthReady, Code: "adapter_ready", CheckedAt: adapter.now().UTC()}, nil
}

// Execute submits an idempotently named job, uploads a bounded package, and
// polls the official status endpoint until the attempt deadline.
func (adapter *Adapter) Execute(ctx context.Context, request providerv1.Request) (providerv1.Result, error) {
	if adapter == nil || ctx == nil {
		return providerv1.Result{}, ErrConfiguration
	}
	if request.Validate() != nil {
		return failed(request, adapter.now, providerv1.FailureInvalidRequest, "invalid_request", providerv1.RetryNever, 0), nil
	}
	advertised := manifest()
	if !pinned(request, advertised) {
		return failed(request, adapter.now, providerv1.FailureUnsupported, "manifest_mismatch", providerv1.RetryNever, 0), nil
	}
	configuration, err := adapter.secrets.ResolveSmileID(ctx, request.Configuration.SecretReference, request.Configuration.CredentialVersion)
	if err != nil || validateConfig(configuration) != nil {
		return failed(request, adapter.now, providerv1.FailureUnauthenticated, "configuration_unavailable", providerv1.RetryNever, 0), nil
	}
	values, err := adapter.resolveInputs(ctx, request.Inputs)
	if err != nil {
		return failed(request, adapter.now, providerv1.FailureInvalidRequest, "input_unavailable", providerv1.RetryNever, 0), nil
	}
	jobType := 6
	if request.Check == "idenqa.check.authority_biometric" {
		jobType = 1
	}
	if request.Check != "idenqa.check.document_biometric" && request.Check != "idenqa.check.authority_biometric" {
		return failed(request, adapter.now, providerv1.FailureUnsupported, "check_unsupported", providerv1.RetryNever, 0), nil
	}
	images, err := adapter.images(ctx, request)
	if err != nil {
		return failed(request, adapter.now, providerv1.FailureInvalidRequest, "evidence_unavailable", providerv1.RetryNever, 0), nil
	}
	defer clearImages(images)
	userID := request.VerificationID
	jobID := request.AttemptID
	timestamp := adapter.now().UTC().Format(timestampLayout)
	signature := sign(configuration.APIKey, timestamp, configuration.PartnerID)
	prep := prepRequest{
		SourceSDK: "rest_api", SourceSDKVersion: "idenqa-0.1.0", Signature: signature, Timestamp: timestamp,
		SmileClientID: configuration.PartnerID, CallbackURL: configuration.CallbackURL,
		PartnerParams: partnerParams{JobType: jobType, JobID: jobID, UserID: userID},
	}
	prepResult, status, retryAfter, prepErr := adapter.jsonRequest(ctx, configuration, http.MethodPost, "/v1/upload", prep)
	duplicate := status == http.StatusBadRequest && stringValue(prepResult["code"]) == "2215"
	if prepErr != nil && !duplicate {
		return transportFailure(request, adapter.now, status, retryAfter), nil
	}
	if !duplicate {
		uploadURL := stringValue(prepResult["upload_url"])
		if !validUploadURL(uploadURL) {
			return failed(request, adapter.now, providerv1.FailureProviderRejected, "invalid_upload_target", providerv1.RetryNever, 0), nil
		}
		archive, archiveErr := packageArchive(values, images)
		if archiveErr != nil {
			return failed(request, adapter.now, providerv1.FailureInvalidRequest, "package_invalid", providerv1.RetryNever, 0), nil
		}
		defer wipe(archive)
		if _, uploadStatus, uploadRetry, uploadErr := adapter.rawRequest(ctx, configuration, http.MethodPut, uploadURL, "application/zip", archive); uploadErr != nil {
			return transportFailure(request, adapter.now, uploadStatus, uploadRetry), nil
		}
	}
	return adapter.poll(ctx, request, configuration, userID, jobID), nil
}

func (adapter *Adapter) poll(ctx context.Context, request providerv1.Request, configuration Config, userID, jobID string) providerv1.Result {
	for {
		if !adapter.now().Before(request.Deadline) {
			return failed(request, adapter.now, providerv1.FailureDeadline, "provider_deadline", providerv1.RetryReconcile, 0)
		}
		timestamp := adapter.now().UTC().Format(timestampLayout)
		payload := map[string]any{"history": false, "image_links": false, "job_id": jobID, "user_id": userID, "partner_id": configuration.PartnerID, "signature": sign(configuration.APIKey, timestamp, configuration.PartnerID), "timestamp": timestamp}
		statusResult, status, retryAfter, err := adapter.jsonRequest(ctx, configuration, http.MethodPost, "/v1/job_status", payload)
		if err != nil {
			return transportFailure(request, adapter.now, status, retryAfter)
		}
		if !verifyResponseSignature(statusResult, configuration) {
			return failed(request, adapter.now, providerv1.FailureProviderRejected, "invalid_response_signature", providerv1.RetryNever, 0)
		}
		complete, _ := statusResult["job_complete"].(bool)
		if complete {
			return normalise(request, statusResult, adapter.now)
		}
		if err := adapter.waiter.Wait(ctx, configuration.PollInterval); err != nil {
			return failed(request, adapter.now, providerv1.FailureCancelled, "poll_cancelled", providerv1.RetryReconcile, 0)
		}
	}
}

type partnerParams struct {
	JobType int    `json:"job_type"`
	JobID   string `json:"job_id"`
	UserID  string `json:"user_id"`
}
type prepRequest struct {
	SourceSDK        string        `json:"source_sdk"`
	SourceSDKVersion string        `json:"source_sdk_version"`
	Signature        string        `json:"signature"`
	Timestamp        string        `json:"timestamp"`
	SmileClientID    string        `json:"smile_client_id"`
	CallbackURL      string        `json:"callback_url"`
	PartnerParams    partnerParams `json:"partner_params"`
}
type image struct {
	Type  int    `json:"image_type_id"`
	Value []byte `json:"-"`
}

func (adapter *Adapter) images(ctx context.Context, request providerv1.Request) ([]image, error) {
	result := make([]image, 0, len(request.Evidence))
	for _, grant := range request.Evidence {
		kind, ok := map[string]int{"selfie": 2, "document.front": 3, "document.back": 7, "liveness": 6}[baseVariant(grant.Variant)]
		if !ok {
			continue
		}
		value, err := adapter.evidence.ReadProviderEvidence(ctx, grant, maximumEvidence)
		if err != nil || len(value) == 0 || len(value) > maximumEvidence {
			wipe(value)
			clearImages(result)
			return nil, ErrEvidence
		}
		result = append(result, image{Type: kind, Value: value})
	}
	if len(result) == 0 || len(result) > 16 {
		clearImages(result)
		return nil, ErrEvidence
	}
	return result, nil
}

func packageArchive(values map[string]string, images []image) ([]byte, error) {
	entries := make([]map[string]any, len(images))
	for index, item := range images {
		entries[index] = map[string]any{"image_type_id": item.Type, "image": base64.StdEncoding.EncodeToString(item.Value)}
	}
	info := map[string]any{
		"package_information": map[string]any{"apiVersion": map[string]int{"buildNumber": 0, "majorVersion": 2, "minorVersion": 0}},
		"id_info":             map[string]any{"country": values["idenqa.input.country"], "id_type": values["idenqa.input.id_type"], "id_number": values["idenqa.input.id_number"], "entered": true},
		"images":              entries,
	}
	encoded, err := json.Marshal(info)
	if err != nil {
		return nil, err
	}
	defer wipe(encoded)
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	entry, err := writer.Create("info.json")
	if err == nil {
		_, err = entry.Write(encoded)
	}
	closeErr := writer.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if output.Len() > maximumArchive {
		return nil, errors.New("archive too large")
	}
	return output.Bytes(), nil
}

func (adapter *Adapter) resolveInputs(ctx context.Context, references []providerv1.InputReference) (map[string]string, error) {
	values := make(map[string]string, len(references))
	for _, reference := range references {
		value, err := adapter.inputs.ResolveProviderInput(ctx, reference.Reference)
		if err != nil || value == "" || len(value) > 512 {
			return nil, errors.New("input unavailable")
		}
		values[reference.Name] = value
	}
	if values["idenqa.input.country"] == "" || values["idenqa.input.id_type"] == "" {
		return nil, errors.New("country and id type required")
	}
	return values, nil
}

func (adapter *Adapter) jsonRequest(ctx context.Context, configuration Config, method, path string, payload any) (map[string]any, int, time.Duration, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, 0, err
	}
	defer wipe(encoded)
	return adapter.rawRequest(ctx, configuration, method, strings.TrimSuffix(configuration.BaseURL, "/")+path, "application/json", encoded)
}

func (adapter *Adapter) rawRequest(ctx context.Context, _ Config, method, endpoint, contentType string, payload []byte) (map[string]any, int, time.Duration, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, 0, err
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set("Accept", "application/json")
	response, err := adapter.client.Do(request)
	if err != nil {
		return nil, 0, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	retryAfter := parseRetryAfter(response.Header.Get("Retry-After"))
	encoded, readErr := io.ReadAll(io.LimitReader(response.Body, maximumBodyBytes+1))
	if readErr != nil || len(encoded) > maximumBodyBytes {
		return nil, response.StatusCode, retryAfter, errors.New("invalid bounded response")
	}
	defer wipe(encoded)
	var decoded map[string]any
	if len(encoded) > 0 && json.Unmarshal(encoded, &decoded) != nil {
		return nil, response.StatusCode, retryAfter, errors.New("invalid response")
	}
	if response.StatusCode < 200 || response.StatusCode > 299 {
		return decoded, response.StatusCode, retryAfter, errors.New("provider rejected request")
	}
	return decoded, response.StatusCode, retryAfter, nil
}

func normalise(request providerv1.Request, response map[string]any, now func() time.Time) providerv1.Result {
	success, _ := response["job_success"].(bool)
	result, _ := response["result"].(map[string]any)
	actions, _ := result["Actions"].(map[string]any)
	outcome := providerv1.SignalOutcomeNotSatisfied
	if success {
		outcome = providerv1.SignalOutcomeSatisfied
	}
	signals := []providerv1.Signal{
		{Name: "idenqa.signal.provider_job", Outcome: outcome},
		{Name: "idenqa.signal.liveness", Outcome: actionOutcome(actions, "Liveness_Check", "Selfie_Check")},
		{Name: "idenqa.signal.face_match_1to1", Outcome: actionOutcome(actions, "Selfie_To_ID_Card_Compare", "Selfie_To_ID_Authority_Compare")},
		{Name: "idenqa.signal.document_authenticity", Outcome: actionOutcome(actions, "Document_Check", "Verify_ID_Number")},
	}
	return providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted, Signals: signals, CompletedAt: now().UTC()}
}

func actionOutcome(actions map[string]any, names ...string) providerv1.SignalOutcome {
	for _, name := range names {
		value := strings.ToLower(stringValue(actions[name]))
		switch value {
		case "passed", "completed", "verified", "approved", "returned":
			return providerv1.SignalOutcomeSatisfied
		case "failed", "not verified", "rejected":
			return providerv1.SignalOutcomeNotSatisfied
		}
	}
	return providerv1.SignalOutcomeInconclusive
}

func sign(apiKey, timestamp, partnerID string) string {
	mac := hmac.New(sha256.New, []byte(apiKey))
	_, _ = mac.Write([]byte(timestamp + partnerID + "sid_request"))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func verifyResponseSignature(response map[string]any, configuration Config) bool {
	timestamp := stringValue(response["timestamp"])
	encodedSignature := stringValue(response["signature"])
	if encodedSignature == "" {
		encodedSignature = stringValue(response["sec_key"])
	}
	received, err := base64.StdEncoding.DecodeString(encodedSignature)
	if err != nil || timestamp == "" {
		return false
	}
	expected, _ := base64.StdEncoding.DecodeString(sign(configuration.APIKey, timestamp, configuration.PartnerID))
	return hmac.Equal(received, expected)
}

func manifest() providerv1.Manifest {
	return providerv1.Manifest{
		Package:       providerv1.PackageProvenance{AdapterID: "smileid", AdapterVersion: "0.1.0", PackageDigest: packageDigest, Contract: providerv1.CurrentVersion},
		Configuration: providerv1.ConfigurationSchema{ID: "smileid.tenant.v1", Digest: configurationDigest},
		Capabilities: []providerv1.Capability{
			{Check: "idenqa.check.document_biometric", AcceptedEvidence: []string{"idenqa.evidence.document_image", "idenqa.evidence.selfie_image"}, AcceptedInputs: authorityInputs(), AcceptedAssurances: []string{"idenqa.assurance.capture_quality", "idenqa.assurance.active_liveness", "idenqa.assurance.face_match_1to1", "idenqa.assurance.document_authenticity"}, ProcessingRegions: []string{"africa"}, SupportsIdempotency: true},
			{Check: "idenqa.check.authority_biometric", AcceptedEvidence: []string{"idenqa.evidence.selfie_image"}, AcceptedInputs: authorityInputs(), AcceptedAssurances: []string{"idenqa.assurance.active_liveness", "idenqa.assurance.face_match_1to1"}, ProcessingRegions: []string{"africa"}, SupportsIdempotency: true},
		},
		Restrictions: providerv1.Restrictions{NetworkRequired: true, MaximumGrants: 16, MaximumResultSize: 32 * 1024, MaximumDuration: 10 * time.Minute},
	}
}

func authorityInputs() []string {
	return []string{"idenqa.input.country", "idenqa.input.id_type", "idenqa.input.id_number"}
}

func validateConfig(configuration Config) error {
	base, err := url.Parse(configuration.BaseURL)
	callback, callbackErr := url.Parse(configuration.CallbackURL)
	if err != nil || callbackErr != nil || base.Scheme != "https" || base.Host == "" || base.User != nil || callback.Scheme != "https" || callback.Host == "" ||
		configuration.PartnerID == "" || configuration.APIKey == "" || (configuration.Mode != "sandbox" && configuration.Mode != "production") ||
		configuration.Region != "africa" || configuration.PollInterval <= 0 || configuration.PollInterval > time.Minute {
		return ErrConfiguration
	}
	return nil
}

func pinned(request providerv1.Request, advertised providerv1.Manifest) bool {
	if request.Adapter != advertised.Package || request.Restrictions != advertised.Restrictions || request.Configuration.SchemaDigest != advertised.Configuration.Digest {
		return false
	}
	for _, candidate := range advertised.Capabilities {
		left, _ := json.Marshal(candidate)
		right, _ := json.Marshal(request.Capability)
		if candidate.Check == request.Check && bytes.Equal(left, right) {
			return true
		}
	}
	return false
}

func validUploadURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != "" && parsed.User == nil
}
func baseVariant(value string) string {
	if strings.HasPrefix(value, "liveness.") {
		return "liveness"
	}
	return value
}
func stringValue(value any) string {
	switch item := value.(type) {
	case string:
		return item
	case json.Number:
		return item.String()
	case float64:
		return strconv.FormatInt(int64(item), 10)
	default:
		return ""
	}
}
func parseRetryAfter(value string) time.Duration {
	seconds, err := strconv.Atoi(value)
	if err != nil || seconds < 0 || seconds > 3600 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
func clearImages(values []image) {
	for index := range values {
		wipe(values[index].Value)
	}
}

func failed(request providerv1.Request, now func() time.Time, class providerv1.FailureClass, code string, retry providerv1.RetryDisposition, after time.Duration) providerv1.Result {
	return providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeFailed, Failure: &providerv1.Failure{Class: class, Code: code, Retry: retry, RetryAfter: after}, CompletedAt: now().UTC()}
}
func transportFailure(request providerv1.Request, now func() time.Time, status int, after time.Duration) providerv1.Result {
	switch status {
	case 400, 422:
		return failed(request, now, providerv1.FailureInvalidRequest, "provider_invalid_request", providerv1.RetryNever, 0)
	case 401:
		return failed(request, now, providerv1.FailureUnauthenticated, "provider_unauthenticated", providerv1.RetryNever, 0)
	case 403:
		return failed(request, now, providerv1.FailureUnauthorized, "provider_unauthorized", providerv1.RetryNever, 0)
	case 429:
		return failed(request, now, providerv1.FailureRateLimited, "provider_rate_limited", providerv1.RetryBackoff, after)
	default:
		return failed(request, now, providerv1.FailureUnavailable, "provider_unavailable", providerv1.RetryBackoff, after)
	}
}

var _ providerv1.Adapter = (*Adapter)(nil)
