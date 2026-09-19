package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/config"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
	"github.com/Mujhtech/idenqa/internal/transport/runner"
	verificationtask "github.com/Mujhtech/idenqa/internal/verification/task"
	"google.golang.org/grpc"
)

// newProviderCallbackRoutes composes the outbound runner connection that
// authenticates provider callbacks. The API owns only public ingress; provider
// credentials and callback bodies remain inside the isolated runner.
func newProviderCallbackRoutes(
	configuration config.API,
	pool database,
	identifiers *id.Generator,
	logger *slog.Logger,
) (*httpapi.ProviderCallbackRoutes, closer, error) {
	settings, err := config.LoadProviderRuntime(configuration.ProviderRuntimeFile)
	if err != nil {
		return nil, nil, err
	}
	credential, err := config.ReadCredentialFile(settings.RunnerCredentialFile)
	if err != nil {
		return nil, nil, err
	}
	bearer, err := runner.NewBearerCredential(credential)
	if err != nil {
		return nil, nil, err
	}
	tlsCredentials, err := runner.ClientTLSCredentials(settings.RunnerCAFile, settings.RunnerServerName)
	if err != nil {
		return nil, nil, err
	}
	options, err := runner.DialOptions(runner.ClientConfig{Credential: bearer, TLS: tlsCredentials})
	if err != nil {
		return nil, nil, err
	}
	connection, err := grpc.NewClient(settings.RunnerAddress, options...)
	if err != nil {
		return nil, nil, errors.New("construct provider callback connection")
	}
	fail := func(err error) (*httpapi.ProviderCallbackRoutes, closer, error) {
		_ = connection.Close()
		return nil, nil, err
	}
	client, err := runner.NewProviderClient(runnerv1.NewProviderRunnerServiceClient(connection))
	if err != nil {
		return fail(err)
	}
	requests, err := providerpostgres.NewRequestStore(pool, clock.System{})
	if err != nil {
		return fail(err)
	}
	service, err := provider.NewCallbackService(
		requests,
		client,
		requests,
		providerCallbackWakeup{database: pool, configuration: configuration, identifiers: identifiers},
		time.Now,
	)
	if err != nil {
		return fail(err)
	}
	routes, err := httpapi.NewProviderCallbackRoutes(service, logger)
	if err != nil {
		return fail(err)
	}
	return routes, providerRunnerConnection{connection}, nil
}

// providerRunnerConnection closes the outbound callback verification channel
// on process shutdown.
type providerRunnerConnection struct{ connection *grpc.ClientConn }

func (connection providerRunnerConnection) Close() { _ = connection.connection.Close() }

// providerCallbackWakeup enqueues the existing async execute intent
// idempotently. The deterministic intent key means an already-scheduled
// continuation is never duplicated; Headgate remains the selected queue.
type providerCallbackWakeup struct {
	database      database
	configuration config.API
	identifiers   *id.Generator
}

func (wakeup providerCallbackWakeup) Wake(ctx context.Context, target provider.CallbackTarget) error {
	pool, ok := wakeup.database.(*processDatabase)
	if !ok || wakeup.identifiers == nil || wakeup.configuration.HeadgateInstallationID == "" {
		return errors.New("provider callback wakeup unavailable")
	}
	now := time.Now().UTC()
	if !now.Before(target.Deadline) {
		return nil
	}
	intent, err := verificationtask.NewAsyncExecuteIntent(
		wakeup.identifiers,
		target.Scope,
		verificationtask.ExecutePayload{CheckID: target.CheckID, AttemptID: target.AttemptID},
		verificationtask.IntentMetadata{ScheduledAt: now, Deadline: target.Deadline},
	)
	if err != nil {
		return err
	}
	adapterConfiguration := taskheadgate.DefaultConfig(wakeup.configuration.HeadgateInstallationID)
	adapterConfiguration.Schema = wakeup.configuration.HeadgateSchema
	adapter, err := taskheadgate.NewPostgres(pool.Native(), adapterConfiguration)
	if err != nil {
		return err
	}
	// A duplicate intent key reports success: the existing continuation will
	// observe the durable receipt on its next bounded poll.
	return adapter.Enqueue(ctx, intent)
}

var _ providerv1.CallbackVerifier = (*runner.ProviderClient)(nil)
