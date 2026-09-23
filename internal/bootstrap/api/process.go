package api

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/Mujhtech/idenqa/internal/fraud"
	fraudpostgres "github.com/Mujhtech/idenqa/internal/fraud/postgres"
	"github.com/Mujhtech/idenqa/internal/identity"
	identitypostgres "github.com/Mujhtech/idenqa/internal/identity/postgres"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	keycustodypostgres "github.com/Mujhtech/idenqa/internal/keycustody/postgres"

	assetstore "github.com/Mujhtech/idenqa/adapters/experience/assetstore"
	ed25519signer "github.com/Mujhtech/idenqa/adapters/experience/ed25519signer"
	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/experience"
	experiencepostgres "github.com/Mujhtech/idenqa/internal/experience/postgres"
	"github.com/Mujhtech/idenqa/internal/model"
	modelpostgres "github.com/Mujhtech/idenqa/internal/model/postgres"
	"github.com/Mujhtech/idenqa/internal/pack"
	packpostgres "github.com/Mujhtech/idenqa/internal/pack/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	tinkcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto/tink"
	"github.com/Mujhtech/idenqa/internal/platform/cursor"
	"github.com/Mujhtech/idenqa/internal/platform/health"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/platform/telemetry"
	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/privacy"
	privacypostgres "github.com/Mujhtech/idenqa/internal/privacy/postgres"
	"github.com/Mujhtech/idenqa/internal/proposal"
	proposalpostgres "github.com/Mujhtech/idenqa/internal/proposal/postgres"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/realtime"
	realtimepostgres "github.com/Mujhtech/idenqa/internal/realtime/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	reviewpostgres "github.com/Mujhtech/idenqa/internal/review/postgres"
	"github.com/Mujhtech/idenqa/internal/support"
	supportpostgres "github.com/Mujhtech/idenqa/internal/support/postgres"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
	"github.com/Mujhtech/idenqa/internal/tenantexport"
	tenantexportpostgres "github.com/Mujhtech/idenqa/internal/tenantexport/postgres"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
	transportrealtime "github.com/Mujhtech/idenqa/internal/transport/realtime"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

type server interface {
	Serve(net.Listener) error
	ServeTLS(net.Listener, string, string) error
	Shutdown(context.Context) error
}

type listenFunc func(context.Context, string, string) (net.Listener, error)

type shutdowner interface {
	Shutdown(context.Context) error
}

type connectionDrainer interface {
	Drain(context.Context) error
}

type database interface {
	Check(context.Context, uint) error
	CheckHeadgate(context.Context, string) error
	WithinTransaction(
		context.Context,
		postgres.TransactionOptions,
		func(context.Context, postgres.Transaction) error,
	) error
	Close()
}

type notificationDatabase interface {
	OpenNotificationListener(context.Context, string) (*postgres.NotificationListener, error)
}

type closer interface{ Close() }

type databaseOpener func(context.Context, postgres.Config) (database, error)

type processDatabase struct {
	*postgres.Pool
}

func (database *processDatabase) CheckHeadgate(ctx context.Context, schema string) error {
	_, err := taskheadgate.CheckPoolSchema(ctx, database.Native(), schema)
	return err
}

// Process owns the API process lifecycle and the resources it constructs.
type Process struct {
	address          string
	shutdownTimeout  time.Duration
	server           server
	tlsEnabled       bool
	listen           listenFunc
	health           *health.State
	logger           *slog.Logger
	build            buildinfo.Info
	telemetry        shutdowner
	realtime         connectionDrainer
	wakeups          closer
	webhookWakeups   closer
	providerRunner   closer
	evidence         EvidenceLifecycle
	database         database
	databaseInterval time.Duration
	databaseTimeout  time.Duration
	headgateSchema   string
}

// NewProcess composes an API process from validated configuration.
func NewProcess(
	ctx context.Context,
	configuration config.API,
	logger *slog.Logger,
	state *health.State,
	build buildinfo.Info,
) (*Process, error) {
	infrastructure, err := configuredEvidence(ctx, configuration)
	if err != nil {
		return nil, err
	}

	return NewProcessWithEvidence(ctx, configuration, logger, state, build, infrastructure)
}

// NewProcessWithEvidence composes the API with provider-backed evidence ports.
// Independent distribution modules construct adapters, then transfer their
// lifecycle ownership here without adding cloud SDKs to the root module.
func NewProcessWithEvidence(
	ctx context.Context,
	configuration config.API,
	logger *slog.Logger,
	state *health.State,
	build buildinfo.Info,
	infrastructure EvidenceInfrastructure,
) (*Process, error) {
	return newProcess(ctx, configuration, logger, state, build, func(
		ctx context.Context,
		databaseConfiguration postgres.Config,
	) (database, error) {
		pool, err := postgres.Open(ctx, databaseConfiguration)
		if err != nil {
			return nil, err
		}
		return &processDatabase{Pool: pool}, nil
	}, infrastructure)
}

func newProcess(
	ctx context.Context,
	configuration config.API,
	logger *slog.Logger,
	state *health.State,
	build buildinfo.Info,
	openDatabase databaseOpener,
	infrastructure EvidenceInfrastructure,
) (*Process, error) {
	constructed := false
	var wakeupHub *realtimepostgres.WakeupHub
	var webhookWakeupHub *deliverypostgres.WakeupHub
	var providerRunner closer
	defer func() {
		if !constructed && infrastructure.enabled() {
			_ = infrastructure.lifecycle.Shutdown(context.Background())
		}
	}()
	defer func() {
		if !constructed && wakeupHub != nil {
			wakeupHub.Close()
		}
	}()
	defer func() {
		if !constructed && webhookWakeupHub != nil {
			webhookWakeupHub.Close()
		}
	}()
	defer func() {
		if !constructed && providerRunner != nil {
			providerRunner.Close()
		}
	}()
	if err := configuration.ValidateRealtimeBootstrap(); err != nil {
		return nil, fmt.Errorf("validate realtime bootstrap configuration: %w", err)
	}

	address := configuration.HTTPAddress()
	tlsEnabled := configuration.HTTPTLSMode == "file"
	var certificate tls.Certificate
	if tlsEnabled {
		loaded, err := tls.LoadX509KeyPair(configuration.HTTPTLSCertFile, configuration.HTTPTLSKeyFile)
		if err != nil {
			return nil, errors.New("load HTTP TLS certificate and key: pair is unreadable or invalid")
		}
		certificate = loaded
	}
	peppers, err := configuredPeppers(configuration)
	if err != nil {
		return nil, err
	}
	cursorCodec, err := configuredCursor(configuration)
	if err != nil {
		return nil, err
	}
	captureSigner, err := configuredCaptureSigner(configuration)
	if err != nil {
		return nil, err
	}
	outcomeSigner, err := configuredOutcomeSigner(configuration)
	if err != nil {
		return nil, err
	}
	connectionPool, err := openDatabase(ctx, postgres.Config{
		URL:                 configuration.DatabaseURL,
		Role:                configuration.DatabaseRole,
		MaxConnections:      configuration.DatabaseMaxConnections,
		MinConnections:      configuration.DatabaseMinConnections,
		MaxConnectionAge:    configuration.DatabaseMaxLifetime,
		MaxConnectionIdle:   configuration.DatabaseMaxIdleTime,
		HealthCheckInterval: configuration.DatabaseHealthInterval,
		ConnectTimeout:      configuration.DatabaseConnectTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("open API database: %w", err)
	}
	checkContext, cancel := context.WithTimeout(ctx, configuration.DatabaseHealthTimeout)
	if err := connectionPool.Check(checkContext, migrations.LatestVersion); err != nil {
		cancel()
		connectionPool.Close()

		return nil, fmt.Errorf("check API database schema: %w", err)
	}
	cancel()
	headgateContext, cancel := context.WithTimeout(ctx, configuration.DatabaseHealthTimeout)
	if err := connectionPool.CheckHeadgate(headgateContext, configuration.HeadgateSchema); err != nil {
		cancel()
		connectionPool.Close()

		return nil, fmt.Errorf("check API Headgate schema: %w", err)
	}
	cancel()

	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		connectionPool.Close()

		return nil, errors.New("construct identifier generator")
	}
	providers, err := telemetry.NewConfiguredProviders(
		ctx,
		"idenqa-api",
		build.Version,
		telemetryConfig(configuration),
	)
	if err != nil {
		connectionPool.Close()

		return nil, fmt.Errorf("construct API telemetry: %w", err)
	}
	metrics, err := telemetry.NewDomainMetrics(providers.MeterProvider())
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct API domain metrics: %w", err)
	}
	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	catalog, err := evidence.BuiltInCatalog()
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct evidence registry catalog: %w", err)
	}
	accessStore, err := accesspostgres.New(connectionPool)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct access persistence: %w", err)
	}
	tenantStore, err := tenantpostgres.New(connectionPool)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct tenant persistence: %w", err)
	}
	authenticator, err := access.NewAuthenticator(accessStore, peppers, clock.System{})
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct API key authenticator: %w", err)
	}
	accessMiddleware, err := httpapi.NewAccessMiddleware(authenticator, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct API access middleware: %w", err)
	}
	tenantReader, err := access.NewTenantReader(tenantStore)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct tenant reader: %w", err)
	}
	policyStore, err := policypostgres.New(connectionPool, infrastructure.keys)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct policy decision persistence: %w", err)
	}
	tenantExportStore, err := tenantexportpostgres.New(connectionPool, policyStore)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct tenant export persistence: %w", err)
	}
	tenantExporter, err := tenantexport.NewExporter(tenantexport.Sources{
		Tenant:                  tenantExportStore,
		CaptureProfiles:         tenantExportStore,
		Policies:                tenantExportStore,
		PolicyRevisions:         tenantExportStore,
		PolicyActivations:       tenantExportStore,
		Verifications:           tenantExportStore,
		VerificationTransitions: tenantExportStore,
		VerificationChecks:      tenantExportStore,
		VerificationAttempts:    tenantExportStore,
		Decisions:               tenantExportStore,
		AuditRecords:            tenantExportStore,
		WebhookEndpoints:        tenantExportStore,
		ReviewCases:             tenantExportStore,
		ReviewFindings:          tenantExportStore,
		IdentitySubjects:        tenantExportStore,
		IdentityRecords:         tenantExportStore,
		EvidenceAssets:          tenantExportStore,
		FraudConfiguration:      tenantExportStore,
		PrivacyDeletions:        tenantExportStore,
		PrivacyHolds:            tenantExportStore,
	}, nil)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct tenant exporter: %w", err)
	}
	tenantSubjectExporter, err := tenantexport.NewSubjectExporter(tenantExportStore, nil)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct subject exporter: %w", err)
	}
	tenantRoutes, err := httpapi.NewTenantRoutes(accessMiddleware, tenantReader, tenantExporter, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct tenant routes: %w", err)
	}
	decisionReader, err := policy.NewReader(policyStore)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct policy decision reader: %w", err)
	}
	decisionRoutes, err := httpapi.NewDecisionRoutes(accessMiddleware, decisionReader, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct policy decision routes: %w", err)
	}
	reviewStore, err := reviewpostgres.New(connectionPool, infrastructure.keys)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct review persistence: %w", err)
	}
	reviewAuthorityFile := config.ReviewAuthorityFile{Path: configuration.ReviewAuthorityFile}
	if err := reviewAuthorityFile.Validate(); err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("validate review authority: %w", err)
	}
	reviewAuthority, err := reviewpostgres.NewAuthority(connectionPool, reviewAuthorityFile)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}

	reviewStore = reviewStore.WithAuthority(reviewAuthority)
	reviewService, err := review.NewAuthorizedService(reviewStore, identifiers, time.Now, reviewAuthority)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct review service: %w", err)
	}
	reviewService.WithMetrics(metrics)
	reviewRoutes, err := httpapi.NewReviewRoutes(accessMiddleware, reviewService, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct review routes: %w", err)
	}
	recaptureStore, err := reviewpostgres.NewRecaptureStore(connectionPool, infrastructure.keys, catalog, clock.System{})
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	recaptureStore = recaptureStore.WithReviewerAuthority(reviewAuthority)
	recaptureService, err := review.NewRecaptureService(
		recaptureStore, reviewAuthority, identifiers, captureSigner, outcomeSigner, clock.System{},
		configuration.VerificationDefaultTTL, configuration.CaptureTokenDefaultTTL,
		configuration.OutcomeTokenDefaultPostTTL, configuration.VerificationIdempotencyTTL,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	reviewRoutes = reviewRoutes.WithRecapture(recaptureService).WithQueue(reviewStore, cursorCodec)
	profileStore, err := verificationpostgres.New(connectionPool, catalog)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct capture profile persistence: %w", err)
	}
	profileService, err := verification.NewService(
		profileStore,
		identifiers,
		catalog,
		clock.System{},
		configuration.ProfileIdempotencyTTL,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct capture profile service: %w", err)
	}
	profileRoutes, err := httpapi.NewProfileRoutes(
		accessMiddleware,
		profileService,
		catalog,
		cursorCodec,
		logger,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct capture profile routes: %w", err)
	}
	sessionStore, err := verificationpostgres.NewSessionStore(connectionPool, infrastructure.keys, catalog)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct verification session persistence: %w", err)
	}
	sessionStore.WithMetrics(metrics)
	sessionService, err := verification.NewSessionService(
		sessionStore,
		identifiers,
		captureSigner,
		outcomeSigner,
		clock.System{},
		verification.SessionLifetimes{
			VerificationDefault:  configuration.VerificationDefaultTTL,
			VerificationMaximum:  configuration.VerificationMaximumTTL,
			CaptureTokenDefault:  configuration.CaptureTokenDefaultTTL,
			CaptureTokenMaximum:  configuration.CaptureTokenMaximumTTL,
			OutcomePostDefault:   configuration.OutcomeTokenDefaultPostTTL,
			OutcomePostMaximum:   configuration.OutcomeTokenMaximumPostTTL,
			IdempotencyRetention: configuration.VerificationIdempotencyTTL,
		},
		configuration.Region,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct verification session service: %w", err)
	}
	var experienceStore *experiencepostgres.Store
	var experienceRoutes RouteRegistrar
	var experienceService *experience.Service
	if !configuration.ExperienceSigningKeys.IsZero() {
		keyring, err := ed25519signer.New(configuration.ExperienceSigningVersion, configuration.ExperienceSigningKeys.Values())
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct experience signing keyring: %w", err)
		}
		experienceStore, err = experiencepostgres.New(connectionPool)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct experience persistence: %w", err)
		}
		mandatoryCopy, err := experience.NewDefaultMandatoryCatalogue()
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct mandatory copy catalogue: %w", err)
		}
		var assetVerifier experience.AssetStore = experience.DenyAssets{}
		if objects, ok := infrastructure.objects.(evidence.ObjectReader); ok && objects != nil {
			verifier, err := assetstore.New(objects)
			if err != nil {
				_ = providers.Shutdown(context.Background())
				connectionPool.Close()

				return nil, fmt.Errorf("construct asset verifier: %w", err)
			}
			assetVerifier = verifier
		}
		experienceService, err = experience.NewService(experience.Deps{
			Repository: experienceStore, Pins: experienceStore, Signer: keyring, Verifier: keyring,
			Assets: assetVerifier, Mandatory: mandatoryCopy, IDs: identifiers, Clock: clock.System{},
		})
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct experience service: %w", err)
		}
		sessionService.WithExperience(experienceService)
	}
	captureAuthenticator, err := verification.NewCaptureAuthenticator(
		sessionStore,
		captureSigner,
		clock.System{},
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct capture authenticator: %w", err)
	}
	captureMiddleware, err := httpapi.NewCaptureAccessMiddleware(captureAuthenticator, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct capture access middleware: %w", err)
	}
	if experienceService != nil {
		registered, err := httpapi.NewExperienceRoutes(accessMiddleware, captureMiddleware, experienceService, cursorCodec, logger)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct experience routes: %w", err)
		}
		experienceRoutes = registered
	}
	captureOutcomeService, err := verification.NewCaptureOutcomeService(sessionStore)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct capture outcome service: %w", err)
	}
	outcomeAuthenticator, err := verification.NewOutcomeAuthenticator(sessionStore, outcomeSigner, clock.System{})
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct outcome authenticator: %w", err)
	}
	captureOutcomeMiddleware, err := httpapi.NewOutcomeAccessMiddleware(outcomeAuthenticator, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct capture outcome access middleware: %w", err)
	}
	captureOutcomeRoutes, err := httpapi.NewCaptureOutcomeRoutes(
		captureOutcomeMiddleware,
		captureOutcomeService,
		logger,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct capture outcome routes: %w", err)
	}
	stopStore, err := verificationpostgres.NewStopStore(connectionPool, infrastructure.keys, identifiers, clock.System{})
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	cancellationService, err := verification.NewCancellationService(stopStore, clock.System{}, configuration.VerificationIdempotencyTTL)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	cancellationMiddleware, err := httpapi.NewCaptureAccessMiddleware(verification.CancellationAuthenticator{Authenticator: captureAuthenticator}, logger)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	cancellationRoutes, err := httpapi.NewCancellationRoutes(accessMiddleware, cancellationMiddleware, cancellationService, logger)
	if err != nil {
		connectionPool.Close()
		return nil, err
	}
	verificationRoutes, err := httpapi.NewVerificationRoutes(
		accessMiddleware,
		captureMiddleware,
		sessionService,
		catalog,
		decisionReader,
		reviewService,
		logger,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct verification routes: %w", err)
	}
	documentSelectionStore, err := verificationpostgres.NewDocumentSelectionStore(sessionStore, clock.System{})
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct document selection store: %w", err)
	}
	documentSelectionService, err := verification.NewDocumentSelectionService(documentSelectionStore, identifiers, clock.System{}, configuration.VerificationIdempotencyTTL)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct document selection service: %w", err)
	}
	verificationRoutes.WithDocumentSelection(documentSelectionService)
	var nativeBootstrapRoutes RouteRegistrar
	if len(configuration.NativeApplicationIDs) > 0 {
		nativeBootstrapService, err := verification.NewNativeBootstrapService(sessionStore, clock.System{}, configuration.NativeApplicationIDs)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("construct native bootstrap service: %w", err)
		}
		nativeBootstrapRoutes, err = httpapi.NewNativeBootstrapRoutes(captureMiddleware, nativeBootstrapService, catalog, logger)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("construct native bootstrap routes: %w", err)
		}
	}
	authorityStore, err := authoritypostgres.New(connectionPool, infrastructure.keys)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct processing authority persistence: %w", err)
	}
	authorityService, err := authority.NewService(
		authorityStore,
		authorityStore,
		authorityStore,
		sessionStore,
		identifiers,
		clock.System{},
		configuration.VerificationIdempotencyTTL,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct processing authority service: %w", err)
	}
	authorityRoutes, err := httpapi.NewAuthorityRoutes(
		accessMiddleware,
		captureMiddleware,
		authorityService,
		logger,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct processing authority routes: %w", err)
	}
	realtimeStore, err := realtimepostgres.New(connectionPool)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct realtime ticket persistence: %w", err)
	}
	realtimeCommandStore, err := realtimepostgres.NewCommandStore(connectionPool, catalog)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct realtime command persistence: %w", err)
	}
	realtimeCommandService, err := realtime.NewCommandService(realtimeCommandStore, clock.System{})
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct realtime command service: %w", err)
	}
	ticketGenerator, err := realtime.NewSystemTicketGenerator()
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct realtime ticket generator: %w", err)
	}
	limitConfig := realtime.DefaultLimitConfig()
	limitConfig.TicketLifetime = configuration.RealtimeTicketLifetime
	limits, err := realtime.NewLimits(limitConfig)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct realtime limits: %w", err)
	}
	ticketService, err := realtime.NewService(
		realtimeStore,
		identifiers,
		ticketGenerator,
		clock.System{},
		limits,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct realtime ticket service: %w", err)
	}
	connectionRoutes, err := httpapi.NewCaptureConnectionRoutes(
		captureMiddleware,
		ticketService,
		configuration.RealtimeWebSocketURL,
		configuration.Region,
		configuration.HTTPCORSAllowedOrigins,
		logger,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct capture connection routes: %w", err)
	}
	replayStore, err := realtimepostgres.NewReplayStore(connectionPool)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct realtime replay store: %w", err)
	}
	if source, ok := connectionPool.(notificationDatabase); ok {
		listener, listenErr := source.OpenNotificationListener(ctx, realtimepostgres.NotificationChannel)
		if listenErr != nil {
			logger.WarnContext(ctx, "realtime notification listener unavailable; using durable polling")
		} else {
			wakeupHub, err = realtimepostgres.NewWakeupHub(listener)
			if err != nil {
				listener.Close()
				_ = providers.Shutdown(context.Background())
				connectionPool.Close()

				return nil, fmt.Errorf("construct realtime wakeup hub: %w", err)
			}
		}
	}
	establishedLoop, err := transportrealtime.NewEstablishedLoop(
		identifiers,
		clock.System{},
		limits,
		realtimeCommandService,
		replayStore,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct realtime established loop: %w", err)
	}
	if wakeupHub != nil {
		if err := establishedLoop.UseReplayWakeups(wakeupHub); err != nil {
			wakeupHub.Close()
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("install realtime wakeups: %w", err)
		}
	}
	connectionLifecycle := transportrealtime.NewConnectionLifecycle()
	helloHandler, err := transportrealtime.NewHelloHandler(
		realtimeStore,
		identifiers,
		clock.System{},
		limits,
		establishedLoop,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct realtime hello handler: %w", err)
	}
	realtimeRoutes, err := transportrealtime.NewRoutes(
		ticketService,
		helloHandler,
		configuration.Region,
		configuration.HTTPCORSAllowedOrigins,
		limits,
		connectionLifecycle,
		logger,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct realtime socket routes: %w", err)
	}

	identityStore, err := identitypostgres.New(connectionPool, infrastructure.keys, infrastructure.keys, identifiers, stopStore)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	identityService, err := identity.NewService(identityStore, time.Now, configuration.Region)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	identityRoutes, err := httpapi.NewIdentityRoutes(accessMiddleware, identityService, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	keyCustodyStore, err := keycustodypostgres.New(connectionPool, infrastructure.keys, infrastructure.keys, identityStore, keyCustodyGenerator(identifiers))
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	keyCustodyService, err := keycustody.NewService(keyCustodyStore, identifiers, time.Now)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	kmsRoutes, err := httpapi.NewKMSRoutes(accessMiddleware, keyCustodyService, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	supportStore, err := supportpostgres.New(connectionPool, identifiers)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	supportService, err := support.NewService(supportStore, access.TenantRegistry(), time.Now)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	supportRoutes, err := httpapi.NewSupportRoutes(accessMiddleware, supportService, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	keyRewrapService, keyDestructionService, keyRecoveryService, err := configuredKeyOperations(
		connectionPool, infrastructure, identifiers, keyCustodyStore,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, err
	}
	keyOperationRoutes, err := keyOperationRoutesOrNil(
		accessMiddleware, keyRewrapService, keyDestructionService, keyRecoveryService, logger,
	)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, err
	}
	fraudStore, err := fraudpostgres.New(connectionPool, infrastructure.keys)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	fraudService, err := fraud.NewService(fraudStore, time.Now)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	fraudRoutes, err := httpapi.NewFraudRoutes(accessMiddleware, fraudService, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	modelStore, err := modelpostgres.NewRegistryStore(connectionPool, time.Now)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	modelService, err := model.NewManagement(modelStore, identifiers, time.Now, configuration.VerificationIdempotencyTTL)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	modelRoutes, err := httpapi.NewModelRoutes(accessMiddleware, modelService, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	registrationStore, err := providerpostgres.NewRegistrationStore(connectionPool, clock.System{})
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct provider registration persistence: %w", err)
	}
	providerHealthPolicy, err := configuration.ProviderHealthPolicy()
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct provider health policy: %w", err)
	}
	providerHealthStore, err := providerpostgres.NewHealthStore(connectionPool, clock.System{}, nil)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct provider health persistence: %w", err)
	}
	providerHealth, err := provider.NewHealthService(providerHealthStore, providerHealthStore, nil, nil, providerHealthPolicy, time.Now)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct provider health service: %w", err)
	}
	providerHealth.WithMetrics(metrics)
	registrationService, err := provider.NewRegistrationManagement(registrationStore, identifiers, time.Now, configuration.VerificationIdempotencyTTL, configuredProviderManifests(configuration))
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct provider registration service: %w", err)
	}
	registrationService.WithHealth(providerHealth)
	providerRegistrationRoutes, err := httpapi.NewProviderRoutes(accessMiddleware, registrationService, cursorCodec, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct provider registration routes: %w", err)
	}
	policyManagementStore, err := policypostgres.NewManagementStore(connectionPool, infrastructure.keys, time.Now)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	policyManagement, err := policy.NewManagement(policyManagementStore, policycel.Compiler{}, identifiers, time.Now, configuration.VerificationIdempotencyTTL)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	policyRoutes, err := httpapi.NewPolicyRoutes(accessMiddleware, policyManagement, cursorCodec, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	policySimulator, err := policy.NewSimulator(policycel.Compiler{})
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	policyScenarioSuite, err := policy.NewScenarioSuite(policySimulator)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	policySimulationRoutes, err := httpapi.NewPolicySimulationRoutes(accessMiddleware, policyManagement, policySimulator, policyScenarioSuite, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	assuranceStore, err := policypostgres.NewAssuranceStore(connectionPool, identifiers)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	assuranceService, err := policy.NewAssuranceManagement(assuranceStore, time.Now)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	assuranceRoutes, err := httpapi.NewAssuranceRoutes(accessMiddleware, assuranceService, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	webhookStore, err := deliverypostgres.NewManagementStore(connectionPool, webhookEnqueuer{connectionPool, configuration}, identifiers, time.Now)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	webhookService, err := delivery.NewManagement(webhookStore, identifiers, infrastructure.keys, time.Now, configuration.VerificationIdempotencyTTL)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	webhookStream, err := delivery.NewStream(webhookStore, infrastructure.keys)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	if source, ok := connectionPool.(notificationDatabase); ok {
		listener, listenErr := source.OpenNotificationListener(ctx, deliverypostgres.WebhookEventChannel)
		if listenErr != nil {
			logger.WarnContext(ctx, "webhook event notification listener unavailable; using durable polling")
		} else {
			webhookWakeupHub, err = deliverypostgres.NewWakeupHub(listener)
			if err != nil {
				listener.Close()
				_ = providers.Shutdown(context.Background())
				connectionPool.Close()
				return nil, fmt.Errorf("construct webhook event wakeup hub: %w", err)
			}
		}
	}
	var webhookWakeups httpapi.WebhookEventWakeups
	if webhookWakeupHub != nil {
		webhookWakeups = webhookWakeupHub
	}
	webhookRoutes, err := httpapi.NewWebhookRoutes(accessMiddleware, webhookService, webhookStream, webhookWakeups, cursorCodec, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	reviewManagement, err := review.NewManagement(reviewStore, time.Now)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	reviewManagementRoutes, err := httpapi.NewReviewManagementRoutes(accessMiddleware, reviewManagement, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	proposalStore, err := proposalpostgres.New(connectionPool, clock.System{})
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct proposal persistence: %w", err)
	}
	proposalRegistry := proposalpostgres.NewRegistryStore(connectionPool)
	proposalModel, proposalBinding, err := configuredProposalModel(ctx, configuration, identifiers, proposalRegistry, proposalStore)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct proposal model: %w", err)
	}
	proposalService, err := proposal.NewService(proposal.ServiceConfig{
		Generator: identifiers,
		Clock:     clock.System{},
		Proposals: proposalStore,
		Commands:  proposalStore,
		Modes:     proposalpostgres.NewModeStore(connectionPool),
		Registry:  proposalRegistry,
		Usage:     proposalStore,
		Model:     proposalModel,
		Binding:   proposalBinding,
		Evidence:  proposalpostgres.NewEvidenceChecker(connectionPool),
		Authority: proposalpostgres.NewAuthorityChecker(connectionPool, clock.System{}),
		Region:    proposalpostgres.NewRegionValidator(connectionPool, clock.System{}),
		Limiter:   proposal.NewInMemoryLimiter(100),
	})
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct proposal service: %w", err)
	}
	proposalRoutes, err := httpapi.NewProposalRoutes(accessMiddleware, proposalService, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct proposal routes: %w", err)
	}

	reviewPolicyStore, err := policypostgres.New(connectionPool, infrastructure.keys)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	reviewEvaluator, err := policycel.NewResolver(reviewPolicyStore, 256)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	followupStore, err := reviewpostgres.NewFollowupStore(connectionPool, infrastructure.keys, reviewAuthority, reviewEvaluator, identifiers, clock.System{})
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	followupService, err := review.NewFollowupService(followupStore, time.Now)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}
	followupRoutes, err := httpapi.NewReviewFollowupRoutes(accessMiddleware, followupService, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}

	packSeeds, err := pack.Seeds()
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("load embedded packs: %w", err)
	}
	// The production pool persists lifecycle transitions. Alternative test
	// databases without the owned PostgreSQL pool keep embedded lifecycle only.
	var packStore pack.Store
	if storePool, ok := connectionPool.(packpostgres.StorePool); ok {
		persisted, storeErr := packpostgres.New(storePool)
		if storeErr != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, storeErr
		}
		packStore = persisted
	}
	packRegistry, err := pack.NewRegistry(packSeeds, packStore, clock.System{}.Now)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("construct pack registry: %w", err)
	}
	if err := packRegistry.Load(ctx); err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, fmt.Errorf("load pack lifecycle: %w", err)
	}
	packRoutes, err := httpapi.NewPackRoutes(accessMiddleware, packRegistry, logger)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()
		return nil, err
	}

	routes := []RouteRegistrar{
		assuranceRoutes, identityRoutes, fraudRoutes, followupRoutes, reviewManagementRoutes, proposalRoutes, tenantRoutes, modelRoutes, policyRoutes, policySimulationRoutes, webhookRoutes, decisionRoutes, reviewRoutes, profileRoutes, verificationRoutes, captureOutcomeRoutes, cancellationRoutes, authorityRoutes, connectionRoutes, realtimeRoutes, providerRegistrationRoutes, packRoutes, kmsRoutes, supportRoutes,
	}
	internalRoutes := []InternalRouteRegistrar{}
	if keyOperationRoutes != nil {
		routes = append(routes, keyOperationRoutes)
	}
	if nativeBootstrapRoutes != nil {
		routes = append(routes, nativeBootstrapRoutes)
	}
	if experienceRoutes != nil {
		routes = append(routes, experienceRoutes)
	}
	var uploadPolicy *evidence.UploadPolicy
	if infrastructure.enabled() {
		policy, err := configuration.EvidenceUploadPolicy()
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, err
		}
		purpose, err := kms.NewPurpose(evidence.ContentEncryptionPurpose)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct evidence encryption purpose: %w", err)
		}
		streaming, err := tinkcrypto.NewStreaming(infrastructure.keys, infrastructure.keys, purpose)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct evidence streaming encryption: %w", err)
		}
		reviewObjects, ok := infrastructure.objects.(evidence.ObjectReader)
		if !ok {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("review evidence requires controlled object reads")
		}
		reviewEvidenceStore, err := reviewpostgres.NewEvidenceStore(connectionPool, infrastructure.keys, reviewAuthority, catalog, identifiers, clock.System{}, streaming, reviewObjects)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, err
		}
		reviewEvidenceService, err := review.NewEvidenceService(reviewEvidenceStore, time.Now)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, err
		}
		reviewEvidenceRoutes, err := httpapi.NewReviewEvidenceRoutes(accessMiddleware, reviewEvidenceService, logger)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, err
		}
		routes = append(routes, reviewEvidenceRoutes)

		evidenceStore, err := evidencepostgres.New(connectionPool, infrastructure.keys, catalog)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct evidence persistence: %w", err)
		}
		evidenceStore.WithMetrics(metrics)
		evidenceAdministration, err := evidence.NewAdministration(
			evidenceStore,
			evidenceStore,
			evidenceStore,
			evidenceStore,
			clock.System{},
		)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("construct evidence administration: %w", err)
		}
		evidenceAdministrationRoutes, err := httpapi.NewEvidenceAdministrationRoutes(accessMiddleware, evidenceAdministration, logger)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("construct evidence administration routes: %w", err)
		}
		grantAdministration, err := evidence.NewGrantAdministration(evidenceStore, authorityService, identifiers, catalog, clock.System{}, time.Hour, configuration.VerificationIdempotencyTTL)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("construct evidence grant administration: %w", err)
		}
		evidenceAdministrationRoutes.WithGrantIssuer(grantAdministration)
		privacyStore, err := privacypostgres.New(connectionPool, infrastructure.keys)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("construct privacy persistence: %w", err)
		}
		evidenceEraser, err := privacypostgres.NewEvidenceEraser(connectionPool, infrastructure.objects, infrastructure.keys, time.Now)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("construct evidence eraser: %w", err)
		}
		identityEraser, err := identitypostgres.NewEraser(identityStore, evidenceEraser, time.Now)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, err
		}
		privacyService, err := privacy.NewService(privacyStore, identityEraser, identifiers, time.Now)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("construct privacy service: %w", err)
		}
		privacyService.WithMetrics(metrics)
		privacyDispatcher, err := privacy.NewDispatcher(
			privacySubjectBundle{exporter: tenantSubjectExporter},
			privacySubjectDeletion{store: identityStore, now: time.Now},
			privacyCorrection{identity: identityStore, followup: followupStore, now: time.Now, retention: configuration.VerificationIdempotencyTTL},
		)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("construct privacy dispatcher: %w", err)
		}
		privacyRequestService, err := privacy.NewRequestService(privacyStore, privacyStore, privacyStore, privacyStore, privacyDispatcher, identifiers, time.Now, privacy.SelectedRequestConfig())
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("construct privacy request service: %w", err)
		}
		privacyRequestService.WithMetrics(metrics)
		authorityService.WithRestrictionGate(privacyRequestService)
		privacyRoutes, err := httpapi.NewPrivacyRoutes(accessMiddleware, privacyService, privacyRequestService, captureOutcomeMiddleware, cursorCodec, logger)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, fmt.Errorf("construct privacy routes: %w", err)
		}
		protector, err := evidence.NewProtector(
			streaming,
			infrastructure.objects,
			evidenceStore,
			registry,
			configuration.EvidenceProtectionCleanup,
		)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct evidence protector: %w", err)
		}
		protector = protector.WithCatalog(catalog)
		issuer, err := authority.NewUploadService(
			authorityStore,
			evidenceStore,
			identifiers,
			clock.System{},
			catalog,
			policy,
			configuration.VerificationIdempotencyTTL,
		)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct evidence upload issuance: %w", err)
		}
		preflight, err := evidence.NewUploadPreflight(evidenceStore, clock.System{})
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct evidence upload preflight: %w", err)
		}
		progressReader, err := evidence.NewProgressReader(evidenceStore)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct capture progress reader: %w", err)
		}
		acceptance, err := authority.NewUploadAcceptanceService(
			authorityStore,
			preflight,
			protector,
			evidenceStore,
			evidenceStore,
			evidenceStore,
			identifiers,
			clock.System{},
		)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct evidence upload acceptance: %w", err)
		}
		uploadRoutes, err := httpapi.NewEvidenceUploadRoutes(
			captureMiddleware,
			issuer,
			preflight,
			acceptance,
			policy,
			logger,
		)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct evidence upload routes: %w", err)
		}
		progressRoutes, err := httpapi.NewCaptureProgressRoutes(captureMiddleware, progressReader, logger)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()

			return nil, fmt.Errorf("construct capture progress routes: %w", err)
		}
		if experienceStore != nil {
			progressRoutes.WithExperiencePins(experienceStore)
		}
		routes = append(routes, uploadRoutes, progressRoutes, privacyRoutes, evidenceAdministrationRoutes)
		uploadPolicy = &policy
	}
	if configuration.ProviderRuntimeFile != "" {
		providerRoutes, err := newProviderEvidenceRoutes(configuration, connectionPool, infrastructure, catalog)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, err
		}
		callbackRoutes, callbackRunner, err := newProviderCallbackRoutes(configuration, connectionPool, identifiers, logger)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, err
		}
		routes = append(routes, callbackRoutes)
		providerRunner = callbackRunner
		internalRoutes = append(internalRoutes, providerRoutes)
	}
	if configuration.ModelRuntimeFile != "" {
		modelRoutes, err := newModelEvidenceRoutes(configuration, connectionPool, infrastructure, catalog)
		if err != nil {
			_ = providers.Shutdown(context.Background())
			connectionPool.Close()
			return nil, err
		}
		internalRoutes = append(internalRoutes, modelRoutes)
	}
	httpServer, err := NewServer(address, state, httpapi.Dependencies{
		Logger:               logger,
		IDs:                  identifiers,
		TracerProvider:       providers.TracerProvider(),
		MeterProvider:        providers.MeterProvider(),
		RequestTimeout:       configuration.HTTPRequestTimeout,
		MaxBodyBytes:         configuration.HTTPMaxBodyBytes,
		AllowedOrigins:       configuration.HTTPCORSAllowedOrigins,
		EvidenceUploadPolicy: uploadPolicy,
	}, routes, internalRoutes)
	if err != nil {
		_ = providers.Shutdown(context.Background())
		connectionPool.Close()

		return nil, fmt.Errorf("construct API HTTP server: %w", err)
	}
	if tlsEnabled {
		httpServer.TLSConfig = &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{certificate},
		}
	}

	process := &Process{
		address:         address,
		shutdownTimeout: configuration.ShutdownTimeout,
		server:          httpServer,
		tlsEnabled:      tlsEnabled,
		listen: func(ctx context.Context, network, address string) (net.Listener, error) {
			var listenConfig net.ListenConfig

			return listenConfig.Listen(ctx, network, address)
		},
		health:           state,
		logger:           logger,
		build:            build,
		telemetry:        providers,
		realtime:         connectionLifecycle,
		wakeups:          wakeupHub,
		webhookWakeups:   webhookWakeupHub,
		providerRunner:   providerRunner,
		evidence:         infrastructure.lifecycle,
		database:         connectionPool,
		databaseInterval: configuration.DatabaseHealthInterval,
		databaseTimeout:  configuration.DatabaseHealthTimeout,
		headgateSchema:   configuration.HeadgateSchema,
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

func configuredPeppers(configuration config.API) (*access.PepperSet, error) {
	configured := configuration.APIKeyPeppers.Values()
	peppers := make(map[access.PepperVersion][]byte, len(configured))
	for version, material := range configured {
		peppers[access.PepperVersion(version)] = material
	}
	set, err := access.NewPepperSet(
		access.PepperVersion(configuration.APIKeyActivePepperVersion),
		peppers,
	)
	if err != nil {
		return nil, fmt.Errorf("configure API key peppers: %w", err)
	}

	return set, nil
}

func configuredCursor(configuration config.API) (*cursor.Codec, error) {
	configured := configuration.CursorKeys.Values()
	keys := make(map[cursor.KeyVersion][]byte, len(configured))
	for version, material := range configured {
		keys[cursor.KeyVersion(version)] = material
	}
	keyring, err := cursor.NewKeyring(cursor.KeyVersion(configuration.CursorActiveKeyVersion), keys)
	if err != nil {
		return nil, fmt.Errorf("configure cursor keys: %w", err)
	}
	codec, err := cursor.New(keyring, clock.System{}, configuration.CursorTTL)
	if err != nil {
		return nil, fmt.Errorf("configure cursor codec: %w", err)
	}

	return codec, nil
}

func configuredCaptureSigner(configuration config.API) (*access.CaptureTokenSigner, error) {
	configured := configuration.CaptureTokenKeys.Values()
	keys := make(map[access.CaptureTokenKeyVersion][]byte, len(configured))
	for version, material := range configured {
		keys[access.CaptureTokenKeyVersion(version)] = material
	}
	keyring, err := access.NewCaptureTokenKeyring(
		access.CaptureTokenKeyVersion(configuration.CaptureTokenActiveVersion),
		keys,
	)
	if err != nil {
		return nil, fmt.Errorf("configure capture-token keys: %w", err)
	}
	signer, err := access.NewCaptureTokenSigner(keyring, clock.System{})
	if err != nil {
		return nil, fmt.Errorf("configure capture-token signer: %w", err)
	}

	return signer, nil
}

func configuredOutcomeSigner(configuration config.API) (*access.OutcomeTokenSigner, error) {
	configured := configuration.OutcomeTokenKeys.Values()
	keys := make(map[access.OutcomeTokenKeyVersion][]byte, len(configured))
	for version, material := range configured {
		keys[access.OutcomeTokenKeyVersion(version)] = material
	}
	keyring, err := access.NewOutcomeTokenKeyring(
		access.OutcomeTokenKeyVersion(configuration.OutcomeTokenActiveVersion),
		keys,
	)
	if err != nil {
		return nil, fmt.Errorf("configure outcome-token keys: %w", err)
	}
	signer, err := access.NewOutcomeTokenSigner(keyring, clock.System{})
	if err != nil {
		return nil, fmt.Errorf("configure outcome-token signer: %w", err)
	}

	return signer, nil
}

// Run serves until startup fails, the server exits, or the context is
// cancelled. Cancellation starts a bounded graceful drain.
func (process *Process) Run(ctx context.Context) (runErr error) {
	defer func() {
		if process.evidence == nil {
			return
		}
		shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), process.shutdownTimeout)
		defer cancel()
		runErr = errors.Join(runErr, wrapError("shut down API evidence infrastructure", process.evidence.Shutdown(shutdownContext)))
	}()
	defer func() {
		if process.telemetry == nil {
			return
		}
		shutdownContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), process.shutdownTimeout)
		defer cancel()
		runErr = errors.Join(runErr, wrapError("shut down API telemetry", process.telemetry.Shutdown(shutdownContext)))
	}()
	defer func() {
		if process.database != nil {
			if process.wakeups != nil {
				process.wakeups.Close()
			}
			if process.webhookWakeups != nil {
				process.webhookWakeups.Close()
			}
			if process.providerRunner != nil {
				process.providerRunner.Close()
			}
			process.database.Close()
		}
	}()

	if err := ctx.Err(); err != nil {
		return fmt.Errorf("start API: %w", err)
	}

	listener, err := process.listen(ctx, "tcp", process.address)
	if err != nil {
		return fmt.Errorf("listen for API traffic: %w", err)
	}

	process.health.MarkStarted()
	process.health.MarkReady()
	monitorContext, stopMonitor := context.WithCancel(ctx)
	monitorDone := make(chan struct{})
	go func() {
		defer close(monitorDone)
		process.monitorDatabase(monitorContext)
	}()
	stopDatabaseMonitor := func() {
		stopMonitor()
		<-monitorDone
	}

	serveErrors := make(chan error, 1)
	go func() {
		if process.tlsEnabled {
			serveErrors <- process.server.ServeTLS(listener, "", "")

			return
		}
		serveErrors <- process.server.Serve(listener)
	}()

	process.logger.InfoContext(
		ctx,
		"api started",
		"address", process.address,
		"tls", process.tlsEnabled,
		"version", process.build.Version,
		"commit", process.build.Commit,
		"build_date", process.build.Date,
	)

	select {
	case serveErr := <-serveErrors:
		stopDatabaseMonitor()
		process.health.BeginDrain()
		drainContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), process.shutdownTimeout)
		defer cancel()

		return errors.Join(
			normalizeServeError(serveErr),
			wrapError("drain realtime connections", process.drainRealtime(drainContext)),
		)
	case <-ctx.Done():
		stopDatabaseMonitor()
		return process.shutdown(ctx, serveErrors)
	}
}

func (process *Process) monitorDatabase(ctx context.Context) {
	if process.database == nil || process.databaseInterval <= 0 || process.databaseTimeout <= 0 {
		return
	}
	ticker := time.NewTicker(process.databaseInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			checkContext, cancel := context.WithTimeout(ctx, process.databaseTimeout)
			err := errors.Join(
				process.database.Check(checkContext, migrations.LatestVersion),
				process.database.CheckHeadgate(checkContext, process.headgateSchema),
			)
			cancel()
			wasReady := process.health.Ready()
			if err != nil {
				process.health.MarkNotReady()
				if wasReady {
					process.logger.WarnContext(ctx, "database readiness failed")
				}

				continue
			}
			process.health.MarkReady()
			if !wasReady {
				process.logger.InfoContext(ctx, "database readiness recovered")
			}
		}
	}
}

func (process *Process) shutdown(parent context.Context, serveErrors <-chan error) error {
	process.health.BeginDrain()
	drainContext := context.WithoutCancel(parent)
	process.logger.InfoContext(drainContext, "api draining")

	shutdownContext, cancel := context.WithTimeout(drainContext, process.shutdownTimeout)
	defer cancel()

	realtimeErrors := make(chan error, 1)
	go func() {
		realtimeErrors <- process.drainRealtime(shutdownContext)
	}()
	shutdownErr := process.server.Shutdown(shutdownContext)
	realtimeErr := <-realtimeErrors
	serveErr := <-serveErrors
	serveErr = normalizeServeError(serveErr)

	if shutdownErr != nil || realtimeErr != nil || serveErr != nil {
		return errors.Join(
			wrapError("shut down API", shutdownErr),
			wrapError("drain realtime connections", realtimeErr),
			serveErr,
		)
	}

	process.logger.InfoContext(drainContext, "api stopped")

	return nil
}

func (process *Process) drainRealtime(ctx context.Context) error {
	if process.realtime == nil {
		return nil
	}

	return process.realtime.Drain(ctx)
}

func normalizeServeError(err error) error {
	if err == nil || errors.Is(err, http.ErrServerClosed) {
		return nil
	}

	return fmt.Errorf("serve API traffic: %w", err)
}

func wrapError(operation string, err error) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%s: %w", operation, err)
}
