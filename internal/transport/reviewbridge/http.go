// Package reviewbridge exposes the fixed local protocol used by the deployment
// agent for short-lived, delegated review commands. It is intended for a
// protected Unix-domain socket only.
package reviewbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/reviewdelegation"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const maxRequestBytes = 64 << 10

type executor interface {
	Claim(context.Context, tenant.Scope, review.Actor, id.ReviewCase, int64) (review.Case, error)
	SubmitFinding(context.Context, tenant.Scope, review.Actor, id.ReviewCase, review.Resolution, string, []id.Grant, int64) (review.Case, error)
}

type observer interface {
	ObserveReviewCommand(context.Context, tenant.Scope, reviewdelegation.Claims) (review.Case, error)
}

// Handler implements the closed local review-command protocol.
type Handler struct {
	executor executor
	observer observer
	verifier *reviewdelegation.Verifier
}

// NewHandler constructs a fail-closed local review-command handler.
func NewHandler(executor executor, observer observer, verifier *reviewdelegation.Verifier) (*Handler, error) {
	if executor == nil || observer == nil || verifier == nil {
		return nil, errors.New("review bridge dependencies are required")
	}
	return &Handler{executor: executor, observer: observer, verifier: verifier}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
		return
	}
	switch request.URL.Path {
	case "/local/v1/review-commands/observe":
		handler.observe(writer, request)
	case "/local/v1/review-commands/execute":
		handler.execute(writer, request)
	default:
		writeError(writer, http.StatusNotFound, "not_found")
	}
}

type command struct {
	ID                 string                    `json:"id"`
	Scope              reviewdelegation.Scope    `json:"scope"`
	ActorID            string                    `json:"actorId"`
	Permission         string                    `json:"permission"`
	Purpose            string                    `json:"purpose"`
	TargetKind         string                    `json:"targetKind"`
	TargetID           string                    `json:"targetId"`
	Operation          string                    `json:"operation"`
	ExpectedVersion    int64                     `json:"expectedVersion"`
	ReasonCode         string                    `json:"reasonCode"`
	Resolution         string                    `json:"resolution,omitempty"`
	EvidenceGrantIDs   []string                  `json:"evidenceGrantIds,omitempty"`
	ApprovalID         string                    `json:"approvalId,omitempty"`
	SupportGrantID     string                    `json:"supportGrantId,omitempty"`
	RequestDigest      string                    `json:"requestDigest"`
	Authority          reviewdelegation.Envelope `json:"authority"`
	State              string                    `json:"state"`
	Attempt            int                       `json:"attempt"`
	ObserveFirst       bool                      `json:"observeFirst"`
	ResultCode         string                    `json:"resultCode,omitempty"`
	AuthoritativeState string                    `json:"authoritativeState,omitempty"`
	CreatedAt          json.RawMessage           `json:"createdAt"`
	UpdatedAt          json.RawMessage           `json:"updatedAt"`
}

type result struct {
	CommandID          string `json:"commandId"`
	ResultCode         string `json:"resultCode"`
	AuthoritativeState string `json:"authoritativeState"`
}

func (handler *Handler) execute(writer http.ResponseWriter, request *http.Request) {
	payload, claims, scope, caseID, ok := handler.decode(writer, request)
	if !ok {
		return
	}
	ctx := reviewdelegation.WithClaims(request.Context(), claims)
	var value review.Case
	var err error
	switch claims.Operation {
	case "claim":
		value, err = handler.executor.Claim(ctx, scope, review.Actor{ID: claims.ActorID}, caseID, claims.ExpectedVersion)
	case "submit_finding":
		grantIDs := make([]id.Grant, 0, len(claims.EvidenceGrantIDs))
		for _, encoded := range claims.EvidenceGrantIDs {
			grantID, parseErr := id.ParseGrant(encoded)
			if parseErr != nil {
				writeError(writer, http.StatusBadRequest, "invalid_request")
				return
			}
			grantIDs = append(grantIDs, grantID)
		}
		value, err = handler.executor.SubmitFinding(ctx, scope, review.Actor{ID: claims.ActorID}, caseID, review.Resolution(claims.Resolution), claims.ReasonCode, grantIDs, claims.ExpectedVersion)
	default:
		writeError(writer, http.StatusBadRequest, "unsupported_operation")
		return
	}
	if err != nil {
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result{CommandID: payload.ID, ResultCode: "claimed", AuthoritativeState: string(value.State)})
}

func (handler *Handler) observe(writer http.ResponseWriter, request *http.Request) {
	payload, claims, scope, _, ok := handler.decode(writer, request)
	if !ok {
		return
	}
	value, err := handler.observer.ObserveReviewCommand(request.Context(), scope, claims)
	if err != nil {
		if errors.Is(err, reviewdelegation.ErrNotFound) {
			writeError(writer, http.StatusNotFound, "not_found")
			return
		}
		writeServiceError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, result{CommandID: payload.ID, ResultCode: resultCode(claims.Operation), AuthoritativeState: string(value.State)})
}

func (handler *Handler) decode(writer http.ResponseWriter, request *http.Request) (command, reviewdelegation.Claims, tenant.Scope, id.ReviewCase, bool) {
	request.Body = http.MaxBytesReader(writer, request.Body, maxRequestBytes)
	decoder := json.NewDecoder(request.Body)
	decoder.DisallowUnknownFields()
	var payload command
	if err := decoder.Decode(&payload); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return command{}, reviewdelegation.Claims{}, tenant.Scope{}, id.ReviewCase{}, false
	}
	if err := ensureEOF(decoder); err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return command{}, reviewdelegation.Claims{}, tenant.Scope{}, id.ReviewCase{}, false
	}
	claims, err := handler.verifier.Verify(payload.Authority)
	if err != nil || !matches(payload, claims) || !validDigest(claims) {
		writeError(writer, http.StatusForbidden, "delegation_forbidden")
		return command{}, reviewdelegation.Claims{}, tenant.Scope{}, id.ReviewCase{}, false
	}
	tenantID, err := id.ParseTenant(claims.Scope.TenantID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return command{}, reviewdelegation.Claims{}, tenant.Scope{}, id.ReviewCase{}, false
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return command{}, reviewdelegation.Claims{}, tenant.Scope{}, id.ReviewCase{}, false
	}
	caseID, err := id.ParseReviewCase(claims.TargetID)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return command{}, reviewdelegation.Claims{}, tenant.Scope{}, id.ReviewCase{}, false
	}
	return payload, claims, scope, caseID, true
}

func matches(command command, claims reviewdelegation.Claims) bool {
	return command.ID == claims.CommandID && command.Scope == claims.Scope && command.ActorID == claims.ActorID &&
		command.Permission == claims.Permission && command.Purpose == claims.Purpose && command.TargetKind == claims.TargetKind &&
		command.TargetID == claims.TargetID && command.Operation == claims.Operation && command.ExpectedVersion == claims.ExpectedVersion &&
		command.ReasonCode == claims.ReasonCode && command.Resolution == claims.Resolution && slices.Equal(command.EvidenceGrantIDs, claims.EvidenceGrantIDs) && command.ApprovalID == claims.ApprovalID &&
		command.SupportGrantID == claims.SupportGrantID && command.RequestDigest == claims.RequestDigest
}

func validDigest(claims reviewdelegation.Claims) bool {
	var canonical string
	switch claims.Operation {
	case "claim":
		canonical = "claim\x00" + claims.TargetID + "\x00" + strconv.FormatInt(claims.ExpectedVersion, 10)
	case "submit_finding":
		canonical = "submit_finding\x00" + claims.TargetID + "\x00" + strconv.FormatInt(claims.ExpectedVersion, 10) + "\x00" + claims.Resolution + "\x00" + claims.ReasonCode
		for _, grantID := range claims.EvidenceGrantIDs {
			canonical += "\x00" + grantID
		}
	default:
		return false
	}
	digest := sha256.Sum256([]byte(canonical))
	return claims.RequestDigest == hex.EncodeToString(digest[:])
}

func resultCode(operation string) string {
	if operation == "claim" {
		return "claimed"
	}
	return operation
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeServiceError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, review.ErrForbidden):
		writeError(writer, http.StatusForbidden, "forbidden")
	case errors.Is(err, review.ErrConflict):
		writeError(writer, http.StatusConflict, "conflict")
	case errors.Is(err, review.ErrCaseNotFound), errors.Is(err, review.ErrInvalid):
		writeError(writer, http.StatusNotFound, "not_found")
	default:
		writeError(writer, http.StatusInternalServerError, "internal_error")
	}
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"code": code})
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
