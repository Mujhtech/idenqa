package task

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// DecisionBuilder performs authoritative loading and deterministic evaluation
// before the short fenced effect transaction begins.
type DecisionBuilder interface {
	Build(context.Context, tenant.Scope, policy.AuthorRequest) (policy.Decision, error)
}

// DecisionStore is the exact transactional persistence consumed by Handler.
type DecisionStore interface {
	Find(context.Context, tenant.Scope, id.Decision) (policy.Decision, error)
	FindWithin(
		context.Context,
		tenant.Scope,
		postgres.Transaction,
		id.Decision,
	) (policy.Decision, error)
	AppendWithin(context.Context, tenant.Scope, postgres.Transaction, policy.Decision) error
}

// CompletionStore commits the decision's workflow completion and delivery work
// in the same fenced transaction as the immutable decision.
type CompletionStore interface {
	CompleteWithin(context.Context, tenant.Scope, postgres.Transaction, policy.Decision, id.Task) error
}

// Handler authors one replay-safe machine decision through a fence-verified
// Headgate transaction. It never accepts non-transactional execution.
type Handler struct {
	store      DecisionStore
	builder    DecisionBuilder
	completion CompletionStore
	routing    RoutingStore
	evaluation EvaluationBuilder
}

// NewHandler constructs the exact version-1 policy authoring handler.
func NewHandler(store DecisionStore, builder DecisionBuilder) (*Handler, error) {
	if store == nil || builder == nil {
		return nil, errors.New("policy task: author dependencies are required")
	}
	return &Handler{store: store, builder: builder}, nil
}

// NewHandlerWithCompletion composes decision persistence with atomic workflow
// completion. NewHandler remains available for isolated policy authoring.
func NewHandlerWithCompletion(store DecisionStore, builder DecisionBuilder, completion CompletionStore) (*Handler, error) {
	handler, err := NewHandler(store, builder)
	if err != nil {
		return nil, err
	}
	if completion == nil {
		return nil, errors.New("policy task: completion dependency is required")
	}
	handler.completion = completion
	return handler, nil
}

// Handle fails closed when a driver cannot fence the decision append.
func (handler *Handler) Handle(context.Context, platformtask.Delivery) platformtask.Result {
	return platformtask.Quarantine(errors.New("policy authoring requires transactional completion"))
}

// Prepare performs authoritative reads and evaluation outside the transaction,
// returning only the bounded replay check or append effect.
func (handler *Handler) Prepare(
	ctx context.Context,
	delivery platformtask.Delivery,
) (platformtask.TransactionWork, platformtask.Result) {
	request, err := DecodeAuthorRequest(delivery.Intent.Payload())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	scope, err := tenant.NewScope(delivery.Intent.TenantID())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	existing, err := handler.store.Find(ctx, scope, request.DecisionID)
	if err == nil {
		if err := policy.ValidateAuthorReplay(existing, scope, request); err != nil {
			return nil, platformtask.Quarantine(err)
		}
		return handler.replayWork(scope, request, delivery.Intent.ID()), platformtask.Complete()
	}
	if !errors.Is(err, policy.ErrDecisionNotFound) {
		return nil, prepareResult(err)
	}
	if handler.routing != nil {
		return handler.prepareRouting(ctx, scope, request, delivery.Intent.ID())
	}
	decision, err := handler.builder.Build(ctx, scope, request)
	if err != nil {
		return nil, prepareResult(err)
	}
	return handler.appendWork(scope, request, decision, delivery.Intent.ID()), platformtask.Complete()
}

func (handler *Handler) replayWork(
	scope tenant.Scope,
	request policy.AuthorRequest,
	actor id.Task,
) platformtask.TransactionWork {
	return func(ctx context.Context, transaction postgres.Transaction) platformtask.Result {
		decision, err := handler.store.FindWithin(ctx, scope, transaction, request.DecisionID)
		if err != nil {
			return effectResult(err)
		}
		if err := policy.ValidateAuthorReplay(decision, scope, request); err != nil {
			return platformtask.Quarantine(err)
		}
		return handler.complete(ctx, scope, transaction, decision, actor)
	}
}

func (handler *Handler) appendWork(
	scope tenant.Scope,
	request policy.AuthorRequest,
	candidate policy.Decision,
	actor id.Task,
) platformtask.TransactionWork {
	return func(ctx context.Context, transaction postgres.Transaction) platformtask.Result {
		existing, err := handler.store.FindWithin(ctx, scope, transaction, request.DecisionID)
		if err == nil {
			if err := policy.ValidateAuthorReplay(existing, scope, request); err != nil {
				return platformtask.Quarantine(err)
			}
			return handler.complete(ctx, scope, transaction, existing, actor)
		}
		if !errors.Is(err, policy.ErrDecisionNotFound) {
			return effectResult(err)
		}
		if err := handler.store.AppendWithin(ctx, scope, transaction, candidate); err != nil {
			return effectResult(err)
		}
		stored, err := handler.store.FindWithin(ctx, scope, transaction, request.DecisionID)
		if err != nil {
			return effectResult(err)
		}
		if stored.Digest() != candidate.Digest() {
			return platformtask.Quarantine(policy.ErrDecisionConflict)
		}
		if err := policy.ValidateAuthorReplay(stored, scope, request); err != nil {
			return platformtask.Quarantine(err)
		}
		return handler.complete(ctx, scope, transaction, stored, actor)
	}
}

func (handler *Handler) complete(ctx context.Context, scope tenant.Scope, transaction postgres.Transaction, decision policy.Decision, actor id.Task) platformtask.Result {
	if handler.completion != nil {
		if err := handler.completion.CompleteWithin(ctx, scope, transaction, decision, actor); err != nil {
			return effectResult(err)
		}
	}
	return platformtask.Complete()
}

func prepareResult(err error) platformtask.Result {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return platformtask.Retry(platformtask.RetryClassTransient, err)
	case errors.Is(err, policy.ErrActivationNotFound), errors.Is(err, policy.ErrRevisionNotFound),
		errors.Is(err, policy.ErrActivationConflict), errors.Is(err, policy.ErrRevisionConflict):
		return platformtask.Retry(platformtask.RetryClassConflict, err)
	case isPolicySemanticError(err):
		return platformtask.Quarantine(err)
	default:
		return platformtask.Retry(platformtask.RetryClassUnavailable, err)
	}
}

func effectResult(err error) platformtask.Result {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return platformtask.Retry(platformtask.RetryClassTransient, err)
	case errors.Is(err, policy.ErrDecisionConflict), errors.Is(err, policy.ErrDecisionNotFound):
		return platformtask.Retry(platformtask.RetryClassConflict, err)
	case isPolicySemanticError(err):
		return platformtask.Quarantine(err)
	default:
		return platformtask.Retry(
			platformtask.RetryClassUnavailable,
			fmt.Errorf("persist policy decision effect: %w", err),
		)
	}
}

func isPolicySemanticError(err error) bool {
	return errors.Is(err, authority.ErrProcessingNotPermitted) || errors.Is(err, authority.ErrSubjectResponseRequired) ||
		errors.Is(err, policy.ErrInvalid) || errors.Is(err, policy.ErrConflict) ||
		errors.Is(err, policy.ErrVersion) || errors.Is(err, policy.ErrStaleFact) ||
		errors.Is(err, policy.ErrReproduction)
}

var _ platformtask.TransactionalHandler = (*Handler)(nil)
