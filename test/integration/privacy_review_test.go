//go:build integration

package integration_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	localobjects "github.com/Mujhtech/idenqa/internal/platform/objectstore/local"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/privacy"
	privacypostgres "github.com/Mujhtech/idenqa/internal/privacy/postgres"
	"github.com/Mujhtech/idenqa/internal/review"
	reviewpostgres "github.com/Mujhtech/idenqa/internal/review/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestPrivacyAndReviewForcedRLSHistoryAndConcurrency(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ids, _ := id.NewSystemGenerator()
	now := time.Now().UTC().Truncate(time.Second)
	firstTenant, verificationID := seedExecutionVerification(t, admin, ids, now)
	secondTenant, _ := seedExecutionVerification(t, admin, ids, now)

	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	firstScope, _ := tenant.NewScope(firstTenant)
	decision := newIntegrationDecision(t, ids, firstTenant, verificationID, id.Decision{}, now)
	decisionStore, _ := policypostgres.New(runtime)
	if err := decisionStore.Append(ctx, firstScope, decision); err != nil {
		t.Fatal(err)
	}

	deletionID, _ := ids.NewDeletion()
	caseID, _ := ids.NewReviewCase()
	findingID, _ := ids.NewFinding()
	err = runtime.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if err := setIntegrationTenantScope(ctx, tx, firstTenant.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.retention_bindings
			(tenant_id,aggregate_id,data_class,region,retention_seconds,expires_at,policy_digest,created_at)
			VALUES ($1,$2,'raw_evidence','ng-1',$3,$4,$5,$6)`, firstTenant.String(), verificationID.String(), int64(30*24*time.Hour/time.Second), now.Add(30*24*time.Hour), sixtyFour('a'), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.deletion_requests
			(tenant_id,id,aggregate_id,region,state,backup_expires_at,version,requested_at,updated_at)
			VALUES ($1,$2,$3,'ng-1','requested',$4,1,$5,$5)`, firstTenant.String(), deletionID.String(), verificationID.String(), now.Add(35*24*time.Hour), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.review_cases
			(tenant_id,id,verification_id,challenged_decision_id,region,required_certification,oversight,state,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,'ng-1','document.level2','single','open',1,$5,$5)`, firstTenant.String(), caseID.String(), verificationID.String(), decision.ID().String(), now); err != nil {
			return err
		}
		claimed, err := tx.Exec(ctx, `UPDATE idenqa.review_cases SET state='claimed',assigned_reviewer='reviewer-1',version=2,updated_at=$3
			WHERE tenant_id=$1 AND id=$2 AND version=1 AND state='open'`, firstTenant.String(), caseID.String(), now.Add(time.Second))
		if err != nil || claimed.RowsAffected() != 1 {
			t.Fatalf("first claim rows=%d error=%v", claimed.RowsAffected(), err)
		}
		stale, err := tx.Exec(ctx, `UPDATE idenqa.review_cases SET assigned_reviewer='reviewer-2' WHERE tenant_id=$1 AND id=$2 AND version=1 AND state='open'`, firstTenant.String(), caseID.String())
		if err != nil || stale.RowsAffected() != 0 {
			t.Fatalf("stale claim rows=%d error=%v", stale.RowsAffected(), err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.review_findings
			(tenant_id,case_id,id,reviewer_id,resolution,reason_code,evidence_grant_ids,recorded_at)
			VALUES ($1,$2,$3,'reviewer-1','satisfy','document_authentic','[]'::jsonb,$4)`, firstTenant.String(), caseID.String(), findingID.String(), now.Add(2*time.Second)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	assertHistoryMutationRejected(t, runtime, firstTenant.String(), findingID.String())
	assertPrivacyReviewHidden(t, runtime, secondTenant.String())
}

func TestPrivacyCoordinationDiscoversOnlyDueWorkAcrossTenantRLS(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ids, _ := id.NewSystemGenerator()
	now := time.Now().UTC().Truncate(time.Second)
	tenantID, verificationID := seedExecutionVerification(t, admin, ids, now)
	dueID, _ := ids.NewDeletion()
	heldID, _ := ids.NewDeletion()
	futureID, _ := ids.NewDeletion()
	holdID, _ := ids.NewLegalHold()
	err = admin.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.deletion_requests
			(tenant_id,id,aggregate_id,region,state,backup_expires_at,version,requested_at,updated_at)
			VALUES ($1,$2,$3,'ng-1','requested',$4,3,$5,$5),
			       ($1,$6,$3,'ng-1','blocked_by_legal_hold',$4,4,$5,$5),
			       ($1,$7,$3,'ng-1','awaiting_backup_expiry',$8,5,$5,$5)`,
			tenantID.String(), dueID.String(), verificationID.String(), now.Add(35*24*time.Hour), now.Add(-time.Hour), heldID.String(), futureID.String(), now.Add(time.Hour)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.legal_holds
			(tenant_id,id,aggregate_id,authority,reason,starts_at,review_at,created_at)
			VALUES ($1,$2,$3,'court-order','active hold',$4,$5,$6)`, tenantID.String(), holdID.String(), verificationID.String(), now.Add(-2*time.Hour), now.Add(24*time.Hour), now.Add(-3*time.Hour))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}

	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	store, err := privacypostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := store.ListDueDeletions(ctx, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].TenantID != tenantID || targets[0].DeletionID != dueID || targets[0].Version != 3 || !targets[0].DueAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("unexpected due targets: %#v", targets)
	}
}

func TestEvidenceDeletionMarksMetadataAfterExactObjectRemoval(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ids, _ := id.NewSystemGenerator()
	now := time.Now().UTC().Truncate(time.Second)
	tenantID, verificationID := seedExecutionVerification(t, admin, ids, now)
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	scope, _ := tenant.NewScope(tenantID)
	objects, err := localobjects.Open(localobjects.Config{Directory: t.TempDir(), MaxObjectBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	defer objects.Close()
	key, _ := objectstore.NewKey("tenants/" + tenantID.String() + "/evidence/probe/content/1")
	object, err := objects.Put(ctx, key, func(writer io.Writer) error { _, err := io.WriteString(writer, "ciphertext"); return err })
	if err != nil {
		t.Fatal(err)
	}
	evidenceID, _ := ids.NewEvidence()
	subjectIdentifier, _ := ids.NewSubject()
	subjectID := subjectIdentifier.String()
	err = runtime.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if err := setIntegrationTenantScope(ctx, tx, tenantID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.subjects(id,tenant_id,verification_id,created_at) VALUES($1,$2,$3,$4)`, subjectID, tenantID.String(), verificationID.String(), now); err != nil {
			return err
		}
		record := object.Record()
		digest := "sha256:" + strings.Repeat("a", 64)
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.evidence_assets
			(id,tenant_id,subject_id,verification_id,requirement_key,evidence_type,artefact,acquisition_method,assurances,
			 registry_schema_version,registry_revision,registry_digest,region,retention_class,content_revision,
			 object_key,object_version,ciphertext_size,ciphertext_checksum,envelope_format_version,content_algorithm,key_purpose,
			 key_provider,key_reference,key_version,key_algorithm,wrapped_key,context_schema_version,context_digest,plaintext_digest,
			 media_type,integrity,state,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,'selfie','selfie','image','upload',ARRAY[]::text[],1,1,$5,'ng-1','raw_evidence',1,
			$6,$7,$8,$9,1,'test.aead','evidence.content','test.provider','key.reference','v1','test.wrap',$10,1,$5,$5,
			'image/jpeg','verified','available',1,$11,$11)`, evidenceID.String(), tenantID.String(), subjectID, verificationID.String(), digest,
			record.Key, record.Version, record.Size, record.Checksum, []byte{1}, now)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.evidence_asset_audit(tenant_id,evidence_id,aggregate_version,action,occurred_at) VALUES($1,$2,1,'create',$3)`, tenantID.String(), evidenceID.String(), now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	privacyStore, _ := privacypostgres.New(runtime)
	targets, err := privacyStore.EvidenceTargets(ctx, scope, verificationID.String(), "ng-1")
	if err != nil || len(targets) != 1 {
		t.Fatalf("targets=%d error=%v", len(targets), err)
	}
	eraser, err := privacypostgres.NewEvidenceEraser(runtime, objects, func() time.Time { return now.Add(time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	if err := eraser.Delete(ctx, targets[0]); err != nil {
		t.Fatal(err)
	}
	if err := eraser.Delete(ctx, targets[0]); err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if _, err := objects.Open(ctx, object); err == nil {
		t.Fatal("deleted ciphertext remains readable")
	}
	err = admin.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		var state string
		var audit int
		if err := tx.QueryRow(ctx, `SELECT state FROM idenqa.evidence_assets WHERE tenant_id=$1 AND id=$2`, tenantID.String(), evidenceID.String()).Scan(&state); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.evidence_asset_audit WHERE tenant_id=$1 AND evidence_id=$2 AND action='delete'`, tenantID.String(), evidenceID.String()).Scan(&audit); err != nil {
			return err
		}
		if state != "deleted" || audit != 1 {
			t.Fatalf("state=%s delete audit=%d", state, audit)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPrivacyAndReviewAdaptersCommitAtomicAudit(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ids, _ := id.NewSystemGenerator()
	now := time.Now().UTC().Truncate(time.Second)
	tenantID, verificationID := seedExecutionVerification(t, admin, ids, now)
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	scope, _ := tenant.NewScope(tenantID)
	decision := newIntegrationDecision(t, ids, tenantID, verificationID, id.Decision{}, now)
	decisionStore, _ := policypostgres.New(runtime)
	if err := decisionStore.Append(ctx, scope, decision); err != nil {
		t.Fatal(err)
	}

	privacyStore, err := privacypostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	clockNow := now
	eraser := &integrationEraser{}
	privacyService, err := privacy.NewService(privacyStore, eraser, ids, func() time.Time { return clockNow })
	if err != nil {
		t.Fatal(err)
	}
	privacyActor := privacy.Actor{ID: "operator", Permissions: []privacy.Permission{privacy.PermissionRequestDeletion, privacy.PermissionRunDeletion, privacy.PermissionManageHold}}
	deletion, err := privacyService.RequestDeletion(ctx, scope, privacyActor, verificationID.String(), "ng-1", []privacy.Target{{Kind: "raw", Reference: "object-1", Region: "ng-1"}, {Kind: "derived", Reference: "object-2", Region: "ng-1"}}, now.Add(35*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	hold, err := privacyService.CreateHold(ctx, scope, privacyActor, verificationID.String(), "court", "case-1", now, now.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := privacyService.Run(ctx, scope, privacyActor, deletion.ID); !errors.Is(err, privacy.ErrHeld) {
		t.Fatalf("held run error=%v", err)
	}
	clockNow = now.Add(time.Hour)
	if _, err := privacyService.ReleaseHold(ctx, scope, privacyActor, hold.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := privacyService.Run(ctx, scope, privacyActor, deletion.ID); err != nil {
		t.Fatal(err)
	}
	clockNow = now.Add(36 * 24 * time.Hour)
	completed, err := privacyService.Run(ctx, scope, privacyActor, deletion.ID)
	if err != nil || completed.State != privacy.DeletionCompleted {
		t.Fatalf("complete=%s error=%v", completed.State, err)
	}
	if len(eraser.targets) != 2 {
		t.Fatalf("erased targets=%d", len(eraser.targets))
	}
	if replayed, err := privacyService.ReplayTombstones(ctx, scope, privacyActor, 10); err != nil || replayed != 1 {
		t.Fatalf("replayed=%d error=%v", replayed, err)
	}

	reviewStore, err := reviewpostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	reviewKey, err := ids.NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	reviewAuthority, err := review.NewRegistry([]review.Assignment{{TenantID: scope.ID().String(), APIKeyID: reviewKey.String(), OperatorID: "reviewer-1", Permissions: []review.Permission{review.PermissionClaim}, Certifications: []string{"document.level2"}, Regions: []string{"ng-1"}, NotBefore: clockNow.Add(-time.Hour), ExpiresAt: clockNow.Add(time.Hour)}})
	if err != nil {
		t.Fatal(err)
	}
	reviewService, err := review.NewAuthorizedService(reviewStore, ids, func() time.Time { return clockNow }, reviewAuthority)
	if err != nil {
		t.Fatal(err)
	}
	reviewActor := review.Actor{ID: reviewKey.String()}
	caseValue, err := reviewService.OpenCase(ctx, scope, reviewActor, verificationID, decision.ID(), "ng-1", "document.level2", review.OversightDual)
	if err != nil {
		t.Fatal(err)
	}
	clockNow = clockNow.Add(time.Second)
	claimed, err := reviewService.Claim(ctx, scope, reviewActor, caseValue.ID, caseValue.Version)
	if err != nil || claimed.State != review.CaseClaimed {
		t.Fatalf("claim=%s error=%v", claimed.State, err)
	}

	err = runtime.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if err := setIntegrationTenantScope(ctx, tx, tenantID.String()); err != nil {
			return err
		}
		var records, tombstones int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.audit_records WHERE tenant_id=$1`, tenantID.String()).Scan(&records); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.deletion_tombstones WHERE tenant_id=$1`, tenantID.String()).Scan(&tombstones); err != nil {
			return err
		}
		if records < 10 || tombstones != 1 {
			t.Fatalf("audit records=%d tombstones=%d", records, tombstones)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

type integrationEraser struct{ targets []privacy.Target }

func (eraser *integrationEraser) Delete(_ context.Context, target privacy.Target) error {
	eraser.targets = append(eraser.targets, target)
	return nil
}

func assertHistoryMutationRejected(t *testing.T, pool *idenqapostgres.Pool, tenantID, findingID string) {
	t.Helper()
	err := pool.WithinTransaction(t.Context(), idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if err := setIntegrationTenantScope(ctx, tx, tenantID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE idenqa.review_findings SET reason_code='changed' WHERE id=$1", findingID)
		return err
	})
	if err == nil {
		t.Fatal("immutable review finding accepted mutation")
	}
}

func assertPrivacyReviewHidden(t *testing.T, pool *idenqapostgres.Pool, tenantID string) {
	t.Helper()
	err := pool.WithinTransaction(t.Context(), idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if err := setIntegrationTenantScope(ctx, tx, tenantID); err != nil {
			return err
		}
		for _, table := range []string{"retention_bindings", "deletion_requests", "review_cases", "review_findings"} {
			var count int
			if err := tx.QueryRow(ctx, "SELECT count(*) FROM idenqa."+table).Scan(&count); err != nil {
				return err
			}
			if count != 0 {
				t.Fatalf("%s crossed tenant scope: %d", table, count)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func setIntegrationTenantScope(ctx context.Context, tx idenqapostgres.Transaction, tenantID string) error {
	var selected string
	return tx.QueryRow(ctx, "SELECT set_config('idenqa.tenant_id',$1,true)", tenantID).Scan(&selected)
}

func sixtyFour(character byte) string { return string(makeFilled(64, character)) }

func makeFilled(length int, character byte) []byte {
	result := make([]byte, length)
	for index := range result {
		result[index] = character
	}
	return result
}
