package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// ModelRoutes exposes core evaluation registry operations to tenant API keys.
type ModelRoutes struct {
	access  *AccessMiddleware
	service *model.Management
	logger  *slog.Logger
}

// NewModelRoutes composes the authenticated registry surface.
func NewModelRoutes(middleware *AccessMiddleware, service *model.Management, logger *slog.Logger) (*ModelRoutes, error) {
	if middleware == nil || service == nil || logger == nil {
		return nil, model.ErrRegistryInvalid
	}
	return &ModelRoutes{middleware, service, logger}, nil
}

// Register adds closed model commands and immutable revision/history inspection.
func (routes *ModelRoutes) Register(router chi.Router) {
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionModelsRead)).Get("/models/{modelName}", routes.get)
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionModelsRead)).Get("/models/{modelName}/revisions/{kind}/{revision}", routes.revision)
	router.With(routes.access.Authenticate, routes.access.Require(access.PermissionModelsRead)).Get("/models/{modelName}/history", routes.history)
	for _, operation := range []string{"register", "threshold", "activate", "rollback", "retire"} {
		permission := access.PermissionModelsWrite
		if operation == "activate" || operation == "rollback" || operation == "retire" {
			permission = access.PermissionModelsActivate
		}
		router.With(routes.access.Authenticate, routes.access.Require(permission)).Post("/models/{modelName}/"+operation, func(w http.ResponseWriter, r *http.Request) { routes.mutate(w, r, operation) })
	}
}
func (routes *ModelRoutes) mutate(w http.ResponseWriter, r *http.Request, operation string) {
	key, err := parseIdempotencyKey(r.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.reply(w, r, nil, err)
		return
	}
	command := model.RegistryCommand{Name: chi.URLParam(r, "modelName"), Operation: operation}
	switch operation {
	case "register":
		body, e := decodePolicyJSON[struct {
			ExpectedVersion int64              `json:"expected_version"`
			Reason          string             `json:"reason"`
			Registration    model.Registration `json:"registration"`
		}](r)
		err = e
		command.ExpectedVersion, command.Reason, command.Registration = body.ExpectedVersion, body.Reason, &body.Registration
	case "threshold":
		body, e := decodePolicyJSON[struct {
			ExpectedVersion int64              `json:"expected_version"`
			Reason          string             `json:"reason"`
			Thresholds      model.ThresholdSet `json:"thresholds"`
		}](r)
		err = e
		command.ExpectedVersion, command.Reason, command.Thresholds = body.ExpectedVersion, body.Reason, &body.Thresholds
	case "activate", "rollback":
		body, e := decodePolicyJSON[struct {
			ExpectedVersion int64            `json:"expected_version"`
			Reason          string           `json:"reason"`
			Deployment      model.Deployment `json:"deployment"`
		}](r)
		err = e
		command.ExpectedVersion, command.Reason, command.Deployment = body.ExpectedVersion, body.Reason, &body.Deployment
	case "retire":
		body, e := decodePolicyJSON[struct {
			ExpectedVersion int64  `json:"expected_version"`
			Reason          string `json:"reason"`
		}](r)
		err = e
		command.ExpectedVersion, command.Reason = body.ExpectedVersion, body.Reason
	}
	if err != nil {
		routes.reply(w, r, nil, invalidRequest(err))
		return
	}
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.Execute(r.Context(), actor, key, command)
	routes.reply(w, r, result, err)
}
func (routes *ModelRoutes) get(w http.ResponseWriter, r *http.Request) {
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.Get(r.Context(), actor, chi.URLParam(r, "modelName"))
	routes.reply(w, r, result, err)
}
func (routes *ModelRoutes) revision(w http.ResponseWriter, r *http.Request) {
	number, err := strconv.ParseInt(chi.URLParam(r, "revision"), 10, 64)
	if err != nil {
		routes.reply(w, r, nil, model.ErrRegistryNotFound)
		return
	}
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.Revision(r.Context(), actor, chi.URLParam(r, "modelName"), chi.URLParam(r, "kind"), number)
	routes.reply(w, r, result, err)
}
func (routes *ModelRoutes) history(w http.ResponseWriter, r *http.Request) {
	before := int64(0)
	limit := 50
	for key, values := range r.URL.Query() {
		if len(values) != 1 || (key != "before" && key != "limit") {
			routes.reply(w, r, nil, model.ErrRegistryInvalid)
			return
		}
		number, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil {
			routes.reply(w, r, nil, model.ErrRegistryInvalid)
			return
		}
		if key == "before" {
			before = number
		} else {
			limit = int(number)
		}
	}
	actor, _ := AccessContext(r.Context())
	result, err := routes.service.History(r.Context(), actor, chi.URLParam(r, "modelName"), before, limit)
	routes.reply(w, r, result, err)
}
func (routes *ModelRoutes) reply(w http.ResponseWriter, r *http.Request, value any, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		switch {
		case errors.Is(err, model.ErrRegistryInvalid):
			err = invalidRequest(err)
		case errors.Is(err, model.ErrRegistryNotFound):
			err = apierror.New(404, apierror.CodeNotFound, "Not found", "The model resource was not found.", err)
		case errors.Is(err, model.ErrRegistryConflict):
			err = apierror.New(409, apierror.CodeConflict, "Conflict", "The model registry version conflicts with current state.", err)
		}
		if e := respond.WriteProblem(w, r, err, requestIDString(r.Context())); e != nil {
			routes.logger.ErrorContext(r.Context(), "write model registry problem")
		}
		return
	}
	if e := respond.JSON(w, r, 200, value); e != nil {
		routes.logger.ErrorContext(r.Context(), "write model registry response")
	}
}
