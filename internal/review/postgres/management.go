package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Mujhtech/idenqa/internal/platform/id"

	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idempg "github.com/Mujhtech/idenqa/internal/platform/idempotency/postgres"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// Administer commits configuration, history, audit and idempotency atomically.
func (s *Store) Administer(ctx context.Context, scope tenant.Scope, input review.Administration) (review.AdministrationRecord, error) {
	var result review.AdministrationRecord
	if input.ExpectedVersion < 0 || input.ExpectedVersion == 9223372036854775807 || input.Retry.TenantID() != scope.ID() || input.Retry.Principal().String() != input.Actor.ID || input.Retry.Operation() != "reviews.admin."+input.Kind {
		return result, review.ErrInvalid
	}
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationSerializable}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		q := sqlgen.New(tx)
		reservation, err := idempg.Reserve(ctx, q, input.Retry)
		if err != nil {
			return err
		}
		if replay, ok := reservation.Result(); ok {
			return json.Unmarshal(replay.Body(), &result)
		}
		version := input.ExpectedVersion + 1
		switch input.Kind {
		case "operator":
			var config review.OperatorConfiguration
			if json.Unmarshal(input.Configuration, &config) != nil || config.Assignment.TenantID != scope.ID().String() || config.Assignment.APIKeyID != input.Reference {
				return review.ErrInvalid
			}
			if _, err := review.NewRegistry([]review.Assignment{config.Assignment}); err != nil {
				return err
			}
			encoded, err := json.Marshal(config.Assignment)
			if err != nil {
				return err
			}
			var currentVersion int64
			var operator string
			err = tx.QueryRow(ctx, `SELECT version,operator_id FROM idenqa.review_operator_assignments WHERE tenant_id=$1 AND api_key_id=$2 FOR UPDATE`, scope.ID().String(), input.Reference).Scan(&currentVersion, &operator)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if currentVersion != input.ExpectedVersion || (operator != "" && operator != config.Assignment.OperatorID) {
				return review.ErrConflict
			}
			_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_operator_assignments(tenant_id,api_key_id,operator_id,version,assignment,revoked,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,api_key_id) DO UPDATE SET version=excluded.version,assignment=excluded.assignment,revoked=excluded.revoked,updated_at=excluded.updated_at`, scope.ID().String(), input.Reference, config.Assignment.OperatorID, version, encoded, config.Revoked, input.At)
			if err != nil {
				return err
			}
		case "policy":
			var config review.PolicySettings
			if json.Unmarshal(input.Configuration, &config) != nil || config.Validate() != nil || config.TenantID != scope.ID().String() {
				return review.ErrInvalid
			}
			var digest string
			if err := tx.QueryRow(ctx, `SELECT digest FROM idenqa.policy_revisions WHERE tenant_id=$1 AND policy_id=$2 AND revision=$3`, scope.ID().String(), config.PolicyID, config.Revision).Scan(&digest); errors.Is(err, pgx.ErrNoRows) {
				return review.ErrInvalid
			} else if err != nil {
				return err
			}
			if digest != config.PolicyDigest {
				return review.ErrConflict
			}
			var currentVersion int64
			err := tx.QueryRow(ctx, `SELECT version FROM idenqa.review_policy_settings WHERE tenant_id=$1 AND policy_id=$2 AND policy_revision=$3 FOR UPDATE`, scope.ID().String(), config.PolicyID, config.Revision).Scan(&currentVersion)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if currentVersion != input.ExpectedVersion {
				return review.ErrConflict
			}
			_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_policy_settings(tenant_id,policy_id,policy_revision,policy_digest,version,configuration,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7) ON CONFLICT(tenant_id,policy_id,policy_revision) DO UPDATE SET version=excluded.version,configuration=excluded.configuration,updated_at=excluded.updated_at`, scope.ID().String(), config.PolicyID, config.Revision, config.PolicyDigest, version, input.Configuration, input.At)
			if err != nil {
				return err
			}
		case "queue":
			var config review.QueueConfiguration
			if json.Unmarshal(input.Configuration, &config) != nil || config.Validate() != nil {
				return review.ErrInvalid
			}
			var currentVersion int64
			err := tx.QueryRow(ctx, `SELECT version FROM idenqa.review_case_operations WHERE tenant_id=$1 AND case_id=$2 FOR UPDATE`, scope.ID().String(), input.Reference).Scan(&currentVersion)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			if currentVersion != input.ExpectedVersion {
				return review.ErrConflict
			}
			_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_case_operations(tenant_id,case_id,version,priority,due_at,language,reason,assurance,risk,sampled,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT(tenant_id,case_id) DO UPDATE SET version=excluded.version,priority=excluded.priority,due_at=excluded.due_at,language=excluded.language,reason=excluded.reason,assurance=excluded.assurance,risk=excluded.risk,sampled=excluded.sampled,updated_at=excluded.updated_at`, scope.ID().String(), input.Reference, version, config.Priority, config.DueAt, config.Language, config.Reason, config.Assurance, config.Risk, config.Sampled, input.At)
			if err != nil {
				return err
			}
		case "case-settings":
			var config review.PolicySettings
			if json.Unmarshal(input.Configuration, &config) != nil || config.Validate() != nil || config.TenantID != scope.ID().String() {
				return review.ErrInvalid
			}
			identifier, err := id.ParseReviewCase(input.Reference)
			if err != nil {
				return review.ErrInvalid
			}
			value, err := s.findCaseWithin(ctx, scope, tx, identifier)
			if err != nil {
				return err
			}
			if config.RequiredCertificate != value.RequiredCertificate || config.Oversight != value.Oversight || !slices.Equal(config.PermittedFindings, value.PermittedFindings) {
				return review.ErrConflict
			}
			var matches bool
			err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.policy_routing_receipts r JOIN idenqa.policy_snapshots p ON p.tenant_id=r.tenant_id AND p.snapshot_digest=r.snapshot_digest WHERE r.tenant_id=$1 AND r.request_id=$2 AND p.policy_id=$3 AND p.policy_revision=$4 AND p.policy_digest=$5)`, scope.ID().String(), value.RoutingRequest.String(), config.PolicyID, config.Revision, config.PolicyDigest).Scan(&matches)
			if err != nil {
				return err
			}
			if !matches {
				return review.ErrConflict
			}
			if err := pinCaseSettings(ctx, tx, scope, value, config, input.At); err != nil {
				return err
			}

		default:
			return review.ErrInvalid
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.review_administration_history(tenant_id,kind,reference,version,actor_key_id,configuration,recorded_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, scope.ID().String(), input.Kind, input.Reference, version, input.Actor.ID, input.Configuration, input.At)
		if err != nil {
			return err
		}
		if err := appendReviewAudit(ctx, tx, scope, input.Actor, input.Reference, "review.admin."+input.Kind, input.At, version); err != nil {
			return err
		}
		result = review.AdministrationRecord{Kind: input.Kind, Reference: input.Reference, Version: version, Configuration: input.Configuration}
		encoded, err := json.Marshal(result)
		if err != nil {
			return err
		}
		replay, err := idempotency.NewResult(200, encoded)
		if err != nil {
			return err
		}
		return idempg.Complete(ctx, q, input.Retry, replay, input.At)
	})
	return result, err
}

// ReadAdministration returns the latest tenant-scoped configuration revision.
func (s *Store) ReadAdministration(ctx context.Context, scope tenant.Scope, kind, reference string) (review.AdministrationRecord, error) {
	result := review.AdministrationRecord{Kind: kind, Reference: reference}
	err := s.pool.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		if err := setScope(ctx, tx, scope); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `SELECT version,configuration FROM idenqa.review_administration_history WHERE tenant_id=$1 AND kind=$2 AND reference=$3 ORDER BY version DESC LIMIT 1`, scope.ID().String(), kind, reference).Scan(&result.Version, &result.Configuration)
		if errors.Is(err, pgx.ErrNoRows) {
			return review.ErrConflict
		}
		return err
	})
	return result, err
}
