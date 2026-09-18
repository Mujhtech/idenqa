package task

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type testClock struct{ at time.Time }

func (clock testClock) Now() time.Time { return clock.at }

type evaluationRepository struct {
	targets         []Target
	saved           bool
	builds, commits int
	request         review.EvaluationRequest
}

func (repository *evaluationRepository) ListReady(context.Context, time.Time, int) ([]Target, error) {
	return repository.targets, nil
}
func (repository *evaluationRepository) Find(_ context.Context, _ tenant.Scope, request review.EvaluationRequest) (review.Evaluation, error) {
	repository.request = request
	if repository.saved {
		return review.Evaluation{Request: request}, nil
	}
	return review.Evaluation{}, review.ErrEvaluationNotFound
}
func (repository *evaluationRepository) Build(_ context.Context, _ tenant.Scope, request review.EvaluationRequest) (review.Evaluation, error) {
	repository.builds++
	return review.Evaluation{Request: request}, nil
}
func (repository *evaluationRepository) CommitWithin(context.Context, tenant.Scope, postgres.Transaction, review.Evaluation, id.Task) error {
	repository.commits++
	repository.saved = true
	return nil
}

type evaluationQueue struct{ intents []platformtask.Intent }

func (queue *evaluationQueue) Enqueue(_ context.Context, intents ...platformtask.Intent) error {
	queue.intents = append(queue.intents, intents...)
	return nil
}

func TestEvaluationTaskUsesDistinctStableIdentityAndCommittedReplay(t *testing.T) {
	ids, _ := id.NewSystemGenerator()
	tenantID, _ := ids.NewTenant()
	caseID, _ := ids.NewReviewCase()
	request := review.EvaluationRequest{CaseID: caseID, Version: 3}
	repository := &evaluationRepository{targets: []Target{{TenantID: tenantID, Request: request}}}
	queue := &evaluationQueue{}
	worker, err := New(repository, ids, queue, testClock{time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := worker.Schedule(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if len(queue.intents) != 2 || queue.intents[0].IdempotencyKey() != queue.intents[1].IdempotencyKey() || queue.intents[0].Key() != EvaluationKey {
		t.Fatal("unstable task identity")
	}
	var wire map[string]any
	if err := json.Unmarshal(queue.intents[0].Payload(), &wire); err != nil || wire["case_id"] != caseID.String() {
		t.Fatalf("wire=%v %v", wire, err)
	}
	for range 2 {
		work, result := worker.Prepare(t.Context(), platformtask.Delivery{Intent: queue.intents[0]})
		if result.Outcome != platformtask.OutcomeComplete || work == nil {
			t.Fatalf("prepare=%+v", result)
		}
		if result := work(t.Context(), nil); result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("work=%+v", result)
		}
	}
	if repository.builds != 1 || repository.commits != 2 || repository.request != request {
		t.Fatalf("builds=%d commits=%d request=%+v", repository.builds, repository.commits, repository.request)
	}
	if worker.Handle(t.Context(), platformtask.Delivery{}).Outcome != platformtask.OutcomeQuarantine {
		t.Fatal("unfenced execution allowed")
	}
}
