//go:build integration

package headgate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/Mujhtech/idenqa/internal/platform/task/tasktest"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	libheadgate "github.com/mujhtech/headgate/go"
)

func TestPostgresAdapterConformance(t *testing.T) {
	url := os.Getenv("DATABASE_TEST_URL")
	if url == "" {
		t.Skip("DATABASE_TEST_URL is not configured")
	}
	tasktest.Run(t, func(t *testing.T) tasktest.Fixture {
		schema := fmt.Sprintf("headgate_contract_%d", time.Now().UnixNano())
		adapter, store := newIntegrationAdapter(t, url, schema)
		harness := &postgresHarness{
			adapter: adapter, store: store, leases: make(map[string]libheadgate.LeaseRef),
			intents: make(map[string]task.Intent),
		}
		intent := integrationIntent(t)
		return tasktest.Fixture{
			Harness: harness, Intent: intent, Lease: 200 * time.Millisecond,
			Advance: func(duration time.Duration) { time.Sleep(duration + 3*time.Millisecond) },
		}
	})
}

func TestPostgresWorkerRestartReclaimsExpiredLease(t *testing.T) {
	url := os.Getenv("DATABASE_TEST_URL")
	if url == "" {
		t.Skip("DATABASE_TEST_URL is not configured")
	}
	schema := fmt.Sprintf("headgate_test_%d", time.Now().UnixNano())
	migrator, err := OpenMigrator(t.Context(), url, schema, 5*time.Second, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		connection, connectErr := pgx.Connect(context.Background(), url)
		if connectErr == nil {
			_, _ = connection.Exec(
				context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE",
			)
			_ = connection.Close(context.Background())
		}
	})
	if _, err := CheckPoolSchema(t.Context(), pool, schema); err != nil {
		t.Fatalf("CheckPoolSchema() error = %v", err)
	}
	configuration := DefaultConfig("idenqa-integration")
	configuration.Schema = schema
	adapter, err := NewPostgres(pool, configuration)
	if err != nil {
		t.Fatal(err)
	}
	intent := integrationIntent(t)
	if err := adapter.Enqueue(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	claims, err := adapter.store.Admit(t.Context(), libheadgate.AdmitRequest{
		Worker: "crashed-worker", LeaseID: "lease-one", Queues: []string{QueueVerification},
		Capacity: 1, Lease: 10 * time.Millisecond, Quantum: 100,
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("Admit() = %d claims, %v", len(claims), err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := adapter.store.ReclaimExpired(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if _, err := adapter.store.PromoteDue(t.Context(), 10); err != nil {
		t.Fatal(err)
	}
	registry := task.NewRegistry()
	if err := registry.Register(intent.Key(), task.HandlerFunc(
		func(context.Context, task.Delivery) task.Result { return task.Complete() },
	)); err != nil {
		t.Fatal(err)
	}
	worker, err := adapter.NewWorker(registry, DefaultWorkerConfig(), nil)
	if err != nil {
		t.Fatal(err)
	}
	completed, err := worker.Drain(t.Context(), 1)
	if err != nil || len(completed) != 1 || completed[0] != intent.ID().String() {
		t.Fatalf("Drain() = %v, %v", completed, err)
	}
}

func TestTransactionalHandlerCommitsEffectWithHeadgateCompletion(t *testing.T) {
	url := os.Getenv("DATABASE_TEST_URL")
	if url == "" {
		t.Skip("DATABASE_TEST_URL is not configured")
	}
	probePool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(probePool.Close)
	table := pgx.Identifier{fmt.Sprintf("idenqa_task_effect_%d", time.Now().UnixNano())}.Sanitize()
	if _, err := probePool.Exec(t.Context(), "CREATE TABLE "+table+" (task_id text PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = probePool.Exec(context.Background(), "DROP TABLE "+table) })

	tests := []struct {
		name  string
		steal bool
		want  int
	}{
		{name: "live fence commits", want: 1},
		{name: "lost fence rolls back", steal: true, want: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			schema := fmt.Sprintf("headgate_once_%d", time.Now().UnixNano())
			adapter, store := newIntegrationAdapter(t, url, schema)
			if _, err := probePool.Exec(t.Context(), "TRUNCATE "+table); err != nil {
				t.Fatal(err)
			}
			intent := integrationIntent(t)
			registry := task.NewRegistry()
			var isolation string
			handler := transactionalProbe{work: func(ctx context.Context, delivery task.Delivery, transaction platformpostgres.Transaction) task.Result {
				if err := transaction.QueryRow(ctx, "SHOW transaction_isolation").Scan(&isolation); err != nil {
					return task.Retry(task.RetryClassUnavailable, err)
				}
				if _, err := transaction.Exec(ctx, "INSERT INTO "+table+" (task_id) VALUES ($1)", delivery.Intent.ID().String()); err != nil {
					return task.Retry(task.RetryClassUnavailable, err)
				}
				if test.steal {
					if err := store.OperatorCancel(ctx, delivery.Intent.ID().String()); err != nil {
						return task.Retry(task.RetryClassUnavailable, err)
					}
				}
				return task.Complete()
			}}
			if err := registry.Register(intent.Key(), handler); err != nil {
				t.Fatal(err)
			}
			if err := adapter.Enqueue(t.Context(), intent); err != nil {
				t.Fatal(err)
			}
			worker, err := adapter.NewWorker(registry, DefaultWorkerConfig(), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = worker.Drain(t.Context(), 1)
			if isolation != "serializable" {
				t.Fatalf("effect transaction isolation = %q, want serializable", isolation)
			}
			var count int
			if err := probePool.QueryRow(t.Context(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != test.want {
				t.Fatalf("effect rows = %d, want %d", count, test.want)
			}
			job, err := store.GetJob(t.Context(), intent.ID().String(), false)
			if err != nil {
				t.Fatal(err)
			}
			wantState := "completed"
			if test.steal {
				wantState = "cancelled"
			}
			if job.State != wantState {
				t.Fatalf("job state = %q, want %q", job.State, wantState)
			}
		})
	}
}

type transactionalProbe struct {
	work func(context.Context, task.Delivery, platformpostgres.Transaction) task.Result
}

func (probe transactionalProbe) Handle(context.Context, task.Delivery) task.Result {
	return task.Quarantine(errors.New("transaction required"))
}

func (probe transactionalProbe) Prepare(
	_ context.Context,
	delivery task.Delivery,
) (task.TransactionWork, task.Result) {
	return func(ctx context.Context, transaction platformpostgres.Transaction) task.Result {
		return probe.work(ctx, delivery, transaction)
	}, task.Complete()
}

func newIntegrationAdapter(
	t *testing.T,
	url, schema string,
) (*Adapter, libheadgate.InspectStore) {
	t.Helper()
	migrator, err := OpenMigrator(t.Context(), url, schema, 5*time.Second, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	pool, err := pgxpool.New(t.Context(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		connection, connectErr := pgx.Connect(context.Background(), url)
		if connectErr == nil {
			_, _ = connection.Exec(
				context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE",
			)
			_ = connection.Close(context.Background())
		}
	})
	configuration := DefaultConfig("idenqa-conformance")
	configuration.Schema = schema
	configuration.RetryBase = time.Millisecond
	configuration.RetryCap = time.Millisecond
	adapter, err := NewPostgres(pool, configuration)
	if err != nil {
		t.Fatal(err)
	}
	workerConfiguration := DefaultWorkerConfig()
	if err := adapter.ApplyPolicies(t.Context(), workerConfiguration); err != nil {
		t.Fatal(err)
	}
	store, ok := adapter.store.(libheadgate.InspectStore)
	if !ok {
		t.Fatal("PostgreSQL store does not implement Headgate inspection")
	}
	return adapter, store
}

type postgresHarness struct {
	adapter *Adapter
	store   libheadgate.InspectStore
	mutex   sync.Mutex
	serial  uint64
	leases  map[string]libheadgate.LeaseRef
	intents map[string]task.Intent
}

func (harness *postgresHarness) Enqueue(ctx context.Context, intents ...task.Intent) error {
	if err := harness.adapter.Enqueue(ctx, intents...); err != nil {
		return err
	}
	harness.mutex.Lock()
	defer harness.mutex.Unlock()
	for _, intent := range intents {
		harness.intents[intent.ID().String()] = intent
	}
	return nil
}

func (harness *postgresHarness) Claim(
	ctx context.Context,
	lease time.Duration,
) (task.Delivery, error) {
	if _, err := harness.store.ReclaimExpired(ctx, 100); err != nil {
		return task.Delivery{}, classify(err)
	}
	time.Sleep(2 * time.Millisecond)
	if _, err := harness.store.PromoteDue(ctx, 100); err != nil {
		return task.Delivery{}, classify(err)
	}
	harness.mutex.Lock()
	harness.serial++
	serial := harness.serial
	harness.mutex.Unlock()
	units, err := harness.store.Admit(ctx, libheadgate.AdmitRequest{
		Worker: "conformance", LeaseID: fmt.Sprintf("lease-%d", serial), Queues: queues,
		Capacity: 1, Lease: lease, Quantum: 100,
	})
	if err != nil {
		return task.Delivery{}, classify(err)
	}
	if len(units) == 0 || len(units[0].Claims) == 0 {
		return task.Delivery{}, task.ErrNotFound
	}
	claim := units[0].Claims[0]
	intent, err := intentOf(claim.Envelope)
	if err != nil {
		return task.Delivery{}, err
	}
	reference := libheadgate.LeaseRef{
		JobID: intent.ID().String(), LeaseID: claim.LeaseID, Fence: claim.Fence,
	}
	harness.mutex.Lock()
	harness.leases[intent.ID().String()] = reference
	harness.mutex.Unlock()
	return task.Delivery{
		Intent: intent, Attempt: claim.Envelope.Attempt + 1,
		CrashAttempt: claim.Envelope.CrashAttempt, Fence: claim.Fence, LeaseUntil: claim.Expires,
	}, nil
}

func (harness *postgresHarness) Heartbeat(
	ctx context.Context,
	taskID id.Task,
	fence uint64,
	lease time.Duration,
) error {
	reference, exists := harness.reference(taskID.String())
	if !exists || reference.Fence != fence {
		return task.ErrLeaseLost
	}
	lost, err := harness.store.Renew(ctx, []libheadgate.LeaseRef{reference}, lease)
	if err != nil {
		return classify(err)
	}
	if len(lost) != 0 {
		return task.ErrLeaseLost
	}
	return nil
}

func (harness *postgresHarness) Resolve(
	ctx context.Context,
	delivery task.Delivery,
	result task.Result,
) error {
	if err := result.Validate(); err != nil {
		return err
	}
	reference, exists := harness.reference(delivery.Intent.ID().String())
	if !exists || reference.Fence != delivery.Fence {
		return task.ErrLeaseLost
	}
	outcome := libheadgate.OutcomeSuccess
	message := ""
	delay := int64(0)
	switch result.Outcome {
	case task.OutcomeComplete:
	case task.OutcomeRetry:
		outcome = libheadgate.OutcomeRetry
		message = result.Err.Error()
		delay = delivery.Intent.Retry().Backoff(
			delivery.Attempt, delivery.Intent.ID().String(),
		).Milliseconds()
	case task.OutcomeCancel:
		outcome = libheadgate.OutcomeSkip
		message = result.Err.Error()
	case task.OutcomeQuarantine:
		outcome = libheadgate.OutcomeUndecodable
		message = result.Err.Error()
	}
	if err := harness.store.AckAttempt(ctx, reference, outcome, message, delay, nil); err != nil {
		return classify(err)
	}
	return nil
}

func (harness *postgresHarness) Cancel(ctx context.Context, taskID id.Task) error {
	if err := harness.store.OperatorCancel(ctx, taskID.String()); err != nil {
		return classify(err)
	}
	return nil
}

func (harness *postgresHarness) Snapshot(taskID id.Task) (task.Snapshot, error) {
	job, err := harness.store.GetJob(context.Background(), taskID.String(), false)
	if err != nil {
		if errors.Is(err, libheadgate.ErrNotFound) {
			return task.Snapshot{}, task.ErrNotFound
		}
		return task.Snapshot{}, classify(err)
	}
	return harness.snapshotOf(*job), nil
}

func (harness *postgresHarness) Snapshots() []task.Snapshot {
	page, err := harness.store.ListJobs(
		context.Background(), libheadgate.JobFilter{}, "", 100,
	)
	if err != nil {
		return nil
	}
	result := make([]task.Snapshot, 0, len(page.Jobs))
	for _, job := range page.Jobs {
		result = append(result, harness.snapshotOf(job))
	}
	return result
}

func (harness *postgresHarness) reference(taskID string) (libheadgate.LeaseRef, bool) {
	harness.mutex.Lock()
	defer harness.mutex.Unlock()
	reference, exists := harness.leases[taskID]
	return reference, exists
}

func (harness *postgresHarness) snapshotOf(job libheadgate.JobSummary) task.Snapshot {
	harness.mutex.Lock()
	intent := harness.intents[job.ID]
	harness.mutex.Unlock()
	state := task.StateScheduled
	switch job.State {
	case "running":
		state = task.StateRunning
	case "completed":
		state = task.StateCompleted
	case "cancelled":
		state = task.StateCancelled
	case "archived", "undecodable", "quarantined":
		state = task.StateQuarantined
	}
	return task.Snapshot{
		Intent: intent, State: state, Attempt: job.Attempt,
		CrashAttempt: job.CrashAttempt, NextAttemptAt: time.UnixMilli(job.ScheduledAtMs),
	}
}

func integrationIntent(t *testing.T) task.Intent {
	t.Helper()
	now := time.Now().UTC()
	generator, err := id.NewGenerator(integrationClock{now}, bytes.NewReader(bytes.Repeat([]byte{9}, 128)))
	if err != nil {
		t.Fatal(err)
	}
	taskID, _ := generator.NewTask()
	tenantID, _ := generator.NewTenant()
	name, _ := task.NewName("verification.integration")
	intent, err := task.NewIntent(task.IntentSpec{
		ID: taskID, TenantID: tenantID, Key: task.Key{Name: name, Version: 1},
		Queue: QueueVerification, PartitionKey: tenantID.String(), IdempotencyKey: "restart-once",
		// Keep the fixture immediately due even when the process and database clocks
		// straddle a millisecond boundary during enqueue.
		Payload: map[string]string{"verification_id": "ver_integration"}, ScheduledAt: now.Add(-time.Second),
		Retry: task.RetryPolicy{
			MaxAttempts: 3, InitialBackoff: time.Millisecond,
			MaximumBackoff: time.Millisecond, JitterPercent: 0,
		},
		Retention: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

type integrationClock struct{ now time.Time }

func (clock integrationClock) Now() time.Time { return clock.now }
