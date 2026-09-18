//go:build integration

package integration_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/authority"
	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/Mujhtech/idenqa/internal/verification/syntheticplan"
)

type completionProbe struct{ fail bool }

func (probe completionProbe) EnqueueTx(ctx context.Context, tx pg.Transaction, intents ...task.Intent) error {
	if err := (processingProbe{}).EnqueueTx(ctx, tx, intents...); err != nil {
		return err
	}
	if probe.fail {
		return errPlannedQueueFailure
	}
	return nil
}

func TestVerificationCompletionAtomicReplayAndAuthority(t *testing.T) {
	for _, scenario := range []string{"rollback_replay", "withdrawn", "wrong_assignment"} {
		t.Run(scenario, func(t *testing.T) {
			runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
				decision := prepareCompletion(t, f)
				source := fixedIntegrationClock{now: f.now}
				decisions, err := policypostgres.NewGuarded(f.runtime, source)
				if err != nil {
					t.Fatal(err)
				}
				actor, err := f.ids.NewTask()
				if err != nil {
					t.Fatal(err)
				}
				complete := func(failAfter bool) error {
					store, err := verificationpostgres.NewCompletionStore(f.runtime, f.ids, source)
					if err != nil {
						return err
					}
					return f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
						if err := decisions.AppendWithin(ctx, f.scope, tx, decision); err != nil {
							return err
						}
						if err := store.CompleteWithin(ctx, f.scope, tx, decision, actor); err != nil {
							return err
						}
						if failAfter {
							// Simulate a downstream effect failure after the completion
							// writes to prove the decision, transition, outbox and webhook
							// event roll back together.
							return errPlannedQueueFailure
						}
						return nil
					})
				}
				switch scenario {
				case "withdrawn":
					revokeProcessingAuthority(t, f, "withdrawal")
					if err := complete(false); !errors.Is(err, authority.ErrProcessingNotPermitted) {
						t.Fatalf("withdrawn completion: %v", err)
					}
				case "wrong_assignment":
					other, err := f.ids.NewDecision()
					if err != nil {
						t.Fatal(err)
					}
					decision, err = policy.NewDecision(policy.DecisionInput{ID: other, Snapshot: decision.Snapshot(), Evaluation: decision.Evaluation(), Actor: decision.Actor(), DecidedAt: decision.DecidedAt()})
					if err != nil {
						t.Fatal(err)
					}
					if err := complete(false); !errors.Is(err, policy.ErrInvalid) {
						t.Fatalf("wrong assigned decision: %v", err)
					}
				default:
					if err := complete(true); !errors.Is(err, errPlannedQueueFailure) {
						t.Fatalf("injected enqueue failure: %v", err)
					}
				}
				assertCompletionCounts(t, f, 0, 0, 0, 1, 2)
				if scenario != "rollback_replay" {
					return
				}
				if err := complete(false); err != nil {
					t.Fatal(err)
				}
				assertCompletionCounts(t, f, 1, 1, 0, 2, 2)
				deliveryStore, err := deliverypostgres.New(f.runtime)
				if err != nil {
					t.Fatal(err)
				}
				if err := runWebhookFanout(t, f, deliveryStore, source); err != nil {
					t.Fatal(err)
				}
				assertCompletionCounts(t, f, 1, 1, 1, 2, 3)
				// New endpoints and elapsed authority do not create new subscribers
				// once the committed completion event has fanned out, and exact
				// replay preserves the committed event identity.
				manager, err := delivery.NewManager(deliveryStore, f.ids, integrationProtector{}, source.Now)
				if err != nil {
					t.Fatal(err)
				}
				_, secret, err := manager.CreateEndpoint(t.Context(), f.scope, "https://new.example.com/idenqa")
				if err != nil {
					t.Fatal(err)
				}
				clear(secret)
				revokeProcessingAuthority(t, f, "withdrawal")
				source.now = f.creation.Session.ExpiresAt()
				if err := complete(false); err != nil {
					t.Fatalf("exact replay after withdrawal: %v", err)
				}
				assertCompletionCounts(t, f, 1, 1, 1, 2, 3)
				var eventID, committedID, state string
				if err := f.admin.Native().QueryRow(t.Context(), `SELECT d.event_id,e.id,s.state FROM idenqa.webhook_deliveries d JOIN idenqa.webhook_events e ON e.tenant_id=d.tenant_id AND e.id=d.event_id JOIN idenqa.verification_transitions t ON t.tenant_id=e.tenant_id AND t.to_state='completed' JOIN idenqa.verification_sessions s ON s.tenant_id=t.tenant_id AND s.id=t.verification_id WHERE d.tenant_id=$1`, f.scope.ID().String()).Scan(&eventID, &committedID, &state); err != nil {
					t.Fatal(err)
				}
				if eventID != committedID || state != "completed" {
					t.Fatalf("completion event/record/state=%s/%s/%s", eventID, committedID, state)
				}
			})
		})
	}
}

func assertCompletionCounts(t *testing.T, f captureAcceptanceFixture, decisions, events, deliveries, transitions, tasks int) {
	t.Helper()
	var gotDecisions, gotEvents, gotDeliveries, gotTransitions, gotTasks int
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.verification_decisions),(SELECT count(*) FROM idenqa.webhook_events WHERE event_type='verification.completed'),(SELECT count(*) FROM idenqa.webhook_deliveries),(SELECT count(*) FROM idenqa.verification_transitions),(SELECT count(*) FROM public.processing_task_probe)`).Scan(&gotDecisions, &gotEvents, &gotDeliveries, &gotTransitions, &gotTasks); err != nil {
		t.Fatal(err)
	}
	if gotDecisions != decisions || gotEvents != events || gotDeliveries != deliveries || gotTransitions != transitions || gotTasks != tasks {
		t.Fatalf("partial completion decisions/events/deliveries/transitions/tasks=%d/%d/%d/%d/%d expected=%d/%d/%d/%d/%d", gotDecisions, gotEvents, gotDeliveries, gotTransitions, gotTasks, decisions, events, deliveries, transitions, tasks)
	}
}

// runWebhookFanout executes one bounded fanout batch for the oldest pending
// catalogue event through the real fenced handler.
func runWebhookFanout(t *testing.T, f captureAcceptanceFixture, store *deliverypostgres.Store, source fixedIntegrationClock) error {
	t.Helper()
	var encoded string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT id FROM idenqa.webhook_events WHERE tenant_id=$1 AND state='pending' AND event_type='verification.completed' ORDER BY created_at,id LIMIT 1`, f.scope.ID().String()).Scan(&encoded); err != nil {
		return err
	}
	eventID, err := id.ParseEvent(encoded)
	if err != nil {
		return err
	}
	handler, err := deliverytask.NewFanoutHandler(store, f.ids, processingProbe{}, source.Now)
	if err != nil {
		return err
	}
	intent, err := deliverytask.NewFanoutIntent(f.ids, f.scope, eventID, source.Now().UTC().Truncate(time.Microsecond))
	if err != nil {
		return err
	}
	work, result := handler.Prepare(t.Context(), task.Delivery{Intent: intent, Attempt: 1, Fence: 1})
	if result.Outcome != task.OutcomeComplete || work == nil {
		return errors.New("fanout prepare did not produce work")
	}

	err = f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if returned := work(ctx, tx); returned.Outcome != task.OutcomeComplete {
			return fmt.Errorf("fanout batch did not complete: %v: %w", returned.Outcome, returned.Err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	var endpoints, deliveries int
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT (SELECT count(*) FROM idenqa.webhook_endpoints WHERE tenant_id=$1 AND event_types && ARRAY['verification.completed','*']::text[]),(SELECT count(*) FROM idenqa.webhook_deliveries WHERE tenant_id=$1)`, f.scope.ID().String()).Scan(&endpoints, &deliveries); err != nil {
		return err
	}
	if deliveries == 0 {
		return fmt.Errorf("fanout produced no deliveries: subscribed endpoints=%d", endpoints)
	}

	return nil
}

func prepareCompletion(t *testing.T, f captureAcceptanceFixture) policy.Decision {
	t.Helper()
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
	planner, err := verificationpostgres.NewProcessingStore(f.runtime, syntheticplan.Plan{}, f.ids, processingProbe{}, source)
	if err != nil {
		t.Fatal(err)
	}
	if started, err := planner.StartProcessing(t.Context(), f.scope, f.creation.Session.ID()); err != nil || !started {
		t.Fatalf("start=%v %v", started, err)
	}
	// This completion-boundary test seeds terminal check state; the public-flow
	// test separately executes the complete synthetic plan through Headgate.
	if _, err := f.admin.Native().Exec(t.Context(), `UPDATE idenqa.verification_checks SET state='completed',outcome='passed',version=version+1 WHERE tenant_id=$1`, f.scope.ID().String()); err != nil {
		t.Fatal(err)
	}
	deliveryStore, err := deliverypostgres.New(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := delivery.NewManager(deliveryStore, f.ids, integrationProtector{}, source.Now)
	if err != nil {
		t.Fatal(err)
	}
	_, secret, err := manager.CreateEndpoint(t.Context(), f.scope, "https://hooks.example.com/idenqa")
	if err != nil {
		t.Fatal(err)
	}
	clear(secret)
	var decisionValue, policyValue, region string
	if err := f.admin.Native().QueryRow(t.Context(), `SELECT decision_id,policy_id,region FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, f.scope.ID().String(), f.creation.Session.ID().String()).Scan(&decisionValue, &policyValue, &region); err != nil {
		t.Fatal(err)
	}
	decisionID, err := id.ParseDecision(decisionValue)
	if err != nil {
		t.Fatal(err)
	}
	policyID, err := id.ParsePolicy(policyValue)
	if err != nil {
		t.Fatal(err)
	}
	base := integrationDecision(t, f.ids, f.scope.ID(), f.creation.Session.ID(), decisionID, id.Decision{}, policy.ActorMachine, f.now)
	previous := base.Snapshot()
	reference := previous.Policy()
	reference.ID = policyID
	snapshot, err := policy.NewSnapshot(policy.SnapshotInput{TenantID: f.scope.ID(), VerificationID: f.creation.Session.ID(), AuthorityID: f.declaration.ID(), AcknowledgementID: previous.AcknowledgementID(), Region: region, Policy: reference, Evaluator: previous.Evaluator(), EvaluatedAt: previous.EvaluatedAt(), Facts: previous.Facts()})
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := policy.Resolve(snapshot, base.Evaluation().Results(), base.Evaluation().Assurance())
	if err != nil {
		t.Fatal(err)
	}
	decision, err := policy.NewDecision(policy.DecisionInput{ID: decisionID, Snapshot: snapshot, Evaluation: evaluation, Actor: policy.ActorMachine, DecidedAt: f.now})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}
