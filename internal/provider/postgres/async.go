package postgres

import (
	"context"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

// ClaimAsync atomically records submission ownership and a bounded polling fence.
func (store *RequestStore) ClaimAsync(ctx context.Context, request providerv1.Request) (provider.AsyncClaim, error) {
	scope, _, err := provider.RequestScope(request)
	if err != nil {
		return provider.AsyncClaim{}, err
	}
	digest, err := provider.RequestDigest(request)
	if err != nil {
		return provider.AsyncClaim{}, err
	}
	verificationID, err := id.ParseVerification(request.VerificationID)
	if err != nil {
		return provider.AsyncClaim{}, err
	}
	var claim provider.AsyncClaim
	err = store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		now := store.source.Now().UTC().Truncate(time.Microsecond)
		if !now.Before(request.Deadline) {
			return provider.ErrRequestUnavailable
		}
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		if err := authoritypostgres.ValidateExecutionWithin(ctx, tx, scope, verificationID, now, store.source, verification.SessionStateProcessing); err != nil {
			return err
		}
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_attempts a JOIN idenqa.verification_checks c ON c.tenant_id=a.tenant_id AND c.id=a.check_id WHERE a.tenant_id=$1 AND a.id=$2 AND a.verification_id=$3 AND a.state='running' AND c.state='running' AND a.deadline>$4 AND NOT EXISTS(SELECT 1 FROM idenqa.verification_attempts newer WHERE newer.tenant_id=a.tenant_id AND newer.check_id=a.check_id AND newer.attempt_number>a.attempt_number))`, request.TenantID, request.AttemptID, request.VerificationID, now).Scan(&active); err != nil {
			return err
		}
		if !active {
			return provider.ErrRequestUnavailable
		}
		bound := &RequestStore{pool: boundTransaction{tx}, source: store.source}
		initial, result, err := bound.Claim(ctx, request)
		if err != nil {
			return err
		}
		claim.Initial = initial
		claim.Result = result
		if result != nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.provider_async_operations(tenant_id,attempt_id,request_digest,next_poll_at) VALUES($1,$2,$3,$4) ON CONFLICT DO NOTHING`, request.TenantID, request.AttemptID, digest, now); err != nil {
			return err
		}
		var stored string
		var lease *time.Time
		var next time.Time
		if err := tx.QueryRow(ctx, `SELECT request_digest,fence,lease_expires_at,next_poll_at FROM idenqa.provider_async_operations WHERE tenant_id=$1 AND attempt_id=$2 FOR UPDATE`, request.TenantID, request.AttemptID).Scan(&stored, &claim.Fence, &lease, &next); err != nil {
			return err
		}
		if stored != digest {
			return provider.ErrRequestUnavailable
		}
		if next.After(now) || (lease != nil && lease.After(now)) {
			return nil
		}
		claim.Fence++
		claim.Acquired = true
		_, err = tx.Exec(ctx, `UPDATE idenqa.provider_async_operations SET fence=$3,lease_expires_at=$4 WHERE tenant_id=$1 AND attempt_id=$2`, request.TenantID, request.AttemptID, claim.Fence, now.Add(45*time.Second))
		return err
	})
	return claim, err
}

// SaveProgress retains the exact provider reference and commits only the current fence.
func (store *RequestStore) SaveProgress(ctx context.Context, request providerv1.Request, claim provider.AsyncClaim, progress providerv1.Progress) error {
	if !claim.Acquired || progress.ValidateForRequest(request) != nil {
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
	return store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		now := store.source.Now().UTC().Truncate(time.Microsecond)
		tag, err := tx.Exec(ctx, `UPDATE idenqa.provider_async_operations SET provider_job_id=COALESCE(provider_job_id,NULLIF($5,'')),lease_expires_at=NULL,next_poll_at=$6 WHERE tenant_id=$1 AND attempt_id=$2 AND request_digest=$3 AND fence=$4 AND lease_expires_at>$7 AND (provider_job_id IS NULL OR $5='' OR provider_job_id=$5)`, request.TenantID, request.AttemptID, digest, claim.Fence, progress.ProviderJobID, now.Add(5*time.Second), now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return provider.ErrDispatchPending
		}
		if progress.Result != nil {
			bound := &RequestStore{pool: boundTransaction{tx}, source: store.source}
			return bound.Complete(ctx, request, *progress.Result)
		}
		return nil
	})
}

// Expire preserves a received terminal result before recording an unresolved job
// timeout. This is an operational outcome, never negative identity evidence.
func (store *RequestStore) Expire(ctx context.Context, scope tenant.Scope, check verification.Check, attempt verification.Attempt) (providerv1.Result, error) {
	request, err := store.Load(ctx, scope, check, attempt)
	if err != nil {
		return providerv1.Result{}, err
	}
	if store.source.Now().Before(request.Deadline) {
		return providerv1.Result{}, provider.ErrRequestUnavailable
	}
	result := providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeFailed, Failure: &providerv1.Failure{Class: providerv1.FailureDeadline, Code: "provider_job_unresolved", Retry: providerv1.RetryReconcile}, CompletedAt: request.Deadline}
	err = store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		if err := authoritypostgres.ValidateExecutionWithin(ctx, tx, scope, check.VerificationID, store.source.Now().UTC(), store.source, verification.SessionStateProcessing); err != nil {
			return err
		}
		bound := &RequestStore{pool: boundTransaction{tx}, source: store.source}
		_, saved, err := bound.Claim(ctx, request)
		if err != nil {
			return err
		}
		if saved != nil {
			result = *saved
			return nil
		}
		return bound.Complete(ctx, request, result)
	})
	return result, err
}
