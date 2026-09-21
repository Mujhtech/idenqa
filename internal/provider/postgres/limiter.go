package postgres

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

const (
	admissionRetention = 24 * time.Hour
	leaseIDBytes       = 16
)

// LimitStore bounds concurrent and periodic provider dispatches for one
// tenant provider configuration boundary. Admission uses a transaction-scoped
// advisory lock so several API or worker processes share one PostgreSQL-backed
// limit without Redis. Released and completed dispatches release their lease
// immediately; a crashed process holds at most one bounded lease TTL.
type LimitStore struct {
	pool   transactionRunner
	source clock.Clock
}

// NewLimitStore constructs the durable provider admission limiter.
func NewLimitStore(pool transactionRunner, source clock.Clock) (*LimitStore, error) {
	if pool == nil || source == nil {
		return nil, provider.ErrLimitInvalid
	}
	return &LimitStore{pool: pool, source: source}, nil
}

// Acquire admits one dispatch or returns provider.ErrThrottled before any
// external call. The returned lease must be released on completion or failure.
func (store *LimitStore) Acquire(ctx context.Context, scope tenant.Scope, key string, limit provider.Limit) (provider.Lease, error) {
	if store == nil || store.pool == nil || ctx == nil || scope.ID().IsZero() || !validLimitKey(key) || limit.Validate() != nil {
		return nil, provider.ErrLimitInvalid
	}
	now := store.source.Now().UTC().Truncate(time.Microsecond)
	leaseID, err := newLeaseID()
	if err != nil {
		return nil, provider.ErrLimitInvalid
	}
	expiresAt := now.Add(limit.LeaseTTL)
	admitted := false
	err = store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "idenqa.provider.limit:"+scope.ID().String()+":"+key); err != nil {
			return fmt.Errorf("lock provider dispatch limit: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM idenqa.provider_dispatch_leases WHERE tenant_id=$1 AND limit_key=$2 AND expires_at <= $3`, scope.ID().String(), key, now); err != nil {
			return fmt.Errorf("expire provider dispatch leases: %w", err)
		}
		var active int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.provider_dispatch_leases WHERE tenant_id=$1 AND limit_key=$2 AND expires_at > $3`, scope.ID().String(), key, now).Scan(&active); err != nil {
			return fmt.Errorf("count provider dispatch leases: %w", err)
		}
		if active >= limit.MaximumConcurrent {
			return provider.ErrThrottled
		}
		capacity := limit.RateLimit + limit.RateBurst
		windowStart := now.Truncate(limit.RatePeriod)
		var count int64
		err := tx.QueryRow(ctx, `INSERT INTO idenqa.provider_dispatch_admissions(tenant_id,limit_key,window_start,admitted) VALUES($1,$2,$3,1) ON CONFLICT (tenant_id,limit_key,window_start) DO UPDATE SET admitted=idenqa.provider_dispatch_admissions.admitted+1 WHERE idenqa.provider_dispatch_admissions.admitted < $4 RETURNING admitted`, scope.ID().String(), key, windowStart, capacity).Scan(&count)
		if errors.Is(err, pgx.ErrNoRows) {
			return provider.ErrThrottled
		}
		if err != nil {
			return fmt.Errorf("admit provider dispatch rate: %w", err)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM idenqa.provider_dispatch_admissions WHERE tenant_id=$1 AND limit_key=$2 AND window_start < $3`, scope.ID().String(), key, now.Add(-admissionRetention)); err != nil {
			return fmt.Errorf("expire provider dispatch admissions: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.provider_dispatch_leases(tenant_id,limit_key,lease_id,acquired_at,expires_at) VALUES($1,$2,$3,$4,$5)`, scope.ID().String(), key, leaseID, now, expiresAt); err != nil {
			return fmt.Errorf("insert provider dispatch lease: %w", err)
		}
		admitted = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !admitted {
		return nil, provider.ErrThrottled
	}
	return &limitLease{pool: store.pool, tenantID: scope.ID().String(), key: key, leaseID: leaseID}, nil
}

type limitLease struct {
	pool     transactionRunner
	tenantID string
	key      string
	leaseID  string
}

// Release removes the lease. It is idempotent and safe after cancellation.
func (lease *limitLease) Release(ctx context.Context) error {
	if lease == nil || lease.pool == nil || ctx == nil {
		return nil
	}
	return lease.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, lease.tenantID); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM idenqa.provider_dispatch_leases WHERE tenant_id=$1 AND limit_key=$2 AND lease_id=$3`, lease.tenantID, lease.key, lease.leaseID); err != nil {
			return fmt.Errorf("release provider dispatch lease: %w", err)
		}
		return nil
	})
}

func validLimitKey(key string) bool {
	if len(key) != 64 {
		return false
	}
	for index := 0; index < len(key); index++ {
		character := key[index]
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func newLeaseID() (string, error) {
	raw := make([]byte, leaseIDBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

var _ provider.Limiter = (*LimitStore)(nil)
