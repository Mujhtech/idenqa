package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/go-chi/chi/v5"
)

// ReviewFollowupRoutes exposes policy-controlled review follow-up.
type ReviewFollowupRoutes struct {
	base    *ReviewRoutes
	service *review.FollowupService
}

// NewReviewFollowupRoutes constructs policy follow-up endpoints.
func NewReviewFollowupRoutes(auth *AccessMiddleware, service *review.FollowupService, logger *slog.Logger) (*ReviewFollowupRoutes, error) {
	if auth == nil || service == nil || logger == nil {
		return nil, review.ErrInvalid
	}
	return &ReviewFollowupRoutes{&ReviewRoutes{access: auth, logger: logger}, service}, nil
}

// Register mounts authenticated review endpoints.
func (r *ReviewFollowupRoutes) Register(router chi.Router) {
	router.With(r.base.access.Authenticate, r.base.access.Require(access.PermissionAppealsWrite)).Post("/decisions/{decisionID}/review-cases", r.intake)
	mw := []func(http.Handler) http.Handler{r.base.access.Authenticate, r.base.access.Require(access.PermissionReviewsWrite)}
	router.With(mw...).Post("/review-cases/{caseID}/arbitrations", func(w http.ResponseWriter, req *http.Request) { r.execute(w, req, false) })
	router.With(mw...).Post("/review-cases/{caseID}/corrections/evaluate", func(w http.ResponseWriter, req *http.Request) { r.execute(w, req, true) })
}
func (r *ReviewFollowupRoutes) execute(w http.ResponseWriter, req *http.Request, correction bool) {
	auth, _, ok := r.base.authority(req)
	if !ok {
		r.base.problem(w, req, access.ErrInvalidCredential)
		return
	}
	caseID, err := r.base.caseID(req)
	if err != nil {
		r.base.problem(w, req, review.ErrInvalid)
		return
	}
	var version int64
	var resolution review.Resolution
	var reason string
	if correction {
		input, err := decodeJSONBody[reviewerRequest](req)
		if err != nil {
			r.base.problem(w, req, invalidRequest(err))
			return
		}
		version = input.ExpectedVersion
	} else {
		input, err := decodeJSONBody[struct {
			ExpectedVersion int64             `json:"expected_version"`
			Resolution      review.Resolution `json:"resolution"`
			Reason          string            `json:"reason_code"`
		}](req)
		if err != nil {
			r.base.problem(w, req, invalidRequest(err))
			return
		}
		version, resolution, reason = input.ExpectedVersion, input.Resolution, input.Reason
	}
	key, err := parseIdempotencyKey(req.Header.Values("Idempotency-Key"))
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	result, err := r.service.Execute(req.Context(), auth, caseID, version, resolution, reason, key, correction)
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	r.base.write(w, req, http.StatusOK, result)
}

func (r *ReviewFollowupRoutes) intake(w http.ResponseWriter, req *http.Request) {
	auth, _, ok := r.base.authority(req)
	if !ok {
		r.base.problem(w, req, access.ErrInvalidCredential)
		return
	}
	decisionID, err := id.ParseDecision(chi.URLParam(req, "decisionID"))
	if err != nil {
		r.base.problem(w, req, review.ErrInvalid)
		return
	}
	key, err := parseIdempotencyKey(req.Header.Values("Idempotency-Key"))
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	result, err := r.service.Intake(req.Context(), auth, decisionID, key)
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	r.base.write(w, req, http.StatusCreated, result)
}
