//go:build integration

package integration_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
)

func TestPostgreSQLFoundation(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()

	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("OpenMigrator() error = %v", err)
	}
	report, err := migrator.Preflight(ctx)
	if err != nil {
		t.Fatalf("Preflight() empty database error = %v", err)
	}
	if report.Current != 0 || !report.Pending || report.Latest != migrations.LatestVersion {
		t.Fatalf("empty report = %+v", report)
	}
	report, err = migrator.Up(ctx)
	if err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if report.Current != migrations.LatestVersion || report.Pending || report.Dirty {
		t.Fatalf("migrated report = %+v", report)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}

	pool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("Open() pool error = %v", err)
	}
	defer pool.Close()
	if err := pool.Check(ctx, migrations.LatestVersion); err != nil {
		t.Fatalf("Check() error = %v", err)
	}

	rollbackCause := errors.New("force rollback")
	err = pool.WithinTransaction(
		ctx,
		idenqapostgres.TransactionOptions{Isolation: idenqapostgres.IsolationSerializable},
		func(ctx context.Context, transaction idenqapostgres.Transaction) error {
			if _, err := transaction.Exec(ctx, "CREATE TABLE idenqa.transaction_rollback_probe (value bigint)"); err != nil {
				return fmt.Errorf("create rollback probe: %w", err)
			}

			return rollbackCause
		},
	)
	if !errors.Is(err, rollbackCause) {
		t.Fatalf("WithinTransaction() error = %v, want rollback cause", err)
	}
	connection, err := pgx.Connect(ctx, database.url)
	if err != nil {
		t.Fatalf("connect for rollback assertion: %v", err)
	}
	defer func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close rollback assertion connection: %v", err)
		}
	}()
	var relation *string
	if err := connection.QueryRow(ctx, "SELECT to_regclass('idenqa.transaction_rollback_probe')::text").Scan(&relation); err != nil {
		t.Fatalf("query rollback probe: %v", err)
	}
	if relation != nil {
		t.Fatalf("rolled-back relation = %q, want nil", *relation)
	}

	migrator, err = idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("reopen migrator: %v", err)
	}
	if _, err := migrator.DownOne(ctx, idenqapostgres.RollbackGuard{
		Environment: "production",
		Confirmed:   true,
	}); !errors.Is(err, idenqapostgres.ErrRollbackForbidden) {
		t.Fatalf("production DownOne() error = %v", err)
	}
	report, err = migrator.DownOne(ctx, idenqapostgres.RollbackGuard{
		Environment: "test",
		Confirmed:   true,
	})
	if err != nil {
		t.Fatalf("test DownOne() error = %v", err)
	}
	if report.Current != migrations.LatestVersion-1 || !report.Pending || report.Dirty {
		t.Fatalf("rolled-back report = %+v", report)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close rollback migrator: %v", err)
	}
	if err := pool.Check(ctx, migrations.LatestVersion); !errors.Is(err, idenqapostgres.ErrSchemaIncompatible) {
		t.Fatalf("Check() after rollback error = %v, want ErrSchemaIncompatible", err)
	}
}

func TestPostgreSQLReadinessFailureAndRecovery(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("OpenMigrator() error = %v", err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}

	pool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer pool.Close()
	if err := pool.Check(ctx, migrations.LatestVersion); err != nil {
		t.Fatalf("initial Check() error = %v", err)
	}

	database.setConnectionsAllowed(t, false)
	failureContext, cancelFailureCheck := context.WithTimeout(ctx, 2*time.Second)
	err = pool.Check(failureContext, migrations.LatestVersion)
	cancelFailureCheck()
	if !errors.Is(err, idenqapostgres.ErrUnavailable) {
		t.Fatalf("Check() while unavailable error = %v, want ErrUnavailable", err)
	}
	database.setConnectionsAllowed(t, true)

	deadline := time.Now().Add(5 * time.Second)
	for {
		err := pool.Check(ctx, migrations.LatestVersion)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Check() did not recover: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

type isolatedDatabase struct {
	admin *pgx.Conn
	name  string
	url   string
	roles []string
}

func createIsolatedDatabase(t *testing.T) *isolatedDatabase {
	t.Helper()

	adminURL := os.Getenv("DATABASE_TEST_URL")
	if adminURL == "" {
		t.Skip("DATABASE_TEST_URL is not configured")
	}
	admin, err := pgx.Connect(t.Context(), adminURL)
	if err != nil {
		t.Fatalf("connect to PostgreSQL test administrator: %v", err)
	}
	name := "idenqa_test_" + randomSuffix(t)
	identifier := pgx.Identifier{name}.Sanitize()
	if _, err := admin.Exec(t.Context(), "CREATE DATABASE "+identifier); err != nil {
		_ = admin.Close(context.Background())
		t.Fatalf("create isolated database: %v", err)
	}
	parsed, err := url.Parse(adminURL)
	if err != nil {
		t.Fatalf("parse test database URL: %v", err)
	}
	parsed.Path = "/" + name
	database := &isolatedDatabase{admin: admin, name: name, url: parsed.String()}
	t.Cleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		database.setConnectionsAllowed(t, true)
		_, _ = admin.Exec(cleanupContext, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()", name)
		if _, err := admin.Exec(cleanupContext, "DROP DATABASE IF EXISTS "+identifier); err != nil {
			t.Errorf("drop isolated database: %v", err)
		}
		for _, role := range database.roles {
			if _, err := admin.Exec(cleanupContext, "DROP ROLE IF EXISTS "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Errorf("drop isolated database role: %v", err)
			}
		}
		if err := admin.Close(cleanupContext); err != nil {
			t.Errorf("close PostgreSQL test administrator: %v", err)
		}
	})

	return database
}

func (database *isolatedDatabase) createRuntimeRole(t *testing.T) string {
	t.Helper()

	role := "idenqa_runtime_" + randomSuffix(t)
	if _, err := database.admin.Exec(t.Context(), "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" NOLOGIN"); err != nil {
		t.Fatalf("create isolated runtime role: %v", err)
	}
	database.roles = append(database.roles, role)
	connection, err := pgx.Connect(t.Context(), database.url)
	if err != nil {
		t.Fatalf("connect to isolated database for grants: %v", err)
	}
	defer func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close isolated grant connection: %v", err)
		}
	}()
	statements := []string{
		"GRANT SELECT, INSERT ON idenqa.evidence_temporal_frames TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT ON idenqa.pack_release_states, idenqa.pack_release_history TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.review_recapture_evaluation_requests,idenqa.capture_recoveries, idenqa.capture_recovery_uploads TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.model_registries TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.model_registry_revisions, idenqa.model_registry_history TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT ON public.schema_migrations TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT USAGE ON SCHEMA idenqa TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.tenants TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.api_keys TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.capture_profiles TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.capture_profile_revisions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT INSERT ON idenqa.capture_profile_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE, DELETE ON idenqa.idempotency_records TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.verification_sessions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.verification_checks TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.verification_attempts TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_ready_provider_captures(timestamptz,integer,text,text,text) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.provider_requests TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.provider_dispatches TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.provider_registrations TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.provider_registration_history TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.provider_async_operations TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.provider_health_snapshots TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, DELETE ON idenqa.provider_dispatch_leases TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE, DELETE ON idenqa.provider_dispatch_admissions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.provider_callback_receipts TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.resolve_provider_callback(text) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.model_requests TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.model_dispatches TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.verification_observations TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.verification_attempt_diagnostics TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.verification_result_inbox TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.verification_reconciliations TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.policy_snapshots TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.policy_evaluations TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.policy_routing_receipts TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.verification_input_requests TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.review_recapture_acknowledgements, idenqa.review_recaptures, idenqa.review_evaluation_requests,idenqa.review_evaluations TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.review_operator_assignments,idenqa.review_policy_settings,idenqa.review_case_operations TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.review_administration_history,idenqa.review_case_settings,idenqa.review_evidence_access,idenqa.review_arbitrations,idenqa.review_correction_evaluations,idenqa.review_correction_intakes,idenqa.review_appeal_history TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_expired_review_appeals(timestamptz,integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_ready_review_evaluations(timestamptz,integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.verification_decisions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.policies TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.policy_revisions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.policy_activations TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.webhook_endpoints TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.webhook_secrets TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.webhook_deliveries TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.webhook_delivery_attempts TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.webhook_events TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.audit_heads TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.audit_records TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.audit_keys TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.audit_checkpoints TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.retention_bindings TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.legal_holds TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.deletion_requests TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.deletion_targets TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.deletion_tombstones TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.review_cases TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.review_findings TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.appeals TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.capture_tokens TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.outcome_tokens TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.experiences TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.experience_revisions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.experience_targeting TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.experience_events TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.experience_session_pins TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE, DELETE ON idenqa.websocket_connection_tickets TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.realtime_client_commands TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.realtime_streams TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE, DELETE ON idenqa.realtime_events TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.realtime_acknowledgements TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT INSERT ON idenqa.verification_session_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT INSERT ON idenqa.capture_document_selection_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.verification_transitions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.notice_versions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT INSERT ON idenqa.notice_version_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.subjects TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.processing_authorities TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT INSERT ON idenqa.authority_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.subject_responses TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.evidence_assets TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.evidence_asset_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT INSERT ON idenqa.evidence_key_rewrap_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.evidence_upload_intents TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT INSERT ON idenqa.evidence_upload_intent_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.evidence_object_reconciliations TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT INSERT ON idenqa.evidence_object_reconciliation_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.evidence_processing_grants TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.evidence_grant_redemptions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.evidence_grant_redemption_outcomes TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT INSERT ON idenqa.evidence_processing_grant_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT INSERT ON idenqa.evidence_grant_access_attempt_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.outbox_events TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_due_verification_reconciliations(timestamptz, integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_pending_check_progress_tenants(integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.identity_subjects TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE, DELETE ON idenqa.identity_keys,idenqa.identity_current TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, DELETE ON idenqa.identity_record_values,idenqa.identity_identifier_tokens TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.assurance_profiles,idenqa.verification_assurance TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.policy_assurance_assignments TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.identity_subject_verifications,idenqa.identity_records,idenqa.identity_record_edges,idenqa.identity_record_evidence,idenqa.identity_configurations,idenqa.identity_receipts TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_expired_identity_tenants(timestamptz,integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.fraud_configurations,idenqa.fraud_receipts,idenqa.fraud_proposals TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.fraud_keys TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, DELETE ON idenqa.fraud_links TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_expired_fraud_tenants(timestamptz,integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_ready_verification_captures(timestamptz, integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_ready_webhook_deliveries(timestamptz, integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_ready_webhook_events(timestamptz, integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.expire_webhook_data(timestamptz, integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_due_verification_expirations(timestamptz, integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_ready_policy_authorships(timestamptz, integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_due_privacy_deletions(timestamptz, integer) TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.proposals TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.accepted_commands TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.proposal_mode_configs TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.prompt_registry,idenqa.generative_model_registry,idenqa.impact_assessments TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.proposal_generation_usage TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.proposal_generation_activations TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.proposal_generation_activation_history TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.privacy_requests TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.privacy_request_events,idenqa.privacy_request_decisions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.privacy_restrictions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.privacy_disclosures TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.processor_inventory TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.processor_inventory_revisions TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.hmac_key_domains,idenqa.hmac_keys TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.support_grants,idenqa.break_glass_requests TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.break_glass_uses TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.key_rewrap_state TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.key_rewrap_audit TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT ON idenqa.key_destruction_verifications,idenqa.key_destruction_schedules TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT SELECT, INSERT, UPDATE ON idenqa.key_recovery_ceremonies TO " + pgx.Identifier{role}.Sanitize(),
		"GRANT EXECUTE ON FUNCTION idenqa.list_key_rewrap_tenants(text, integer) TO " + pgx.Identifier{role}.Sanitize(),
	}
	for _, statement := range statements {
		if _, err := connection.Exec(t.Context(), statement); err != nil {
			t.Fatalf("grant isolated runtime role: %v", err)
		}
	}

	return role
}

func (database *isolatedDatabase) grantHeadgateRuntime(t *testing.T, role string) {
	t.Helper()
	connection, err := pgx.Connect(t.Context(), database.url)
	if err != nil {
		t.Fatalf("connect to isolated database for Headgate grants: %v", err)
	}
	defer func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close isolated Headgate grant connection: %v", err)
		}
	}()
	identifier := pgx.Identifier{role}.Sanitize()
	statements := []string{
		"GRANT USAGE ON SCHEMA headgate TO " + identifier,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA headgate TO " + identifier,
		"GRANT USAGE, SELECT, UPDATE ON ALL SEQUENCES IN SCHEMA headgate TO " + identifier,
	}
	for _, statement := range statements {
		if _, err := connection.Exec(t.Context(), statement); err != nil {
			t.Fatalf("grant isolated Headgate runtime role: %v", err)
		}
	}
}

func (database *isolatedDatabase) setConnectionsAllowed(t *testing.T, allowed bool) {
	t.Helper()

	operationContext, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 5*time.Second)
	defer cancel()

	identifier := pgx.Identifier{database.name}.Sanitize()
	if _, err := database.admin.Exec(operationContext, fmt.Sprintf("ALTER DATABASE %s WITH ALLOW_CONNECTIONS %t", identifier, allowed)); err != nil {
		t.Fatalf("set isolated database connection policy: %v", err)
	}
	if !allowed {
		if _, err := database.admin.Exec(
			operationContext,
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()",
			database.name,
		); err != nil {
			t.Fatalf("terminate isolated database connections: %v", err)
		}
	}
}

func randomSuffix(t *testing.T) string {
	t.Helper()

	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		t.Fatalf("generate database suffix: %v", err)
	}

	return hex.EncodeToString(buffer)
}

func migrationConfig(databaseURL string) idenqapostgres.MigrationConfig {
	return idenqapostgres.MigrationConfig{
		URL:              databaseURL,
		ConnectTimeout:   5 * time.Second,
		StatementTimeout: 30 * time.Second,
	}
}

func poolConfig(databaseURL string) idenqapostgres.Config {
	return idenqapostgres.Config{
		URL:                 databaseURL,
		MaxConnections:      4,
		MinConnections:      0,
		MaxConnectionAge:    time.Minute,
		MaxConnectionIdle:   time.Minute,
		HealthCheckInterval: time.Second,
		ConnectTimeout:      5 * time.Second,
	}
}
