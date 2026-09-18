// Package task owns durable review policy re-evaluation, separate from initial policy.author.
package task

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// EvaluationKey is a distinct versioned contract so original policy.author replay remains intact.
var EvaluationKey = func() platformtask.Key {
	name, err := platformtask.NewName("review.evaluate")
	if err != nil {
		panic(err)
	}
	return platformtask.Key{Name: name, Version: 1}
}()

type identifiers interface{ NewTask() (id.Task, error) }

// Target is reference-only installation-wide discovery output.
type Target struct {
	TenantID id.Tenant
	Request  review.EvaluationRequest
}

// Repository is the narrow worker evaluation boundary.
type Repository interface {
	ListReady(context.Context, time.Time, int) ([]Target, error)
	Find(context.Context, tenant.Scope, review.EvaluationRequest) (review.Evaluation, error)
	Build(context.Context, tenant.Scope, review.EvaluationRequest) (review.Evaluation, error)
	CommitWithin(context.Context, tenant.Scope, postgres.Transaction, review.Evaluation, id.Task) error
}

// Worker supplies discovery and fence-only execution for immutable finding versions.
type Worker struct {
	repository Repository
	ids        identifiers
	enqueuer   platformtask.Enqueuer
	clock      clock.Clock
}

// New constructs review task composition without transport or domain dependencies on Headgate.
func New(repository Repository, ids identifiers, enqueuer platformtask.Enqueuer, source clock.Clock) (*Worker, error) {
	if repository == nil || ids == nil || enqueuer == nil || source == nil {
		return nil, review.ErrInvalid
	}
	return &Worker{repository: repository, ids: ids, enqueuer: enqueuer, clock: source}, nil
}

// Schedule discovers durable accepted-finding requests after restart and enqueues idempotently.
func (worker *Worker) Schedule(ctx context.Context) error {
	now := worker.clock.Now().UTC()
	targets, err := worker.repository.ListReady(ctx, now, 100)
	if err != nil {
		return err
	}
	if len(targets) > 100 {
		return review.ErrInvalid
	}
	for _, target := range targets {
		if target.TenantID.IsZero() || target.Request.CaseID.IsZero() || target.Request.Version < 1 {
			return review.ErrInvalid
		}
		taskID, err := worker.ids.NewTask()
		if err != nil {
			return err
		}
		intent, err := platformtask.NewIntent(platformtask.IntentSpec{ID: taskID, TenantID: target.TenantID, Key: EvaluationKey, Queue: taskheadgate.QueueVerification, PartitionKey: target.TenantID.String(), IdempotencyKey: fmt.Sprintf("review.evaluate:%s:%d", target.Request.CaseID.String(), target.Request.Version), Payload: struct {
			CaseID  string `json:"case_id"`
			Version int64  `json:"version"`
		}{target.Request.CaseID.String(), target.Request.Version}, ScheduledAt: now, Deadline: now.Add(2 * time.Minute), Retry: platformtask.RetryPolicy{MaxAttempts: 5, InitialBackoff: time.Second, MaximumBackoff: 30 * time.Second, JitterPercent: 20}, Retention: 30 * 24 * time.Hour})
		if err != nil {
			return err
		}
		if err := worker.enqueuer.Enqueue(ctx, intent); err != nil {
			return err
		}
	}
	return nil
}

// Handle rejects drivers lacking transaction fencing.
func (*Worker) Handle(context.Context, platformtask.Delivery) platformtask.Result {
	return platformtask.Quarantine(review.ErrForbidden)
}

// Prepare restores committed results before evaluating current immutable inputs.
func (worker *Worker) Prepare(ctx context.Context, delivery platformtask.Delivery) (platformtask.TransactionWork, platformtask.Result) {
	var wire struct {
		CaseID  string `json:"case_id"`
		Version int64  `json:"version"`
	}
	decoder := json.NewDecoder(bytes.NewReader(delivery.Intent.Payload()))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&wire) != nil || wire.CaseID == "" || wire.Version < 1 {
		return nil, platformtask.Quarantine(review.ErrInvalid)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, platformtask.Quarantine(review.ErrInvalid)
	}
	caseID, err := id.ParseReviewCase(wire.CaseID)
	if err != nil {
		return nil, platformtask.Quarantine(review.ErrInvalid)
	}
	request := review.EvaluationRequest{CaseID: caseID, Version: wire.Version}
	scope, err := tenant.NewScope(delivery.Intent.TenantID())
	if err != nil {
		return nil, platformtask.Quarantine(err)
	}
	value, err := worker.repository.Find(ctx, scope, request)
	if errors.Is(err, review.ErrEvaluationNotFound) {
		value, err = worker.repository.Build(ctx, scope, request)
	}
	if err != nil {
		return nil, classify(err)
	}
	return func(ctx context.Context, tx postgres.Transaction) platformtask.Result {
		return classify(worker.repository.CommitWithin(ctx, scope, tx, value, delivery.Intent.ID()))
	}, platformtask.Complete()
}
func classify(err error) platformtask.Result {
	if err == nil {
		return platformtask.Complete()
	}
	if errors.Is(err, review.ErrInvalid) || errors.Is(err, review.ErrForbidden) || errors.Is(err, policy.ErrInvalid) || errors.Is(err, policy.ErrReproduction) || errors.Is(err, policy.ErrStaleFact) || errors.Is(err, authority.ErrProcessingNotPermitted) {
		return platformtask.Quarantine(err)
	}
	return platformtask.Retry(platformtask.RetryClassUnavailable, err)
}
