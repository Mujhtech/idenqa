package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// AcknowledgeRecapture atomically records an immutable acknowledgement and its audit.
func (store *RecaptureStore) AcknowledgeRecapture(ctx context.Context, scope tenant.Scope, input review.AcknowledgeRecapture) (review.RecaptureAcknowledgement, error) {
	var result review.RecaptureAcknowledgement
	if input.CaseID.IsZero() || input.CaseVersion < 1 || input.DecisionID.IsZero() || input.ActorID.IsZero() || len(input.ReviewerID) == 0 || len(input.ReviewerID) > 128 || !review.ValidTime(input.At) || input.At.After(store.clock.Now().UTC()) || input.Idempotency.TenantID() != scope.ID() || input.Idempotency.Principal().String() != input.ActorID.String() || input.Idempotency.Operation() != review.OperationRecaptureAcknowledge {
		return result, review.ErrInvalid
	}
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		currentCase, err := store.findCaseWithin(ctx, scope, tx, input.CaseID)
		if err != nil {
			return err
		}
		if err := store.checkAuthority(ctx, tx, scope, review.Actor{ID: input.ActorID.String()}, currentCase, review.PermissionResolve); err != nil {
			return err
		}

		q := sqlgen.New(tx)
		reservation, err := idempg.Reserve(ctx, q, input.Idempotency)
		if err != nil {
			return err
		}
		result, err = findAcknowledgement(ctx, tx, scope, input.CaseID, input.CaseVersion)
		if err == nil {
			if result.DecisionID != input.DecisionID {
				return review.ErrConflict
			}
		} else {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if _, exists := reservation.Result(); exists {
				return review.ErrConflict
			}
			var childValue string
			err = tx.QueryRow(ctx, `SELECT r.child_verification_id FROM idenqa.review_recaptures r JOIN idenqa.review_cases c ON c.tenant_id=r.tenant_id AND c.id=r.case_id AND c.version=r.case_version JOIN idenqa.verification_sessions s ON s.tenant_id=r.tenant_id AND s.id=r.child_verification_id WHERE r.tenant_id=$1 AND r.case_id=$2 AND r.case_version=$3 AND s.state='completed' AND s.completed_decision_id=$4 AND s.updated_at<=$5 FOR UPDATE OF c`, scope.ID().String(), input.CaseID.String(), input.CaseVersion, input.DecisionID.String(), input.At).Scan(&childValue)
			if errors.Is(err, pgx.ErrNoRows) {
				return review.ErrConflict
			}
			if err != nil {
				return err
			}
			child, err := id.ParseVerification(childValue)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_recapture_acknowledgements(tenant_id,case_id,case_version,child_verification_id,decision_id,actor_key_id,reviewer_id,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, scope.ID().String(), input.CaseID.String(), input.CaseVersion, child.String(), input.DecisionID.String(), input.ActorID.String(), input.ReviewerID, input.At)
			if err != nil {
				return err
			}
			if err := appendReviewAudit(ctx, tx, scope, review.Actor{ID: input.ActorID.String()}, input.CaseID.String(), "review.case.child_outcome_acknowledged", input.At, input.CaseVersion); err != nil {
				return err
			}
			result = review.RecaptureAcknowledgement{CaseID: input.CaseID, CaseVersion: input.CaseVersion, ChildID: child, DecisionID: input.DecisionID, ActorID: input.ActorID, ReviewerID: input.ReviewerID, RecordedAt: input.At}
		}
		if _, exists := reservation.Result(); exists {
			return nil
		}
		encoded, err := json.Marshal(struct {
			CaseID     string `json:"case_id"`
			Version    int64  `json:"case_version"`
			DecisionID string `json:"decision_id"`
		}{result.CaseID.String(), result.CaseVersion, result.DecisionID.String()})
		if err != nil {
			return err
		}
		replay, err := idempotency.NewResult(200, encoded)
		if err != nil {
			return err
		}
		return idempg.Complete(ctx, q, input.Idempotency, replay, input.At)
	})
	return result, err
}
func findAcknowledgement(ctx context.Context, tx pg.Transaction, scope tenant.Scope, caseID id.ReviewCase, version int64) (review.RecaptureAcknowledgement, error) {
	var result review.RecaptureAcknowledgement
	var child, decision, actor string
	err := tx.QueryRow(ctx, `SELECT child_verification_id,decision_id,actor_key_id,reviewer_id,recorded_at FROM idenqa.review_recapture_acknowledgements WHERE tenant_id=$1 AND case_id=$2 AND case_version=$3`, scope.ID().String(), caseID.String(), version).Scan(&child, &decision, &actor, &result.ReviewerID, &result.RecordedAt)
	if err != nil {
		return result, err
	}
	result.RecordedAt = result.RecordedAt.UTC()
	result.CaseID = caseID
	result.CaseVersion = version
	result.ChildID, err = id.ParseVerification(child)
	if err != nil {
		return result, err
	}
	result.DecisionID, err = id.ParseDecision(decision)
	if err != nil {
		return result, err
	}
	result.ActorID, err = id.ParseAPIKey(actor)
	return result, err
}
