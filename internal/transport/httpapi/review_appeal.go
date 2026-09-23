package httpapi

import (
	"context"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/go-chi/chi/v5"
)

type appealLifecycle interface {
	WithdrawAppeal(context.Context, access.Context, id.Appeal, int64) (review.Appeal, error)
	ReadAppeal(context.Context, access.Context, id.Appeal) (review.Appeal, error)
}

func (routes *ReviewRoutes) registerAppealLifecycle(router chi.Router) {
	service, ok := routes.service.(appealLifecycle)
	if !ok {
		return
	}
	router.With(routes.access.Authorize(access.PermissionAppealsWrite)).Post("/appeals/{appealID}/withdraw", func(w http.ResponseWriter, req *http.Request) {
		auth, _, ok := routes.authority(req)
		if !ok {
			routes.problem(w, req, access.ErrInvalidCredential)
			return
		}
		identifier, err := routes.appealID(req)
		if err != nil {
			routes.problem(w, req, review.ErrInvalid)
			return
		}
		body, err := decodeJSONBody[reviewerRequest](req)
		if err != nil {
			routes.problem(w, req, invalidRequest(err))
			return
		}
		value, err := service.WithdrawAppeal(req.Context(), auth, identifier, body.ExpectedVersion)
		if err != nil {
			routes.problem(w, req, err)
			return
		}
		routes.writeAppeal(w, req, http.StatusOK, value)
	})
	router.With(routes.access.Authorize(access.PermissionReviewsRead)).Get("/appeals/{appealID}", func(w http.ResponseWriter, req *http.Request) {
		auth, _, ok := routes.authority(req)
		if !ok {
			routes.problem(w, req, access.ErrInvalidCredential)
			return
		}
		identifier, err := routes.appealID(req)
		if err != nil {
			routes.problem(w, req, review.ErrInvalid)
			return
		}
		value, err := service.ReadAppeal(req.Context(), auth, identifier)
		if err != nil {
			routes.problem(w, req, err)
			return
		}
		routes.writeAppeal(w, req, http.StatusOK, value)
	})
}
