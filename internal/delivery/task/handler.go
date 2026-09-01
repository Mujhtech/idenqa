package task

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Repository is the narrow delivery state consumed by Handler.
type Repository interface {
	FindDelivery(context.Context, tenant.Scope, id.Delivery) (delivery.Intent, error)
	FindEndpoint(context.Context, tenant.Scope, id.WebhookEndpoint) (delivery.Endpoint, error)
	RecordAttempt(context.Context, tenant.Scope, id.Delivery, delivery.Attempt, bool, bool, time.Time) error
}

// Sender performs one DNS-pinned callback attempt.
type Sender interface {
	Send(context.Context, string, delivery.Signature, []byte) (delivery.SafeDiagnostic, bool, bool, error)
}

// Handler performs at-least-once HTTP delivery and durably records response-free metadata.
type Handler struct {
	repository Repository
	unwrapper  platformcrypto.KeyUnwrapper
	sender     Sender
	now        func() time.Time
}

// NewHandler constructs the durable webhook delivery handler.
func NewHandler(repository Repository, unwrapper platformcrypto.KeyUnwrapper, sender Sender, now func() time.Time) (*Handler, error) {
	if repository == nil || unwrapper == nil || sender == nil || now == nil {
		return nil, delivery.ErrInvalid
	}
	return &Handler{repository: repository, unwrapper: unwrapper, sender: sender, now: now}, nil
}

// Handle executes one at-least-once attempt and records safe diagnostics.
func (handler *Handler) Handle(ctx context.Context, work platformtask.Delivery) platformtask.Result {
	deliveryID, err := Decode(work.Intent.Payload())
	if err != nil {
		return platformtask.Quarantine(err)
	}
	scope, err := tenant.NewScope(work.Intent.TenantID())
	if err != nil {
		return platformtask.Quarantine(err)
	}
	intent, err := handler.repository.FindDelivery(ctx, scope, deliveryID)
	if err != nil {
		return taskError(err)
	}
	if intent.State == delivery.StateDelivered || intent.State == delivery.StateExhausted || intent.State == delivery.StateCancelled {
		return platformtask.Complete()
	}
	maximumAttempts := uint32(intent.MaxAttempts) //nolint:gosec // Intent validation requires a positive value at most twenty.
	if work.Attempt == 0 || work.Attempt > maximumAttempts {
		return platformtask.Quarantine(delivery.ErrInvalid)
	}
	attemptNumber := int32(work.Attempt) //nolint:gosec // MaxAttempts is validated to at most twenty above.
	endpoint, err := handler.repository.FindEndpoint(ctx, scope, intent.EndpointID)
	if err != nil {
		return taskError(err)
	}
	if !endpoint.DisabledAt.IsZero() {
		return platformtask.Cancel(delivery.ErrDisabled)
	}
	contextData := []byte(fmt.Sprintf("v1\n%s\n%s\n%d", scope.ID().String(), endpoint.ID.String(), endpoint.Active.Version))
	secret, err := handler.unwrapper.Unwrap(ctx, webhookSecretPurposeForTask(), endpoint.Active.Wrapped, contextData)
	if err != nil {
		return platformtask.Retry(platformtask.RetryClassUnavailable, errors.New("delivery: unwrap signing secret"))
	}
	defer clear(secret)
	now := handler.now().UTC()
	signature, err := delivery.Sign(secret, intent.EventID, now, intent.Body)
	if err != nil {
		return platformtask.Quarantine(err)
	}
	diagnostic, succeeded, retry, sendErr := handler.sender.Send(ctx, endpoint.URL, signature, intent.Body)
	next := now.Add(backoff(work.Attempt, diagnostic.RetryAfter))
	attempt := delivery.Attempt{Number: attemptNumber, SecretVersion: endpoint.Active.Version, SignatureTimestamp: now.Unix(), Diagnostic: diagnostic, CompletedAt: now}
	if err := handler.repository.RecordAttempt(ctx, scope, deliveryID, attempt, succeeded, retry, next); err != nil {
		return platformtask.Retry(platformtask.RetryClassUnavailable, err)
	}
	if succeeded || !retry || attemptNumber >= intent.MaxAttempts {
		return platformtask.Complete()
	}
	if sendErr == nil {
		sendErr = errors.New("delivery: retryable endpoint response")
	}
	return platformtask.Retry(platformtask.RetryClassTransient, sendErr)
}

func taskError(err error) platformtask.Result {
	if errors.Is(err, delivery.ErrNotFound) || errors.Is(err, delivery.ErrInvalid) {
		return platformtask.Quarantine(err)
	}
	return platformtask.Retry(platformtask.RetryClassUnavailable, err)
}

func backoff(attempt uint32, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	delay := time.Second << min(attempt-1, 12)
	return min(delay, time.Hour)
}

func webhookSecretPurposeForTask() kms.Purpose {
	purpose, _ := kms.NewPurpose("delivery.webhook-secret")
	return purpose
}

var _ platformtask.Handler = (*Handler)(nil)
