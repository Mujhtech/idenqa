package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/reviewbrowser"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/go-chi/chi/v5"
)

// ReviewBrowserRoutes serves direct case-bound evidence operations without
// converting a workforce actor into an API-key principal.
type ReviewBrowserRoutes struct {
	base     handlerBase
	sessions *reviewbrowser.Service
	evidence *review.EvidenceService
	reviews  *review.Service
}

// NewReviewBrowserRoutes constructs case-bound browser evidence routes.
func NewReviewBrowserRoutes(sessions *reviewbrowser.Service, evidence *review.EvidenceService, reviews *review.Service, logger *slog.Logger) (*ReviewBrowserRoutes, error) {
	if sessions == nil || evidence == nil || reviews == nil || logger == nil {
		return nil, reviewbrowser.ErrInvalid
	}
	return &ReviewBrowserRoutes{base: newHandlerBase(logger, "review_browser"), sessions: sessions, evidence: evidence, reviews: reviews}, nil
}

// Register mounts review-browser bootstrap and evidence endpoints.
func (routes *ReviewBrowserRoutes) Register(router chi.Router) {
	router.Post("/review-evidence-sessions/redeem", routes.redeem)
	router.Get("/review-evidence-sessions/current/evidence", routes.list)
	router.Post("/review-evidence-sessions/current/evidence-grants", routes.issue)
	router.Post("/review-evidence-sessions/current/evidence-grants/{grantID}/content", routes.read)
	router.Post("/review-evidence-sessions/current/findings", routes.finding)
}

func (routes *ReviewBrowserRoutes) redeem(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store, private")
	origin := request.Header.Get("Origin")
	payload, err := decodeJSONBody[struct {
		Authority reviewbrowser.Envelope `json:"authority"`
	}](request)
	if err != nil {
		routes.base.problem(writer, request, invalidRequest(err))
		return
	}
	result, err := routes.sessions.Redeem(request.Context(), payload.Authority, origin)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.base.writeJSON(writer, request, http.StatusCreated, result)
}

func (routes *ReviewBrowserRoutes) list(writer http.ResponseWriter, request *http.Request) {
	session, ok := routes.authorize(writer, request)
	if !ok {
		return
	}
	ctx := reviewbrowser.WithSession(request.Context(), session)
	items, err := routes.evidence.ListDelegated(ctx, session.Scope, session.Actor, session.CaseID, session.Version)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.base.writeJSON(writer, request, http.StatusOK, struct {
		Items []review.EvidenceMetadata `json:"items"`
	}{items})
}

func (routes *ReviewBrowserRoutes) issue(writer http.ResponseWriter, request *http.Request) {
	session, ok := routes.authorize(writer, request)
	if !ok {
		return
	}
	payload, err := decodeJSONBody[struct {
		EvidenceID string `json:"evidenceId"`
	}](request)
	if err != nil {
		routes.base.problem(writer, request, invalidRequest(err))
		return
	}
	evidenceID, err := id.ParseEvidence(payload.EvidenceID)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.base.problem(writer, request, err)
		return
	}
	ctx := reviewbrowser.WithSession(request.Context(), session)
	result, err := routes.evidence.IssueDelegated(ctx, session.Scope, session.Actor, session.CaseID, session.Version, evidenceID, key)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.base.writeJSON(writer, request, http.StatusCreated, struct {
		GrantID    string    `json:"grantId"`
		EvidenceID string    `json:"evidenceId"`
		ExpiresAt  time.Time `json:"expiresAt"`
	}{result.GrantID.String(), result.EvidenceID.String(), result.ExpiresAt})
}

func (routes *ReviewBrowserRoutes) read(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store, private")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	writer.Header().Set("Referrer-Policy", "no-referrer")
	session, ok := routes.authorize(writer, request)
	if !ok {
		return
	}
	grantID, err := id.ParseGrant(chi.URLParam(request, "grantID"))
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	ctx, cancel := context.WithTimeout(reviewbrowser.WithSession(request.Context(), session), 30*time.Second)
	defer cancel()
	buffer := &reviewDisplayBuffer{}
	defer func() { clear(buffer.output.Bytes()); buffer.output.Reset() }()
	if err := routes.evidence.ReadDelegated(ctx, session.Scope, session.Actor, session.CaseID, session.Version, grantID, buffer); err != nil || buffer.output.Len() == 0 {
		routes.problem(writer, request, review.ErrForbidden)
		return
	}
	writer.Header().Set("Content-Type", "image/png")
	writer.Header().Set("Content-Disposition", "inline; filename=review.png")
	if _, err := writer.Write(buffer.output.Bytes()); err != nil {
		routes.base.logger.ErrorContext(ctx, "write review browser evidence")
	}
}

func (routes *ReviewBrowserRoutes) finding(writer http.ResponseWriter, request *http.Request) {
	session, ok := routes.authorize(writer, request)
	if !ok {
		return
	}
	payload, err := decodeJSONBody[struct {
		Resolution       review.Resolution `json:"resolution"`
		ReasonCode       string            `json:"reasonCode"`
		EvidenceGrantIDs []string          `json:"evidenceGrantIds"`
	}](request)
	if err != nil {
		routes.base.problem(writer, request, invalidRequest(err))
		return
	}
	grantIDs := make([]id.Grant, 0, len(payload.EvidenceGrantIDs))
	for _, value := range payload.EvidenceGrantIDs {
		grantID, err := id.ParseGrant(value)
		if err != nil {
			routes.problem(writer, request, review.ErrInvalid)
			return
		}
		grantIDs = append(grantIDs, grantID)
	}
	ctx := reviewbrowser.WithSession(request.Context(), session)
	value, err := routes.reviews.SubmitFinding(ctx, session.Scope, session.Actor, session.CaseID, payload.Resolution, payload.ReasonCode, grantIDs, session.Version)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.base.writeJSON(writer, request, http.StatusOK, struct {
		CaseID  string `json:"caseId"`
		State   string `json:"state"`
		Version int64  `json:"version"`
	}{value.ID.String(), string(value.State), value.Version})
}

func (routes *ReviewBrowserRoutes) authorize(writer http.ResponseWriter, request *http.Request) (reviewbrowser.Session, bool) {
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") || strings.Count(header, " ") != 1 {
		routes.base.problem(writer, request, access.ErrInvalidCredential)
		return reviewbrowser.Session{}, false
	}
	session, err := routes.sessions.Authenticate(request.Context(), strings.TrimPrefix(header, "Bearer "), request.Header.Get("Origin"))
	if err != nil {
		routes.problem(writer, request, err)
		return reviewbrowser.Session{}, false
	}
	return session, true
}

func (routes *ReviewBrowserRoutes) problem(writer http.ResponseWriter, request *http.Request, err error) {
	routes.base.problem(writer, request, reviewBrowserProblem(err))
}

func reviewBrowserProblem(err error) error {
	switch {
	case errors.Is(err, reviewbrowser.ErrReplay):
		return apierror.New(http.StatusConflict, apierror.CodeReviewSessionReplayed, "Session already redeemed", "This single-use review evidence authority was already redeemed.", err)
	case errors.Is(err, reviewbrowser.ErrExpired):
		return apierror.New(http.StatusUnauthorized, apierror.CodeReviewSessionExpired, "Session expired", "The review evidence session expired.", err)
	case errors.Is(err, review.ErrConflict):
		return apierror.New(http.StatusPreconditionFailed, apierror.CodeReviewCaseStale, "Review case changed", "The review case changed after this evidence session was authorised.", err)
	case errors.Is(err, review.ErrForbidden):
		return apierror.New(http.StatusForbidden, apierror.CodeReviewAuthorityRevoked, "Review authority revoked", "The reviewer is no longer authorised for this case.", err)
	case errors.Is(err, reviewbrowser.ErrForbidden), errors.Is(err, reviewbrowser.ErrInvalid):
		return access.ErrInvalidCredential
	default:
		return err
	}
}
