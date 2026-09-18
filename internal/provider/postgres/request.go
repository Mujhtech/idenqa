// Package postgres persists immutable provider requests and dispatch receipts.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

type transactionRunner interface {
	WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
}

// RequestStore owns tenant-scoped reference envelopes and dispatch history.
type RequestStore struct {
	pool   transactionRunner
	source clock.Clock
}

// NewRequestStore constructs durable provider dispatch persistence.
func NewRequestStore(pool transactionRunner, source clock.Clock) (*RequestStore, error) {
	if pool == nil || source == nil {
		return nil, provider.ErrRequestUnavailable
	}
	return &RequestStore{pool, source}, nil
}

// SaveWithin appends the request with its owning attempt, grant and task transaction.
func (store *RequestStore) SaveWithin(ctx context.Context, tx pg.Transaction, check verification.Check, request providerv1.Request) error {
	digest, err := provider.RequestDigest(request)
	if err != nil {
		return err
	}
	attempts := check.Attempts()
	if len(attempts) != 1 || attempts[0].ID.String() != request.AttemptID || attempts[0].Provenance.RequestDigest != digest || check.TenantID.String() != request.TenantID || check.VerificationID.String() != request.VerificationID {
		return provider.ErrRequestUnavailable
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO idenqa.provider_requests(tenant_id,attempt_id,verification_id,check_id,request_digest,request_body) VALUES($1,$2,$3,$4,$5,$6)`, request.TenantID, request.AttemptID, request.VerificationID, check.ID.String(), digest, body)
	return err
}

// Load checks current processing authority and the immutable attempt binding.
func (store *RequestStore) Load(ctx context.Context, scope tenant.Scope, check verification.Check, attempt verification.Attempt) (providerv1.Request, error) {
	var request providerv1.Request
	if scope.ID() != check.TenantID {
		return request, provider.ErrRequestUnavailable
	}
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationRepeatableRead}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		if err := authoritypostgres.ValidateProcessingWithin(ctx, tx, scope, check.VerificationID, store.source.Now().UTC(), store.source, verification.SessionStateProcessing); err != nil {
			return err
		}
		var body []byte
		var digest string
		err := tx.QueryRow(ctx, `SELECT request_body,request_digest FROM idenqa.provider_requests WHERE tenant_id=$1 AND attempt_id=$2 AND check_id=$3 AND verification_id=$4`, scope.ID().String(), attempt.ID.String(), check.ID.String(), check.VerificationID.String()).Scan(&body, &digest)
		if errors.Is(err, pgx.ErrNoRows) {
			return provider.ErrRequestUnavailable
		}
		if err != nil {
			return err
		}
		if json.Unmarshal(body, &request) != nil {
			return provider.ErrRequestUnavailable
		}
		actual, err := provider.RequestDigest(request)
		if err != nil || actual != digest || digest != attempt.Provenance.RequestDigest || request.AttemptID != attempt.ID.String() || request.TenantID != scope.ID().String() || request.VerificationID != check.VerificationID.String() || !request.Deadline.Equal(attempt.Deadline) {
			return provider.ErrRequestUnavailable
		}
		return nil
	})
	return request, err
}

// Claim permits at most one initial external dispatch for a durable attempt.
func (store *RequestStore) Claim(ctx context.Context, request providerv1.Request) (bool, *providerv1.Result, error) {
	scope, _, err := provider.RequestScope(request)
	if err != nil {
		return false, nil, err
	}
	digest, err := provider.RequestDigest(request)
	if err != nil {
		return false, nil, err
	}
	claimed := false
	var result *providerv1.Result
	err = store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO idenqa.provider_dispatches(tenant_id,attempt_id,request_digest,claimed_at) SELECT tenant_id,attempt_id,request_digest,$4 FROM idenqa.provider_requests WHERE tenant_id=$1 AND attempt_id=$2 AND request_digest=$3 ON CONFLICT DO NOTHING`, request.TenantID, request.AttemptID, digest, store.source.Now().UTC().Truncate(time.Microsecond))
		if err != nil {
			return err
		}
		claimed = tag.RowsAffected() == 1
		var stored string
		var body []byte
		if err := tx.QueryRow(ctx, `SELECT request_digest,result_body FROM idenqa.provider_dispatches WHERE tenant_id=$1 AND attempt_id=$2`, request.TenantID, request.AttemptID).Scan(&stored, &body); err != nil {
			return provider.ErrRequestUnavailable
		}
		if stored != digest {
			return provider.ErrRequestUnavailable
		}
		if len(body) > 0 {
			var value providerv1.Result
			if json.Unmarshal(body, &value) != nil || value.ValidateForRequest(request) != nil {
				return provider.ErrRequestUnavailable
			}
			result = &value
		}
		return nil
	})
	return claimed, result, err
}

// Complete retains the first validated result; a retry cannot replace its meaning.
func (store *RequestStore) Complete(ctx context.Context, request providerv1.Request, result providerv1.Result) error {
	if result.ValidateForRequest(request) != nil {
		return provider.ErrRequestUnavailable
	}
	scope, _, err := provider.RequestScope(request)
	if err != nil {
		return err
	}
	digest, err := provider.RequestDigest(request)
	if err != nil {
		return err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE idenqa.provider_dispatches SET result_body=$4 WHERE tenant_id=$1 AND attempt_id=$2 AND request_digest=$3 AND result_body IS NULL`, request.TenantID, request.AttemptID, digest, body)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return provider.ErrRequestUnavailable
		}
		return nil
	})
}
