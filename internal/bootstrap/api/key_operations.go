package api

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	keycustodypostgres "github.com/Mujhtech/idenqa/internal/keycustody/postgres"
	"github.com/Mujhtech/idenqa/internal/keyrewrap"
	keyrewrappostgres "github.com/Mujhtech/idenqa/internal/keyrewrap/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
)

// configuredKeyOperations composes the fleet rewrap, verified destruction, and
// recovery ceremony surfaces. It is only available when the process owns a key
// provider; otherwise the routes are not mounted.
func configuredKeyOperations(
	connectionPool database,
	infrastructure EvidenceInfrastructure,
	identifiers *id.Generator,
	custodyStore *keycustodypostgres.Store,
) (*keyrewrap.Service, *keycustody.DestructionService, *keycustody.RecoveryService, error) {
	if !infrastructure.enabled() {
		return nil, nil, nil, nil
	}
	repository, err := keyrewrappostgres.New(connectionPool, infrastructure.keys, infrastructure.keys, time.Now)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("construct key rewrap repository: %w", err)
	}
	catalog, err := evidence.BuiltInCatalog()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load evidence registry: %w", err)
	}
	evidenceStore, err := evidencepostgres.New(connectionPool, infrastructure.keys, catalog)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("construct evidence key persistence: %w", err)
	}
	evidenceRewrapper, err := evidence.NewRewrapper(evidenceStore, evidenceStore, infrastructure.keys, infrastructure.keys)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("construct evidence key rewrapper: %w", err)
	}
	workerIdentifier, err := identifiers.NewTask()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("construct key rewrap worker identity: %w", err)
	}
	adapters, err := keyRewrapAdapters(repository, evidenceRewrapper, workerIdentifier.String())
	if err != nil {
		return nil, nil, nil, err
	}
	rewrap, err := keyrewrap.NewService(repository, repository, adapters, infrastructure.keys, nil, time.Now)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("construct key rewrap service: %w", err)
	}
	var scheduler keycustody.DestructionScheduler
	if provider, ok := infrastructure.keys.(keycustody.DestructionScheduler); ok {
		scheduler = provider
	}
	destruction, err := keycustody.NewDestructionService(custodyStore, custodyStore, scheduler, identifiers, time.Now)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("construct destruction service: %w", err)
	}
	recovery, err := keycustody.NewRecoveryService(custodyStore, rewrap, rewrap, identifiers, time.Now)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("construct recovery service: %w", err)
	}

	return rewrap, destruction, recovery, nil
}

func keyRewrapAdapters(repository *keyrewrappostgres.Store, evidenceRewrapper *evidence.Rewrapper, workerID string) ([]keyrewrap.Adapter, error) {
	evidenceAdapter, err := keyrewrappostgres.NewEvidenceAdapter(repository, evidenceRewrapper, workerID, time.Now)
	if err != nil {
		return nil, err
	}
	webhookEventAdapter, err := keyrewrappostgres.NewWebhookEventAdapter(repository)
	if err != nil {
		return nil, err
	}
	webhookDeliveryAdapter, err := keyrewrappostgres.NewWebhookDeliveryAdapter(repository)
	if err != nil {
		return nil, err
	}
	webhookSecretAdapter, err := keyrewrappostgres.NewWebhookSecretAdapter(repository)
	if err != nil {
		return nil, err
	}
	hmacAdapter, err := keyrewrappostgres.NewHMACKeyAdapter(repository)
	if err != nil {
		return nil, err
	}
	identityLookupAdapter, err := keyrewrappostgres.NewIdentityLookupAdapter(repository)
	if err != nil {
		return nil, err
	}
	identitySubjectAdapter, err := keyrewrappostgres.NewIdentitySubjectAdapter(repository)
	if err != nil {
		return nil, err
	}
	fraudAdapter, err := keyrewrappostgres.NewFraudKeyAdapter(repository)
	if err != nil {
		return nil, err
	}

	return []keyrewrap.Adapter{
		evidenceAdapter, webhookEventAdapter, webhookDeliveryAdapter, webhookSecretAdapter,
		hmacAdapter, identityLookupAdapter, identitySubjectAdapter, fraudAdapter,
	}, nil
}

// keyOperationRoutesOrNil registers the key-operation surface when it is
// composed and keeps the route slice free of nil registrars.
func keyOperationRoutesOrNil(
	accessMiddleware *httpapi.AccessMiddleware,
	rewrap *keyrewrap.Service,
	destruction *keycustody.DestructionService,
	recovery *keycustody.RecoveryService,
	logger *slog.Logger,
) (RouteRegistrar, error) {
	if rewrap == nil || destruction == nil || recovery == nil {
		return nil, nil
	}
	routes, err := httpapi.NewKeyOperationsRoutes(accessMiddleware, rewrap, destruction, recovery, logger)
	if err != nil {
		return nil, err
	}

	return routes, nil
}
