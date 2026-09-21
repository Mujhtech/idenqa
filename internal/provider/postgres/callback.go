package postgres

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

// ResolveCallback resolves one opaque callback reference through the reviewed
// SECURITY DEFINER function, then loads the exact tenant-scoped request and
// proves the reference binding. Unknown, expired, inactive, or cross-boundary
// references fail closed without disclosure.
func (store *RequestStore) ResolveCallback(ctx context.Context, reference string) (provider.CallbackTarget, error) {
	digest, err := provider.CallbackTokenDigest(reference)
	if err != nil || digest == "" {
		return provider.CallbackTarget{}, provider.ErrCallbackUnavailable
	}
	var target provider.CallbackTarget
	var checkValue string
	err = store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		var (
			tenantValue, attemptValue, verificationValue, attemptState, checkState string
			deadline                                                               time.Time
		)
		if err := tx.QueryRow(ctx, `SELECT tenant_id, attempt_id, verification_id, check_id, attempt_deadline, attempt_state, check_state FROM idenqa.resolve_provider_callback($1)`, digest).
			Scan(&tenantValue, &attemptValue, &verificationValue, &checkValue, &deadline, &attemptState, &checkState); err != nil {
			return err
		}
		if attemptState != "running" || checkState != "running" {
			return provider.ErrCallbackUnavailable
		}
		tenantID, err := id.ParseTenant(tenantValue)
		if err != nil {
			return provider.ErrCallbackUnavailable
		}
		scope, err := tenant.NewScope(tenantID)
		if err != nil {
			return provider.ErrCallbackUnavailable
		}
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		var body []byte
		var storedDigest string
		if err := tx.QueryRow(ctx, `SELECT request_body, request_digest FROM idenqa.provider_requests WHERE tenant_id=$1 AND attempt_id=$2 AND callback_token_digest=$3`, tenantValue, attemptValue, digest).Scan(&body, &storedDigest); err != nil {
			return err
		}
		var request providerv1.Request
		if json.Unmarshal(body, &request) != nil {
			return provider.ErrCallbackUnavailable
		}
		actual, err := provider.RequestDigest(request)
		if err != nil || actual != storedDigest || subtle.ConstantTimeCompare([]byte(request.CallbackReference), []byte(reference)) != 1 ||
			request.TenantID != tenantValue || request.AttemptID != attemptValue || request.VerificationID != verificationValue ||
			!request.Deadline.Equal(deadline) {
			return provider.ErrCallbackUnavailable
		}
		target = provider.CallbackTarget{Scope: scope, Request: request, Deadline: deadline.UTC()}
		return nil
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return provider.CallbackTarget{}, provider.ErrCallbackUnavailable
	}
	if err != nil {
		return provider.CallbackTarget{}, err
	}
	attemptID, err := id.ParseAttempt(target.Request.AttemptID)
	if err != nil {
		return provider.CallbackTarget{}, provider.ErrCallbackUnavailable
	}
	checkID, err := id.ParseCheck(checkValue)
	if err != nil {
		return provider.CallbackTarget{}, provider.ErrCallbackUnavailable
	}
	target.AttemptID = attemptID
	target.CheckID = checkID
	return target, nil
}

// SaveCallbackProgress records one normalized progress value keyed by provider
// replay identity. The first terminal content for an identity wins; an
// identical replay is an exact duplicate and conflicting terminal content is a
// bounded conflict.
func (store *RequestStore) SaveCallbackProgress(ctx context.Context, target provider.CallbackTarget, progress providerv1.Progress) (provider.CallbackReceipt, error) {
	if progress.ValidateForRequest(target.Request) != nil {
		return provider.CallbackReceipt{}, provider.ErrCallbackInvalid
	}
	if progress.Result != nil {
		// The document observation is transient: consume it into bounded Core
		// signals before the receipt is digested or persisted.
		consumed, err := verification.ConsumeProviderDocument(*progress.Result)
		if err != nil {
			return provider.CallbackReceipt{}, provider.ErrCallbackInvalid
		}
		progress.Result = &consumed
	}
	replayID := progress.ReplayID
	if replayID == "" {
		replayID = progress.ProviderJobID
	}
	if replayID == "" {
		return provider.CallbackReceipt{}, provider.ErrCallbackInvalid
	}
	progressDigest, err := provider.CallbackProgressDigest(progress)
	if err != nil {
		return provider.CallbackReceipt{}, err
	}
	configurationDigest, err := provider.CallbackConfigurationDigest(target.Request)
	if err != nil {
		return provider.CallbackReceipt{}, err
	}
	body, err := json.Marshal(progress)
	if err != nil {
		return provider.CallbackReceipt{}, err
	}
	var resultDigest *string
	if progress.Result != nil {
		value, err := verification.ProviderResultFingerprint(*progress.Result)
		if err != nil {
			return provider.CallbackReceipt{}, err
		}
		resultDigest = &value
	}
	receipt := provider.CallbackReceipt{Terminal: progress.Result != nil}
	err = store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, target.Scope.ID().String()); err != nil {
			return err
		}
		now := store.source.Now().UTC().Truncate(time.Microsecond)
		var confirmed string
		err := tx.QueryRow(ctx, `INSERT INTO idenqa.provider_callback_receipts(tenant_id,attempt_id,verification_id,check_id,provider_job_id,provider_replay_id,configuration_digest,progress_digest,result_digest,progress_body,received_at) VALUES($1,$2,$3,$4,NULLIF($5,''),$6,$7,$8,$9,$10,$11) ON CONFLICT (tenant_id,attempt_id,provider_replay_id) DO UPDATE SET progress_body=EXCLUDED.progress_body,progress_digest=EXCLUDED.progress_digest,result_digest=EXCLUDED.result_digest,provider_job_id=COALESCE(idenqa.provider_callback_receipts.provider_job_id,EXCLUDED.provider_job_id),received_at=EXCLUDED.received_at WHERE idenqa.provider_callback_receipts.result_digest IS NULL AND EXCLUDED.result_digest IS NOT NULL RETURNING progress_digest`,
			target.Request.TenantID, target.Request.AttemptID, target.Request.VerificationID, target.CheckID.String(),
			progress.ProviderJobID, replayID, configurationDigest, progressDigest, resultDigest, body, now).Scan(&confirmed)
		if err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		// The stored receipt is either identical replay or already terminal.
		var storedProgress, storedResult, storedJob, storedConfiguration string
		if err := tx.QueryRow(ctx, `SELECT progress_digest,COALESCE(result_digest,''),COALESCE(provider_job_id,''),configuration_digest FROM idenqa.provider_callback_receipts WHERE tenant_id=$1 AND attempt_id=$2 AND provider_replay_id=$3`, target.Request.TenantID, target.Request.AttemptID, replayID).
			Scan(&storedProgress, &storedResult, &storedJob, &storedConfiguration); err != nil {
			return err
		}
		if storedConfiguration != configurationDigest || (storedJob != "" && progress.ProviderJobID != "" && storedJob != progress.ProviderJobID) {
			return provider.ErrCallbackConflict
		}
		switch {
		case storedProgress == progressDigest:
			receipt.Duplicate = true
			receipt.Terminal = storedResult != ""
		case storedResult != "" && progress.Result == nil:
			// A terminal receipt already exists; the pending delivery adds nothing.
			receipt.Duplicate = true
			receipt.Terminal = true
		default:
			return provider.ErrCallbackConflict
		}
		return nil
	})
	if err != nil {
		return provider.CallbackReceipt{}, err
	}
	return receipt, nil
}

var _ provider.CallbackResolver = (*RequestStore)(nil)
var _ provider.CallbackReceiptStore = (*RequestStore)(nil)
