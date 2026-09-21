package task

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/delivery"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Repository supplies scoped delivery reads and fenced state writes.
type Repository interface {
	FindDelivery(context.Context, tenant.Scope, id.Delivery) (delivery.Intent, error)
	FindEndpoint(context.Context, tenant.Scope, id.WebhookEndpoint) (delivery.Endpoint, error)
	RecordAttemptWithin(context.Context, tenant.Scope, platformpostgres.Transaction, id.Delivery, delivery.Attempt, bool, bool, time.Time) error
	FinishWithin(context.Context, tenant.Scope, platformpostgres.Transaction, id.Delivery, int32, delivery.State, time.Time) error
}

// TransactionalEnqueuer joins successor task intent to the recorded callback result.
type TransactionalEnqueuer interface {
	EnqueueTx(context.Context, platformpostgres.Transaction, ...platformtask.Intent) error
}

// Sender performs one DNS-pinned callback attempt.
type Sender interface {
	Send(context.Context, string, delivery.Signature, []byte) (delivery.SafeDiagnostic, bool, bool, error)
}

// Handler sends at least once and commits bounded response metadata with its task fence.
type Handler struct {
	repository  Repository
	unwrapper   platformcrypto.KeyUnwrapper
	sender      Sender
	identifiers IdentifierGenerator
	enqueuer    TransactionalEnqueuer
	now         func() time.Time
	metrics     Metrics
}

// NewHandler constructs a durable webhook handler with atomic retry continuation.
func NewHandler(repository Repository, unwrapper platformcrypto.KeyUnwrapper, sender Sender, identifiers IdentifierGenerator, enqueuer TransactionalEnqueuer, now func() time.Time) (*Handler, error) {
	if repository == nil || unwrapper == nil || sender == nil || identifiers == nil || enqueuer == nil || now == nil {
		return nil, delivery.ErrInvalid
	}
	return &Handler{
		repository:  repository,
		unwrapper:   unwrapper,
		sender:      sender,
		identifiers: identifiers,
		enqueuer:    enqueuer,
		now:         now,
	}, nil
}

// WithMetrics attaches the bounded delivery metric receiver.
func (handler *Handler) WithMetrics(metrics Metrics) *Handler {
	if handler != nil && metrics != nil {
		handler.metrics = metrics
	}
	return handler
}

// Handle fails closed when the runner cannot provide a fenced transaction.
func (*Handler) Handle(context.Context, platformtask.Delivery) platformtask.Result {
	return platformtask.Quarantine(errors.New("delivery: fenced transaction required"))
}

// Prepare performs the external send before its database effect. A crash can
// resend the same event; tenants deduplicate by the signed event ID. A callback
// retry commits its result and successor task while completing this task, since
// returning Retry from a transaction would roll those durable effects back.
func (handler *Handler) Prepare(ctx context.Context, work platformtask.Delivery) (platformtask.TransactionWork, platformtask.Result) {
	deliveryID, number, err := decodeAttempt(work.Intent.Payload())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	if work.Intent.Key() != DeliverKey {
		return nil, platformtask.Quarantine(delivery.ErrInvalid)
	}
	scope, err := tenant.NewScope(work.Intent.TenantID())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	intent, err := handler.repository.FindDelivery(ctx, scope, deliveryID)
	if err != nil {
		return nil, taskError(err)
	}
	if intent.State != delivery.StatePending || number <= intent.AttemptCount {
		return noEffect, platformtask.Complete()
	}
	if number != intent.AttemptCount+1 || number > intent.MaxAttempts {
		return nil, platformtask.Quarantine(delivery.ErrConflict)
	}
	now := handler.now().UTC().Truncate(time.Microsecond)
	deadline := intent.CreatedAt.Add(MaximumDeliveryDuration)
	if !now.Before(deadline) {
		return handler.finish(scope, intent, delivery.StateExhausted, now), platformtask.Complete()
	}
	if now.Before(intent.NextAttemptAt) {
		return nil, platformtask.Retry(platformtask.RetryClassTransient, errors.New("delivery: attempt not due"))
	}
	endpoint, err := handler.repository.FindEndpoint(ctx, scope, intent.EndpointID)
	if err != nil {
		return nil, taskError(err)
	}
	if !endpoint.DisabledAt.IsZero() {
		return handler.finish(scope, intent, delivery.StateCancelled, now), platformtask.Complete()
	}
	contextData := []byte(fmt.Sprintf("v1\n%s\n%s\n%d", scope.ID().String(), endpoint.ID.String(), endpoint.Active.Version))
	secret, err := handler.unwrapper.Unwrap(ctx, webhookSecretPurposeForTask(), endpoint.Active.Wrapped, contextData)
	if err != nil {
		return nil, platformtask.Retry(platformtask.RetryClassUnavailable, errors.New("delivery: unwrap signing secret"))
	}
	defer clear(secret)
	body := intent.Body
	if intent.BodyWrapping != nil {
		plaintext, unwrapErr := handler.unwrapper.Unwrap(ctx, webhookBodyPurposeForTask(), *intent.BodyWrapping, delivery.BodyContext(scope.ID().String(), intent.EventID.String()))
		if unwrapErr != nil {
			return nil, platformtask.Retry(platformtask.RetryClassUnavailable, errors.New("delivery: unwrap event body"))
		}
		defer clear(plaintext)
		body = plaintext
	}
	signature, err := delivery.Sign(secret, intent.EventID, now, body)
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	sendContext, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	diagnostic, succeeded, retry, sendErr := handler.sender.Send(sendContext, endpoint.URL, signature, body)
	if sendErr != nil && succeeded {
		return nil, platformtask.Quarantine(delivery.ErrInvalid)
	}
	if err := diagnostic.Validate(); err != nil {
		return nil, platformtask.Quarantine(err)
	}
	completedAt := handler.now().UTC().Truncate(time.Microsecond)
	next := completedAt.Add(callbackBackoff(intent.ID, number, diagnostic.RetryAfter))
	retry = retry && !succeeded && number < intent.MaxAttempts && next.Before(deadline)
	handler.observeAttempt(number, intent.MaxAttempts, now, completedAt, succeeded, retry)
	attempt := delivery.Attempt{
		Number:             number,
		SecretVersion:      endpoint.Active.Version,
		SignatureTimestamp: now.Unix(),
		Diagnostic:         diagnostic,
		CompletedAt:        completedAt,
	}
	var successor platformtask.Intent
	if retry {
		successor, err = NewAttemptIntent(handler.identifiers, scope, intent.ID, number+1, next, deadline)
		if err != nil {
			return nil, taskError(err)
		}
	}
	return func(ctx context.Context, tx platformpostgres.Transaction) platformtask.Result {
		if err := handler.repository.RecordAttemptWithin(ctx, scope, tx, deliveryID, attempt, succeeded, retry, next); err != nil {
			return taskError(err)
		}
		if retry {
			if err := handler.enqueuer.EnqueueTx(ctx, tx, successor); err != nil {
				return taskError(err)
			}
		}
		return platformtask.Complete()
	}, platformtask.Complete()
}

func (handler *Handler) observeAttempt(number, maximum int32, startedAt, completedAt time.Time, succeeded, retry bool) {
	if handler.metrics == nil {
		return
	}
	outcome := observability.DeliveryReject
	switch {
	case succeeded:
		outcome = observability.Delivered
	case retry:
		outcome = observability.Retried
	case number >= maximum:
		outcome = observability.Exhausted
	}
	duration := completedAt.Sub(startedAt)
	if duration < 0 {
		duration = 0
	}
	handler.metrics.RecordDeliveryAttempt(observability.DeliveryAttempt{
		Outcome:  outcome,
		Attempt:  number,
		Duration: duration,
	})
}

func (handler *Handler) finish(scope tenant.Scope, intent delivery.Intent, state delivery.State, at time.Time) platformtask.TransactionWork {
	return func(ctx context.Context, tx platformpostgres.Transaction) platformtask.Result {
		if err := handler.repository.FinishWithin(ctx, scope, tx, intent.ID, intent.AttemptCount, state, at); err != nil {
			return taskError(err)
		}
		return platformtask.Complete()
	}
}

func noEffect(context.Context, platformpostgres.Transaction) platformtask.Result {
	return platformtask.Complete()
}

func taskError(err error) platformtask.Result {
	if errors.Is(err, delivery.ErrNotFound) || errors.Is(err, delivery.ErrInvalid) {
		return platformtask.Quarantine(err)
	}
	return platformtask.Retry(platformtask.RetryClassUnavailable, err)
}

// callbackBackoff preserves Retry-After and otherwise applies stable bounded
// jitter so a restarted coordinator retains the committed due time.
func callbackBackoff(deliveryID id.Delivery, attempt int32, retryAfter time.Duration) time.Duration {
	if retryAfter > 0 {
		return retryAfter
	}
	delay := min(time.Second<<min(attempt-1, 12), time.Hour)
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", deliveryID.String(), attempt)))
	percent := int64(int(digest[0])%41 - 20)
	jitter := time.Duration(int64(delay) * percent / 100)
	return min(delay+jitter, time.Hour)
}

func webhookSecretPurposeForTask() kms.Purpose {
	purpose, _ := kms.NewPurpose("delivery.webhook-secret")
	return purpose
}

func webhookBodyPurposeForTask() kms.Purpose {
	return delivery.BodyPurpose()
}

var _ platformtask.TransactionalHandler = (*Handler)(nil)
