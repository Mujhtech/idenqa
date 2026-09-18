package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/access"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// TenantReader is the application capability consumed by the HTTP route.
type TenantReader interface {
	Current(context.Context, access.Context) (tenant.Tenant, error)
}

// TenantRoutes adapts the authenticated tenant read use case to HTTP.
type TenantRoutes struct {
	access *AccessMiddleware
	reader TenantReader
	logger *slog.Logger
}

// NewTenantRoutes constructs the protected tenant HTTP surface.
func NewTenantRoutes(
	accessMiddleware *AccessMiddleware,
	reader TenantReader,
	logger *slog.Logger,
) (*TenantRoutes, error) {
	if accessMiddleware == nil || reader == nil || logger == nil {
		return nil, errors.New("tenant route dependencies are required")
	}

	return &TenantRoutes{access: accessMiddleware, reader: reader, logger: logger}, nil
}

// Register adds the authenticated tenant metadata route.
func (routes *TenantRoutes) Register(router chi.Router) {
	router.With(
		routes.access.Authenticate,
		routes.access.Require(access.PermissionTenantRead),
	).Get("/tenant", routes.current)
}

func (routes *TenantRoutes) current(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		writeAccessProblem(writer, request, routes.logger, access.ErrInvalidCredential, true)

		return
	}
	value, err := routes.reader.Current(request.Context(), authority)
	if err != nil {
		if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
			routes.logger.ErrorContext(request.Context(), "write tenant read failure response")
		}

		return
	}
	resource := openapiv1.Tenant{
		ID:        value.ID().String(),
		State:     openapiv1.TenantState(value.State()),
		CreatedAt: value.CreatedAt(),
		UpdatedAt: value.UpdatedAt(),
	}
	writer.Header().Set("ETag", strconv.Quote(strconv.FormatInt(value.Version(), 10)))
	if err := respond.JSON(writer, request, http.StatusOK, resource); err != nil {
		routes.logger.ErrorContext(request.Context(), "write tenant response")
	}
}
