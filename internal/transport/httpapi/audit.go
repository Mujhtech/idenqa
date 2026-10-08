package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/audit"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

type auditReader interface {
	List(context.Context, access.Context, uint64, int) ([]audit.Record, error)
}

// AuditRoutes exposes reference-only immutable tenant audit metadata.
type AuditRoutes struct {
	access *AccessMiddleware
	reader auditReader
	logger *slog.Logger
}

// NewAuditRoutes constructs routes for tenant audit-record reads.
func NewAuditRoutes(accessMiddleware *AccessMiddleware, reader auditReader, logger *slog.Logger) (*AuditRoutes, error) {
	if accessMiddleware == nil || reader == nil || logger == nil {
		return nil, errors.New("audit route dependencies are required")
	}
	return &AuditRoutes{access: accessMiddleware, reader: reader, logger: logger}, nil
}

// Register mounts the audit-record endpoint on router.
func (routes *AuditRoutes) Register(router chi.Router) {
	router.With(routes.access.Authorize(access.PermissionAuditExport)).Get("/audit-records", routes.list)
}

func (routes *AuditRoutes) list(writer http.ResponseWriter, request *http.Request) {
	query := request.URL.Query()
	for key, values := range query {
		if (key != "before" && key != "limit") || len(values) != 1 {
			routes.problem(writer, request, invalidRequest(errors.New("audit query is invalid")))
			return
		}
	}
	limit := 50
	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			routes.problem(writer, request, invalidRequest(errors.New("audit limit must be from 1 to 100")))
			return
		}
		limit = parsed
	}
	var before uint64
	if value := query.Get("before"); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 64)
		if err != nil || parsed == 0 {
			routes.problem(writer, request, invalidRequest(errors.New("audit cursor is invalid")))
			return
		}
		before = parsed
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
	type resource struct {
		Sequence    uint64 `json:"sequence"`
		EventID     string `json:"event_id"`
		EventType   string `json:"event_type"`
		AggregateID string `json:"aggregate_id"`
		ActorID     string `json:"actor_id"`
		OccurredAt  string `json:"occurred_at"`
	}
	data := make([]resource, 0, len(records))
	for _, record := range records {
		data = append(data, resource{record.Sequence, record.EventID, record.EventType, record.AggregateID, record.ActorID, record.OccurredAt.Format("2006-01-02T15:04:05.999999999Z07:00")})
	}
	response := struct {
		Data       []resource `json:"data"`
		NextBefore *string    `json:"next_before,omitempty"`
	}{Data: data}
	if len(records) == limit {
		value := strconv.FormatUint(records[len(records)-1].Sequence, 10)
		response.NextBefore = &value
	}
	if err := respond.JSON(writer, request, http.StatusOK, response); err != nil {
		routes.logger.ErrorContext(request.Context(), "write audit records response")
	}
}

func (routes *AuditRoutes) problem(writer http.ResponseWriter, request *http.Request, err error) {
	if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
		routes.logger.ErrorContext(request.Context(), "write audit records failure response")
	}
}
