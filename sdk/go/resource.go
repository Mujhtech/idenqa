package idenqa

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// MutationOptions carries a caller-owned key that must be reused for exact retries.
type MutationOptions struct{ IdempotencyKey string }

// PaginationOptions selects one bounded page. Keep filters and limit unchanged when following Cursor.
type PaginationOptions struct {
	Limit  int
	Cursor string
}

func (o PaginationOptions) values() url.Values {
	return queryValues(o.Limit, map[string]string{"cursor": o.Cursor})
}

// HistoryOptions bounds a non-paginated history read; zero uses the server default.
type HistoryOptions struct{ Limit int }

func (o HistoryOptions) values() url.Values { return queryValues(o.Limit, nil) }

// DecisionHistoryOptions selects an exclusive immutable decision cursor.
type DecisionHistoryOptions struct {
	Limit  int
	Before string
}

func (o DecisionHistoryOptions) values() url.Values {
	return queryValues(o.Limit, map[string]string{"before": o.Before})
}

// ImpactAssessmentListOptions uses an exclusive creation-time cursor.
type ImpactAssessmentListOptions struct {
	Limit  int
	Before time.Time
}

func (o ImpactAssessmentListOptions) values() url.Values {
	values := queryValues(o.Limit, nil)
	if !o.Before.IsZero() {
		values.Set("before", o.Before.Format(time.RFC3339Nano))
	}
	return values
}

// PrivacyRequestListOptions filters tenant privacy requests.
type PrivacyRequestListOptions struct {
	PaginationOptions
	State     PrivacyRequestState
	Type      PrivacyRequestType
	SubjectID string
}

func (o PrivacyRequestListOptions) values() url.Values {
	return queryValues(o.Limit, map[string]string{"cursor": o.Cursor, "state": string(o.State), "type": string(o.Type), "subject_id": o.SubjectID})
}

// PrivacyRestrictionListOptions optionally restricts a page to a subject.
type PrivacyRestrictionListOptions struct {
	PaginationOptions
	SubjectID string
}

func (o PrivacyRestrictionListOptions) values() url.Values {
	return queryValues(o.Limit, map[string]string{"cursor": o.Cursor, "subject_id": o.SubjectID})
}

// PrivacyDisclosureListOptions optionally restricts a page to a privacy request.
type PrivacyDisclosureListOptions struct {
	PaginationOptions
	RequestID string
}

func (o PrivacyDisclosureListOptions) values() url.Values {
	return queryValues(o.Limit, map[string]string{"cursor": o.Cursor, "request_id": o.RequestID})
}

// DeletionListOptions optionally restricts a page to an aggregate.
type DeletionListOptions struct {
	PaginationOptions
	AggregateID string
}

func (o DeletionListOptions) values() url.Values {
	return queryValues(o.Limit, map[string]string{"cursor": o.Cursor, "aggregate_id": o.AggregateID})
}

// RetentionOptions identifies the aggregate whose retention is inspected.
type RetentionOptions struct{ AggregateID string }

func (o RetentionOptions) values() url.Values { return url.Values{"aggregate_id": {o.AggregateID}} }

// ListVerificationDecisions calls the published GET /v1/verifications/{id}/decisions operation.
func (client *Client) ListVerificationDecisions(ctx context.Context, verificationID string, options DecisionHistoryOptions) (*Response[PolicyDecisionList], error) {
	return callResource[PolicyDecisionList](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/verifications/%s/decisions",
		Identifier: verificationID, Prefix: "ver",
		Query: options.values(),
	})
}

// CreateVerificationReconsideration calls the published POST /v1/verifications/{id}/reconsiderations operation.
func (client *Client) CreateVerificationReconsideration(ctx context.Context, verificationID string, input VerificationReconsiderationCreate, options MutationOptions) (*Response[ReviewFollowup], error) {
	return callResource[ReviewFollowup](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/verifications/%s/reconsiderations",
		Identifier: verificationID, Prefix: "ver",
		Input:          input,
		IdempotencyKey: options.IdempotencyKey, RequireKey: true,
	})
}

// GetEvidence calls the published GET /v1/evidence/{id} operation.
func (client *Client) GetEvidence(ctx context.Context, evidenceID string) (*Response[EvidenceMetadata], error) {
	return callResource[EvidenceMetadata](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/evidence/%s",
		Identifier: evidenceID, Prefix: "evd",
	})
}

// ListEvidenceLifecycle calls the published GET /v1/evidence/{id}/lifecycle operation.
func (client *Client) ListEvidenceLifecycle(ctx context.Context, evidenceID string, options HistoryOptions) (*Response[EvidenceLifecycleList], error) {
	return callResource[EvidenceLifecycleList](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/evidence/%s/lifecycle",
		Identifier: evidenceID, Prefix: "evd",
		Query: options.values(),
	})
}

// GetEvidenceAccessGrant calls the published GET /v1/evidence-access-grants/{id} operation.
func (client *Client) GetEvidenceAccessGrant(ctx context.Context, grantID string) (*Response[EvidenceAccessGrant], error) {
	return callResource[EvidenceAccessGrant](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/evidence-access-grants/%s",
		Identifier: grantID, Prefix: "grt",
	})
}

// CreateEvidenceAccessGrant calls the published POST /v1/evidence/{id}/access-grants operation.
func (client *Client) CreateEvidenceAccessGrant(ctx context.Context, evidenceID string, input EvidenceAccessGrantCreate, options MutationOptions) (*Response[EvidenceAccessGrant], error) {
	return callResource[EvidenceAccessGrant](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/evidence/%s/access-grants",
		Identifier: evidenceID, Prefix: "evd",
		Input:          input,
		IdempotencyKey: options.IdempotencyKey, RequireKey: true,
	})
}

// RevokeEvidenceAccessGrant calls the published POST /v1/evidence-access-grants/{id}/revoke operation.
func (client *Client) RevokeEvidenceAccessGrant(ctx context.Context, grantID string, input EvidenceAccessGrantRevoke, options MutationOptions) (*Response[EvidenceAccessGrant], error) {
	return callResource[EvidenceAccessGrant](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/evidence-access-grants/%s/revoke",
		Identifier: grantID, Prefix: "grt",
		Input:          input,
		IdempotencyKey: options.IdempotencyKey, RequireKey: true,
	})
}

// GetConsentReceipt calls the published GET /v1/consent-receipts/{id} operation.
func (client *Client) GetConsentReceipt(ctx context.Context, consentID string) (*Response[SubjectResponse], error) {
	return callResource[SubjectResponse](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/consent-receipts/%s",
		Identifier: consentID, Prefix: "ack",
	})
}

// RevokeConsentReceipt calls the published POST /v1/consent-receipts/{id}/revoke operation.
func (client *Client) RevokeConsentReceipt(ctx context.Context, consentID string, input ConsentReceiptRevoke, options MutationOptions) (*Response[SubjectResponse], error) {
	return callResource[SubjectResponse](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/consent-receipts/%s/revoke",
		Identifier: consentID, Prefix: "ack",
		Input:          input,
		IdempotencyKey: options.IdempotencyKey, RequireKey: true,
	})
}

// CreateProposalImpactAssessment calls the published POST /v1/proposal-impact-assessments operation.
// Creation has no idempotency-key contract; reconcile ambiguous failures before retrying.
func (client *Client) CreateProposalImpactAssessment(ctx context.Context, input ProposalImpactAssessmentCreate) (*Response[ProposalImpactAssessment], error) {
	return callResource[ProposalImpactAssessment](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/proposal-impact-assessments",
		Input: input,
	})
}

// GetProposalImpactAssessment calls the published GET /v1/proposal-impact-assessments/{id} operation.
func (client *Client) GetProposalImpactAssessment(ctx context.Context, assessmentID string) (*Response[ProposalImpactAssessment], error) {
	return callResource[ProposalImpactAssessment](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/proposal-impact-assessments/%s",
		Identifier: assessmentID, Prefix: "imp",
	})
}

// ListProposalImpactAssessments calls the published GET /v1/proposal-impact-assessments operation.
func (client *Client) ListProposalImpactAssessments(ctx context.Context, options ImpactAssessmentListOptions) (*Response[ProposalImpactAssessmentList], error) {
	return callResource[ProposalImpactAssessmentList](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/proposal-impact-assessments",
		Query: options.values(),
	})
}

// CreatePrivacyRequest calls the published POST /v1/privacy-requests operation.
// Creation has no idempotency-key contract; reconcile ambiguous failures before retrying.
func (client *Client) CreatePrivacyRequest(ctx context.Context, input PrivacyRequestCreate) (*Response[PrivacyRequestSummary], error) {
	return callResource[PrivacyRequestSummary](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/privacy-requests",
		Input: input,
	})
}

// GetPrivacyRequest calls the published GET /v1/privacy-requests/{id} operation.
func (client *Client) GetPrivacyRequest(ctx context.Context, requestID string) (*Response[PrivacyRequestStatus], error) {
	return callResource[PrivacyRequestStatus](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/privacy-requests/%s",
		Identifier: requestID, Prefix: "prq",
	})
}

// ListPrivacyRequests calls the published GET /v1/privacy-requests operation.
func (client *Client) ListPrivacyRequests(ctx context.Context, options PrivacyRequestListOptions) (*Response[PrivacyRequestList], error) {
	return callResource[PrivacyRequestList](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/privacy-requests",
		Query: options.values(),
	})
}

// ApprovePrivacyRequest calls the published POST /v1/privacy-requests/{id}/approve operation.
func (client *Client) ApprovePrivacyRequest(ctx context.Context, requestID string, input PrivacyRequestDecision) (*Response[PrivacyRequestStatus], error) {
	return callResource[PrivacyRequestStatus](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/privacy-requests/%s/approve",
		Identifier: requestID, Prefix: "prq",
		Input: input,
	})
}

// DenyPrivacyRequest calls the published POST /v1/privacy-requests/{id}/deny operation.
func (client *Client) DenyPrivacyRequest(ctx context.Context, requestID string, input PrivacyRequestDecision) (*Response[PrivacyRequestStatus], error) {
	return callResource[PrivacyRequestStatus](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/privacy-requests/%s/deny",
		Identifier: requestID, Prefix: "prq",
		Input: input,
	})
}

// WithdrawPrivacyRequest calls the published POST /v1/privacy-requests/{id}/withdraw operation.
func (client *Client) WithdrawPrivacyRequest(ctx context.Context, requestID string, input PrivacyRequestExpectedVersion) (*Response[PrivacyRequestStatus], error) {
	return callResource[PrivacyRequestStatus](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/privacy-requests/%s/withdraw",
		Identifier: requestID, Prefix: "prq",
		Input: input,
	})
}

// ExecutePrivacyRequest calls the published POST /v1/privacy-requests/{id}/execute operation.
func (client *Client) ExecutePrivacyRequest(ctx context.Context, requestID string, input PrivacyRequestExpectedVersion) (*Response[PrivacyRequestStatus], error) {
	return callResource[PrivacyRequestStatus](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/privacy-requests/%s/execute",
		Identifier: requestID, Prefix: "prq",
		Input: input,
	})
}

// ListPrivacyRestrictions calls the published GET /v1/privacy-restrictions operation.
func (client *Client) ListPrivacyRestrictions(ctx context.Context, options PrivacyRestrictionListOptions) (*Response[PrivacyRestrictionList], error) {
	return callResource[PrivacyRestrictionList](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/privacy-restrictions",
		Query: options.values(),
	})
}

// LiftPrivacyRestriction calls the published POST /v1/privacy-restrictions/{id}/lift operation.
func (client *Client) LiftPrivacyRestriction(ctx context.Context, restrictionID string, input PrivacyRestrictionLift) (*Response[PrivacyRestriction], error) {
	return callResource[PrivacyRestriction](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/privacy-restrictions/%s/lift",
		Identifier: restrictionID, Prefix: "prs",
		Input: input,
	})
}

// CreatePrivacyDisclosure calls the published POST /v1/privacy-disclosures operation.
func (client *Client) CreatePrivacyDisclosure(ctx context.Context, input PrivacyDisclosureCreate) (*Response[PrivacyDisclosure], error) {
	return callResource[PrivacyDisclosure](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/privacy-disclosures",
		Input: input,
	})
}

// ListPrivacyDisclosures calls the published GET /v1/privacy-disclosures operation.
func (client *Client) ListPrivacyDisclosures(ctx context.Context, options PrivacyDisclosureListOptions) (*Response[PrivacyDisclosureList], error) {
	return callResource[PrivacyDisclosureList](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/privacy-disclosures",
		Query: options.values(),
	})
}

// CreatePrivacyProcessor calls the published POST /v1/privacy-processors operation.
func (client *Client) CreatePrivacyProcessor(ctx context.Context, input PrivacyProcessorPut) (*Response[PrivacyProcessor], error) {
	return callResource[PrivacyProcessor](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/privacy-processors",
		Input: input,
	})
}

// GetPrivacyProcessor calls the published GET /v1/privacy-processors/{id} operation.
func (client *Client) GetPrivacyProcessor(ctx context.Context, processorID string) (*Response[PrivacyProcessor], error) {
	return callResource[PrivacyProcessor](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/privacy-processors/%s",
		Identifier: processorID, Prefix: "prc",
	})
}

// UpdatePrivacyProcessor calls the published PUT /v1/privacy-processors/{id} operation.
func (client *Client) UpdatePrivacyProcessor(ctx context.Context, processorID string, input PrivacyProcessorPut) (*Response[PrivacyProcessor], error) {
	return callResource[PrivacyProcessor](ctx, client, resourceRequest{
		Method: "PUT", Path: "v1/privacy-processors/%s",
		Identifier: processorID, Prefix: "prc",
		Input: input,
	})
}

// ListPrivacyProcessors calls the published GET /v1/privacy-processors operation.
func (client *Client) ListPrivacyProcessors(ctx context.Context, options PaginationOptions) (*Response[PrivacyProcessorList], error) {
	return callResource[PrivacyProcessorList](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/privacy-processors",
		Query: options.values(),
	})
}

// ListDeletions calls the published GET /v1/deletions operation.
func (client *Client) ListDeletions(ctx context.Context, options DeletionListOptions) (*Response[PrivacyDeletionList], error) {
	return callResource[PrivacyDeletionList](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/deletions",
		Query: options.values(),
	})
}

// GetDeletionStatus calls the published GET /v1/deletions/{id} operation.
func (client *Client) GetDeletionStatus(ctx context.Context, deletionID string) (*Response[PrivacyDeletionStatus], error) {
	return callResource[PrivacyDeletionStatus](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/deletions/%s",
		Identifier: deletionID, Prefix: "del",
	})
}

// GetRetentionResolution calls the published GET /v1/retention/resolutions operation.
func (client *Client) GetRetentionResolution(ctx context.Context, options RetentionOptions) (*Response[PrivacyRetentionResolution], error) {
	return callResource[PrivacyRetentionResolution](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/retention/resolutions",
		Query: options.values(),
	})
}

type resourceRequest struct {
	Method         string
	Path           string
	Identifier     string
	Prefix         string
	Input          any
	Query          url.Values
	IdempotencyKey string
	RequireKey     bool
}

var resourceIdentifier = regexp.MustCompile("^[a-z]+_[0-9A-HJKMNP-TV-Z]{26}$")
var impactIdentifier = regexp.MustCompile("^imp_[0-9]+$")

func callResource[T any](ctx context.Context, client *Client, request resourceRequest) (*Response[T], error) {
	if request.Prefix != "" {
		valid := resourceIdentifier.MatchString(request.Identifier)
		if request.Prefix == "imp" {
			valid = impactIdentifier.MatchString(request.Identifier)
		}
		if !valid || !strings.HasPrefix(request.Identifier, request.Prefix+"_") {
			return nil, errors.New("idenqa: invalid resource identifier")
		}
		request.Path = fmt.Sprintf(request.Path, request.Identifier)
	}
	if value := request.Query.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 100 {
			return nil, errors.New("idenqa: limit must be from 1 to 100")
		}
	}
	if request.Path == "v1/retention/resolutions" && request.Query.Get("aggregate_id") == "" {
		return nil, errors.New("idenqa: aggregate identifier is required")
	}
	if len(request.Query) > 0 {
		request.Path += "?" + request.Query.Encode()
	}
	headers := make(http.Header)
	if request.RequireKey {
		encoded, err := encodeIdempotencyKey(request.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		headers.Set("Idempotency-Key", encoded)
	}
	result := &Response[T]{}
	metadata, err := client.doJSON(ctx, request.Method, request.Path, request.Input, &result.Data, headers)
	if err != nil {
		return nil, err
	}
	result.ResponseMetadata = metadata
	return result, nil
}

func encodeIdempotencyKey(key string) (string, error) {
	if key == "" {
		return "", errors.New("idenqa: idempotency key is required")
	}
	for _, char := range key {
		if char < 0x20 || char > 0x7e {
			return "", errors.New("idenqa: idempotency key must be printable ASCII")
		}
	}
	encoded := strconv.Quote(key)
	if len(encoded) > 130 {
		return "", errors.New("idenqa: encoded idempotency key exceeds 130 characters")
	}
	return encoded, nil
}

func queryValues(limit int, filters map[string]string) url.Values {
	values := make(url.Values)
	if limit != 0 {
		values.Set("limit", strconv.Itoa(limit))
	}
	for key, value := range filters {
		if value != "" {
			values.Set(key, value)
		}
	}
	return values
}
