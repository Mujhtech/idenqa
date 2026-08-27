package access

import (
	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// TenantReadRepository is the narrow tenant capability consumed by the
// authenticated tenant-metadata use case.
type TenantReadRepository interface {
	Find(context.Context, tenant.Scope, id.Tenant) (tenant.Tenant, error)
}

// TenantReader authorises and reads the authenticated tenant's safe metadata.
type TenantReader struct{ repository TenantReadRepository }

// NewTenantReader constructs the authenticated tenant-metadata use case.
func NewTenantReader(repository TenantReadRepository) (*TenantReader, error) {
	if repository == nil {
		return nil, errors.New("tenant reader repository is required")
	}

	return &TenantReader{repository: repository}, nil
}

// Current returns only the tenant identified by verified authority. The
// permission and tenant boundary are enforced here independently of HTTP.
func (reader *TenantReader) Current(ctx context.Context, authority Context) (tenant.Tenant, error) {
	if reader == nil || reader.repository == nil {
		return tenant.Tenant{}, errors.New("tenant reader is not initialised")
	}
	if err := authority.Require(PermissionTenantRead); err != nil {
		return tenant.Tenant{}, err
	}
	scope := authority.TenantScope()
	if scope.ID().IsZero() {
		return tenant.Tenant{}, ErrInsufficientScope
	}

	value, err := reader.repository.Find(ctx, scope, scope.ID())
	if err != nil {
		return tenant.Tenant{}, fmt.Errorf("read authenticated tenant: %w", err)
	}

	return value, nil
}
