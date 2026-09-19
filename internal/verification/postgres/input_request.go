package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

// InputRequestStore persists and reads the active policy-authored subject-input
// request for one verification. Recording supersedes the previous active row so
// the newest request is authoritative without rewriting history.
type InputRequestStore struct{ pool transactionRunner }

// NewInputRequestStore constructs the tenant-scoped input-request adapter.
func NewInputRequestStore(pool transactionRunner) (*InputRequestStore, error) {
	if pool == nil {
		return nil, errors.New("verification postgres: input request pool is required")
	}
	return &InputRequestStore{pool: pool}, nil
}

// RecordWithin supersedes any active request and appends one immutable row
// inside the caller's routing transaction. The unique active index guarantees
// at most one current request per verification.
func (store *InputRequestStore) RecordWithin(
	ctx context.Context,
	scope tenant.Scope,
	tx platformpostgres.Transaction,
	request verification.InputRequest,
) error {
	if tx == nil || scope.ID().IsZero() || request.ID().IsZero() ||
		request.VerificationID().IsZero() || request.RequestedAt().IsZero() {
		return verification.ErrSessionConflict
	}
	if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
		return fmt.Errorf("set input request tenant scope: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE idenqa.verification_input_requests
SET superseded_at=$3
WHERE tenant_id=$1 AND verification_id=$2 AND superseded_at IS NULL`,
		scope.ID().String(), request.VerificationID().String(), request.RequestedAt()); err != nil {
		return fmt.Errorf("supersede active verification input request: %w", err)
	}
	var caseID *string
	if !request.CaseID().IsZero() {
		encoded := request.CaseID().String()
		caseID = &encoded
	}
	if _, err := tx.Exec(ctx, `INSERT INTO idenqa.verification_input_requests
(tenant_id,id,verification_id,case_id,reason_codes,actor_id,requested_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		scope.ID().String(), request.ID().String(), request.VerificationID().String(),
		caseID, request.ReasonCodes(), request.ActorID(), request.RequestedAt()); err != nil {
		return fmt.Errorf("insert verification input request: %w", err)
	}
	return nil
}

// FindCurrent returns the active request for a verification, or
// ErrInputRequestNotFound when none is active.
func (store *InputRequestStore) FindCurrent(
	ctx context.Context,
	scope tenant.Scope,
	verificationID id.Verification,
) (verification.InputRequest, error) {
	if scope.ID().IsZero() || verificationID.IsZero() {
		return verification.InputRequest{}, verification.ErrInputRequestNotFound
	}
	var request verification.InputRequest
	err := store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			var err error
			request, err = findCurrentInputRequest(ctx, tx, scope, verificationID)
			return err
		},
	)
	return request, err
}

func findCurrentInputRequest(
	ctx context.Context,
	tx platformpostgres.Transaction,
	scope tenant.Scope,
	verificationID id.Verification,
) (verification.InputRequest, error) {
	if _, err := sqlgen.New(tx).SetTenantScope(ctx, scope.ID().String()); err != nil {
		return verification.InputRequest{}, fmt.Errorf("set input request tenant scope: %w", err)
	}
	var encodedID, actorID string
	var reasonCodes []string
	var requestedAt time.Time
	var caseID *string
	err := tx.QueryRow(ctx, `SELECT id,reason_codes,actor_id,requested_at,case_id
FROM idenqa.verification_input_requests
WHERE tenant_id=$1 AND verification_id=$2 AND superseded_at IS NULL`,
		scope.ID().String(), verificationID.String()).Scan(
		&encodedID, &reasonCodes, &actorID, &requestedAt, &caseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return verification.InputRequest{}, verification.ErrInputRequestNotFound
	}
	if err != nil {
		return verification.InputRequest{}, fmt.Errorf("find current verification input request: %w", err)
	}
	identifier, err := id.ParseInputRequest(encodedID)
	if err != nil {
		return verification.InputRequest{}, verification.ErrSessionConflict
	}
	var reviewCase id.ReviewCase
	if caseID != nil {
		reviewCase, err = id.ParseReviewCase(*caseID)
		if err != nil {
			return verification.InputRequest{}, verification.ErrSessionConflict
		}
	}
	return verification.NewInputRequest(identifier, verificationID, reviewCase, reasonCodes, actorID, requestedAt.UTC())
}
