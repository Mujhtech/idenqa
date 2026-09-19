// Package headgate adapts Headgate without exposing its types to domain or
// public contracts.
package headgate

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	libheadgate "github.com/mujhtech/headgate/go"
	"github.com/mujhtech/headgate/go/driver/headgatepgx"
)

const (
	// QueueVerification isolates provider and model evaluation work.
	QueueVerification = "verification"
	// QueueEvidence isolates evidence processing from decision work.
	QueueEvidence = "evidence"
	// QueueDelivery isolates tenant-facing webhook delivery.
	QueueDelivery = "delivery"
	// QueueMaintenance contains bounded reconciliation and cleanup work.
	QueueMaintenance = "maintenance"
	// SystemPartition identifies installation-wide maintenance work.
	SystemPartition = "system"
	// DutyVerificationReconciliation elects one installation-wide scheduler.
	DutyVerificationReconciliation = "idenqa-verification-reconciliation"
	// DutyPolicyAuthorship elects one installation-wide decision scheduler.
	DutyPolicyAuthorship = "idenqa-policy-authorship"
	// DutyPrivacyDeletion elects one installation-wide lifecycle scheduler.
	DutyPrivacyDeletion = "idenqa-privacy-deletion"
	// DutyWebhookRetention elects one installation-wide payload expiry worker.
	DutyWebhookRetention = "idenqa-webhook-retention"

	headerTaskName       = "idenqa-task-name"
	headerIdempotencyKey = "idenqa-idempotency-key"

	kindVerification = "idenqa:verification"
	kindEvidence     = "idenqa:evidence"
	kindDelivery     = "idenqa:delivery"
	kindMaintenance  = "idenqa:maintenance"
)

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,62}[a-z0-9]$|^[a-z]$`)

// Config contains the production adapter choices that must remain stable for
// one installation.
type Config struct {
	InstallationID string
	Schema         string
	Queues         []string
	CrashLimit     int32
	RetryBase      time.Duration
	RetryCap       time.Duration
}

// RunDuty executes one bounded singleton operation while this holder owns the
// Headgate duty lease. false means another worker owns this tick.
func (adapter *Adapter) RunDuty(
	ctx context.Context,
	duty string,
	holder string,
	lease time.Duration,
	work func(context.Context) error,
) (claimed bool, result error) {
	if adapter == nil || adapter.store == nil || !identifierPattern.MatchString(duty) ||
		!identifierPattern.MatchString(holder) || lease <= 0 || lease > 2*time.Hour || work == nil {
		return false, fmt.Errorf("%w: headgate duty", task.ErrInvalid)
	}
	claimed, err := adapter.store.ClaimDuty(ctx, duty, holder, lease)
	if err != nil {
		return false, classify(err)
	}
	if !claimed {
		return false, nil
	}
	defer func() {
		releaseContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := adapter.store.ReleaseDuty(releaseContext, duty, holder); err != nil {
			result = errors.Join(result, classify(err))
		}
	}()
	return true, work(ctx)
}

// DefaultConfig returns the selected open-source deployment layout.
func DefaultConfig(installationID string) Config {
	return Config{
		InstallationID: installationID,
		Schema:         "headgate",
		Queues: []string{
			QueueVerification, QueueEvidence, QueueDelivery, QueueMaintenance,
		},
		CrashLimit: 3,
		RetryBase:  time.Second,
		RetryCap:   time.Hour,
	}
}

// Validate rejects configuration that could silently route to another
// installation or PostgreSQL namespace.
func (configuration Config) Validate() error {
	if !identifierPattern.MatchString(configuration.InstallationID) ||
		!identifierPattern.MatchString(configuration.Schema) || len(configuration.Queues) == 0 ||
		configuration.CrashLimit == 0 || configuration.CrashLimit > 100 ||
		configuration.RetryBase < time.Millisecond || configuration.RetryCap < configuration.RetryBase {
		return fmt.Errorf("%w: headgate configuration", task.ErrInvalid)
	}
	seen := make(map[string]struct{}, len(configuration.Queues))
	for _, queue := range configuration.Queues {
		if !identifierPattern.MatchString(queue) {
			return fmt.Errorf("%w: headgate queue", task.ErrInvalid)
		}
		if _, exists := seen[queue]; exists {
			return fmt.Errorf("%w: duplicate headgate queue", task.ErrInvalid)
		}
		seen[queue] = struct{}{}
	}
	return nil
}

// Adapter translates owned Idenqa intents to Headgate envelopes.
type Adapter struct {
	client         *libheadgate.Client
	store          libheadgate.Store
	installationID string
	queues         map[string]struct{}
}

// JobSummary is a payload-free failed-work inspection record.
type JobSummary struct {
	ID, Kind, Queue, State, FailureClass string
	Attempt, CrashAttempt, MaxAttempts   uint32
	EnqueuedAt, ScheduledAt              time.Time
}

// ListJobs returns a bounded payload-free page for operational inspection.
func (adapter *Adapter) ListJobs(ctx context.Context, state string, limit uint32) ([]JobSummary, string, error) {
	if adapter == nil || state == "" || limit == 0 || limit > 1000 {
		return nil, "", fmt.Errorf("%w: headgate inspection", task.ErrInvalid)
	}
	inspector, ok := adapter.store.(libheadgate.InspectStore)
	if !ok {
		return nil, "", fmt.Errorf("%w: headgate inspection", task.ErrInvalid)
	}
	page, err := inspector.ListJobs(ctx, libheadgate.JobFilter{State: libheadgate.Ptr(state)}, "", limit)
	if err != nil {
		return nil, "", classify(err)
	}
	result := make([]JobSummary, 0, len(page.Jobs))
	for _, job := range page.Jobs {
		result = append(result, JobSummary{ID: job.ID, Kind: job.Kind, Queue: job.Queue, State: job.State,
			FailureClass: failureClass(job.ErrorsJSON), Attempt: job.Attempt, CrashAttempt: job.CrashAttempt,
			MaxAttempts: job.MaxAttempts, EnqueuedAt: time.UnixMilli(job.EnqueuedAtMs).UTC(), ScheduledAt: time.UnixMilli(job.ScheduledAtMs).UTC()})
	}
	return result, page.NextCursor, nil
}

// RetryJob explicitly retries one terminal job without exposing its payload.
func (adapter *Adapter) RetryJob(ctx context.Context, identifier string) error {
	if adapter == nil || identifier == "" {
		return fmt.Errorf("%w: headgate retry", task.ErrInvalid)
	}
	inspector, ok := adapter.store.(libheadgate.InspectStore)
	if !ok {
		return fmt.Errorf("%w: headgate retry", task.ErrInvalid)
	}
	return classify(inspector.OperatorRetry(ctx, identifier))
}

func failureClass(encoded string) string {
	if encoded == "" {
		return "none"
	}
	return "recorded"
}

// New constructs an adapter around a caller-owned Headgate store.
func New(store libheadgate.Store, configuration Config) (*Adapter, error) {
	if store == nil || configuration.Validate() != nil {
		return nil, fmt.Errorf("%w: headgate adapter", task.ErrInvalid)
	}
	queues := make(map[string]struct{}, len(configuration.Queues))
	for _, queue := range configuration.Queues {
		queues[queue] = struct{}{}
	}
	return &Adapter{
		client: libheadgate.NewClient(store), store: store,
		installationID: configuration.InstallationID, queues: queues,
	}, nil
}

// NewPostgres constructs the selected PostgreSQL-backed adapter in an explicit
// schema. It does not create, validate, or migrate that schema.
func NewPostgres(pool *pgxpool.Pool, configuration Config) (*Adapter, error) {
	if pool == nil || configuration.Validate() != nil {
		return nil, fmt.Errorf("%w: headgate PostgreSQL adapter", task.ErrInvalid)
	}
	options := postgresOptions(configuration)
	store, err := headgatepgx.WithOptionsInSchema(pool, options, configuration.Schema)
	if err != nil {
		return nil, fmt.Errorf("construct headgate PostgreSQL store: %w", err)
	}
	return New(store, configuration)
}

func postgresOptions(configuration Config) headgatepgx.Options {
	options := headgatepgx.DefaultOptions()
	options.CrashLimit = configuration.CrashLimit
	options.RetryBaseMs = configuration.RetryBase.Milliseconds()
	options.RetryCapMs = configuration.RetryCap.Milliseconds()
	return options
}

// Enqueue inserts owned intents through Headgate's producer boundary.
func (adapter *Adapter) Enqueue(ctx context.Context, intents ...task.Intent) error {
	if adapter == nil || adapter.client == nil {
		return fmt.Errorf("%w: headgate adapter", task.ErrInvalid)
	}
	envelopes, err := adapter.envelopes(intents)
	if err != nil {
		return err
	}
	if err := adapter.client.Enqueue(ctx, envelopes); err != nil {
		if errors.Is(err, libheadgate.ErrDuplicate) {
			return nil
		}
		return classify(err)
	}
	return nil
}

// EnqueueTx commits application state and task insertion through the caller's
// existing pgx transaction. It never writes Headgate tables directly.
func (adapter *Adapter) EnqueueTx(
	ctx context.Context,
	transaction postgres.Transaction,
	intents ...task.Intent,
) error {
	if adapter == nil || adapter.client == nil || transaction == nil {
		return fmt.Errorf("%w: transactional headgate enqueue", task.ErrInvalid)
	}
	pgxTransaction, ok := transaction.(pgx.Tx)
	if !ok {
		return fmt.Errorf("%w: transaction is not pgx", task.ErrInvalid)
	}
	envelopes, err := adapter.envelopes(intents)
	if err != nil {
		return err
	}
	if err := adapter.client.EnqueueTx(ctx, headgatepgx.WrapTx(pgxTransaction), envelopes); err != nil {
		if errors.Is(err, libheadgate.ErrDuplicate) {
			return nil
		}
		return classify(err)
	}
	return nil
}

func (adapter *Adapter) envelopes(intents []task.Intent) ([]libheadgate.Envelope, error) {
	if len(intents) == 0 {
		return nil, fmt.Errorf("%w: empty headgate enqueue batch", task.ErrInvalid)
	}
	envelopes := make([]libheadgate.Envelope, 0, len(intents))
	for _, intent := range intents {
		if err := intent.Validate(); err != nil {
			return nil, err
		}
		if _, exists := adapter.queues[intent.Queue()]; !exists {
			return nil, fmt.Errorf("%w: unconfigured task queue %s", task.ErrInvalid, intent.Queue())
		}
		if intent.Queue() == QueueMaintenance {
			if intent.PartitionKey() != intent.TenantID().String() && intent.PartitionKey() != SystemPartition {
				return nil, fmt.Errorf("%w: maintenance partition", task.ErrInvalid)
			}
		} else if intent.PartitionKey() != intent.TenantID().String() {
			return nil, fmt.Errorf("%w: tenant task partition", task.ErrInvalid)
		}
		envelope, err := envelopeOf(intent, adapter.installationID)
		if err != nil {
			return nil, err
		}
		envelopes = append(envelopes, envelope)
	}
	return envelopes, nil
}

func envelopeOf(intent task.Intent, installationID string) (libheadgate.Envelope, error) {
	if !identifierPattern.MatchString(installationID) {
		return libheadgate.Envelope{}, fmt.Errorf("%w: installation identity", task.ErrInvalid)
	}
	payload := intent.Payload()
	retentionMilliseconds := intent.Retention().Milliseconds()
	if retentionMilliseconds <= 0 {
		return libheadgate.Envelope{}, fmt.Errorf("%w: retention precision", task.ErrInvalid)
	}
	headers := map[string]string{
		headerTaskName:                intent.Key().Name.String(),
		headerIdempotencyKey:          intent.IdempotencyKey(),
		"idenqa-installation-id":      installationID,
		"idenqa-tenant-id":            intent.TenantID().String(),
		"idenqa-correlation-id":       intent.CorrelationID(),
		"idenqa-causation-id":         intent.CausationID(),
		"idenqa-retry-initial-ms":     strconv.FormatInt(intent.Retry().InitialBackoff.Milliseconds(), 10),
		"idenqa-retry-maximum-ms":     strconv.FormatInt(intent.Retry().MaximumBackoff.Milliseconds(), 10),
		"idenqa-retry-jitter-percent": strconv.FormatUint(uint64(intent.Retry().JitterPercent), 10),
		"idenqa-scheduled-at-ms":      strconv.FormatInt(intent.ScheduledAt().UnixMilli(), 10),
		"idenqa-retention-ms":         strconv.FormatInt(retentionMilliseconds, 10),
		libheadgate.TraceparentHeader: intent.Traceparent(),
		libheadgate.TracestateHeader:  intent.Tracestate(),
	}
	for key, value := range headers {
		if value == "" {
			delete(headers, key)
		}
	}
	envelope := libheadgate.Envelope{
		ID: intent.ID().String(), Kind: carrierKind(intent.Queue()), Queue: intent.Queue(),
		SchemaVersion: intent.Key().Version, Payload: payload, PartitionKey: intent.PartitionKey(),
		RateClass: intent.Queue(),
		Weight:    1, Fingerprint: libheadgate.Fingerprint(intent.Key().Name.String(), payload),
		MaxAttempts: intent.Retry().MaxAttempts, ScheduledAtMs: intent.ScheduledAt().UnixMilli(),
		UniqueKey:   append([]byte(intent.TenantID().String()), []byte("\x00"+intent.IdempotencyKey())...),
		RetentionMs: retentionMilliseconds, Headers: headers,
	}
	if !intent.Deadline().IsZero() {
		envelope.DeadlineMs = intent.Deadline().UnixMilli()
		timeout := intent.Deadline().Sub(intent.ScheduledAt())
		if timeout > 0 {
			envelope.TimeoutMs = timeout.Milliseconds()
		}
	}
	if err := libheadgate.ValidateEnqueue([]libheadgate.Envelope{envelope}); err != nil {
		return libheadgate.Envelope{}, classify(err)
	}
	return envelope, nil
}

func carrierKind(queue string) string {
	switch queue {
	case QueueVerification:
		return kindVerification
	case QueueEvidence:
		return kindEvidence
	case QueueDelivery:
		return kindDelivery
	case QueueMaintenance:
		return kindMaintenance
	default:
		return ""
	}
}

func classify(err error) error {
	switch {
	case errors.Is(err, libheadgate.ErrDuplicate):
		return errors.Join(task.ErrDuplicate, err)
	case errors.Is(err, libheadgate.ErrIDConflict):
		return errors.Join(task.ErrConflict, err)
	case errors.Is(err, libheadgate.ErrInvalid):
		return errors.Join(task.ErrInvalid, err)
	case errors.Is(err, libheadgate.ErrLeaseLost):
		return errors.Join(task.ErrLeaseLost, err)
	case errors.Is(err, libheadgate.ErrBackpressure):
		return errors.Join(task.ErrBackpressure, err)
	case errors.Is(err, libheadgate.ErrUnavailable):
		return errors.Join(task.ErrUnavailable, err)
	default:
		return fmt.Errorf("headgate operation: %w", err)
	}
}

var _ task.Enqueuer = (*Adapter)(nil)
