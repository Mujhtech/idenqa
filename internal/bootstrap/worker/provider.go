package worker

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	"github.com/Mujhtech/idenqa/adapters/providers/smileid"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/evidence"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/runner"
	"github.com/Mujhtech/idenqa/internal/verification"
	"google.golang.org/grpc"
)

type providerRuntime struct {
	plan          *provider.Plan
	requests      *providerpostgres.RequestStore
	preparation   *providerpostgres.Preparation
	executor      providerv1.Executor
	breaker       *provider.BreakerExecutor
	remote        providerv1.Executor
	limited       *provider.LimitedExecutor
	registrations *providerpostgres.RegistrationStore
	health        *provider.HealthService
	healthStore   *providerpostgres.HealthStore
	breakers      *provider.BreakerRegistry
	connection    *grpc.ClientConn
}

// withMetrics attaches bounded provider dispatch, throttle and health metrics
// to the durable store, the health supervisor, the fail-fast wrapper, the
// admission limiter and whichever inner executor concrete type is configured.
func (runtime *providerRuntime) withMetrics(metrics provider.Metrics) {
	if runtime == nil {
		return
	}
	runtime.requests.WithMetrics(metrics)
	runtime.health.WithMetrics(metrics)
	runtime.breaker.WithMetrics(metrics)
	runtime.limited.WithMetrics(metrics)
	switch executor := runtime.remote.(type) {
	case *provider.DurableExecutor:
		executor.WithMetrics(metrics)
	case *provider.AsyncExecutor:
		executor.WithMetrics(metrics)
	}
}

func configuredProvider(ctx context.Context, configuration config.Worker, pool *pg.Pool, ids *id.Generator, wrapper platformcrypto.KeyWrapper) (*providerRuntime, error) {
	if configuration.ProviderRuntimeFile == "" {
		return nil, nil
	}
	if configuration.SyntheticProcessing {
		return nil, errors.New("provider and synthetic processing cannot be enabled together")
	}
	settings, err := config.LoadProviderRuntime(configuration.ProviderRuntimeFile)
	if err != nil {
		return nil, err
	}
	manifestDescription := dojah.Description()
	if settings.Adapter == "smileid" {
		manifestDescription = smileid.Description()
	}
	plan, err := provider.NewPlan(settings.Binding, manifestDescription)
	if err != nil {
		return nil, err
	}
	credential, err := config.ReadCredentialFile(settings.RunnerCredentialFile)
	if err != nil {
		return nil, err
	}
	bearer, err := runner.NewBearerCredential(credential)
	if err != nil {
		return nil, err
	}
	tlsCredentials, err := runner.ClientTLSCredentials(settings.RunnerCAFile, settings.RunnerServerName)
	if err != nil {
		return nil, err
	}
	options, err := runner.DialOptions(runner.ClientConfig{Credential: bearer, TLS: tlsCredentials})
	if err != nil {
		return nil, err
	}
	connection, err := grpc.NewClient(settings.RunnerAddress, options...)
	if err != nil {
		return nil, errors.New("construct provider connection")
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = connection.Close()
		}
	}()
	client, err := runner.NewProviderClient(runnerv1.NewProviderRunnerServiceClient(connection))
	if err != nil {
		return nil, err
	}
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	manifest, err := client.Manifest(startup)
	if err != nil {
		return nil, fmt.Errorf("read provider runner manifest: %w", err)
	}
	if !sameManifest(manifest, plan.Manifest) {
		return nil, errors.New("provider runner manifest does not match deployed plan")
	}
	if err := client.ValidateConfiguration(startup, settings.Binding.Configuration); err != nil {
		return nil, errors.New("provider runner configuration unavailable")
	}
	requests, err := providerpostgres.NewRequestStore(pool, clock.System{})
	if err != nil {
		return nil, err
	}
	registrations, err := providerpostgres.NewRegistrationStore(pool, clock.System{})
	if err != nil {
		return nil, err
	}
	var remote providerv1.Executor
	if settings.Adapter == "smileid" {
		remote, err = provider.NewAsyncExecutor(requests, client, time.Now)
	} else {
		remote, err = provider.NewDurableExecutor(requests, client, time.Now)
	}
	if err != nil {
		return nil, err
	}
	healthPolicy, err := configuration.ProviderHealthPolicy()
	if err != nil {
		return nil, err
	}
	healthStore, err := providerpostgres.NewHealthStore(pool, clock.System{}, wrapper)
	if err != nil {
		return nil, err
	}
	breakers, err := provider.NewBreakerRegistry(healthPolicy.Breaker, time.Now, 4096)
	if err != nil {
		return nil, err
	}
	health, err := provider.NewHealthService(healthStore, healthStore, breakers, client, healthPolicy, time.Now)
	if err != nil {
		return nil, err
	}
	health.WithRegion(plan.Binding.Region).WithPersistence(true)
	limit, err := configuration.DispatchLimit()
	if err != nil {
		return nil, err
	}
	limitStore, err := providerpostgres.NewLimitStore(pool, clock.System{})
	if err != nil {
		return nil, err
	}
	breaker, err := provider.NewBreakerExecutor(remote, breakers, health, time.Now)
	if err != nil {
		return nil, err
	}
	// Admission is the outermost boundary: a throttled request is retried
	// unchanged and never consumes a circuit breaker probe.
	limited, err := provider.NewLimitedExecutor(breaker, limitStore, limit)
	if err != nil {
		return nil, err
	}
	catalog, err := evidence.BuiltInCatalog()
	if err != nil {
		return nil, err
	}
	preparation := &providerpostgres.Preparation{Plan: plan, Requests: requests, IDs: ids, Catalog: catalog, Clock: clock.System{}, Wrapper: wrapper}
	accepted = true
	return &providerRuntime{plan, requests, preparation, limited, breaker, remote, limited, registrations, health, healthStore, breakers, connection}, nil
}

// Protobuf repeated fields do not distinguish nil and empty slices.
func sameManifest(a, b providerv1.Manifest) bool {
	if a.Package != b.Package || a.Configuration != b.Configuration || a.Restrictions != b.Restrictions || len(a.Capabilities) != len(b.Capabilities) {
		return false
	}
	for i := range a.Capabilities {
		x, y := a.Capabilities[i], b.Capabilities[i]
		if x.Check != y.Check || !slices.Equal(x.AcceptedEvidence, y.AcceptedEvidence) || !slices.Equal(x.AcceptedInputs, y.AcceptedInputs) || !slices.Equal(x.AcceptedAssurances, y.AcceptedAssurances) || !slices.Equal(x.ProcessingRegions, y.ProcessingRegions) {
			return false
		}
		x.AcceptedEvidence = nil
		y.AcceptedEvidence = nil
		x.AcceptedInputs = nil
		y.AcceptedInputs = nil
		x.AcceptedAssurances = nil
		y.AcceptedAssurances = nil
		x.ProcessingRegions = nil
		y.ProcessingRegions = nil
		if !reflect.DeepEqual(x, y) {
			return false
		}
	}
	return true
}

type syntheticRequests struct{ enabled bool }

func (loader syntheticRequests) Load(_ context.Context, _ tenant.Scope, _ verification.Check, attempt verification.Attempt) (providerv1.Request, error) {
	if !loader.enabled || (attempt.Provenance.RunnerID != "synthetic.document" && attempt.Provenance.RunnerID != "synthetic.runner") {
		return providerv1.Request{}, provider.ErrRequestUnavailable
	}
	return providerv1.Request{Contract: providerv1.CurrentVersion, AttemptID: attempt.ID.String()}, nil
}
