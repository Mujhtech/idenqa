package postgres

import (
	"context"

	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ReferencedKeyVersion reports whether persistent identifier tokens still
// reference a key version. An unreadable ledger reports referenced so a retire
// never removes a key that live identifiers may resolve through.
func (s *Store) ReferencedKeyVersion(ctx context.Context, scope tenant.Scope, domain string, version int64) (bool, error) {
	_ = domain
	if s == nil || s.pool == nil || ctx == nil || scope.ID().IsZero() || version < 1 {
		return true, nil
	}
	referenced := true
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}

		return tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.identity_identifier_tokens
			WHERE tenant_id=$1 AND key_version=$2)`, scope.ID().String(), version).Scan(&referenced)
	})
	if err != nil {
		return true, err
	}

	return referenced, nil
}
