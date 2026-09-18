package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
)

func TestDecisionRoutesReadExactAndLatestSafeReports(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("decisions:read"))
	bundle, report, decisionID, verificationID := decisionHTTPBundle(t, fixture.tenantID)
	reader := &decisionHTTPReaderStub{report: report, bundle: bundle}
	router := decisionHTTPRouter(t, fixture, reader)

	exact := authenticatedDecisionRequest(
		t,
		router,
		fixture.encoded,
		"/v1/decisions/"+decisionID.String(),
		"",
	)
	if exact.Code != http.StatusOK {
		t.Fatalf("exact status = %d, want %d; body=%s", exact.Code, http.StatusOK, exact.Body)
	}
	var resource openapiv1.PolicyDecisionReport
	if err := json.Unmarshal(exact.Body.Bytes(), &resource); err != nil {
		t.Fatal(err)
	}
	if resource.DecisionID != decisionID.String() || !bool(resource.Reproduced) ||
		resource.PolicyRevision != int(report.PolicyRevision) {
		t.Fatalf("report = %+v, want reproduced exact decision", resource)
	}
	for _, forbidden := range []string{"facts", "reason_codes", "observation_ids", "canonical"} {
		if strings.Contains(exact.Body.String(), `"`+forbidden+`"`) {
			t.Fatalf("safe report contains forbidden field %q", forbidden)
		}
	}
	if got, want := exact.Header().Get("ETag"), decisionETag(report.DecisionDigest); got != want {
		t.Fatalf("exact ETag = %q, want %q", got, want)
	}
	if got := exact.Header().Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("exact Cache-Control = %q", got)
	}

	latest := authenticatedDecisionRequest(
		t,
		router,
		fixture.encoded,
		"/v1/verifications/"+verificationID.String()+"/decision",
		"",
	)
	if latest.Code != http.StatusOK || reader.latestVerificationID.String() != verificationID.String() {
		t.Fatalf("latest status=%d verification=%q", latest.Code, reader.latestVerificationID.String())
	}
	if reader.tenantID.String() != fixture.tenantID.String() {
		t.Fatalf("application tenant = %q, want %q", reader.tenantID.String(), fixture.tenantID.String())
	}
}

func TestDecisionRoutesExportByteExactCanonicalBundle(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("decisions:export"))
	bundle, report, decisionID, _ := decisionHTTPBundle(t, fixture.tenantID)
	reader := &decisionHTTPReaderStub{report: report, bundle: bundle}
	router := decisionHTTPRouter(t, fixture, reader)
	path := "/v1/decisions/" + decisionID.String() + "/bundle"

	response := authenticatedDecisionRequest(t, router, fixture.encoded, path, "")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body)
	}
	if !bytes.Equal(response.Body.Bytes(), bundle.Canonical()) {
		t.Fatal("export response changed canonical bundle bytes")
	}
	if got := response.Header().Get("Content-Type"); got != decisionBundleMediaType {
		t.Fatalf("Content-Type = %q, want %q", got, decisionBundleMediaType)
	}
	if got, want := response.Header().Get("ETag"), decisionETag(bundle.Digest()); got != want {
		t.Fatalf("ETag = %q, want %q", got, want)
	}

	notModified := authenticatedDecisionRequest(
		t,
		router,
		fixture.encoded,
		path,
		decisionETag(bundle.Digest()),
	)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 ||
		notModified.Header().Get("ETag") != decisionETag(bundle.Digest()) {
		t.Fatalf("not modified response = status %d headers=%v body=%q", notModified.Code, notModified.Header(), notModified.Body)
	}
}

func TestDecisionRoutesSeparateReadAndExportPermissions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		permission access.Pattern
		path       func(id.Decision, id.Verification) string
		want       int
	}{
		{name: "read permits report", permission: "decisions:read", path: func(decision id.Decision, _ id.Verification) string {
			return "/v1/decisions/" + decision.String()
		}, want: http.StatusOK},
		{name: "read cannot export", permission: "decisions:read", path: func(decision id.Decision, _ id.Verification) string {
			return "/v1/decisions/" + decision.String() + "/bundle"
		}, want: http.StatusForbidden},
		{name: "export permits bundle", permission: "decisions:export", path: func(decision id.Decision, _ id.Verification) string {
			return "/v1/decisions/" + decision.String() + "/bundle"
		}, want: http.StatusOK},
		{name: "export cannot read report", permission: "decisions:export", path: func(decision id.Decision, _ id.Verification) string {
			return "/v1/decisions/" + decision.String()
		}, want: http.StatusForbidden},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newHTTPAccessFixture(t, nil, test.permission)
			bundle, report, decisionID, verificationID := decisionHTTPBundle(t, fixture.tenantID)
			router := decisionHTTPRouter(t, fixture, &decisionHTTPReaderStub{report: report, bundle: bundle})
			response := authenticatedDecisionRequest(
				t,
				router,
				fixture.encoded,
				test.path(decisionID, verificationID),
				"",
			)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, test.want, response.Body)
			}
		})
	}
}

func TestDecisionRoutesHideMalformedAndUnavailableIdentifiers(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("decisions:read"))
	bundle, report, _, _ := decisionHTTPBundle(t, fixture.tenantID)
	malformedRouter := decisionHTTPRouter(t, fixture, &decisionHTTPReaderStub{report: report, bundle: bundle})
	missingRouter := decisionHTTPRouter(t, fixture, &decisionHTTPReaderStub{err: policy.ErrDecisionNotFound})

	malformed := authenticatedDecisionRequest(
		t,
		malformedRouter,
		fixture.encoded,
		"/v1/decisions/not-a-decision",
		"",
	)
	validMissing := authenticatedDecisionRequest(
		t,
		missingRouter,
		fixture.encoded,
		"/v1/decisions/dec_01M11HEQG00000000000000000",
		"",
	)
	if malformed.Code != http.StatusNotFound || validMissing.Code != http.StatusNotFound ||
		malformed.Body.String() != validMissing.Body.String() {
		t.Fatalf("non-disclosing responses differ: malformed=%s missing=%s", malformed.Body, validMissing.Body)
	}
}

func TestDecisionRoutesRejectCaptureCredentialAndMissingDependencies(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("decisions:*"))
	bundle, report, decisionID, _ := decisionHTTPBundle(t, fixture.tenantID)
	reader := &decisionHTTPReaderStub{report: report, bundle: bundle}
	router := decisionHTTPRouter(t, fixture, reader)
	response := authenticatedDecisionRequest(
		t,
		router,
		"idq_cap_v1.1.payload.signature",
		"/v1/decisions/"+decisionID.String(),
		"",
	)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("capture credential status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
	if _, err := NewDecisionRoutes(nil, reader, fixture.logger); err == nil {
		t.Error("NewDecisionRoutes(nil access) error = nil")
	}
	if _, err := NewDecisionRoutes(fixture.middleware, nil, fixture.logger); err == nil {
		t.Error("NewDecisionRoutes(nil reader) error = nil")
	}
	if _, err := NewDecisionRoutes(fixture.middleware, reader, nil); err == nil {
		t.Error("NewDecisionRoutes(nil logger) error = nil")
	}
}

func TestDecisionRoutesSupportGeneratedClient(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("decisions:*"))
	bundle, report, decisionID, verificationID := decisionHTTPBundle(t, fixture.tenantID)
	router := decisionHTTPRouter(t, fixture, &decisionHTTPReaderStub{report: report, bundle: bundle})
	client, err := openapiv1.NewClientWithResponses(
		"https://idenqa.test",
		openapiv1.WithHTTPClient(handlerDoer{handler: router}),
		openapiv1.WithRequestEditorFn(func(_ context.Context, request *http.Request) error {
			request.Header.Set("Authorization", "Bearer "+fixture.encoded)

			return nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	exact, err := client.GetDecisionWithResponse(t.Context(), decisionID.String(), nil)
	if err != nil || exact.JSON200 == nil || exact.JSON200.DecisionDigest != report.DecisionDigest {
		t.Fatalf("generated exact response = %+v error=%v", exact, err)
	}
	latest, err := client.GetLatestDecisionWithResponse(t.Context(), verificationID.String(), nil)
	if err != nil || latest.JSON200 == nil || latest.JSON200.DecisionID != report.DecisionID {
		t.Fatalf("generated latest response = %+v error=%v", latest, err)
	}
	exported, err := client.ExportDecisionBundleWithResponse(t.Context(), decisionID.String(), nil)
	if err != nil || exported.ApplicationVndIdenqaDecisionBundleV1JSON200 == nil ||
		exported.ApplicationVndIdenqaDecisionBundleV1JSON200.BundleDigest != bundle.Digest() ||
		!bytes.Equal(exported.Body, bundle.Canonical()) {
		t.Fatalf("generated export response = %+v error=%v", exported, err)
	}
}

type decisionHTTPReaderStub struct {
	report               policy.ReproductionReport
	bundle               policy.DecisionBundle
	err                  error
	tenantID             id.Tenant
	decisionID           id.Decision
	latestVerificationID id.Verification
}

func (reader *decisionHTTPReaderStub) Find(
	_ context.Context,
	authority access.Context,
	decisionID id.Decision,
) (policy.ReproductionReport, error) {
	reader.tenantID, reader.decisionID = authority.TenantScope().ID(), decisionID

	return reader.report, reader.err
}

func (reader *decisionHTTPReaderStub) FindLatest(
	_ context.Context,
	authority access.Context,
	verificationID id.Verification,
) (policy.ReproductionReport, error) {
	reader.tenantID, reader.latestVerificationID = authority.TenantScope().ID(), verificationID

	return reader.report, reader.err
}

func (reader *decisionHTTPReaderStub) Export(
	_ context.Context,
	authority access.Context,
	decisionID id.Decision,
) (policy.DecisionBundle, policy.ReproductionReport, error) {
	reader.tenantID, reader.decisionID = authority.TenantScope().ID(), decisionID

	return reader.bundle, reader.report, reader.err
}

func decisionHTTPRouter(
	t *testing.T,
	fixture *httpAccessFixture,
	reader DecisionReader,
) http.Handler {
	t.Helper()
	routes, err := NewDecisionRoutes(fixture.middleware, reader, fixture.logger)
	if err != nil {
		t.Fatal(err)
	}
	router := versionedRouter(t, routes)

	return router
}

func authenticatedDecisionRequest(
	t *testing.T,
	handler http.Handler,
	credential string,
	path string,
	etag string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(context.Background(), http.MethodGet, path, nil)
	request.Header.Set("Authorization", "Bearer "+credential)
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	return response
}

func decisionHTTPBundle(
	t testing.TB,
	tenantID id.Tenant,
) (policy.DecisionBundle, policy.ReproductionReport, id.Decision, id.Verification) {
	t.Helper()
	const ulid = "01M11HEQG00000000000000000"
	parse := func(prefix id.Prefix) string { return string(prefix) + "_" + ulid }
	verificationID, _ := id.ParseVerification(parse(id.VerificationPrefix))
	authorityID, _ := id.ParseAuthority(parse(id.AuthorityPrefix))
	acknowledgementID, _ := id.ParseAcknowledgement(parse(id.AcknowledgementPrefix))
	policyID, _ := id.ParsePolicy(parse(id.PolicyPrefix))
	decisionID, _ := id.ParseDecision(parse(id.DecisionPrefix))
	factKey, _ := policy.NewFactKey("authority.processing_permitted")
	now := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)
	snapshot, err := policy.NewSnapshot(policy.SnapshotInput{
		TenantID: tenantID, VerificationID: verificationID, AuthorityID: authorityID,
		AcknowledgementID: acknowledgementID, Region: "tenant_home",
		Policy:      policy.Reference{ID: policyID, Revision: 3, SchemaMajor: 1, SchemaMinor: 0, Digest: strings.Repeat("a", 64)},
		Evaluator:   policy.EvaluatorReference{Major: 1, Minor: 0, Digest: strings.Repeat("b", 64)},
		EvaluatedAt: now,
		Facts: []policy.Fact{{
			Key: factKey, State: policy.RequirementSatisfied, ObservedAt: now.Add(-time.Minute),
			Source: policy.FactSource{Kind: policy.FactSourceProcessingAuthority,
				Authority: &policy.AuthoritySource{AuthorityID: authorityID}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := policy.Resolve(snapshot, []policy.RequirementResult{{
		Name: "identity", State: policy.RequirementSatisfied,
		ContributingFacts: []policy.FactKey{factKey}, Candidate: policy.DirectiveCompleteVerified,
		Priority: 1,
	}}, "identity_verified")
	if err != nil {
		t.Fatal(err)
	}
	decision, err := policy.NewDecision(policy.DecisionInput{
		ID: decisionID, Snapshot: snapshot, Evaluation: evaluation,
		Actor: policy.ActorMachine, DecidedAt: now.Add(time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	bundle, report, err := policy.NewDecisionBundle(decision)
	if err != nil {
		t.Fatal(err)
	}

	return bundle, report, decisionID, verificationID
}
