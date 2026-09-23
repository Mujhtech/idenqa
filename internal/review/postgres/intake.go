package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// OpenCorrection opens one versioned case per eligible challenged decision.
func (s *FollowupStore) OpenCorrection(ctx context.Context, scope tenant.Scope, decisionID id.Decision, actor review.Actor, retry idempotency.Request) (review.FollowupResult, error) {
	return s.openCorrection(ctx, scope, id.Verification{}, decisionID, actor, retry, "reviews.correction.intake")
}

// OpenReconsideration binds the challenged decision to the verification in the public route.
func (s *FollowupStore) OpenReconsideration(ctx context.Context, scope tenant.Scope, verificationID id.Verification, decisionID id.Decision, actor review.Actor, retry idempotency.Request) (review.FollowupResult, error) {
	if verificationID.IsZero() {
		return review.FollowupResult{}, review.ErrInvalid
	}
	return s.openCorrection(ctx, scope, verificationID, decisionID, actor, retry, "verifications.reconsider")
}

func (s *FollowupStore) openCorrection(ctx context.Context, scope tenant.Scope, verificationID id.Verification, decisionID id.Decision, actor review.Actor, retry idempotency.Request, operation string) (review.FollowupResult, error) {
	var result review.FollowupResult
	if decisionID.IsZero() || retry.Operation() != operation || retry.TenantID() != scope.ID() || retry.Principal().String() != actor.ID {
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
		if replay, ok := reservation.Result(); ok {
			return json.Unmarshal(replay.Body(), &result)
		}
		var caseValue string
		err = tx.QueryRow(ctx, `SELECT case_id FROM idenqa.review_correction_intakes WHERE tenant_id=$1 AND decision_id=$2`, scope.ID().String(), decisionID.String()).Scan(&caseValue)
		if err == nil {
			identifier, err := id.ParseReviewCase(caseValue)
			if err != nil {
				return err
			}
			value, err := s.findCaseWithin(ctx, scope, tx, identifier)
			if err != nil {
				return err
			}
			if !verificationID.IsZero() && value.VerificationID != verificationID {
				return review.ErrInvalid
			}
			result = review.FollowupResult{CaseID: caseValue, Version: value.Version}
		} else {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			decision, err := s.policies.FindWithin(ctx, scope, tx, decisionID)
			if err != nil {
				return err
			}
			if !verificationID.IsZero() && decision.Snapshot().VerificationID() != verificationID {
				return review.ErrInvalid
			}
			settings, err := findPolicySettings(ctx, tx, scope, decision.Snapshot())
			if err != nil {
				return err
			}
			if settings == nil {
				return review.ErrForbidden
			}
			now := s.clock.Now().UTC().Truncate(time.Microsecond)
			if !now.Before(decision.DecidedAt().Add(time.Duration(settings.AppealWindowSeconds) * time.Second)) {
				return review.ErrConflict
			}
			var eligible bool
			if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_sessions s WHERE s.tenant_id=$1 AND s.id=$2 AND s.state='completed') AND NOT EXISTS(SELECT 1 FROM idenqa.verification_decisions WHERE tenant_id=$1 AND verification_id=$2 AND supersedes_id=$3)`, scope.ID().String(), decision.Snapshot().VerificationID().String(), decisionID.String()).Scan(&eligible); err != nil {
				return err
			}
			if !eligible {
				return review.ErrConflict
			}
			identifier, err := s.ids.NewReviewCase()
			if err != nil {
				return err
			}
			value, err := review.NewCase(identifier, decision.Snapshot().VerificationID(), decisionID, decision.Snapshot().Region(), settings.RequiredCertificate, settings.Oversight, now)
			if err != nil {
				return err
			}
			value.PermittedFindings = settings.PermittedFindings
			if err := s.CreateCaseWithin(ctx, scope, tx, actor, value); err != nil {
				return err
			}
			if err := pinCaseSettings(ctx, tx, scope, value, *settings, now); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_correction_intakes(tenant_id,decision_id,case_id,recorded_at) VALUES($1,$2,$3,$4)`, scope.ID().String(), decisionID.String(), identifier.String(), now)
			if err != nil {
				return err
			}
			result = review.FollowupResult{CaseID: identifier.String(), Version: 1}
		}
		body, err := json.Marshal(result)
		if err != nil {
			return err
		}
		replay, err := idempotency.NewResult(201, body)
		if err != nil {
			return err
		}
		return idempg.Complete(ctx, q, retry, replay, s.clock.Now().UTC())
	})
	return result, err
}
