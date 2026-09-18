package postgres

import (
	"context"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ListQueue uses bounded keyset pagination and exact filters; callers never supply SQL expressions.
func (s *Store) ListQueue(ctx context.Context, scope tenant.Scope, q review.QueueQuery) ([]review.QueueItem, error) {
	if q.Limit < 1 || q.Limit > 100 || q.At.IsZero() {
		return nil, review.ErrInvalid
	}
	var result []review.QueueItem
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT c.id,coalesce(o.version,0),coalesce(o.priority,0),o.due_at,coalesce(o.language,''),coalesce(o.reason,''),coalesce(o.assurance,''),coalesce(o.risk,''),coalesce(o.sampled,false),coalesce(o.due_at,c.created_at)
 FROM idenqa.review_cases c LEFT JOIN idenqa.review_case_operations o ON o.tenant_id=c.tenant_id AND o.case_id=c.id
 WHERE c.tenant_id=$1 AND ($2='' OR c.state=$2) AND ($3='' OR c.region=$3) AND ($4='' OR c.assigned_reviewer=$4)
 AND ($5='' OR o.language=$5) AND ($6='' OR o.reason=$6) AND ($7='' OR o.assurance=$7) AND ($8='' OR o.risk=$8) AND ($9='' OR c.required_certification=$9)
 AND ($10::boolean IS NULL OR coalesce(o.sampled,false)=$10) AND ($11::boolean IS NULL OR coalesce(o.due_at<=$12 AND c.state<>'resolved',false)=$11)
 AND ($13='' OR (-coalesce(o.priority,0),coalesce(o.due_at,c.created_at),c.id)>(-$14::integer,$15::timestamptz,$13))
 ORDER BY coalesce(o.priority,0) DESC,coalesce(o.due_at,c.created_at),c.id LIMIT $16`, scope.ID().String(), q.State, q.Region, q.Reviewer, q.Language, q.Reason, q.Assurance, q.Risk, q.Certificate, q.Sampled, q.Overdue, q.At, q.AfterID, q.AfterPriority, q.AfterDue, q.Limit+1)
		if err != nil {
			return fmt.Errorf("list review queue: %w", err)
		}
		var caseIDs []id.ReviewCase
		for rows.Next() {
			var item review.QueueItem
			var caseValue string
			if err := rows.Scan(&caseValue, &item.Version, &item.Priority, &item.DueAt, &item.Language, &item.Reason, &item.Assurance, &item.Risk, &item.Sampled, &item.SortAt); err != nil {
				rows.Close()
				return err
			}
			identifier, err := id.ParseReviewCase(caseValue)
			if err != nil {
				rows.Close()
				return err
			}
			caseIDs = append(caseIDs, identifier)
			result = append(result, item)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		for i, identifier := range caseIDs {
			value, err := s.findCaseWithin(ctx, scope, tx, identifier)
			if err != nil {
				return err
			}
			result[i].Case = value
		}
		return nil
	})
	return result, err
}
