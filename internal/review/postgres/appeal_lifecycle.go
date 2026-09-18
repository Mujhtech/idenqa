package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// WithdrawAppeal allows the requester to withdraw an active appeal.
func (s *Store) WithdrawAppeal(ctx context.Context, scope tenant.Scope, actor review.Actor, identifier id.Appeal, version int64) (review.Appeal, error) {
	if identifier.IsZero() || version < 1 {
		return review.Appeal{}, review.ErrInvalid
	}
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		var requester, state string
		var current int64
		var deadline time.Time
		if err := tx.QueryRow(ctx, `SELECT coalesce(requested_by,''),state,version,deadline FROM idenqa.appeals WHERE tenant_id=$1 AND id=$2 FOR UPDATE`, scope.ID().String(), identifier.String()).Scan(&requester, &state, &current, &deadline); err != nil {
			return review.ErrForbidden
		}
		if requester != actor.ID {
			return review.ErrForbidden
		}
		if state == "withdrawn" && current == version+1 {
			return nil
		}
		if current != version || (state != "requested" && state != "independent_review" && state != "awaiting_input") || !s.clock.Now().Before(deadline) {
			return review.ErrConflict
		}
		now := s.clock.Now().UTC().Truncate(time.Microsecond)
		if _, err := tx.Exec(ctx, `UPDATE idenqa.appeals SET state='withdrawn',outcome=NULL,reason_code=NULL,version=version+1,updated_at=$3 WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier.String(), now); err != nil {
			return err
		}
		return appendReviewAudit(ctx, tx, scope, actor, identifier.String(), "review.appeal.withdrawn", now, version+1)
	})
	if err != nil {
		return review.Appeal{}, err
	}
	return s.FindAppeal(ctx, scope, identifier)
}

// ExpireAppeals reconciles bounded overdue appeals with atomic history and audit.
func (s *Store) ExpireAppeals(ctx context.Context, at time.Time, limit int) error {
	type target struct {
		tenant  string
		appeal  string
		version int64
	}
	var targets []target
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id,appeal_id,version FROM idenqa.list_expired_review_appeals($1,$2)`, at, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var value target
			if err := rows.Scan(&value.tenant, &value.appeal, &value.version); err != nil {
				return err
			}
			targets = append(targets, value)
		}
		return rows.Err()
	})
	if err != nil {
		return err
	}
	for _, target := range targets {
		tenantID, err := id.ParseTenant(target.tenant)
		if err != nil {
			return err
		}
		scope, err := tenant.NewScope(tenantID)
		if err != nil {
			return err
		}
		err = s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
			if err := setScope(ctx, tx, scope); err != nil {
				return err
			}
			tag, err := tx.Exec(ctx, `UPDATE idenqa.appeals SET state='expired',outcome=NULL,reason_code=NULL,version=version+1,updated_at=$4 WHERE tenant_id=$1 AND id=$2 AND version=$3 AND state IN ('requested','independent_review','awaiting_input') AND deadline<=$4`, target.tenant, target.appeal, target.version, at)
			if err != nil {
				return err
			}
			if tag.RowsAffected() == 0 {
				return nil
			}
			return appendReviewAudit(ctx, tx, scope, review.Actor{ID: "worker.review"}, target.appeal, "review.appeal.expired", at, target.version+1)
		})
		if err != nil {
			return errors.Join(review.ErrConflict, err)
		}
	}
	return nil
}

// CreateAppealWithKey commits appeal intake and its retry identity together.
func (s *Store) CreateAppealWithKey(ctx context.Context, scope tenant.Scope, actor review.Actor, value review.Appeal, now time.Time, retry idempotency.Request) (review.Appeal, error) {
	var result review.Appeal
	if retry.TenantID() != scope.ID() || retry.Principal().String() != actor.ID || retry.Operation() != "reviews.appeal.request" {
		return result, review.ErrInvalid
	}
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		q := sqlgen.New(tx)
		reservation, err := idempg.Reserve(ctx, q, retry)
		if err != nil {
			return err
		}
		bound := *s
		bound.pool = boundTransaction{tx}
		if replay, ok := reservation.Result(); ok {
			var wire struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(replay.Body(), &wire) != nil {
				return review.ErrConflict
			}
			identifier, err := id.ParseAppeal(wire.ID)
			if err != nil {
				return err
			}
			result, err = bound.FindAppeal(ctx, scope, identifier)
			return err
		}
		if err := bound.CreateAppeal(ctx, scope, actor, value, now); err != nil {
			return err
		}
		result = value
		body, err := json.Marshal(struct {
			ID string `json:"id"`
		}{value.ID.String()})
		if err != nil {
			return err
		}
		replay, err := idempotency.NewResult(201, body)
		if err != nil {
			return err
		}
		return idempg.Complete(ctx, q, retry, replay, now)
	})
	return result, err
}
