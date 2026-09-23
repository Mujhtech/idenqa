package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/go-chi/chi/v5"
)

// ReviewManagementRoutes exposes versioned tenant review administration.
type ReviewManagementRoutes struct {
	base    *ReviewRoutes
	service *review.Management
}

// NewReviewManagementRoutes constructs tenant review administration endpoints.
func NewReviewManagementRoutes(auth *AccessMiddleware, service *review.Management, logger *slog.Logger) (*ReviewManagementRoutes, error) {
	if auth == nil || service == nil || logger == nil {
		return nil, review.ErrInvalid
	}
	return &ReviewManagementRoutes{
		base:    &ReviewRoutes{handlerBase: newHandlerBase(logger, "review"), access: auth},
		service: service,
	}, nil
}

// Register mounts authenticated review endpoints.
func (r *ReviewManagementRoutes) Register(router chi.Router) {
	mw := []func(http.Handler) http.Handler{r.base.access.Authorize(access.PermissionReviewsAdmin)}
	router.With(mw...).Put("/review-cases/{caseID}/queue", r.queue)
	router.With(mw...).Put("/review-cases/{caseID}/settings", r.bindCase)

	router.With(mw...).Put("/review-operators/{keyID}", r.operator)
	router.With(mw...).Put("/review-policies/{policyID}/revisions/{revision}", r.policy)
	router.With(mw...).Get("/review-operators/{keyID}", func(w http.ResponseWriter, req *http.Request) { r.read(w, req, "operator", chi.URLParam(req, "keyID")) })
	router.With(mw...).Get("/review-policies/{policyID}/revisions/{revision}", func(w http.ResponseWriter, req *http.Request) {
		r.read(w, req, "policy", chi.URLParam(req, "policyID")+"/"+chi.URLParam(req, "revision"))
	})
}
func (r *ReviewManagementRoutes) operator(w http.ResponseWriter, req *http.Request) {
	auth, _, ok := r.base.authority(req)
	if !ok {
		r.base.problem(w, req, access.ErrInvalidCredential)
		return
	}
	keyID, err := id.ParseAPIKey(chi.URLParam(req, "keyID"))
	if err != nil {
		r.base.problem(w, req, review.ErrInvalid)
		return
	}
	input, err := decodeJSONBody[struct {
		ExpectedVersion int64                        `json:"expected_version"`
		Configuration   review.OperatorConfiguration `json:"configuration"`
	}](req)
	if err != nil {
		r.base.problem(w, req, invalidRequest(err))
		return
	}
	key, err := parseIdempotencyKey(req.Header.Values("Idempotency-Key"))
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	result, err := r.service.PutOperator(req.Context(), auth, keyID, input.Configuration, input.ExpectedVersion, key)
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	r.base.write(w, req, http.StatusOK, result)
}
func (r *ReviewManagementRoutes) policy(w http.ResponseWriter, req *http.Request) {
	auth, _, ok := r.base.authority(req)
	if !ok {
		r.base.problem(w, req, access.ErrInvalidCredential)
		return
	}
	input, err := decodeJSONBody[struct {
		ExpectedVersion int64                 `json:"expected_version"`
		Configuration   review.PolicySettings `json:"configuration"`
	}](req)
	if err != nil {
		r.base.problem(w, req, invalidRequest(err))
		return
	}
	version, err := strconv.ParseUint(chi.URLParam(req, "revision"), 10, 32)
	if err != nil || version == 0 || input.Configuration.PolicyID != chi.URLParam(req, "policyID") || input.Configuration.Revision != uint32(version) {
		r.base.problem(w, req, review.ErrInvalid)
		return
	}
	key, err := parseIdempotencyKey(req.Header.Values("Idempotency-Key"))
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	result, err := r.service.PutPolicy(req.Context(), auth, input.Configuration, input.ExpectedVersion, key)
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	r.base.write(w, req, http.StatusOK, result)
}
func (r *ReviewManagementRoutes) read(w http.ResponseWriter, req *http.Request, kind, reference string) {
	auth, _, ok := r.base.authority(req)
	if !ok {
		r.base.problem(w, req, access.ErrInvalidCredential)
		return
	}
	result, err := r.service.Read(req.Context(), auth, kind, reference)
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	r.base.write(w, req, http.StatusOK, result)
}

func (r *ReviewManagementRoutes) queue(w http.ResponseWriter, req *http.Request) {
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
	input, err := decodeJSONBody[struct {
		ExpectedVersion int64                     `json:"expected_version"`
		Configuration   review.QueueConfiguration `json:"configuration"`
	}](req)
	if err != nil {
		r.base.problem(w, req, invalidRequest(err))
		return
	}
	key, err := parseIdempotencyKey(req.Header.Values("Idempotency-Key"))
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	result, err := r.service.PutQueue(req.Context(), auth, caseID, input.Configuration, input.ExpectedVersion, key)
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	r.base.write(w, req, http.StatusOK, result)
}
func (r *ReviewManagementRoutes) bindCase(w http.ResponseWriter, req *http.Request) {
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
	input, err := decodeJSONBody[review.PolicySettings](req)
	if err != nil {
		r.base.problem(w, req, invalidRequest(err))
		return
	}
	key, err := parseIdempotencyKey(req.Header.Values("Idempotency-Key"))
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	result, err := r.service.BindCase(req.Context(), auth, caseID, input, key)
	if err != nil {
		r.base.problem(w, req, err)
		return
	}
	r.base.write(w, req, http.StatusOK, result)
}
