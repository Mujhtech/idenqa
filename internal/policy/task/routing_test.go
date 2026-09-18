package task

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type evaluationBuilderStub struct {
	decisionBuilderStub
	snapshot    policy.Snapshot
	evaluation  policy.Evaluation
	evaluations int
}

func (builder *evaluationBuilderStub) Evaluate(context.Context, tenant.Scope, policy.AuthorRequest) (policy.Snapshot, policy.Evaluation, error) {
	builder.evaluations++
	return builder.snapshot, builder.evaluation, builder.err
}

type routingStoreStub struct {
	saved *policy.Routing
	calls int
	err   error
}

func (store *routingStoreStub) FindRouting(context.Context, tenant.Scope, id.Decision) (policy.Routing, error) {
	if store.saved == nil {
		return policy.Routing{}, policy.ErrDecisionNotFound
	}
	return *store.saved, nil
}
func (store *routingStoreStub) RouteWithin(_ context.Context, _ tenant.Scope, _ postgres.Transaction, routing policy.Routing, _ id.Task) error {
	store.calls++
	if store.err != nil {
		return store.err
	}
	store.saved = &routing
	return nil
}

func TestHandlerRoutesAndReplaysWithoutTerminalCompletion(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		state     policy.RequirementState
		directive policy.Directive
	}{
		{name: "request input", state: policy.RequirementUnavailable, directive: policy.DirectiveRequestInput},
		{name: "manual review", state: policy.RequirementSatisfied, directive: policy.DirectiveRouteManualReview},
		{name: "fail workflow", state: policy.RequirementProhibited, directive: policy.DirectiveFailWorkflow},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newTaskFixture(t)
			results := fixture.decision.Evaluation().Results()
			for index := range results {
				results[index].State = test.state
				results[index].Candidate = test.directive
			}
			evaluation, err := policy.Resolve(fixture.decision.Snapshot(), results, "")
			if err != nil {
				t.Fatal(err)
			}
			builder := &evaluationBuilderStub{snapshot: fixture.decision.Snapshot(), evaluation: evaluation}
			decisions := newDecisionStoreStub()
			completion := &completionStoreStub{}
			routing := &routingStoreStub{}
			handler, err := NewHandlerWithRouting(decisions, builder, completion, routing)
			if err != nil {
				t.Fatal(err)
			}
			for attempt := 0; attempt < 2; attempt++ {
				work, result := handler.Prepare(t.Context(), fixture.delivery(t))
				if result.Outcome != platformtask.OutcomeComplete || work == nil {
					t.Fatalf("prepare=%+v", result)
				}
				if result := work(t.Context(), transactionStub{}); result.Outcome != platformtask.OutcomeComplete {
					t.Fatalf("effect=%+v", result)
				}
				builder.err = errors.New("must not evaluate committed routing")
			}
			if builder.evaluations != 1 || routing.calls != 2 || decisions.appendCalls != 0 || completion.calls != 0 {
				t.Fatalf("evaluations/routes/decisions/completion=%d/%d/%d/%d", builder.evaluations, routing.calls, decisions.appendCalls, completion.calls)
			}
			precise := fixture.request
			precise.DecidedAt = precise.DecidedAt.Add(time.Nanosecond)
			if _, err := policy.NewRouting(fixture.scope, precise, builder.snapshot, builder.evaluation); !errors.Is(err, policy.ErrInvalid) {
				t.Fatalf("sub-microsecond routing accepted: %v", err)
			}
			changed := fixture.request
			changed.DecidedAt = changed.DecidedAt.Add(time.Microsecond)
			if err := routing.saved.ValidateReplay(fixture.scope, changed); !errors.Is(err, policy.ErrDecisionConflict) {
				t.Fatalf("changed request=%v", err)
			}
		})
	}
}

func TestRoutingHandlerStillCompletesTerminalDecisions(t *testing.T) {
	fixture := newTaskFixture(t)
	builder := &evaluationBuilderStub{snapshot: fixture.decision.Snapshot(), evaluation: fixture.decision.Evaluation()}
	decisions := newDecisionStoreStub()
	completion := &completionStoreStub{}
	routing := &routingStoreStub{}
	handler, err := NewHandlerWithRouting(decisions, builder, completion, routing)
	if err != nil {
		t.Fatal(err)
	}
	work, result := handler.Prepare(t.Context(), fixture.delivery(t))
	if result.Outcome != platformtask.OutcomeComplete || work == nil {
		t.Fatalf("prepare=%+v", result)
	}
	if result := work(t.Context(), transactionStub{}); result.Outcome != platformtask.OutcomeComplete {
		t.Fatalf("effect=%+v", result)
	}
	if routing.calls != 0 || decisions.appendCalls != 1 || completion.calls != 1 {
		t.Fatalf("routes/decisions/completion=%d/%d/%d", routing.calls, decisions.appendCalls, completion.calls)
	}
	if _, err := policy.NewRouting(fixture.scope, fixture.request, fixture.decision.Snapshot(), fixture.decision.Evaluation()); !errors.Is(err, policy.ErrInvalid) {
		t.Fatalf("terminal routing accepted: %v", err)
	}
}
