//go:build integration

package integration_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	bootstrapworker "github.com/Mujhtech/idenqa/internal/bootstrap/worker"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/Mujhtech/idenqa/internal/verification/synthetic"
	"github.com/Mujhtech/idenqa/internal/verification/syntheticplan"
)

var errPlannedQueueFailure = errors.New("injected queue failure after insertion")

type failingProcessingEnqueuer struct{ adapter *taskheadgate.Adapter }

func (queue failingProcessingEnqueuer) EnqueueTx(ctx context.Context, tx pg.Transaction, intents ...task.Intent) error {
	if err := queue.adapter.EnqueueTx(ctx, tx, intents...); err != nil {
		return err
	}
	return errPlannedQueueFailure
}

func assertCapturedProcessing(t *testing.T, admin, runtime *pg.Pool, scope tenant.Scope, verificationID id.Verification, client *http.Client, baseURL, credential string) id.Decision {
	t.Helper()
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("IDENQA_HEADGATE_INSTALLATION_ID", "idenqa-test")
	configuration, err := config.LoadWorker("")
	if err != nil {
		t.Fatal(err)
	}
	adapter, err := taskheadgate.NewPostgres(runtime.Native(), taskheadgate.DefaultConfig(configuration.HeadgateInstallationID))
	if err != nil {
		t.Fatal(err)
	}
	failing, err := verificationpostgres.NewProcessingStore(runtime, integrationProtector{}, syntheticplan.Plan{}, ids, failingProcessingEnqueuer{adapter}, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	var auditBefore int
	if err := admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.audit_records WHERE tenant_id=$1`, scope.ID().String()).Scan(&auditBefore); err != nil {
		t.Fatal(err)
	}
	if started, err := failing.StartProcessing(t.Context(), scope, verificationID); started || !errors.Is(err, errPlannedQueueFailure) {
		t.Fatalf("failed queue start: %v, %v", started, err)
	}
	var checks, transitions, jobs, auditAfter int
	var state string
	if err := admin.Native().QueryRow(t.Context(), `SELECT state,
 (SELECT count(*) FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2),
 (SELECT count(*) FROM idenqa.verification_transitions WHERE tenant_id=$1 AND verification_id=$2),
 (SELECT count(*) FROM headgate.headgate_job),
 (SELECT count(*) FROM idenqa.audit_records WHERE tenant_id=$1)
 FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), verificationID.String()).Scan(&state, &checks, &transitions, &jobs, &auditAfter); err != nil {
		t.Fatal(err)
	}
	if state != "collecting" || checks != 0 || transitions != 0 || jobs != 0 || auditBefore != auditAfter {
		t.Fatalf("partial start escaped rollback: state=%s checks=%d transitions=%d jobs=%d audit=%d/%d", state, checks, transitions, jobs, auditBefore, auditAfter)
	}

	// The capture is already durable before this process starts. No test creates
	// checks or tasks: the runnable worker must discover, plan and execute it.
	receiver := newJourneyReceiver(t, client, baseURL, credential)
	configuration.SyntheticProcessing = true
	configuration.ProgressPollInterval = 20 * time.Millisecond
	startWorker := func() func() {
		t.Helper()
		infrastructure := receiver.infrastructure(t, configuration.EvidenceLocalKeyringFile)
		process, err := bootstrapworker.NewProcessWithInfrastructure(t.Context(), configuration, slog.New(slog.NewJSONHandler(io.Discard, nil)), buildinfo.Info{Version: "integration"}, task.NewRegistry(), bootstrapworker.EvidenceInfrastructure{}, infrastructure)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- process.Run(ctx) }()
		var once sync.Once
		stop := func() {
			once.Do(func() {
				cancel()
				if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
					t.Error(err)
				}
				if err := process.Close(context.WithoutCancel(t.Context())); err != nil {
					t.Error(err)
				}
			})
		}
		t.Cleanup(stop)
		return stop
	}
	stop := startWorker()
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var completed int
		if err := admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2 AND state='completed'`, scope.ID().String(), verificationID.String()).Scan(&completed); err != nil {
			t.Fatal(err)
		}
		var attempted int
		if err := admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.webhook_deliveries WHERE tenant_id=$1 AND attempt_count=1 AND state='pending'`, scope.ID().String()).Scan(&attempted); err != nil {
			t.Fatal(err)
		}
		if completed == 2 && attempted == 1 {
			break
		}
		select {
		case <-deadline.C:
			var diagnostics string
			if err := admin.Native().QueryRow(t.Context(), `SELECT json_build_object('jobs',(SELECT json_agg(json_build_object('kind',kind,'state',state,'errors',errors)) FROM headgate.headgate_job),'state',(SELECT state FROM idenqa.verification_sessions WHERE id=$1),'decisions',(SELECT count(*) FROM idenqa.verification_decisions),'deliveries',(SELECT count(*) FROM idenqa.webhook_deliveries))::text`, verificationID.String()).Scan(&diagnostics); err != nil {
				t.Fatal(err)
			}
			t.Log(diagnostics)
			t.Fatalf("worker failed to execute automatically planned checks; completed=%d", completed)
		case <-tick.C:
		}
	}
	stop()
	receiver.enableSuccess()
	stopRestart := startWorker()
	planner, err := verificationpostgres.NewProcessingStore(runtime, integrationProtector{}, syntheticplan.Plan{}, ids, adapter, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	if started, err := planner.StartProcessing(t.Context(), scope, verificationID); started || err != nil {
		t.Fatalf("restart duplicated plan: %v %v", started, err)
	}
	decisionID := receiver.waitForDelivery(t, admin, scope, verificationID)
	stopRestart()
	if err := admin.Native().QueryRow(t.Context(), `SELECT
 (SELECT count(*) FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2),
 (SELECT count(*) FROM idenqa.verification_transitions WHERE tenant_id=$1 AND verification_id=$2),
 (SELECT count(*) FROM idenqa.verification_observations WHERE tenant_id=$1 AND verification_id=$2)`, scope.ID().String(), verificationID.String()).Scan(&checks, &transitions, &jobs); err != nil {
		t.Fatal(err)
	}
	if checks != 2 || transitions != 2 || jobs != 2 {
		t.Fatalf("processing replay changed plan: checks=%d transitions=%d observations=%d", checks, transitions, jobs)
	}
	return decisionID
}

// Transactional probe is deliberately database-backed so rollback and races
// can be asserted without a second queue migration in every denial fixture.
type processingProbe struct{}

func (processingProbe) EnqueueTx(ctx context.Context, tx pg.Transaction, intents ...task.Intent) error {
	for _, intent := range intents {
		if _, err := tx.Exec(ctx, `INSERT INTO processing_task_probe (id) VALUES ($1)`, intent.ID().String()); err != nil {
			return err
		}
	}
	return nil
}

func TestCaptureProcessingConcurrentStartAndDenials(t *testing.T) {
	for _, scenario := range []string{"concurrent", "withdrawn", "refusal", "new_consent", "expired", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
				evidenceStore, mutations := f.prepare(t)
				for _, mutation := range mutations {
					if _, err := evidenceStore.AcceptUpload(t.Context(), f.scope, mutation); err != nil {
						t.Fatal(err)
					}
				}
				// Administrative probe grants emulate an owned transactional queue boundary.
				if _, err := f.admin.Native().Exec(t.Context(), `CREATE TABLE public.processing_task_probe (id text PRIMARY KEY); GRANT INSERT,SELECT ON public.processing_task_probe TO PUBLIC`); err != nil {
					t.Fatal(err)
				}
				source := fixedIntegrationClock{now: f.now}
				switch scenario {
				case "withdrawn":
					declaration := f.declaration
					if err := declaration.Withdraw(f.now); err != nil {
						t.Fatal(err)
					}
					request := integrationIdempotencyRequest(t, f.scope.ID(), declaration.Record().CreatedBy, "authorities.withdraw", "processing-withdraw", []byte(`{}`), f.now)
					if _, err := f.authorities.Transition(t.Context(), f.scope, authority.TransitionMutation{Authority: declaration, ExpectedVersion: f.declaration.Record().Version, Action: authority.StateWithdrawn, Actor: declaration.Record().CreatedBy, EventID: mustCaptureEvent(t, f.ids), Idempotency: request}); err != nil {
						t.Fatal(err)
					}
				case "refusal":
					revokeProcessingAuthority(t, f, "refusal")
				case "new_consent":
					appendNewProcessingConsent(t, f)
				case "expired":
					source.now = f.creation.Session.ExpiresAt()
				case "cancelled":
					lifecycle, err := verificationpostgres.NewLifecycleStore(f.runtime, integrationProtector{}, source)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := lifecycle.Apply(t.Context(), f.scope, verification.LifecycleCommand{EventID: mustCaptureEvent(t, f.ids), VerificationID: f.creation.Session.ID(), ExpectedVersion: 1, Target: verification.SessionStateCancelled, ActorID: f.declaration.Record().CreatedBy.String(), OccurredAt: f.now}); err != nil {
						t.Fatal(err)
					}
				}
				planner, err := verificationpostgres.NewProcessingStore(f.runtime, integrationProtector{}, syntheticplan.Plan{}, f.ids, processingProbe{}, source)
				if err != nil {
					t.Fatal(err)
				}
				if scenario != "concurrent" {
					targets, err := planner.ListReadyCaptures(t.Context(), source.now, 1)
					if err != nil || len(targets) != 0 {
						t.Fatalf("ineligible capture filled discovery batch: %d, %v", len(targets), err)
					}
					started, err := planner.StartProcessing(t.Context(), f.scope, f.creation.Session.ID())
					if started || (err != nil && !errors.Is(err, authority.ErrProcessingNotPermitted)) {
						t.Fatalf("invalidated processing start: %v %v", started, err)
					}
				} else {
					var wait sync.WaitGroup
					results := make(chan bool, 2)
					failures := make(chan error, 2)
					for range 2 {
						wait.Go(func() {
							started, err := planner.StartProcessing(t.Context(), f.scope, f.creation.Session.ID())
							results <- started
							failures <- err
						})
					}
					wait.Wait()
					close(results)
					close(failures)
					starts := 0
					for started := range results {
						if started {
							starts++
						}
					}
					for err := range failures {
						if err != nil {
							t.Fatal(err)
						}
					}
					if starts != 1 {
						t.Fatalf("concurrent starts=%d", starts)
					}
					// A collecting expectation cannot authorize a later processing mutation.
					err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
						return authoritypostgres.ValidateProcessingWithin(ctx, tx, f.scope, f.creation.Session.ID(), f.now, source, verification.SessionStateCollecting)
					})
					if !errors.Is(err, authority.ErrProcessingNotPermitted) {
						t.Fatalf("wrong lifecycle authorization: %v", err)
					}
				}
				var checks, tasks int
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.verification_checks),(SELECT count(*) FROM public.processing_task_probe)`).Scan(&checks, &tasks); err != nil {
					t.Fatal(err)
				}
				want := 0
				if scenario == "concurrent" {
					want = 2
				}
				if checks != want || tasks != want {
					t.Fatalf("atomic checks/tasks=%d/%d want %d", checks, tasks, want)
				}
			})
		})
	}
}

func TestCaptureProcessingRejectsPreparedResultsAfterInvalidation(t *testing.T) {
	for _, scenario := range []string{"withdrawal", "refusal", "expired", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
				evidenceStore, mutations := f.prepare(t)
				for _, mutation := range mutations {
					if _, err := evidenceStore.AcceptUpload(t.Context(), f.scope, mutation); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := f.admin.Native().Exec(t.Context(), `CREATE TABLE public.processing_task_probe (id text PRIMARY KEY); GRANT INSERT,SELECT ON public.processing_task_probe TO PUBLIC`); err != nil {
					t.Fatal(err)
				}
				source := fixedIntegrationClock{now: f.now}
				planner, err := verificationpostgres.NewProcessingStore(f.runtime, integrationProtector{}, syntheticplan.Plan{}, f.ids, processingProbe{}, source)
				if err != nil {
					t.Fatal(err)
				}
				if started, err := planner.StartProcessing(t.Context(), f.scope, f.creation.Session.ID()); !started || err != nil {
					t.Fatalf("start: %v %v", started, err)
				}
				var checkValue string
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT id FROM idenqa.verification_checks WHERE tenant_id=$1 AND verification_id=$2 AND name='synthetic.document'`, f.scope.ID().String(), f.creation.Session.ID().String()).Scan(&checkValue); err != nil {
					t.Fatal(err)
				}
				checkID, err := id.ParseCheck(checkValue)
				if err != nil {
					t.Fatal(err)
				}
				primitive, err := verificationpostgres.NewCheckStore(f.runtime, integrationProtector{})
				if err != nil {
					t.Fatal(err)
				}
				check, err := primitive.FindCheck(t.Context(), f.scope, checkID)
				if err != nil {
					t.Fatal(err)
				}
				attempt := check.Attempts()[0]
				result, err := (synthetic.Provider{Scenario: synthetic.Success, Now: source.Now}).Execute(t.Context(), providerv1.Request{Contract: providerv1.CurrentVersion, AttemptID: attempt.ID.String()})
				if err != nil {
					t.Fatal(err)
				}
				fingerprint, err := verification.ProviderResultFingerprint(result)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := verification.ApplyProviderResult(&check, result, f.ids, attempt.Fence); err != nil {
					t.Fatal(err)
				}
				receipt, err := verification.NewResultReceipt(attempt.ID, fingerprint, f.now)
				if err != nil {
					t.Fatal(err)
				}
				switch scenario {
				case "withdrawal", "refusal":
					revokeProcessingAuthority(t, f, scenario)
				case "expired":
					source.now = f.creation.Session.ExpiresAt()
				case "cancelled":
					lifecycle, err := verificationpostgres.NewLifecycleStore(f.runtime, integrationProtector{}, source)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := lifecycle.Apply(t.Context(), f.scope, verification.LifecycleCommand{EventID: mustCaptureEvent(t, f.ids), VerificationID: f.creation.Session.ID(), ExpectedVersion: 2, Target: verification.SessionStateCancelled, ActorID: f.declaration.Record().CreatedBy.String(), OccurredAt: f.now}); err != nil {
						t.Fatal(err)
					}
				}
				guarded, err := verificationpostgres.NewGuardedCheckStore(f.runtime, integrationProtector{}, source)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := guarded.FindCheck(t.Context(), f.scope, checkID); !errors.Is(err, authority.ErrProcessingNotPermitted) {
					t.Fatalf("dispatch after invalidation: %v", err)
				}
				err = f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
					_, err := guarded.SaveCheckWithin(ctx, f.scope, tx, verification.CheckCommit{Check: check, ExpectedVersion: 2, EventID: mustCaptureEvent(t, f.ids), Receipt: &receipt})
					return err
				})
				if !errors.Is(err, authority.ErrProcessingNotPermitted) {
					t.Fatalf("commit prepared result after invalidation: %v", err)
				}
				stored, err := primitive.FindCheck(t.Context(), f.scope, checkID)
				if err != nil {
					t.Fatal(err)
				}
				if stored.State != verification.CheckRunning || stored.Version != 2 || len(stored.Attempts()[0].Observations) != 0 {
					t.Fatal("invalidated prepared result escaped rollback")
				}
				var inbox int
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT count(*) FROM idenqa.verification_result_inbox WHERE tenant_id=$1 AND verification_id=$2`, f.scope.ID().String(), f.creation.Session.ID().String()).Scan(&inbox); err != nil {
					t.Fatal(err)
				}
				if inbox != 0 {
					t.Fatal("invalidated result claimed inbox")
				}
			})
		})
	}
}
