//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/privacy"
	privacypostgres "github.com/Mujhtech/idenqa/internal/privacy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/tenantexport"
	tenantexportpostgres "github.com/Mujhtech/idenqa/internal/tenantexport/postgres"
)

type unusedDecisionRepository struct{}

func (unusedDecisionRepository) Append(context.Context, tenant.Scope, policy.Decision) error {
	return errors.New("not used")
}
func (unusedDecisionRepository) Find(context.Context, tenant.Scope, id.Decision) (policy.Decision, error) {
	return policy.Decision{}, errors.New("not used")
}
func (unusedDecisionRepository) FindLatest(context.Context, tenant.Scope, id.Verification) (policy.Decision, error) {
	return policy.Decision{}, errors.New("not used")
}

type integrationSubjectBundle struct {
	exporter *tenantexport.SubjectExporter
}

func (bundle integrationSubjectBundle) ExportSubjectBundle(ctx context.Context, scope tenant.Scope, subjectID string, structured bool) (privacy.SubjectBundle, error) {
	selection := tenantexport.AccessSubjectCollections()
	if structured {
		selection = tenantexport.PortabilitySubjectCollections()
	}
	result, err := bundle.exporter.Export(ctx, scope, subjectID, selection, func([]byte) error { return nil })
	if err != nil {
		return privacy.SubjectBundle{}, err
	}
	return privacy.SubjectBundle{Digest: result.Digest, Bytes: result.Bytes}, nil
}

type integrationSubjectDeletion struct {
	service *privacy.Service
	now     func() time.Time
}

func (deletion integrationSubjectDeletion) RequestSubjectDeletion(ctx context.Context, scope tenant.Scope, actor privacy.Actor, subjectID, region string) (string, error) {
	requestedAt := deletion.now().UTC()
	deletionRequest, err := deletion.service.RequestDeletion(ctx, scope, privacy.Actor{ID: actor.ID, Permissions: []privacy.Permission{privacy.PermissionRequestDeletion}}, subjectID, region,
		[]privacy.Target{{Kind: "identity_subject", Reference: "subject:" + subjectID, Region: region}}, requestedAt.Add(privacy.SelectedDefaults()[privacy.DataClassBackup]))
	if err != nil {
		return "", err
	}
	return deletionRequest.ID.String(), nil
}

type requestTestClock struct{ now time.Time }

func (clock *requestTestClock) Now() time.Time { return clock.now }

func seedIdentitySubject(t *testing.T, pool *idenqapostgres.Pool, generator *id.Generator, tenantID id.Tenant, verificationID id.Verification, now time.Time) id.Subject {
	t.Helper()
	subjectID, err := generator.NewSubject()
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := generator.NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Native().Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := tx.Exec(t.Context(), `INSERT INTO idenqa.api_keys
		(id,tenant_id,label,digest,pepper_version,requested_scopes,resolved_scopes,version,created_at,updated_at)
		VALUES ($1,$2,'fixture',$3,1,ARRAY['subjects:*'],ARRAY['subjects:read'],1,$4,$4)`,
		keyID.String(), tenantID.String(), bytes.Repeat([]byte{'a'}, 32), now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO idenqa.identity_subjects
		(tenant_id,id,region,state,version,created_at,updated_at) VALUES ($1,$2,'ng-1','active',1,$3,$3)`,
		tenantID.String(), subjectID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO idenqa.identity_subject_verifications
		(tenant_id,subject_id,verification_id,actor_key_id,linked_at) VALUES ($1,$2,$3,$4,$5)`,
		tenantID.String(), subjectID.String(), verificationID.String(), keyID.String(), now); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	return subjectID
}

// TestPrivacyRequestWorkflowIntegration proves tenant-scoped persistence,
// decision and expiry semantics, subject-scoped export digests, erasure
// deletion with legal holds, restriction gating, replay idempotency, the
// closed subject projection, and forced row-level security isolation.
func TestPrivacyRequestWorkflowIntegration(t *testing.T) {
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
	firstTenant, firstVerification := seedExecutionVerification(t, admin, ids, now)
	secondTenant, _ := seedExecutionVerification(t, admin, ids, now)
	subjectID := seedIdentitySubject(t, admin, ids, firstTenant, firstVerification, now)

	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	firstScope, _ := tenant.NewScope(firstTenant)
	secondScope, _ := tenant.NewScope(secondTenant)

	privacyStore, err := privacypostgres.New(runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	exportStore, err := tenantexportpostgres.New(runtime, unusedDecisionRepository{})
	if err != nil {
		t.Fatal(err)
	}
	subjectExporter, err := tenantexport.NewSubjectExporter(exportStore, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	clock := &requestTestClock{now: now}
	eraser := &integrationEraser{}
	deletionService, err := privacy.NewService(privacyStore, eraser, ids, clock.Now)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := privacy.NewDispatcher(
		integrationSubjectBundle{exporter: subjectExporter},
		integrationSubjectDeletion{service: deletionService, now: clock.Now},
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	service, err := privacy.NewRequestService(privacyStore, privacyStore, privacyStore, privacyStore, dispatcher, ids, clock.Now, privacy.SelectedRequestConfig())
	if err != nil {
		t.Fatal(err)
	}
	writer := privacy.Actor{ID: "key_writer", Permissions: []privacy.Permission{privacy.PermissionWritePrivacyRequests, privacy.PermissionReadPrivacyRequests}}
	approver := privacy.Actor{ID: "key_approver", Permissions: []privacy.Permission{privacy.PermissionApprovePrivacyRequests, privacy.PermissionReadPrivacyRequests}}

	t.Run("access approve executes subject export with digest", func(t *testing.T) {
		request, err := service.Create(ctx, firstScope, writer, privacy.CreateRequestInput{Type: privacy.RequestAccess, SubjectID: subjectID.String(), Region: "ng-1", Payload: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		approved, err := service.Decide(ctx, firstScope, approver, request.ID, privacy.OutcomeApproved, privacy.ReasonAccessApproved, request.Version+1)
		if err != nil || approved.State != privacy.RequestStateApproved {
			t.Fatalf("approve = %+v, %v", approved, err)
		}
		completed, err := service.Execute(ctx, firstScope, approver, request.ID, approved.Version)
		if err != nil || completed.State != privacy.RequestStateCompleted || completed.EffectKind != "subject_export" {
			t.Fatalf("execute = %+v, %v", completed, err)
		}
		if len(completed.EffectDigest) != len("sha256:")+64 || completed.EffectDigest[:7] != "sha256:" {
			t.Fatalf("effect digest = %q", completed.EffectDigest)
		}
		direct, err := integrationSubjectBundle{exporter: subjectExporter}.ExportSubjectBundle(ctx, firstScope, subjectID.String(), false)
		if err != nil || direct.Digest != completed.EffectDigest {
			t.Fatalf("direct export = %+v, %v; workflow digest = %q", direct, err, completed.EffectDigest)
		}
		portability, err := service.Create(ctx, firstScope, writer, privacy.CreateRequestInput{Type: privacy.RequestPortability, SubjectID: subjectID.String(), Region: "ng-1", Payload: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		approved, err = service.Decide(ctx, firstScope, approver, portability.ID, privacy.OutcomeApproved, privacy.ReasonPortabilityApproved, portability.Version+1)
		if err != nil {
			t.Fatal(err)
		}
		structured, err := service.Execute(ctx, firstScope, approver, portability.ID, approved.Version)
		if err != nil || structured.State != privacy.RequestStateCompleted {
			t.Fatalf("portability = %+v, %v", structured, err)
		}
		directStructured, err := integrationSubjectBundle{exporter: subjectExporter}.ExportSubjectBundle(ctx, firstScope, subjectID.String(), true)
		if err != nil || directStructured.Digest != structured.EffectDigest {
			t.Fatalf("structured digest = %+v, %v", directStructured, err)
		}
		if structured.EffectDigest == completed.EffectDigest {
			t.Fatal("structured subset digest matched the full access bundle")
		}
	})

	t.Run("replay adds nothing", func(t *testing.T) {
		request, err := service.Create(ctx, firstScope, writer, privacy.CreateRequestInput{Type: privacy.RequestAccess, SubjectID: subjectID.String(), Region: "ng-1", Payload: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		approved, err := service.Decide(ctx, firstScope, approver, request.ID, privacy.OutcomeApproved, privacy.ReasonAccessApproved, request.Version+1)
		if err != nil {
			t.Fatal(err)
		}
		replayApproval, err := service.Decide(ctx, firstScope, approver, request.ID, privacy.OutcomeApproved, privacy.ReasonAccessApproved, approved.Version)
		if err != nil || replayApproval.Version != approved.Version {
			t.Fatalf("approval replay = %+v, %v", replayApproval, err)
		}
		if _, err := service.Decide(ctx, firstScope, approver, request.ID, privacy.OutcomeDenied, privacy.ReasonIdentityUnverified, approved.Version); !errors.Is(err, privacy.ErrConflict) {
			t.Fatalf("conflicting decision error = %v", err)
		}
		completed, err := service.Execute(ctx, firstScope, approver, request.ID, approved.Version)
		if err != nil {
			t.Fatal(err)
		}
		before, err := countRequestEvents(ctx, runtime, firstTenant.String(), request.ID.String())
		if err != nil {
			t.Fatal(err)
		}
		replayed, err := service.Execute(ctx, firstScope, approver, request.ID, completed.Version)
		if err != nil || replayed.Version != completed.Version || replayed.EffectDigest != completed.EffectDigest {
			t.Fatalf("replay = %+v, %v", replayed, err)
		}
		after, err := countRequestEvents(ctx, runtime, firstTenant.String(), request.ID.String())
		if err != nil {
			t.Fatal(err)
		}
		if before != after {
			t.Fatalf("replay added events: before=%d after=%d", before, after)
		}
	})

	t.Run("erasure respects legal holds and completes", func(t *testing.T) {
		request, err := service.Create(ctx, firstScope, writer, privacy.CreateRequestInput{Type: privacy.RequestErasure, SubjectID: subjectID.String(), Region: "ng-1", Payload: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		approved, err := service.Decide(ctx, firstScope, approver, request.ID, privacy.OutcomeApproved, privacy.ReasonErasureApproved, request.Version+1)
		if err != nil {
			t.Fatal(err)
		}
		completed, err := service.Execute(ctx, firstScope, approver, request.ID, approved.Version)
		if err != nil || completed.EffectKind != "deletion_request" {
			t.Fatalf("erasure execute = %+v, %v", completed, err)
		}
		deletionID, err := id.ParseDeletion(completed.EffectRef)
		if err != nil {
			t.Fatalf("effect reference %q is not a deletion id: %v", completed.EffectRef, err)
		}
		holdActor := privacy.Actor{ID: "key_approver", Permissions: []privacy.Permission{privacy.PermissionManageHold}}
		deletionActor := privacy.Actor{ID: "key_approver", Permissions: []privacy.Permission{privacy.PermissionRunDeletion}}
		hold, err := deletionService.CreateHold(ctx, firstScope, holdActor, subjectID.String(), "court-order", "pending-proceedings", now, now.Add(24*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := deletionService.Run(ctx, firstScope, deletionActor, deletionID); !errors.Is(err, privacy.ErrHeld) {
			t.Fatalf("held run error = %v", err)
		}
		clock.now = now.Add(time.Hour)
		if _, err := deletionService.ReleaseHold(ctx, firstScope, holdActor, hold.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := deletionService.Run(ctx, firstScope, deletionActor, deletionID); err != nil {
			t.Fatal(err)
		}
		clock.now = now.Add(36 * 24 * time.Hour)
		finished, err := deletionService.Run(ctx, firstScope, deletionActor, deletionID)
		if err != nil || finished.State != privacy.DeletionCompleted {
			t.Fatalf("deletion = %+v, %v", finished, err)
		}
	})

	t.Run("restriction blocks new processing and lifts", func(t *testing.T) {
		request, err := service.Create(ctx, firstScope, writer, privacy.CreateRequestInput{Type: privacy.RequestRestriction, SubjectID: subjectID.String(), Region: "ng-1", Payload: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		approved, err := service.Decide(ctx, firstScope, approver, request.ID, privacy.OutcomeApproved, privacy.ReasonRestrictionApproved, request.Version+1)
		if err != nil {
			t.Fatal(err)
		}
		executed, err := service.Execute(ctx, firstScope, approver, request.ID, approved.Version)
		if err != nil || executed.EffectKind != "restriction" {
			t.Fatalf("restriction execute = %+v, %v", executed, err)
		}
		blocked, err := service.Blocked(ctx, firstScope, subjectID.String())
		if err != nil || !blocked {
			t.Fatalf("Blocked() = %v, %v", blocked, err)
		}
		restrictions, err := service.Restrictions(ctx, firstScope, writer, subjectID.String(), "", 10)
		if err != nil || len(restrictions) != 1 {
			t.Fatalf("restrictions = %+v, %v", restrictions, err)
		}
		if _, err := service.LiftRestriction(ctx, firstScope, approver, restrictions[0].ID, privacy.RestrictionLiftedByTenant, restrictions[0].Version); err != nil {
			t.Fatal(err)
		}
		blocked, err = service.Blocked(ctx, firstScope, subjectID.String())
		if err != nil || blocked {
			t.Fatalf("Blocked after lift = %v, %v", blocked, err)
		}
	})

	t.Run("outcome channel cannot decide and projection is closed", func(t *testing.T) {
		authority := privacy.SubjectAuthority{Scope: firstScope, VerificationID: firstVerification}
		projection, err := service.CreateSubject(ctx, authority, privacy.CreateRequestInput{Type: privacy.RequestAccess, SubjectID: subjectID.String(), Region: "ng-1", Payload: []byte(`{}`)})
		if err != nil || projection.Status != privacy.SubjectStatusReceived {
			t.Fatalf("CreateSubject = %+v, %v", projection, err)
		}
		page, err := service.SubjectList(ctx, authority, "", 10)
		if err != nil || len(page.Requests) != 1 {
			t.Fatalf("SubjectList = %+v, %v", page, err)
		}
		projection = page.Requests[0]
		if projection.Type == "" || projection.Status == "" || projection.RequestedAt.IsZero() || projection.UpdatedAt.IsZero() {
			t.Fatalf("projection = %+v", projection)
		}
		subjectActor := privacy.Actor{ID: "subject:" + firstVerification.String(), Permissions: []privacy.Permission{privacy.PermissionWritePrivacyRequests}}
		requests, err := service.List(ctx, firstScope, writer, privacy.RequestFilter{}, "", 5)
		if err != nil || len(requests.Requests) == 0 {
			t.Fatalf("tenant list = %+v, %v", requests, err)
		}
		if _, err := service.Decide(ctx, firstScope, subjectActor, requests.Requests[0].ID, privacy.OutcomeApproved, privacy.ReasonAccessApproved, requests.Requests[0].Version); !errors.Is(err, privacy.ErrConflict) {
			t.Fatalf("subject decide error = %v", err)
		}
		if _, err := service.Execute(ctx, firstScope, subjectActor, requests.Requests[0].ID, requests.Requests[0].Version); !errors.Is(err, privacy.ErrConflict) {
			t.Fatalf("subject execute error = %v", err)
		}
	})

	t.Run("expiry advances undecided requests", func(t *testing.T) {
		expires := clock.now.Add(time.Minute)
		request, err := service.Create(ctx, firstScope, writer, privacy.CreateRequestInput{Type: privacy.RequestAccess, SubjectID: subjectID.String(), Region: "ng-1", Payload: []byte(`{}`), ExpiresAt: &expires})
		if err != nil {
			t.Fatal(err)
		}
		clock.now = expires.Add(time.Second)
		expired, err := service.ExpireDue(ctx, firstScope, writer, 10)
		if err != nil || len(expired) != 1 || expired[0].ID != request.ID || expired[0].State != privacy.RequestStateExpired {
			t.Fatalf("ExpireDue = %+v, %v", expired, err)
		}
		if _, err := service.Withdraw(ctx, firstScope, writer, request.ID, request.Version); !errors.Is(err, privacy.ErrConflict) {
			t.Fatalf("terminal withdraw error = %v", err)
		}
	})

	t.Run("row level security isolates tenants", func(t *testing.T) {
		foreign, err := service.Create(ctx, secondScope, writer, privacy.CreateRequestInput{Type: privacy.RequestAccess, SubjectID: subjectID.String(), Region: "ng-1", Payload: []byte(`{}`)})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Find(ctx, firstScope, writer, foreign.ID); !errors.Is(err, privacy.ErrInvalid) {
			t.Fatalf("cross-tenant find error = %v", err)
		}
		own, err := service.List(ctx, firstScope, writer, privacy.RequestFilter{}, "", 100)
		if err != nil {
			t.Fatal(err)
		}
		for _, request := range own.Requests {
			if request.ID == foreign.ID {
				t.Fatalf("foreign request %s visible", foreign.ID)
			}
		}
	})
}

func countRequestEvents(ctx context.Context, pool *idenqapostgres.Pool, tenantID, requestID string) (int, error) {
	var count int
	err := pool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if err := setIntegrationTenantScope(ctx, tx, tenantID); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.privacy_request_events WHERE tenant_id=$1 AND request_id=$2`, tenantID, requestID).Scan(&count)
	})
	return count, err
}
