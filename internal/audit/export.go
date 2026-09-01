package audit

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ExportPermission is the exact application action required for audit export.
const ExportPermission = "audit:export"

// ExportAuthorizer rechecks authority at the application boundary.
type ExportAuthorizer interface {
	Authorize(context.Context, tenant.Scope, string, string) error
}

// ExportRepository reads one exact tenant chain and its public key history.
type ExportRepository interface {
	Export(context.Context, tenant.Scope) (Export, map[string]ed25519.PublicKey, error)
}

// Exporter is the authorised online boundary for portable audit exports.
type Exporter struct {
	authorizer ExportAuthorizer
	repository ExportRepository
}

// NewExporter constructs the authorised export boundary.
func NewExporter(authorizer ExportAuthorizer, repository ExportRepository) (*Exporter, error) {
	if authorizer == nil || repository == nil {
		return nil, ErrInvalid
	}
	return &Exporter{authorizer: authorizer, repository: repository}, nil
}

// Export rechecks exact permission before reading tenant history.
func (exporter *Exporter) Export(ctx context.Context, scope tenant.Scope, actorID string) (Export, map[string]ed25519.PublicKey, error) {
	if exporter == nil || actorID == "" || scope.ID().IsZero() {
		return Export{}, nil, ErrInvalid
	}
	if err := exporter.authorizer.Authorize(ctx, scope, actorID, ExportPermission); err != nil {
		return Export{}, nil, fmt.Errorf("authorize audit export: %w", err)
	}
	exported, keys, err := exporter.repository.Export(ctx, scope)
	if err != nil {
		return Export{}, nil, fmt.Errorf("export audit history: %w", err)
	}
	if _, err := Verify(exported, keys); err != nil {
		return Export{}, nil, errors.Join(ErrChain, err)
	}
	return exported, cloneKeys(keys), nil
}

func cloneKeys(keys map[string]ed25519.PublicKey) map[string]ed25519.PublicKey {
	result := make(map[string]ed25519.PublicKey, len(keys))
	for keyID, key := range keys {
		result[keyID] = append(ed25519.PublicKey(nil), key...)
	}
	return result
}
