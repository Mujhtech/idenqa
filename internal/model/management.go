package model

import (
	"github.com/Mujhtech/idenqa/internal/platform/observability"

	"context"
	"encoding/json"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// RegistryRepository owns atomic registry state, immutable revisions and receipts.
type RegistryRepository interface {
	Apply(context.Context, tenant.Scope, idempotency.Request, id.Event, RegistryCommand) (RegistryReceipt, error)
	Get(context.Context, tenant.Scope, string) (RegistryState, error)
	Revision(context.Context, tenant.Scope, string, string, int64) (RegistryRevision, error)
	History(context.Context, tenant.Scope, string, int64, int) ([]RegistryReceipt, error)
	RollbackEligible(context.Context, tenant.Scope, string, Deployment) (bool, error)
}

type registryIDs interface{ NewEvent() (id.Event, error) }

// Management authorizes every operation, including idempotent replay.
type Management struct {
	tracer observability.Tracer

	repository RegistryRepository
	ids        registryIDs
	now        func() time.Time
	retention  time.Duration
}

// NewManagement composes tenant model administration without infrastructure types.
func NewManagement(repository RegistryRepository, ids registryIDs, now func() time.Time, retention time.Duration) (*Management, error) {
	if repository == nil || ids == nil || now == nil || retention <= 0 {
		return nil, ErrRegistryInvalid
	}
	return &Management{repository: repository, ids: ids, now: now, retention: retention}, nil
}

// Execute commits one version-checked evaluation deployment or immutable revision.
func (service *Management) Execute(ctx context.Context, actor access.Context, key string, command RegistryCommand) (spanResult0 RegistryReceipt, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "model.Management.Execute")
	defer observability.EndSpan(completeSpan, &spanErr)

	permission := access.PermissionModelsWrite
	if command.Operation == "activate" || command.Operation == "rollback" || command.Operation == "retire" {
		permission = access.PermissionModelsActivate
	}
	if err := actor.Require(permission); err != nil {
		return RegistryReceipt{}, err
	}
	if err := command.Validate(); err != nil {
		return RegistryReceipt{}, err
	}
	raw, err := json.Marshal(command)
	if err != nil {
		return RegistryReceipt{}, err
	}
	request, err := idempotency.NewRequest(actor.TenantScope().ID(), actor.Principal().KeyID(), "models."+command.Operation, key, raw, service.now().UTC().Truncate(time.Microsecond), service.retention)
	if err != nil {
		return RegistryReceipt{}, err
	}
	event, err := service.ids.NewEvent()
	if err != nil {
		return RegistryReceipt{}, err
	}
	return service.repository.Apply(ctx, actor.TenantScope(), request, event, command)
}

// Get reads current deployment metadata for an authorized tenant.
func (service *Management) Get(ctx context.Context, actor access.Context, name string) (spanResult0 RegistryState, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "model.Management.Get")
	defer observability.EndSpan(completeSpan, &spanErr)

	if err := actor.Require(access.PermissionModelsRead); err != nil {
		return RegistryState{}, err
	}
	if !registryName.MatchString(name) {
		return RegistryState{}, ErrRegistryNotFound
	}
	return service.repository.Get(ctx, actor.TenantScope(), name)
}

// Revision reads immutable provenance even after retirement.
func (service *Management) Revision(ctx context.Context, actor access.Context, name, kind string, revision int64) (spanResult0 RegistryRevision, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "model.Management.Revision")
	defer observability.EndSpan(completeSpan, &spanErr)

	if err := actor.Require(access.PermissionModelsRead); err != nil {
		return RegistryRevision{}, err
	}
	if !registryName.MatchString(name) || (kind != "model" && kind != "threshold") || revision < 1 {
		return RegistryRevision{}, ErrRegistryNotFound
	}
	return service.repository.Revision(ctx, actor.TenantScope(), name, kind, revision)
}

// History exposes bounded immutable command history with a descending version cursor.
func (service *Management) History(ctx context.Context, actor access.Context, name string, before int64, limit int) (spanResult0 []RegistryReceipt, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, service.operationTracer(), "model.Management.History")
	defer observability.EndSpan(completeSpan, &spanErr)

	if err := actor.Require(access.PermissionModelsRead); err != nil {
		return nil, err
	}
	if !registryName.MatchString(name) || before < 0 || limit < 1 || limit > 100 {
		return nil, ErrRegistryInvalid
	}
	return service.repository.History(ctx, actor.TenantScope(), name, before, limit)
}

// WithTracer injects operation tracing during composition, before concurrent use.
func (service *Management) WithTracer(tracer observability.Tracer) *Management {
	if service != nil {
		service.tracer = tracer
	}
	return service
}

func (service *Management) operationTracer() observability.Tracer {
	if service == nil {
		return nil
	}
	return service.tracer
}
