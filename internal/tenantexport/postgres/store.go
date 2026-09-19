// Package postgres adapts tenant export sources to bounded, tenant-scoped,
// read-only PostgreSQL keyset reads. It composes the existing decision
// repository so exported bundles stay byte-canonical.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/tenantexport"
	"github.com/jackc/pgx/v5"
)

type transactionRunner interface {
	WithinTransaction(
		context.Context,
		platformpostgres.TransactionOptions,
		func(context.Context, platformpostgres.Transaction) error,
	) error
}

// Store implements every tenantexport source through tenant-scoped keyset
// reads. Every statement also filters tenant_id explicitly, so isolation holds
// even where row-level security is not enforced for the connection role.
type Store struct {
	pool      transactionRunner
	decisions policy.Repository
}

// New constructs the read-only tenant export adapter.
func New(pool transactionRunner, decisions policy.Repository) (*Store, error) {
	if pool == nil || decisions == nil {
		return nil, errors.New("tenantexport postgres: pool and decision repository are required")
	}
	return &Store{pool: pool, decisions: decisions}, nil
}

// Tenant reads the single authenticated tenant metadata record.
func (store *Store) Tenant(ctx context.Context, scope tenant.Scope) (tenantexport.Record, error) {
	var record tenantexport.Record
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var value tenantexport.TenantRecord
		var disabledAt *time.Time
		err := tx.QueryRow(ctx, `SELECT id,state,version,created_at,updated_at,disabled_at
			FROM idenqa.tenants WHERE id=$1`, scope.ID().String()).
			Scan(&value.ID, &value.State, &value.Version, &value.CreatedAt, &value.UpdatedAt, &disabledAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return tenant.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("read tenant metadata: %w", err)
		}
		value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
		value.DisabledAt = utc(disabledAt)
		record = tenantexport.Record{Position: value.ID, Fields: value}
		return nil
	})
	return record, err
}

// CaptureProfiles reads capture profiles with their published revision pin.
func (store *Store) CaptureProfiles(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT profiles.id,profiles.name,profiles.state,profiles.version,
				profiles.latest_revision,COALESCE(profiles.draft_revision,0),COALESCE(profiles.published_revision,0),
				COALESCE(revisions.digest,''),profiles.created_at,profiles.updated_at,profiles.deactivated_at
			FROM idenqa.capture_profiles AS profiles
			LEFT JOIN idenqa.capture_profile_revisions AS revisions
			  ON revisions.tenant_id=profiles.tenant_id
			 AND revisions.profile_id=profiles.id
			 AND revisions.revision=profiles.published_revision
			WHERE profiles.tenant_id=$1 AND profiles.id>$2
			ORDER BY profiles.id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list capture profiles: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.CaptureProfileRecord
			var draft, published int32
			var deactivatedAt *time.Time
			if err := rows.Scan(&value.ID, &value.Name, &value.State, &value.Version, &value.LatestRevision,
				&draft, &published, &value.PublishedDigest, &value.CreatedAt, &value.UpdatedAt, &deactivatedAt); err != nil {
				return fmt.Errorf("scan capture profile: %w", err)
			}
			value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
			value.DraftRevision = optionalInt32(draft)
			value.PublishedRevision = optionalInt32(published)
			value.DeactivatedAt = utc(deactivatedAt)
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// Policies reads policy catalogs and activation state.
func (store *Store) Policies(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,activation_version,COALESCE(active_revision,0),created_at,updated_at
			FROM idenqa.policies WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`,
			scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list policies: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.PolicyRecord
			var active int64
			if err := rows.Scan(&value.ID, &value.ActivationVersion, &active, &value.CreatedAt, &value.UpdatedAt); err != nil {
				return fmt.Errorf("scan policy: %w", err)
			}
			value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
			value.ActiveRevision = optionalInt64(active)
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// PolicyRevisions reads immutable revision metadata.
func (store *Store) PolicyRevisions(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	afterPolicy, afterRevision, err := parseTuplePosition(after)
	if err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err = store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT policy_id,revision,schema_major,schema_minor,digest,
				evaluator_major,evaluator_minor,evaluator_digest,created_at
			FROM idenqa.policy_revisions
			WHERE tenant_id=$1 AND (policy_id,revision)>($2,$3)
			ORDER BY policy_id,revision LIMIT $4`, scope.ID().String(), afterPolicy, afterRevision, limit)
		if err != nil {
			return fmt.Errorf("list policy revisions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.PolicyRevisionRecord
			if err := rows.Scan(&value.PolicyID, &value.Revision, &value.SchemaMajor, &value.SchemaMinor,
				&value.Digest, &value.EvaluatorMajor, &value.EvaluatorMinor, &value.EvaluatorDigest,
				&value.CreatedAt); err != nil {
				return fmt.Errorf("scan policy revision: %w", err)
			}
			value.CreatedAt = value.CreatedAt.UTC()
			records = append(records, tenantexport.Record{
				Position: tuplePosition(value.PolicyID, value.Revision), Fields: value,
			})
		}
		return rows.Err()
	})
	return records, err
}

// PolicyActivations reads immutable activation history.
func (store *Store) PolicyActivations(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	afterPolicy, afterVersion, err := parseTuplePosition(after)
	if err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err = store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT policy_id,activation_version,revision,COALESCE(previous_revision,0),
				actor_key_id,activated_at
			FROM idenqa.policy_activations
			WHERE tenant_id=$1 AND (policy_id,activation_version)>($2,$3)
			ORDER BY policy_id,activation_version LIMIT $4`, scope.ID().String(), afterPolicy, afterVersion, limit)
		if err != nil {
			return fmt.Errorf("list policy activations: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.PolicyActivationRecord
			var previous int64
			if err := rows.Scan(&value.PolicyID, &value.ActivationVersion, &value.Revision, &previous,
				&value.ActorID, &value.ActivatedAt); err != nil {
				return fmt.Errorf("scan policy activation: %w", err)
			}
			value.ActivatedAt = value.ActivatedAt.UTC()
			value.PreviousRevision = optionalInt64(previous)
			records = append(records, tenantexport.Record{
				Position: tuplePosition(value.PolicyID, value.ActivationVersion), Fields: value,
			})
		}
		return rows.Err()
	})
	return records, err
}

// Verifications reads verification session snapshots.
func (store *Store) Verifications(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,state,version,source_profile_id,source_profile_revision,
				source_profile_digest,COALESCE(policy_id,''),COALESCE(decision_id,''),COALESCE(region,''),
				requirements,COALESCE(failure_class,''),COALESCE(failure_code,''),
				created_at,updated_at,expires_at,capture_completed_at,COALESCE(completed_decision_id,''),
				expiry_discovered_at
			FROM idenqa.verification_sessions
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list verification sessions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.VerificationRecord
			var requirements []byte
			var captureCompletedAt, expiryDiscoveredAt *time.Time
			if err := rows.Scan(&value.ID, &value.State, &value.Version, &value.ProfileID,
				&value.ProfileRevision, &value.ProfileDigest, &value.PolicyID, &value.DecisionID,
				&value.Region, &requirements, &value.FailureClass, &value.FailureCode,
				&value.CreatedAt, &value.UpdatedAt, &value.ExpiresAt, &captureCompletedAt,
				&value.CompletedDecisionID, &expiryDiscoveredAt); err != nil {
				return fmt.Errorf("scan verification session: %w", err)
			}
			value.CreatedAt, value.UpdatedAt, value.ExpiresAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC(), value.ExpiresAt.UTC()
			value.Requirements = json.RawMessage(requirements)
			value.CaptureCompletedAt = utc(captureCompletedAt)
			value.ExpiryDiscoveredAt = utc(expiryDiscoveredAt)
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// VerificationTransitions reads lifecycle receipts.
func (store *Store) VerificationTransitions(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT event_id,verification_id,from_state,to_state,expected_version,
				resulting_version,COALESCE(decision_id,''),actor_id,command_digest,occurred_at
			FROM idenqa.verification_transitions
			WHERE tenant_id=$1 AND event_id>$2 ORDER BY event_id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list verification transitions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.VerificationTransitionRecord
			if err := rows.Scan(&value.EventID, &value.VerificationID, &value.FromState, &value.ToState,
				&value.ExpectedVersion, &value.ResultingVersion, &value.DecisionID, &value.ActorID,
				&value.CommandDigest, &value.OccurredAt); err != nil {
				return fmt.Errorf("scan verification transition: %w", err)
			}
			value.OccurredAt = value.OccurredAt.UTC()
			records = append(records, tenantexport.Record{Position: value.EventID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// VerificationChecks reads check metadata.
func (store *Store) VerificationChecks(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,verification_id,name,state,COALESCE(outcome,''),version,created_at,updated_at
			FROM idenqa.verification_checks
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list verification checks: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.VerificationCheckRecord
			if err := rows.Scan(&value.ID, &value.VerificationID, &value.Name, &value.State, &value.Outcome,
				&value.Version, &value.CreatedAt, &value.UpdatedAt); err != nil {
				return fmt.Errorf("scan verification check: %w", err)
			}
			value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// VerificationAttempts reads attempt provenance metadata.
func (store *Store) VerificationAttempts(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,verification_id,check_id,attempt_number,fence,runner_kind,runner_id,
				runner_version,package_digest,contract_major,contract_minor,request_digest,configuration_digest,
				state,started_at,deadline,finished_at,COALESCE(failure_class,''),COALESCE(failure_code,''),
				COALESCE(retry_disposition,''),COALESCE(retry_after_milliseconds,0),COALESCE(result_digest,'')
			FROM idenqa.verification_attempts
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list verification attempts: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.VerificationAttemptRecord
			var finishedAt *time.Time
			if err := rows.Scan(&value.ID, &value.VerificationID, &value.CheckID, &value.AttemptNumber,
				&value.Fence, &value.RunnerKind, &value.RunnerID, &value.RunnerVersion, &value.PackageDigest,
				&value.ContractMajor, &value.ContractMinor, &value.RequestDigest, &value.ConfigurationDigest,
				&value.State, &value.StartedAt, &value.Deadline, &finishedAt, &value.FailureClass,
				&value.FailureCode, &value.RetryDisposition, &value.RetryAfterMilliseconds,
				&value.ResultDigest); err != nil {
				return fmt.Errorf("scan verification attempt: %w", err)
			}
			value.StartedAt, value.Deadline = value.StartedAt.UTC(), value.Deadline.UTC()
			value.FinishedAt = utc(finishedAt)
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// Decisions lists decision identifiers in one bounded read and composes exact
// canonical bundles from the existing tenant-scoped decision repository.
func (store *Store) Decisions(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	identifiers := []string{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.verification_decisions
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list decisions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var identifier string
			if err := rows.Scan(&identifier); err != nil {
				return fmt.Errorf("scan decision id: %w", err)
			}
			identifiers = append(identifiers, identifier)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	records := make([]tenantexport.Record, 0, len(identifiers))
	for _, encoded := range identifiers {
		decisionID, err := id.ParseDecision(encoded)
		if err != nil {
			return nil, tenantexport.ErrInvalid
		}
		decision, err := store.decisions.Find(ctx, scope, decisionID)
		if err != nil {
			return nil, fmt.Errorf("read decision %s: %w", encoded, err)
		}
		bundle, report, err := policy.NewDecisionBundle(decision)
		if err != nil {
			return nil, fmt.Errorf("reproduce decision bundle %s: %w", encoded, err)
		}
		records = append(records, tenantexport.Record{Position: encoded, Fields: tenantexport.DecisionRecord{
			DecisionID:     report.DecisionID,
			VerificationID: report.VerificationID,
			DecisionDigest: report.DecisionDigest,
			BundleDigest:   report.BundleDigest,
			Directive:      string(report.Directive),
			Outcome:        string(report.Outcome),
			Assurance:      report.Assurance,
			Actor:          string(report.Actor),
			Supersedes:     report.Supersedes,
			DecidedAt:      report.DecidedAt.UTC(),
			Bundle:         bundle.Canonical(),
		}})
	}
	return records, nil
}

// AuditRecords reads the append-only reference-only audit chain.
func (store *Store) AuditRecords(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	afterSequence, err := parseIntegerPosition(after)
	if err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err = store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT sequence,event_id,event_type,aggregate_id,actor_id,occurred_at,
				event_digest,previous_hash,hash
			FROM idenqa.audit_records
			WHERE tenant_id=$1 AND sequence>$2 ORDER BY sequence LIMIT $3`, scope.ID().String(), afterSequence, limit)
		if err != nil {
			return fmt.Errorf("list audit records: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.AuditChainRecord
			if err := rows.Scan(&value.Sequence, &value.EventID, &value.EventType, &value.AggregateID,
				&value.ActorID, &value.OccurredAt, &value.EventDigest, &value.PreviousHash,
				&value.Hash); err != nil {
				return fmt.Errorf("scan audit record: %w", err)
			}
			value.OccurredAt = value.OccurredAt.UTC()
			records = append(records, tenantexport.Record{
				Position: strconv.FormatInt(value.Sequence, 10), Fields: value,
			})
		}
		return rows.Err()
	})
	return records, err
}

// WebhookEndpoints reads endpoint configuration without signing secrets.
func (store *Store) WebhookEndpoints(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,url,event_types,schema_version,version,secret_version,
				previous_secret_valid_until,disabled_at,COALESCE(disabled_reason,''),created_at,updated_at
			FROM idenqa.webhook_endpoints
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list webhook endpoints: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.WebhookEndpointRecord
			var previousValidUntil, disabledAt *time.Time
			if err := rows.Scan(&value.ID, &value.URL, &value.EventTypes, &value.SchemaVersion, &value.Version,
				&value.SecretVersion, &previousValidUntil, &disabledAt, &value.DisabledReason,
				&value.CreatedAt, &value.UpdatedAt); err != nil {
				return fmt.Errorf("scan webhook endpoint: %w", err)
			}
			value.PreviousSecretValidUntil = utc(previousValidUntil)
			value.DisabledAt = utc(disabledAt)
			value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// ReviewCases reads manual review case metadata.
func (store *Store) ReviewCases(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,verification_id,challenged_decision_id,
				COALESCE(superseding_decision_id,''),region,required_certification,oversight,state,
				COALESCE(assigned_reviewer,''),permitted_findings,version,created_at,updated_at
			FROM idenqa.review_cases
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list review cases: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.ReviewCaseRecord
			var permittedFindings []byte
			if err := rows.Scan(&value.ID, &value.VerificationID, &value.ChallengedDecisionID,
				&value.SupersedingDecisionID, &value.Region, &value.RequiredCertification, &value.Oversight,
				&value.State, &value.AssignedReviewer, &permittedFindings, &value.Version,
				&value.CreatedAt, &value.UpdatedAt); err != nil {
				return fmt.Errorf("scan review case: %w", err)
			}
			value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
			value.PermittedFindings = json.RawMessage(permittedFindings)
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// ReviewFindings reads immutable finding metadata.
func (store *Store) ReviewFindings(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,case_id,reviewer_id,resolution,reason_code,
				evidence_grant_ids,recorded_at
			FROM idenqa.review_findings
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list review findings: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.ReviewFindingRecord
			var grants []byte
			if err := rows.Scan(&value.ID, &value.CaseID, &value.ReviewerID, &value.Resolution,
				&value.ReasonCode, &grants, &value.RecordedAt); err != nil {
				return fmt.Errorf("scan review finding: %w", err)
			}
			value.RecordedAt = value.RecordedAt.UTC()
			value.EvidenceGrantIDs = json.RawMessage(grants)
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// IdentitySubjects reads persistent subject metadata without ciphertext.
func (store *Store) IdentitySubjects(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,region,state,version,(external_token IS NOT NULL),
				COALESCE(deletion_id,''),created_at,updated_at,erased_at
			FROM idenqa.identity_subjects
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list identity subjects: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.IdentitySubjectRecord
			var erasedAt *time.Time
			if err := rows.Scan(&value.ID, &value.Region, &value.State, &value.Version,
				&value.HasExternalReference, &value.DeletionID, &value.CreatedAt, &value.UpdatedAt,
				&erasedAt); err != nil {
				return fmt.Errorf("scan identity subject: %w", err)
			}
			value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
			value.ErasedAt = utc(erasedAt)
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// IdentityRecords reads safe metadata, masks, and digests. Encrypted values,
// lookup tokens, and provider payload metadata are never exported.
func (store *Store) IdentityRecords(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT records.id,records.subject_id,records.verification_id,records.kind,
				records.name,records.sequence,records.series_id,COALESCE(records.supersedes,''),
				encode(sha256(convert_to(records.metadata::text,'UTF8')),'hex'),
				(values.record_id IS NOT NULL),
				COALESCE(tokens.namespace,''),COALESCE(tokens.issuer,''),COALESCE(tokens.region,''),
				records.recorded_at,records.retain_until
			FROM idenqa.identity_records AS records
			LEFT JOIN idenqa.identity_record_values AS values
			  ON values.tenant_id=records.tenant_id AND values.record_id=records.id
			LEFT JOIN idenqa.identity_identifier_tokens AS tokens
			  ON tokens.tenant_id=records.tenant_id AND tokens.record_id=records.id
			WHERE records.tenant_id=$1 AND records.id>$2
			ORDER BY records.id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list identity records: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.IdentityRecordRecord
			var metadataDigest string
			if err := rows.Scan(&value.ID, &value.SubjectID, &value.VerificationID, &value.Kind,
				&value.Name, &value.Sequence, &value.SeriesID, &value.Supersedes, &metadataDigest,
				&value.HasValue, &value.IdentifierNamespace, &value.IdentifierIssuer,
				&value.IdentifierRegion, &value.RecordedAt, &value.RetainUntil); err != nil {
				return fmt.Errorf("scan identity record: %w", err)
			}
			value.MetadataDigest = "sha256:" + metadataDigest
			value.RecordedAt, value.RetainUntil = value.RecordedAt.UTC(), value.RetainUntil.UTC()
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// EvidenceAssets reads evidence asset metadata without ciphertext or keys.
func (store *Store) EvidenceAssets(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,subject_id,verification_id,requirement_key,evidence_type,
				artefact,acquisition_method,assurances,region,retention_class,content_revision,
				object_key,object_version,ciphertext_size,ciphertext_checksum,plaintext_digest,
				media_type,integrity,state,version,created_at,updated_at
			FROM idenqa.evidence_assets
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list evidence assets: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.EvidenceAssetRecord
			if err := rows.Scan(&value.ID, &value.SubjectID, &value.VerificationID, &value.RequirementKey,
				&value.EvidenceType, &value.Artefact, &value.AcquisitionMethod, &value.Assurances,
				&value.Region, &value.RetentionClass, &value.ContentRevision, &value.ObjectKey,
				&value.ObjectVersion, &value.CiphertextSize, &value.CiphertextChecksum,
				&value.PlaintextDigest, &value.MediaType, &value.Integrity, &value.State,
				&value.Version, &value.CreatedAt, &value.UpdatedAt); err != nil {
				return fmt.Errorf("scan evidence asset: %w", err)
			}
			value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// FraudConfiguration reads versioned tenant fraud rules.
func (store *Store) FraudConfiguration(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	afterVersion, err := parseIntegerPosition(after)
	if err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err = store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT version,configuration,digest,actor_key_id,recorded_at
			FROM idenqa.fraud_configurations
			WHERE tenant_id=$1 AND version>$2 ORDER BY version LIMIT $3`, scope.ID().String(), afterVersion, limit)
		if err != nil {
			return fmt.Errorf("list fraud configuration: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.FraudConfigurationRecord
			var configuration []byte
			if err := rows.Scan(&value.Version, &configuration, &value.Digest, &value.ActorID,
				&value.RecordedAt); err != nil {
				return fmt.Errorf("scan fraud configuration: %w", err)
			}
			value.RecordedAt = value.RecordedAt.UTC()
			value.Configuration = json.RawMessage(configuration)
			records = append(records, tenantexport.Record{
				Position: strconv.FormatInt(value.Version, 10), Fields: value,
			})
		}
		return rows.Err()
	})
	return records, err
}

// PrivacyDeletions reads deletion workflow status.
func (store *Store) PrivacyDeletions(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,aggregate_id,region,state,backup_retention_seconds,
				backup_expires_at,COALESCE(failure_class,''),version,requested_at,updated_at,completed_at
			FROM idenqa.deletion_requests
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list privacy deletions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.PrivacyDeletionRecord
			var completedAt *time.Time
			if err := rows.Scan(&value.ID, &value.AggregateID, &value.Region, &value.State,
				&value.BackupRetentionSeconds, &value.BackupExpiresAt, &value.FailureClass,
				&value.Version, &value.RequestedAt, &value.UpdatedAt, &completedAt); err != nil {
				return fmt.Errorf("scan privacy deletion: %w", err)
			}
			value.BackupExpiresAt = value.BackupExpiresAt.UTC()
			value.RequestedAt, value.UpdatedAt = value.RequestedAt.UTC(), value.UpdatedAt.UTC()
			value.CompletedAt = utc(completedAt)
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

// PrivacyHolds reads legal-hold status.
func (store *Store) PrivacyHolds(ctx context.Context, scope tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id,aggregate_id,authority,reason,starts_at,review_at,released_at,created_at
			FROM idenqa.legal_holds
			WHERE tenant_id=$1 AND id>$2 ORDER BY id LIMIT $3`, scope.ID().String(), after, limit)
		if err != nil {
			return fmt.Errorf("list privacy holds: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value tenantexport.PrivacyHoldRecord
			var releasedAt *time.Time
			if err := rows.Scan(&value.ID, &value.AggregateID, &value.Authority, &value.Reason,
				&value.StartsAt, &value.ReviewAt, &releasedAt, &value.CreatedAt); err != nil {
				return fmt.Errorf("scan privacy hold: %w", err)
			}
			value.StartsAt, value.ReviewAt, value.CreatedAt = value.StartsAt.UTC(), value.ReviewAt.UTC(), value.CreatedAt.UTC()
			value.ReleasedAt = utc(releasedAt)
			records = append(records, tenantexport.Record{Position: value.ID, Fields: value})
		}
		return rows.Err()
	})
	return records, err
}

func (store *Store) read(
	ctx context.Context,
	scope tenant.Scope,
	work func(context.Context, platformpostgres.Transaction) error,
) error {
	if scope.ID().IsZero() {
		return tenantexport.ErrInvalid
	}
	return store.pool.WithinTransaction(
		ctx,
		platformpostgres.TransactionOptions{ReadOnly: true, Isolation: platformpostgres.IsolationRepeatableRead},
		func(ctx context.Context, tx platformpostgres.Transaction) error {
			var applied string
			if err := tx.QueryRow(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, scope.ID().String()).Scan(&applied); err != nil {
				return fmt.Errorf("set tenant export scope: %w", err)
			}
			return work(ctx, tx)
		},
	)
}

func validLimit(limit int) error {
	if limit < 1 || limit > tenantexport.DefaultBatchSize {
		return tenantexport.ErrInvalid
	}
	return nil
}

func optionalInt32(value int32) *int32 {
	if value == 0 {
		return nil
	}
	cloned := value
	return &cloned
}

func optionalInt64(value int64) *int64 {
	if value == 0 {
		return nil
	}
	cloned := value
	return &cloned
}

func utc(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	normalized := value.UTC()
	return &normalized
}

func tuplePosition(identifier string, sequence int64) string {
	return identifier + "#" + strconv.FormatInt(sequence, 10)
}

func parseTuplePosition(position string) (string, int64, error) {
	if position == "" {
		return "", 0, nil
	}
	index := strings.LastIndex(position, "#")
	if index <= 0 || index == len(position)-1 {
		return "", 0, tenantexport.ErrInvalid
	}
	sequence, err := strconv.ParseInt(position[index+1:], 10, 64)
	if err != nil || sequence < 1 {
		return "", 0, tenantexport.ErrInvalid
	}
	return position[:index], sequence, nil
}

func parseIntegerPosition(position string) (int64, error) {
	if position == "" {
		return 0, nil
	}
	value, err := strconv.ParseInt(position, 10, 64)
	if err != nil || value < 0 {
		return 0, tenantexport.ErrInvalid
	}
	return value, nil
}
