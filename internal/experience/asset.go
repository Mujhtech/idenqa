package experience

import (
	"context"
	"io"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// OpenAsset authorises an immutable revision's named asset for tenant preview
// or export. Callers cannot supply an object reference or cross revision bounds.
func (service *Service) OpenAsset(ctx context.Context, authority access.Context, identifier id.Experience, version uint32, key string) (contract.Asset, io.ReadCloser, error) {
	if err := authority.Require(access.PermissionExperiencesRead); err != nil {
		return contract.Asset{}, nil, err
	}
	if version == 0 || key == "" {
		return contract.Asset{}, nil, ErrInvalid
	}
	revision, err := service.repository.Revision(ctx, authority.TenantScope(), identifier, version)
	if err != nil {
		return contract.Asset{}, nil, err
	}
	return service.openDocumentAsset(ctx, authority.TenantScope(), revision.Manifest.Document, key)
}

// OpenPinnedAsset serves only an asset in the session's already stored pin.
// An image request never creates a pin, and revocation observes the same safe
// fallback as capture resolution. The caller supplies a validated capture scope.
func (service *Service) OpenPinnedAsset(ctx context.Context, scope tenant.Scope, session id.Verification, key string) (contract.Asset, io.ReadCloser, error) {
	if scope.ID().IsZero() || session.IsZero() || key == "" {
		return contract.Asset{}, nil, ErrInvalid
	}
	pin, err := service.pins.FindPin(ctx, scope, session)
	if err != nil {
		return contract.Asset{}, nil, err
	}
	resolution, err := service.resumePinned(ctx, scope, pin, service.clock.Now().UTC())
	if err != nil {
		return contract.Asset{}, nil, err
	}
	return service.openDocumentAsset(ctx, scope, resolution.Manifest.Document, key)
}

func (service *Service) openDocumentAsset(ctx context.Context, scope tenant.Scope, document contract.Document, key string) (contract.Asset, io.ReadCloser, error) {
	for _, asset := range document.Assets {
		if asset.Key != key {
			continue
		}
		reader, ok := service.assets.(AssetReader)
		if !ok {
			return contract.Asset{}, nil, ErrAssetUnavailable
		}
		body, err := reader.Open(ctx, scope, asset)
		if err != nil {
			return contract.Asset{}, nil, ErrAssetUnavailable
		}
		return asset, body, nil
	}
	return contract.Asset{}, nil, ErrNotFound
}
