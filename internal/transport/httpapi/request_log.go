package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/requestlog"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel/trace"
)

type requestLogRecorder interface {
	Append(context.Context, tenant.Scope, requestlog.Record) error
}

type requestLogReader interface {
	Get(context.Context, access.Context, string) (requestlog.Record, error)
	List(context.Context, access.Context, requestlog.Cursor, int) ([]requestlog.Record, error)
	Aggregate(context.Context, access.Context, requestlog.AnalyticsWindow) (requestlog.Analytics, error)
}

type requestLogAuthority struct {
	scope tenant.Scope
	actor string
}

type requestLogAuthorityKey struct{}

const requestLogParameterLimit = 16 << 10

type requestBodyCapture struct {
	io.ReadCloser
	buffer    bytes.Buffer
	truncated bool
}

func (capture *requestBodyCapture) Read(destination []byte) (int, error) {
	count, err := capture.ReadCloser.Read(destination)
	remaining := requestLogParameterLimit - capture.buffer.Len()
	if remaining > 0 {
		copied := min(count, remaining)
		_, _ = capture.buffer.Write(destination[:copied])
	}
	if count > remaining {
		capture.truncated = true
	}
	return count, err
}

func operationalRequestJournal(recorder requestLogRecorder, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			started := time.Now().UTC()
			queryParameters := captureQueryParameters(request)
			bodyCapture := &requestBodyCapture{ReadCloser: request.Body}
			request.Body = bodyCapture
			authority := &requestLogAuthority{}
			ctx := context.WithValue(request.Context(), requestLogAuthorityKey{}, authority)
			wrapped := chimiddleware.NewWrapResponseWriter(writer, request.ProtoMajor)
			next.ServeHTTP(wrapped, request.WithContext(ctx))
			route := chi.RouteContext(request.Context()).RoutePattern()
			if authority.scope.ID().IsZero() || authority.actor == "" || route == "" || route == "/v1/request-logs" {
				return
			}
			status := wrapped.Status()
			if status == 0 {
				status = http.StatusOK
			}
			traceReference := ""
			if spanContext := trace.SpanContextFromContext(request.Context()); spanContext.IsValid() {
				traceReference = spanContext.TraceID().String()
			}
			record := requestlog.Record{
				RequestID: requestIDString(request.Context()), ActorKeyID: authority.actor,
				Method: request.Method, RouteTemplate: route, StatusCode: status,
				DurationMilliseconds: time.Since(started).Milliseconds(), OccurredAt: started,
				TraceReference:  traceReference,
				ClientIPAddress: clientIPAddress(request.RemoteAddr),
				QueryParameters: queryParameters,
				BodyParameters:  captureBodyParameters(request.Header.Get("Content-Type"), bodyCapture),
			}
			persistCtx, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), 2*time.Second)
			defer cancel()
			if err := recorder.Append(persistCtx, authority.scope, record); err != nil {
				logger.ErrorContext(persistCtx, "persist API request log", "request_id", record.RequestID, "error", err)
			}
		})
	}
}

func clientIPAddress(remoteAddress string) string {
	host, _, err := net.SplitHostPort(remoteAddress)
	if err == nil {
		return host
	}
	return remoteAddress
}

func captureQueryParameters(request *http.Request) json.RawMessage {
	parameters := make(map[string]any, len(request.URL.Query()))
	for key, values := range request.URL.Query() {
		if sensitiveParameter(key) {
			parameters[key] = "[REDACTED]"
			continue
		}
		bounded := make([]string, len(values))
		for index, value := range values {
			bounded[index] = boundParameter(value)
		}
		if len(bounded) == 1 {
			parameters[key] = bounded[0]
		} else {
			parameters[key] = bounded
		}
	}
	encoded, _ := json.Marshal(parameters)
	return encoded
}

func captureBodyParameters(contentType string, capture *requestBodyCapture) json.RawMessage {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "application/json" || capture.buffer.Len() == 0 {
		return nil
	}
	if capture.truncated {
		return json.RawMessage(`{"_capture":"omitted_too_large"}`)
	}
	var body map[string]any
	if json.Unmarshal(capture.buffer.Bytes(), &body) != nil {
		return nil
	}
	redactParameters(body, 0)
	encoded, _ := json.Marshal(body)
	return encoded
}

func redactParameters(value map[string]any, depth int) {
	for key, item := range value {
		if sensitiveParameter(key) {
			value[key] = "[REDACTED]"
			continue
		}
		value[key] = sanitizeParameterValue(item, depth)
	}
}

func sanitizeParameterValue(value any, depth int) any {
	if depth >= 8 {
		return "[OMITTED]"
	}
	switch nested := value.(type) {
	case map[string]any:
		redactParameters(nested, depth+1)
		return nested
	case []any:
		for index, item := range nested {
			nested[index] = sanitizeParameterValue(item, depth+1)
		}
		return nested
	case string:
		return boundParameter(nested)
	default:
		return nested
	}
}

func sensitiveParameter(key string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), ".", "_"))
	for _, fragment := range []string{"authorization", "credential", "password", "secret", "token", "ticket", "signature", "cookie", "evidence", "selfie", "document", "biometric", "subject", "email", "phone", "address", "birth", "passport", "national_id", "first_name", "last_name", "full_name"} {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func boundParameter(value string) string {
	const maximum = 1024
	if len(value) <= maximum {
		return value
	}
	return value[:maximum] + "…"
}

func bindRequestLogAuthority(ctx context.Context, authority access.Context) {
	metadata, ok := ctx.Value(requestLogAuthorityKey{}).(*requestLogAuthority)
	if !ok || metadata == nil {
		return
	}
	metadata.scope = authority.TenantScope()
	metadata.actor = authority.Principal().KeyID().String()
}

type RequestLogRoutes struct {
	access *AccessMiddleware
	reader requestLogReader
	logger *slog.Logger
	now    func() time.Time
}

func NewRequestLogRoutes(accessMiddleware *AccessMiddleware, reader requestLogReader, logger *slog.Logger) (*RequestLogRoutes, error) {
	if accessMiddleware == nil || reader == nil || logger == nil {
		return nil, errors.New("request-log route dependencies are required")
	}
	return &RequestLogRoutes{access: accessMiddleware, reader: reader, logger: logger, now: time.Now}, nil
}

func (routes *RequestLogRoutes) Register(router chi.Router) {
	router.With(routes.access.Authorize(access.PermissionAuditExport)).Get("/request-logs", routes.list)
	router.With(routes.access.Authorize(access.PermissionAuditExport)).Get("/request-logs/{requestID}", routes.get)
	router.With(routes.access.Authorize(access.PermissionAuditExport)).Get("/request-analytics", routes.analytics)
}

func (routes *RequestLogRoutes) analytics(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	if len(query) != 1 || len(query["window"]) != 1 {
		routes.problem(writer, request, invalidRequest(errors.New("request analytics window is required")))
		return
	}
	days := 0
	switch query.Get("window") {
	case "7d":
		days = 7
	case "30d":
		days = 30
	case "90d":
		days = 90
	default:
		routes.problem(writer, request, invalidRequest(errors.New("request analytics window is invalid")))
		return
	}
	authority, ok := AccessContext(request.Context())
	if !ok {
		writeAccessProblem(writer, request, routes.logger, access.ErrInvalidCredential, true)
		return
	}
	to := routes.now().UTC().Truncate(time.Second)
	result, err := routes.reader.Aggregate(request.Context(), authority, requestlog.AnalyticsWindow{From: to.Add(-time.Duration(days) * 24 * time.Hour), To: to})
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	if err := respond.JSON(writer, request, http.StatusOK, result); err != nil {
		routes.logger.ErrorContext(request.Context(), "write API request analytics response")
	}
}

type requestLogResource struct {
	RequestID            string          `json:"request_id"`
	ActorKeyID           string          `json:"actor_key_id"`
	Method               string          `json:"method"`
	RouteTemplate        string          `json:"route_template"`
	StatusCode           int             `json:"status_code"`
	DurationMilliseconds int64           `json:"duration_ms"`
	OccurredAt           string          `json:"occurred_at"`
	TraceReference       string          `json:"trace_reference,omitempty"`
	ClientIPAddress      string          `json:"client_ip_address"`
	QueryParameters      json.RawMessage `json:"query_parameters"`
	BodyParameters       json.RawMessage `json:"body_parameters,omitempty"`
}

func projectRequestLogResource(record requestlog.Record) requestLogResource {
	return requestLogResource{
		RequestID: record.RequestID, ActorKeyID: record.ActorKeyID, Method: record.Method,
		RouteTemplate: record.RouteTemplate, StatusCode: record.StatusCode,
		DurationMilliseconds: record.DurationMilliseconds, OccurredAt: record.OccurredAt.Format(time.RFC3339Nano),
		TraceReference: record.TraceReference, ClientIPAddress: record.ClientIPAddress,
		QueryParameters: record.QueryParameters, BodyParameters: record.BodyParameters,
	}
}

func (routes *RequestLogRoutes) get(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		writeAccessProblem(writer, request, routes.logger, access.ErrInvalidCredential, true)
		return
	}
	record, err := routes.reader.Get(request.Context(), authority, chi.URLParam(request, "requestID"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	if err := respond.JSON(writer, request, http.StatusOK, projectRequestLogResource(record)); err != nil {
		routes.logger.ErrorContext(request.Context(), "write API request log response")
	}
}

func (routes *RequestLogRoutes) list(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	for key, values := range query {
		if (key != "before" && key != "limit") || len(values) != 1 {
			routes.problem(writer, request, invalidRequest(errors.New("request-log query is invalid")))
			return
		}
	}
	limit := 50
	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			routes.problem(writer, request, invalidRequest(errors.New("request-log limit must be from 1 to 100")))
			return
		}
		limit = parsed
	}
	var before requestlog.Cursor
	if value := query.Get("before"); value != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		parts := strings.SplitN(string(decoded), "|", 2)
		if err != nil || len(parts) != 2 {
			routes.problem(writer, request, invalidRequest(errors.New("request-log cursor is invalid")))
			return
		}
		parsed, err := time.Parse(time.RFC3339Nano, parts[0])
		if err != nil || parts[1] == "" {
			routes.problem(writer, request, invalidRequest(errors.New("request-log cursor is invalid")))
			return
		}
		before = requestlog.Cursor{OccurredAt: parsed.UTC(), RequestID: parts[1]}
	}
	authority, ok := AccessContext(request.Context())
	if !ok {
		writeAccessProblem(writer, request, routes.logger, access.ErrInvalidCredential, true)
		return
	}
	records, err := routes.reader.List(request.Context(), authority, before, limit)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	data := make([]requestLogResource, 0, len(records))
	for _, record := range records {
		data = append(data, projectRequestLogResource(record))
	}
	response := struct {
		Data       []requestLogResource `json:"data"`
		NextBefore *string              `json:"next_before,omitempty"`
	}{Data: data}
	if len(records) == limit {
		last := records[len(records)-1]
		value := base64.RawURLEncoding.EncodeToString([]byte(last.OccurredAt.Format(time.RFC3339Nano) + "|" + last.RequestID))
		response.NextBefore = &value
	}
	if err := respond.JSON(writer, request, http.StatusOK, response); err != nil {
		routes.logger.ErrorContext(request.Context(), "write API request logs response")
	}
}

func (routes *RequestLogRoutes) problem(writer http.ResponseWriter, request *http.Request, err error) {
	if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
		routes.logger.ErrorContext(request.Context(), "write API request logs failure response")
	}
}
