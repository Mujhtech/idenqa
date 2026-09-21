package keycustody

import (
	"context"
	"slices"

	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Provider issues and verifies predictable-identifier tokens against the
// retained active key versions. It carries no authorization: application
// services decide which domains a caller may use.
type Provider struct{ repository Repository }

// NewProvider constructs a token provider over the key repository.
func NewProvider(repository Repository) (*Provider, error) {
	if repository == nil {
		return nil, ErrInvalid
	}

	return &Provider{repository: repository}, nil
}

// Issue derives a token under the domain's explicit active version and returns
// the exact version used. Disabling the active version fails issuance closed
// even when older verification-only versions remain.
func (provider *Provider) Issue(ctx context.Context, scope tenant.Scope, domain string, parts ...string) (string, int64, error) {
	metadata, err := provider.repository.Read(ctx, scope, domain)
	if err != nil {
		return "", 0, err
	}
	if metadata.ActiveVersion < 1 {
		return "", 0, ErrNoActiveKey
	}
	active := metadata.ActiveVersion
	material, err := provider.repository.Material(ctx, scope, domain, active)
	if err != nil {
		return "", 0, err
	}
	defer clear(material)
	token, err := Token(material, domain, parts...)
	if err != nil {
		return "", 0, err
	}

	return token, active, nil
}

// Verify accepts a token created under any retained active version and returns
// the exact matching version. Retired and disabled versions never verify.
func (provider *Provider) Verify(ctx context.Context, scope tenant.Scope, domain, token string, parts ...string) (int64, error) {
	if token == "" {
		return 0, ErrInvalid
	}
	versions, err := provider.activeVersions(ctx, scope, domain)
	if err != nil {
		return 0, err
	}
	for index := len(versions) - 1; index >= 0; index-- {
		material, err := provider.repository.Material(ctx, scope, domain, versions[index].Version)
		if err != nil {
			return 0, err
		}
		expected, err := Token(material, domain, parts...)
		clear(material)
		if err != nil {
			return 0, err
		}
		if EqualToken(expected, token) {
			return versions[index].Version, nil
		}
	}

	return 0, ErrNotFound
}

// ActiveVersions returns the retained active versions in ascending order.
func (provider *Provider) ActiveVersions(ctx context.Context, scope tenant.Scope, domain string) ([]Version, error) {
	return provider.activeVersions(ctx, scope, domain)
}

func (provider *Provider) activeVersions(ctx context.Context, scope tenant.Scope, domain string) ([]Version, error) {
	if provider == nil || provider.repository == nil || ctx == nil || scope.ID().IsZero() {
		return nil, ErrInvalid
	}
	if _, err := ParseDomain(domain); err != nil {
		return nil, err
	}
	versions, err := provider.repository.Versions(ctx, scope, domain)
	if err != nil {
		return nil, err
	}
	active := make([]Version, 0, len(versions))
	for _, version := range versions {
		if version.State == StateActive {
			active = append(active, version)
		}
	}
	slices.SortFunc(active, func(first, second Version) int {
		return int(first.Version - second.Version)
	})

	return active, nil
}
