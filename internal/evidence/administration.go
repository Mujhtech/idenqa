package evidence

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Administration exposes safe tenant evidence metadata and processing-grant reads.
type Administration struct {
	assets    AssetFinder
	lifecycle LifecycleReader
	grants    GrantFinder
	revoker   GrantRevoker
	clock     clock.Clock
}

// GrantCommandRepository atomically reserves retries, issues a grant, and stores its receipt.
type GrantCommandRepository interface {
	ApplyGrant(context.Context, tenant.Scope, idempotency.Request, func(GrantCommandStore) (Grant, error)) (Grant, error)
}

// GrantCommandStore is the transaction-bound capability used by grant commands.
type GrantCommandStore interface {
	AssetFinder
	GrantCreator
	GrantFinder
	GrantRevoker
}

// GrantAdministration owns idempotent tenant-requested capability issuance.
type GrantAdministration struct {
	repository GrantCommandRepository
	authorizer ReadAuthorizer
	ids        GrantIDGenerator
	catalog    Catalog
	clock      clock.Clock
	maximumTTL time.Duration
	retention  time.Duration
}

// NewGrantAdministration constructs public grant issuance without exposing storage internals.
func NewGrantAdministration(repository GrantCommandRepository, authorizer ReadAuthorizer, ids GrantIDGenerator, catalog Catalog, source clock.Clock, maximumTTL, retention time.Duration) (*GrantAdministration, error) {
	if repository == nil || authorizer == nil || ids == nil || catalog.IsZero() || source == nil || maximumTTL <= 0 || retention <= 0 {
		return nil, errors.New("evidence: grant administration dependencies are invalid")
	}
	return &GrantAdministration{repository: repository, authorizer: authorizer, ids: ids, catalog: catalog, clock: source, maximumTTL: maximumTTL, retention: retention}, nil
}

// Issue atomically creates or exactly replays one purpose-bound processing grant.
func (service *GrantAdministration) Issue(ctx context.Context, auth access.Context, key string, input GrantInput) (Grant, error) {
	if err := auth.Require(access.PermissionEvidenceGrantsWrite); err != nil {
		return Grant{}, err
	}
	actor := auth.Principal().KeyID().String()
	input.Attribution = CommandAttribution{Principal: Actor{Type: "tenant.api_key", ID: actor}, TenantActor: Actor{Type: "tenant.api_key", ID: actor}, Reason: input.Attribution.Reason}
	canonical, err := json.Marshal(struct {
		EvidenceID, CheckReference, RunnerIdentity, WorkloadVersion, Purpose, RecipientReference, OutputDestination, Reason string
		PermittedVariants                                                                                                   []string
		MaximumUses                                                                                                         uint32
		TTL                                                                                                                 int64
	}{input.EvidenceID.String(), input.CheckReference, input.Runner.Identity, input.Runner.WorkloadVersion, string(input.Purpose), input.RecipientReference, input.OutputDestination, input.Attribution.Reason, input.PermittedVariants, input.MaximumUses, int64(input.TTL)})
	if err != nil {
		return Grant{}, err
	}
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	retry, err := idempotency.NewRequest(auth.TenantScope().ID(), auth.Principal().KeyID(), "evidence_grants.issue", key, canonical, now, service.retention)
	if err != nil {
		return Grant{}, err
	}
	return service.repository.ApplyGrant(ctx, auth.TenantScope(), retry, func(store GrantCommandStore) (Grant, error) {
		issuer, err := NewGrantIssuer(store, service.authorizer, store, service.ids, service.catalog, service.clock, service.maximumTTL)
		if err != nil {
			return Grant{}, err
		}
		return issuer.Issue(ctx, auth.TenantScope(), input)
	})
}

// Revoke atomically revokes or exactly replays one processing-grant command.
func (service *GrantAdministration) Revoke(ctx context.Context, auth access.Context, identifier id.Grant, key, reason string) (Grant, error) {
	if err := auth.Require(access.PermissionEvidenceGrantsWrite); err != nil {
		return Grant{}, err
	}
	actor := auth.Principal().KeyID().String()
	attribution := CommandAttribution{Principal: Actor{Type: "tenant.api_key", ID: actor}, TenantActor: Actor{Type: "tenant.api_key", ID: actor}, Reason: reason}
	canonical, err := json.Marshal(struct {
		GrantID string `json:"grant_id"`
		Reason  string `json:"reason"`
	}{identifier.String(), reason})
	if err != nil {
		return Grant{}, err
	}
	now := service.clock.Now().UTC().Truncate(time.Microsecond)
	retry, err := idempotency.NewRequest(auth.TenantScope().ID(), auth.Principal().KeyID(), "evidence_grants.revoke", key, canonical, now, service.retention)
	if err != nil {
		return Grant{}, err
	}
	return service.repository.ApplyGrant(ctx, auth.TenantScope(), retry, func(store GrantCommandStore) (Grant, error) {
		return store.RevokeGrant(ctx, auth.TenantScope(), identifier, attribution, now)
	})
}

// NewAdministration constructs the tenant evidence administration boundary.
func NewAdministration(assets AssetFinder, lifecycle LifecycleReader, grants GrantFinder, revoker GrantRevoker, source clock.Clock) (*Administration, error) {
	if assets == nil || lifecycle == nil || grants == nil || revoker == nil || source == nil {
		return nil, errors.New("evidence: administration dependencies are invalid")
	}
	return &Administration{assets: assets, lifecycle: lifecycle, grants: grants, revoker: revoker, clock: source}, nil
}

// Find returns one tenant-owned asset. Callers must project Record without Content.
func (service *Administration) Find(ctx context.Context, auth access.Context, identifier id.Evidence) (Asset, error) {
	if err := auth.Require(access.PermissionEvidenceRead); err != nil {
		return Asset{}, err
	}
	return service.assets.Find(ctx, auth.TenantScope(), identifier)
}

// History returns bounded append-only evidence lifecycle metadata.
func (service *Administration) History(ctx context.Context, auth access.Context, identifier id.Evidence, limit int) ([]LifecycleEvent, error) {
	if err := auth.Require(access.PermissionEvidenceRead); err != nil {
		return nil, err
	}
	return service.lifecycle.Lifecycle(ctx, auth.TenantScope(), identifier, limit)
}

// FindGrant returns one safe tenant-owned processing-grant record.
func (service *Administration) FindGrant(ctx context.Context, auth access.Context, identifier id.Grant) (Grant, error) {
	if err := auth.Require(access.PermissionEvidenceGrantsRead); err != nil {
		return Grant{}, err
	}
	return service.grants.FindGrant(ctx, auth.TenantScope(), identifier)
}

// RevokeGrant irreversibly revokes one processing grant with API-key attribution.
func (service *Administration) RevokeGrant(ctx context.Context, auth access.Context, identifier id.Grant, reason string) (Grant, error) {
	if err := auth.Require(access.PermissionEvidenceGrantsWrite); err != nil {
		return Grant{}, err
	}
	actor := auth.Principal().KeyID().String()
	return service.revoker.RevokeGrant(ctx, auth.TenantScope(), identifier, CommandAttribution{
		Principal:   Actor{Type: "tenant.api_key", ID: actor},
		TenantActor: Actor{Type: "tenant.api_key", ID: actor},
		Reason:      reason,
	}, service.clock.Now().UTC().Truncate(time.Microsecond))
}
