package worker

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/evidence"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"github.com/Mujhtech/idenqa/internal/model"
	modelpostgres "github.com/Mujhtech/idenqa/internal/model/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/runner"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"google.golang.org/grpc"
)

type modelRuntime struct {
	plan        verification.CheckPlanner
	requests    *modelpostgres.RequestStore
	preparation verificationpostgres.PlannedCheckPreparation
	executor    modelv1.Executor
	connection  interface{ Close() error }
	signals     []string
}

func configuredModel(ctx context.Context, configuration config.Worker, pool *pg.Pool, ids *id.Generator) (*modelRuntime, error) {
	if configuration.ModelRuntimeFile == "" {
		return nil, nil
	}
	if configuration.SyntheticProcessing {
		return nil, errors.New("model and synthetic processing cannot be enabled together")
	}
	settings, err := config.LoadModelRuntimes(configuration.ModelRuntimeFile)
	if err != nil {
		return nil, err
	}
	var items []*modelRuntime
	accepted := false
	defer func() {
		if !accepted {
			for _, item := range items {
				_ = item.connection.Close()
			}
		}
	}()
	routes := []executionRoute{}
	executors := modelExecutors{}
	connections := runtimeConnections{}
	for _, setting := range settings {
		item, err := configuredModelSettings(ctx, setting, pool, ids)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		routes = append(routes, executionRoute{item.plan, item.preparation, item.signals})
		executors[setting.Binding.Configuration] = item.executor
		connections = append(connections, item.connection)
	}
	combined, err := composeRoutes(routes)
	if err != nil {
		return nil, err
	}
	accepted = true
	return &modelRuntime{plan: combined, preparation: combined, requests: items[0].requests, executor: executors, connection: connections, signals: combined.signals}, nil
}
func configuredModelSettings(ctx context.Context, settings config.ModelRuntime, pool *pg.Pool, ids *id.Generator) (*modelRuntime, error) {

	plan, err := model.NewPlan(settings.Binding, settings.Manifest)
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
		return nil, errors.New("construct model connection")
	}
	accepted := false
	defer func() {
		if !accepted {
			_ = connection.Close()
		}
	}()
	client, err := runner.NewModelClient(runnerv1.NewModelRunnerServiceClient(connection))
	if err != nil {
		return nil, err
	}
	startup, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	manifest, err := client.Manifest(startup)
	if err != nil {
		return nil, fmt.Errorf("read model runner manifest: %w", err)
	}
	if !sameModelManifest(manifest, plan.Manifest) {
		return nil, errors.New("model runner manifest does not match deployed plan")
	}
	if err := client.ValidateConfiguration(startup, settings.Binding.Configuration); err != nil {
		return nil, errors.New("model runner configuration unavailable")
	}
	requests, err := modelpostgres.NewRequestStore(pool, clock.System{})
	if err != nil {
		return nil, err
	}
	supervised, err := model.NewSupervisedExecutor(client, client, time.Now)
	if err != nil {
		return nil, err
	}
	executor, err := model.NewDurableExecutor(requests, supervised, time.Now)
	if err != nil {
		return nil, err
	}
	catalog, err := evidence.BuiltInCatalog()
	if err != nil {
		return nil, err
	}
	preparation := &modelpostgres.Preparation{Plan: plan, Requests: requests, IDs: ids, Catalog: catalog, Clock: clock.System{}}
	accepted = true
	return &modelRuntime{plan: plan, requests: requests, preparation: preparation, executor: executor, connection: connection, signals: plan.Capability.OutputSignals}, nil
}

// Fixture models require explicit opt-in and exact synthetic provenance.
type syntheticModelRequests struct{ enabled bool }

func (loader syntheticModelRequests) Load(_ context.Context, _ tenant.Scope, _ verification.Check, attempt verification.Attempt) (modelv1.Request, error) {
	if !loader.enabled || attempt.Provenance.RunnerID != "synthetic.liveness" {
		return modelv1.Request{}, model.ErrRequestUnavailable
	}
	return modelv1.Request{Contract: modelv1.CurrentVersion, AttemptID: attempt.ID.String()}, nil
}

func sameModelManifest(a, b modelv1.Manifest) bool {
	if a.Provenance != b.Provenance || a.Restrictions != b.Restrictions || len(a.Capabilities) != len(b.Capabilities) {
		return false
	}
	for i, x := range a.Capabilities {
		y := b.Capabilities[i]
		if x.Evaluation != y.Evaluation || !slices.Equal(x.AcceptedEvidence, y.AcceptedEvidence) || !slices.Equal(x.RequiredAssurances, y.RequiredAssurances) || !slices.Equal(x.OutputSignals, y.OutputSignals) {
			return false
		}
	}
	return true
}
