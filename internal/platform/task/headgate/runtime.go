package headgate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/jackc/pgx/v5"
	libheadgate "github.com/mujhtech/headgate/go"
)

type carrierPayload struct{ payload json.RawMessage }

func (payload *carrierPayload) UnmarshalJSON(encoded []byte) error {
	var object map[string]json.RawMessage
	if len(encoded) == 0 || json.Unmarshal(encoded, &object) != nil || object == nil {
		return errors.New("carrier payload must be a JSON object")
	}
	payload.payload = append(json.RawMessage(nil), encoded...)
	return nil
}

type verificationCarrier struct{ carrierPayload }
type evidenceCarrier struct{ carrierPayload }
type deliveryCarrier struct{ carrierPayload }
type maintenanceCarrier struct{ carrierPayload }

var queues = []string{QueueVerification, QueueEvidence, QueueDelivery, QueueMaintenance}

func (verificationCarrier) Kind() string { return kindVerification }
func (evidenceCarrier) Kind() string     { return kindEvidence }
func (deliveryCarrier) Kind() string     { return kindDelivery }
func (maintenanceCarrier) Kind() string  { return kindMaintenance }

// WorkerConfig contains bounded process-local execution controls. Fleet-wide
// policies remain durable Headgate admission configuration.
type WorkerConfig struct {
	WorkerID          string
	QueueWorkers      map[string]int
	QueuePolicies     map[string]QueuePolicy
	LeaseDuration     time.Duration
	ShutdownTimeout   time.Duration
	EmptyPollFloor    time.Duration
	EmptyPollCeiling  time.Duration
	MemoryLimitBytes  uint64
	MemoryCheckPeriod time.Duration
}

// QueuePolicy is the fleet-wide admission policy for one tenant-partitioned queue.
type QueuePolicy struct {
	RateLimit         int64
	RatePeriod        time.Duration
	RateBurst         int64
	TenantConcurrency uint64
}

// DefaultWorkerConfig returns conservative defaults for a small installation.
func DefaultWorkerConfig() WorkerConfig {
	return WorkerConfig{
		QueueWorkers: map[string]int{
			QueueVerification: 8,
			QueueEvidence:     4,
			QueueDelivery:     8,
			QueueMaintenance:  2,
		},
		QueuePolicies: map[string]QueuePolicy{
			QueueVerification: {
				RateLimit: 120, RatePeriod: time.Minute, RateBurst: 20, TenantConcurrency: 4,
			},
			QueueEvidence: {
				RateLimit: 60, RatePeriod: time.Minute, RateBurst: 10, TenantConcurrency: 2,
			},
			QueueDelivery: {
				RateLimit: 300, RatePeriod: time.Minute, RateBurst: 50, TenantConcurrency: 8,
			},
			QueueMaintenance: {
				RateLimit: 30, RatePeriod: time.Minute, RateBurst: 5, TenantConcurrency: 1,
			},
		},
		LeaseDuration: 30 * time.Second, ShutdownTimeout: 25 * time.Second,
		EmptyPollFloor: 50 * time.Millisecond, EmptyPollCeiling: 2 * time.Second,
		MemoryCheckPeriod: 30 * time.Second,
	}
}

// Validate rejects unbounded or internally inconsistent worker controls.
func (configuration WorkerConfig) Validate() error {
	if len(configuration.QueueWorkers) != len(queues) || len(configuration.QueuePolicies) != len(queues) ||
		configuration.LeaseDuration < 3*time.Second ||
		configuration.ShutdownTimeout <= 0 || configuration.EmptyPollFloor < time.Millisecond ||
		configuration.EmptyPollCeiling < configuration.EmptyPollFloor || configuration.MemoryCheckPeriod <= 0 {
		return fmt.Errorf("%w: headgate worker configuration", task.ErrInvalid)
	}
	for _, queue := range queues {
		workers, exists := configuration.QueueWorkers[queue]
		if !exists || workers < 1 || workers > 1024 {
			return fmt.Errorf("%w: headgate queue workers", task.ErrInvalid)
		}
		policy, exists := configuration.QueuePolicies[queue]
		if !exists || policy.RateLimit < 1 || policy.RateLimit > 1_000_000_000 ||
			policy.RatePeriod < time.Millisecond || policy.RatePeriod > 24*time.Hour ||
			policy.RateBurst < 1 || policy.RateBurst > 1_000_000_000 ||
			policy.TenantConcurrency < 1 || policy.TenantConcurrency > 100_000 {
			return fmt.Errorf("%w: headgate queue policy", task.ErrInvalid)
		}
	}
	return nil
}

// Worker owns one Headgate runner and translates attempts into owned deliveries.
type Worker struct{ runner *libheadgate.Runner }

// NewWorker constructs a worker without starting background goroutines.
func (adapter *Adapter) NewWorker(
	registry *task.Registry,
	configuration WorkerConfig,
	telemetry libheadgate.Telemetry,
) (*Worker, error) {
	if adapter == nil || adapter.store == nil || registry == nil || configuration.Validate() != nil {
		return nil, fmt.Errorf("%w: headgate worker", task.ErrInvalid)
	}
	headgateRegistry := libheadgate.NewRegistry()
	if err := registerCarrier[verificationCarrier](headgateRegistry, registry); err != nil {
		return nil, fmt.Errorf("register verification carrier: %w", err)
	}
	if err := registerCarrier[evidenceCarrier](headgateRegistry, registry); err != nil {
		return nil, fmt.Errorf("register evidence carrier: %w", err)
	}
	if err := registerCarrier[deliveryCarrier](headgateRegistry, registry); err != nil {
		return nil, fmt.Errorf("register delivery carrier: %w", err)
	}
	if err := registerCarrier[maintenanceCarrier](headgateRegistry, registry); err != nil {
		return nil, fmt.Errorf("register maintenance carrier: %w", err)
	}
	queues := make(map[string]libheadgate.QueueConfig, len(configuration.QueueWorkers))
	for queue, workers := range configuration.QueueWorkers {
		queues[queue] = libheadgate.QueueConfig{MaxWorkers: workers}
	}
	runner := libheadgate.NewRunner(newRuntimeStore(adapter.store), headgateRegistry, libheadgate.Config{
		Queues: queues, LeaseDuration: configuration.LeaseDuration,
		ShutdownTimeout: configuration.ShutdownTimeout,
		WorkerID:        configuration.WorkerID, MemoryLimitBytes: configuration.MemoryLimitBytes,
		MemoryCheckInterval: configuration.MemoryCheckPeriod, Telemetry: telemetry,
		EmptyPollBackoff: libheadgate.BackoffConfig{
			Floor: configuration.EmptyPollFloor, Ceiling: configuration.EmptyPollCeiling,
			Multiplier: 2, Jitter: 0.2,
		},
	})
	return &Worker{runner: runner}, nil
}

// Run blocks until cancellation or shutdown.
func (worker *Worker) Run(ctx context.Context) error {
	if worker == nil || worker.runner == nil {
		return fmt.Errorf("%w: headgate worker", task.ErrInvalid)
	}
	if err := worker.runner.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return classify(err)
	}
	return nil
}

// Shutdown stops admission and begins bounded graceful drain.
func (worker *Worker) Shutdown() {
	if worker != nil && worker.runner != nil {
		worker.runner.Shutdown()
	}
}

// Drain executes at most count due jobs synchronously. It exists for bounded
// operational drains and the shared production-adapter conformance harness.
func (worker *Worker) Drain(ctx context.Context, count int) ([]string, error) {
	if worker == nil || worker.runner == nil || count < 1 {
		return nil, fmt.Errorf("%w: Headgate drain", task.ErrInvalid)
	}
	completed, err := worker.runner.Drain(ctx, count)
	if err != nil {
		return nil, classify(err)
	}
	return completed, nil
}

type retryError struct {
	class task.RetryClass
	cause error
}

func (err *retryError) Error() string { return err.cause.Error() }
func (err *retryError) Unwrap() error { return err.cause }

type carrier interface {
	libheadgate.Args
	encodedPayload() json.RawMessage
}

func (payload verificationCarrier) encodedPayload() json.RawMessage { return payload.payload }
func (payload evidenceCarrier) encodedPayload() json.RawMessage     { return payload.payload }
func (payload deliveryCarrier) encodedPayload() json.RawMessage     { return payload.payload }
func (payload maintenanceCarrier) encodedPayload() json.RawMessage  { return payload.payload }

func registerCarrier[T carrier](headgateRegistry *libheadgate.Registry, registry *task.Registry) error {
	return libheadgate.RegisterExtracted1[T](
		headgateRegistry,
		libheadgate.ExtractMetadata(),
		func(ctx context.Context, job *libheadgate.Job[T], metadata libheadgate.Metadata) error {
			return executeJob(ctx, registry, job, metadata)
		},
	)
}

func execute(ctx context.Context, registry *task.Registry, claim libheadgate.Claim) error {
	intent, err := intentOf(claim.Envelope)
	if err != nil {
		return &libheadgate.UndecodableError{Cause: err}
	}
	handler, err := registry.Resolve(intent.Key())
	if err != nil {
		if !registry.HasName(intent.Key().Name) {
			return libheadgate.Snooze(30 * time.Second)
		}
		return &libheadgate.UndecodableError{Cause: err}
	}
	result := handler.Handle(ctx, task.Delivery{
		Intent: intent, Attempt: claim.Envelope.Attempt + 1,
		CrashAttempt: claim.Envelope.CrashAttempt, Fence: claim.Fence, LeaseUntil: claim.Expires,
	})
	return resultError(result)
}

func executeJob[T carrier](
	ctx context.Context,
	registry *task.Registry,
	job *libheadgate.Job[T],
	metadata libheadgate.Metadata,
) error {
	intent, err := intentOfJob(job, metadata)
	if err != nil {
		return &libheadgate.UndecodableError{Cause: err}
	}
	handler, err := registry.Resolve(intent.Key())
	if err != nil {
		if !registry.HasName(intent.Key().Name) {
			return libheadgate.Snooze(30 * time.Second)
		}
		return &libheadgate.UndecodableError{Cause: err}
	}
	delivery := task.Delivery{
		Intent: intent, Attempt: job.Attempt + 1, CrashAttempt: job.CrashAttempt,
		Fence: job.Fence,
	}
	transactional, ok := handler.(task.TransactionalHandler)
	if !ok {
		return resultError(handler.Handle(ctx, delivery))
	}
	work, result := transactional.Prepare(ctx, delivery)
	if err := result.Validate(); err != nil {
		return &libheadgate.UndecodableError{Cause: err}
	}
	if result.Outcome != task.OutcomeComplete {
		return resultError(result)
	}
	if work == nil {
		return &libheadgate.UndecodableError{Cause: task.ErrInvalid}
	}
	return job.Once(ctx, func(transaction libheadgate.Tx) error {
		pgxTransaction, ok := transaction.Unwrap().(pgx.Tx)
		if !ok {
			return fmt.Errorf("%w: transactional handler requires pgx", task.ErrInvalid)
		}
		return resultError(work(ctx, platformpostgres.Transaction(pgxTransaction)))
	})
}

func resultError(result task.Result) error {
	if err := result.Validate(); err != nil {
		return &libheadgate.UndecodableError{Cause: err}
	}
	switch result.Outcome {
	case task.OutcomeComplete:
		return nil
	case task.OutcomeRetry:
		return &retryError{class: result.Class, cause: result.Err}
	case task.OutcomeCancel:
		return errors.Join(libheadgate.ErrSkipJob, result.Err)
	case task.OutcomeQuarantine:
		return &libheadgate.UndecodableError{Cause: result.Err}
	default:
		return &libheadgate.UndecodableError{Cause: task.ErrInvalid}
	}
}

func intentOfJob[T carrier](job *libheadgate.Job[T], metadata libheadgate.Metadata) (task.Intent, error) {
	if job == nil {
		return task.Intent{}, fmt.Errorf("%w: nil Headgate job", task.ErrInvalid)
	}
	taskID, err := id.ParseTask(job.ID)
	if err != nil {
		return task.Intent{}, fmt.Errorf("parse task ID: %w", err)
	}
	tenantID, err := id.ParseTenant(metadata.Headers["idenqa-tenant-id"])
	if err != nil {
		return task.Intent{}, fmt.Errorf("parse task tenant: %w", err)
	}
	name, err := task.NewName(metadata.Headers[headerTaskName])
	if err != nil {
		return task.Intent{}, err
	}
	initialBackoff, err := durationHeader(metadata.Headers, "idenqa-retry-initial-ms")
	if err != nil {
		return task.Intent{}, err
	}
	maximumBackoff, err := durationHeader(metadata.Headers, "idenqa-retry-maximum-ms")
	if err != nil {
		return task.Intent{}, err
	}
	jitter, err := uint8Header(metadata.Headers, "idenqa-retry-jitter-percent")
	if err != nil {
		return task.Intent{}, err
	}
	scheduledAt, err := millisecondsHeader(metadata.Headers, "idenqa-scheduled-at-ms")
	if err != nil {
		return task.Intent{}, err
	}
	retention, err := durationHeader(metadata.Headers, "idenqa-retention-ms")
	if err != nil {
		return task.Intent{}, err
	}
	return task.NewIntent(task.IntentSpec{
		ID: taskID, TenantID: tenantID, Key: task.Key{Name: name, Version: metadata.SchemaVersion},
		Queue: job.Queue, PartitionKey: job.PartitionKey,
		IdempotencyKey: metadata.Headers[headerIdempotencyKey], Payload: job.Args.encodedPayload(),
		ScheduledAt: scheduledAt, Deadline: job.Deadline,
		Retry: task.RetryPolicy{
			MaxAttempts: job.MaxAttempts, InitialBackoff: initialBackoff,
			MaximumBackoff: maximumBackoff, JitterPercent: jitter,
		},
		Retention: retention, CorrelationID: metadata.Headers["idenqa-correlation-id"],
		CausationID: metadata.Headers["idenqa-causation-id"],
		Traceparent: metadata.Headers[libheadgate.TraceparentHeader],
		Tracestate:  metadata.Headers[libheadgate.TracestateHeader],
	})
}

func intentOf(envelope libheadgate.Envelope) (task.Intent, error) {
	taskID, err := id.ParseTask(envelope.ID)
	if err != nil {
		return task.Intent{}, fmt.Errorf("parse task ID: %w", err)
	}
	tenantID, err := id.ParseTenant(envelope.Headers["idenqa-tenant-id"])
	if err != nil {
		return task.Intent{}, fmt.Errorf("parse task tenant: %w", err)
	}
	name, err := task.NewName(envelope.Headers[headerTaskName])
	if err != nil {
		return task.Intent{}, err
	}
	idempotencyKey := envelope.Headers[headerIdempotencyKey]
	if idempotencyKey == "" {
		uniqueKey := string(envelope.UniqueKey)
		prefix := tenantID.String() + "\x00"
		if !strings.HasPrefix(uniqueKey, prefix) {
			return task.Intent{}, fmt.Errorf("%w: task unique key", task.ErrInvalid)
		}
		idempotencyKey = strings.TrimPrefix(uniqueKey, prefix)
	}
	if idempotencyKey == "" {
		return task.Intent{}, fmt.Errorf("%w: task unique key", task.ErrInvalid)
	}
	initialBackoff, err := durationHeader(envelope.Headers, "idenqa-retry-initial-ms")
	if err != nil {
		return task.Intent{}, err
	}
	maximumBackoff, err := durationHeader(envelope.Headers, "idenqa-retry-maximum-ms")
	if err != nil {
		return task.Intent{}, err
	}
	jitter, err := uint8Header(envelope.Headers, "idenqa-retry-jitter-percent")
	if err != nil {
		return task.Intent{}, err
	}
	return task.NewIntent(task.IntentSpec{
		ID: taskID, TenantID: tenantID, Key: task.Key{Name: name, Version: envelope.SchemaVersion},
		Queue: envelope.Queue, PartitionKey: envelope.PartitionKey,
		IdempotencyKey: idempotencyKey, Payload: json.RawMessage(envelope.Payload),
		ScheduledAt: time.UnixMilli(envelope.ScheduledAtMs), Deadline: optionalTime(envelope.DeadlineMs),
		Retry: task.RetryPolicy{
			MaxAttempts: envelope.MaxAttempts, InitialBackoff: initialBackoff,
			MaximumBackoff: maximumBackoff, JitterPercent: jitter,
		},
		Retention:     time.Duration(envelope.RetentionMs) * time.Millisecond,
		CorrelationID: envelope.Headers["idenqa-correlation-id"],
		CausationID:   envelope.Headers["idenqa-causation-id"],
		Traceparent:   envelope.Headers[libheadgate.TraceparentHeader],
		Tracestate:    envelope.Headers[libheadgate.TracestateHeader],
	})
}

func durationHeader(headers map[string]string, name string) (time.Duration, error) {
	value, err := strconv.ParseInt(headers[name], 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%w: task duration header", task.ErrInvalid)
	}
	return time.Duration(value) * time.Millisecond, nil
}

func uint8Header(headers map[string]string, name string) (uint8, error) {
	value, err := strconv.ParseUint(headers[name], 10, 8)
	if err != nil {
		return 0, fmt.Errorf("%w: task integer header", task.ErrInvalid)
	}
	return uint8(value), nil
}

func optionalTime(milliseconds int64) time.Time {
	if milliseconds == 0 {
		return time.Time{}
	}
	return time.UnixMilli(milliseconds)
}

func millisecondsHeader(headers map[string]string, name string) (time.Time, error) {
	value, err := strconv.ParseInt(headers[name], 10, 64)
	if err != nil || value <= 0 {
		return time.Time{}, fmt.Errorf("%w: task time header", task.ErrInvalid)
	}
	return time.UnixMilli(value), nil
}
