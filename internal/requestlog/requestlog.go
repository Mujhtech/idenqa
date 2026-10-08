// Package requestlog owns the operational API request journal.
package requestlog

import (
	"github.com/Mujhtech/idenqa/internal/platform/observability"

	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

var (
	// ErrInvalid reports invalid request-log data or service configuration.
	ErrInvalid = errors.New("request log: invalid")
	// ErrNotFound reports that a request-log record does not exist in the tenant scope.
	ErrNotFound = errors.New("request log: not found")
)

// Record contains allowlisted operational metadata and bounded, redacted
// request parameters. Raw paths, headers, credentials, evidence bytes, and
// unredacted bodies do not belong here.
type Record struct {
	RequestID, ActorKeyID, Method, RouteTemplate, TraceReference, ClientIPAddress string
	QueryParameters, BodyParameters                                               json.RawMessage
	StatusCode                                                                    int
	DurationMilliseconds                                                          int64
	OccurredAt                                                                    time.Time
}

// Cursor identifies the exclusive position after which a request-log page continues.
type Cursor struct {
	OccurredAt time.Time
	RequestID  string
}

// AnalyticsWindow is a closed UTC interval used for privacy-safe operational
// aggregation. It never selects request bodies or subject identifiers.
type AnalyticsWindow struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// VolumeBucket contains privacy-safe request counts for one UTC day.
type VolumeBucket struct {
	Day      time.Time `json:"day"`
	Requests int64     `json:"requests"`
	Errors   int64     `json:"errors"`
}

// EndpointMetric contains aggregate request counts and latency for one route.
type EndpointMetric struct {
	Method                 string `json:"method"`
	RouteTemplate          string `json:"route_template"`
	Requests               int64  `json:"requests"`
	Successes              int64  `json:"successes"`
	P95LatencyMilliseconds int64  `json:"p95_latency_ms"`
}

// StatusMetric contains the count for one HTTP status class.
type StatusMetric struct {
	Class string `json:"class"`
	Count int64  `json:"count"`
}

// Analytics contains privacy-safe aggregate request metrics for a time window.
type Analytics struct {
	Window                 AnalyticsWindow  `json:"window"`
	Requests               int64            `json:"requests"`
	Successes              int64            `json:"successes"`
	Errors                 int64            `json:"errors"`
	P95LatencyMilliseconds int64            `json:"p95_latency_ms"`
	PreviousRequests       int64            `json:"previous_requests"`
	Volume                 []VolumeBucket   `json:"volume"`
	Endpoints              []EndpointMetric `json:"endpoints"`
	Statuses               []StatusMetric   `json:"statuses"`
}

// AnalyticsRepository reads aggregate request metrics for a tenant.
type AnalyticsRepository interface {
	Aggregate(context.Context, tenant.Scope, AnalyticsWindow) (Analytics, error)
}

// Repository persists and reads tenant-scoped request-log records.
type Repository interface {
	Append(context.Context, tenant.Scope, Record) error
	Get(context.Context, tenant.Scope, string) (Record, error)
	List(context.Context, tenant.Scope, Cursor, int) ([]Record, error)
}

// Service validates and authorises tenant request-log operations.
type Service struct {
	tracer     observability.Tracer
	repository Repository
}

// NewService constructs a request-log service backed by repository.
func NewService(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, ErrInvalid
	}
	return &Service{repository: repository}, nil
}

// Append stores one validated request-log record for scope.
func (service *Service) Append(ctx context.Context, scope tenant.Scope, record Record) (spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "requestlog.Service.Append")
	defer observability.EndSpan(completeSpan, &spanErr)

	if service == nil || service.repository == nil || scope.ID().IsZero() || !valid(record) {
		return ErrInvalid
	}
	if err := service.repository.Append(ctx, scope, record); err != nil {
		return fmt.Errorf("append API request log: %w", err)
	}
	return nil
}

// Get returns one request-log record after checking audit-export permission.
func (service *Service) Get(ctx context.Context, authority access.Context, requestID string) (spanResult0 Record, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "requestlog.Service.Get")
	defer observability.EndSpan(completeSpan, &spanErr)

	if service == nil || service.repository == nil || authority.TenantScope().ID().IsZero() || strings.TrimSpace(requestID) == "" {
		return Record{}, ErrInvalid
	}
	if err := authority.Require(access.PermissionAuditExport); err != nil {
		return Record{}, err
	}
	record, err := service.repository.Get(ctx, authority.TenantScope(), requestID)
	if err != nil {
		return Record{}, fmt.Errorf("get API request log: %w", err)
	}
	if !valid(record) || record.RequestID != requestID {
		return Record{}, ErrInvalid
	}
	return record, nil
}

// List returns a newest-first page. before is exclusive; zero starts now.
func (service *Service) List(ctx context.Context, authority access.Context, before Cursor, limit int) (spanResult0 []Record, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "requestlog.Service.List")
	defer observability.EndSpan(completeSpan, &spanErr)

	if service == nil || service.repository == nil || authority.TenantScope().ID().IsZero() || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	if err := authority.Require(access.PermissionAuditExport); err != nil {
		return nil, err
	}
	records, err := service.repository.List(ctx, authority.TenantScope(), before, limit)
	if err != nil {
		return nil, fmt.Errorf("list API request logs: %w", err)
	}
	for index, record := range records {
		if !valid(record) || (index > 0 && records[index-1].OccurredAt.Before(record.OccurredAt)) {
			return nil, ErrInvalid
		}
	}
	return records, nil
}

// Aggregate returns bounded, content-free traffic metrics for one tenant.
func (service *Service) Aggregate(ctx context.Context, authority access.Context, window AnalyticsWindow) (spanResult0 Analytics, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "requestlog.Service.Aggregate")
	defer observability.EndSpan(completeSpan, &spanErr)

	if service == nil || service.repository == nil || authority.TenantScope().ID().IsZero() || !validWindow(window) {
		return Analytics{}, ErrInvalid
	}
	if err := authority.Require(access.PermissionAuditExport); err != nil {
		return Analytics{}, err
	}
	repository, ok := service.repository.(AnalyticsRepository)
	if !ok {
		return Analytics{}, ErrInvalid
	}
	result, err := repository.Aggregate(ctx, authority.TenantScope(), window)
	if err != nil {
		return Analytics{}, fmt.Errorf("aggregate API request logs: %w", err)
	}
	result.Window = window
	return result, nil
}

func validWindow(window AnalyticsWindow) bool {
	return !window.From.IsZero() && !window.To.IsZero() && window.From.Location() == time.UTC && window.To.Location() == time.UTC &&
		window.To.After(window.From) && window.To.Sub(window.From) <= 90*24*time.Hour
}

func valid(record Record) bool {
	return record.RequestID != "" && record.ActorKeyID != "" && record.Method != "" &&
		strings.HasPrefix(record.RouteTemplate, "/v1/") && !strings.Contains(record.RouteTemplate, "?") &&
		validIPAddress(record.ClientIPAddress) && validJSONObject(record.QueryParameters) && validOptionalJSONObject(record.BodyParameters) &&
		record.StatusCode >= 100 && record.StatusCode <= 599 && record.DurationMilliseconds >= 0 &&
		!record.OccurredAt.IsZero() && record.OccurredAt.Location() == time.UTC
}

func validIPAddress(value string) bool {
	_, err := netip.ParseAddr(value)
	return err == nil
}

func validJSONObject(value json.RawMessage) bool {
	var object map[string]any
	return len(value) > 0 && json.Unmarshal(value, &object) == nil && object != nil
}

func validOptionalJSONObject(value json.RawMessage) bool {
	return len(value) == 0 || validJSONObject(value)
}

// WithTracer injects operation tracing during composition, before concurrent use.
func (service *Service) WithTracer(tracer observability.Tracer) *Service {
	if service != nil {
		service.tracer = tracer
	}
	return service
}

func (service *Service) operationTracer() observability.Tracer {
	if service == nil {
		return nil
	}
	return service.tracer
}
