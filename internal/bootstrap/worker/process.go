package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	fraudpostgres "github.com/Mujhtech/idenqa/internal/fraud/postgres"
	identitypostgres "github.com/Mujhtech/idenqa/internal/identity/postgres"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/config"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	"github.com/Mujhtech/idenqa/internal/pack"
	packpostgres "github.com/Mujhtech/idenqa/internal/pack/postgres"
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
	reviewpostgres "github.com/Mujhtech/idenqa/internal/review/postgres"
	reviewtask "github.com/Mujhtech/idenqa/internal/review/task"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/Mujhtech/idenqa/internal/verification/synthetic"
	"github.com/Mujhtech/idenqa/internal/verification/syntheticplan"
	verificationtask "github.com/Mujhtech/idenqa/internal/verification/task"
)

// Process owns the worker runtime and every resource composed for it.
type Process struct {
	identity               *identitypostgres.Store
	reviewWorker           *reviewtask.Worker
	providerConnection     interface{ Close() error }
	worker                 *taskheadgate.Worker
	coordinator            *verificationtask.Coordinator
	fraud                  *fraudpostgres.Store
	processing             *verificationpostgres.ProcessingStore
	processingBatch        int
	policyCoordinator      *policytask.Coordinator
	privacyCoordinator     *privacytask.Coordinator
	duties                 *taskheadgate.Adapter
	progressListener       *postgres.NotificationListener
	providers              *telemetry.Providers
	database               *postgres.Pool
	evidence               EvidenceLifecycle
	deliveryLifecycle      EvidenceLifecycle
	deliveryCoordinator    *deliverytask.Coordinator
	fanoutCoordinator      *deliverytask.FanoutCoordinator
	webhookRetention       *deliverypostgres.Store
	expiryCoordinator      *verificationtask.ExpiryCoordinator
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
	delivery, err := configuredLocalDelivery(configuration)
	if err != nil {
		if infrastructure.enabled() {
			_ = infrastructure.lifecycle.Shutdown(context.Background())
		}
		return nil, err
	}
	return NewProcessWithInfrastructure(ctx, configuration, logger, build, registry, infrastructure, delivery)
}

// NewProcessWithInfrastructure composes provider-neutral evidence and signed delivery resources.
func NewProcessWithInfrastructure(ctx context.Context, configuration config.Worker, logger *slog.Logger, build buildinfo.Info, registry *task.Registry, infrastructure EvidenceInfrastructure, deliveryInfrastructure DeliveryInfrastructure) (*Process, error) {
	constructed := false
	defer func() {
		if !constructed {
			if infrastructure.enabled() {
				_ = infrastructure.lifecycle.Shutdown(context.Background())
			}
			if deliveryInfrastructure.enabled() {
				_ = deliveryInfrastructure.lifecycle.Shutdown(context.Background())
			}
		}
	}()
	if logger == nil || registry == nil {
		return nil, errors.New("worker logger and task registry are required")
	}
	providers, err := telemetry.NewConfiguredProviders(
		ctx,
		"idenqa-worker",
		build.Version,
		telemetryConfig(configuration.API),
	)
	if err != nil {
		return nil, fmt.Errorf("construct worker telemetry: %w", err)
	}
	metrics, err := telemetry.NewDomainMetrics(providers.MeterProvider())
	if err != nil {
		_ = providers.Shutdown(context.Background())
		return nil, fmt.Errorf("construct worker domain metrics: %w", err)
	}
	defer func() {
		if !constructed {
			_ = providers.Shutdown(context.Background())
		}
	}()
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
	checkStore, err := verificationpostgres.NewGuardedCheckStore(connectionPool, deliveryInfrastructure.wrapper, clock.System{})
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct verification check store: %w", err)
	}
	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct worker identifiers: %w", err)
	}
	realProvider, err := configuredProvider(ctx, configuration, connectionPool, identifiers, deliveryInfrastructure.wrapper)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	providerTransferred := false
	defer func() {
		if realProvider != nil && !providerTransferred {
			_ = realProvider.connection.Close()
		}
	}()
	realModel, err := configuredModel(ctx, configuration, connectionPool, identifiers, deliveryInfrastructure.wrapper)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	modelTransferred := false
	defer func() {
		if realModel != nil && !modelTransferred {
			_ = realModel.connection.Close()
		}
	}()
	var processing *verificationpostgres.ProcessingStore
	if configuration.SyntheticProcessing {
		processing, err = verificationpostgres.NewProcessingStore(connectionPool, deliveryInfrastructure.wrapper, syntheticplan.Plan{}, identifiers, adapter, clock.System{})
		if err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct synthetic processing planner: %w", err)
		}
		logger.WarnContext(ctx, "synthetic processing enabled: fixture checks do not verify identity")
	}
	routes := []executionRoute{}
	if realProvider != nil {
		route := executionRoute{planner: realProvider.plan, preparation: realProvider.preparation, signals: realProvider.plan.OutputSignals()}
		if realProvider.registrations != nil {
			route.registration = &registrationRoute{source: realProvider.registrations, manifest: realProvider.plan.Manifest,
				template: realProvider.plan.Binding, deployment: realProvider.plan, preparation: realProvider.preparation}
		}
		routes = append(routes, route)
	}
	if realModel != nil {
		routes = append(routes, executionRoute{planner: realModel.plan, preparation: realModel.preparation, signals: realModel.signals})
	}
	if len(routes) > 0 {
		combined, composeErr := composeRoutes(ctx, routes)
		if composeErr != nil {
			connectionPool.Close()
			return nil, composeErr
		}
		processing, err = verificationpostgres.NewProcessingStore(connectionPool, deliveryInfrastructure.wrapper, combined, identifiers, adapter, clock.System{})
		if err != nil {
			connectionPool.Close()
			return nil, err
		}
		if err := processing.WithPreparation(combined); err != nil {
			connectionPool.Close()
			return nil, err
		}
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
	executeHandler, err := verificationtask.NewExecuteHandlerWithRequests(
		checkStore,
		identifiers,
		synthetic.Provider{Scenario: synthetic.Success, Now: clock.System{}.Now},
		synthetic.Model{Scenario: synthetic.Success, Now: clock.System{}.Now},
		syntheticRequests{enabled: configuration.SyntheticProcessing},
	)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct verification execute handler: %w", err)
	}
	if realProvider != nil {
		executeHandler, err = verificationtask.NewExecuteHandlerWithRequests(checkStore, identifiers, realProvider.executor, synthetic.Model{Scenario: synthetic.Success, Now: clock.System{}.Now}, realProvider.requests)
		if err != nil {
			connectionPool.Close()
			return nil, err
		}
	}
	if err := executeHandler.WithModelRequests(syntheticModelRequests{enabled: configuration.SyntheticProcessing}); err != nil {
		connectionPool.Close()
		return nil, err
	}
	if realModel != nil {
		if realProvider != nil {
			executeHandler, err = verificationtask.NewExecuteHandlerWithRequests(checkStore, identifiers, realProvider.executor, realModel.executor, realProvider.requests)
		} else {
			executeHandler, err = verificationtask.NewExecuteHandlerWithRequests(checkStore, identifiers, synthetic.Provider{Scenario: synthetic.Success, Now: clock.System{}.Now}, realModel.executor, syntheticRequests{})
		}
		if err != nil {
			connectionPool.Close()
			return nil, err
		}
		if err := executeHandler.WithModelRequests(realModel.requests); err != nil {
			connectionPool.Close()
			return nil, err
		}
	}
	lifecycleStore, err := verificationpostgres.NewLifecycleStore(connectionPool, deliveryInfrastructure.wrapper, clock.System{})
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct verification lifecycle store: %w", err)
	}
	lifecycleStore.WithMetrics(metrics)
	externalWait, err := verificationpostgres.NewExternalWaitStore(connectionPool, lifecycleStore, identifiers, clock.System{})
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct verification external wait store: %w", err)
	}
	if err := executeHandler.WithExternalWait(externalWait); err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("configure verification external wait: %w", err)
	}
	if err := executeHandler.WithSemanticRetries(identifiers, adapter); err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("configure verification semantic retries: %w", err)
	}
	packSeeds, err := pack.Seeds()
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("load embedded packs: %w", err)
	}
	packStore, err := packpostgres.New(connectionPool)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	packRegistry, err := pack.NewRegistry(packSeeds, packStore, clock.System{}.Now)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct pack registry: %w", err)
	}
	if err := packRegistry.Load(ctx); err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("load pack lifecycle: %w", err)
	}
	if err := executeHandler.WithDocumentSupport(packRegistry); err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("configure document support: %w", err)
	}
	if err := registry.Register(verificationtask.ExecuteKey, executeHandler); err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("register verification execute handler: %w", err)
	}
	if err := registry.Register(verificationtask.AsyncExecuteKey, executeHandler); err != nil {
		connectionPool.Close()
		return nil, err
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
	policyStore, err := policypostgres.NewGuarded(connectionPool, deliveryInfrastructure.wrapper, clock.System{})
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct policy store: %w", err)
	}
	stopStore, err := verificationpostgres.NewStopStore(connectionPool, deliveryInfrastructure.wrapper, identifiers, clock.System{})
	if err != nil {
		connectionPool.Close()
		return nil, err
	}

	identityStore, err := identitypostgres.New(connectionPool, deliveryInfrastructure.wrapper, deliveryInfrastructure.unwrapper, identifiers, stopStore)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	fraudStore, err := fraudpostgres.New(connectionPool, deliveryInfrastructure.unwrapper)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	policySource, err := policypostgres.NewSource(
		connectionPool, policypostgres.SessionPolicySelector{}, policypostgres.DirectFactProjector{},
		fraudStore, identityStore, reviewpostgres.RecaptureFactProjector{},
	)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct authoritative policy source: %w", err)
	}
	policySource.EnableDecisionContext()
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
	completion, err := verificationpostgres.NewCompletionStore(connectionPool, deliveryInfrastructure.wrapper, identifiers, clock.System{})
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	completion.WithMetrics(metrics)
	reviewRules, err := config.LoadReviewRouting(configuration.ReviewRoutingFile)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("load review routing: %w", err)
	}
	reviewRouting, err := reviewpostgres.NewRoutingStore(connectionPool, deliveryInfrastructure.wrapper, identifiers, clock.System{}, reviewRules)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct review routing: %w", err)
	}
	policyHandler, err := policytask.NewHandlerWithRouting(policyStore, policyBuilder, completion, reviewRouting)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct policy author handler: %w", err)
	}
	reviewEvaluations, err := reviewpostgres.NewEvaluationStore(connectionPool, deliveryInfrastructure.wrapper, policyEvaluator, completion, clock.System{})
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	reviewWorker, err := reviewtask.New(reviewEvaluations, identifiers, adapter, clock.System{})
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	if err := registry.Register(reviewtask.EvaluationKey, reviewWorker); err != nil {
		connectionPool.Close()
		return nil, err
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
	expiryHandler, err := verificationtask.NewExpiryHandler(stopStore)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	if err := registry.Register(verificationtask.ExpireKey, expiryHandler); err != nil {
		connectionPool.Close()
		return nil, err
	}
	expiryCoordinator, err := verificationtask.NewExpiryCoordinator(stopStore, identifiers, adapter, clock.System{})
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	var deliveryCoordinator *deliverytask.Coordinator
	var fanoutCoordinator *deliverytask.FanoutCoordinator
	var webhookRetention *deliverypostgres.Store
	if deliveryInfrastructure.enabled() {
		store, err := deliverypostgres.New(connectionPool)
		if err != nil {
			connectionPool.Close()
			return nil, err
		}
		handler, err := deliverytask.NewHandler(store, deliveryInfrastructure.unwrapper, deliveryInfrastructure.sender, identifiers, adapter, clock.System{}.Now)
		if err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct webhook handler: %w", err)
		}
		handler.WithMetrics(metrics)
		if err := registry.Register(deliverytask.DeliverKey, handler); err != nil {
			connectionPool.Close()
			return nil, err
		}
		fanout, err := deliverytask.NewFanoutHandler(store, identifiers, adapter, clock.System{}.Now)
		if err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct webhook fanout handler: %w", err)
		}
		if err := registry.Register(deliverytask.FanoutKey, fanout); err != nil {
			connectionPool.Close()
			return nil, err
		}
		deliveryCoordinator, err = deliverytask.NewCoordinator(store, identifiers, adapter, clock.System{}, deliverytask.CoordinationBatch)
		if err != nil {
			connectionPool.Close()
			return nil, err
		}
		fanoutCoordinator, err = deliverytask.NewFanoutCoordinator(store, identifiers, adapter, clock.System{}, deliverytask.CoordinationBatch)
		if err != nil {
			connectionPool.Close()
			return nil, err
		}
		webhookRetention = store
	}
	var privacyCoordinator *privacytask.Coordinator
	if infrastructure.enabled() {
		privacyStore, err := privacypostgres.New(connectionPool, deliveryInfrastructure.wrapper)
		if err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct worker privacy persistence: %w", err)
		}
		evidenceEraser, err := privacypostgres.NewEvidenceEraser(connectionPool, infrastructure.objects, deliveryInfrastructure.wrapper, time.Now)
		if err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct worker evidence eraser: %w", err)
		}
		identityEraser, err := identitypostgres.NewEraser(identityStore, evidenceEraser, time.Now)
		if err != nil {
			connectionPool.Close()
			return nil, err
		}
		privacyService, err := privacy.NewService(privacyStore, identityEraser, identifiers, time.Now)
		if err != nil {
			connectionPool.Close()
			return nil, fmt.Errorf("construct worker privacy service: %w", err)
		}
		privacyService.WithMetrics(metrics)
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
	bridge, err := taskheadgate.NewTelemetry(
		providers.TracerProvider(), providers.MeterProvider(), configuration.HeadgateInstallationID,
	)
	if err != nil {
		connectionPool.Close()
		return nil, fmt.Errorf("construct task telemetry: %w", err)
	}
	if realProvider != nil {
		realProvider.withMetrics(metrics)
	}
	if realModel != nil {
		realModel.withMetrics(metrics)
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
		connectionPool.Close()
		return nil, err
	}
	backgroundWorker, err := adapter.NewWorker(registry, workerConfiguration, bridge)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	progressListener, listenErr := connectionPool.OpenNotificationListener(
		ctx, verificationpostgres.CheckProgressNotificationChannel,
	)
	if listenErr != nil {
		logger.WarnContext(ctx, "verification progress notification listener unavailable; using durable polling")
	}
	var providerConnection interface{ Close() error }
	if realProvider != nil {
		providerConnection = realProvider.connection
		providerTransferred = true
	}
	if realModel != nil {
		if providerConnection != nil {
			providerConnection = runtimeConnections{providerConnection, realModel.connection}
		} else {
			providerConnection = realModel.connection
		}
		modelTransferred = true
	}
	process := &Process{providerConnection: providerConnection,
		deliveryCoordinator: deliveryCoordinator,
		fanoutCoordinator:   fanoutCoordinator,
		webhookRetention:    webhookRetention,
		expiryCoordinator:   expiryCoordinator,
		processing:          processing, processingBatch: configuration.ReconciliationBatchSize,
		identity: identityStore, fraud: fraudStore, reviewWorker: reviewWorker, worker: backgroundWorker, coordinator: coordinator, policyCoordinator: policyCoordinator, privacyCoordinator: privacyCoordinator, duties: adapter,
		progressListener: progressListener, providers: providers, database: connectionPool,
		evidence: infrastructure.lifecycle, deliveryLifecycle: deliveryInfrastructure.lifecycle,
		logger: logger, workerID: workerID,
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
	process.scheduleExpirations(ctx)
	process.scheduleWebhookDeliveries(ctx)
	process.startCapturedSessions(ctx)
	process.scheduleReconciliations(ctx)
	process.schedulePolicyAuthorships(ctx)
	process.schedulePrivacyDeletions(ctx)
	process.expireWebhookData(ctx)
	process.projectPendingProgress(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-reconciliationTicker.C:
			process.scheduleReconciliations(ctx)
			process.schedulePrivacyDeletions(ctx)
			process.expireWebhookData(ctx)
		case <-progressTicker.C:
			process.scheduleExpirations(ctx)
			process.scheduleWebhookDeliveries(ctx)
			process.startCapturedSessions(ctx)
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

func (process *Process) expireWebhookData(ctx context.Context) {
	if process.webhookRetention == nil {
		return
	}
	lease := min(2*process.reconciliationInterval, 2*time.Hour)
	claimed, err := process.duties.RunDuty(ctx, taskheadgate.DutyWebhookRetention, process.workerID, lease, func(ctx context.Context) error {
		result, err := process.webhookRetention.ExpireWebhookData(ctx, time.Now().UTC().Truncate(time.Microsecond), 500)
		if err == nil && (result.PayloadsExpired > 0 || result.TombstonesPurged > 0) {
			process.logger.InfoContext(ctx, "expired webhook retention data",
				"payloads", result.PayloadsExpired, "attempts", result.AttemptsExpired, "tombstones", result.TombstonesPurged)
		}
		return err
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		process.logger.ErrorContext(ctx, "expire webhook retention data", "error", err)
	} else if claimed {
		process.logger.DebugContext(ctx, "completed webhook retention duty")
	}
}

func (process *Process) scheduleWebhookDeliveries(ctx context.Context) {
	if process.deliveryCoordinator == nil {
		return
	}
	scheduled, err := process.deliveryCoordinator.ScheduleReadyDeliveries(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		process.logger.ErrorContext(ctx, "schedule webhook deliveries", "error", err)
	} else if scheduled > 0 {
		process.logger.DebugContext(ctx, "scheduled webhook deliveries", "count", scheduled)
	}
	if process.fanoutCoordinator == nil {
		return
	}
	fanned, err := process.fanoutCoordinator.ScheduleReadyFanouts(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		process.logger.ErrorContext(ctx, "schedule webhook fanout", "error", err)
	} else if fanned > 0 {
		process.logger.DebugContext(ctx, "scheduled webhook fanout", "count", fanned)
	}
}

func (process *Process) startCapturedSessions(ctx context.Context) {
	if process.processing == nil {
		return
	}
	started, err := process.processing.Sweep(ctx, process.processingBatch)
	if err != nil && !errors.Is(err, context.Canceled) {
		process.logger.ErrorContext(ctx, "start captured verifications", "error", err)
	} else if started > 0 {
		process.logger.InfoContext(ctx, "started captured verifications", "count", started)
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
			if process.identity != nil {
				if err := process.identity.Cleanup(ctx, time.Now().UTC()); err != nil {
					return err
				}
			}
			if process.fraud != nil {
				if err := process.fraud.Cleanup(ctx); err != nil {
					return err
				}
			}
			if process.reviewWorker != nil {
				if err := process.reviewWorker.Schedule(ctx); err != nil {
					return err
				}
			}
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
	if process.providerConnection != nil {
		err = errors.Join(err, process.providerConnection.Close())
	}
	if process.evidence != nil {
		err = errors.Join(err, process.evidence.Shutdown(ctx))
	}
	if process.deliveryLifecycle != nil {
		err = errors.Join(err, process.deliveryLifecycle.Shutdown(ctx))
	}
	process.database.Close()
	return err
}

func (process *Process) scheduleExpirations(ctx context.Context) {
	if process.expiryCoordinator == nil {
		return
	}
	if _, err := process.expiryCoordinator.ScheduleExpired(ctx); err != nil && !errors.Is(err, context.Canceled) {
		process.logger.ErrorContext(ctx, "schedule verification expirations", "error", err)
	}
}
