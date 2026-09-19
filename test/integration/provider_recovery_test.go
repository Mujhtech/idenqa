//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/Mujhtech/idenqa/internal/verification/synthetic"
	verificationtask "github.com/Mujhtech/idenqa/internal/verification/task"
)

// providerRecoveryFixture is one durably processing verification with an exact
// asynchronous provider request, one running check/attempt, and the same
// execution delivery a worker would receive. Every provider effect in these
// tests goes through the owned RequestStore, CheckStore, ExternalWaitStore and
// ExecuteHandler against Postgres; only the remote runner is substituted.
type providerRecoveryFixture struct {
	checkID   id.Check
	attemptID id.Attempt
	callback  id.ProviderCallback
	request   providerv1.Request
	intent    platformtask.Intent
}

// seedProviderRecoveryFixture accepts the fixture's evidence, moves the session
// into processing, then seeds the exact check, attempt and provider request the
// provider worker would have prepared. Accepted uploads keep the session
// authority valid for execution-state validation under RLS.
func seedProviderRecoveryFixture(t *testing.T, f captureAcceptanceFixture) providerRecoveryFixture {
	t.Helper()
	evidenceStore, mutations := f.prepare(t)
	for _, mutation := range mutations {
		if _, err := evidenceStore.AcceptUpload(t.Context(), f.scope, mutation); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	var version int64
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT state,version FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.creation.Session.ID().String()).Scan(&state, &version); err != nil {
		t.Fatal(err)
	}
	if state == string(verification.SessionStateCollecting) {
		lifecycle, err := verificationpostgres.NewLifecycleStore(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := lifecycle.Apply(t.Context(), f.scope, verification.LifecycleCommand{
			EventID: mustCaptureEvent(t, f.ids), VerificationID: f.creation.Session.ID(), ExpectedVersion: version,
			Target: verification.SessionStateProcessing, ActorID: f.declaration.Record().CreatedBy.String(), OccurredAt: f.now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	started := f.now
	// The real worker deadline is wall-clock bounded, so the durable attempt
	// stays open well beyond the fixed observation clock.
	deadline := time.Now().UTC().Truncate(time.Microsecond).Add(23 * time.Hour)
	callback, err := f.ids.NewProviderCallback()
	if err != nil {
		t.Fatal(err)
	}
	checkValue := seedCallbackCheck(t, f, f.creation.Session.ID().String(), started)
	request := callbackIntegrationRequest(t, f, f.creation.Session.ID().String(), callback.String(), deadline)
	attemptValue := seedCallbackRequest(t, f, f.creation.Session.ID(), checkValue, request, started, deadline)
	request.AttemptID = attemptValue
	checkID, err := id.ParseCheck(checkValue)
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := id.ParseAttempt(attemptValue)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := verificationtask.NewAsyncExecuteIntent(f.ids, f.scope, verificationtask.ExecutePayload{CheckID: checkID, AttemptID: attemptID}, verificationtask.IntentMetadata{
		ScheduledAt: f.now.Add(2 * time.Minute),
		Deadline:    deadline,
	})
	if err != nil {
		t.Fatal(err)
	}
	return providerRecoveryFixture{checkID: checkID, attemptID: attemptID, callback: callback, request: request, intent: intent}
}

// recoveryAdvancer substitutes only the remote runner call while keeping the
// durable claim, fence and result persistence real.
type recoveryAdvancer struct {
	calls   atomic.Int32
	advance func(call int32, resume bool) (providerv1.Progress, error)
}

func (advancer *recoveryAdvancer) Advance(_ context.Context, _ providerv1.Request, resume bool) (providerv1.Progress, error) {
	return advancer.advance(advancer.calls.Add(1), resume)
}

func (advancer *recoveryAdvancer) callCount() int { return int(advancer.calls.Load()) }

// recoveryRejectingVerifier models an adapter-authenticated rejection or an
// unavailable callback verification without any normalized progress.
type recoveryRejectingVerifier struct{ err error }

func (verifier recoveryRejectingVerifier) VerifyCallback(context.Context, providerv1.Request, providerv1.CallbackEnvelope) (providerv1.Progress, error) {
	return providerv1.Progress{}, verifier.err
}

// failingProviderCommitStore injects one failure after the fenced result commit
// has written its check, inbox and outbox effects, so the enclosing transaction
// must roll every one of them back. Dispatch persistence stays untouched.
type failingProviderCommitStore struct {
	*verificationpostgres.CheckStore
	fail atomic.Bool
}

var errProviderRecoveryCommit = errors.New("injected provider result commit failure")

func (store *failingProviderCommitStore) SaveCheckWithin(ctx context.Context, scope tenant.Scope, transaction platformpostgres.Transaction, commit verification.CheckCommit) (bool, error) {
	duplicate, err := store.CheckStore.SaveCheckWithin(ctx, scope, transaction, commit)
	if err != nil || !store.fail.Load() {
		return duplicate, err
	}
	return false, errProviderRecoveryCommit
}

// providerRecoveryWorker is one worker generation: fresh stores, fences and
// runner client over the same durable Postgres state.
type providerRecoveryWorker struct {
	handler  *verificationtask.ExecuteHandler
	executor *provider.AsyncExecutor
}

func newProviderRecoveryWorker(
	t *testing.T,
	f captureAcceptanceFixture,
	source fixedIntegrationClock,
	advance *recoveryAdvancer,
	checks verificationtask.CheckStore,
) *providerRecoveryWorker {
	t.Helper()
	requests, err := providerpostgres.NewRequestStore(f.runtime, source)
	if err != nil {
		t.Fatal(err)
	}
	executor, err := provider.NewAsyncExecutor(requests, advance, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	if checks == nil {
		checks, err = verificationpostgres.NewGuardedCheckStore(f.runtime, integrationProtector{}, source)
		if err != nil {
			t.Fatal(err)
		}
	}
	lifecycle, err := verificationpostgres.NewLifecycleStore(f.runtime, integrationProtector{}, source)
	if err != nil {
		t.Fatal(err)
	}
	wait, err := verificationpostgres.NewExternalWaitStore(f.runtime, lifecycle, f.ids, source)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := verificationtask.NewExecuteHandlerWithRequests(checks, f.ids, executor, synthetic.Model{Scenario: synthetic.Success, Now: source.Now}, requests)
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.WithExternalWait(wait); err != nil {
		t.Fatal(err)
	}
	return &providerRecoveryWorker{handler: handler, executor: executor}
}

func (worker *providerRecoveryWorker) prepare(t *testing.T, fixture providerRecoveryFixture) (platformtask.TransactionWork, platformtask.Result) {
	t.Helper()
	return worker.handler.Prepare(t.Context(), platformtask.Delivery{Intent: fixture.intent, Attempt: 1, Fence: 1})
}

// runProviderRecoveryCommit runs the fenced transaction effect exactly as the
// worker driver does and reports both the classified task result and the
// transaction outcome.
func runProviderRecoveryCommit(t *testing.T, f captureAcceptanceFixture, work platformtask.TransactionWork) (platformtask.Result, error) {
	t.Helper()
	var result platformtask.Result
	err := f.runtime.WithinTransaction(t.Context(), platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		result = work(ctx, tx)
		if result.Outcome == platformtask.OutcomeComplete {
			return nil
		}
		return result.Err
	})
	return result, err
}

func recoveryProviderResult(fixture providerRecoveryFixture, completedAt time.Time, outcome providerv1.SignalOutcome) providerv1.Result {
	return providerv1.Result{
		Contract: fixture.request.Contract, AttemptID: fixture.request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted,
		Signals: []providerv1.Signal{{Name: "idenqa.signal.provider_job", Outcome: outcome}}, CompletedAt: completedAt.UTC(),
	}
}

func recoveryCallbackProgress(fixture providerRecoveryFixture, job string, completedAt time.Time, outcome providerv1.SignalOutcome) providerv1.Progress {
	result := recoveryProviderResult(fixture, completedAt, outcome)
	return providerv1.Progress{ProviderJobID: job, ReplayID: job, Result: &result}
}

func newProviderRecoveryCallbackService(t *testing.T, f captureAcceptanceFixture, verifier providerv1.CallbackVerifier) *provider.CallbackService {
	t.Helper()
	requests, err := providerpostgres.NewRequestStore(f.runtime, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	service, err := provider.NewCallbackService(requests, verifier, requests, nil, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func recoveryCallbackEnvelope() providerv1.CallbackEnvelope {
	return providerv1.CallbackEnvelope{
		Method:  "POST",
		Headers: []providerv1.CallbackHeader{{Name: "Content-Type", Value: "application/json"}},
		Body:    []byte(`{"status":"clear"}`),
	}
}

type providerRecoveryCounts struct {
	observations, inbox, dispatchResults, awaitingExternal, resumedProcessing int
	checkState, sessionState                                                  string
}

// readProviderRecoveryCounts inspects durable effects with the administrative
// pool while every write above used the forced-RLS runtime role.
func readProviderRecoveryCounts(t *testing.T, f captureAcceptanceFixture, fixture providerRecoveryFixture) providerRecoveryCounts {
	t.Helper()
	var counts providerRecoveryCounts
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT
		(SELECT count(*) FROM idenqa.verification_observations WHERE tenant_id=$1 AND verification_id=$2),
		(SELECT count(*) FROM idenqa.verification_result_inbox WHERE tenant_id=$1 AND verification_id=$2),
		(SELECT count(*) FROM idenqa.provider_dispatches WHERE tenant_id=$1 AND attempt_id=$4 AND result_body IS NOT NULL),
		(SELECT count(*) FROM idenqa.verification_transitions WHERE tenant_id=$1 AND verification_id=$2 AND to_state='awaiting_external'),
		(SELECT count(*) FROM idenqa.verification_transitions WHERE tenant_id=$1 AND verification_id=$2 AND from_state='awaiting_external' AND to_state='processing'),
		(SELECT state FROM idenqa.verification_checks WHERE tenant_id=$1 AND id=$3),
		(SELECT state FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2)`,
		f.scope.ID().String(), f.creation.Session.ID().String(), fixture.checkID.String(), fixture.attemptID.String()).
		Scan(&counts.observations, &counts.inbox, &counts.dispatchResults, &counts.awaitingExternal, &counts.resumedProcessing, &counts.checkState, &counts.sessionState); err != nil {
		t.Fatal(err)
	}
	return counts
}

func recoveryCallbackReceipts(t *testing.T, f captureAcceptanceFixture, fixture providerRecoveryFixture) int {
	t.Helper()
	var receipts int
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.provider_callback_receipts WHERE tenant_id=$1 AND attempt_id=$2`, f.scope.ID().String(), fixture.attemptID.String()).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	return receipts
}

func assertRecoveryPending(t *testing.T, f captureAcceptanceFixture, fixture providerRecoveryFixture) {
	t.Helper()
	counts := readProviderRecoveryCounts(t, f, fixture)
	if counts.observations != 0 || counts.inbox != 0 || counts.dispatchResults != 0 || counts.awaitingExternal != 1 ||
		counts.checkState != string(verification.CheckRunning) || counts.sessionState != string(verification.SessionStateAwaitingExternal) {
		t.Fatalf("pending state = %+v", counts)
	}
}

// assertRecoverySingleResult proves exactly one authoritative provider result
// was accepted for the attempt: one observation set, one inbox claim and one
// completed check with the session left in processing.
func assertRecoverySingleResult(t *testing.T, f captureAcceptanceFixture, fixture providerRecoveryFixture) {
	t.Helper()
	counts := readProviderRecoveryCounts(t, f, fixture)
	if counts.observations != 1 || counts.inbox != 1 || counts.dispatchResults != 1 ||
		counts.checkState != string(verification.CheckCompleted) || counts.sessionState != string(verification.SessionStateProcessing) {
		t.Fatalf("single result state = %+v", counts)
	}
}

// assertRecoveryDirectResult additionally proves a terminal callback or status
// result never created an awaiting_external edge on the direct completion path.
func assertRecoveryDirectResult(t *testing.T, f captureAcceptanceFixture, fixture providerRecoveryFixture) {
	t.Helper()
	assertRecoverySingleResult(t, f, fixture)
	counts := readProviderRecoveryCounts(t, f, fixture)
	if counts.awaitingExternal != 0 || counts.resumedProcessing != 0 {
		t.Fatalf("direct completion created external wait edges = %+v", counts)
	}
}

// assertRecoveryCommitted additionally proves the exactly-once
// awaiting_external -> processing edge of a recovered external wait.
func assertRecoveryCommitted(t *testing.T, f captureAcceptanceFixture, fixture providerRecoveryFixture) {
	t.Helper()
	assertRecoverySingleResult(t, f, fixture)
	counts := readProviderRecoveryCounts(t, f, fixture)
	if counts.awaitingExternal != 1 || counts.resumedProcessing != 1 {
		t.Fatalf("external wait edges = %+v", counts)
	}
}

// assertRecoveryReplayAddsNothing proves a redelivered task whose attempt and
// check are already terminal produces no transaction work and no new durable
// effect; the fenced retry classification stays bounded.
func assertRecoveryReplayAddsNothing(t *testing.T, fixture providerRecoveryFixture, worker *providerRecoveryWorker, verify func()) {
	t.Helper()
	work, result := worker.prepare(t, fixture)
	if work != nil || result.Outcome != platformtask.OutcomeRetry {
		t.Fatalf("replayed delivery = work=%v result=%+v", work != nil, result)
	}
	verify()
}

// TestProviderCallbackReceiptSurvivesWorkerReplacement proves one verified
// terminal callback is durably receipted while the executing worker is gone,
// and that a replacement worker adopts it without polling the provider: exactly
// one result, one observation set and one awaiting_external -> processing edge.
// Identical redelivery adds nothing before or after the replacement commit.
func TestProviderCallbackReceiptSurvivesWorkerReplacement(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		ctx := t.Context()
		fixture := seedProviderRecoveryFixture(t, f)

		pending := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
			if resume {
				t.Fatalf("initial submission used a recovery resume: call=%d", call)
			}
			return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1"}, nil
		}}
		first := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2 * time.Minute)}, pending, nil)
		work, result := first.prepare(t, fixture)
		if work != nil || result.Outcome != platformtask.OutcomeRetry || result.Class != platformtask.RetryClassUnavailable {
			t.Fatalf("pending execution = work=%v result=%+v", work != nil, result)
		}
		assertRecoveryPending(t, f, fixture)
		// The worker stops here; the callback arrives through the API ingress
		// and is verified into a durable receipt only.
		progress := recoveryCallbackProgress(fixture, "job-1", f.now.Add(time.Minute), providerv1.SignalOutcomeSatisfied)
		service := newProviderRecoveryCallbackService(t, f, callbackIntegrationVerifier{progress: progress})
		accepted, err := service.Handle(ctx, fixture.callback.String(), recoveryCallbackEnvelope())
		if err != nil || accepted.Status != provider.CallbackAccepted || recoveryCallbackReceipts(t, f, fixture) != 1 {
			t.Fatalf("callback receipt = %+v receipts=%d err=%v", accepted, recoveryCallbackReceipts(t, f, fixture), err)
		}
		// A fresh callback service (process replacement) sees the identical
		// delivery as an exact duplicate.
		duplicate, err := newProviderRecoveryCallbackService(t, f, callbackIntegrationVerifier{progress: progress}).
			Handle(ctx, fixture.callback.String(), recoveryCallbackEnvelope())
		if err != nil || duplicate.Status != provider.CallbackDuplicate {
			t.Fatalf("pre-restart duplicate = %+v err=%v", duplicate, err)
		}

		replacement := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
			t.Fatalf("replacement worker polled the provider instead of adopting its receipt: call=%d", call)
			return providerv1.Progress{}, nil
		}}
		second := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2*time.Minute + 10*time.Second)}, replacement, nil)
		work, result = second.prepare(t, fixture)
		if work == nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("replacement prepare = work=%v result=%+v", work != nil, result)
		}
		if result, err = runProviderRecoveryCommit(t, f, work); err != nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("replacement commit = %+v err=%v", result, err)
		}
		assertRecoveryCommitted(t, f, fixture)
		if replacement.callCount() != 0 {
			t.Fatalf("replacement polled the provider %d times", replacement.callCount())
		}

		// After the terminal commit the same reference fails closed with no
		// additional effect, and a full delivery replay stays a duplicate.
		if _, err := newProviderRecoveryCallbackService(t, f, callbackIntegrationVerifier{progress: progress}).
			Handle(ctx, fixture.callback.String(), recoveryCallbackEnvelope()); !errors.Is(err, provider.ErrCallbackUnavailable) {
			t.Fatalf("post-restart redelivery = %v", err)
		}
		if recoveryCallbackReceipts(t, f, fixture) != 1 {
			t.Fatalf("redelivery changed receipt count to %d", recoveryCallbackReceipts(t, f, fixture))
		}
		assertRecoveryReplayAddsNothing(t, fixture, second, func() { assertRecoveryCommitted(t, f, fixture) })
	})
}

// TestProviderCallbackAndPollConvergeOnFirstTerminalResult proves the same
// provider replay identity delivered once through the durable callback receipt
// and once through status polling converges to a single authoritative result:
// the first terminal content wins, no second poll or observation is recorded,
// and conflicting later content can never replace the committed meaning.
func TestProviderCallbackAndPollConvergeOnFirstTerminalResult(t *testing.T) {
	t.Run("callback_first", func(t *testing.T) {
		runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
			ctx := t.Context()
			fixture := seedProviderRecoveryFixture(t, f)
			first := recoveryCallbackProgress(fixture, "job-1", f.now.Add(time.Minute), providerv1.SignalOutcomeSatisfied)
			service := newProviderRecoveryCallbackService(t, f, callbackIntegrationVerifier{progress: first})
			accepted, err := service.Handle(ctx, fixture.callback.String(), recoveryCallbackEnvelope())
			if err != nil || accepted.Status != provider.CallbackAccepted {
				t.Fatalf("callback receipt = %+v err=%v", accepted, err)
			}
			conflicting := recoveryCallbackProgress(fixture, "job-1", f.now.Add(time.Minute), providerv1.SignalOutcomeNotSatisfied)
			if _, err := newProviderRecoveryCallbackService(t, f, callbackIntegrationVerifier{progress: conflicting}).
				Handle(ctx, fixture.callback.String(), recoveryCallbackEnvelope()); !errors.Is(err, provider.ErrCallbackConflict) {
				t.Fatalf("conflicting callback = %v", err)
			}
			duplicate, err := newProviderRecoveryCallbackService(t, f, callbackIntegrationVerifier{progress: first}).
				Handle(ctx, fixture.callback.String(), recoveryCallbackEnvelope())
			if err != nil || duplicate.Status != provider.CallbackDuplicate || recoveryCallbackReceipts(t, f, fixture) != 1 {
				t.Fatalf("identical callback = %+v receipts=%d err=%v", duplicate, recoveryCallbackReceipts(t, f, fixture), err)
			}

			polling := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
				t.Fatalf("status polling replaced an adopted callback receipt: call=%d", call)
				return providerv1.Progress{}, nil
			}}
			worker := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2 * time.Minute)}, polling, nil)
			work, result := worker.prepare(t, fixture)
			if work == nil || result.Outcome != platformtask.OutcomeComplete {
				t.Fatalf("callback-first prepare = work=%v result=%+v", work != nil, result)
			}
			if result, err = runProviderRecoveryCommit(t, f, work); err != nil || result.Outcome != platformtask.OutcomeComplete {
				t.Fatalf("callback-first commit = %+v err=%v", result, err)
			}
			assertRecoveryDirectResult(t, f, fixture)
			if polling.callCount() != 0 {
				t.Fatalf("callback-first polled the provider %d times", polling.callCount())
			}
			// The losing later content is still unreachable after commit.
			if _, err := newProviderRecoveryCallbackService(t, f, callbackIntegrationVerifier{progress: conflicting}).
				Handle(ctx, fixture.callback.String(), recoveryCallbackEnvelope()); err == nil {
				t.Fatal("conflicting callback after terminal commit was accepted")
			}
			if recoveryCallbackReceipts(t, f, fixture) != 1 {
				t.Fatal("conflicting callback changed the receipt count")
			}
			assertRecoveryDirectResult(t, f, fixture)
		})
	})

	t.Run("poll_first", func(t *testing.T) {
		runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
			ctx := t.Context()
			fixture := seedProviderRecoveryFixture(t, f)
			polled := recoveryProviderResult(fixture, f.now.Add(time.Minute), providerv1.SignalOutcomeSatisfied)
			polling := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
				if !resume {
					return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1", Result: &polled}, nil
				}
				return providerv1.Progress{}, errors.New("unexpected resubmission")
			}}
			worker := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2 * time.Minute)}, polling, nil)
			// The status result is persisted into the dispatch before the local
			// commit, modelling an external success whose worker was lost.
			persisted, err := worker.executor.Execute(ctx, fixture.request)
			if err != nil || persisted.Signals[0].Outcome != providerv1.SignalOutcomeSatisfied {
				t.Fatalf("status poll = %+v err=%v", persisted, err)
			}
			// A conflicting callback with the same replay identity arrives while
			// the check is still running; it is receipted but cannot replace the
			// first terminal result.
			conflicting := recoveryCallbackProgress(fixture, "job-1", f.now.Add(time.Minute), providerv1.SignalOutcomeNotSatisfied)
			accepted, err := newProviderRecoveryCallbackService(t, f, callbackIntegrationVerifier{progress: conflicting}).
				Handle(ctx, fixture.callback.String(), recoveryCallbackEnvelope())
			if err != nil || accepted.Status != provider.CallbackAccepted || recoveryCallbackReceipts(t, f, fixture) != 1 {
				t.Fatalf("later callback = %+v receipts=%d err=%v", accepted, recoveryCallbackReceipts(t, f, fixture), err)
			}
			work, result := worker.prepare(t, fixture)
			if work == nil || result.Outcome != platformtask.OutcomeComplete {
				t.Fatalf("poll-first prepare = work=%v result=%+v", work != nil, result)
			}
			if result, err = runProviderRecoveryCommit(t, f, work); err != nil || result.Outcome != platformtask.OutcomeComplete {
				t.Fatalf("poll-first commit = %+v err=%v", result, err)
			}
			assertRecoveryDirectResult(t, f, fixture)
			if polling.callCount() != 1 {
				t.Fatalf("poll-first race polled %d times, want exactly one persisted status result", polling.callCount())
			}
			// The committed dispatch meaning stays the first terminal result.
			var body []byte
			if err := f.admin.Native().QueryRow(ctx, `SELECT result_body FROM idenqa.provider_dispatches WHERE tenant_id=$1 AND attempt_id=$2`, f.scope.ID().String(), fixture.attemptID.String()).Scan(&body); err != nil {
				t.Fatal(err)
			}
			var stored providerv1.Result
			if json.Unmarshal(body, &stored) != nil || stored.Signals[0].Outcome != providerv1.SignalOutcomeSatisfied {
				t.Fatalf("authoritative dispatch result = %s", body)
			}
			// A full delivery replay adds nothing.
			assertRecoveryReplayAddsNothing(t, fixture, worker, func() {
				assertRecoveryDirectResult(t, f, fixture)
				if recoveryCallbackReceipts(t, f, fixture) != 1 {
					t.Fatal("replay changed the receipt count")
				}
			})
		})
	})
}

// TestExternalProviderResultSurvivesFencedCommitFailure proves an already
// persisted terminal provider result survives a local fenced-commit failure:
// the session stays awaiting_external, the replacement attempt adopts the
// durable dispatch result without another poll, and exactly one observation set
// and one resume transition are recorded. Retry/replay adds nothing.
func TestExternalProviderResultSurvivesFencedCommitFailure(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		fixture := seedProviderRecoveryFixture(t, f)
		pending := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
			return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1"}, nil
		}}
		first := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2 * time.Minute)}, pending, nil)
		work, result := first.prepare(t, fixture)
		if work != nil || result.Outcome != platformtask.OutcomeRetry {
			t.Fatalf("pending prepare = work=%v result=%+v", work != nil, result)
		}
		assertRecoveryPending(t, f, fixture)

		terminal := recoveryProviderResult(fixture, f.now.Add(time.Minute), providerv1.SignalOutcomeSatisfied)
		runner := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
			if !resume {
				t.Fatalf("status continuation submitted instead of resuming: call=%d", call)
			}
			return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1", Result: &terminal}, nil
		}}
		failingStore, err := verificationpostgres.NewGuardedCheckStore(f.runtime, integrationProtector{}, fixedIntegrationClock{now: f.now.Add(2*time.Minute + 10*time.Second)})
		if err != nil {
			t.Fatal(err)
		}
		injected := &failingProviderCommitStore{CheckStore: failingStore}
		injected.fail.Store(true)
		second := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2*time.Minute + 10*time.Second)}, runner, injected)
		work, result = second.prepare(t, fixture)
		if work == nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("terminal prepare = work=%v result=%+v", work != nil, result)
		}
		result, err = runProviderRecoveryCommit(t, f, work)
		if !errors.Is(err, errProviderRecoveryCommit) || result.Outcome != platformtask.OutcomeRetry || result.Class != platformtask.RetryClassUnavailable {
			t.Fatalf("injected commit = %+v err=%v", result, err)
		}
		// The provider result was persisted before the local commit; no local
		// check, inbox or session effect escaped the rollback.
		counts := readProviderRecoveryCounts(t, f, fixture)
		if counts.dispatchResults != 1 || counts.observations != 0 || counts.inbox != 0 ||
			counts.checkState != string(verification.CheckRunning) || counts.sessionState != string(verification.SessionStateAwaitingExternal) {
			t.Fatalf("post-failure state = %+v", counts)
		}

		replacement := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
			t.Fatalf("replacement polled a persisted terminal result: call=%d", call)
			return providerv1.Progress{}, nil
		}}
		third := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2*time.Minute + 20*time.Second)}, replacement, nil)
		work, result = third.prepare(t, fixture)
		if work == nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("replacement prepare = work=%v result=%+v", work != nil, result)
		}
		if result, err = runProviderRecoveryCommit(t, f, work); err != nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("replacement commit = %+v err=%v", result, err)
		}
		assertRecoveryCommitted(t, f, fixture)
		if runner.callCount() != 1 || replacement.callCount() != 0 {
			t.Fatalf("provider poll counts = %d/%d", runner.callCount(), replacement.callCount())
		}

		assertRecoveryReplayAddsNothing(t, fixture, third, func() { assertRecoveryCommitted(t, f, fixture) })
	})
}

// TestProviderRunnerLossRetriesThenRecoversStatusOnly proves the executing task
// classifies an unavailable runner as a bounded operational retry, records no
// identity outcome and leaves the session non-terminal; once the runner answers
// the same attempt completes through a status-only resume.
func TestProviderRunnerLossRetriesThenRecoversStatusOnly(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		fixture := seedProviderRecoveryFixture(t, f)
		unavailable := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
			return providerv1.Progress{}, errors.New("provider runner unavailable")
		}}
		first := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2 * time.Minute)}, unavailable, nil)
		work, result := first.prepare(t, fixture)
		if work != nil || result.Outcome != platformtask.OutcomeRetry || result.Class != platformtask.RetryClassUnavailable {
			t.Fatalf("unavailable runner = work=%v result=%+v", work != nil, result)
		}
		counts := readProviderRecoveryCounts(t, f, fixture)
		if counts.observations != 0 || counts.inbox != 0 || counts.dispatchResults != 0 || counts.awaitingExternal != 1 ||
			counts.checkState != string(verification.CheckRunning) || counts.sessionState != string(verification.SessionStateAwaitingExternal) {
			t.Fatalf("outage state = %+v", counts)
		}
		if unavailable.callCount() != 1 {
			t.Fatalf("outage polling calls = %d", unavailable.callCount())
		}

		terminal := recoveryProviderResult(fixture, f.now.Add(time.Minute), providerv1.SignalOutcomeSatisfied)
		recovered := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
			if !resume {
				t.Fatalf("recovery resubmitted the provider operation: call=%d", call)
			}
			return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1", Result: &terminal}, nil
		}}
		second := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2*time.Minute + 10*time.Second)}, recovered, nil)
		work, result = second.prepare(t, fixture)
		if work == nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("recovery prepare = work=%v result=%+v", work != nil, result)
		}
		if result, err := runProviderRecoveryCommit(t, f, work); err != nil || result.Outcome != platformtask.OutcomeComplete {
			t.Fatalf("recovery commit = %+v err=%v", result, err)
		}
		assertRecoveryCommitted(t, f, fixture)
	})
}

// TestRejectedProviderCallbackFallsBackToStatusPolling proves a typed rejection
// or an unavailable callback verification records no receipt and no identity
// outcome, and that the bounded status-only path still completes the same
// attempt exactly once.
func TestRejectedProviderCallbackFallsBackToStatusPolling(t *testing.T) {
	tests := []struct {
		name      string
		verifier  providerv1.CallbackVerifier
		rejection bool
	}{
		{name: "rejected", verifier: recoveryRejectingVerifier{err: providerv1.Reject(providerv1.CallbackRejectionSignature, "response-signature")}, rejection: true},
		{name: "unavailable", verifier: recoveryRejectingVerifier{err: errors.New("provider callback verification unavailable")}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
				ctx := t.Context()
				fixture := seedProviderRecoveryFixture(t, f)
				service := newProviderRecoveryCallbackService(t, f, test.verifier)
				_, err := service.Handle(ctx, fixture.callback.String(), recoveryCallbackEnvelope())
				if test.rejection {
					if !errors.Is(err, provider.ErrCallbackRejected) {
						t.Fatalf("rejected callback = %v", err)
					}
				} else if err == nil {
					t.Fatal("unavailable callback verification succeeded")
				}
				counts := readProviderRecoveryCounts(t, f, fixture)
				if counts.observations != 0 || counts.inbox != 0 || counts.dispatchResults != 0 ||
					counts.checkState != string(verification.CheckRunning) || recoveryCallbackReceipts(t, f, fixture) != 0 {
					t.Fatalf("callback rejection recorded an outcome: %+v", counts)
				}

				terminal := recoveryProviderResult(fixture, f.now.Add(time.Minute), providerv1.SignalOutcomeSatisfied)
				polling := &recoveryAdvancer{advance: func(call int32, resume bool) (providerv1.Progress, error) {
					return providerv1.Progress{ProviderJobID: "job-1", ReplayID: "job-1", Result: &terminal}, nil
				}}
				worker := newProviderRecoveryWorker(t, f, fixedIntegrationClock{now: f.now.Add(2 * time.Minute)}, polling, nil)
				work, result := worker.prepare(t, fixture)
				if work == nil || result.Outcome != platformtask.OutcomeComplete {
					t.Fatalf("fallback prepare = work=%v result=%+v", work != nil, result)
				}
				if result, err := runProviderRecoveryCommit(t, f, work); err != nil || result.Outcome != platformtask.OutcomeComplete {
					t.Fatalf("fallback commit = %+v err=%v", result, err)
				}
				assertRecoveryDirectResult(t, f, fixture)
			})
		})
	}
}
