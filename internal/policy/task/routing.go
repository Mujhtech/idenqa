package task

import (
	"context"
	"errors"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// EvaluationBuilder resolves canonical terminal or nonterminal policy meaning.
type EvaluationBuilder interface {
	DecisionBuilder
	Evaluate(context.Context, tenant.Scope, policy.AuthorRequest) (policy.Snapshot, policy.Evaluation, error)
}

// RoutingStore owns atomic nonterminal provenance and workflow effects.
type RoutingStore interface {
	FindRouting(context.Context, tenant.Scope, id.Decision) (policy.Routing, error)
	RouteWithin(context.Context, tenant.Scope, postgres.Transaction, policy.Routing, id.Task) error
}

// NewHandlerWithRouting enables explicit review routing alongside terminal completion.
func NewHandlerWithRouting(store DecisionStore, builder EvaluationBuilder, completion CompletionStore, routing RoutingStore) (*Handler, error) {
	handler, err := NewHandlerWithCompletion(store, builder, completion)
	if err != nil {
		return nil, err
	}
	if routing == nil {
		return nil, policy.ErrInvalid
	}
	handler.routing = routing
	handler.evaluation = builder
	return handler, nil
}

func (handler *Handler) prepareRouting(ctx context.Context, scope tenant.Scope, request policy.AuthorRequest, actor id.Task) (platformtask.TransactionWork, platformtask.Result) {
	existing, err := handler.routing.FindRouting(ctx, scope, request.DecisionID)
	if err == nil {
		if err := existing.ValidateReplay(scope, request); err != nil {
			return nil, platformtask.Quarantine(err)
		}
		return handler.routeWork(scope, existing, actor), platformtask.Complete()
	}
	if !errors.Is(err, policy.ErrDecisionNotFound) {
		return nil, prepareResult(err)
	}
	snapshot, evaluation, err := handler.evaluation.Evaluate(ctx, scope, request)
	if err != nil {
		return nil, prepareResult(err)
	}
	if evaluation.AuthorisesCompletion() {
		decision, err := policy.NewDecision(policy.DecisionInput{ID: request.DecisionID, Snapshot: snapshot, Evaluation: evaluation, Actor: policy.ActorMachine, Supersedes: request.Supersedes, DecidedAt: request.DecidedAt})
		if err != nil {
			return nil, prepareResult(err)
		}
		return handler.appendWork(scope, request, decision, actor), platformtask.Complete()
	}
	routing, err := policy.NewRouting(scope, request, snapshot, evaluation)
	if err != nil {
		return nil, prepareResult(err)
	}
	return handler.routeWork(scope, routing, actor), platformtask.Complete()
}

func (handler *Handler) routeWork(scope tenant.Scope, routing policy.Routing, actor id.Task) platformtask.TransactionWork {
	return func(ctx context.Context, tx postgres.Transaction) platformtask.Result {
		if err := handler.routing.RouteWithin(ctx, scope, tx, routing, actor); err != nil {
			return effectResult(err)
		}
		return platformtask.Complete()
	}
}
