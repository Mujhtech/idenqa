package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	"github.com/Mujhtech/idenqa/internal/keyrewrap"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// KeyOperationsRoutes is the authenticated fleet rewrap, verified destruction,
// and dual-control recovery ceremony surface.
type KeyOperationsRoutes struct {
	access      *AccessMiddleware
	rewrap      *keyrewrap.Service
	destruction *keycustody.DestructionService
	recovery    *keycustody.RecoveryService
	logger      *slog.Logger
}

// NewKeyOperationsRoutes constructs API-key protected key-operation routes.
func NewKeyOperationsRoutes(
	accessMiddleware *AccessMiddleware,
	rewrap *keyrewrap.Service,
	destruction *keycustody.DestructionService,
	recovery *keycustody.RecoveryService,
	logger *slog.Logger,
) (*KeyOperationsRoutes, error) {
	if accessMiddleware == nil || rewrap == nil || destruction == nil || recovery == nil || logger == nil {
		return nil, keycustody.ErrInvalid
	}

	return &KeyOperationsRoutes{access: accessMiddleware, rewrap: rewrap, destruction: destruction, recovery: recovery, logger: logger}, nil
}

// Register mounts bounded status reads and audited operations.
func (r *KeyOperationsRoutes) Register(router chi.Router) {
	router.With(r.access.Authenticate, r.access.Require(access.PermissionKMSRead)).
		Get("/kms/rewrap", r.rewrapStatus)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionKMSWrite)).
		Post("/kms/rewrap/run", r.rewrapRun)

	router.With(r.access.Authenticate, r.access.Require(access.PermissionKMSWrite)).
		Post("/kms/destruction-verifications", r.verifyDestruction)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionKMSRead)).
		Get("/kms/destruction-verifications/{id}", r.readDestruction)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionKMSWrite)).
		Post("/kms/destruction-verifications/{id}/schedule", r.scheduleDestruction)

	router.With(r.access.Authenticate, r.access.Require(access.PermissionKMSWrite)).
		Post("/kms/recovery-ceremonies", r.startRecovery)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionKMSRead)).
		Get("/kms/recovery-ceremonies/{id}", r.readRecovery)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionKMSWrite)).
		Post("/kms/recovery-ceremonies/{id}/approve", r.approveRecovery)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionKMSWrite)).
		Post("/kms/recovery-ceremonies/{id}/complete", r.completeRecovery)
	router.With(r.access.Authenticate, r.access.Require(access.PermissionKMSWrite)).
		Post("/kms/recovery-ceremonies/{id}/abort", r.abortRecovery)
}

type rewrapStatusResponse struct {
	Classes []rewrapClassState `json:"classes"`
}

type rewrapClassState struct {
	Class          string `json:"class"`
	Generation     int64  `json:"generation"`
	Status         string `json:"status"`
	Epoch          string `json:"epoch"`
	CursorTenant   string `json:"cursor_tenant"`
	CursorObject   string `json:"cursor_object"`
	Processed      int64  `json:"processed"`
	Rewrapped      int64  `json:"rewrapped"`
	Skipped        int64  `json:"skipped"`
	Failed         int64  `json:"failed"`
	LastError      string `json:"last_error,omitempty"`
	NextAttemptUTC string `json:"next_attempt_at,omitempty"`
}

type rewrapRunResponse struct {
	Batches []rewrapBatchResult `json:"batches"`
}

type rewrapBatchResult struct {
	Class        string `json:"class"`
	Generation   int64  `json:"generation"`
	Status       string `json:"status"`
	Rewrapped    int    `json:"rewrapped"`
	Skipped      int    `json:"skipped"`
	Failed       int    `json:"failed"`
	EpochChanged bool   `json:"epoch_changed"`
}

func (r *KeyOperationsRoutes) rewrapStatus(w http.ResponseWriter, q *http.Request) {
	states, err := r.rewrap.Statuses(q.Context())
	if err != nil {
		r.reply(w, q, nil, err)

		return
	}
	response := rewrapStatusResponse{Classes: make([]rewrapClassState, 0, len(states))}
	for _, state := range states {
		entry := rewrapClassState{
			Class: state.Class.String(), Generation: state.Generation, Status: state.Status,
			Epoch: state.Epoch.Key, CursorTenant: state.Cursor.Tenant.String(), CursorObject: state.Cursor.Object,
			Processed: state.Processed, Rewrapped: state.Rewrapped, Skipped: state.Skipped,
			Failed: state.Failed, LastError: state.LastError,
		}
		if !state.NextAttemptAt.IsZero() {
			entry.NextAttemptUTC = state.NextAttemptAt.Format(time.RFC3339)
		}
		response.Classes = append(response.Classes, entry)
	}
	r.reply(w, q, response, nil)
}

func (r *KeyOperationsRoutes) rewrapRun(w http.ResponseWriter, q *http.Request) {
	body, err := decodeJSONBody[struct {
		Class string `json:"class"`
		Batch int    `json:"batch"`
	}](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	if body.Batch == 0 {
		body.Batch = keyrewrap.DefaultBatch
	}
	results := []keyrewrap.Result{}
	if body.Class == "" {
		results, err = r.rewrap.SweepAll(q.Context(), body.Batch)
	} else {
		class, parseErr := keyrewrap.ParseClass(body.Class)
		if parseErr != nil {
			r.reply(w, q, nil, invalidRequest(errors.New("class is invalid")))

			return
		}
		var result keyrewrap.Result
		result, err = r.rewrap.SweepClass(q.Context(), class, body.Batch)
		results = append(results, result)
	}
	if err != nil {
		r.reply(w, q, nil, err)

		return
	}
	response := rewrapRunResponse{Batches: make([]rewrapBatchResult, 0, len(results))}
	for _, result := range results {
		response.Batches = append(response.Batches, rewrapBatchResult{
			Class: result.Class.String(), Generation: result.Generation, Status: result.Status,
			Rewrapped: result.Rewrapped, Skipped: result.Skipped, Failed: result.Failed,
			EpochChanged: result.EpochChanged,
		})
	}
	r.reply(w, q, response, nil)
}

type destructionVerifyRequest struct {
	Provider  string `json:"provider"`
	Reference string `json:"reference"`
	Version   string `json:"version"`
	Algorithm string `json:"algorithm"`
	Reason    string `json:"reason"`
}

type destructionScheduleRequest struct {
	Window     string `json:"window"`
	RecordOnly bool   `json:"record_only"`
	Reason     string `json:"reason"`
}

type destructionReceiptResponse struct {
	ID         string           `json:"id"`
	State      string           `json:"state"`
	Provider   string           `json:"provider"`
	Reference  string           `json:"reference"`
	Version    string           `json:"version"`
	Algorithm  string           `json:"algorithm"`
	Total      int64            `json:"total"`
	Counts     map[string]int64 `json:"counts"`
	Verifier   string           `json:"verifier"`
	Reason     string           `json:"reason"`
	Digest     string           `json:"digest"`
	VerifiedAt string           `json:"verified_at"`
}

type destructionScheduleResponse struct {
	ID                 string `json:"id"`
	VerificationID     string `json:"verification_id"`
	Mode               string `json:"mode"`
	ProviderDeletionAt string `json:"provider_deletion_at,omitempty"`
	Actor              string `json:"actor"`
	Reason             string `json:"reason"`
	CreatedAt          string `json:"created_at"`
}

func (r *KeyOperationsRoutes) verifyDestruction(w http.ResponseWriter, q *http.Request) {
	body, err := decodeJSONBody[destructionVerifyRequest](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	auth, _ := AccessContext(q.Context())
	value, err := r.destruction.ExecuteAuthorized(q.Context(), auth, "verify", keycustody.DestructionTarget{
		Provider: body.Provider, Reference: body.Reference, Version: body.Version, Algorithm: body.Algorithm,
	}, "", 0, false, body.Reason)
	if err != nil {
		if receipt, ok := value.(keycustody.VerificationReceipt); ok && receipt.ID != "" {
			r.reply(w, q, destructionResponse(receipt), err)

			return
		}
		r.reply(w, q, nil, err)

		return
	}
	receipt, _ := value.(keycustody.VerificationReceipt)
	r.reply(w, q, destructionResponse(receipt), nil)
}

func destructionResponse(receipt keycustody.VerificationReceipt) destructionReceiptResponse {
	counts := make(map[string]int64, len(receipt.Counts))
	for class, count := range receipt.Counts {
		counts[string(class)] = count
	}

	return destructionReceiptResponse{
		ID: receipt.ID, State: receipt.State, Provider: receipt.Target.Provider,
		Reference: receipt.Target.Reference, Version: receipt.Target.Version,
		Algorithm: receipt.Target.Algorithm, Total: receipt.Total, Counts: counts,
		Verifier: receipt.Verifier, Reason: receipt.Reason, Digest: receipt.Digest,
		VerifiedAt: receipt.VerifiedAt.Format(time.RFC3339),
	}
}

func (r *KeyOperationsRoutes) readDestruction(w http.ResponseWriter, q *http.Request) {
	receipt, err := r.destruction.ReceiptDirect(q.Context(), chi.URLParam(q, "id"))
	if err != nil {
		r.reply(w, q, nil, err)

		return
	}
	r.reply(w, q, destructionResponse(receipt), nil)
}

func (r *KeyOperationsRoutes) scheduleDestruction(w http.ResponseWriter, q *http.Request) {
	body, err := decodeJSONBody[destructionScheduleRequest](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	window := 7 * 24 * time.Hour
	if body.Window != "" {
		parsed, parseErr := time.ParseDuration(body.Window)
		if parseErr != nil {
			r.reply(w, q, nil, invalidRequest(errors.New("window must be a Go duration such as 168h")))

			return
		}
		window = parsed
	}
	auth, _ := AccessContext(q.Context())
	value, err := r.destruction.ExecuteAuthorized(q.Context(), auth, "schedule", keycustody.DestructionTarget{},
		chi.URLParam(q, "id"), window, body.RecordOnly, body.Reason)
	if err != nil {
		r.reply(w, q, nil, err)

		return
	}
	schedule, _ := value.(keycustody.DestructionSchedule)
	response := destructionScheduleResponse{
		ID: schedule.ID, VerificationID: schedule.VerificationID, Mode: schedule.Mode,
		Actor: schedule.Actor, Reason: schedule.Reason, CreatedAt: schedule.CreatedAt.Format(time.RFC3339),
	}
	if schedule.ProviderDeletionAt != nil {
		response.ProviderDeletionAt = schedule.ProviderDeletionAt.Format(time.RFC3339)
	}
	r.reply(w, q, response, nil)
}

type recoveryStartRequest struct {
	Kind   string `json:"kind"`
	Class  string `json:"class"`
	Tenant string `json:"tenant"`
	Target struct {
		Provider  string `json:"provider"`
		Reference string `json:"reference"`
		Version   string `json:"version"`
		Algorithm string `json:"algorithm"`
	} `json:"target"`
	Reason string `json:"reason"`
}

type recoveryVersionedRequest struct {
	ExpectedVersion int64  `json:"expected_version"`
	Reason          string `json:"reason"`
}

type recoveryCeremonyResponse struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"`
	Class       string         `json:"class"`
	Tenant      string         `json:"tenant,omitempty"`
	State       string         `json:"state"`
	Version     int64          `json:"version"`
	StartedBy   string         `json:"started_by"`
	StartedAt   string         `json:"started_at"`
	ApproveBy   string         `json:"approve_by"`
	ApprovedBy  string         `json:"approved_by,omitempty"`
	ApprovedAt  string         `json:"approved_at,omitempty"`
	UsableUntil string         `json:"usable_until,omitempty"`
	CompletedBy string         `json:"completed_by,omitempty"`
	CompletedAt string         `json:"completed_at,omitempty"`
	AbortedBy   string         `json:"aborted_by,omitempty"`
	AbortReason string         `json:"abort_reason,omitempty"`
	Receipt     map[string]any `json:"receipt,omitempty"`
	Reason      string         `json:"reason"`
	UpdatedAt   string         `json:"updated_at"`
}

func (r *KeyOperationsRoutes) startRecovery(w http.ResponseWriter, q *http.Request) {
	body, err := decodeJSONBody[recoveryStartRequest](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	auth, _ := AccessContext(q.Context())
	result, err := r.recovery.Execute(q.Context(), auth, keycustody.RecoveryCommand{
		Operation: "start", Kind: body.Kind, Class: body.Class, TenantID: body.Tenant,
		Target: keycustody.DestructionTarget{
			Provider: body.Target.Provider, Reference: body.Target.Reference,
			Version: body.Target.Version, Algorithm: body.Target.Algorithm,
		},
		Reason: body.Reason,
	})
	r.reply(w, q, recoveryResponse(result.Ceremony), err)
}

func (r *KeyOperationsRoutes) readRecovery(w http.ResponseWriter, q *http.Request) {
	auth, _ := AccessContext(q.Context())
	result, err := r.recovery.Read(q.Context(), auth, chi.URLParam(q, "id"))
	r.reply(w, q, recoveryResponse(result.Ceremony), err)
}

func (r *KeyOperationsRoutes) approveRecovery(w http.ResponseWriter, q *http.Request) {
	r.transitionRecovery(w, q, "approve")
}

func (r *KeyOperationsRoutes) completeRecovery(w http.ResponseWriter, q *http.Request) {
	r.transitionRecovery(w, q, "complete")
}

func (r *KeyOperationsRoutes) abortRecovery(w http.ResponseWriter, q *http.Request) {
	r.transitionRecovery(w, q, "abort")
}

func (r *KeyOperationsRoutes) transitionRecovery(w http.ResponseWriter, q *http.Request, operation string) {
	body, err := decodeJSONBody[recoveryVersionedRequest](q)
	if err != nil {
		r.reply(w, q, nil, invalidRequest(err))

		return
	}
	auth, _ := AccessContext(q.Context())
	result, err := r.recovery.Execute(q.Context(), auth, keycustody.RecoveryCommand{
		Operation: operation, Identifier: chi.URLParam(q, "id"),
		ExpectedVersion: body.ExpectedVersion, Reason: body.Reason,
	})
	r.reply(w, q, recoveryResponse(result.Ceremony), err)
}

func recoveryResponse(ceremony keycustody.RecoveryCeremony) recoveryCeremonyResponse {
	response := recoveryCeremonyResponse{
		ID: ceremony.ID, Kind: ceremony.Kind, Class: ceremony.Class, State: ceremony.State,
		Version: ceremony.Version, StartedBy: ceremony.StartedBy,
		StartedAt:  ceremony.StartedAt.Format(time.RFC3339),
		ApproveBy:  ceremony.ApproveBy.Format(time.RFC3339),
		ApprovedBy: ceremony.ApprovedBy, CompletedBy: ceremony.CompletedBy,
		AbortedBy: ceremony.AbortedBy, AbortReason: ceremony.AbortReason,
		Receipt: ceremony.Receipt, Reason: ceremony.Reason,
		UpdatedAt: ceremony.UpdatedAt.Format(time.RFC3339),
	}
	if !ceremony.TenantID.IsZero() {
		response.Tenant = ceremony.TenantID.String()
	}
	if !ceremony.ApprovedAt.IsZero() {
		response.ApprovedAt = ceremony.ApprovedAt.Format(time.RFC3339)
	}
	if !ceremony.UsableUntil.IsZero() {
		response.UsableUntil = ceremony.UsableUntil.Format(time.RFC3339)
	}
	if !ceremony.CompletedAt.IsZero() {
		response.CompletedAt = ceremony.CompletedAt.Format(time.RFC3339)
	}

	return response
}

func (r *KeyOperationsRoutes) reply(w http.ResponseWriter, q *http.Request, value any, err error) {
	w.Header().Set("Cache-Control", "no-store")
	if err != nil {
		switch {
		case errors.Is(err, keycustody.ErrInvalid), errors.Is(err, keyrewrap.ErrInvalid):
			err = invalidRequest(err)
		case errors.Is(err, keycustody.ErrNotFound):
			err = apierror.New(404, apierror.CodeNotFound, "Not found", "The key record was not found.", err)
		case errors.Is(err, keycustody.ErrRecoveryForbidden):
			err = apierror.New(403, apierror.CodeInsufficientScope, "Forbidden", "The key operation is not permitted.", err)
		case errors.Is(err, keycustody.ErrDestructionBlocked):
			err = apierror.New(409, apierror.CodeConflict, "Conflict", "Live references still target the key material.", err)
		case errors.Is(err, keycustody.ErrDestructionUnverified):
			err = apierror.New(409, apierror.CodeConflict, "Conflict", "An unexpired verified receipt is required.", err)
		case errors.Is(err, keycustody.ErrRecoveryExpired):
			err = apierror.New(409, apierror.CodeConflict, "Conflict", "The recovery ceremony validity window has elapsed.", err)
		case errors.Is(err, keycustody.ErrRecoveryState):
			err = apierror.New(409, apierror.CodeConflict, "Conflict", "The recovery ceremony state does not permit this operation.", err)
		case errors.Is(err, keycustody.ErrConflict), errors.Is(err, keyrewrap.ErrConflict):
			err = apierror.New(409, apierror.CodeConflict, "Conflict", "The key record changed since it was read.", err)
		case errors.Is(err, keycustody.ErrUnavailable), errors.Is(err, keyrewrap.ErrUnavailable):
			err = apierror.New(503, apierror.CodeServiceUnavailable, "Unavailable", "Key operations are unavailable.", err)
		}
		if writeErr := respond.WriteProblem(w, q, err, requestIDString(q.Context())); writeErr != nil {
			r.logger.ErrorContext(q.Context(), "write key operations problem")
		}

		return
	}
	if err := respond.JSON(w, q, 200, value); err != nil {
		r.logger.ErrorContext(q.Context(), "write key operations response")
	}
}
