// Package postgres persists immutable model requests and dispatch receipts.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
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

// NewRequestStore constructs durable model dispatch persistence.
func NewRequestStore(pool transactionRunner, source clock.Clock) (*RequestStore, error) {
	if pool == nil || source == nil {
		return nil, model.ErrRequestUnavailable
	}
	return &RequestStore{pool, source}, nil
}

// SaveWithin appends the request with its owning attempt, grant and task transaction.
func (store *RequestStore) SaveWithin(ctx context.Context, tx pg.Transaction, check verification.Check, request modelv1.Request) error {
	return store.saveWithin(ctx, tx, check, request, nil)
}

// SaveRegisteredWithin persists the exact registry selection alongside its immutable request.
func (store *RequestStore) SaveRegisteredWithin(ctx context.Context, tx pg.Transaction, check verification.Check, request modelv1.Request, plan *model.Plan) error {
	if plan == nil || plan.Binding.Registry == nil {
		return model.ErrRegistryInvalid
	}
	return store.saveWithin(ctx, tx, check, request, plan)
}

func (store *RequestStore) saveWithin(ctx context.Context, tx pg.Transaction, check verification.Check, request modelv1.Request, plan *model.Plan) error {
	digest, err := model.RequestDigest(request)
	if err != nil {
		return err
	}
	attempts := check.Attempts()
	if len(attempts) != 1 || attempts[0].ID.String() != request.AttemptID || attempts[0].Provenance.RequestDigest != digest || check.TenantID.String() != request.TenantID || check.VerificationID.String() != request.VerificationID {
		return model.ErrRequestUnavailable
	}
	body, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if plan != nil && plan.Binding.Registry != nil {
		if request.Provenance != plan.Manifest.Provenance || request.Configuration != plan.Binding.Configuration || request.Restrictions != plan.Manifest.Restrictions || !reflect.DeepEqual(request.Capability, plan.Capability) {
			return model.ErrRegistryInvalid
		}
		scope, err := tenant.NewScope(check.TenantID)
		if err != nil {
			return err
		}
		if err := ValidateSelection(ctx, tx, scope, plan); err != nil {
			return err
		}
		state, err := readRegistry(ctx, tx, scope, plan.Binding.Registry.Name, true)
		if err != nil {
			return err
		}
		selection, err := json.Marshal(plan.Binding.Registry)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.model_requests(tenant_id,attempt_id,verification_id,check_id,request_digest,request_body,registry_name,registry_version,registry_selection) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, request.TenantID, request.AttemptID, request.VerificationID, check.ID.String(), digest, body, state.Name, state.Version, selection)
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO idenqa.model_requests(tenant_id,attempt_id,verification_id,check_id,request_digest,request_body) VALUES($1,$2,$3,$4,$5,$6)`, request.TenantID, request.AttemptID, request.VerificationID, check.ID.String(), digest, body)
	return err
}

// Load checks current processing authority and the immutable attempt binding.
func (store *RequestStore) Load(ctx context.Context, scope tenant.Scope, check verification.Check, attempt verification.Attempt) (modelv1.Request, error) {
	var request modelv1.Request
	if scope.ID() != check.TenantID {
		return request, model.ErrRequestUnavailable
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
		err := tx.QueryRow(ctx, `SELECT request_body,request_digest FROM idenqa.model_requests WHERE tenant_id=$1 AND attempt_id=$2 AND check_id=$3 AND verification_id=$4`, scope.ID().String(), attempt.ID.String(), check.ID.String(), check.VerificationID.String()).Scan(&body, &digest)
		if errors.Is(err, pgx.ErrNoRows) {
			return model.ErrRequestUnavailable
		}
		if err != nil {
			return err
		}
		if json.Unmarshal(body, &request) != nil {
			return model.ErrRequestUnavailable
		}
		actual, err := model.RequestDigest(request)
		if err != nil || actual != digest || digest != attempt.Provenance.RequestDigest || request.AttemptID != attempt.ID.String() || request.TenantID != scope.ID().String() || request.VerificationID != check.VerificationID.String() || !request.Deadline.Equal(attempt.Deadline) {
			return model.ErrRequestUnavailable
		}
		return nil
	})
	return request, err
}

// Claim permits at most one initial external dispatch for a durable attempt.
func (store *RequestStore) Claim(ctx context.Context, request modelv1.Request) (bool, *modelv1.Result, error) {
	scope, _, err := model.RequestScope(request)
	if err != nil {
		return false, nil, err
	}
	digest, err := model.RequestDigest(request)
	if err != nil {
		return false, nil, err
	}
	claimed := false
	var result *modelv1.Result
	err = store.pool.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
			return err
		}
		tag, err := tx.Exec(ctx, `INSERT INTO idenqa.model_dispatches(tenant_id,attempt_id,request_digest,claimed_at) SELECT tenant_id,attempt_id,request_digest,$4 FROM idenqa.model_requests WHERE tenant_id=$1 AND attempt_id=$2 AND request_digest=$3 ON CONFLICT DO NOTHING`, request.TenantID, request.AttemptID, digest, store.source.Now().UTC().Truncate(time.Microsecond))
		if err != nil {
			return err
		}
		claimed = tag.RowsAffected() == 1
		verificationID, err := id.ParseVerification(request.VerificationID)
		if err != nil {
			return model.ErrRequestUnavailable
		}
		now := store.source.Now().UTC()
		if err := authoritypostgres.ValidateProcessingWithin(ctx, tx, scope, verificationID, now, store.source, verification.SessionStateProcessing); err != nil {
			return err
		}
		var active bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_attempts a JOIN idenqa.verification_checks c ON c.tenant_id=a.tenant_id AND c.id=a.check_id WHERE a.tenant_id=$1 AND a.id=$2 AND a.verification_id=$3 AND a.state='running' AND c.state='running' AND a.deadline>$4 AND NOT EXISTS(SELECT 1 FROM idenqa.verification_attempts newer WHERE newer.tenant_id=a.tenant_id AND newer.check_id=a.check_id AND newer.attempt_number>a.attempt_number))`, request.TenantID, request.AttemptID, request.VerificationID, now).Scan(&active); err != nil {
			return err
		}
		if !active {
			return model.ErrRequestUnavailable
		}

		var stored string
		var body []byte
		if err := tx.QueryRow(ctx, `SELECT request_digest,result_body FROM idenqa.model_dispatches WHERE tenant_id=$1 AND attempt_id=$2`, request.TenantID, request.AttemptID).Scan(&stored, &body); err != nil {
			return model.ErrRequestUnavailable
		}
		if stored != digest {
			return model.ErrRequestUnavailable
		}
		if len(body) > 0 {
			var value modelv1.Result
			if json.Unmarshal(body, &value) != nil || value.ValidateForRequest(request) != nil {
				return model.ErrRequestUnavailable
			}
			result = &value
		}
		return nil
	})
	if err != nil {
		return false, nil, err
	}
	return claimed, result, nil
}

// Complete retains the first validated result; a retry cannot replace its meaning.
func (store *RequestStore) Complete(ctx context.Context, request modelv1.Request, result modelv1.Result) error {
	if result.ValidateForRequest(request) != nil {
		return model.ErrRequestUnavailable
	}
	scope, _, err := model.RequestScope(request)
	if err != nil {
		return err
	}
	digest, err := model.RequestDigest(request)
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
		tag, err := tx.Exec(ctx, `UPDATE idenqa.model_dispatches SET result_body=$4 WHERE tenant_id=$1 AND attempt_id=$2 AND request_digest=$3 AND result_body IS NULL`, request.TenantID, request.AttemptID, digest, body)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return model.ErrRequestUnavailable
		}
		return nil
	})
}
