package smileid

import (
	"context"
	"crypto/hmac"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

var callbackJobPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// maximumCallbackTimestampSkew bounds replay from a captured Smile ID
// verification webhook. Smile ID delivers the HMAC over the timestamp, partner
// ID and the literal "sid_request" in response headers.
const maximumCallbackTimestampSkew = 5 * time.Minute

// callbackPayload is the reviewed Smile ID verification-webhook shape.
type callbackPayload struct {
	Status    string `json:"status"`
	Message   string `json:"message"`
	Reason    string `json:"reason"`
	Product   string `json:"product"`
	Completed string `json:"completed_at"`
	Partner   struct {
		JobType *int   `json:"job_type"`
		JobID   string `json:"job_id"`
		UserID  string `json:"user_id"`
	} `json:"partner_params"`
}

// VerifyCallback authenticates one raw Smile ID verification webhook using the
// documented Response-Signature scheme and normalises it into bounded provider
// progress. It never returns or retains the raw callback.
func (adapter *Adapter) VerifyCallback(
	ctx context.Context,
	request providerv1.Request,
	callback providerv1.CallbackEnvelope,
) (providerv1.Progress, error) {
	if adapter == nil || ctx == nil || request.Validate() != nil || callback.Validate() != nil {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionMalformed, "callback")
	}
	if !pinned(request, manifest()) {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionUnsupported, "request")
	}
	if request.Check != "idenqa.check.document_biometric" && request.Check != "idenqa.check.authority_biometric" {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionUnsupported, "check")
	}
	if callback.Method != http.MethodPost {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionMalformed, "method")
	}
	configuration, err := adapter.secrets.ResolveSmileID(ctx, request.Configuration.SecretReference, request.Configuration.CredentialVersion)
	if err != nil || validateConfig(configuration) != nil {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionConfiguration, "configuration")
	}
	headers := callbackHeaders(callback.Headers)
	if !verifyCallbackSignature(configuration, headers["response-timestamp"], headers["response-signature"]) {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionSignature, "response-signature")
	}
	signedAt, err := time.Parse(time.RFC3339Nano, headers["response-timestamp"])
	if err != nil {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionMalformed, "response-timestamp")
	}
	now := adapter.now().UTC()
	if signedAt.Before(now.Add(-maximumCallbackTimestampSkew)) || signedAt.After(now.Add(time.Minute)) {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionStale, "response-timestamp")
	}
	if !callbackJobPattern.MatchString(headers["job-id"]) || headers["user-id"] != request.VerificationID {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionIdentity, "job-id")
	}
	var payload callbackPayload
	if json.Unmarshal(callback.Body, &payload) != nil {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionMalformed, "body")
	}
	if payload.Partner.JobType != nil && *payload.Partner.JobType != 6 && *payload.Partner.JobType != 1 {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionIdentity, "partner_params.job_type")
	}
	if payload.Partner.JobID != "" && payload.Partner.JobID != request.AttemptID {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionIdentity, "partner_params.job_id")
	}
	if payload.Partner.UserID != "" && payload.Partner.UserID != request.VerificationID {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionIdentity, "partner_params.user_id")
	}
	progress := providerv1.Progress{ProviderJobID: headers["job-id"], ReplayID: headers["job-id"]}
	switch payload.Status {
	case "clear", "attention", "block":
		result := normaliseCallback(request, payload.Status, adapter.now)
		progress.Result = &result
	case "error":
		result := failed(request, adapter.now, providerv1.FailureUnavailable, "provider_callback_error", providerv1.RetryReconcile, 0)
		progress.Result = &result
	default:
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionUnsupported, "status")
	}
	if progress.ValidateForRequest(request) != nil {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionMalformed, "progress")
	}
	return progress, nil
}

// callbackTarget binds the configured public callback URL to one opaque
// per-attempt reference. Without a reference the configured URL is unchanged.
func callbackTarget(configuration Config, request providerv1.Request) string {
	if request.CallbackReference == "" {
		return configuration.CallbackURL
	}
	target := strings.TrimSuffix(configuration.CallbackURL, "/") + "/v1/provider-callbacks/" + request.CallbackReference
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return configuration.CallbackURL
	}
	return target
}

func verifyCallbackSignature(configuration Config, timestamp, encodedSignature string) bool {
	received, err := base64.StdEncoding.DecodeString(encodedSignature)
	if err != nil || timestamp == "" {
		return false
	}
	expected, _ := base64.StdEncoding.DecodeString(sign(configuration.APIKey, timestamp, configuration.PartnerID))
	return hmac.Equal(received, expected)
}

func callbackHeaders(headers []providerv1.CallbackHeader) map[string]string {
	indexed := make(map[string]string, len(headers))
	for _, header := range headers {
		indexed[strings.ToLower(header.Name)] = header.Value
	}
	return indexed
}

// normaliseCallback maps the documented overall webhook status into the same
// closed signal vocabulary as status polling. Per-action checks are absent
// from the webhook payload, so they remain inconclusive rather than inferred.
func normaliseCallback(request providerv1.Request, status string, now func() time.Time) providerv1.Result {
	overall := providerv1.SignalOutcomeInconclusive
	switch status {
	case "clear":
		overall = providerv1.SignalOutcomeSatisfied
	case "block":
		overall = providerv1.SignalOutcomeNotSatisfied
	}
	signals := []providerv1.Signal{
		{Name: "idenqa.signal.provider_job", Outcome: overall},
		{Name: "idenqa.signal.liveness", Outcome: providerv1.SignalOutcomeInconclusive},
		{Name: "idenqa.signal.face_match_1to1", Outcome: providerv1.SignalOutcomeInconclusive},
		{Name: "idenqa.signal.document_authenticity", Outcome: providerv1.SignalOutcomeInconclusive},
	}
	return providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted, Signals: signals, CompletedAt: now().UTC()}
}
