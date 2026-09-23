package idenqa

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResourceOperations(t *testing.T) {
	tests := []struct {
		name, method, path     string
		keyed, body, retention bool
		call                   func(context.Context, *Client) (ResponseMetadata, error)
	}{
		{name: "ListVerificationDecisions", method: "GET", path: "/mount/v1/verifications/ver_01M11HEQG00000000000000000/decisions", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.ListVerificationDecisions(ctx, "ver_01M11HEQG00000000000000000", DecisionHistoryOptions{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "CreateVerificationReconsideration", method: "POST", path: "/mount/v1/verifications/ver_01M11HEQG00000000000000000/reconsiderations", keyed: true, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.CreateVerificationReconsideration(ctx, "ver_01M11HEQG00000000000000000", VerificationReconsiderationCreate{}, MutationOptions{IdempotencyKey: `sdk"\\retry`})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "GetEvidence", method: "GET", path: "/mount/v1/evidence/evd_01M11HEQG00000000000000000", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.GetEvidence(ctx, "evd_01M11HEQG00000000000000000")
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "ListEvidenceLifecycle", method: "GET", path: "/mount/v1/evidence/evd_01M11HEQG00000000000000000/lifecycle", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.ListEvidenceLifecycle(ctx, "evd_01M11HEQG00000000000000000", HistoryOptions{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "GetEvidenceAccessGrant", method: "GET", path: "/mount/v1/evidence-access-grants/grt_01M11HEQG00000000000000000", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.GetEvidenceAccessGrant(ctx, "grt_01M11HEQG00000000000000000")
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "CreateEvidenceAccessGrant", method: "POST", path: "/mount/v1/evidence/evd_01M11HEQG00000000000000000/access-grants", keyed: true, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.CreateEvidenceAccessGrant(ctx, "evd_01M11HEQG00000000000000000", EvidenceAccessGrantCreate{}, MutationOptions{IdempotencyKey: `sdk"\\retry`})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "RevokeEvidenceAccessGrant", method: "POST", path: "/mount/v1/evidence-access-grants/grt_01M11HEQG00000000000000000/revoke", keyed: true, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.RevokeEvidenceAccessGrant(ctx, "grt_01M11HEQG00000000000000000", EvidenceAccessGrantRevoke{}, MutationOptions{IdempotencyKey: `sdk"\\retry`})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "GetConsentReceipt", method: "GET", path: "/mount/v1/consent-receipts/ack_01M11HEQG00000000000000000", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.GetConsentReceipt(ctx, "ack_01M11HEQG00000000000000000")
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "RevokeConsentReceipt", method: "POST", path: "/mount/v1/consent-receipts/ack_01M11HEQG00000000000000000/revoke", keyed: true, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.RevokeConsentReceipt(ctx, "ack_01M11HEQG00000000000000000", ConsentReceiptRevoke{}, MutationOptions{IdempotencyKey: `sdk"\\retry`})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "CreateProposalImpactAssessment", method: "POST", path: "/mount/v1/proposal-impact-assessments", keyed: false, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.CreateProposalImpactAssessment(ctx, ProposalImpactAssessmentCreate{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "GetProposalImpactAssessment", method: "GET", path: "/mount/v1/proposal-impact-assessments/imp_1790000000000000000", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.GetProposalImpactAssessment(ctx, "imp_1790000000000000000")
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "ListProposalImpactAssessments", method: "GET", path: "/mount/v1/proposal-impact-assessments", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.ListProposalImpactAssessments(ctx, ImpactAssessmentListOptions{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "CreatePrivacyRequest", method: "POST", path: "/mount/v1/privacy-requests", keyed: false, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.CreatePrivacyRequest(ctx, PrivacyRequestCreate{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "GetPrivacyRequest", method: "GET", path: "/mount/v1/privacy-requests/prq_01M11HEQG00000000000000000", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.GetPrivacyRequest(ctx, "prq_01M11HEQG00000000000000000")
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "ListPrivacyRequests", method: "GET", path: "/mount/v1/privacy-requests", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.ListPrivacyRequests(ctx, PrivacyRequestListOptions{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "ApprovePrivacyRequest", method: "POST", path: "/mount/v1/privacy-requests/prq_01M11HEQG00000000000000000/approve", keyed: false, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.ApprovePrivacyRequest(ctx, "prq_01M11HEQG00000000000000000", PrivacyRequestDecision{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "DenyPrivacyRequest", method: "POST", path: "/mount/v1/privacy-requests/prq_01M11HEQG00000000000000000/deny", keyed: false, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.DenyPrivacyRequest(ctx, "prq_01M11HEQG00000000000000000", PrivacyRequestDecision{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "WithdrawPrivacyRequest", method: "POST", path: "/mount/v1/privacy-requests/prq_01M11HEQG00000000000000000/withdraw", keyed: false, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.WithdrawPrivacyRequest(ctx, "prq_01M11HEQG00000000000000000", PrivacyRequestExpectedVersion{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "ExecutePrivacyRequest", method: "POST", path: "/mount/v1/privacy-requests/prq_01M11HEQG00000000000000000/execute", keyed: false, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.ExecutePrivacyRequest(ctx, "prq_01M11HEQG00000000000000000", PrivacyRequestExpectedVersion{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "ListPrivacyRestrictions", method: "GET", path: "/mount/v1/privacy-restrictions", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.ListPrivacyRestrictions(ctx, PrivacyRestrictionListOptions{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "LiftPrivacyRestriction", method: "POST", path: "/mount/v1/privacy-restrictions/prs_01M11HEQG00000000000000000/lift", keyed: false, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.LiftPrivacyRestriction(ctx, "prs_01M11HEQG00000000000000000", PrivacyRestrictionLift{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "CreatePrivacyDisclosure", method: "POST", path: "/mount/v1/privacy-disclosures", keyed: false, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.CreatePrivacyDisclosure(ctx, PrivacyDisclosureCreate{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "ListPrivacyDisclosures", method: "GET", path: "/mount/v1/privacy-disclosures", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.ListPrivacyDisclosures(ctx, PrivacyDisclosureListOptions{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "CreatePrivacyProcessor", method: "POST", path: "/mount/v1/privacy-processors", keyed: false, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.CreatePrivacyProcessor(ctx, PrivacyProcessorPut{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "GetPrivacyProcessor", method: "GET", path: "/mount/v1/privacy-processors/prc_01M11HEQG00000000000000000", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.GetPrivacyProcessor(ctx, "prc_01M11HEQG00000000000000000")
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "UpdatePrivacyProcessor", method: "PUT", path: "/mount/v1/privacy-processors/prc_01M11HEQG00000000000000000", keyed: false, body: true, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.UpdatePrivacyProcessor(ctx, "prc_01M11HEQG00000000000000000", PrivacyProcessorPut{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "ListPrivacyProcessors", method: "GET", path: "/mount/v1/privacy-processors", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.ListPrivacyProcessors(ctx, PaginationOptions{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "ListDeletions", method: "GET", path: "/mount/v1/deletions", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.ListDeletions(ctx, DeletionListOptions{})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "GetDeletionStatus", method: "GET", path: "/mount/v1/deletions/del_01M11HEQG00000000000000000", keyed: false, body: false, retention: false, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.GetDeletionStatus(ctx, "del_01M11HEQG00000000000000000")
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
		{name: "GetRetentionResolution", method: "GET", path: "/mount/v1/retention/resolutions", keyed: false, body: false, retention: true, call: func(ctx context.Context, client *Client) (ResponseMetadata, error) {
			result, err := client.GetRetentionResolution(ctx, RetentionOptions{AggregateID: "subject & other"})
			if err != nil {
				return ResponseMetadata{}, err
			}
			return result.ResponseMetadata, nil
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != test.method || r.URL.Path != test.path {
					t.Errorf("request = %s %s", r.Method, r.URL.Path)
				}
				if r.Header.Get("Authorization") != "Bearer fixture" {
					t.Error("missing tenant credential")
				}
				if test.keyed && r.Header.Get("Idempotency-Key") != `"sdk\"\\\\retry"` {
					t.Errorf("idempotency header = %q", r.Header.Get("Idempotency-Key"))
				}
				if !test.keyed && r.Header.Get("Idempotency-Key") != "" {
					t.Error("invented idempotency contract")
				}
				if test.retention && r.URL.Query().Get("aggregate_id") != "subject & other" {
					t.Error("query did not preserve aggregate")
				}
				if test.body && r.Header.Get("Content-Type") != "application/json" {
					t.Error("missing JSON content type")
				}
				w.Header().Set("X-Request-ID", "req_fixture")
				w.Header().Set("ETag", `"v2"`)
				w.Header().Set("Location", "/v1/created")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"future_field":true}`)
			}))
			defer server.Close()
			client, err := NewClient(server.URL+"/mount", "fixture", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			metadata, err := test.call(t.Context(), client)
			if err != nil {
				t.Fatal(err)
			}
			if calls != 1 || metadata.RequestID != "req_fixture" || metadata.ETag != `"v2"` || metadata.Location != "/v1/created" {
				t.Fatalf("lost response metadata: %+v, calls=%d", metadata, calls)
			}
		})
	}
}

func TestResourceValidation(t *testing.T) {
	tests := []struct {
		name string
		call func(*Client) error
	}{
		{"path injection", func(c *Client) error { _, e := c.GetEvidence(t.Context(), "../other"); return e }},
		{"wrong identifier kind", func(c *Client) error { _, e := c.GetEvidence(t.Context(), "grt_01M11HEQG00000000000000000"); return e }},
		{"invalid page limit", func(c *Client) error {
			_, e := c.ListDeletions(t.Context(), DeletionListOptions{PaginationOptions: PaginationOptions{Limit: 101}})
			return e
		}},
		{"missing aggregate", func(c *Client) error { _, e := c.GetRetentionResolution(t.Context(), RetentionOptions{}); return e }},
		{"missing retry key", func(c *Client) error {
			_, e := c.RevokeConsentReceipt(t.Context(), "ack_01M11HEQG00000000000000000", ConsentReceiptRevoke{Reason: "withdraw"}, MutationOptions{})
			return e
		}},
		{"header injection", func(c *Client) error {
			_, e := c.RevokeConsentReceipt(t.Context(), "ack_01M11HEQG00000000000000000", ConsentReceiptRevoke{Reason: "withdraw"}, MutationOptions{IdempotencyKey: "a\nb"})
			return e
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, err := NewClient("https://core.example.test", "fixture", &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("invalid input reached network")
				return nil, errors.New("unexpected request")
			})})
			if err != nil {
				t.Fatal(err)
			}
			if err := test.call(client); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}

func TestResourcePaginationAndProblem(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") != "opaque+/=" || r.URL.Query().Get("subject_id") != "subject & other" || r.URL.Query().Get("state") != "approved" || r.URL.Query().Get("limit") != "3" {
			t.Errorf("query = %v", r.URL.Query())
		}
		w.Header().Set("X-Request-ID", "req_denied")
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"code":"insufficient_scope","detail":"private details","status":403}`)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, "fixture", server.Client())
	_, err := client.ListPrivacyRequests(t.Context(), PrivacyRequestListOptions{PaginationOptions: PaginationOptions{Limit: 3, Cursor: "opaque+/="}, SubjectID: "subject & other", State: "approved"})
	var problem *APIError
	if !errors.As(err, &problem) || problem.StatusCode != 403 || problem.Problem.Code != "insufficient_scope" || problem.RequestID != "req_denied" {
		t.Fatalf("problem = %v", err)
	}
	if strings.Contains(err.Error(), "private details") {
		t.Fatal("error logs private response")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
