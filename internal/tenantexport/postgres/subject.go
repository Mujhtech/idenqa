package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/tenantexport"
	"github.com/jackc/pgx/v5"
)

// IdentitySubject reads one exact subject's persistent metadata.
func (store *Store) IdentitySubject(ctx context.Context, scope tenant.Scope, subjectID string) (tenantexport.Record, error) {
	if _, err := id.ParseSubject(subjectID); err != nil {
		return tenantexport.Record{}, tenantexport.ErrInvalid
	}
	var result tenantexport.Record
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		var value tenantexport.IdentitySubjectRecord
		var erasedAt *time.Time
		err := tx.QueryRow(ctx, `SELECT id,region,state,version,(external_token IS NOT NULL),
				COALESCE(deletion_id,''),created_at,updated_at,erased_at
			FROM idenqa.identity_subjects
			WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), subjectID).Scan(
			&value.ID, &value.Region, &value.State, &value.Version, &value.HasExternalReference,
			&value.DeletionID, &value.CreatedAt, &value.UpdatedAt, &erasedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return tenantexport.ErrInvalid
		}
		if err != nil {
			return fmt.Errorf("find subject export identity subject: %w", err)
		}
		value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
		value.ErasedAt = utc(erasedAt)
		result = tenantexport.Record{Position: value.ID, Fields: value}
		return nil
	})
	return result, err
}

// IdentityRecordsForSubject reads one subject's safe identity metadata without values.
func (store *Store) IdentityRecordsForSubject(ctx context.Context, scope tenant.Scope, subjectID, after string, limit int) ([]tenantexport.Record, error) {
	return store.subjectRecords(ctx, scope, subjectID, after, limit, `SELECT records.id,records.subject_id,records.verification_id,records.kind,
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
		WHERE records.tenant_id=$1 AND records.subject_id=$2 AND records.id>$3
		ORDER BY records.id LIMIT $4`, func(rows pgx.Rows) (tenantexport.Record, error) {
		var value tenantexport.IdentityRecordRecord
		var metadataDigest string
		if err := rows.Scan(&value.ID, &value.SubjectID, &value.VerificationID, &value.Kind,
			&value.Name, &value.Sequence, &value.SeriesID, &value.Supersedes, &metadataDigest,
			&value.HasValue, &value.IdentifierNamespace, &value.IdentifierIssuer,
			&value.IdentifierRegion, &value.RecordedAt, &value.RetainUntil); err != nil {
			return tenantexport.Record{}, fmt.Errorf("scan subject identity record: %w", err)
		}
		value.MetadataDigest = "sha256:" + metadataDigest
		value.RecordedAt, value.RetainUntil = value.RecordedAt.UTC(), value.RetainUntil.UTC()
		return tenantexport.Record{Position: value.ID, Fields: value}, nil
	})
}

// VerificationsForSubject reads the verification sessions linked to one subject.
func (store *Store) VerificationsForSubject(ctx context.Context, scope tenant.Scope, subjectID, after string, limit int) ([]tenantexport.Record, error) {
	return store.subjectRecords(ctx, scope, subjectID, after, limit, `SELECT sessions.id,sessions.state,sessions.version,sessions.source_profile_id,
			sessions.source_profile_revision,sessions.source_profile_digest,COALESCE(sessions.policy_id,''),
			COALESCE(sessions.decision_id,''),COALESCE(sessions.region,''),sessions.requirements,
			COALESCE(sessions.failure_class,''),COALESCE(sessions.failure_code,''),
			sessions.created_at,sessions.updated_at,sessions.expires_at,sessions.capture_completed_at,
			COALESCE(sessions.completed_decision_id,''),sessions.expiry_discovered_at
		FROM idenqa.verification_sessions AS sessions
		JOIN idenqa.identity_subject_verifications AS links
		  ON links.tenant_id=sessions.tenant_id AND links.verification_id=sessions.id
		WHERE sessions.tenant_id=$1 AND links.subject_id=$2 AND sessions.id>$3
		ORDER BY sessions.id LIMIT $4`, func(rows pgx.Rows) (tenantexport.Record, error) {
		var value tenantexport.VerificationRecord
		var requirements []byte
		var captureCompletedAt, expiryDiscoveredAt *time.Time
		if err := rows.Scan(&value.ID, &value.State, &value.Version, &value.ProfileID,
			&value.ProfileRevision, &value.ProfileDigest, &value.PolicyID, &value.DecisionID,
			&value.Region, &requirements, &value.FailureClass, &value.FailureCode,
			&value.CreatedAt, &value.UpdatedAt, &value.ExpiresAt, &captureCompletedAt,
			&value.CompletedDecisionID, &expiryDiscoveredAt); err != nil {
			return tenantexport.Record{}, fmt.Errorf("scan subject verification: %w", err)
		}
		value.Requirements = json.RawMessage(requirements)
		value.CaptureCompletedAt = utc(captureCompletedAt)
		value.ExpiryDiscoveredAt = utc(expiryDiscoveredAt)
		value.CreatedAt, value.UpdatedAt, value.ExpiresAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC(), value.ExpiresAt.UTC()
		return tenantexport.Record{Position: value.ID, Fields: value}, nil
	})
}

// VerificationTransitionsForSubject reads subject-linked lifecycle receipts.
func (store *Store) VerificationTransitionsForSubject(ctx context.Context, scope tenant.Scope, subjectID, after string, limit int) ([]tenantexport.Record, error) {
	return store.subjectRecords(ctx, scope, subjectID, after, limit, `SELECT event_id,verification_id,from_state,to_state,expected_version,
			resulting_version,COALESCE(decision_id,''),actor_id,command_digest,occurred_at
		FROM idenqa.verification_transitions
		WHERE tenant_id=$1 AND verification_id IN(
			SELECT verification_id FROM idenqa.identity_subject_verifications WHERE tenant_id=$1 AND subject_id=$2
		) AND event_id>$3 ORDER BY event_id LIMIT $4`, func(rows pgx.Rows) (tenantexport.Record, error) {
		var value tenantexport.VerificationTransitionRecord
		if err := rows.Scan(&value.EventID, &value.VerificationID, &value.FromState, &value.ToState,
			&value.ExpectedVersion, &value.ResultingVersion, &value.DecisionID, &value.ActorID,
			&value.CommandDigest, &value.OccurredAt); err != nil {
			return tenantexport.Record{}, fmt.Errorf("scan subject verification transition: %w", err)
		}
		value.OccurredAt = value.OccurredAt.UTC()
		return tenantexport.Record{Position: value.EventID, Fields: value}, nil
	})
}

// VerificationChecksForSubject reads subject-linked check metadata.
func (store *Store) VerificationChecksForSubject(ctx context.Context, scope tenant.Scope, subjectID, after string, limit int) ([]tenantexport.Record, error) {
	return store.subjectRecords(ctx, scope, subjectID, after, limit, `SELECT id,verification_id,name,state,COALESCE(outcome,''),version,created_at,updated_at
		FROM idenqa.verification_checks
		WHERE tenant_id=$1 AND verification_id IN(
			SELECT verification_id FROM idenqa.identity_subject_verifications WHERE tenant_id=$1 AND subject_id=$2
		) AND id>$3 ORDER BY id LIMIT $4`, func(rows pgx.Rows) (tenantexport.Record, error) {
		var value tenantexport.VerificationCheckRecord
		if err := rows.Scan(&value.ID, &value.VerificationID, &value.Name, &value.State, &value.Outcome,
			&value.Version, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return tenantexport.Record{}, fmt.Errorf("scan subject verification check: %w", err)
		}
		value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
		return tenantexport.Record{Position: value.ID, Fields: value}, nil
	})
}

// VerificationAttemptsForSubject reads subject-linked attempt provenance.
func (store *Store) VerificationAttemptsForSubject(ctx context.Context, scope tenant.Scope, subjectID, after string, limit int) ([]tenantexport.Record, error) {
	return store.subjectRecords(ctx, scope, subjectID, after, limit, `SELECT id,verification_id,check_id,attempt_number,fence,runner_kind,runner_id,
			runner_version,package_digest,contract_major,contract_minor,request_digest,configuration_digest,
			state,started_at,deadline,finished_at,COALESCE(failure_class,''),COALESCE(failure_code,''),
			COALESCE(retry_disposition,''),COALESCE(retry_after_milliseconds,0),COALESCE(result_digest,'')
		FROM idenqa.verification_attempts
		WHERE tenant_id=$1 AND verification_id IN(
			SELECT verification_id FROM idenqa.identity_subject_verifications WHERE tenant_id=$1 AND subject_id=$2
		) AND id>$3 ORDER BY id LIMIT $4`, func(rows pgx.Rows) (tenantexport.Record, error) {
		var value tenantexport.VerificationAttemptRecord
		var finishedAt *time.Time
		if err := rows.Scan(&value.ID, &value.VerificationID, &value.CheckID, &value.AttemptNumber,
			&value.Fence, &value.RunnerKind, &value.RunnerID, &value.RunnerVersion, &value.PackageDigest,
			&value.ContractMajor, &value.ContractMinor, &value.RequestDigest, &value.ConfigurationDigest,
			&value.State, &value.StartedAt, &value.Deadline, &finishedAt, &value.FailureClass,
			&value.FailureCode, &value.RetryDisposition, &value.RetryAfterMilliseconds,
			&value.ResultDigest); err != nil {
			return tenantexport.Record{}, fmt.Errorf("scan subject verification attempt: %w", err)
		}
		value.StartedAt, value.Deadline = value.StartedAt.UTC(), value.Deadline.UTC()
		value.FinishedAt = utc(finishedAt)
		return tenantexport.Record{Position: value.ID, Fields: value}, nil
	})
}

// EvidenceAssetsForSubject reads evidence metadata without object locations,
// checksums, or key references.
func (store *Store) EvidenceAssetsForSubject(ctx context.Context, scope tenant.Scope, subjectID, after string, limit int) ([]tenantexport.Record, error) {
	return store.subjectRecords(ctx, scope, subjectID, after, limit, `SELECT id,verification_id,requirement_key,evidence_type,artefact,acquisition_method,
			assurances,region,retention_class,content_revision,ciphertext_size,plaintext_digest,media_type,
			integrity,state,version,created_at,updated_at
		FROM idenqa.evidence_assets
		WHERE tenant_id=$1 AND subject_id=$2 AND id>$3 ORDER BY id LIMIT $4`, func(rows pgx.Rows) (tenantexport.Record, error) {
		var value tenantexport.SubjectEvidenceRecord
		if err := rows.Scan(&value.ID, &value.VerificationID, &value.RequirementKey, &value.EvidenceType,
			&value.Artefact, &value.AcquisitionMethod, &value.Assurances, &value.Region, &value.RetentionClass,
			&value.ContentRevision, &value.CiphertextSize, &value.PlaintextDigest, &value.MediaType,
			&value.Integrity, &value.State, &value.Version, &value.CreatedAt, &value.UpdatedAt); err != nil {
			return tenantexport.Record{}, fmt.Errorf("scan subject evidence asset: %w", err)
		}
		value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
		return tenantexport.Record{Position: value.ID, Fields: value}, nil
	})
}

// DecisionsForSubject composes exact byte-canonical decision bundles for the
// subject's verifications.
func (store *Store) DecisionsForSubject(ctx context.Context, scope tenant.Scope, subjectID, after string, limit int) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	identifiers := []string{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, `SELECT id FROM idenqa.verification_decisions
			WHERE tenant_id=$1 AND verification_id IN(
				SELECT verification_id FROM idenqa.identity_subject_verifications WHERE tenant_id=$1 AND subject_id=$2
			) AND id>$3 ORDER BY id LIMIT $4`, scope.ID().String(), subjectID, after, limit)
		if err != nil {
			return fmt.Errorf("list subject decisions: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var identifier string
			if err := rows.Scan(&identifier); err != nil {
				return fmt.Errorf("scan subject decision id: %w", err)
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
			return nil, fmt.Errorf("read subject decision %s: %w", encoded, err)
		}
		bundle, report, err := policy.NewDecisionBundle(decision)
		if err != nil {
			return nil, fmt.Errorf("reproduce subject decision bundle %s: %w", encoded, err)
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

// ReviewCasesForSubject reads subject-linked review case metadata.
func (store *Store) ReviewCasesForSubject(ctx context.Context, scope tenant.Scope, subjectID, after string, limit int) ([]tenantexport.Record, error) {
	return store.subjectRecords(ctx, scope, subjectID, after, limit, `SELECT cases.id,cases.verification_id,cases.challenged_decision_id,
			COALESCE(cases.superseding_decision_id,''),cases.region,cases.required_certification,cases.oversight,cases.state,
			COALESCE(cases.assigned_reviewer,''),cases.permitted_findings,cases.version,cases.created_at,cases.updated_at
		FROM idenqa.review_cases AS cases
		WHERE cases.tenant_id=$1 AND cases.verification_id IN(
			SELECT verification_id FROM idenqa.identity_subject_verifications WHERE tenant_id=$1 AND subject_id=$2
		) AND cases.id>$3 ORDER BY cases.id LIMIT $4`, func(rows pgx.Rows) (tenantexport.Record, error) {
		var value tenantexport.ReviewCaseRecord
		var permittedFindings []byte
		if err := rows.Scan(&value.ID, &value.VerificationID, &value.ChallengedDecisionID,
			&value.SupersedingDecisionID, &value.Region, &value.RequiredCertification, &value.Oversight,
			&value.State, &value.AssignedReviewer, &permittedFindings, &value.Version,
			&value.CreatedAt, &value.UpdatedAt); err != nil {
			return tenantexport.Record{}, fmt.Errorf("scan subject review case: %w", err)
		}
		value.PermittedFindings = json.RawMessage(permittedFindings)
		value.CreatedAt, value.UpdatedAt = value.CreatedAt.UTC(), value.UpdatedAt.UTC()
		return tenantexport.Record{Position: value.ID, Fields: value}, nil
	})
}

func (store *Store) subjectRecords(
	ctx context.Context,
	scope tenant.Scope,
	subjectID, after string,
	limit int,
	query string,
	scan func(pgx.Rows) (tenantexport.Record, error),
) ([]tenantexport.Record, error) {
	if err := validLimit(limit); err != nil {
		return nil, err
	}
	records := []tenantexport.Record{}
	err := store.read(ctx, scope, func(ctx context.Context, tx platformpostgres.Transaction) error {
		rows, err := tx.Query(ctx, query, scope.ID().String(), subjectID, after, limit)
		if err != nil {
			return fmt.Errorf("list subject export records: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			record, err := scan(rows)
			if err != nil {
				return err
			}
			if record.Position == "" {
				return tenantexport.ErrInvalid
			}
			records = append(records, record)
		}
		return rows.Err()
	})
	return records, err
}

var _ tenantexport.SubjectSources = (*Store)(nil)
