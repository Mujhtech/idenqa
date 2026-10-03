package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/go-chi/chi/v5"
)

// AccessCatalogRoutes exposes the exact tenant-assignable permission catalog.
type AccessCatalogRoutes struct {
	handlerBase
	access   *AccessMiddleware
	registry access.Registry
}

// NewAccessCatalogRoutes constructs the authenticated access-catalog route.
func NewAccessCatalogRoutes(middleware *AccessMiddleware, registry access.Registry, logger *slog.Logger) (*AccessCatalogRoutes, error) {
	if middleware == nil || logger == nil || len(registry.Permissions()) == 0 {
		return nil, errors.New("access catalog route dependencies are required")
	}
	return &AccessCatalogRoutes{handlerBase: newHandlerBase(logger, "access_catalog"), access: middleware, registry: registry}, nil
}

// Register mounts the versioned permission catalog.
func (routes *AccessCatalogRoutes) Register(router chi.Router) {
	router.With(routes.access.Authorize(access.PermissionTenantRead)).Get("/access/permissions", routes.list)
}

type permissionCatalogEntry struct {
	ID       string `json:"id"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

func (routes *AccessCatalogRoutes) list(writer http.ResponseWriter, request *http.Request) {
	permissions := routes.registry.Permissions()
	entries := make([]permissionCatalogEntry, 0, len(permissions))
	for _, permission := range permissions {
		resource, action, _ := strings.Cut(string(permission), ":")
		entries = append(entries, permissionCatalogEntry{ID: string(permission), Resource: resource, Action: action})
	}
	routes.reply(writer, request, struct {
		Version string                   `json:"version"`
		Data    []permissionCatalogEntry `json:"data"`
	}{Version: "idenqa.core/access-permissions/v1", Data: entries}, nil)
}
