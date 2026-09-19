//go:build integration

package integration_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	reviewpostgres "github.com/Mujhtech/idenqa/internal/review/postgres"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
	"github.com/go-chi/chi/v5"
)

const (
	reviewHTTPRegion              = "tenant-region-ng"
	reviewHTTPRequiredCertificate = "document.level2"
)

type followupWire struct {
	CaseID     string `json:"case_id"`
	Version    int64  `json:"version"`
	DecisionID string `json:"decision_id"`
	Directive  string `json:"directive"`
}

type reviewCaseWire struct {
	ID                    string `json:"id"`
	State                 string `json:"state"`
	AssignedReviewer      string `json:"assigned_reviewer"`
	SupersedingDecisionID string `json:"superseding_decision_id"`
	Version               int64  `json:"version"`
}

type appealWire struct {
	ID                    string `json:"id"`
	CaseID                string `json:"case_id"`
	State                 string `json:"state"`
	Outcome               string `json:"outcome"`
	AssignedReviewer      string `json:"assigned_reviewer"`
	SupersedingDecisionID string `json:"superseding_decision_id"`
	Version               int64  `json:"version"`
}

// reviewHTTPJourney is a real public-route harness over the fixture database.
// Every reviewer action carries a real access credential; reviewer identity,
// certification and external assertions come only from server configuration.
type reviewHTTPJourney struct {
	server               *httptest.Server
	client               *http.Client
	authority            *reviewpostgres.Authority
	intake               string
	reviewer             string
	resolver             string
	appealer             string
	admin                string
	findOnly             string
	uncertified          string
	unasserted           string
	reviewerAssignment   review.Assignment
	unassertedAssignment review.Assignment
}

func startReviewHTTPJourney(t *testing.T, f captureAcceptanceFixture, evaluator policy.Evaluator) *reviewHTTPJourney {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x6d}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	accessStore, err := accesspostgres.New(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := access.NewAuthenticator(accessStore, peppers, fixedIntegrationClock{now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := httpapi.NewAccessMiddleware(authenticator, logger)
	if err != nil {
		t.Fatal(err)
	}
	newKey := func(label string, patterns ...string) (access.Key, string) {
		t.Helper()
		identifier, err := f.ids.NewAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		generator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0x51}, 32)))
		if err != nil {
			t.Fatal(err)
		}
		presented, err := generator.Generate(f.scope.ID(), identifier)
		if err != nil {
			t.Fatal(err)
		}
		digest, version, err := peppers.Digest(presented)
		if err != nil {
			t.Fatal(err)
		}
		resolved := make([]access.Pattern, 0, len(patterns))
		for _, pattern := range patterns {
			resolved = append(resolved, access.Pattern(pattern))
		}
		grant, err := access.TenantRegistry().Resolve(resolved...)
		if err != nil {
			t.Fatal(err)
		}
		key, err := access.RestoreKey(access.KeyRecord{
			ID: identifier, TenantID: f.scope.ID(), Label: label, Digest: digest,
			PepperVersion: version, Grant: grant, Version: 1, CreatedAt: f.now, UpdatedAt: f.now,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := accessStore.Create(t.Context(), f.scope, key); err != nil {
			t.Fatal(err)
		}
		return key, presented.Reveal()
	}
	_, intake := newKey("review http intake", string(access.PermissionAppealsWrite))
	reviewerKey, reviewer := newKey("review http reviewer", string(access.PermissionReviewsWrite))
	resolverKey, resolver := newKey("review http resolver", string(access.PermissionReviewsWrite))
	appealerKey, appealer := newKey("review http appeal", string(access.PermissionAppealsWrite))
	_, admin := newKey("review http admin", string(access.PermissionReviewsAdmin))
	findOnlyKey, findOnly := newKey("review http find only", string(access.PermissionReviewsWrite))
	uncertifiedKey, uncertified := newKey("review http uncertified", string(access.PermissionReviewsWrite))
	unassertedKey, unasserted := newKey("review http unasserted", string(access.PermissionReviewsWrite))

	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const (
		issuer = "issuer.registry"
		keyID  = "2026-09"
	)
	// The deployment verifier observes wall-clock time; assertions are real-time
	// valid while assignment validity follows the fixture clock.
	signedAt := time.Now().UTC().Truncate(time.Microsecond)
	sign := func(operator, certificate string) string {
		t.Helper()
		encoded, err := json.Marshal(map[string]any{
			"issuer": issuer, "key_id": keyID, "reviewer_id": operator,
			"certificate": certificate, "region": reviewHTTPRegion,
			"issued_at": signedAt.Add(-time.Hour), "expires_at": signedAt.Add(time.Hour),
		})
		if err != nil {
			t.Fatal(err)
		}
		signature := ed25519.Sign(private, encoded)
		return "v1." + base64.RawURLEncoding.EncodeToString(encoded) + "." + base64.RawURLEncoding.EncodeToString(signature)
	}
	assignment := func(key access.Key, operator string, permissions []review.Permission, certificate string) review.Assignment {
		value := review.Assignment{
			TenantID: f.scope.ID().String(), APIKeyID: key.ID().String(), OperatorID: operator,
			Permissions: permissions, Regions: []string{reviewHTTPRegion},
			NotBefore: f.now.Add(-time.Hour), ExpiresAt: f.now.Add(time.Hour),
		}
		if certificate != "" {
			value.Certifications = []string{certificate}
			value.CertificationAssertions = []review.CertificateAssertion{{Certificate: certificate, Region: reviewHTTPRegion, Token: sign(operator, certificate)}}
		}
		return value
	}
	reviewerAssignment := assignment(reviewerKey, "reviewer.correction", []review.Permission{review.PermissionClaim, review.PermissionFind}, reviewHTTPRequiredCertificate)
	resolverAssignment := assignment(resolverKey, "resolver.independent", []review.Permission{review.PermissionResolve}, reviewHTTPRequiredCertificate)
	appealerAssignment := assignment(appealerKey, "appeal.independent", []review.Permission{review.PermissionAppeal}, reviewHTTPRequiredCertificate)
	findOnlyAssignment := assignment(findOnlyKey, "reviewer.findonly", []review.Permission{review.PermissionClaim, review.PermissionFind}, reviewHTTPRequiredCertificate)
	uncertifiedAssignment := assignment(uncertifiedKey, "reviewer.uncertified", []review.Permission{review.PermissionClaim, review.PermissionFind, review.PermissionResolve}, "")
	unassertedAssignment := assignment(unassertedKey, "reviewer.unasserted", []review.Permission{review.PermissionClaim, review.PermissionFind}, reviewHTTPRequiredCertificate)
	unassertedAssignment.CertificationAssertions = nil

	document, err := json.Marshal(map[string]any{
		"assignments": []review.Assignment{resolverAssignment, appealerAssignment, findOnlyAssignment, uncertifiedAssignment},
		"certification_issuers": map[string]map[string]string{
			issuer: {keyID: base64.StdEncoding.EncodeToString(public)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "review-authority.json")
	if err := os.WriteFile(path, document, 0600); err != nil {
		t.Fatal(err)
	}
	authorityFile := config.ReviewAuthorityFile{Path: path}
	if err := authorityFile.Validate(); err != nil {
		t.Fatal(err)
	}
	authority, err := reviewpostgres.NewAuthority(f.runtime, authorityFile)
	if err != nil {
		t.Fatal(err)
	}
	reviewStore, err := reviewpostgres.NewWithClock(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	reviewStore = reviewStore.WithAuthority(authority)
	reviewService, err := review.NewAuthorizedService(reviewStore, f.ids, func() time.Time { return f.now }, authority)
	if err != nil {
		t.Fatal(err)
	}
	reviewRoutes, err := httpapi.NewReviewRoutes(middleware, reviewService, logger)
	if err != nil {
		t.Fatal(err)
	}
	followupStore, err := reviewpostgres.NewFollowupStore(f.runtime, integrationProtector{}, authority, evaluator, f.ids, fixedIntegrationClock{now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	followupService, err := review.NewFollowupService(followupStore, func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	followupRoutes, err := httpapi.NewReviewFollowupRoutes(middleware, followupService, logger)
	if err != nil {
		t.Fatal(err)
	}
	management, err := review.NewManagement(reviewStore, func() time.Time { return f.now })
	if err != nil {
		t.Fatal(err)
	}
	managementRoutes, err := httpapi.NewReviewManagementRoutes(middleware, management, logger)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Route("/v1", func(router chi.Router) {
		reviewRoutes.Register(router)
		followupRoutes.Register(router)
		managementRoutes.Register(router)
	})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	return &reviewHTTPJourney{
		server: server, client: server.Client(), authority: authority,
		intake: intake, reviewer: reviewer, resolver: resolver, appealer: appealer, admin: admin,
		findOnly: findOnly, uncertified: uncertified, unasserted: unasserted,
		reviewerAssignment: reviewerAssignment, unassertedAssignment: unassertedAssignment,
	}
}

func administerReviewPolicyHTTP(t *testing.T, journey *reviewHTTPJourney, settings review.PolicySettings) {
	t.Helper()
	performPublicJSONRequest(t, journey.client, publicJSONRequest{
		Method: http.MethodPut,
		URL:    journey.server.URL + "/v1/review-policies/" + settings.PolicyID + "/revisions/" + strconv.FormatUint(uint64(settings.Revision), 10),
		Bearer: journey.admin, IdempotencyKey: "review-policy-settings-http",
		Body: map[string]any{"expected_version": 0, "configuration": settings}, WantStatus: http.StatusOK,
	})
}

// administerReviewOperator writes a durable assignment through the owned store,
// exercising the same PostgreSQL authority adapter the API composes.
func administerReviewOperator(t *testing.T, f captureAcceptanceFixture, authority review.Authority, assignment review.Assignment) {
	t.Helper()
	store, err := reviewpostgres.NewWithClock(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	store = store.WithAuthority(authority)
	actor := reviewActorKey(t, f)
	body, err := json.Marshal(review.OperatorConfiguration{Assignment: assignment})
	if err != nil {
		t.Fatal(err)
	}
	input := review.Administration{
		Kind: "operator", Reference: assignment.APIKeyID, ExpectedVersion: 0, Configuration: body,
		Actor: review.Actor{ID: actor.String()}, At: f.now,
		Retry: integrationIdempotencyRequest(t, f.scope.ID(), actor, "reviews.admin.operator", "review-operator-"+assignment.OperatorID, body, f.now),
	}
	if _, err := store.Administer(t.Context(), f.scope, input); err != nil {
		t.Fatalf("administer review operator: %v", err)
	}
}

func requestCorrectionHTTP(t *testing.T, journey *reviewHTTPJourney, bearer, caseID, key string, expected int64, want int) review.FollowupResult {
	t.Helper()
	body := map[string]any{"expected_version": expected}
	var result followupWire
	performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + caseID + "/corrections/evaluate",
		Bearer: bearer, IdempotencyKey: key, Body: body, WantStatus: want, Result: &result})
	return review.FollowupResult{CaseID: result.CaseID, Version: result.Version, DecisionID: result.DecisionID, Directive: policy.Directive(result.Directive)}
}

func claimCaseHTTP(t *testing.T, journey *reviewHTTPJourney, bearer, caseID string, want int) reviewCaseWire {
	t.Helper()
	var result reviewCaseWire
	performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + caseID + "/claim",
		Bearer: bearer, Body: map[string]any{"expected_version": 1}, WantStatus: want, Result: &result})
	return result
}

// TestReviewCorrectionHTTPJourney composes the correction branch through real
// public routes: intake, claim, certified finding, policy-authored successor,
// immutable original decision, catalogue event and idempotent replay.
func TestReviewCorrectionHTTPJourney(t *testing.T) {
	runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
		base := prepareCompletion(t, f)
		settings := reviewPolicySettings(f, base, review.OversightSingle)
		seedReviewPolicyRevision(t, f, base.Snapshot().Policy(), base.Snapshot().Evaluator())
		evaluator := &factDirectiveEvaluator{reference: base.Snapshot().Evaluator(), fact: mustFactKey(t, "review.correction"), name: "correction", terminal: true}
		journey := startReviewHTTPJourney(t, f, evaluator)
		administerReviewPolicyHTTP(t, journey, settings)
		administerReviewOperator(t, f, journey.authority, journey.reviewerAssignment)
		administerReviewOperator(t, f, journey.authority, journey.unassertedAssignment)
		completeIntegrationVerification(t, f, base)
		originalCanonical, originalDigest := selectReviewDecisionBytes(t, f, base.ID())

		var intake followupWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/decisions/" + base.ID().String() + "/review-cases",
			Bearer: journey.intake, IdempotencyKey: "correction-intake-http", WantStatus: http.StatusCreated, Result: &intake})
		if intake.CaseID == "" || intake.Version != 1 {
			t.Fatalf("correction intake = %+v", intake)
		}
		var intakeReplay followupWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/decisions/" + base.ID().String() + "/review-cases",
			Bearer: journey.intake, IdempotencyKey: "correction-intake-http", WantStatus: http.StatusCreated, Result: &intakeReplay})
		if intakeReplay.CaseID != intake.CaseID || intakeReplay.Version != intake.Version {
			t.Fatalf("correction intake replay = %+v", intakeReplay)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_correction_intakes WHERE tenant_id=$1`, f.scope.ID().String()); count != 1 {
			t.Fatalf("correction intakes=%d", count)
		}

		// Transport scope is mandatory regardless of server authority.
		claimCaseHTTP(t, journey, journey.intake, intake.CaseID, http.StatusForbidden)
		// Caller bodies cannot assert reviewer identity, certifications or
		// external assertions; those come only from server-managed configuration.
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + intake.CaseID + "/claim",
			Bearer: journey.reviewer, Body: map[string]any{"expected_version": 1, "reviewer_id": "spoof"}, WantStatus: http.StatusBadRequest})
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + intake.CaseID + "/claim",
			Bearer: journey.reviewer, Body: map[string]any{"expected_version": 1, "certifications": []string{reviewHTTPRequiredCertificate}}, WantStatus: http.StatusBadRequest})
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + intake.CaseID + "/claim",
			Bearer: journey.reviewer, Body: map[string]any{"expected_version": 1, "certification_assertions": []map[string]any{{"token": "v1.spoof"}}}, WantStatus: http.StatusBadRequest})
		// A configured external issuer denies a certification without an assertion.
		claimCaseHTTP(t, journey, journey.unasserted, intake.CaseID, http.StatusForbidden)
		// Application authority still requires the exact case certification.
		claimCaseHTTP(t, journey, journey.uncertified, intake.CaseID, http.StatusForbidden)
		// Durable assignment resolution verifies the configured assertion.
		claimed := claimCaseHTTP(t, journey, journey.reviewer, intake.CaseID, http.StatusOK)
		if claimed.ID != intake.CaseID || claimed.State != "claimed" || claimed.AssignedReviewer != "reviewer.correction" || claimed.Version != 2 {
			t.Fatalf("claimed case = %+v", claimed)
		}
		// A reviewer without reviews:find cannot record a finding.
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + intake.CaseID + "/findings",
			Bearer: journey.resolver, Body: map[string]any{"resolution": "satisfy", "reason_code": "document_reviewed", "grant_ids": []string{"grt_01K4AR9V8FQ2G7ZXCPNM5T6JWH"}, "expected_version": 2},
			WantStatus: http.StatusForbidden})
		caseID, err := id.ParseReviewCase(intake.CaseID)
		if err != nil {
			t.Fatal(err)
		}
		grant := seedReviewEvidenceGrant(t, f, caseID, claimed.Version, "reviewer.correction")
		var resolved reviewCaseWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + intake.CaseID + "/findings",
			Bearer: journey.reviewer, Body: map[string]any{"resolution": "satisfy", "reason_code": "document_reviewed", "grant_ids": []string{grant.String()}, "expected_version": claimed.Version},
			WantStatus: http.StatusOK, Result: &resolved})
		if resolved.State != "resolved" || resolved.Version != 3 {
			t.Fatalf("resolved case = %+v", resolved)
		}

		// Correction authorship needs reviews:resolve, not merely reviews:find.
		requestCorrectionHTTP(t, journey, journey.findOnly, intake.CaseID, "correction-denied-http", resolved.Version, http.StatusForbidden)
		// A finding reviewer cannot author the successor.
		requestCorrectionHTTP(t, journey, journey.reviewer, intake.CaseID, "correction-self-http", resolved.Version, http.StatusForbidden)
		correction := requestCorrectionHTTP(t, journey, journey.resolver, intake.CaseID, "correction-evaluate-http", resolved.Version, http.StatusOK)
		if correction.DecisionID == "" || correction.Version != resolved.Version+1 || correction.Directive != policy.DirectiveCompleteVerified {
			t.Fatalf("correction result = %+v", correction)
		}
		successorID, err := id.ParseDecision(correction.DecisionID)
		if err != nil {
			t.Fatal(err)
		}
		policies, err := policypostgres.New(f.runtime, integrationProtector{})
		if err != nil {
			t.Fatal(err)
		}
		successor, err := policies.Find(t.Context(), f.scope, successorID)
		if err != nil {
			t.Fatal(err)
		}
		if successor.Supersedes() != base.ID() || successor.Snapshot().Policy() != base.Snapshot().Policy() {
			t.Fatal("successor lineage changed the challenged decision or pinned policy")
		}
		canonical, digest := selectReviewDecisionBytes(t, f, base.ID())
		if canonical != originalCanonical || digest != originalDigest {
			t.Fatal("original decision bytes changed")
		}
		if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.verification_decisions SET canonical='{}' WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), base.ID().String()); err == nil {
			t.Fatal("original decision was mutable")
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.outbox_events WHERE tenant_id=$1 AND event_type='verification.decision.corrected'`, f.scope.ID().String()); count != 1 {
			t.Fatalf("correction outbox=%d", count)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.webhook_events WHERE tenant_id=$1 AND event_type=$2`, f.scope.ID().String(), string(webhookv1.DecisionCorrected)); count != 1 {
			t.Fatalf("correction catalogue=%d", count)
		}
		replay := requestCorrectionHTTP(t, journey, journey.resolver, intake.CaseID, "correction-evaluate-http", resolved.Version, http.StatusOK)
		if replay != correction {
			t.Fatalf("correction replay = %+v", replay)
		}
		requestCorrectionHTTP(t, journey, journey.resolver, intake.CaseID, "correction-duplicate-http", resolved.Version, http.StatusConflict)
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_correction_evaluations WHERE tenant_id=$1`, f.scope.ID().String()); count != 1 {
			t.Fatalf("correction evaluations=%d", count)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.verification_decisions WHERE tenant_id=$1 AND verification_id=$2`, f.scope.ID().String(), base.Snapshot().VerificationID().String()); count != 2 {
			t.Fatalf("verification decisions=%d", count)
		}
	}, reviewHTTPRegion, reviewHTTPRegion)
}

// TestReviewAppealHTTPJourney composes appeal intake, withdrawal, independent
// assignment and receipted overturn through real public routes.
func TestReviewAppealHTTPJourney(t *testing.T) {
	runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
		base := prepareCompletion(t, f)
		settings := reviewPolicySettings(f, base, review.OversightSingle)
		seedReviewPolicyRevision(t, f, base.Snapshot().Policy(), base.Snapshot().Evaluator())
		evaluator := &factDirectiveEvaluator{reference: base.Snapshot().Evaluator(), fact: mustFactKey(t, "review.correction"), name: "correction", terminal: true}
		journey := startReviewHTTPJourney(t, f, evaluator)
		administerReviewPolicyHTTP(t, journey, settings)
		administerReviewOperator(t, f, journey.authority, journey.reviewerAssignment)
		administerReviewOperator(t, f, journey.authority, journey.unassertedAssignment)
		completeIntegrationVerification(t, f, base)

		var intake followupWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/decisions/" + base.ID().String() + "/review-cases",
			Bearer: journey.intake, IdempotencyKey: "appeal-correction-intake-http", WantStatus: http.StatusCreated, Result: &intake})
		claimed := claimCaseHTTP(t, journey, journey.reviewer, intake.CaseID, http.StatusOK)
		caseID, err := id.ParseReviewCase(intake.CaseID)
		if err != nil {
			t.Fatal(err)
		}
		grant := seedReviewEvidenceGrant(t, f, caseID, claimed.Version, "reviewer.correction")
		var resolved reviewCaseWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + intake.CaseID + "/findings",
			Bearer: journey.reviewer, Body: map[string]any{"resolution": "satisfy", "reason_code": "document_reviewed", "grant_ids": []string{grant.String()}, "expected_version": claimed.Version},
			WantStatus: http.StatusOK, Result: &resolved})
		if resolved.State != "resolved" {
			t.Fatalf("appeal source case = %+v", resolved)
		}
		deadline := f.now.Add(30 * time.Minute)

		var appeal appealWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + intake.CaseID + "/appeals",
			Bearer: journey.intake, IdempotencyKey: "appeal-intake-http", Body: map[string]any{"deadline": deadline}, WantStatus: http.StatusCreated, Result: &appeal})
		if appeal.ID == "" || appeal.State != "requested" || appeal.Version != 1 {
			t.Fatalf("appeal intake = %+v", appeal)
		}
		var appealReplay appealWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + intake.CaseID + "/appeals",
			Bearer: journey.intake, IdempotencyKey: "appeal-intake-http", Body: map[string]any{"deadline": deadline}, WantStatus: http.StatusCreated, Result: &appealReplay})
		if appealReplay.ID != appeal.ID {
			t.Fatalf("appeal intake replay = %+v", appealReplay)
		}
		// Only the requesting credential can withdraw.
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/appeals/" + appeal.ID + "/withdraw",
			Bearer: journey.appealer, Body: map[string]any{"expected_version": 1}, WantStatus: http.StatusForbidden})
		var withdrawn appealWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/appeals/" + appeal.ID + "/withdraw",
			Bearer: journey.intake, Body: map[string]any{"expected_version": 1}, WantStatus: http.StatusOK, Result: &withdrawn})
		if withdrawn.State != "withdrawn" || withdrawn.Version != 2 {
			t.Fatalf("withdrawn appeal = %+v", withdrawn)
		}
		var withdrawalReplay appealWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/appeals/" + appeal.ID + "/withdraw",
			Bearer: journey.intake, Body: map[string]any{"expected_version": 1}, WantStatus: http.StatusOK, Result: &withdrawalReplay})
		if withdrawalReplay.State != "withdrawn" || withdrawalReplay.Version != withdrawn.Version {
			t.Fatalf("withdrawal replay = %+v", withdrawalReplay)
		}

		var overturn appealWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + intake.CaseID + "/appeals",
			Bearer: journey.intake, IdempotencyKey: "appeal-overturn-http", Body: map[string]any{"deadline": deadline}, WantStatus: http.StatusCreated, Result: &overturn})
		var assigned appealWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/appeals/" + overturn.ID + "/assign",
			Bearer: journey.appealer, Body: map[string]any{"expected_version": 1}, WantStatus: http.StatusOK, Result: &assigned})
		if assigned.State != "independent_review" || assigned.AssignedReviewer != "appeal.independent" || assigned.Version != 2 {
			t.Fatalf("assigned appeal = %+v", assigned)
		}
		correction := requestCorrectionHTTP(t, journey, journey.resolver, intake.CaseID, "appeal-correction-evaluate-http", resolved.Version, http.StatusOK)
		successorID, err := id.ParseDecision(correction.DecisionID)
		if err != nil || successorID.IsZero() {
			t.Fatalf("appeal correction successor = %+v error=%v", correction, err)
		}
		resolveBody := func(successor string) map[string]any {
			return map[string]any{"outcome": "overturned", "reason_code": "new_evidence", "superseding_decision_id": successor, "expected_version": 2}
		}
		var overturned appealWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/appeals/" + overturn.ID + "/resolve",
			Bearer: journey.appealer, Body: resolveBody(successorID.String()), WantStatus: http.StatusOK, Result: &overturned})
		if overturned.State != "resolved" || overturned.Outcome != "overturned" || overturned.SupersedingDecisionID != successorID.String() || overturned.Version != 3 {
			t.Fatalf("overturned appeal = %+v", overturned)
		}
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/appeals/" + overturn.ID + "/resolve",
			Bearer: journey.appealer, Body: resolveBody(successorID.String()), WantStatus: http.StatusConflict})
		if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.review_appeal_history SET record='{}' WHERE tenant_id=$1`, f.scope.ID().String()); err == nil {
			t.Fatal("appeal history was mutable")
		}
		var requested, independentReviews, awaitingInputs, resolvedCount, withdrawnCount int
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT
 count(*) FILTER (WHERE record->>'state'='requested'),
 count(*) FILTER (WHERE record->>'state'='independent_review'),
 count(*) FILTER (WHERE record->>'state'='awaiting_input'),
 count(*) FILTER (WHERE record->>'state'='resolved'),
 count(*) FILTER (WHERE record->>'state'='withdrawn')
 FROM idenqa.review_appeal_history WHERE tenant_id=$1`, f.scope.ID().String()).Scan(&requested, &independentReviews, &awaitingInputs, &resolvedCount, &withdrawnCount); err != nil {
			t.Fatal(err)
		}
		if requested != 2 || independentReviews != 1 || awaitingInputs != 0 || resolvedCount != 1 || withdrawnCount != 1 {
			t.Fatalf("appeal history requested=%d assigned=%d awaiting=%d resolved=%d withdrawn=%d", requested, independentReviews, awaitingInputs, resolvedCount, withdrawnCount)
		}
	}, reviewHTTPRegion, reviewHTTPRegion)
}

// TestReviewAppealUnreceiptedSuccessorHTTP proves a direct successor without a
// policy-authored correction receipt cannot be attached to an appeal overturn.
func TestReviewAppealUnreceiptedSuccessorHTTP(t *testing.T) {
	runAuthorityPersistenceInRegion(t, func(f captureAcceptanceFixture) {
		base := prepareCompletion(t, f)
		settings := reviewPolicySettings(f, base, review.OversightSingle)
		seedReviewPolicyRevision(t, f, base.Snapshot().Policy(), base.Snapshot().Evaluator())
		evaluator := &factDirectiveEvaluator{reference: base.Snapshot().Evaluator(), fact: mustFactKey(t, "review.correction"), name: "correction", terminal: true}
		journey := startReviewHTTPJourney(t, f, evaluator)
		administerReviewPolicyHTTP(t, journey, settings)
		completeIntegrationVerification(t, f, base)

		var intake followupWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/decisions/" + base.ID().String() + "/review-cases",
			Bearer: journey.intake, IdempotencyKey: "unreceipted-intake-http", WantStatus: http.StatusCreated, Result: &intake})
		deadline := f.now.Add(30 * time.Minute)
		var appeal appealWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/review-cases/" + intake.CaseID + "/appeals",
			Bearer: journey.intake, IdempotencyKey: "unreceipted-appeal-http", Body: map[string]any{"deadline": deadline}, WantStatus: http.StatusCreated, Result: &appeal})
		var assigned appealWire
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/appeals/" + appeal.ID + "/assign",
			Bearer: journey.appealer, Body: map[string]any{"expected_version": 1}, WantStatus: http.StatusOK, Result: &assigned})
		if assigned.State != "independent_review" {
			t.Fatalf("assigned appeal = %+v", assigned)
		}
		directID, err := f.ids.NewDecision()
		if err != nil {
			t.Fatal(err)
		}
		direct, err := policy.NewDecision(policy.DecisionInput{ID: directID, Snapshot: base.Snapshot(), Evaluation: base.Evaluation(),
			Actor: policy.ActorMachine, Supersedes: base.ID(), DecidedAt: base.DecidedAt().Add(time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		unguarded, err := policypostgres.New(f.runtime, integrationProtector{})
		if err != nil {
			t.Fatal(err)
		}
		if err := unguarded.Append(t.Context(), f.scope, direct); err != nil {
			t.Fatal(err)
		}
		performPublicJSONRequest(t, journey.client, publicJSONRequest{Method: http.MethodPost, URL: journey.server.URL + "/v1/appeals/" + appeal.ID + "/resolve",
			Bearer: journey.appealer, Body: map[string]any{"outcome": "overturned", "reason_code": "new_evidence", "superseding_decision_id": directID.String(), "expected_version": 2},
			WantStatus: http.StatusForbidden})
		var state string
		var version int64
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT state,version FROM idenqa.appeals WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), appeal.ID).Scan(&state, &version); err != nil {
			t.Fatal(err)
		}
		if state != "independent_review" || version != 2 {
			t.Fatalf("denied overturn changed appeal state=%s version=%d", state, version)
		}
		if count := countReviewRows(t, f, `SELECT count(*) FROM idenqa.review_correction_evaluations WHERE tenant_id=$1`, f.scope.ID().String()); count != 0 {
			t.Fatalf("correction receipts=%d", count)
		}
	}, reviewHTTPRegion, reviewHTTPRegion)
}
