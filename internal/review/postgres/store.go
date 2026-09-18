// Package postgres adapts review application ports to PostgreSQL.
package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	deliverypostgres "github.com/Mujhtech/idenqa/internal/delivery/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
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
type Store struct {
	authority review.Authority
	pool      transactionRunner
	clock     clock.Clock
	wrapper   platformcrypto.KeyWrapper
}

// New constructs the review PostgreSQL adapter.
func New(pool transactionRunner, wrapper platformcrypto.KeyWrapper) (*Store, error) {
	if pool == nil {
		return nil, errors.New("review postgres: pool is required")
	}
	return &Store{pool: pool, clock: clock.System{}, wrapper: wrapper}, nil
}

// NewWithClock supplies an explicit current-authority clock for review writes.
func NewWithClock(pool transactionRunner, wrapper platformcrypto.KeyWrapper, source clock.Clock) (*Store, error) {
	store, err := New(pool, wrapper)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, review.ErrInvalid
	}
	store.clock = source
	return store, nil
}

// CreateCase persists a newly opened review case and its audit record atomically.
func (store *Store) CreateCase(ctx context.Context, scope tenant.Scope, actor review.Actor, value review.Case) error {
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{Isolation: platformpostgres.IsolationSerializable}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		return store.CreateCaseWithin(ctx, scope, tx, actor, value)
	})
}

// CreateCaseWithin joins the routing effect so case, audit and lifecycle commit together.
func (store *Store) CreateCaseWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, actor review.Actor, value review.Case) error {
	if tx == nil || actor.ID == "" || value.Validate() != nil {
		return review.ErrInvalid
	}
	if err := setScope(ctx, tx, scope); err != nil {
		return err
	}
	rules, err := json.Marshal(value.PermittedFindings)
	if err != nil {
		return err
	}
	if value.PermittedFindings == nil {
		rules = []byte("[]")
	}
	_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_cases
 (tenant_id,id,verification_id,challenged_decision_id,routing_request_id,region,required_certification,oversight,state,version,created_at,updated_at,permitted_findings)
 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`, scope.ID().String(), value.ID.String(), value.VerificationID.String(), nullableID(value.ChallengedDecision.String()), nullableID(value.RoutingRequest.String()), value.Region, value.RequiredCertificate, string(value.Oversight), string(value.State), value.Version, value.CreatedAt, value.UpdatedAt, rules)
	if err != nil {
		return err
	}
	if err := appendReviewAudit(ctx, tx, scope, actor, value.ID.String(), "review.case.opened", value.UpdatedAt, value.Version); err != nil {
		return err
	}

	return deliverypostgres.EmitCatalogueEvent(ctx, tx, store.wrapper, scope.ID().String(), value.Region, webhookv1.CaseCreated,
		"case.created:"+value.ID.String()+":"+strconv.FormatInt(value.Version, 10), value.UpdatedAt,
		map[string]any{"case_id": value.ID.String(), "verification_id": value.VerificationID.String(),
			"case": map[string]any{"id": value.ID.String(), "type": "review_case", "verification_id": value.VerificationID.String(), "state": string(value.State), "required_certification": value.RequiredCertificate}})
}

// FindCase restores a tenant-scoped review case and its immutable findings.
func (store *Store) FindCase(ctx context.Context, scope tenant.Scope, identifier id.ReviewCase) (review.Case, error) {
	var result review.Case
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var err error
		result, err = store.findCaseWithin(ctx, scope, tx, identifier)
		return err

	})
	return result, err
}

func (store *Store) findCaseWithin(ctx context.Context, scope tenant.Scope, tx platformpostgres.Transaction, identifier id.ReviewCase) (review.Case, error) {
	var result review.Case
	err := func() error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		var verificationID, challengedID, supersedingID, routingID *string
		var assignedReviewer *string
		var findingRules []byte
		var oversight, state string
		err := tx.QueryRow(ctx, `SELECT verification_id,challenged_decision_id,superseding_decision_id,routing_request_id,region,required_certification,
		oversight,state,assigned_reviewer,version,created_at,updated_at,permitted_findings FROM idenqa.review_cases WHERE tenant_id=$1 AND id=$2`,
			scope.ID().String(), identifier.String()).Scan(&verificationID, &challengedID, &supersedingID, &routingID, &result.Region, &result.RequiredCertificate,
			&oversight, &state, &assignedReviewer, &result.Version, &result.CreatedAt, &result.UpdatedAt, &findingRules)
		if errors.Is(err, pgx.ErrNoRows) {
			return review.ErrInvalid
		}
		if err != nil || verificationID == nil {
			return errors.Join(review.ErrInvalid, err)
		}
		if err := json.Unmarshal(findingRules, &result.PermittedFindings); err != nil {
			return review.ErrInvalid
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
		if challengedID != nil {
			result.ChallengedDecision, err = id.ParseDecision(*challengedID)
			if err != nil {
				return review.ErrInvalid
			}
		}
		if routingID != nil {
			result.RoutingRequest, err = id.ParseDecision(*routingID)
			if err != nil {
				return review.ErrInvalid
			}
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
	}()
	return result, err
}

// SaveCase applies an optimistic case transition and optional immutable finding.
func (store *Store) SaveCase(ctx context.Context, scope tenant.Scope, actor review.Actor, value review.Case, expectedVersion int64, finding *review.Finding) error {
	if value.Validate() != nil || value.Version != expectedVersion+1 {
		return review.ErrInvalid
	}
	return store.mutate(ctx, scope, actor, value.ID.String(), "review.case."+string(value.State), value.UpdatedAt, value.Version, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if !value.ChallengedDecision.IsZero() {
			principal, err := resolveWithin(ctx, tx, store.authority, scope, actor, value.Region, store.clock.Now().UTC())
			if store.authority != nil {
				if err != nil {
					return err
				}
				if err := independentOfDecision(ctx, tx, scope, value.ChallengedDecision, principal.ID); err != nil {
					return err
				}
			}
		}
		permission := review.PermissionClaim
		if finding != nil {
			permission = review.PermissionFind
		} else if !value.SupersedesDecision.IsZero() {
			permission = review.PermissionResolve
		}
		if err := store.checkAuthority(ctx, tx, scope, actor, value, permission); err != nil {
			return err
		}
		if !value.SupersedesDecision.IsZero() {
			if err := validateSuccessor(ctx, tx, scope, value.VerificationID, value.ChallengedDecision, value.SupersedesDecision); err != nil {
				return err
			}
		}

		if finding != nil {
			if err := store.validateFindingWithin(ctx, scope, tx, value, *finding); err != nil {
				return err
			}
		}
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
		if finding != nil && !value.RoutingRequest.IsZero() && value.State == review.CaseResolved {
			if _, _, err := value.AcceptedFact(); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, `INSERT INTO idenqa.review_evaluation_requests(tenant_id,case_id,case_version,created_at) VALUES($1,$2,$3,$4)`, scope.ID().String(), value.ID.String(), value.Version, value.UpdatedAt)
			if err != nil {
				return err
			}
		}
		eventType := webhookv1.Type("")
		fields := map[string]any{"case_id": value.ID.String(), "verification_id": value.VerificationID.String()}
		if finding != nil {
			eventType = webhookv1.CaseFindingRecorded
			fields["finding_id"] = finding.ID.String()
			fields["resolution"] = string(finding.Resolution)
			fields["reason_code"] = finding.ReasonCode
		} else if value.AssignedReviewer != "" && value.SupersedesDecision.IsZero() {
			eventType = webhookv1.CaseAssigned
			fields["operator_id"] = value.AssignedReviewer
		}
		name := "case"
		fields[name] = map[string]any{"id": value.ID.String(), "type": "review_case", "verification_id": value.VerificationID.String(), "state": string(value.State)}
		if value.AssignedReviewer != "" {
			fields[name].(map[string]any)["assigned_reviewer"] = value.AssignedReviewer
		}
		if finding != nil {
			fields[name].(map[string]any)["finding_id"] = finding.ID.String()
			fields[name].(map[string]any)["resolution"] = string(finding.Resolution)
			fields[name].(map[string]any)["reason_code"] = finding.ReasonCode
		}
		if eventType != "" {
			seed := string(eventType) + ":" + value.ID.String() + ":" + strconv.FormatInt(value.Version, 10)
			if err := deliverypostgres.EmitCatalogueEvent(ctx, tx, store.wrapper, scope.ID().String(), value.Region, eventType, seed, value.UpdatedAt, fields); err != nil {
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
			err := tx.QueryRow(ctx, `SELECT g.region,g.expires_at,g.revoked_at FROM idenqa.evidence_processing_grants g
 JOIN idenqa.review_evidence_access b ON b.tenant_id=g.tenant_id AND b.grant_id=g.id
 WHERE g.tenant_id=$1 AND g.id=$2 AND b.reviewer_id=$3`, scope.ID().String(), identifier.String(), reviewerID).Scan(&grant.Region, &grant.ExpiresAt, &revoked)
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
		var encoded []byte
		if err := tx.QueryRow(ctx, `SELECT configuration FROM idenqa.review_case_settings WHERE tenant_id=$1 AND case_id=$2`, scope.ID().String(), value.CaseID.String()).Scan(&encoded); err != nil {
			return err
		}
		var settings review.PolicySettings
		if json.Unmarshal(encoded, &settings) != nil || settings.Validate() != nil {
			return review.ErrForbidden
		}
		var decidedAt time.Time
		if err := tx.QueryRow(ctx, `SELECT decided_at FROM idenqa.verification_decisions WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), value.ChallengedDecision.String()).Scan(&decidedAt); err != nil {
			return err
		}
		var superseded bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.verification_decisions WHERE tenant_id=$1 AND supersedes_id=$2)`, scope.ID().String(), value.ChallengedDecision.String()).Scan(&superseded); err != nil {
			return err
		}
		if superseded {
			return review.ErrConflict
		}
		if value.Deadline.After(decidedAt.Add(time.Duration(settings.AppealWindowSeconds) * time.Second)) {
			return review.ErrInvalid
		}

		_, err := tx.Exec(ctx, `INSERT INTO idenqa.appeals
			(tenant_id,id,case_id,challenged_decision_id,original_reviewers,state,deadline,version,created_at,updated_at,requested_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$9,$10)`, scope.ID().String(), value.ID.String(), value.CaseID.String(),
			value.ChallengedDecision.String(), reviewers, string(value.State), value.Deadline, value.Version, now, actor.ID)
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
		related, err := store.findCaseWithin(ctx, scope, tx, value.CaseID)
		if err != nil {
			return err
		}
		if store.authority != nil {
			principal, err := resolveWithin(ctx, tx, store.authority, scope, actor, related.Region, store.clock.Now().UTC())
			if err != nil {
				return err
			}
			if err := independentOfDecision(ctx, tx, scope, value.ChallengedDecision, principal.ID); err != nil {
				return err
			}
		}

		if err := independentOfCase(ctx, tx, scope, related, value.AssignedReviewer); err != nil {
			return err
		}
		if !store.clock.Now().Before(value.Deadline) {
			return review.ErrConflict
		}
		if value.Outcome != review.AppealOverturned {
			if err := validateCaseProcessing(ctx, tx, scope, related, now, store.clock); err != nil {
				return err
			}
		}
		if err := store.checkAuthority(ctx, tx, scope, actor, related, review.PermissionAppeal); err != nil {
			return err
		}
		if !value.SupersedingDecision.IsZero() {
			if err := validateSuccessor(ctx, tx, scope, related.VerificationID, value.ChallengedDecision, value.SupersedingDecision); err != nil {
				return err
			}
		}

		tag, err := tx.Exec(ctx, `UPDATE idenqa.appeals SET assigned_reviewer=$3,state=$4,outcome=$5,reason_code=$6,
			superseding_decision_id=$7,version=$8,updated_at=$9 WHERE tenant_id=$1 AND id=$2 AND version=$10`, scope.ID().String(), value.ID.String(),
			nullable(value.AssignedReviewer), string(value.State), nullable(string(value.Outcome)), nullable(value.ReasonCode),
			nullableID(value.SupersedingDecision.String()), value.Version, now, expectedVersion)
		if err != nil || tag.RowsAffected() != 1 {
			return errors.Join(review.ErrConflict, err)
		}

		fields := map[string]any{"appeal_id": value.ID.String(), "case_id": value.CaseID.String(), "state": string(value.State),
			"appeal": map[string]any{"id": value.ID.String(), "type": "appeal", "case_id": value.CaseID.String(), "state": string(value.State)}}
		if value.Outcome != "" {
			fields["outcome"] = string(value.Outcome)
			fields["appeal"].(map[string]any)["outcome"] = string(value.Outcome)
		}
		return deliverypostgres.EmitCatalogueEvent(ctx, tx, store.wrapper, scope.ID().String(), related.Region, webhookv1.AppealUpdated,
			"appeal.updated:"+value.ID.String()+":"+strconv.FormatInt(value.Version, 10), now, fields)
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
		return appendReviewAudit(ctx, tx, scope, actor, aggregateID, eventType, at, version)
	})
}

func appendReviewAudit(ctx context.Context, tx platformpostgres.Transaction, scope tenant.Scope, actor review.Actor, aggregateID, eventType string, at time.Time, version int64, operationIDs ...string) error {
	identity := fmt.Sprintf("%s:%s:%d", eventType, aggregateID, version)
	if len(operationIDs) > 0 {
		identity += ":" + operationIDs[0]
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s\n%s\n%d\n", eventType, aggregateID, version)))
	_, err := auditpostgres.AppendInTransaction(ctx, tx, scope, auditpostgres.Event{
		EventID: token("event", identity), EventType: eventType,
		AggregateID: token("review", aggregateID), ActorID: token("actor", actor.ID), EventDigest: hex.EncodeToString(digest[:]), OccurredAt: at,
	})
	return err
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
