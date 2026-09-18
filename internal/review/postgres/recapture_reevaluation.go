package postgres

import (
	"context"
	"encoding/json"
	"errors"

	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

// ReevaluateRecapture atomically advances the case and persists an independently replayable intent.
func (store *RecaptureStore) ReevaluateRecapture(ctx context.Context, scope tenant.Scope, input review.AcknowledgeRecapture) (review.RecaptureReevaluation, error) {
	var result review.RecaptureReevaluation
	if input.CaseID.IsZero() || input.CaseVersion < 1 || input.CaseVersion == 9223372036854775807 || input.DecisionID.IsZero() || input.ActorID.IsZero() || len(input.ReviewerID) == 0 || len(input.ReviewerID) > 128 || !review.ValidTime(input.At) || input.At.After(store.clock.Now().UTC()) || input.Idempotency.TenantID() != scope.ID() || input.Idempotency.Principal().String() != input.ActorID.String() || input.Idempotency.Operation() != review.OperationRecaptureReevaluate {
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
		result, err = findRecaptureReevaluation(ctx, tx, scope, input.CaseID, input.CaseVersion)
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
			value, err := store.findCaseWithin(ctx, scope, tx, input.CaseID)
			if err != nil {
				return err
			}
			if value.Version != input.CaseVersion || input.At.Before(value.UpdatedAt) {
				return review.ErrConflict
			}
			if value.State != review.CaseResolved {
				return review.ErrConflict
			}
			// Lock/fence parent authority before the case, matching worker completion ordering.
			if err := authoritypostgres.ValidateProcessingWithin(ctx, tx, scope, value.VerificationID, input.At, store.clock, verification.SessionStateManualReview); err != nil {
				return err
			}
			ack, err := findAcknowledgement(ctx, tx, scope, input.CaseID, input.CaseVersion)
			if errors.Is(err, pgx.ErrNoRows) {
				return review.ErrConflict
			}
			if err != nil {
				return err
			}
			if ack.DecisionID != input.DecisionID || input.At.Before(ack.RecordedAt) {
				return review.ErrConflict
			}
			var ready bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.review_evaluations WHERE tenant_id=$1 AND case_id=$2 AND case_version=$3)`, scope.ID().String(), input.CaseID.String(), input.CaseVersion).Scan(&ready); err != nil {
				return err
			}
			if !ready {
				return review.ErrConflict
			}
			tag, err := tx.Exec(ctx, `UPDATE idenqa.review_cases SET version=version+1,updated_at=$4 WHERE tenant_id=$1 AND id=$2 AND version=$3 AND state='resolved'`, scope.ID().String(), input.CaseID.String(), input.CaseVersion, input.At)
			if err != nil {
				return err
			}
			if tag.RowsAffected() != 1 {
				return review.ErrConflict
			}
			_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_evaluation_requests(tenant_id,case_id,case_version,created_at) VALUES($1,$2,$3,$4)`, scope.ID().String(), input.CaseID.String(), input.CaseVersion+1, input.At)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_recapture_evaluation_requests(tenant_id,case_id,source_version,target_version,decision_id,actor_key_id,reviewer_id,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, scope.ID().String(), input.CaseID.String(), input.CaseVersion, input.CaseVersion+1, input.DecisionID.String(), input.ActorID.String(), input.ReviewerID, input.At)
			if err != nil {
				return err
			}
			if err := appendReviewAudit(ctx, tx, scope, review.Actor{ID: input.ActorID.String()}, input.CaseID.String(), "review.case.child_reevaluation_requested", input.At, input.CaseVersion+1); err != nil {
				return err
			}
			result = review.RecaptureReevaluation{CaseID: input.CaseID, SourceVersion: input.CaseVersion, TargetVersion: input.CaseVersion + 1, DecisionID: input.DecisionID, ActorID: input.ActorID, ReviewerID: input.ReviewerID, RecordedAt: input.At}
		}
		if _, exists := reservation.Result(); exists {
			return nil
		}
		encoded, err := json.Marshal(struct {
			CaseID     string `json:"case_id"`
			Version    int64  `json:"case_version"`
			DecisionID string `json:"decision_id"`
		}{result.CaseID.String(), result.TargetVersion, result.DecisionID.String()})
		if err != nil {
			return err
		}
		replay, err := idempotency.NewResult(202, encoded)
		if err != nil {
			return err
		}
		return idempg.Complete(ctx, q, input.Idempotency, replay, input.At)
	})
	return result, err
}

func findRecaptureReevaluation(ctx context.Context, tx pg.Transaction, scope tenant.Scope, caseID id.ReviewCase, version int64) (review.RecaptureReevaluation, error) {
	result := review.RecaptureReevaluation{CaseID: caseID, SourceVersion: version}
	var decision, actor string
	err := tx.QueryRow(ctx, `SELECT target_version,decision_id,actor_key_id,reviewer_id,recorded_at FROM idenqa.review_recapture_evaluation_requests WHERE tenant_id=$1 AND case_id=$2 AND source_version=$3`, scope.ID().String(), caseID.String(), version).Scan(&result.TargetVersion, &decision, &actor, &result.ReviewerID, &result.RecordedAt)
	if err != nil {
		return result, err
	}
	result.RecordedAt = result.RecordedAt.UTC()
	result.DecisionID, err = id.ParseDecision(decision)
	if err != nil {
		return result, err
	}
	result.ActorID, err = id.ParseAPIKey(actor)
	return result, err
}
