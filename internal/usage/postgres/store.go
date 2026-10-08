// Package postgres owns the regional usage-receipt outbox persistence.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	usagev1 "github.com/Mujhtech/idenqa/contracts/usage/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
)

type runner interface {
	WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
}

// Store persists the regional receipt delivery acknowledgement.
type Store struct{ pool runner }

// New constructs a tenant-scoped receipt store.
func New(pool runner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("usage receipt database required")
	}
	return &Store{pool}, nil
}

// Read returns a bounded page of pending receipts for one tenant.
func (s *Store) Read(ctx context.Context, tenantID string, limit int) ([]usagev1.Receipt, error) {
	if _, err := id.ParseTenant(tenantID); err != nil {
		return nil, errors.New("invalid receipt tenant")
	}
	if tenantID == "" || limit < 1 || limit > 1000 {
		return nil, errors.New("invalid receipt scope or limit")
	}
	receipts := make([]usagev1.Receipt, 0)
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, tenantID); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT receipt FROM idenqa.usage_receipts WHERE tenant_id=$1 AND delivered_at IS NULL ORDER BY created_at,id LIMIT $2`, tenantID, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var data []byte
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var receipt usagev1.Receipt
			if json.Unmarshal(data, &receipt) != nil {
				return errors.New("invalid stored receipt")
			}
			receipts = append(receipts, receipt)
		}
		return rows.Err()
	})
	return receipts, err
}

// Acknowledge marks an exact receipt digest delivered without changing its meaning.
func (s *Store) Acknowledge(ctx context.Context, tenantID, receiptID, expectedDigest string, now time.Time) error {
	if _, err := id.ParseTenant(tenantID); err != nil {
		return errors.New("invalid receipt tenant")
	}
	if _, err := hex.DecodeString(receiptID); err != nil {
		return errors.New("invalid receipt identity")
	}
	if _, err := hex.DecodeString(expectedDigest); err != nil {
		return errors.New("invalid receipt digest")
	}
	if tenantID == "" || len(receiptID) != 64 || len(expectedDigest) != 64 || now.IsZero() {
		return errors.New("invalid receipt acknowledgement")
	}
	return s.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, tenantID); err != nil {
			return err
		}
		var data []byte
		if err := tx.QueryRow(ctx, `SELECT receipt FROM idenqa.usage_receipts WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, tenantID, receiptID).Scan(&data); err != nil {
			return err
		}
		var receipt usagev1.Receipt
		if json.Unmarshal(data, &receipt) != nil {
			return errors.New("invalid stored receipt")
		}
		canonical, err := json.Marshal(receipt)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(canonical)
		if hex.EncodeToString(digest[:]) != expectedDigest {
			return errors.New("receipt digest conflict")
		}
		_, err = tx.Exec(ctx, `UPDATE idenqa.usage_receipts SET delivered_at=COALESCE(delivered_at,$3) WHERE tenant_id=$1 AND id=$2`, tenantID, receiptID, now.UTC())
		return err
	})
}
