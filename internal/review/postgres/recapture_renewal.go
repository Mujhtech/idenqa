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
	"github.com/Mujhtech/idenqa/internal/verification"
	"github.com/jackc/pgx/v5"
)

// RenewRecapture atomically commits token replacement, review audit and exact replay.
func (store *RecaptureStore) RenewRecapture(ctx context.Context, scope tenant.Scope, caseID id.ReviewCase, version int64, input verification.CaptureRenewal) (verification.SessionCreation, error) {
	var result verification.SessionCreation
	if caseID.IsZero() || version < 1 || input.Idempotency.TenantID() != scope.ID() || input.Idempotency.Principal().String() != input.Actor.String() || input.Idempotency.Operation() != review.OperationRecaptureRenew || input.At.After(store.clock.Now().UTC()) {
		return result, review.ErrInvalid
	}
	err := store.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		currentCase, err := store.findCaseWithin(ctx, scope, tx, caseID)
		if err != nil {
			return err
		}
		if err := store.checkAuthority(ctx, tx, scope, review.Actor{ID: input.Actor.String()}, currentCase, review.PermissionFind); err != nil {
			return err
		}

		q := sqlgen.New(tx)
		reserved, err := idempg.Reserve(ctx, q, input.Idempotency)
		if err != nil {
			return err
		}
		var childValue string
		err = tx.QueryRow(ctx, `SELECT child_verification_id FROM idenqa.review_recaptures WHERE tenant_id=$1 AND case_id=$2 AND case_version=$3`, scope.ID().String(), caseID.String(), version).Scan(&childValue)
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
		if replay, ok := reserved.Result(); ok {
			var saved struct {
				Token string `json:"capture_token_id"`
			}
			if err := json.Unmarshal(replay.Body(), &saved); err != nil {
				return err
			}
			token, err := id.ParseCaptureToken(saved.Token)
			if err != nil {
				return err
			}
			result, err = store.sessions.RestoreCreationWithin(ctx, scope, tx, child, token)
			return err
		}
		current, err := store.findCaseWithin(ctx, scope, tx, caseID)
		if err != nil {
			return err
		}
		if current.Version != version {
			return review.ErrConflict
		}
		result, err = store.sessions.RenewCaptureWithin(ctx, scope, tx, child, input, store.clock)
		if err != nil {
			return err
		}
		if err := appendReviewAudit(ctx, tx, scope, review.Actor{ID: input.Actor.String()}, caseID.String(), "review.case.capture_renewed", input.At, version, input.Token.String()); err != nil {
			return err
		}
		encoded, err := json.Marshal(struct {
			Token string `json:"capture_token_id"`
		}{result.Credential.ID().String()})
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
