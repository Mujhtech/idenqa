// Package postgres adapts review application ports to PostgreSQL.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type transactionRunner interface {
	WithinTransaction(context.Context, platformpostgres.TransactionOptions, func(context.Context, platformpostgres.Transaction) error) error
}

// Store persists tenant-scoped review aggregates with atomic audit records.
type Store struct{ pool transactionRunner }

// New constructs the review PostgreSQL adapter.
func New(pool transactionRunner) (*Store, error) {
	if pool == nil {
		return nil, errors.New("review postgres: pool is required")
	}
	return &Store{pool: pool}, nil
}

// CreateCase persists a newly opened review case and its audit record atomically.
func (store *Store) CreateCase(ctx context.Context, scope tenant.Scope, actor review.Actor, value review.Case) error {
	return store.mutate(ctx, scope, actor, value.ID.String(), "review.case.opened", value.UpdatedAt, value.Version, func(ctx context.Context, tx platformpostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.review_cases
			(tenant_id,id,verification_id,challenged_decision_id,region,required_certification,oversight,state,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, scope.ID().String(), value.ID.String(), value.VerificationID.String(),
			value.ChallengedDecision.String(), value.Region, value.RequiredCertificate, string(value.Oversight), string(value.State), value.Version, value.CreatedAt, value.UpdatedAt)
		return err
	})
}

// FindCase restores a tenant-scoped review case and its immutable findings.
func (store *Store) FindCase(ctx context.Context, scope tenant.Scope, identifier id.ReviewCase) (review.Case, error) {
	var result review.Case
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		var verificationID, challengedID, supersedingID *string
		var assignedReviewer *string
		var oversight, state string
		err := tx.QueryRow(ctx, `SELECT verification_id,challenged_decision_id,superseding_decision_id,region,required_certification,
			oversight,state,assigned_reviewer,version,created_at,updated_at FROM idenqa.review_cases WHERE tenant_id=$1 AND id=$2`,
			scope.ID().String(), identifier.String()).Scan(&verificationID, &challengedID, &supersedingID, &result.Region, &result.RequiredCertificate,
			&oversight, &state, &assignedReviewer, &result.Version, &result.CreatedAt, &result.UpdatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return review.ErrInvalid
		}
		if err != nil || verificationID == nil || challengedID == nil {
			return errors.Join(review.ErrInvalid, err)
		}
		result.ID = identifier
		result.CreatedAt = result.CreatedAt.UTC()
		result.UpdatedAt = result.UpdatedAt.UTC()
		if assignedReviewer != nil {
			result.AssignedReviewer = *assignedReviewer
		}
		result.VerificationID, err = id.ParseVerification(*verificationID)
		if err != nil {
			return review.ErrInvalid
		}
		result.ChallengedDecision, err = id.ParseDecision(*challengedID)
		if err != nil {
			return review.ErrInvalid
		}
		if supersedingID != nil {
			result.SupersedesDecision, err = id.ParseDecision(*supersedingID)
			if err != nil {
				return review.ErrInvalid
			}
		}
		result.Oversight, result.State = review.Oversight(oversight), review.CaseState(state)
		rows, err := tx.Query(ctx, `SELECT id,reviewer_id,resolution,reason_code,evidence_grant_ids,recorded_at
			FROM idenqa.review_findings WHERE tenant_id=$1 AND case_id=$2 ORDER BY recorded_at,id`, scope.ID().String(), identifier.String())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var finding review.Finding
			var findingID, resolution string
			var encoded []byte
			if err := rows.Scan(&findingID, &finding.ReviewerID, &resolution, &finding.ReasonCode, &encoded, &finding.RecordedAt); err != nil {
				return err
			}
			finding.ID, err = id.ParseFinding(findingID)
			if err != nil {
				return review.ErrInvalid
			}
			finding.Resolution = review.Resolution(resolution)
			finding.RecordedAt = finding.RecordedAt.UTC()
			var grants []string
			if err := json.Unmarshal(encoded, &grants); err != nil {
				return review.ErrInvalid
			}
			for _, grant := range grants {
				parsed, err := id.ParseGrant(grant)
				if err != nil {
					return review.ErrInvalid
				}
				finding.GrantIDs = append(finding.GrantIDs, parsed)
			}
			result.Findings = append(result.Findings, finding)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return result.Validate()
	})
	return result, err
}

// SaveCase applies an optimistic case transition and optional immutable finding.
func (store *Store) SaveCase(ctx context.Context, scope tenant.Scope, actor review.Actor, value review.Case, expectedVersion int64, finding *review.Finding) error {
	if value.Validate() != nil || value.Version != expectedVersion+1 {
		return review.ErrInvalid
	}
	return store.mutate(ctx, scope, actor, value.ID.String(), "review.case."+string(value.State), value.UpdatedAt, value.Version, func(ctx context.Context, tx platformpostgres.Transaction) error {
		tag, err := tx.Exec(ctx, `UPDATE idenqa.review_cases SET superseding_decision_id=$3,state=$4,assigned_reviewer=$5,version=$6,updated_at=$7
			WHERE tenant_id=$1 AND id=$2 AND version=$8`, scope.ID().String(), value.ID.String(), nullableID(value.SupersedesDecision.String()),
			string(value.State), nullable(value.AssignedReviewer), value.Version, value.UpdatedAt, expectedVersion)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.Join(review.ErrConflict, err)
		}
		if finding != nil {
			grants := make([]string, len(finding.GrantIDs))
			for index, grant := range finding.GrantIDs {
				grants[index] = grant.String()
			}
			encoded, err := json.Marshal(grants)
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_findings
				(tenant_id,case_id,id,reviewer_id,resolution,reason_code,evidence_grant_ids,recorded_at)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, scope.ID().String(), value.ID.String(), finding.ID.String(), finding.ReviewerID,
				string(finding.Resolution), finding.ReasonCode, encoded, finding.RecordedAt)
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// FindGrants resolves active, reviewer-bound evidence grant metadata.
func (store *Store) FindGrants(ctx context.Context, scope tenant.Scope, reviewerID, region string, identifiers []id.Grant, at time.Time) ([]review.EvidenceGrant, error) {
	if len(identifiers) == 0 || len(identifiers) > 32 {
		return nil, review.ErrForbidden
	}
	result := make([]review.EvidenceGrant, 0, len(identifiers))
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		for _, identifier := range identifiers {
			var grant review.EvidenceGrant
			var revoked *time.Time
			err := tx.QueryRow(ctx, `SELECT region,expires_at,revoked_at FROM idenqa.evidence_processing_grants
				WHERE tenant_id=$1 AND id=$2 AND recipient_reference=$3`, scope.ID().String(), identifier.String(), reviewerID).Scan(&grant.Region, &grant.ExpiresAt, &revoked)
			if errors.Is(err, pgx.ErrNoRows) || err == nil && grant.Region != region {
				return review.ErrForbidden
			}
			if err != nil {
				return err
			}
			grant.ID, grant.ReviewerID = identifier, reviewerID
			grant.ExpiresAt = grant.ExpiresAt.UTC()
			if revoked != nil {
				grant.RevokedAt = revoked.UTC()
			}
			if !grant.ExpiresAt.After(at) || !grant.RevokedAt.IsZero() {
				return review.ErrForbidden
			}
			result = append(result, grant)
		}
		return nil
	})
	return result, err
}

// CreateAppeal persists a requested appeal and its audit record atomically.
func (store *Store) CreateAppeal(ctx context.Context, scope tenant.Scope, actor review.Actor, value review.Appeal, now time.Time) error {
	reviewers, err := json.Marshal(value.OriginalReviewers)
	if err != nil {
		return review.ErrInvalid
	}
	return store.mutate(ctx, scope, actor, value.ID.String(), "review.appeal.requested", now, value.Version, func(ctx context.Context, tx platformpostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.appeals
			(tenant_id,id,case_id,challenged_decision_id,original_reviewers,state,deadline,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9)`, scope.ID().String(), value.ID.String(), value.CaseID.String(),
			value.ChallengedDecision.String(), reviewers, string(value.State), value.Deadline, value.Version, now)
		return err
	})
}

// FindAppeal restores a tenant-scoped appeal.
func (store *Store) FindAppeal(ctx context.Context, scope tenant.Scope, identifier id.Appeal) (review.Appeal, error) {
	var result review.Appeal
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		var caseID, decisionID string
		var reviewers []byte
		var state string
		var outcome, reason, successor *string
		var assignedReviewer *string
		err := tx.QueryRow(ctx, `SELECT case_id,challenged_decision_id,original_reviewers,assigned_reviewer,state,outcome,reason_code,
			superseding_decision_id,deadline,version FROM idenqa.appeals WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), identifier.String()).Scan(
			&caseID, &decisionID, &reviewers, &assignedReviewer, &state, &outcome, &reason, &successor, &result.Deadline, &result.Version)
		if errors.Is(err, pgx.ErrNoRows) {
			return review.ErrInvalid
		}
		if err != nil {
			return err
		}
		result.ID, result.State = identifier, review.AppealState(state)
		result.Deadline = result.Deadline.UTC()
		if assignedReviewer != nil {
			result.AssignedReviewer = *assignedReviewer
		}
		result.CaseID, err = id.ParseReviewCase(caseID)
		if err != nil {
			return review.ErrInvalid
		}
		result.ChallengedDecision, err = id.ParseDecision(decisionID)
		if err != nil {
			return review.ErrInvalid
		}
		if err := json.Unmarshal(reviewers, &result.OriginalReviewers); err != nil {
			return review.ErrInvalid
		}
		if outcome != nil {
			result.Outcome = review.AppealOutcome(*outcome)
		}
		if reason != nil {
			result.ReasonCode = *reason
		}
		if successor != nil {
			result.SupersedingDecision, err = id.ParseDecision(*successor)
			if err != nil {
				return review.ErrInvalid
			}
		}
		return nil
	})
	return result, err
}

// SaveAppeal applies an optimistic appeal transition with atomic audit.
func (store *Store) SaveAppeal(ctx context.Context, scope tenant.Scope, actor review.Actor, value review.Appeal, expectedVersion int64, now time.Time) error {
	return store.mutate(ctx, scope, actor, value.ID.String(), "review.appeal."+string(value.State), now, value.Version, func(ctx context.Context, tx platformpostgres.Transaction) error {
		tag, err := tx.Exec(ctx, `UPDATE idenqa.appeals SET assigned_reviewer=$3,state=$4,outcome=$5,reason_code=$6,
			superseding_decision_id=$7,version=$8,updated_at=$9 WHERE tenant_id=$1 AND id=$2 AND version=$10`, scope.ID().String(), value.ID.String(),
			nullable(value.AssignedReviewer), string(value.State), nullable(string(value.Outcome)), nullable(value.ReasonCode),
			nullableID(value.SupersedingDecision.String()), value.Version, now, expectedVersion)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.Join(review.ErrConflict, err)
		}
		return nil
	})
}

func (store *Store) mutate(ctx context.Context, scope tenant.Scope, actor review.Actor, aggregateID, eventType string, at time.Time, version int64, work func(context.Context, platformpostgres.Transaction) error) error {
	if actor.ID == "" || at.IsZero() || at.Location() != time.UTC {
		return review.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		if err := work(ctx, tx); err != nil {
			return err
		}
		digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n", eventType, aggregateID, version)))
		_, err := auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{
			EventID: token("event", fmt.Sprintf("%s:%s:%d", eventType, aggregateID, version)), EventType: eventType,
			AggregateID: token("review", aggregateID), ActorID: token("actor", actor.ID), EventDigest: hex.EncodeToString(digest[:]), OccurredAt: at,
		})
		return err
	})
}

func setScope(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope) error {
	if scope.ID().IsZero() {
		return review.ErrInvalid
	}
	var value string
	return tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&value)
}

func token(prefix, value string) string {
	digest := sha256.Sum256([]byte(value))
	return prefix + ":" + hex.EncodeToString(digest[:12])
}
func nullable(value string) any {
	if value == "" {
		return nil
	}
	return value
}
func nullableID(value string) any { return nullable(value) }

var _ review.Repository = (*Store)(nil)
