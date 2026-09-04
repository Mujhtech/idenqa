package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/platform/telemetry"
	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	policytask "github.com/Mujhtech/idenqa/internal/policy/task"
	"github.com/Mujhtech/idenqa/internal/privacy"
	privacypostgres "github.com/Mujhtech/idenqa/internal/privacy/postgres"
	privacytask "github.com/Mujhtech/idenqa/internal/privacy/task"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/Mujhtech/idenqa/internal/verification/synthetic"
	verificationtask "github.com/Mujhtech/idenqa/internal/verification/task"
)

// Process owns the worker runtime and every resource composed for it.
type Process struct {
	worker                 *taskheadgate.Worker
	coordinator            *verificationtask.Coordinator
	policyCoordinator      *policytask.Coordinator
	privacyCoordinator     *privacytask.Coordinator
	duties                 *taskheadgate.Adapter
	progressListener       *postgres.NotificationListener
	providers              *telemetry.Providers
	database               *postgres.Pool
	evidence               EvidenceLifecycle
	logger                 *slog.Logger
	workerID               string
	reconciliationInterval time.Duration
	progressPollInterval   time.Duration
}

// NewProcess validates both application and Headgate schemas before worker admission.
func NewProcess(
	ctx context.Context,
	configuration config.Worker,
	logger *slog.Logger,
	build buildinfo.Info,
	registry *task.Registry,
) (*Process, error) {
	infrastructure, err := configuredLocalEvidence(configuration)
	if err != nil {
		return nil, err
	}
	return NewProcessWithEvidence(ctx, configuration, logger, build, registry, infrastructure)
}

// NewProcessWithEvidence composes worker-owned regional object deletion.
func NewProcessWithEvidence(
	ctx context.Context,
	configuration config.Worker,
	logger *slog.Logger,
	build buildinfo.Info,
	registry *task.Registry,
	infrastructure EvidenceInfrastructure,
) (*Process, error) {
	constructed := false
	defer func() {
		if !constructed && infrastructure.enabled() {
			_ = infrastructure.lifecycle.Shutdown(context.Background())
		}
	}()
	if logger == nil || registry == nil {
		return nil, errors.New("worker logger and task registry are required")
	}
	connectionPool, err := postgres.Open(ctx, postgres.Config{
		URL: configuration.DatabaseURL, Role: configuration.DatabaseRole,
		MaxConnections:      configuration.DatabaseMaxConnections,
		MinConnections:      configuration.DatabaseMinConnections,
		MaxConnectionAge:    configuration.DatabaseMaxLifetime,
		MaxConnectionIdle:   configuration.DatabaseMaxIdleTime,
		HealthCheckInterval: configuration.DatabaseHealthInterval,
		ConnectTimeout:      configuration.DatabaseConnectTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("open worker database: %w", err)
	}
	if err := connectionPool.Check(ctx, migrations.LatestVersion); err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("check application database schema: %w", err)
	}
	_, err = taskheadgate.CheckPoolSchema(ctx, connectionPool.Native(), configuration.HeadgateSchema)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	adapterConfiguration := taskheadgate.DefaultConfig(configuration.HeadgateInstallationID)
	adapterConfiguration.Schema = configuration.HeadgateSchema
	adapterConfiguration.CrashLimit = configuration.CrashLimit
	adapterConfiguration.RetryBase = configuration.RetryBase
	adapterConfiguration.RetryCap = configuration.RetryCap
	adapter, err := taskheadgate.NewPostgres(connectionPool.Native(), adapterConfiguration)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	checkStore, err := verificationpostgres.NewCheckStore(connectionPool)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct verification check store: %w", err)
	}
	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct worker identifiers: %w", err)
	}
	workerID := configuration.WorkerID
	if workerID == "" {
		generatedWorkerID, generateErr := identifiers.NewTask()
		if generateErr != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct worker identity: %w", generateErr)
		}
		workerID = strings.ToLower(generatedWorkerID.String())
	}
	executeHandler, err := verificationtask.NewExecuteHandler(
		checkStore,
		identifiers,
		synthetic.Provider{Scenario: synthetic.Success, Now: clock.System{}.Now},
		synthetic.Model{Scenario: synthetic.Success, Now: clock.System{}.Now},
	)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct verification execute handler: %w", err)
	}
	if err := registry.Register(verificationtask.ExecuteKey, executeHandler); err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("register verification execute handler: %w", err)
	}
	reconcileHandler, err := verificationtask.NewReconcileHandler(
		checkStore, identifiers, clock.System{}, configuration.ReconciliationItemLease,
	)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct verification reconcile handler: %w", err)
	}
	if err := registry.Register(verificationtask.ReconcileKey, reconcileHandler); err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("register verification reconcile handler: %w", err)
	}
	policyStore, err := policypostgres.New(connectionPool)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct policy store: %w", err)
	}
	policySource, err := policypostgres.NewSource(
		connectionPool, policypostgres.SessionPolicySelector{}, policypostgres.DirectFactProjector{},
	)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct authoritative policy source: %w", err)
	}
	policyInputs, err := policy.NewActiveInputLoader(policySource, policyStore)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct active policy input loader: %w", err)
	}
	policyEvaluator, err := policycel.NewResolver(policyStore, 256)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct policy evaluator: %w", err)
	}
	policyBuilder, err := policy.NewBuilder(policyInputs, policyEvaluator)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct policy decision builder: %w", err)
	}
	policyHandler, err := policytask.NewHandler(policyStore, policyBuilder)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct policy author handler: %w", err)
	}
	if err := registry.Register(policytask.AuthorKey, policyHandler); err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("register policy author handler: %w", err)
	}
	coordinator, err := verificationtask.NewCoordinator(
		checkStore, identifiers, adapter, clock.System{}, configuration.ReconciliationBatchSize,
	)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct verification coordinator: %w", err)
	}
	policyCoordinator, err := policytask.NewCoordinator(
		policyStore, identifiers, adapter, clock.System{}, configuration.ReconciliationBatchSize,
	)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct policy coordinator: %w", err)
	}
	var privacyCoordinator *privacytask.Coordinator
	if infrastructure.enabled() {
		privacyStore, err := privacypostgres.New(connectionPool)
		if err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct worker privacy persistence: %w", err)
		}
		evidenceEraser, err := privacypostgres.NewEvidenceEraser(connectionPool, infrastructure.objects, time.Now)
		if err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct worker evidence eraser: %w", err)
		}
		privacyService, err := privacy.NewService(privacyStore, evidenceEraser, identifiers, time.Now)
		if err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct worker privacy service: %w", err)
		}
		privacyHandler, err := privacytask.NewHandler(privacyService)
		if err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct privacy deletion handler: %w", err)
		}
		if err := registry.Register(privacytask.DeleteKey, privacyHandler); err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("register privacy deletion handler: %w", err)
		}
		privacyCoordinator, err = privacytask.NewCoordinator(privacyStore, identifiers, adapter, clock.System{}, configuration.ReconciliationBatchSize)
		if err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct privacy deletion coordinator: %w", err)
		}
	}
	providers, err := telemetry.NewConfiguredProviders(
		ctx,
		"idenqa-worker",
		build.Version,
		telemetryConfig(configuration.API),
	)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct worker telemetry: %w", err)
	}
	bridge, err := taskheadgate.NewTelemetry(
		providers.TracerProvider(), providers.MeterProvider(), configuration.HeadgateInstallationID,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct task telemetry: %w", err)
	}
	workerConfiguration := taskheadgate.DefaultWorkerConfig()
	workerConfiguration.WorkerID = workerID
	workerConfiguration.QueueWorkers[taskheadgate.QueueVerification] = configuration.VerificationWorkers
	workerConfiguration.QueueWorkers[taskheadgate.QueueEvidence] = configuration.EvidenceWorkers
	workerConfiguration.QueueWorkers[taskheadgate.QueueDelivery] = configuration.DeliveryWorkers
	workerConfiguration.QueueWorkers[taskheadgate.QueueMaintenance] = configuration.MaintenanceWorkers
	workerConfiguration.QueuePolicies[taskheadgate.QueueVerification] = taskheadgate.QueuePolicy{
		RateLimit:         configuration.VerificationRateLimit,
		RatePeriod:        configuration.VerificationRatePeriod,
		RateBurst:         configuration.VerificationRateBurst,
		TenantConcurrency: configuration.VerificationTenantConcurrency,
	}
	workerConfiguration.QueuePolicies[taskheadgate.QueueEvidence] = taskheadgate.QueuePolicy{
		RateLimit:         configuration.EvidenceRateLimit,
		RatePeriod:        configuration.EvidenceRatePeriod,
		RateBurst:         configuration.EvidenceRateBurst,
		TenantConcurrency: configuration.EvidenceTenantConcurrency,
	}
	workerConfiguration.QueuePolicies[taskheadgate.QueueDelivery] = taskheadgate.QueuePolicy{
		RateLimit:         configuration.DeliveryRateLimit,
		RatePeriod:        configuration.DeliveryRatePeriod,
		RateBurst:         configuration.DeliveryRateBurst,
		TenantConcurrency: configuration.DeliveryTenantConcurrency,
	}
	workerConfiguration.QueuePolicies[taskheadgate.QueueMaintenance] = taskheadgate.QueuePolicy{
		RateLimit:         configuration.MaintenanceRateLimit,
		RatePeriod:        configuration.MaintenanceRatePeriod,
		RateBurst:         configuration.MaintenanceRateBurst,
		TenantConcurrency: configuration.MaintenanceTenantConcurrency,
	}
	workerConfiguration.LeaseDuration = configuration.LeaseDuration
	workerConfiguration.ShutdownTimeout = configuration.ShutdownTimeout
	workerConfiguration.EmptyPollFloor = configuration.EmptyPollFloor
	workerConfiguration.EmptyPollCeiling = configuration.EmptyPollCeiling
	workerConfiguration.MemoryLimitBytes = configuration.MemoryLimitBytes
	workerConfiguration.MemoryCheckPeriod = configuration.MemoryCheckPeriod
	if err := adapter.ApplyPolicies(ctx, workerConfiguration); err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	backgroundWorker, err := adapter.NewWorker(registry, workerConfiguration, bridge)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	progressListener, listenErr := connectionPool.OpenNotificationListener(
		ctx, verificationpostgres.CheckProgressNotificationChannel,
	)
	if listenErr != nil {
		logger.WarnContext(ctx, "verification progress notification listener unavailable; using durable polling")
	}
	process := &Process{
		worker: backgroundWorker, coordinator: coordinator, policyCoordinator: policyCoordinator, privacyCoordinator: privacyCoordinator, duties: adapter,
		progressListener: progressListener, providers: providers, database: connectionPool,
		evidence: infrastructure.lifecycle,
		logger:   logger, workerID: workerID,
		reconciliationInterval: configuration.ReconciliationSweepInterval,
		progressPollInterval:   configuration.ProgressPollInterval,
	}
	constructed = true
	return process, nil
}

func telemetryConfig(configuration config.API) telemetry.Config {
	return telemetry.Config{
		Protocol:           configuration.TelemetryProtocol,
		Endpoint:           configuration.TelemetryEndpoint,
		Insecure:           configuration.TelemetryInsecure,
		Headers:            configuration.TelemetryHeaders.Values(),
		TLSCAFile:          configuration.TelemetryTLSCAFile,
		TLSCertificateFile: configuration.TelemetryTLSCertFile,
		TLSKeyFile:         configuration.TelemetryTLSKeyFile,
		TLSServerName:      configuration.TelemetryTLSServerName,
		TraceSamplingRatio: configuration.TelemetryTraceSampleRatio,
		MetricInterval:     configuration.TelemetryMetricInterval,
		ExportTimeout:      configuration.TelemetryExportTimeout,
	}
}

// Run blocks through admission and graceful drain.
func (process *Process) Run(ctx context.Context) error {
	if process == nil || process.worker == nil || process.coordinator == nil || process.policyCoordinator == nil || process.duties == nil {
		return errors.New("worker process is incomplete")
	}
	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	var wait sync.WaitGroup
	wait.Go(func() { process.runCoordination(runContext) })
	err := process.worker.Run(runContext)
	cancel()
	wait.Wait()
	return err
}

func (process *Process) runCoordination(ctx context.Context) {
	reconciliationTicker := time.NewTicker(process.reconciliationInterval)
	progressTicker := time.NewTicker(process.progressPollInterval)
	defer reconciliationTicker.Stop()
	defer progressTicker.Stop()

	notifications := make(chan id.Tenant, 1)
	var notificationWait sync.WaitGroup
	if process.progressListener != nil {
		notificationWait.Go(func() { process.forwardProgressNotifications(ctx, notifications) })
	}
	defer notificationWait.Wait()
	process.scheduleReconciliations(ctx)
	process.schedulePolicyAuthorships(ctx)
	process.schedulePrivacyDeletions(ctx)
	process.projectPendingProgress(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-reconciliationTicker.C:
			process.scheduleReconciliations(ctx)
			process.schedulePrivacyDeletions(ctx)
		case <-progressTicker.C:
			process.projectPendingProgress(ctx)
			process.schedulePolicyAuthorships(ctx)
		case tenantID := <-notifications:
			projected, err := process.coordinator.ProjectTenantProgress(ctx, tenantID)
			if err != nil && !errors.Is(err, context.Canceled) {
				process.logger.ErrorContext(ctx, "project notified verification progress", "error", err)
			} else if projected > 0 {
				process.logger.DebugContext(ctx, "projected notified verification progress", "count", projected)
			}
		}
	}
}

func (process *Process) schedulePrivacyDeletions(ctx context.Context) {
	if process.privacyCoordinator == nil {
		return
	}
	lease := min(2*process.reconciliationInterval, 2*time.Hour)
	claimed, err := process.duties.RunDuty(ctx, taskheadgate.DutyPrivacyDeletion, process.workerID, lease, func(ctx context.Context) error {
		scheduled, err := process.privacyCoordinator.ScheduleDueDeletions(ctx)
		if err == nil && scheduled > 0 {
			process.logger.InfoContext(ctx, "scheduled privacy deletion", "count", scheduled)
		}
		return err
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		process.logger.ErrorContext(ctx, "schedule privacy deletion", "error", err)
		return
	}
	if !claimed {
		process.logger.DebugContext(ctx, "privacy deletion scheduler held by another worker")
	}
}

func (process *Process) schedulePolicyAuthorships(ctx context.Context) {
	lease := min(2*process.progressPollInterval, time.Minute)
	claimed, err := process.duties.RunDuty(
		ctx, taskheadgate.DutyPolicyAuthorship, process.workerID, lease,
		func(ctx context.Context) error {
			scheduled, err := process.policyCoordinator.ScheduleReadyAuthorships(ctx)
			if err == nil && scheduled > 0 {
				process.logger.InfoContext(ctx, "scheduled policy authorship", "count", scheduled)
			}
			return err
		},
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		process.logger.ErrorContext(ctx, "schedule policy authorship", "error", err)
		return
	}
	if !claimed {
		process.logger.DebugContext(ctx, "policy authorship scheduler held by another worker")
	}
}

func (process *Process) scheduleReconciliations(ctx context.Context) {
	lease := min(2*process.reconciliationInterval, 2*time.Hour)
	claimed, err := process.duties.RunDuty(
		ctx,
		taskheadgate.DutyVerificationReconciliation,
		process.workerID,
		lease,
		func(ctx context.Context) error {
			scheduled, err := process.coordinator.ScheduleReconciliations(ctx)
			if err == nil && scheduled > 0 {
				process.logger.InfoContext(ctx, "scheduled verification reconciliation", "count", scheduled)
			}
			return err
		},
	)
	if err != nil && !errors.Is(err, context.Canceled) {
		process.logger.ErrorContext(ctx, "schedule verification reconciliation", "error", err)
		return
	}
	if !claimed {
		process.logger.DebugContext(ctx, "verification reconciliation scheduler held by another worker")
	}
}

func (process *Process) projectPendingProgress(ctx context.Context) {
	projected, err := process.coordinator.ProjectPendingProgress(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		process.logger.ErrorContext(ctx, "project pending verification progress", "error", err)
		return
	}
	if projected > 0 {
		process.logger.DebugContext(ctx, "projected pending verification progress", "count", projected)
	}
}

func (process *Process) forwardProgressNotifications(ctx context.Context, destination chan<- id.Tenant) {
	for {
		payload, err := process.progressListener.Wait(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				process.logger.WarnContext(ctx, "verification progress notifications stopped; durable polling remains active")
			}
			return
		}
		tenantID, err := id.ParseTenant(payload)
		if err != nil {
			process.logger.WarnContext(ctx, "discarding invalid verification progress notification")
			continue
		}
		select {
		case destination <- tenantID:
		default:
		}
	}
}

// Close flushes telemetry and releases PostgreSQL after the runner exits.
func (process *Process) Close(ctx context.Context) error {
	if process == nil {
		return nil
	}
	if process.progressListener != nil {
		process.progressListener.Close()
	}
	err := process.providers.Shutdown(ctx)
	if process.evidence != nil {
		err = errors.Join(err, process.evidence.Shutdown(ctx))
	}
	process.database.Close()
	return err
}
