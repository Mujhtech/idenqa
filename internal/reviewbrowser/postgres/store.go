// Package postgres persists tenant-scoped, case-bound browser review sessions.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/reviewbrowser"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type transactionRunner interface {
	WithinTransaction(context.Context, pg.TransactionOptions, func(context.Context, pg.Transaction) error) error
}

type Store struct{ pool transactionRunner }

func New(pool transactionRunner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("review browser postgres: pool is required")
	}
	return &Store{pool: pool}, nil
}

func (store *Store) Consume(ctx context.Context, session reviewbrowser.Session, tokenHash, nonceHash, claimsHash [32]byte) error {
	if session.Scope.ID().IsZero() || session.BootstrapID == "" || session.Actor.ID == "" || session.CaseID.IsZero() ||
		session.Version < 1 || session.Region == "" || session.Origin == "" || session.CreatedAt.IsZero() || !session.ExpiresAt.After(session.CreatedAt) {
		return reviewbrowser.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, session.Scope); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.review_evidence_session_bootstraps
			(tenant_id,bootstrap_id,nonce_hash,claims_hash,actor_id,case_id,case_version,region,origin,consumed_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, session.Scope.ID().String(), session.BootstrapID, nonceHash[:], claimsHash[:],
			session.Actor.ID, session.CaseID.String(), session.Version, session.Region, session.Origin, session.CreatedAt)
		if err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) && databaseError.Code == "23505" {
				return reviewbrowser.ErrReplay
			}
			return fmt.Errorf("consume review browser bootstrap: %w", err)
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_evidence_sessions
			(tenant_id,token_hash,bootstrap_id,actor_id,case_id,case_version,region,origin,created_at,expires_at)
			VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, session.Scope.ID().String(), tokenHash[:], session.BootstrapID,
			session.Actor.ID, session.CaseID.String(), session.Version, session.Region, session.Origin, session.CreatedAt, session.ExpiresAt)
		if err != nil {
			return fmt.Errorf("persist review browser session: %w", err)
		}
		return nil
	})
}

func (store *Store) Authenticate(ctx context.Context, scope tenant.Scope, tokenHash [32]byte, origin string, now time.Time) (reviewbrowser.Session, error) {
	var session reviewbrowser.Session
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		var caseID string
		err := tx.QueryRow(ctx, `SELECT bootstrap_id,actor_id,case_id,case_version,region,origin,created_at,expires_at
			FROM idenqa.review_evidence_sessions
			WHERE tenant_id=$1 AND token_hash=$2 AND origin=$3`, scope.ID().String(), tokenHash[:], origin).
			Scan(&session.BootstrapID, &session.Actor.ID, &caseID, &session.Version, &session.Region, &session.Origin, &session.CreatedAt, &session.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return reviewbrowser.ErrForbidden
		}
		if err != nil {
			return fmt.Errorf("authenticate review browser session: %w", err)
		}
		if !session.ExpiresAt.After(now) {
			return reviewbrowser.ErrExpired
		}
		session.CaseID, err = id.ParseReviewCase(caseID)
		if err != nil {
			return reviewbrowser.ErrInvalid
		}
		session.Scope = scope
		return nil
	})
	return session, err
}

func setScope(ctx context.Context, tx pg.Transaction, scope tenant.Scope) error {
	if tx == nil || scope.ID().IsZero() {
		return reviewbrowser.ErrInvalid
	}
	var value string
	return tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&value)
}

var _ reviewbrowser.Store = (*Store)(nil)
