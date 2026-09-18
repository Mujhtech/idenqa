// Package postgres implements tenant-forced fraud persistence and key custody.
package postgres

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"time"

	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/fraud"
	"github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type transactionRunner interface {
	WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
}

// Store persists tenant-owned fraud material and immutable receipts.
type Store struct {
	pool transactionRunner
	keys crypto.KeyUnwrapper
}

// New composes persistence with provider-neutral key custody.
func New(pool transactionRunner, keys crypto.KeyUnwrapper) (*Store, error) {
	if pool == nil {
		return nil, fraud.ErrInvalid
	}
	return &Store{pool, keys}, nil
}
func setScope(ctx context.Context, tx pg.Transaction, scope tenant.Scope) error {
	if scope.ID().IsZero() {
		return fraud.ErrInvalid
	}
	var v string
	return tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&v)
}
func configuration(ctx context.Context, tx pg.Transaction, scope tenant.Scope, at time.Time) (fraud.Configuration, int64, error) {
	var c fraud.Configuration
	var b []byte
	var v int64
	e := tx.QueryRow(ctx, `SELECT version,configuration FROM idenqa.fraud_configurations WHERE tenant_id=$1 AND recorded_at<=$2 ORDER BY version DESC LIMIT 1`, scope.ID().String(), at).Scan(&v, &b)
	if errors.Is(e, pgx.ErrNoRows) {
		return c, 0, nil
	}
	if e != nil {
		return c, 0, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, 0, e
	}
	return c, v, c.Validate()
}
func (s *Store) key(ctx context.Context, tx pg.Transaction, scope tenant.Scope, region string, create bool) ([]byte, error) {
	if s.keys == nil {
		return nil, fraud.ErrUnavailable
	}
	purpose, _ := kms.NewPurpose("fraud.correlation.v1")
	aad, _ := json.Marshal([]string{"fraud.correlation.v1", scope.ID().String(), region, "1"})
	var raw []byte
	e := tx.QueryRow(ctx, `SELECT wrapped_key FROM idenqa.fraud_keys WHERE tenant_id=$1 AND region=$2`, scope.ID().String(), region).Scan(&raw)
	if errors.Is(e, pgx.ErrNoRows) && create {
		plain := make([]byte, 32)
		if _, e = rand.Read(plain); e != nil {
			return nil, e
		}
		defer clear(plain)
		wrapper, ok := s.keys.(crypto.KeyWrapper)
		if !ok {
			return nil, fraud.ErrUnavailable
		}
		var wrapped kms.WrappedKey
		wrapped, e = wrapper.Wrap(ctx, purpose, plain, aad)
		if e != nil {
			return nil, e
		}
		raw, e = json.Marshal(wrapped.Record())
		if e != nil {
			return nil, e
		}
		_, e = tx.Exec(ctx, `INSERT INTO idenqa.fraud_keys(tenant_id,region,wrapped_key)VALUES($1,$2,$3) ON CONFLICT DO NOTHING`, scope.ID().String(), region, raw)
		if e != nil {
			return nil, e
		}
		e = tx.QueryRow(ctx, `SELECT wrapped_key FROM idenqa.fraud_keys WHERE tenant_id=$1 AND region=$2`, scope.ID().String(), region).Scan(&raw)
	}
	if e != nil {
		return nil, e
	}
	var record kms.WrappedKeyRecord
	if e = json.Unmarshal(raw, &record); e != nil {
		return nil, e
	}
	wrapped, e := kms.NewWrappedKey(record)
	if e != nil {
		return nil, e
	}
	plain, e := s.keys.Unwrap(ctx, purpose, wrapped, aad)
	if e != nil {
		return nil, e
	}
	if len(plain) != 32 {
		clear(plain)
		return nil, fraud.ErrUnavailable
	}
	return plain, nil
}

// Execute commits a validated command, idempotency result and audit atomically.
func (s *Store) Execute(ctx context.Context, scope tenant.Scope, c fraud.Command) (fraud.Result, error) {
	var result fraud.Result
	if c.Retry.TenantID() != scope.ID() || c.Retry.Operation() != "fraud."+c.Operation {
		return result, fraud.ErrInvalid
	}
	e := s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if e := setScope(ctx, tx, scope); e != nil {
			return e
		}
		q := sqlgen.New(tx)
		r, e := idempg.Reserve(ctx, q, c.Retry)
		if e != nil {
			return e
		}
		if replay, ok := r.Result(); ok {
			return json.Unmarshal(replay.Body(), &result)
		}
		switch c.Operation {
		case "configure":
			if c.Configuration == nil || c.Configuration.Validate() != nil {
				return fraud.ErrInvalid
			}
			_, v, e := configuration(ctx, tx, scope, c.At)
			if e != nil {
				return e
			}
			if v != c.ExpectedVersion {
				return fraud.ErrConflict
			}
			if c.Configuration.Enabled {
				key, e := s.key(ctx, tx, scope, c.Configuration.Region, true)
				clear(key)
				if e != nil {
					return e
				}
			}
			for _, source := range c.Configuration.Sources {
				var exists bool
				if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.api_keys WHERE tenant_id=$1 AND id=$2)`, scope.ID().String(), source.KeyID).Scan(&exists); e != nil {
					return e
				}
				if !exists {
					return fraud.ErrInvalid
				}
			}
			b, e := json.Marshal(c.Configuration)
			if e != nil {
				return e
			}
			result = fraud.Result{Version: v + 1, Digest: fraud.Digest(c.Configuration), Configuration: c.Configuration}
			_, e = tx.Exec(ctx, `INSERT INTO idenqa.fraud_configurations(tenant_id,version,configuration,digest,actor_key_id,recorded_at)VALUES($1,$2,$3,$4,$5,$6)`, scope.ID().String(), result.Version, b, result.Digest, c.Retry.Principal().String(), c.At)
			if e != nil {
				return e
			}
		case "ingest":
			var e error
			result, e = s.ingest(ctx, tx, scope, c)
			if e != nil {
				return e
			}
		case "propose":
			if c.Proposal == nil {
				return fraud.ErrInvalid
			}
			var b []byte
			e := tx.QueryRow(ctx, `SELECT receipt FROM idenqa.fraud_receipts WHERE tenant_id=$1 AND digest=$2`, scope.ID().String(), c.Proposal.ReceiptDigest).Scan(&b)
			if errors.Is(e, pgx.ErrNoRows) {
				return fraud.ErrNotFound
			}
			if e != nil {
				return e
			}
			var receipt fraud.Receipt
			if e = json.Unmarshal(b, &receipt); e != nil {
				return e
			}
			for _, signal := range c.Proposal.Signals {
				if !slices.ContainsFunc(receipt.Findings, func(f fraud.Finding) bool {
					return f.Signal == signal && f.State == "not_satisfied" && len(f.Sources) > 0
				}) {
					return fraud.ErrInvalid
				}
			}
			b, e = json.Marshal(c.Proposal)
			if e != nil {
				return e
			}
			result.Digest = fraud.Digest(c.Proposal)
			_, e = tx.Exec(ctx, `INSERT INTO idenqa.fraud_proposals(tenant_id,receipt_digest,digest,proposal,actor_key_id,recorded_at)VALUES($1,$2,$3,$4,$5,$6)ON CONFLICT DO NOTHING`, scope.ID().String(), c.Proposal.ReceiptDigest, result.Digest, b, c.Retry.Principal().String(), c.At)
			if e != nil {
				return e
			}
		default:
			return fraud.ErrInvalid
		}
		eventDigest := fraud.Digest(result)
		_, e = auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{EventID: "fraud." + fraud.Digest([]string{c.Operation, c.Retry.Principal().String(), c.Retry.Key(), c.At.Format(time.RFC3339Nano)}), EventType: "fraud." + c.Operation, AggregateID: "fraud." + eventDigest, ActorID: c.Retry.Principal().String(), EventDigest: eventDigest, OccurredAt: c.At})
		if e != nil {
			return e
		}
		b, e := json.Marshal(result)
		if e != nil {
			return e
		}
		replay, e := idempotency.NewResult(200, b)
		if e != nil {
			return e
		}
		return idempg.Complete(ctx, q, c.Retry, replay, c.At)
	})
	return result, e
}

// Read returns configuration or a redacted receipt in tenant scope.
func (s *Store) Read(ctx context.Context, scope tenant.Scope, kind, reference string) (fraud.Result, error) {
	var result fraud.Result
	e := s.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if e := setScope(ctx, tx, scope); e != nil {
			return e
		}
		var b []byte
		var e error
		switch kind {
		case "configuration", "configuration_revision":
			if kind == "configuration_revision" {
				version, err := strconv.ParseInt(reference, 10, 64)
				if err != nil || version < 1 {
					return fraud.ErrInvalid
				}
				e = tx.QueryRow(ctx, `SELECT version,configuration,digest FROM idenqa.fraud_configurations WHERE tenant_id=$1 AND version=$2`, scope.ID().String(), version).Scan(&result.Version, &b, &result.Digest)
			} else {
				e = tx.QueryRow(ctx, `SELECT version,configuration,digest FROM idenqa.fraud_configurations WHERE tenant_id=$1 ORDER BY version DESC LIMIT 1`, scope.ID().String()).Scan(&result.Version, &b, &result.Digest)
			}
			if e == nil {
				result.Configuration = &fraud.Configuration{}
				e = json.Unmarshal(b, result.Configuration)
			}
		case "receipt":
			e = tx.QueryRow(ctx, `SELECT receipt FROM idenqa.fraud_receipts WHERE tenant_id=$1 AND digest=$2`, scope.ID().String(), reference).Scan(&b)
			if e == nil {
				result.Receipt = &fraud.Receipt{}
				e = json.Unmarshal(b, result.Receipt)
				if e == nil {
					digest := result.Receipt.Digest
					result.Receipt.Digest = ""
					if fraud.Digest(result.Receipt) != digest {
						return fraud.ErrConflict
					}
					result.Receipt.Digest = digest
					for i := range result.Receipt.Findings {
						result.Receipt.Findings[i].Sources = nil
						result.Receipt.Findings[i].Groups = nil
					}
				}
			}
		case "proposal":
			e = tx.QueryRow(ctx, `SELECT proposal FROM idenqa.fraud_proposals WHERE tenant_id=$1 AND digest=$2`, scope.ID().String(), reference).Scan(&b)
			if e == nil {
				result.Proposal = &fraud.Proposal{}
				e = json.Unmarshal(b, result.Proposal)
				result.Digest = reference
			}
		default:
			return fraud.ErrInvalid
		}
		if errors.Is(e, pgx.ErrNoRows) {
			return fraud.ErrNotFound
		}
		return e
	})
	return result, e
}
