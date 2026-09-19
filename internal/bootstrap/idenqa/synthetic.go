package idenqa

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/evidence"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/spf13/cobra"
)

const (
	syntheticDefaultProfileFile = "examples/synthetic/capture-profile.json"
	syntheticDefaultPolicyFile  = "examples/synthetic/policy.json"
	syntheticDefaultPrefix      = "synthetic"
	syntheticDefaultTimeout     = 2 * time.Minute
	syntheticDefaultPoll        = time.Second
	syntheticMaximumFileBytes   = 1 << 20
	syntheticMaximumResponse    = 1 << 20
	syntheticMaximumDetailBytes = 512

	syntheticProfileName  = "Synthetic selfie verification"
	syntheticPolicyReason = "synthetic_journey"
	syntheticLocation     = "en-NG"
	syntheticRegion       = "tenant.region.ng"
	syntheticMediaType    = "image/jpeg"
)

type syntheticOptions struct {
	apiURL, keyFile, profileFile, policyFile, idempotencyPrefix string
	region                                                      string
	timeout, pollInterval                                       time.Duration
	jsonOutput                                                  bool
}

func newSyntheticCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "synthetic",
		Short: "Run a complete synthetic verification journey through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("synthetic requires an operation"))
		},
	}
	root.AddCommand(newSyntheticRunCommand())
	return root
}

func newSyntheticRunCommand() *cobra.Command {
	options := &syntheticOptions{
		timeout: syntheticDefaultTimeout, pollInterval: syntheticDefaultPoll,
		region: syntheticRegion,
	}
	command := &cobra.Command{
		Use:   "run",
		Short: "Run one deterministic synthetic journey and print every step",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runSynthetic(command, options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.apiURL, "api-url", "", "Core API base URL (HTTPS, or local loopback HTTP)")
	flags.StringVar(&options.keyFile, "api-key-file", "", "read API credential from this file (otherwise IDENQA_API_KEY)")
	flags.StringVar(&options.profileFile, "profile-file", syntheticDefaultProfileFile, "capture-profile document to ensure and publish")
	flags.StringVar(&options.policyFile, "policy-file", syntheticDefaultPolicyFile, "policy definition to ensure and activate")
	flags.StringVar(&options.idempotencyPrefix, "idempotency-prefix", syntheticDefaultPrefix, "stable prefix for the profile and policy ensure steps")
	flags.StringVar(&options.region, "region", syntheticRegion, "processing-authority and evidence region; must match the deployment IDENQA_REGION for self-hosted Core")
	flags.DurationVar(&options.timeout, "timeout", syntheticDefaultTimeout, "bounded wait for the completed decision")
	flags.DurationVar(&options.pollInterval, "poll-interval", syntheticDefaultPoll, "poll interval while awaiting the completed decision")
	flags.BoolVar(&options.jsonOutput, "json", false, "print each journey step as one JSON object per line")
	return command
}

func runSynthetic(command *cobra.Command, options *syntheticOptions) error {
	base, err := parsePublicAPIURL(options.apiURL)
	if err != nil {
		return cli.UsageError(err)
	}
	if options.timeout <= 0 || options.pollInterval <= 0 || options.pollInterval > options.timeout {
		return cli.UsageError(errors.New("timeout must be positive and at least one poll interval"))
	}
	if !validSyntheticPrefix(options.idempotencyPrefix) {
		return cli.UsageError(errors.New("idempotency-prefix must be 1 to 100 characters from a-z, A-Z, 0-9, dot, colon, underscore, or hyphen"))
	}
	if !validSyntheticRegion(options.region) {
		return cli.UsageError(errors.New("region must be a bounded processing-authority code from a-z, 0-9, dot, underscore, or hyphen"))
	}
	credential, err := readPublicAPICredential(options.keyFile)
	if err != nil {
		return err
	}
	if credential == "" {
		return cli.UsageError(errors.New("an API credential file or IDENQA_API_KEY is required"))
	}
	profileDocument, err := readSyntheticDocument(options.profileFile, "capture profile")
	if err != nil {
		return err
	}
	catalog, err := evidence.BuiltInCatalog()
	if err != nil {
		return cli.RuntimeError("synthetic journey", errors.New("load built-in evidence catalog"))
	}
	profile, err := verification.ParseProfileFromCatalog(profileDocument, catalog)
	if err != nil {
		return cli.RuntimeError("synthetic journey", fmt.Errorf("capture profile file is invalid: %w", err))
	}
	if len(profile.Requirements) != 1 {
		return cli.RuntimeError("synthetic journey", errors.New("capture profile file must declare exactly one requirement"))
	}
	policyDocument, err := readSyntheticDocument(options.policyFile, "policy definition")
	if err != nil {
		return err
	}
	if err := validateSyntheticPolicy(policyDocument); err != nil {
		return cli.RuntimeError("synthetic journey", fmt.Errorf("policy definition file is invalid: %w", err))
	}
	runID, err := syntheticRunID()
	if err != nil {
		return cli.RuntimeError("synthetic journey", errors.New("generate synthetic run identifier"))
	}
	client := &syntheticClient{
		base: base, credential: credential,
		http: &http.Client{
			Timeout:       30 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		out: command.OutOrStdout(), jsonOutput: options.jsonOutput,
		prefix: options.idempotencyPrefix, runID: runID, region: options.region,
	}
	return client.run(command.Context(), options, profileDocument, policyDocument, profile.Requirements[0])
}

type syntheticClient struct {
	base       *url.URL
	credential string
	http       *http.Client
	out        io.Writer
	jsonOutput bool
	prefix     string
	runID      string
	region     string
}

func (client *syntheticClient) run(
	ctx context.Context,
	options *syntheticOptions,
	profileDocument, policyDocument []byte,
	requirement verification.Requirement,
) error {
	profileID, profileVersion, err := client.createProfile(ctx, profileDocument)
	if err != nil {
		return err
	}
	if err := client.publishProfile(ctx, profileID, profileVersion); err != nil {
		return err
	}
	policyID, activationVersion, err := client.createPolicy(ctx, policyDocument)
	if err != nil {
		return err
	}
	if err := client.activatePolicy(ctx, policyID, activationVersion); err != nil {
		return err
	}
	verificationID, captureToken, expiresAt, err := client.createVerification(ctx, profileID, policyID)
	if err != nil {
		return err
	}
	noticeID, err := client.createNotice(ctx)
	if err != nil {
		return err
	}
	if err := client.declareAuthority(ctx, verificationID, noticeID, expiresAt); err != nil {
		return err
	}
	if err := client.readAuthoritySnapshot(ctx, captureToken, noticeID); err != nil {
		return err
	}
	if err := client.recordConsent(ctx, captureToken); err != nil {
		return err
	}
	body := syntheticArtefact()
	if err := client.uploadEvidence(ctx, captureToken, requirement, body); err != nil {
		return err
	}
	if err := client.readProgress(ctx, captureToken, verificationID, 1); err != nil {
		return err
	}
	decision, err := client.awaitDecision(ctx, options, verificationID)
	if err != nil {
		return err
	}
	return client.readDecision(ctx, decision)
}

func (client *syntheticClient) createProfile(ctx context.Context, document []byte) (string, int64, error) {
	var created syntheticProfileMutation
	_, err := client.call(ctx, syntheticRequest{
		step: "profile.create", method: http.MethodPost, path: "/v1/capture-profiles",
		permission: "capture_profiles:write", idempotencyKey: client.key("profile-create"),
		body:       syntheticProfileWrite{Name: syntheticProfileName, Document: document},
		wantStatus: http.StatusCreated, result: &created,
	})
	if err != nil {
		return "", 0, err
	}
	if created.ProfileID == "" {
		return "", 0, cli.RuntimeError("synthetic journey", errors.New("step profile.create: API omitted the profile identifier"))
	}
	if err := client.step("profile.create", map[string]string{
		"profile_id": created.ProfileID, "version": strconv.FormatInt(created.Version, 10),
	}); err != nil {
		return "", 0, err
	}
	return created.ProfileID, created.Version, nil
}

func (client *syntheticClient) publishProfile(ctx context.Context, profileID string, version int64) error {
	var published syntheticProfileMutation
	_, err := client.call(ctx, syntheticRequest{
		step: "profile.publish", method: http.MethodPost,
		path:       "/v1/capture-profiles/" + profileID + "/publish",
		permission: "capture_profiles:write", idempotencyKey: client.key("profile-publish"),
		ifMatch:    strconv.Quote(strconv.FormatInt(version, 10)),
		wantStatus: http.StatusOK, result: &published,
	})
	if err != nil {
		return err
	}
	fields := map[string]string{
		"profile_id": profileID, "version": strconv.FormatInt(published.Version, 10),
	}
	if published.PublishedRevision != nil {
		fields["published_revision"] = strconv.Itoa(*published.PublishedRevision)
	}
	return client.step("profile.publish", fields)
}

func (client *syntheticClient) createPolicy(ctx context.Context, document []byte) (string, int64, error) {
	var created syntheticPolicyMutation
	_, err := client.call(ctx, syntheticRequest{
		step: "policy.create", method: http.MethodPost, path: "/v1/policies",
		permission: "policies:write", idempotencyKey: client.key("policy-create"),
		body: syntheticPolicyWrite{Definition: document}, wantStatus: http.StatusOK, result: &created,
	})
	if err != nil {
		return "", 0, err
	}
	if created.Policy.ID == "" {
		return "", 0, cli.RuntimeError("synthetic journey", errors.New("step policy.create: API omitted the policy identifier"))
	}
	fields := map[string]string{
		"policy_id": created.Policy.ID, "activation_version": strconv.FormatInt(created.Policy.ActivationVersion, 10),
		"replayed": strconv.FormatBool(created.Replayed),
	}
	if created.Revision != nil {
		fields["revision"] = strconv.FormatInt(created.Revision.Revision, 10)
	}
	if err := client.step("policy.create", fields); err != nil {
		return "", 0, err
	}
	return created.Policy.ID, created.Policy.ActivationVersion, nil
}

func (client *syntheticClient) activatePolicy(ctx context.Context, policyID string, expectedVersion int64) error {
	var activated syntheticPolicyMutation
	_, err := client.call(ctx, syntheticRequest{
		step: "policy.activate", method: http.MethodPost, path: "/v1/policies/" + policyID + "/activate",
		permission: "policies:activate", idempotencyKey: client.key("policy-activate"),
		body:       syntheticPolicyActivation{Revision: 1, ExpectedVersion: expectedVersion, Reason: syntheticPolicyReason},
		wantStatus: http.StatusOK, result: &activated,
	})
	if err != nil {
		return err
	}
	fields := map[string]string{
		"policy_id": policyID, "activation_version": strconv.FormatInt(activated.Policy.ActivationVersion, 10),
		"replayed": strconv.FormatBool(activated.Replayed),
	}
	if activated.Activation != nil {
		fields["revision"] = strconv.FormatInt(activated.Activation.Revision, 10)
	}
	return client.step("policy.activate", fields)
}

func (client *syntheticClient) createVerification(ctx context.Context, profileID, policyID string) (string, string, time.Time, error) {
	var created syntheticVerificationCreated
	_, err := client.call(ctx, syntheticRequest{
		step: "verification.create", method: http.MethodPost, path: "/v1/verifications",
		permission: "verification_sessions:create", idempotencyKey: client.key("verification-" + client.runID),
		body:       syntheticVerificationWrite{CaptureProfileID: profileID, PolicyID: policyID},
		wantStatus: http.StatusCreated, result: &created,
	})
	if err != nil {
		return "", "", time.Time{}, err
	}
	if created.Session.ID == "" || created.CaptureToken == nil || *created.CaptureToken == "" {
		return "", "", time.Time{}, cli.RuntimeError("synthetic journey", errors.New("step verification.create: API omitted the session identifier or capture token"))
	}
	if err := client.step("verification.create", map[string]string{
		"verification_id": created.Session.ID,
		"state":           created.Session.State,
	}); err != nil {
		return "", "", time.Time{}, err
	}
	return created.Session.ID, *created.CaptureToken, created.Session.ExpiresAt, nil
}

func (client *syntheticClient) createNotice(ctx context.Context) (string, error) {
	// PostgreSQL timestamptz preserves microseconds while the immutable notice
	// digest binds the exact input instant. Keep the synthetic fixture on the
	// documented whole-second Core boundary so the persisted digest always
	// reproduces.
	now := time.Now().UTC().Truncate(time.Second)
	var notice syntheticNotice
	_, err := client.call(ctx, syntheticRequest{
		step: "notice.create", method: http.MethodPost, path: "/v1/notices",
		permission: "notices:write", idempotencyKey: client.key("notice-" + client.runID),
		body: syntheticNoticeWrite{
			Key: "tenant.notice.synthetic_demo", Locale: syntheticLocation,
			Controller: "Idenqa synthetic demonstration", Recipient: "Synthetic subject",
			Copy: syntheticNoticeCopy{
				Title:        "Synthetic identity verification",
				Summary:      "This synthetic journey demonstrates the public verification contract.",
				Purpose:      "Identity verification only.",
				Consequences: "Collection stops if consent is refused.",
			},
			EffectiveAt: now.Add(-time.Minute),
		},
		wantStatus: http.StatusCreated, result: &notice,
	})
	if err != nil {
		return "", err
	}
	if notice.ID == "" {
		return "", cli.RuntimeError("synthetic journey", errors.New("step notice.create: API omitted the notice identifier"))
	}
	if err := client.step("notice.create", map[string]string{"notice_id": notice.ID}); err != nil {
		return "", err
	}
	return notice.ID, nil
}

func (client *syntheticClient) declareAuthority(
	ctx context.Context,
	verificationID, noticeID string,
	expiresAt time.Time,
) error {
	// Match the documented whole-second Core authority boundary.
	now := time.Now().UTC().Truncate(time.Second)
	var declaration syntheticAuthority
	_, err := client.call(ctx, syntheticRequest{
		step: "authority.declare", method: http.MethodPost,
		path:       "/v1/verifications/" + verificationID + "/authority",
		permission: "authorities:write", idempotencyKey: client.key("authority-" + client.runID),
		body: syntheticAuthorityWrite{
			NoticeID: noticeID, Category: "tenant.authority.customer_declared",
			Purpose: "idenqa.purpose.identity_verification", Jurisdiction: "tenant.jurisdiction.ng",
			PolicyPack: "tenant.policy.identity_v1", ConsentRequired: true,
			RecipientReference: "tenant.recipient.primary", RecipientDisplayName: "Synthetic subject",
			Regions: []string{client.region}, RetentionReference: "tenant.retention.identity_v1",
			ValidFrom: now.Add(-time.Minute), ExpiresAt: expiresAt,
		},
		wantStatus: http.StatusCreated, result: &declaration,
	})
	if err != nil {
		return err
	}
	if declaration.ID == "" {
		return cli.RuntimeError("synthetic journey", errors.New("step authority.declare: API omitted the authority identifier"))
	}
	return client.step("authority.declare", map[string]string{
		"authority_id": declaration.ID, "notice_id": noticeID,
	})
}

func (client *syntheticClient) readAuthoritySnapshot(ctx context.Context, captureToken, noticeID string) error {
	var snapshot syntheticAuthoritySnapshot
	_, err := client.call(ctx, syntheticRequest{
		step: "authority.snapshot", method: http.MethodGet, path: "/v1/capture/authority",
		bearer: captureToken, wantStatus: http.StatusOK, result: &snapshot,
	})
	if err != nil {
		return err
	}
	if snapshot.Authority.ID == "" || snapshot.Notice.ID != noticeID {
		return cli.RuntimeError("synthetic journey", errors.New("step authority.snapshot: capture authority does not reflect the declaration"))
	}
	return client.step("authority.snapshot", map[string]string{
		"authority_id": snapshot.Authority.ID, "notice_id": snapshot.Notice.ID,
	})
}

func (client *syntheticClient) recordConsent(ctx context.Context, captureToken string) error {
	var response syntheticSubjectResponse
	_, err := client.call(ctx, syntheticRequest{
		step: "authority.consent", method: http.MethodPost, path: "/v1/capture/authority/responses",
		bearer: captureToken, idempotencyKey: client.key("consent-" + client.runID),
		body:       syntheticSubjectResponseWrite{Action: "consent", Locale: syntheticLocation},
		wantStatus: http.StatusCreated, result: &response,
	})
	if err != nil {
		return err
	}
	if response.ID == "" {
		return cli.RuntimeError("synthetic journey", errors.New("step authority.consent: API omitted the response identifier"))
	}
	return client.step("authority.consent", map[string]string{
		"response_id": response.ID, "action": response.Action,
	})
}

func (client *syntheticClient) uploadEvidence(
	ctx context.Context,
	captureToken string,
	requirement verification.Requirement,
	body []byte,
) error {
	var issued syntheticEvidenceUpload
	issuedHeaders, err := client.call(ctx, syntheticRequest{
		step: "evidence.issue", method: http.MethodPost, path: "/v1/evidence-uploads",
		bearer: captureToken, idempotencyKey: client.key("evidence-" + client.runID),
		body: syntheticEvidenceUploadWrite{
			RequirementKey: requirement.Key, Artefact: string(requirement.Artefacts[0]),
			AcquisitionMethod: string(requirement.Acquisition.Methods[0]),
			ExpectedBytes:     int64(len(body)), ExpectedDigest: string(platformcrypto.Sum(body)),
			MediaType: syntheticMediaType, Region: client.region,
		},
		wantStatus: http.StatusCreated, result: &issued,
	})
	if err != nil {
		return err
	}
	if issued.ID == "" || issued.MaximumBytes < int64(len(body)) {
		return cli.RuntimeError("synthetic journey", errors.New("step evidence.issue: API omitted the upload identifier or rejected the artefact size"))
	}
	if err := client.step("evidence.issue", map[string]string{
		"upload_id": issued.ID, "evidence_id": issued.EvidenceID,
		"maximum_bytes": strconv.FormatInt(issued.MaximumBytes, 10),
	}); err != nil {
		return err
	}
	if err := client.putEvidence(ctx, captureToken, issuedHeaders.Get("ETag"), issued, body); err != nil {
		return err
	}
	return client.step("evidence.upload", map[string]string{
		"upload_id": issued.ID, "evidence_id": issued.EvidenceID, "state": issued.State,
	})
}

func (client *syntheticClient) putEvidence(
	ctx context.Context,
	captureToken, ifMatch string,
	issued syntheticEvidenceUpload,
	body []byte,
) error {
	target := *client.base
	target.Path = strings.TrimRight(client.base.Path, "/") + "/v1/evidence-uploads/" + issued.ID
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, target.String(), bytes.NewReader(body))
	if err != nil {
		return cli.RuntimeError("synthetic journey", errors.New("step evidence.upload: construct upload request"))
	}
	request.Header.Set("Authorization", "Bearer "+captureToken)
	request.Header.Set("Content-Type", syntheticMediaType)
	request.Header.Set("Content-Digest", syntheticContentDigest(body))
	if ifMatch != "" {
		request.Header.Set("If-Match", ifMatch)
	}
	response, err := client.http.Do(request)
	if err != nil {
		return cli.RuntimeError("synthetic journey", errors.New("step evidence.upload: API request failed"))
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, syntheticMaximumResponse+1))
	if err != nil || len(raw) > syntheticMaximumResponse {
		return cli.RuntimeError("synthetic journey", errors.New("step evidence.upload: read bounded API response"))
	}
	if response.StatusCode != http.StatusOK {
		var problem syntheticProblem
		_ = json.Unmarshal(raw, &problem)
		return syntheticFailure("evidence.upload", "evidence upload through the capture token", response.StatusCode, problem)
	}
	var accepted syntheticEvidenceUpload
	if err := json.Unmarshal(raw, &accepted); err != nil {
		return cli.RuntimeError("synthetic journey", errors.New("step evidence.upload: decode API response"))
	}
	if accepted.State != "accepted" {
		return cli.RuntimeError("synthetic journey", errors.New("step evidence.upload: API did not accept the uploaded artefact"))
	}
	issued.State = accepted.State
	return nil
}

func (client *syntheticClient) readProgress(
	ctx context.Context,
	captureToken, verificationID string,
	total int,
) error {
	var progress syntheticCaptureProgress
	_, err := client.call(ctx, syntheticRequest{
		step: "capture.progress", method: http.MethodGet, path: "/v1/capture/progress",
		bearer: captureToken, wantStatus: http.StatusOK, result: &progress,
	})
	if err != nil {
		return err
	}
	if progress.VerificationID != verificationID || len(progress.Completions) != total {
		return cli.RuntimeError("synthetic journey", fmt.Errorf(
			"step capture.progress: completed %d of %d required capture steps", len(progress.Completions), total,
		))
	}
	return client.step("capture.progress", map[string]string{
		"verification_id": verificationID, "completed": strconv.Itoa(len(progress.Completions)),
		"total": strconv.Itoa(total),
	})
}

func (client *syntheticClient) awaitDecision(
	ctx context.Context,
	options *syntheticOptions,
	verificationID string,
) (syntheticDecisionReference, error) {
	pollContext, cancel := context.WithTimeout(ctx, options.timeout)
	defer cancel()
	ticker := time.NewTicker(options.pollInterval)
	defer ticker.Stop()
	lastState := "unknown"
	for {
		var session syntheticSession
		_, err := client.call(pollContext, syntheticRequest{
			step: "verification.await", method: http.MethodGet,
			path:       "/v1/verifications/" + verificationID,
			permission: "verification_sessions:read", wantStatus: http.StatusOK, result: &session,
		})
		if err != nil {
			if errors.Is(pollContext.Err(), context.DeadlineExceeded) {
				return syntheticDecisionReference{}, syntheticTimeout(verificationID, options.timeout, lastState)
			}
			return syntheticDecisionReference{}, err
		}
		lastState = session.State
		switch session.State {
		case "completed":
			if session.CurrentDecision == nil {
				return syntheticDecisionReference{}, cli.RuntimeError("synthetic journey", errors.New(
					"step verification.await: verification completed but no decision was projected; the API credential requires decisions:read",
				))
			}
			if err := client.step("verification.await", map[string]string{
				"verification_id": verificationID, "state": session.State,
				"version": strconv.FormatInt(session.Version, 10),
			}); err != nil {
				return syntheticDecisionReference{}, err
			}
			return *session.CurrentDecision, nil
		case "failed":
			class, code := "unknown", "unknown"
			if session.Failure != nil {
				class, code = session.Failure.Class, session.Failure.Code
			}
			return syntheticDecisionReference{}, cli.RuntimeError("synthetic journey", fmt.Errorf(
				"step verification.await: verification failed class=%s code=%s", class, code,
			))
		case "cancelled", "expired":
			return syntheticDecisionReference{}, cli.RuntimeError("synthetic journey", fmt.Errorf(
				"step verification.await: verification reached terminal state %q", session.State,
			))
		}
		select {
		case <-pollContext.Done():
			return syntheticDecisionReference{}, syntheticTimeout(verificationID, options.timeout, lastState)
		case <-ticker.C:
		}
	}
}

func (client *syntheticClient) readDecision(ctx context.Context, decision syntheticDecisionReference) error {
	var report syntheticDecisionReport
	_, err := client.call(ctx, syntheticRequest{
		step: "decision.read", method: http.MethodGet, path: "/v1/decisions/" + decision.DecisionID,
		permission: "decisions:read", wantStatus: http.StatusOK, result: &report,
	})
	if err != nil {
		return err
	}
	fields := map[string]string{
		"decision_id": report.DecisionID, "outcome": report.Outcome,
		"directive": report.Directive, "verification_id": report.VerificationID,
	}
	if report.PolicyID != "" {
		fields["policy_id"] = report.PolicyID
	}
	return client.step("decision.read", fields)
}

func (client *syntheticClient) call(ctx context.Context, request syntheticRequest) (http.Header, error) {
	var body io.Reader
	if request.body != nil {
		encoded, err := json.Marshal(request.body)
		if err != nil {
			return nil, cli.RuntimeError("synthetic journey", fmt.Errorf("step %s: encode request", request.step))
		}
		body = bytes.NewReader(encoded)
	}
	target := *client.base
	target.Path = strings.TrimRight(client.base.Path, "/") + request.path
	httpRequest, err := http.NewRequestWithContext(ctx, request.method, target.String(), body)
	if err != nil {
		return nil, cli.RuntimeError("synthetic journey", fmt.Errorf("step %s: construct request", request.step))
	}
	credential := client.credential
	if request.bearer != "" {
		credential = request.bearer
	}
	httpRequest.Header.Set("Authorization", "Bearer "+credential)
	if request.body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	if request.idempotencyKey != "" {
		httpRequest.Header.Set("Idempotency-Key", strconv.Quote(request.idempotencyKey))
	}
	if request.ifMatch != "" {
		httpRequest.Header.Set("If-Match", request.ifMatch)
	}
	response, err := client.http.Do(httpRequest)
	if err != nil {
		return nil, cli.RuntimeError("synthetic journey", fmt.Errorf("step %s: API request failed", request.step))
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, syntheticMaximumResponse+1))
	if err != nil || len(raw) > syntheticMaximumResponse {
		return nil, cli.RuntimeError("synthetic journey", fmt.Errorf("step %s: read bounded API response", request.step))
	}
	if response.StatusCode != request.wantStatus {
		var problem syntheticProblem
		_ = json.Unmarshal(raw, &problem)
		return nil, syntheticFailure(request.step, request.permission, response.StatusCode, problem)
	}
	if request.result != nil {
		if err := json.Unmarshal(raw, request.result); err != nil {
			return nil, cli.RuntimeError("synthetic journey", fmt.Errorf("step %s: decode API response", request.step))
		}
	}
	return response.Header.Clone(), nil
}

func (client *syntheticClient) step(name string, fields map[string]string) error {
	if client.jsonOutput {
		encoded, err := json.Marshal(syntheticStepOutput{Step: name, Status: "ok", Fields: fields})
		if err != nil {
			return cli.RuntimeError("synthetic journey", errors.New("encode step output"))
		}
		if _, err := fmt.Fprintln(client.out, string(encoded)); err != nil {
			return cli.RuntimeError("synthetic journey", errors.New("write step output"))
		}
		return nil
	}
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString("step=" + name + " status=ok")
	for _, key := range keys {
		builder.WriteString(" " + key + "=" + fields[key])
	}
	if _, err := fmt.Fprintln(client.out, builder.String()); err != nil {
		return cli.RuntimeError("synthetic journey", errors.New("write step output"))
	}
	return nil
}

func (client *syntheticClient) key(suffix string) string {
	return client.prefix + "-" + suffix
}

type syntheticRequest struct {
	step           string
	method         string
	path           string
	bearer         string
	permission     string
	idempotencyKey string
	ifMatch        string
	body           any
	wantStatus     int
	result         any
}

type syntheticStepOutput struct {
	Step   string            `json:"step"`
	Status string            `json:"status"`
	Fields map[string]string `json:"fields,omitempty"`
}

type syntheticProblem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail"`
	Code   string `json:"code"`
}

type syntheticProfileWrite struct {
	Name     string          `json:"name"`
	Document json.RawMessage `json:"document"`
}

type syntheticProfileMutation struct {
	ProfileID         string `json:"profile_id"`
	Version           int64  `json:"version"`
	Revision          int    `json:"revision"`
	PublishedRevision *int   `json:"published_revision,omitempty"`
	State             string `json:"state"`
}

type syntheticPolicyWrite struct {
	Definition json.RawMessage `json:"definition"`
}

type syntheticPolicyActivation struct {
	Revision        int64  `json:"revision"`
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}

type syntheticPolicySummary struct {
	ID                string `json:"id"`
	LatestRevision    int64  `json:"latest_revision"`
	ActiveRevision    *int64 `json:"active_revision,omitempty"`
	ActivationVersion int64  `json:"activation_version"`
}

type syntheticPolicyRevision struct {
	Revision int64 `json:"revision"`
}

type syntheticPolicyActivationInfo struct {
	Revision int64 `json:"revision"`
	Version  int64 `json:"version"`
}

type syntheticPolicyMutation struct {
	Policy     syntheticPolicySummary         `json:"policy"`
	Revision   *syntheticPolicyRevision       `json:"revision,omitempty"`
	Activation *syntheticPolicyActivationInfo `json:"activation,omitempty"`
	Replayed   bool                           `json:"replayed"`
}

type syntheticPolicyDefinition struct {
	SchemaMajor       uint16          `json:"schema_major"`
	SchemaMinor       uint16          `json:"schema_minor"`
	VerifiedAssurance string          `json:"verified_assurance"`
	Rules             []policyv1.Rule `json:"rules"`
}

type syntheticVerificationWrite struct {
	CaptureProfileID string `json:"capture_profile_id"`
	PolicyID         string `json:"policy_id"`
}

type syntheticDecisionReference struct {
	DecisionID string `json:"decision_id"`
	Outcome    string `json:"outcome"`
	Directive  string `json:"directive"`
}

type syntheticFailureDetail struct {
	Class string `json:"class"`
	Code  string `json:"code"`
}

type syntheticSession struct {
	ID              string                      `json:"id"`
	State           string                      `json:"state"`
	Version         int64                       `json:"version"`
	ExpiresAt       time.Time                   `json:"expires_at"`
	CurrentDecision *syntheticDecisionReference `json:"current_decision,omitempty"`
	Failure         *syntheticFailureDetail     `json:"failure,omitempty"`
}

type syntheticVerificationCreated struct {
	Session      syntheticSession `json:"session"`
	CaptureToken *string          `json:"capture_token,omitempty"`
}

type syntheticNoticeCopy struct {
	Title        string `json:"title"`
	Summary      string `json:"summary"`
	Purpose      string `json:"purpose"`
	Consequences string `json:"consequences"`
}

type syntheticNoticeWrite struct {
	Key         string              `json:"key"`
	Locale      string              `json:"locale"`
	Controller  string              `json:"controller"`
	Recipient   string              `json:"recipient"`
	Copy        syntheticNoticeCopy `json:"copy"`
	EffectiveAt time.Time           `json:"effective_at"`
}

type syntheticNotice struct {
	ID        string `json:"id"`
	Recipient string `json:"recipient"`
}

type syntheticAuthorityWrite struct {
	NoticeID             string    `json:"notice_id"`
	Category             string    `json:"category"`
	Purpose              string    `json:"purpose"`
	Jurisdiction         string    `json:"jurisdiction"`
	PolicyPack           string    `json:"policy_pack"`
	ConsentRequired      bool      `json:"consent_required"`
	RecipientReference   string    `json:"recipient_reference"`
	RecipientDisplayName string    `json:"recipient_display_name"`
	Regions              []string  `json:"regions"`
	RetentionReference   string    `json:"retention_reference"`
	ValidFrom            time.Time `json:"valid_from"`
	ExpiresAt            time.Time `json:"expires_at"`
}

type syntheticAuthority struct {
	ID       string `json:"id"`
	NoticeID string `json:"notice_id"`
}

type syntheticAuthoritySnapshot struct {
	Authority syntheticAuthority `json:"authority"`
	Notice    syntheticNotice    `json:"notice"`
}

type syntheticSubjectResponseWrite struct {
	Action string `json:"action"`
	Locale string `json:"locale"`
}

type syntheticSubjectResponse struct {
	ID     string `json:"id"`
	Action string `json:"action"`
}

type syntheticEvidenceUploadWrite struct {
	RequirementKey    string `json:"requirement_key"`
	Artefact          string `json:"artefact"`
	AcquisitionMethod string `json:"acquisition_method"`
	ExpectedBytes     int64  `json:"expected_bytes"`
	ExpectedDigest    string `json:"expected_digest"`
	MediaType         string `json:"media_type"`
	Region            string `json:"region"`
}

type syntheticEvidenceUpload struct {
	ID                string     `json:"id"`
	EvidenceID        string     `json:"evidence_id"`
	State             string     `json:"state"`
	AcquisitionMethod string     `json:"acquisition_method"`
	MaximumBytes      int64      `json:"maximum_bytes"`
	Version           int64      `json:"version"`
	AcceptedAt        *time.Time `json:"accepted_at,omitempty"`
}

type syntheticCaptureCompletion struct {
	RequirementKey string `json:"requirement_key"`
}

type syntheticCaptureProgress struct {
	VerificationID string                       `json:"verification_id"`
	Completions    []syntheticCaptureCompletion `json:"completions"`
}

type syntheticDecisionReport struct {
	DecisionID     string `json:"decision_id"`
	VerificationID string `json:"verification_id"`
	PolicyID       string `json:"policy_id"`
	Outcome        string `json:"outcome"`
	Directive      string `json:"directive"`
}

func readSyntheticDocument(path, label string) ([]byte, error) {
	if path == "" {
		return nil, cli.UsageError(errors.New(label + " file path is required"))
	}
	file, err := os.Open(path) //nolint:gosec // operator-supplied fixture path, not request input
	if err != nil {
		return nil, cli.RuntimeError("synthetic journey", fmt.Errorf("open %s file %q", label, path))
	}
	material, readErr := io.ReadAll(io.LimitReader(file, syntheticMaximumFileBytes+1))
	closeErr := file.Close()
	if readErr != nil || closeErr != nil || len(material) > syntheticMaximumFileBytes {
		return nil, cli.RuntimeError("synthetic journey", fmt.Errorf("read bounded %s file", label))
	}
	if !json.Valid(material) {
		return nil, cli.UsageError(fmt.Errorf("%s file must contain one valid JSON document", label))
	}
	return material, nil
}

func validateSyntheticPolicy(document []byte) error {
	var definition syntheticPolicyDefinition
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&definition); err != nil {
		return errors.New("policy definition must contain only schema_major, schema_minor, verified_assurance and rules")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("policy definition must contain one JSON document")
	}
	canonical, err := policyv1.Canonical(policyv1.Document{
		SchemaMajor: definition.SchemaMajor, SchemaMinor: definition.SchemaMinor,
		PolicyID: "pol_00000000000000000000000000", Revision: 1,
		VerifiedAssurance: definition.VerifiedAssurance, Rules: definition.Rules,
	})
	if err != nil {
		return err
	}
	_, err = policyv1.ParseCanonical(canonical)
	return err
}

func readPublicAPICredential(keyFile string) (string, error) {
	key := os.Getenv("IDENQA_API_KEY")
	if keyFile != "" {
		file, err := os.Open(keyFile) //nolint:gosec // operator-supplied credential path, not request input
		if err != nil {
			return "", cli.RuntimeError("read API credential", errors.New("read API credential file"))
		}
		material, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(material) > 4096 {
			clear(material)
			return "", cli.RuntimeError("read API credential", errors.New("read bounded API credential file"))
		}
		key = strings.TrimSpace(string(material))
		clear(material)
	}
	if key == "" {
		return "", nil
	}
	if strings.ContainsAny(key, "\r\n") {
		return "", cli.UsageError(errors.New("API credential must be a single-line value"))
	}
	return key, nil
}

func parsePublicAPIURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Scheme != "https" && (parsed.Scheme != "http" ||
			(parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "localhost" && parsed.Hostname() != "::1"))) {
		return nil, errors.New("a valid HTTPS API URL or local loopback HTTP URL is required")
	}
	return parsed, nil
}

func syntheticFailure(step, permission string, status int, problem syntheticProblem) error {
	hint := ""
	switch status {
	case http.StatusUnauthorized:
		hint = "; issue a new tenant API key"
	case http.StatusForbidden:
		if permission != "" {
			hint = "; the API credential requires " + permission
		} else {
			hint = "; the API credential is not permitted for this operation"
		}
	case http.StatusNotFound:
		hint = "; the referenced resource does not exist in this tenant"
	case http.StatusConflict:
		hint = "; retry with a different --idempotency-prefix to avoid the conflicting resource"
	case http.StatusTooManyRequests:
		hint = "; the API is rate limiting requests, retry later"
	}
	detail := strings.Join(strings.Fields(problem.Detail), " ")
	if len(detail) > syntheticMaximumDetailBytes {
		detail = detail[:syntheticMaximumDetailBytes]
	}
	message := fmt.Sprintf("step %s: API returned status %d", step, status)
	if problem.Code != "" {
		message += fmt.Sprintf(" (problem %q)", problem.Code)
	}
	if detail != "" {
		message += ": " + detail
	}
	return cli.RuntimeError("synthetic journey", errors.New(message+hint))
}

func syntheticTimeout(verificationID string, timeout time.Duration, state string) error {
	return cli.RuntimeError("synthetic journey", fmt.Errorf(
		"step verification.await: verification %s did not reach a completed decision within %s (last state %q); ensure a worker is running with IDENQA_WORKER_SYNTHETIC_PROCESSING=true",
		verificationID, timeout, state,
	))
}

func syntheticRunID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func validSyntheticPrefix(value string) bool {
	if value == "" || len(value) > 100 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && !strings.ContainsRune("._:-", character) {
			return false
		}
	}
	return true
}

func validSyntheticRegion(value string) bool {
	if value == "" || len(value) > 200 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') &&
			!strings.ContainsRune("._-", character) {
			return false
		}
	}
	return true
}

func syntheticArtefact() []byte {
	return append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte("idenqa synthetic selfie"), 8)...)
}

func syntheticContentDigest(body []byte) string {
	sum := sha256.Sum256(body)
	return "sha-256=:" + base64.StdEncoding.EncodeToString(sum[:]) + ":"
}
