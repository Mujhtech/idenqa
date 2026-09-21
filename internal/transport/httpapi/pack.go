package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/pack"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// PackRoutes exposes immutable pack inspection and the support-level
// projection. It never activates, deprecates, or retires a pack.
type PackRoutes struct {
	access   *AccessMiddleware
	registry *pack.Registry
	logger   *slog.Logger
}

// NewPackRoutes constructs authenticated read-only pack routes.
func NewPackRoutes(middleware *AccessMiddleware, registry *pack.Registry, logger *slog.Logger) (*PackRoutes, error) {
	if middleware == nil || registry == nil || logger == nil {
		return nil, pack.ErrInvalid
	}
	return &PackRoutes{access: middleware, registry: registry, logger: logger}, nil
}

// Register mounts the public pack inspection routes.
func (routes *PackRoutes) Register(router chi.Router) {
	read := router.With(routes.access.Authenticate, routes.access.Require(access.PermissionPacksRead))
	read.Get("/packs", routes.list)
	read.Get("/packs/{country}", routes.country)
	read.Get("/packs/{country}/revisions/{revision}", routes.revision)
	read.Get("/document-support", routes.support)
}

func (routes *PackRoutes) list(writer http.ResponseWriter, request *http.Request) {
	summaries := routes.registry.List()
	if summaries == nil {
		summaries = []pack.Summary{}
	}
	routes.reply(writer, request, struct {
		Data []pack.Summary `json:"data"`
		Page struct {
			HasMore bool `json:"has_more"`
		} `json:"page"`
	}{Data: summaries}, nil)
}

func (routes *PackRoutes) country(writer http.ResponseWriter, request *http.Request) {
	entry, err := routes.registry.Active(chi.URLParam(request, "country"))
	if err != nil {
		routes.reply(writer, request, nil, err)
		return
	}
	routes.reply(writer, request, packEntryResponse(entry), nil)
}

func (routes *PackRoutes) revision(writer http.ResponseWriter, request *http.Request) {
	number, err := strconv.ParseUint(chi.URLParam(request, "revision"), 10, 32)
	if err != nil || number == 0 {
		routes.reply(writer, request, nil, invalidRequest(errors.New("a positive pack revision is required")))
		return
	}
	entry, err := routes.registry.Get(chi.URLParam(request, "country"), uint32(number))
	if err != nil {
		routes.reply(writer, request, nil, err)
		return
	}
	routes.reply(writer, request, packEntryResponse(entry), nil)
}

func (routes *PackRoutes) support(writer http.ResponseWriter, request *http.Request) {
	country := request.URL.Query().Get("country")
	documentType := request.URL.Query().Get("type")
	if country == "" || documentType == "" {
		routes.reply(writer, request, nil, invalidRequest(errors.New("country and type are required")))
		return
	}
	projection, ok := routes.registry.Support(country, documentType)
	if !ok {
		routes.reply(writer, request, nil, pack.ErrNotFound)
		return
	}
	routes.reply(writer, request, projection, nil)
}

type packEntryResponseValue struct {
	Pack           pack.Pack           `json:"pack"`
	LifecycleState pack.LifecycleState `json:"lifecycle_state"`
	Version        int64               `json:"version"`
	UpdatedAt      *time.Time          `json:"updated_at,omitempty"`
}

func packEntryResponse(entry pack.Entry) packEntryResponseValue {
	response := packEntryResponseValue{
		Pack: entry.Pack, LifecycleState: entry.State, Version: entry.Version,
	}
	if !entry.UpdatedAt.IsZero() {
		updated := entry.UpdatedAt.UTC()
		response.UpdatedAt = &updated
	}
	return response
}

func (routes *PackRoutes) reply(writer http.ResponseWriter, request *http.Request, value any, err error) {
	writer.Header().Set("Cache-Control", "no-store")
	if err != nil {
		switch {
		case errors.Is(err, pack.ErrNotFound):
			err = apierror.New(404, apierror.CodeNotFound, "Not found", "The pack resource was not found.", err)
		case errors.Is(err, pack.ErrInvalid), errors.Is(err, pack.ErrConflict):
			err = invalidRequest(err)
		}
		if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
			routes.logger.ErrorContext(request.Context(), "write pack problem")
		}
		return
	}
	if writeErr := respond.JSON(writer, request, http.StatusOK, value); writeErr != nil {
		routes.logger.ErrorContext(request.Context(), "write pack response")
	}
}
