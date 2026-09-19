package smileid

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

// Description returns a fresh immutable catalogue snapshot.
func Description() providerv1.Manifest { return manifest() }

// Advance implements document submission and status-only recovery. The caller
// must durably claim initial submission before invoking resume=false.
func (adapter *Adapter) Advance(ctx context.Context, request providerv1.Request, resume bool) (providerv1.Progress, error) {
	if adapter == nil || ctx == nil || request.Validate() != nil || !pinned(request, manifest()) || request.Check != "idenqa.check.document_biometric" || !adapter.now().Before(request.Deadline) {
		return providerv1.Progress{}, ErrConfiguration
	}
	configuration, err := adapter.secrets.ResolveSmileID(ctx, request.Configuration.SecretReference, request.Configuration.CredentialVersion)
	if err != nil || validateConfig(configuration) != nil {
		return providerv1.Progress{}, ErrConfiguration
	}
	if resume {
		return adapter.status(ctx, request, configuration)
	}
	values, err := adapter.resolveInputs(ctx, request.Inputs)
	if err != nil {
		return providerv1.Progress{}, err
	}
	// The initial product requires an explicit selfie and document front. No
	// SDK capability advertisement or file upload implies liveness assurance.
	if len(request.Evidence) != 2 {
		return providerv1.Progress{}, ErrEvidence
	}
	variants := map[string]bool{}
	for _, grant := range request.Evidence {
		variants[grant.Variant] = true
	}
	if !variants["selfie"] || !variants["document.front"] {
		return providerv1.Progress{}, ErrEvidence
	}
	images, err := adapter.images(ctx, request)
	if err != nil {
		return providerv1.Progress{}, err
	}
	defer clearImages(images)
	for _, image := range images {
		if http.DetectContentType(image.Value) != "image/jpeg" {
			return providerv1.Progress{}, ErrEvidence
		}
	}
	archive, err := packageArchive(values, images)
	if err != nil {
		return providerv1.Progress{}, err
	}
	defer wipe(archive)
	timestamp := adapter.now().UTC().Format(timestampLayout)
	prep := prepRequest{
		SourceSDK:        "rest_api",
		SourceSDKVersion: "idenqa-0.1.1",
		Signature:        sign(configuration.APIKey, timestamp, configuration.PartnerID),
		Timestamp:        timestamp,
		SmileClientID:    configuration.PartnerID,
		CallbackURL:      callbackTarget(configuration, request),
		PartnerParams: partnerParams{
			JobType: 6,
			JobID:   request.AttemptID,
			UserID:  request.VerificationID,
		},
	}
	response, status, _, err := adapter.jsonRequest(ctx, configuration, "/v1/upload", prep)
	if status == http.StatusBadRequest && stringValue(response["code"]) == "2215" {
		return adapter.status(ctx, request, configuration)
	}
	if err != nil || stringValue(response["code"]) != "2202" {
		return providerv1.Progress{}, errors.New("smileid: submission outcome unavailable")
	}
	progress := providerv1.Progress{ProviderJobID: stringValue(response["smile_job_id"])}
	if progress.ProviderJobID == "" || progress.ValidateForRequest(request) != nil {
		return providerv1.Progress{}, errors.New("smileid: invalid submission reference")
	}
	uploadURL := stringValue(response["upload_url"])
	if !validJobUploadURL(uploadURL, configuration.PartnerID, progress.ProviderJobID) {
		return progress, nil
	}
	// If upload acknowledgement is lost, retain the reference and recover using
	// job_status. Never repeat upload preparation or consume a grant again.
	if _, _, _, err := adapter.rawRequest(ctx, configuration, http.MethodPut, uploadURL, "application/zip", archive); err != nil {
		return progress, nil
	}
	return progress, nil
}

func (adapter *Adapter) status(ctx context.Context, request providerv1.Request, configuration Config) (providerv1.Progress, error) {
	timestamp := adapter.now().UTC().Format(timestampLayout)
	payload := map[string]any{"history": false, "image_links": false, "job_id": request.AttemptID, "user_id": request.VerificationID, "partner_id": configuration.PartnerID, "signature": sign(configuration.APIKey, timestamp, configuration.PartnerID), "timestamp": timestamp}
	response, _, _, err := adapter.jsonRequest(ctx, configuration, "/v1/job_status", payload)
	if err != nil {
		return providerv1.Progress{}, errors.New("smileid: status unavailable")
	}
	if !verifyResponseSignature(response, configuration) {
		return providerv1.Progress{}, errors.New("smileid: response signature invalid")
	}
	signedAt, err := time.Parse(time.RFC3339Nano, stringValue(response["timestamp"]))
	if err != nil || signedAt.Before(adapter.now().Add(-5*time.Minute)) || signedAt.After(adapter.now().Add(time.Minute)) {
		return providerv1.Progress{}, errors.New("smileid: response timestamp invalid")
	}
	// Missing jobs and missing uploads are unresolved external outcomes. They do
	// not authorize a new job and cannot be represented as successful completion.
	code := stringValue(response["code"])
	if code == "2304" || code == "2314" {
		return providerv1.Progress{}, nil
	}
	if code != "2302" {
		return providerv1.Progress{}, errors.New("smileid: status unavailable")
	}
	result, _ := response["result"].(map[string]any)
	params, _ := result["PartnerParams"].(map[string]any)
	if kind := stringValue(params["job_type"]); kind != "" && kind != "6" {
		return providerv1.Progress{}, errors.New("smileid: job type mismatch")
	}
	jobID, userID := stringValue(response["job_id"]), stringValue(response["user_id"])
	if jobID == "" {
		jobID = stringValue(params["job_id"])
	}
	if userID == "" {
		userID = stringValue(params["user_id"])
	}
	if jobID != request.AttemptID || userID != request.VerificationID {
		return providerv1.Progress{}, errors.New("smileid: job identity mismatch")
	}
	if value := stringValue(params["job_id"]); value != "" && value != request.AttemptID {
		return providerv1.Progress{}, errors.New("smileid: result identity mismatch")
	}
	if value := stringValue(params["user_id"]); value != "" && value != request.VerificationID {
		return providerv1.Progress{}, errors.New("smileid: result identity mismatch")
	}
	progress := providerv1.Progress{ProviderJobID: stringValue(result["SmileJobID"])}
	if progress.ValidateForRequest(request) != nil {
		return providerv1.Progress{}, errors.New("smileid: job reference invalid")
	}
	complete, ok := response["job_complete"].(bool)
	if !ok {
		return providerv1.Progress{}, errors.New("smileid: completion state invalid")
	}
	if !complete {
		return progress, nil
	}
	if progress.ProviderJobID == "" {
		return providerv1.Progress{}, errors.New("smileid: completed reference missing")
	}
	if _, ok := response["job_success"].(bool); !ok {
		return providerv1.Progress{}, errors.New("smileid: success state invalid")
	}
	normalized := normalise(request, response, adapter.now)
	for i := range normalized.Signals {
		if normalized.Signals[i].Name == "idenqa.signal.liveness" {
			normalized.Signals[i].Outcome = providerv1.SignalOutcomeInconclusive
		}
	}
	progress.Result = &normalized
	return progress, nil
}

// The deployment transport separately pins the reviewed origin and DNS answers.
func validJobUploadURL(value, partner, job string) bool {
	if !validUploadURL(value) {
		return false
	}
	target, err := url.Parse(value)
	if err != nil || target.Fragment != "" || target.RawPath != "" {
		return false
	}
	parts := strings.Split(target.Path, "/")
	return len(parts) == 5 && parts[0] == "" && parts[1] == "videos" && parts[2] == partner && strings.HasPrefix(parts[3], partner+"-"+job+"-") && (parts[4] == "attachments.zip" || parts[4] == "selfie.zip")
}
