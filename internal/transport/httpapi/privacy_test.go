package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/platform/cursor"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type privacyTestService struct {
	page                privacy.DeletionPage
	pageAggregate       string
	pagePosition        string
	pageLimit           int
	status              privacy.DeletionStatus
	resolution          privacy.RetentionResolution
	resolutionAggregate string
}

func (service *privacyTestService) RequestEvidenceDeletion(context.Context, tenant.Scope, privacy.Actor, string, string) (privacy.Deletion, error) {
	return privacy.Deletion{}, errors.New("not used")
}
func (service *privacyTestService) Run(context.Context, tenant.Scope, privacy.Actor, id.Deletion) (privacy.Deletion, error) {
	return privacy.Deletion{}, errors.New("not used")
}
func (service *privacyTestService) CreateHold(context.Context, tenant.Scope, privacy.Actor, string, string, string, time.Time, time.Time) (privacy.Hold, error) {
	return privacy.Hold{}, errors.New("not used")
}
func (service *privacyTestService) ReleaseHold(context.Context, tenant.Scope, privacy.Actor, id.LegalHold) (privacy.Hold, error) {
	return privacy.Hold{}, errors.New("not used")
}
func (service *privacyTestService) FindDeletion(context.Context, tenant.Scope, privacy.Actor, id.Deletion) (privacy.Deletion, error) {
	return service.status.Deletion, nil
}
func (service *privacyTestService) ListDeletions(_ context.Context, _ tenant.Scope, _ privacy.Actor, aggregateID, position string, limit int) (privacy.DeletionPage, error) {
	service.pageAggregate, service.pagePosition, service.pageLimit = aggregateID, position, limit
	return service.page, nil
}
func (service *privacyTestService) DeletionStatus(context.Context, tenant.Scope, privacy.Actor, id.Deletion) (privacy.DeletionStatus, error) {
	return service.status, nil
}
func (service *privacyTestService) ResolveRetention(_ context.Context, _ tenant.Scope, _ privacy.Actor, aggregateID string) (privacy.RetentionResolution, error) {
	service.resolutionAggregate = aggregateID
	return service.resolution, nil
}

func privacyTestCursor(t *testing.T) *cursor.Codec {
	t.Helper()
	keyring, err := cursor.NewKeyring(cursor.KeyVersion(1), map[cursor.KeyVersion][]byte{1: bytes.Repeat([]byte{0x37}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	codec, err := cursor.New(keyring, routerClock{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return codec
}

func privacyTestRouter(t *testing.T, fixture *httpAccessFixture, service *privacyTestService) http.Handler {
	t.Helper()
	routes, err := NewPrivacyRoutes(fixture.middleware, service, privacyTestCursor(t), fixture.logger)
	if err != nil {
		t.Fatal(err)
	}
	return versionedRouter(t, routes)
}

func privacyTestRequest(t *testing.T, handler http.Handler, fixture *httpAccessFixture, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestPrivacyRoutesListDeletionsPagesWithBoundCursor(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("deletions:read"))
	deletionID, _ := id.ParseDeletion("del_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	service := &privacyTestService{page: privacy.DeletionPage{
		Deletions: []privacy.Deletion{{
			ID: deletionID, AggregateID: "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV", Region: "ng-1", State: privacy.DeletionInProgress,
			Targets:     []privacy.Target{{Kind: "raw_evidence", Reference: "raw-v1", Region: "ng-1"}},
			RequestedAt: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 6, 0, 5, 0, 0, time.UTC),
			BackupExpiresAt: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC), Version: 3,
		}},
		HasMore: true,
	}}
	handler := privacyTestRouter(t, fixture, service)

	response := privacyTestRequest(t, handler, fixture, http.MethodGet, "/v1/deletions?limit=10&aggregate_id=ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	if service.pageLimit != 10 || service.pageAggregate != "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV" || service.pagePosition != "" {
		t.Fatalf("service call = aggregate %q position %q limit %d", service.pageAggregate, service.pagePosition, service.pageLimit)
	}
	var page openapiv1.PrivacyDeletionList
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if !page.Page.HasMore || page.Page.NextCursor == nil || *page.Page.NextCursor == "" {
		t.Fatalf("page = %+v", page)
	}
	if len(page.Data) != 1 || page.Data[0].ID != deletionID.String() || len(page.Data[0].State) == 0 {
		t.Fatalf("data = %+v", page.Data)
	}

	next := *page.Page.NextCursor
	response = privacyTestRequest(t, handler, fixture, http.MethodGet, "/v1/deletions?limit=10&aggregate_id=ver_01ARZ3NDEKTSV4RRFFQ69G5FAV&cursor="+next)
	if response.Code != http.StatusOK || service.pagePosition != deletionID.String() {
		t.Fatalf("continuation status = %d position = %q; body=%s", response.Code, service.pagePosition, response.Body)
	}

	response = privacyTestRequest(t, handler, fixture, http.MethodGet, "/v1/deletions?limit=10&aggregate_id=ver_other&cursor="+next)
	assertAccessProblem(t, response, http.StatusBadRequest, "INVALID_REQUEST")

	response = privacyTestRequest(t, handler, fixture, http.MethodGet, "/v1/deletions?unknown=1")
	assertAccessProblem(t, response, http.StatusBadRequest, "INVALID_REQUEST")

	response = privacyTestRequest(t, handler, fixture, http.MethodGet, "/v1/deletions?limit=101")
	assertAccessProblem(t, response, http.StatusBadRequest, "INVALID_REQUEST")
}

func TestPrivacyRoutesDeletionStatusExposesDigestsAndHolds(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("deletions:read"))
	deletionID, _ := id.ParseDeletion("del_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	holdID, _ := id.ParseLegalHold("hld_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	deletion, err := privacy.NewDeletion(deletionID, "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV", "ng-1",
		[]privacy.Target{{Kind: "raw_evidence", Reference: "raw-secret-object-key", Region: "ng-1"}},
		time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	service := &privacyTestService{status: privacy.DeletionStatus{
		Deletion: deletion,
		Targets:  []privacy.TargetStatus{{Kind: "raw_evidence", Reference: "3f2a0c9d1b4e6a8c0d2f4b6a", State: privacy.TargetDeleted}},
		Holds: []privacy.Hold{{
			ID: holdID, AggregateID: deletion.AggregateID, Authority: "court-order", Reason: "pending proceedings",
			StartsAt: time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC), ReviewAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
		}},
	}}
	handler := privacyTestRouter(t, fixture, service)

	response := privacyTestRequest(t, handler, fixture, http.MethodGet, "/v1/deletions/"+deletionID.String())
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body)
	}
	if bytes.Contains(response.Body.Bytes(), []byte("raw-secret-object-key")) {
		t.Fatal("status response exposed the internal target reference")
	}
	var status openapiv1.PrivacyDeletionStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Targets) != 1 || status.Targets[0].State != openapiv1.PrivacyTargetStateDeleted || len(status.Holds) != 1 || status.Holds[0].ID != holdID.String() {
		t.Fatalf("status = %+v", status)
	}

	response = privacyTestRequest(t, handler, fixture, http.MethodGet, "/v1/deletions/not-a-deletion")
	assertAccessProblem(t, response, http.StatusNotFound, "NOT_FOUND")
}

func TestPrivacyRoutesRetentionResolutionRequiresOneAggregate(t *testing.T) {
	t.Parallel()
	fixture := newHTTPAccessFixture(t, nil, access.Pattern("deletions:read"))
	service := &privacyTestService{resolution: privacy.RetentionResolution{
		AggregateID: "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Records: []privacy.RetentionDeadline{{
			ID: "evd_01ARZ3NDEKTSV4RRFFQ69G5FAV", Class: privacy.DataClassRawEvidence, Region: "ng-1",
			Duration: 30 * 24 * time.Hour, ExpiresAt: time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC),
		}},
	}}
	handler := privacyTestRouter(t, fixture, service)

	response := privacyTestRequest(t, handler, fixture, http.MethodGet, "/v1/retention/resolutions?aggregate_id=ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if response.Code != http.StatusOK || service.resolutionAggregate != "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("status = %d aggregate = %q; body=%s", response.Code, service.resolutionAggregate, response.Body)
	}
	var resolution openapiv1.PrivacyRetentionResolution
	if err := json.Unmarshal(response.Body.Bytes(), &resolution); err != nil {
		t.Fatal(err)
	}
	if len(resolution.Records) != 1 || resolution.Records[0].DurationSeconds != int64((30*24*time.Hour)/time.Second) {
		t.Fatalf("resolution = %+v", resolution)
	}

	response = privacyTestRequest(t, handler, fixture, http.MethodGet, "/v1/retention/resolutions")
	assertAccessProblem(t, response, http.StatusBadRequest, "INVALID_REQUEST")
	response = privacyTestRequest(t, handler, fixture, http.MethodGet, "/v1/retention/resolutions?aggregate_id=a&aggregate_id=b")
	assertAccessProblem(t, response, http.StatusBadRequest, "INVALID_REQUEST")
}

func TestPrivacyRoutesRequireReadOrWriteScope(t *testing.T) {
	t.Parallel()
	writerFixture := newHTTPAccessFixture(t, nil, access.Pattern("deletions:write"))
	service := &privacyTestService{}
	writerHandler := privacyTestRouter(t, writerFixture, service)
	assertAccessProblem(t, privacyTestRequest(t, writerHandler, writerFixture, http.MethodGet, "/v1/deletions"), http.StatusForbidden, "INSUFFICIENT_SCOPE")
	assertAccessProblem(t, privacyTestRequest(t, writerHandler, writerFixture, http.MethodGet, "/v1/retention/resolutions?aggregate_id=ver_1"), http.StatusForbidden, "INSUFFICIENT_SCOPE")

	readerFixture := newHTTPAccessFixture(t, nil, access.Pattern("deletions:read"))
	readerHandler := privacyTestRouter(t, readerFixture, service)
	response := privacyTestRequest(t, readerHandler, readerFixture, http.MethodPost, "/v1/deletions/del_01ARZ3NDEKTSV4RRFFQ69G5FAV/run")
	assertAccessProblem(t, response, http.StatusForbidden, "INSUFFICIENT_SCOPE")
}
