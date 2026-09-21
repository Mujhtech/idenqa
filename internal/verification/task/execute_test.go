package task

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/Mujhtech/idenqa/internal/verification/synthetic"
)

type checkStore struct {
	check    verification.Check
	saves    int
	deadline time.Time
}

func (store *checkStore) FindCheck(context.Context, tenant.Scope, id.Check) (verification.Check, error) {
	return store.check, nil
}

func (store *checkStore) FindCheckWithin(context.Context, tenant.Scope, postgres.Transaction, id.Check) (verification.Check, error) {
	return store.check, nil
}

func (store *checkStore) VerificationDeadlineWithin(context.Context, tenant.Scope, postgres.Transaction, id.Verification) (time.Time, error) {
	return store.deadline, nil
}

func (store *checkStore) SaveCheckWithin(
	_ context.Context,
	_ tenant.Scope,
	_ postgres.Transaction,
	commit verification.CheckCommit,
) (bool, error) {
	if commit.ExpectedVersion != store.check.Version {
		return false, verification.ErrCheckVersion
	}
	store.check = commit.Check
	store.saves++
	return false, nil
}

type resultIDs struct{ observation int }

func (*resultIDs) NewEvent() (id.Event, error) { return id.ParseEvent("evt_" + taskTestULID) }

func (generator *resultIDs) NewObservation() (id.Observation, error) {
	values := []string{
		"obs_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		"obs_01ARZ3NDEKTSV4RRFFQ69G5FAW",
	}
	value, err := id.ParseObservation(values[generator.observation%len(values)])
	generator.observation++
	return value, err
}

type semanticRetryIDs struct {
	resultIDs
	attempt id.Attempt
	task    id.Task
}

func (generator *semanticRetryIDs) NewAttempt() (id.Attempt, error) { return generator.attempt, nil }
func (generator *semanticRetryIDs) NewTask() (id.Task, error)       { return generator.task, nil }

type retryEnqueuer struct{ intents []platformtask.Intent }

func (enqueuer *retryEnqueuer) EnqueueTx(_ context.Context, _ postgres.Transaction, intents ...platformtask.Intent) error {
	enqueuer.intents = append(enqueuer.intents, intents...)
	return nil
}

func TestExecuteHandlerCommitsProviderResultWithDomainAndTaskFencesSeparated(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 41)
	store := &checkStore{check: check}
	handler := executeHandler(t, store,
		synthetic.Provider{Scenario: synthetic.Success, Now: func() time.Time { return now.Add(time.Second) }},
		synthetic.Model{Scenario: synthetic.Success, Now: func() time.Time { return now.Add(time.Second) }})
	delivery := executeDelivery(t, check, attempt, now, 99)

	work, prepared := handler.Prepare(t.Context(), delivery)
	if prepared.Outcome != platformtask.OutcomeComplete || work == nil {
		t.Fatalf("Prepare() = work %v, result %+v", work != nil, prepared)
	}
	committed := work(t.Context(), nil)
	if committed.Outcome != platformtask.OutcomeComplete || store.saves != 1 ||
		store.check.State != verification.CheckCompleted || store.check.Outcome != verification.CheckPassed {
		t.Fatalf("commit = %+v, saves=%d, check=%s/%s", committed, store.saves, store.check.State, store.check.Outcome)
	}
	if store.check.Attempts()[0].Fence != 41 || delivery.Fence != 99 {
		t.Fatal("domain attempt fence was replaced by the Headgate lease fence")
	}
}

func TestExecuteHandlerPreservesModelAndOperationalFailureMeaning(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		kind     verification.RunnerKind
		provider providerv1.Executor
		model    modelv1.Executor
		state    verification.CheckState
		outcome  verification.CheckOutcome
	}{
		{name: "model inconclusive", kind: verification.RunnerModel,
			provider: synthetic.Provider{Scenario: synthetic.Success, Now: time.Now},
			model:    synthetic.Model{Scenario: synthetic.Inconclusive, Now: time.Now},
			state:    verification.CheckCompleted, outcome: verification.CheckInconclusive},
		{name: "provider unavailable", kind: verification.RunnerProvider,
			provider: synthetic.Provider{Scenario: synthetic.Unavailable, Now: time.Now},
			model:    synthetic.Model{Scenario: synthetic.Success, Now: time.Now},
			state:    verification.CheckFailed},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			check, attempt, now := taskRunningCheck(t, test.kind, 7)
			store := &checkStore{check: check}
			handler := executeHandler(t, store, providerAt(test.provider, now.Add(time.Second)), modelAt(test.model, now.Add(time.Second)))
			work, result := handler.Prepare(t.Context(), executeDelivery(t, check, attempt, now, 8))
			if result.Outcome != platformtask.OutcomeComplete || work(t.Context(), nil).Outcome != platformtask.OutcomeComplete {
				t.Fatalf("execution result = %+v", result)
			}
			if store.check.State != test.state || store.check.Outcome != test.outcome {
				t.Fatalf("check = %s/%s", store.check.State, store.check.Outcome)
			}
		})
	}
}

func TestExecuteHandlerAtomicallySchedulesBoundedSemanticRetry(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 7)
	store := &checkStore{check: check, deadline: now.Add(30 * time.Minute)}
	handler := executeHandler(t, store,
		synthetic.Provider{Scenario: synthetic.Unavailable, Now: func() time.Time { return now.Add(time.Second) }},
		synthetic.Model{Scenario: synthetic.Success, Now: time.Now})
	nextAttempt, _ := id.ParseAttempt("atm_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	nextTask, _ := id.ParseTask("tsk_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	identifiers := &semanticRetryIDs{attempt: nextAttempt, task: nextTask}
	enqueuer := &retryEnqueuer{}
	if err := handler.WithSemanticRetries(identifiers, enqueuer); err != nil {
		t.Fatal(err)
	}
	work, result := handler.Prepare(t.Context(), executeDelivery(t, check, attempt, now, 8))
	if result.Outcome != platformtask.OutcomeComplete || work == nil {
		t.Fatalf("prepare = %+v", result)
	}
	if committed := work(t.Context(), nil); committed.Outcome != platformtask.OutcomeComplete {
		t.Fatalf("commit = %+v", committed)
	}
	attempts := store.check.Attempts()
	if len(attempts) != 2 || attempts[0].State != verification.AttemptFailed ||
		attempts[1].State != verification.AttemptRunning || attempts[1].Number != 2 || attempts[1].Fence != 8 {
		t.Fatalf("attempts = %+v", attempts)
	}
	if got := attempts[1].StartedAt; !got.Equal(now.Add(2 * time.Second)) {
		t.Fatalf("retry scheduled at %s", got)
	}
	if len(enqueuer.intents) != 1 || enqueuer.intents[0].Key() != ExecuteKey {
		t.Fatalf("retry intents = %+v", enqueuer.intents)
	}
}

func TestExecuteHandlerQuarantinesMalformedResultWithoutWriting(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 5)
	store := &checkStore{check: check}
	handler := executeHandler(t, store,
		synthetic.Provider{Scenario: synthetic.Malformed, Now: func() time.Time { return now.Add(time.Second) }},
		synthetic.Model{Scenario: synthetic.Success, Now: func() time.Time { return now.Add(time.Second) }})
	work, result := handler.Prepare(t.Context(), executeDelivery(t, check, attempt, now, 6))
	if result.Outcome != platformtask.OutcomeComplete {
		t.Fatalf("Prepare() = %+v", result)
	}
	if committed := work(t.Context(), nil); committed.Outcome != platformtask.OutcomeQuarantine || store.saves != 0 {
		t.Fatalf("commit = %+v, saves=%d", committed, store.saves)
	}
}

type documentProvider struct {
	observation providerv1.DocumentObservation
	completedAt time.Time
}

func (executor documentProvider) Execute(_ context.Context, request providerv1.Request) (providerv1.Result, error) {
	return providerv1.Result{
		Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
		Signals:  []providerv1.Signal{{Name: "idenqa.signal.document_quality", Outcome: providerv1.SignalOutcomeSatisfied}},
		Document: &executor.observation, CompletedAt: executor.completedAt,
	}, nil
}

func TestExecuteHandlerConsumesDocumentObservationBeforeCommit(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 23)
	store := &checkStore{check: check}
	observation := providerv1.DocumentObservation{Fields: []providerv1.DocumentField{
		{Name: "document_number", Value: "X90000009"},
		{Name: "document_type", Value: "passport"},
		{Name: "issuing_country", Value: "UTO"},
	}}
	handler := executeHandler(t, store,
		documentProvider{observation: observation, completedAt: now.Add(time.Second)},
		synthetic.Model{Scenario: synthetic.Success, Now: time.Now})
	work, prepared := handler.Prepare(t.Context(), executeDelivery(t, check, attempt, now, 24))
	if prepared.Outcome != platformtask.OutcomeComplete || work == nil {
		t.Fatalf("Prepare() = work %v, result %+v", work != nil, prepared)
	}
	if committed := work(t.Context(), nil); committed.Outcome != platformtask.OutcomeComplete {
		t.Fatalf("commit = %+v", committed)
	}
	if store.check.Outcome == verification.CheckPassed {
		t.Fatalf("document observation produced an identity outcome: %s", store.check.Outcome)
	}
	observations := store.check.Attempts()[0].Observations
	derived := false
	for _, observation := range observations {
		if observation.Signal.Name == verification.SignalDocumentClassification {
			derived = true
		}
	}
	if !derived {
		t.Fatalf("derived document signals are missing: %+v", observations)
	}
	encoded, err := json.Marshal(observations)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "X90000009") {
		t.Fatal("persisted observations retained a raw document value")
	}
}

type failingProvider struct{ err error }

func (provider failingProvider) Execute(context.Context, providerv1.Request) (providerv1.Result, error) {
	return providerv1.Result{}, provider.err
}

type pendingProvider struct{}

func (pendingProvider) Execute(context.Context, providerv1.Request) (providerv1.Result, error) {
	return providerv1.Result{}, provider.ErrDispatchPending
}

type externalWaitSpy struct {
	enters, leaves     int
	enterErr, leaveErr error
	enterActor         string
	leaveActor         string
}

func (spy *externalWaitSpy) Enter(_ context.Context, _ tenant.Scope, _ id.Verification, _ id.Check, actor string) error {
	spy.enters++
	spy.enterActor = actor
	return spy.enterErr
}

func (spy *externalWaitSpy) Leave(_ context.Context, _ postgres.Transaction, _ tenant.Scope, _ id.Verification, actor string) error {
	spy.leaves++
	spy.leaveActor = actor
	return spy.leaveErr
}

func TestExecuteHandlerClassifiesExternalFailureBeforeTransaction(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 5)
	store := &checkStore{check: check}
	handler := executeHandler(t, store, failingProvider{err: errors.New("provider unavailable")},
		synthetic.Model{Scenario: synthetic.Success, Now: time.Now})
	work, result := handler.Prepare(t.Context(), executeDelivery(t, check, attempt, now, 6))
	if work != nil || result.Outcome != platformtask.OutcomeRetry || result.Class != platformtask.RetryClassUnavailable || store.saves != 0 {
		t.Fatalf("Prepare() = work %v, result %+v", work != nil, result)
	}
}

func TestExecuteHandlerEntersExternalWaitForPendingProviderDispatch(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 9)
	store := &checkStore{check: check}
	handler := executeHandler(t, store, pendingProvider{},
		synthetic.Model{Scenario: synthetic.Success, Now: time.Now})
	wait := &externalWaitSpy{}
	if err := handler.WithExternalWait(wait); err != nil {
		t.Fatal(err)
	}
	work, result := handler.Prepare(t.Context(), executeDelivery(t, check, attempt, now, 10))
	if work != nil || result.Outcome != platformtask.OutcomeRetry || result.Class != platformtask.RetryClassUnavailable {
		t.Fatalf("Prepare() = work %v, result %+v", work != nil, result)
	}
	if wait.enters != 1 || wait.leaves != 0 || wait.enterActor != "tsk_"+taskTestULID {
		t.Fatalf("external wait projection = enters %d, leaves %d, actor %q", wait.enters, wait.leaves, wait.enterActor)
	}
}

func TestExecuteHandlerRetriesWhenExternalWaitEntryFails(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 11)
	store := &checkStore{check: check}
	handler := executeHandler(t, store, pendingProvider{},
		synthetic.Model{Scenario: synthetic.Success, Now: time.Now})
	wait := &externalWaitSpy{enterErr: errors.New("projection unavailable")}
	if err := handler.WithExternalWait(wait); err != nil {
		t.Fatal(err)
	}
	work, result := handler.Prepare(t.Context(), executeDelivery(t, check, attempt, now, 12))
	if work != nil || result.Outcome != platformtask.OutcomeRetry || result.Class != platformtask.RetryClassUnavailable || wait.enters != 1 {
		t.Fatalf("Prepare() = work %v, result %+v, enters %d", work != nil, result, wait.enters)
	}
}

func TestExecuteHandlerPendingDispatchWithoutExternalWaitStaysProcessing(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 13)
	store := &checkStore{check: check}
	handler := executeHandler(t, store, pendingProvider{},
		synthetic.Model{Scenario: synthetic.Success, Now: time.Now})
	work, result := handler.Prepare(t.Context(), executeDelivery(t, check, attempt, now, 14))
	if work != nil || result.Outcome != platformtask.OutcomeRetry || result.Class != platformtask.RetryClassUnavailable {
		t.Fatalf("Prepare() = work %v, result %+v", work != nil, result)
	}
}

func TestExecuteHandlerLeavesExternalWaitInsideAppliedCommit(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 15)
	store := &checkStore{check: check}
	handler := executeHandler(t, store,
		synthetic.Provider{Scenario: synthetic.Success, Now: func() time.Time { return now.Add(time.Second) }},
		synthetic.Model{Scenario: synthetic.Success, Now: time.Now})
	wait := &externalWaitSpy{}
	if err := handler.WithExternalWait(wait); err != nil {
		t.Fatal(err)
	}
	work, result := handler.Prepare(t.Context(), executeDelivery(t, check, attempt, now, 16))
	if work == nil || result.Outcome != platformtask.OutcomeComplete {
		t.Fatalf("Prepare() = work %v, result %+v", work != nil, result)
	}
	committed := work(t.Context(), nil)
	if committed.Outcome != platformtask.OutcomeComplete || wait.leaves != 1 || wait.leaveActor != "tsk_"+taskTestULID {
		t.Fatalf("commit = %+v, leaves %d, actor %q", committed, wait.leaves, wait.leaveActor)
	}
}

func TestExecuteHandlerRetriesWhenExternalWaitLeaveFails(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 17)
	store := &checkStore{check: check}
	handler := executeHandler(t, store,
		synthetic.Provider{Scenario: synthetic.Success, Now: func() time.Time { return now.Add(time.Second) }},
		synthetic.Model{Scenario: synthetic.Success, Now: time.Now})
	wait := &externalWaitSpy{leaveErr: errors.New("projection unavailable")}
	if err := handler.WithExternalWait(wait); err != nil {
		t.Fatal(err)
	}
	work, result := handler.Prepare(t.Context(), executeDelivery(t, check, attempt, now, 18))
	if work == nil || result.Outcome != platformtask.OutcomeComplete {
		t.Fatalf("Prepare() = work %v, result %+v", work != nil, result)
	}
	committed := work(t.Context(), nil)
	if committed.Outcome != platformtask.OutcomeRetry || committed.Class != platformtask.RetryClassUnavailable || wait.leaves != 1 {
		t.Fatalf("commit = %+v, leaves %d", committed, wait.leaves)
	}
}

func executeHandler(t *testing.T, store CheckStore, provider providerv1.Executor, model modelv1.Executor) *ExecuteHandler {
	t.Helper()
	handler, err := NewExecuteHandler(store, &resultIDs{}, provider, model)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func taskRunningCheck(t *testing.T, kind verification.RunnerKind, fence uint64) (verification.Check, verification.Attempt, time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	checkID, _ := id.ParseCheck("chk_" + taskTestULID)
	tenantID, _ := id.ParseTenant("ten_" + taskTestULID)
	verificationID, _ := id.ParseVerification("ver_" + taskTestULID)
	attemptID, _ := id.ParseAttempt("atm_" + taskTestULID)
	check, err := verification.NewCheck(checkID, tenantID, verificationID, "synthetic.check", now)
	if err != nil {
		t.Fatal(err)
	}
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	attempt := verification.Attempt{ID: attemptID, Number: 1, Fence: fence, RunnerKind: kind,
		Provenance: verification.Provenance{RunnerID: "synthetic.runner", RunnerVersion: "1.0.0",
			PackageDigest: digest, ContractMajor: 1, RequestDigest: digest, Configuration: digest},
		State: verification.AttemptRunning, StartedAt: now, Deadline: now.Add(time.Minute)}
	if err := check.BeginAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	return check, attempt, now
}

func executeDelivery(t *testing.T, check verification.Check, attempt verification.Attempt, now time.Time, taskFence uint64) platformtask.Delivery {
	t.Helper()
	taskID, _ := id.ParseTask("tsk_" + taskTestULID)
	scope, _ := tenant.NewScope(check.TenantID)
	intent, err := NewExecuteIntent(fixedTaskIDs{taskID}, scope, ExecutePayload{CheckID: check.ID, AttemptID: attempt.ID},
		IntentMetadata{ScheduledAt: now, Deadline: attempt.Deadline})
	if err != nil {
		t.Fatal(err)
	}
	return platformtask.Delivery{Intent: intent, Attempt: 1, Fence: taskFence}
}

func providerAt(executor providerv1.Executor, now time.Time) providerv1.Executor {
	if value, ok := executor.(synthetic.Provider); ok {
		value.Now = func() time.Time { return now }
		return value
	}
	return executor
}

func modelAt(executor modelv1.Executor, now time.Time) modelv1.Executor {
	if value, ok := executor.(synthetic.Model); ok {
		value.Now = func() time.Time { return now }
		return value
	}
	return executor
}

func TestAsyncDeadlineCommitsOperationalTimeoutWithoutCallingProvider(t *testing.T) {
	t.Parallel()
	check, attempt, now := taskRunningCheck(t, verification.RunnerProvider, 41)
	store := &checkStore{check: check}
	// The fixed attempt is already expired. An empty synthetic executor would
	// fail if called; timeout completion must require no external execution.
	handler := executeHandler(t, store, synthetic.Provider{}, synthetic.Model{})
	taskID, _ := id.ParseTask("tsk_" + taskTestULID)
	scope, _ := tenant.NewScope(check.TenantID)
	intent, err := NewAsyncExecuteIntent(fixedTaskIDs{taskID}, scope, ExecutePayload{CheckID: check.ID, AttemptID: attempt.ID}, IntentMetadata{ScheduledAt: now, Deadline: attempt.Deadline})
	if err != nil {
		t.Fatal(err)
	}
	if !intent.Deadline().After(attempt.Deadline) {
		t.Fatal("no time budget for timeout commit")
	}
	work, prepared := handler.Prepare(t.Context(), platformtask.Delivery{Intent: intent, Attempt: 2, Fence: 99})
	if prepared.Outcome != platformtask.OutcomeComplete || work == nil {
		t.Fatalf("timeout preparation: %+v", prepared)
	}
	result := work(t.Context(), nil)
	if result.Outcome != platformtask.OutcomeComplete || store.check.State != verification.CheckTimedOut || len(store.check.Attempts()[0].Observations) != 0 {
		t.Fatalf("timeout result: %+v state=%v", result, store.check.State)
	}
}
