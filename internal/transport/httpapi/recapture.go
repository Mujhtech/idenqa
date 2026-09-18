package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// RecaptureService is the application boundary for review-linked session creation.
type RecaptureService interface {
	Create(context.Context, access.Context, id.ReviewCase, int64, string) (verification.CreatedSession, error)
}

// WithRecapture adds the optional internal recapture route to existing review routes.
func (routes *ReviewRoutes) WithRecapture(service RecaptureService) *ReviewRoutes {
	result := *routes
	result.recapture = service
	return &result
}
func (routes *ReviewRoutes) createRecapture(writer http.ResponseWriter, request *http.Request) {
	auth, _, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	caseID, err := routes.caseID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[reviewerRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	result, err := routes.recapture.Create(request.Context(), auth, caseID, body.ExpectedVersion, key)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeRecapture(writer, request, caseID, result, http.StatusCreated)
}
func (routes *ReviewRoutes) writeRecapture(writer http.ResponseWriter, request *http.Request, caseID id.ReviewCase, result verification.CreatedSession, status int) {
	writer.Header().Set("Location", "/v1/verifications/"+result.Session.ID().String())
	routes.write(writer, request, status, struct {
		VerificationID        string    `json:"verification_id"`
		CaseID                string    `json:"case_id"`
		CaptureToken          string    `json:"capture_token"`
		CaptureTokenID        string    `json:"capture_token_id"`
		CaptureTokenExpiresAt time.Time `json:"capture_token_expires_at"`
		OutcomeToken          string    `json:"outcome_token"`
		OutcomeTokenExpiresAt time.Time `json:"outcome_token_expires_at"`
		VerificationExpiresAt time.Time `json:"verification_expires_at"`
	}{
		result.Session.ID().String(), caseID.String(), result.CaptureToken.Reveal(),
		result.Credential.ID().String(), result.Credential.ExpiresAt(),
		result.OutcomeToken.Reveal(), result.OutcomeCredential.ExpiresAt(), result.Session.ExpiresAt(),
	})
}

type recaptureRenewer interface {
	Renew(context.Context, access.Context, id.ReviewCase, int64, id.CaptureToken, string) (verification.CreatedSession, error)
}

func (routes *ReviewRoutes) renewRecapture(writer http.ResponseWriter, request *http.Request) {
	auth, _, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	caseID, err := routes.caseID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[struct {
		ExpectedVersion int64  `json:"expected_version"`
		ExpectedToken   string `json:"expected_capture_token_id"`
	}](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	expected, err := id.ParseCaptureToken(body.ExpectedToken)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	service, ok := routes.recapture.(recaptureRenewer)
	if !ok {
		routes.problem(writer, request, review.ErrForbidden)
		return
	}
	result, err := service.Renew(request.Context(), auth, caseID, body.ExpectedVersion, expected, key)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeRecapture(writer, request, caseID, result, http.StatusOK)
}

type recaptureReader interface {
	List(context.Context, access.Context, id.ReviewCase) ([]review.RecaptureStatus, error)
}

func (routes *ReviewRoutes) listRecaptures(writer http.ResponseWriter, request *http.Request) {
	auth, _, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	caseID, err := routes.caseID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	service, ok := routes.recapture.(recaptureReader)
	if !ok {
		routes.problem(writer, request, review.ErrForbidden)
		return
	}
	statuses, err := service.List(request.Context(), auth, caseID)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	type resource struct {
		CaseVersion      int64                     `json:"case_version"`
		VerificationID   string                    `json:"verification_id"`
		State            verification.SessionState `json:"state"`
		TokenID          string                    `json:"capture_token_id"`
		TokenExpiresAt   time.Time                 `json:"capture_token_expires_at"`
		ExpiresAt        time.Time                 `json:"verification_expires_at"`
		DecisionID       string                    `json:"decision_id,omitempty"`
		Outcome          string                    `json:"outcome,omitempty"`
		AcknowledgedAt   *time.Time                `json:"acknowledged_at,omitempty"`
		AcknowledgedBy   string                    `json:"acknowledged_by,omitempty"`
		FollowUpRequired bool                      `json:"follow_up_required"`
	}
	items := make([]resource, 0, len(statuses))
	for _, item := range statuses {
		items = append(items, resource{item.CaseVersion, item.ChildID.String(), item.State, item.CaptureTokenID.String(), item.CaptureTokenExpiresAt, item.ExpiresAt, item.DecisionID.String(), string(item.Outcome), item.AcknowledgedAt, item.AcknowledgedBy, !item.DecisionID.IsZero() && item.AcknowledgedAt == nil})
	}
	routes.write(writer, request, http.StatusOK, struct {
		Items []resource `json:"items"`
	}{items})
}

type recaptureAcknowledger interface {
	Acknowledge(context.Context, access.Context, id.ReviewCase, int64, id.Decision, string) (review.RecaptureAcknowledgement, error)
}

func (routes *ReviewRoutes) acknowledgeRecapture(writer http.ResponseWriter, request *http.Request) {
	auth, _, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	caseID, err := routes.caseID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[struct {
		ExpectedVersion int64  `json:"expected_version"`
		DecisionID      string `json:"decision_id"`
	}](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	decision, err := id.ParseDecision(body.DecisionID)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	service, ok := routes.recapture.(recaptureAcknowledger)
	if !ok {
		routes.problem(writer, request, review.ErrForbidden)
		return
	}
	result, err := service.Acknowledge(request.Context(), auth, caseID, body.ExpectedVersion, decision, key)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.write(writer, request, http.StatusOK, struct {
		CaseID         string    `json:"case_id"`
		CaseVersion    int64     `json:"case_version"`
		VerificationID string    `json:"verification_id"`
		DecisionID     string    `json:"decision_id"`
		ActorID        string    `json:"actor_key_id"`
		ReviewerID     string    `json:"reviewer_id"`
		At             time.Time `json:"acknowledged_at"`
	}{result.CaseID.String(), result.CaseVersion, result.ChildID.String(), result.DecisionID.String(), result.ActorID.String(), result.ReviewerID, result.RecordedAt})
}

type recaptureReevaluator interface {
	Reevaluate(context.Context, access.Context, id.ReviewCase, int64, id.Decision, string) (review.RecaptureReevaluation, error)
}

func (routes *ReviewRoutes) reevaluateRecapture(writer http.ResponseWriter, request *http.Request) {
	auth, _, ok := routes.authority(request)
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	caseID, err := routes.caseID(request)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	key, err := parseIdempotencyKey(request.Header.Values("Idempotency-Key"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	body, err := decodeJSONBody[struct {
		ExpectedVersion int64  `json:"expected_version"`
		DecisionID      string `json:"decision_id"`
	}](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	decision, err := id.ParseDecision(body.DecisionID)
	if err != nil {
		routes.problem(writer, request, review.ErrInvalid)
		return
	}
	service, ok := routes.recapture.(recaptureReevaluator)
	if !ok {
		routes.problem(writer, request, review.ErrForbidden)
		return
	}
	result, err := service.Reevaluate(request.Context(), auth, caseID, body.ExpectedVersion, decision, key)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.write(writer, request, http.StatusAccepted, struct {
		CaseID        string    `json:"case_id"`
		CaseVersion   int64     `json:"case_version"`
		SourceVersion int64     `json:"source_version"`
		DecisionID    string    `json:"decision_id"`
		ActorID       string    `json:"actor_key_id"`
		ReviewerID    string    `json:"reviewer_id"`
		At            time.Time `json:"requested_at"`
	}{result.CaseID.String(), result.TargetVersion, result.SourceVersion, result.DecisionID.String(), result.ActorID.String(), result.ReviewerID, result.RecordedAt})
}
