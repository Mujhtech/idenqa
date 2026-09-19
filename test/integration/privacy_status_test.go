//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/privacy"
	privacypostgres "github.com/Mujhtech/idenqa/internal/privacy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// TestPrivacyInspectionReadsAndRetryIdempotency proves tenant isolation, signed
// cursor paging, hold visibility, safe failed-target retry, and that the
// retention read model recomputes exactly the privacy.Resolve result under the
// restricted runtime role.
func TestPrivacyInspectionReadsAndRetryIdempotency(t *testing.T) {
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
	evidenceCreatedAt := now.Add(-48 * time.Hour)
	firstTenant, firstVerification := seedExecutionVerification(t, admin, ids, now)
	secondTenant, secondVerification := seedExecutionVerification(t, admin, ids, now)
	freeVerification := seedVerificationForTenant(t, admin, ids, firstTenant, now)

	heldDeletionID, _ := ids.NewDeletion()
	firstPageDeletionID, _ := ids.NewDeletion()
	secondPageDeletionID, _ := ids.NewDeletion()
	thirdPageDeletionID, _ := ids.NewDeletion()
	foreignDeletionID, _ := ids.NewDeletion()
	holdID, _ := ids.NewLegalHold()
	rawEvidenceID, _ := ids.NewEvidence()
	derivedEvidenceID, _ := ids.NewEvidence()
	subjectID, _ := ids.NewSubject()
	digest := "sha256:" + sixtyFour('a')
	err = admin.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.subjects(id,tenant_id,verification_id,created_at) VALUES($1,$2,$3,$4)`,
			subjectID.String(), firstTenant.String(), firstVerification.String(), evidenceCreatedAt); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.deletion_requests
			(tenant_id,id,aggregate_id,region,state,backup_expires_at,failure_class,version,requested_at,updated_at)
			VALUES ($1,$2,$3,'ng-1','requested',$4,NULL,1,$5,$5),
			       ($1,$6,$7,'ng-1','requested',$4,NULL,1,$5,$5),
			       ($1,$8,$7,'ng-1','in_progress',$4,NULL,2,$5,$5),
			       ($1,$9,$7,'ng-1','requested',$4,NULL,1,$5,$5),
			       ($10,$11,$12,'ng-1','failed',$4,'target_unavailable',3,$5,$5)`,
			firstTenant.String(), heldDeletionID.String(), firstVerification.String(), now.Add(35*24*time.Hour), now,
			firstPageDeletionID.String(), freeVerification.String(), secondPageDeletionID.String(), thirdPageDeletionID.String(),
			secondTenant.String(), foreignDeletionID.String(), secondVerification.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.deletion_targets
			(tenant_id,deletion_id,kind,reference,region,attempts,deleted_at,last_failure_class)
			VALUES ($1,$2,'raw_evidence','raw-pending','ng-1',0,NULL,NULL),
			       ($1,$2,'derived_evidence','derived-failed','ng-1',2,NULL,'target_unavailable'),
			       ($1,$3,'raw_evidence','page-one','ng-1',0,NULL,NULL),
			       ($1,$4,'raw_evidence','page-two','ng-1',0,NULL,NULL),
			       ($1,$5,'raw_evidence','page-three','ng-1',0,NULL,NULL),
			       ($6,$7,'raw_evidence','foreign-target','ng-1',0,NULL,NULL)`,
			firstTenant.String(), heldDeletionID.String(), firstPageDeletionID.String(),
			secondPageDeletionID.String(), thirdPageDeletionID.String(), secondTenant.String(), foreignDeletionID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.legal_holds
			(tenant_id,id,aggregate_id,authority,reason,starts_at,review_at,created_at)
			VALUES ($1,$2,$3,'court-order','pending proceedings',$4,$5,$6)`,
			firstTenant.String(), holdID.String(), firstVerification.String(), now.Add(-2*time.Hour), now.Add(24*time.Hour), now.Add(-3*time.Hour)); err != nil {
			return err
		}
		for index, evidenceID := range []id.Evidence{rawEvidenceID, derivedEvidenceID} {
			class := "raw_evidence"
			if index == 1 {
				class = "derived_evidence"
			}
			if _, err := tx.Exec(ctx, `INSERT INTO idenqa.evidence_assets
				(id,tenant_id,subject_id,verification_id,requirement_key,evidence_type,artefact,acquisition_method,assurances,
				 registry_schema_version,registry_revision,registry_digest,region,retention_class,content_revision,
				 object_key,object_version,ciphertext_size,ciphertext_checksum,envelope_format_version,content_algorithm,key_purpose,
				 key_provider,key_reference,key_version,key_algorithm,wrapped_key,context_schema_version,context_digest,plaintext_digest,
				 media_type,integrity,state,version,created_at,updated_at)
				VALUES ($1,$2,$3,$4,'selfie','selfie','image','upload',ARRAY[]::text[],1,1,$5,'ng-1',$6,1,
				 $7,'v1',16,$5,1,'test.aead','evidence.content','test.provider','key.reference','v1','test.wrap',$8,1,$5,$5,
				 'image/jpeg','verified','available',1,$9,$9)`,
				evidenceID.String(), firstTenant.String(), subjectID.String(), firstVerification.String(), digest, class,
				"tenants/"+firstTenant.String()+"/evidence/"+evidenceID.String()+"/content/1", []byte{1}, evidenceCreatedAt); err != nil {
				return err
			}
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.retention_bindings
			(tenant_id,aggregate_id,data_class,region,retention_seconds,expires_at,policy_digest,created_at)
			VALUES ($1,$2,'raw_evidence','ng-1',$3,$4,$5,$6)`,
			firstTenant.String(), firstVerification.String(), int64(10*24*time.Hour/time.Second), now.Add(10*24*time.Hour), sixtyFour('b'), now)
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
	store, err := privacypostgres.New(runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	firstScope, _ := tenant.NewScope(firstTenant)
	secondScope, _ := tenant.NewScope(secondTenant)
	readActor := privacy.Actor{ID: "operator-read", Permissions: []privacy.Permission{privacy.PermissionReadDeletion}}
	retryActor := privacy.Actor{ID: "operator-run", Permissions: []privacy.Permission{privacy.PermissionRequestDeletion, privacy.PermissionRunDeletion, privacy.PermissionReadDeletion}}
	eraser := &retryEraser{counts: map[string]int{}}
	service, err := privacy.NewService(store, eraser, ids, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	heldStatus, err := service.DeletionStatus(ctx, firstScope, readActor, heldDeletionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(heldStatus.Holds) != 1 || heldStatus.Holds[0].ID != holdID {
		t.Fatalf("holds = %+v", heldStatus.Holds)
	}
	states := map[string]privacy.TargetStatus{}
	for _, target := range heldStatus.Targets {
		if target.Reference == "raw-pending" || target.Reference == "derived-failed" || len(target.Reference) != 24 {
			t.Fatalf("target reference was not digested: %+v", target)
		}
		states[target.Kind] = target
	}
	if states["raw_evidence"].State != privacy.TargetPending || states["derived_evidence"].State != privacy.TargetFailed ||
		states["derived_evidence"].FailureClass != "target_unavailable" {
		t.Fatalf("target states = %+v", heldStatus.Targets)
	}

	page, err := service.ListDeletions(ctx, firstScope, readActor, freeVerification.String(), "", 2)
	if err != nil || !page.HasMore || len(page.Deletions) != 2 {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	seen := map[id.Deletion]bool{page.Deletions[0].ID: true, page.Deletions[1].ID: true}
	next, err := service.ListDeletions(ctx, firstScope, readActor, freeVerification.String(), page.Deletions[1].ID.String(), 2)
	if err != nil || next.HasMore || len(next.Deletions) != 1 || seen[next.Deletions[0].ID] {
		t.Fatalf("next page = %+v, %v", next, err)
	}
	foreign, err := service.ListDeletions(ctx, secondScope, readActor, "", "", 25)
	if err != nil || len(foreign.Deletions) != 1 || foreign.Deletions[0].ID != foreignDeletionID {
		t.Fatalf("foreign page = %+v, %v", foreign, err)
	}
	if _, err := service.DeletionStatus(ctx, secondScope, readActor, heldDeletionID); !errors.Is(err, privacy.ErrInvalid) {
		t.Fatalf("cross-tenant status error = %v", err)
	}

	retryDeletion, err := service.RequestDeletion(ctx, firstScope, retryActor, freeVerification.String(), "ng-1",
		[]privacy.Target{{Kind: "raw_evidence", Reference: "retry-raw", Region: "ng-1"}, {Kind: "derived_evidence", Reference: "retry-derived", Region: "ng-1"}},
		now.Add(35*24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	eraser.failReference = "retry-derived"
	failed, runErr := service.Run(ctx, firstScope, retryActor, retryDeletion.ID)
	if runErr == nil || failed.State != privacy.DeletionFailed {
		t.Fatalf("failed run = %+v, %v", failed, runErr)
	}
	failedTargets := map[string]privacy.Target{}
	for _, target := range failed.Targets {
		failedTargets[target.Kind] = target
	}
	if !failedTargets["raw_evidence"].DeletedAt.IsZero() || !failedTargets["derived_evidence"].DeletedAt.IsZero() ||
		failedTargets["derived_evidence"].LastFailureClass == "" {
		t.Fatalf("failed targets = %+v", failed.Targets)
	}
	eraser.failReference = ""
	retried, err := service.Run(ctx, firstScope, retryActor, retryDeletion.ID)
	if err != nil || retried.State != privacy.DeletionAwaitingBackup {
		t.Fatalf("retry = %+v, %v", retried, err)
	}
	replayed, err := service.Run(ctx, firstScope, retryActor, retryDeletion.ID)
	if err != nil || replayed.State != privacy.DeletionAwaitingBackup || replayed.Version != retried.Version {
		t.Fatalf("replay = %+v, %v", replayed, err)
	}
	if eraser.counts["retry-raw"] != 1 || eraser.counts["retry-derived"] != 2 {
		t.Fatalf("eraser counts = %+v", eraser.counts)
	}

	resolution, err := service.ResolveRetention(ctx, firstScope, readActor, firstVerification.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(resolution.Holds) != 1 || resolution.Holds[0].ID != holdID {
		t.Fatalf("resolution holds = %+v", resolution.Holds)
	}
	if len(resolution.Records) != 2 {
		t.Fatalf("resolution records = %+v", resolution.Records)
	}
	rawExpected, err := privacy.Resolve(privacy.DataClassRawEvidence, "ng-1", evidenceCreatedAt, 10*24*time.Hour, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	derivedExpected, err := privacy.Resolve(privacy.DataClassDerivedEvidence, "ng-1", evidenceCreatedAt, 0, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]privacy.RetentionDeadline{
		rawEvidenceID.String():     {ID: rawEvidenceID.String(), Class: rawExpected.Class, Region: rawExpected.Region, Duration: rawExpected.Duration, ExpiresAt: rawExpected.ExpiresAt},
		derivedEvidenceID.String(): {ID: derivedEvidenceID.String(), Class: derivedExpected.Class, Region: derivedExpected.Region, Duration: derivedExpected.Duration, ExpiresAt: derivedExpected.ExpiresAt},
	}
	for _, record := range resolution.Records {
		want, ok := expected[record.ID]
		if !ok || record.Class != want.Class || record.Region != want.Region || record.Duration != want.Duration || !record.ExpiresAt.Equal(want.ExpiresAt) {
			t.Fatalf("record %s = %+v, want %+v", record.ID, record, want)
		}
	}
	foreignResolution, err := service.ResolveRetention(ctx, secondScope, readActor, firstVerification.String())
	if err != nil || len(foreignResolution.Records) != 0 || len(foreignResolution.Holds) != 0 {
		t.Fatalf("foreign resolution = %+v, %v", foreignResolution, err)
	}
}

func seedVerificationForTenant(t *testing.T, pool *idenqapostgres.Pool, generator *id.Generator, tenantID id.Tenant, now time.Time) id.Verification {
	t.Helper()
	profileID, _ := generator.NewProfile()
	verificationID, _ := generator.NewVerification()
	digest := "sha256:" + sixtyFour('c')
	tx, err := pool.Native().Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := tx.Exec(t.Context(), `INSERT INTO idenqa.capture_profiles
        (id, tenant_id, name, state, version, latest_revision, draft_revision, published_revision, created_at, updated_at)
        VALUES ($1, $2, 'Free fixture', 'active', 1, 1, NULL, 1, $3, $3)`, profileID.String(), tenantID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO idenqa.capture_profile_revisions
        (tenant_id, profile_id, revision, state, schema_version, registry_schema_version, registry_revision,
         registry_digest, document, digest, created_at, updated_at, published_at)
        VALUES ($1, $2, 1, 'published', 1, 1, 1, $3, '{}'::jsonb, $3, $4, $4, $4)`, tenantID.String(), profileID.String(), digest, now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO idenqa.verification_sessions
        (id, tenant_id, state, version, source_profile_id, source_profile_revision, source_profile_digest,
         requirements, created_at, updated_at, expires_at)
        VALUES ($1, $2, 'collecting', 1, $3, 1, $4, '{}'::jsonb, $5, $5, $6)`,
		verificationID.String(), tenantID.String(), profileID.String(), digest, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return verificationID
}

type retryEraser struct {
	failReference string
	counts        map[string]int
}

func (eraser *retryEraser) Delete(_ context.Context, target privacy.Target) error {
	eraser.counts[target.Reference]++
	if target.Reference == eraser.failReference {
		return errors.New("fixture unavailable")
	}
	return nil
}
