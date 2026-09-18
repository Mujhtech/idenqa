package postgres

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/jackc/pgx/v5"
)

// RecaptureStore composes child creation and immutable review lineage in one transaction.
type RecaptureStore struct {
	*Store
	sessions *verificationpostgres.SessionStore
}

// NewRecaptureStore joins the owned review and session adapters.
func NewRecaptureStore(pool transactionRunner, wrapper platformcrypto.KeyWrapper, catalog evidence.Catalog, source clock.Clock) (*RecaptureStore, error) {
	cases, err := NewWithClock(pool, wrapper, source)
	if err != nil {
		return nil, err
	}
	sessions, err := verificationpostgres.NewSessionStore(pool, wrapper, catalog)
	if err != nil {
		return nil, err
	}
	return &RecaptureStore{cases, sessions}, nil
}

// CreateRecapture restores an existing child before checking mutable parent eligibility.
func (store *RecaptureStore) CreateRecapture(ctx context.Context, scope tenant.Scope, caseID id.ReviewCase, version int64, mutation verification.SessionCreateMutation) (verification.SessionCreation, error) {
	var result verification.SessionCreation
	if caseID.IsZero() || version < 1 || mutation.Idempotency.TenantID() != scope.ID() || mutation.Idempotency.Principal().String() != mutation.Actor.String() || mutation.Idempotency.Operation() != review.OperationRecapture {
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
		if err := store.checkAuthority(ctx, tx, scope, review.Actor{ID: mutation.Actor.String()}, currentCase, review.PermissionFind); err != nil {
			return err
		}

		q := sqlgen.New(tx)
		reservation, err := idempg.Reserve(ctx, q, mutation.Idempotency)
		if err != nil {
			return err
		}
		// Durable case-version uniqueness survives idempotency retention and key rotation.
		var childValue, tokenValue string
		err = tx.QueryRow(ctx, `SELECT child_verification_id,capture_token_id FROM idenqa.review_recaptures WHERE tenant_id=$1 AND case_id=$2 AND case_version=$3`, scope.ID().String(), caseID.String(), version).Scan(&childValue, &tokenValue)
		if err == nil {
			child, err := id.ParseVerification(childValue)
			if err != nil {
				return err
			}
			token, err := id.ParseCaptureToken(tokenValue)
			if err != nil {
				return err
			}
			result, err = store.sessions.RestoreCreationWithin(ctx, scope, tx, child, token)
			if err != nil {
				return err
			}
		} else {
			if !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if _, exists := reservation.Result(); exists {
				return review.ErrConflict
			}
			value, err := store.findCaseWithin(ctx, scope, tx, caseID)
			if err != nil {
				return err
			}
			if value.Version != version || mutation.CreatedAt.Before(value.UpdatedAt) {
				return review.ErrConflict
			}
			if value.State != review.CaseResolved {
				return review.ErrConflict
			}
			if err := validateCaseProcessing(ctx, tx, scope, value, mutation.CreatedAt, store.clock); err != nil {
				return err
			}
			var sc, sd, ec, ed string
			err = tx.QueryRow(ctx, `SELECT s.canonical,e.snapshot_digest,p.canonical,e.evaluation_digest FROM idenqa.review_evaluations e JOIN idenqa.policy_snapshots s ON s.tenant_id=e.tenant_id AND s.snapshot_digest=e.snapshot_digest JOIN idenqa.policy_evaluations p ON p.tenant_id=e.tenant_id AND p.evaluation_digest=e.evaluation_digest WHERE e.tenant_id=$1 AND e.case_id=$2 AND e.case_version=$3`, scope.ID().String(), caseID.String(), version).Scan(&sc, &sd, &ec, &ed)
			if errors.Is(err, pgx.ErrNoRows) {
				return review.ErrConflict
			}
			if err != nil {
				return err
			}
			snapshot, err := policy.RestoreSnapshotCanonical([]byte(sc), sd)
			if err != nil {
				return err
			}
			evaluation, err := policy.RestoreEvaluationCanonical(snapshot, []byte(ec), ed)
			if err != nil {
				return err
			}
			if evaluation.Selected() != policy.DirectiveRequestInput {
				return review.ErrForbidden
			}
			var profileValue string
			if err := tx.QueryRow(ctx, `SELECT source_profile_id FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), value.VerificationID.String()).Scan(&profileValue); err != nil {
				return err
			}
			mutation.ProfileID, err = id.ParseProfile(profileValue)
			if err != nil {
				return err
			}
			mutation.PolicyID = snapshot.Policy().ID
			mutation.Region = value.Region
			result, err = store.sessions.CreateRecaptureWithin(ctx, scope, tx, value.VerificationID, mutation)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_recaptures(tenant_id,case_id,case_version,parent_verification_id,child_verification_id,capture_token_id,policy_snapshot_digest,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, scope.ID().String(), caseID.String(), version, value.VerificationID.String(), result.Session.ID().String(), result.Credential.ID().String(), sd, mutation.CreatedAt)
			if err != nil {
				return err
			}
			if err := appendReviewAudit(ctx, tx, scope, review.Actor{ID: mutation.Actor.String()}, caseID.String(), "review.case.recapture_created", mutation.CreatedAt, version); err != nil {
				return err
			}
		}
		if _, exists := reservation.Result(); exists {
			return nil
		}
		encoded, err := json.Marshal(struct {
			Child string `json:"verification_id"`
		}{result.Session.ID().String()})
		if err != nil {
			return err
		}
		replay, err := idempotency.NewResult(201, encoded)
		if err != nil {
			return err
		}
		return idempg.Complete(ctx, q, mutation.Idempotency, replay, mutation.CreatedAt)
	})
	return result, err
}
