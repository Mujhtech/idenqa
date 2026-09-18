package postgres

import (
	"context"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	reviewtask "github.com/Mujhtech/idenqa/internal/review/task"
)

// ListReady discovers only bounded reference-only accepted-finding requests.
func (store *EvaluationStore) ListReady(ctx context.Context, at time.Time, limit int) ([]reviewtask.Target, error) {
	if limit < 1 || limit > 100 {
		return nil, review.ErrInvalid
	}
	if err := store.cases.ExpireAppeals(ctx, at, limit); err != nil {
		return nil, err
	}
	var targets []reviewtask.Target
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT tenant_id,case_id,case_version FROM idenqa.list_ready_review_evaluations($1,$2)`, at, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var tenantValue, caseValue string
			var version int64
			if err := rows.Scan(&tenantValue, &caseValue, &version); err != nil {
				return err
			}
			tenantID, err := id.ParseTenant(tenantValue)
			if err != nil {
				return err
			}
			caseID, err := id.ParseReviewCase(caseValue)
			if err != nil {
				return err
			}
			targets = append(targets, reviewtask.Target{TenantID: tenantID, Request: review.EvaluationRequest{CaseID: caseID, Version: version}})
		}
		return rows.Err()
	})
	return targets, err
}
