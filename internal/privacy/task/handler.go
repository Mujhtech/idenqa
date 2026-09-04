package task

import (
	"context"
	"errors"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// DeletionRunner is the exact service capability consumed by Handler.
type DeletionRunner interface {
	Run(context.Context, tenant.Scope, privacy.Actor, id.Deletion) (privacy.Deletion, error)
}

// Handler executes idempotent exact-object deletion through authoritative state.
type Handler struct{ service DeletionRunner }

// NewHandler constructs the privacy deletion task handler.
func NewHandler(service DeletionRunner) (*Handler, error) {
	if service == nil {
		return nil, errors.New("privacy task: deletion service is required")
	}
	return &Handler{service: service}, nil
}

// Handle advances the workflow. External object deletion is replay-safe and
// every durable transition uses optimistic concurrency and atomic audit.
func (handler *Handler) Handle(ctx context.Context, delivery platformtask.Delivery) platformtask.Result {
	payload, err := DecodeDelete(delivery.Intent.Payload())
	if err != nil {
		return platformtask.Quarantine(err)
	}
	scope, err := tenant.NewScope(delivery.Intent.TenantID())
	if err != nil {
		return platformtask.Quarantine(err)
	}
	_, err = handler.service.Run(ctx, scope, privacy.Actor{ID: "system:privacy-worker", Permissions: []privacy.Permission{privacy.PermissionRunDeletion}}, payload.DeletionID)
	switch {
	case err == nil, errors.Is(err, privacy.ErrHeld):
		return platformtask.Complete()
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return platformtask.Retry(platformtask.RetryClassTransient, err)
	case errors.Is(err, privacy.ErrConflict):
		return platformtask.Retry(platformtask.RetryClassConflict, err)
	case errors.Is(err, privacy.ErrInvalid):
		return platformtask.Quarantine(err)
	default:
		return platformtask.Retry(platformtask.RetryClassUnavailable, err)
	}
}

var _ platformtask.Handler = (*Handler)(nil)
