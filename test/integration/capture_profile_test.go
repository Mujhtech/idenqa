//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestCaptureProfilePersistenceIsolationLifecycleAndReplay(t *testing.T) {
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

	adminPool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()
	tenantStore, err := tenantpostgres.New(adminPool)
	if err != nil {
		t.Fatalf("new tenant store: %v", err)
	}
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("new identifier generator: %v", err)
	}
	tenantAdmin, err := tenant.NewAdmin(tenantStore, generator, clock.System{})
	if err != nil {
		t.Fatalf("new tenant admin: %v", err)
	}
	action := tenant.AdminAction{Actor: "integration-operator", Reason: "verify capture profiles"}
	firstTenant, err := tenantAdmin.Create(ctx, action)
	if err != nil {
		t.Fatalf("create first tenant: %v", err)
	}
	secondTenant, err := tenantAdmin.Create(ctx, action)
	if err != nil {
		t.Fatalf("create second tenant: %v", err)
	}

	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtimePool, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer runtimePool.Close()
	accessStore, err := accesspostgres.New(runtimePool)
	if err != nil {
		t.Fatalf("new access store: %v", err)
	}
	firstScope, err := tenant.NewScope(firstTenant.ID())
	if err != nil {
		t.Fatalf("new first scope: %v", err)
	}
	secondScope, err := tenant.NewScope(secondTenant.ID())
	if err != nil {
		t.Fatalf("new second scope: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	actor := newIntegrationKey(t, generator, firstTenant.ID(), now, id.APIKey{})
	if err := accessStore.Create(ctx, firstScope, actor); err != nil {
		t.Fatalf("create actor key: %v", err)
	}
	secondActor := newIntegrationKey(t, generator, secondTenant.ID(), now, id.APIKey{})
	if err := accessStore.Create(ctx, secondScope, secondActor); err != nil {
		t.Fatalf("create second actor key: %v", err)
	}

	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("BuiltInRegistry() error = %v", err)
	}
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatalf("NewCatalog() error = %v", err)
	}
	profileStore, err := verificationpostgres.New(runtimePool, catalog)
	if err != nil {
		t.Fatalf("new profile store: %v", err)
	}
	document := integrationProfileDocument(t, registry, evidence.MethodFileUpload)
	profileID, err := generator.NewProfile()
	if err != nil {
		t.Fatalf("NewProfile() id error = %v", err)
	}
	profile, draft, err := verification.NewCaptureProfile(
		profileID,
		firstTenant.ID(),
		"Standard identity",
		document,
		registry,
		now,
	)
	if err != nil {
		t.Fatalf("NewCaptureProfile() error = %v", err)
	}
	createRequest := integrationIdempotencyRequest(
		t,
		firstTenant.ID(),
		actor.ID(),
		verification.OperationCreateProfile,
		"create-standard",
		[]byte(`{"name":"Standard identity"}`),
		now,
	)
	createResult := integrationMutationResult(profile, draft)
	mutation := verification.Mutation{
		Kind:        verification.MutationCreate,
		Profile:     profile,
		Revision:    draft,
		Actor:       actor.ID(),
		Idempotency: createRequest,
		Result:      createResult,
		Status:      201,
	}
	created, err := profileStore.Apply(ctx, firstScope, mutation)
	if err != nil {
		t.Fatalf("Apply(create) error = %v", err)
	}
	replayed, err := profileStore.Apply(ctx, firstScope, mutation)
	if err != nil {
		t.Fatalf("Apply(replay) error = %v", err)
	}
	if replayed.ProfileID != created.ProfileID || replayed.Version != created.Version {
		t.Fatalf("replay = %+v, want %+v", replayed, created)
	}
	conflictingRequest := integrationIdempotencyRequest(
		t,
		firstTenant.ID(),
		actor.ID(),
		verification.OperationCreateProfile,
		"create-standard",
		[]byte(`{"name":"Changed"}`),
		now.Add(time.Second),
	)
	mutation.Idempotency = conflictingRequest
	if _, err := profileStore.Apply(ctx, firstScope, mutation); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("Apply(conflicting replay) error = %v, want ErrConflict", err)
	}

	found, err := profileStore.FindProfile(ctx, firstScope, profileID)
	if err != nil {
		t.Fatalf("FindProfile() error = %v", err)
	}
	if _, err := profileStore.FindProfile(ctx, secondScope, profileID); !errors.Is(err, verification.ErrProfileNotFound) {
		t.Fatalf("cross-tenant FindProfile() error = %v, want ErrProfileNotFound", err)
	}
	foundDraft, err := profileStore.FindRevision(ctx, firstScope, profileID, 1)
	if err != nil {
		t.Fatalf("FindRevision(draft) error = %v", err)
	}
	publishedProfile, published, _, err := found.PublishDraft(1, foundDraft, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("PublishDraft() error = %v", err)
	}
	publishRequest := integrationIdempotencyRequest(
		t,
		firstTenant.ID(),
		actor.ID(),
		verification.OperationPublishProfile,
		"publish-standard",
		[]byte(`{"expected_version":1}`),
		now.Add(time.Minute),
	)
	if _, err := profileStore.Apply(ctx, firstScope, verification.Mutation{
		Kind:            verification.MutationPublish,
		Profile:         publishedProfile,
		Revision:        published,
		ExpectedVersion: 1,
		Actor:           actor.ID(),
		Idempotency:     publishRequest,
		Result:          integrationMutationResult(publishedProfile, published),
		Status:          200,
	}); err != nil {
		t.Fatalf("Apply(publish) error = %v", err)
	}
	if err := profileStore.SaveDraft(ctx, firstScope, actor.ID(), publishedProfile, published, 2); !errors.Is(err, verification.ErrProfileConflict) {
		t.Fatalf("SaveDraft(published) error = %v, want ErrProfileConflict", err)
	}

	liveDocument := integrationProfileDocument(t, registry, evidence.MethodLiveCamera)
	supersedingProfile, nextDraft, err := publishedProfile.BeginSupersession(2, liveDocument, registry, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("BeginSupersession() error = %v", err)
	}
	supersedeRequest := integrationIdempotencyRequest(
		t,
		firstTenant.ID(),
		actor.ID(),
		verification.OperationSupersedeProfile,
		"supersede-standard",
		[]byte(`{"expected_version":2}`),
		now.Add(2*time.Minute),
	)
	if _, err := profileStore.Apply(ctx, firstScope, verification.Mutation{
		Kind:            verification.MutationSupersede,
		Profile:         supersedingProfile,
		Revision:        nextDraft,
		ExpectedVersion: 2,
		Actor:           actor.ID(),
		Idempotency:     supersedeRequest,
		Result:          integrationMutationResult(supersedingProfile, nextDraft),
		Status:          200,
	}); err != nil {
		t.Fatalf("Apply(supersede) error = %v", err)
	}
	withDraft, err := profileStore.FindProfile(ctx, firstScope, profileID)
	if err != nil {
		t.Fatalf("FindProfile(superseding) error = %v", err)
	}
	if withDraft.PublishedRevision() == nil || *withDraft.PublishedRevision() != 1 ||
		withDraft.DraftRevision() == nil || *withDraft.DraftRevision() != 2 {
		t.Fatalf("supersession was destructive: profile = %+v", withDraft)
	}
	storedDraft, err := profileStore.FindRevision(ctx, firstScope, profileID, 2)
	if err != nil {
		t.Fatalf("FindRevision(2) error = %v", err)
	}
	updatedProfile, updatedDraft, err := withDraft.UpdateDraft(
		3,
		"Standard identity live",
		storedDraft,
		liveDocument,
		registry,
		now.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatalf("UpdateDraft() error = %v", err)
	}
	if err := profileStore.SaveDraft(ctx, firstScope, actor.ID(), updatedProfile, updatedDraft, 3); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if err := profileStore.SaveDraft(ctx, firstScope, actor.ID(), updatedProfile, updatedDraft, 3); !errors.Is(err, verification.ErrProfileConflict) {
		t.Fatalf("stale SaveDraft() error = %v, want ErrProfileConflict", err)
	}

	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(
			ctx,
			"UPDATE idenqa.capture_profile_revisions SET document = '{}'::jsonb WHERE profile_id = $1 AND revision = 1",
			profileID.String(),
		)
		return err
	})
	if err == nil {
		t.Fatal("database allowed published revision content mutation")
	}

	var auditCount, idempotencyCount int
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.capture_profile_audit WHERE profile_id = $1", profileID.String()).Scan(&auditCount); err != nil {
			return err
		}

		return tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.idempotency_records WHERE tenant_id = $1", firstTenant.ID().String()).Scan(&idempotencyCount)
	})
	if err != nil {
		t.Fatalf("count capture profile records: %v", err)
	}
	if auditCount != 4 || idempotencyCount != 3 {
		t.Fatalf("audit/idempotency counts = %d/%d, want 4/3", auditCount, idempotencyCount)
	}
}

func integrationProfileDocument(
	t *testing.T,
	registry evidence.Registry,
	method evidence.Name,
) verification.Profile {
	t.Helper()

	document, err := verification.NewProfile(registry, []verification.Requirement{{
		Key:          "selfie",
		Purpose:      evidence.PurposeIdentityVerification,
		EvidenceType: evidence.EvidenceSelfieImage,
		Artefacts:    []evidence.Name{evidence.ArtefactSelfieImage},
		Acquisition: verification.Acquisition{
			Strategy: verification.StrategyAnyOf,
			Methods:  []evidence.Name{method},
		},
	}})
	if err != nil {
		t.Fatalf("NewProfile() error = %v", err)
	}

	return document
}

func integrationIdempotencyRequest(
	t *testing.T,
	tenantID id.Tenant,
	actor id.APIKey,
	operation string,
	key string,
	canonical []byte,
	now time.Time,
) idempotency.Request {
	t.Helper()

	request, err := idempotency.NewRequest(
		tenantID,
		actor,
		operation,
		key,
		canonical,
		now,
		24*time.Hour,
	)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}

	return request
}

func integrationMutationResult(
	profile verification.CaptureProfile,
	revision verification.Revision,
) verification.MutationResult {
	return verification.MutationResult{
		ProfileID:         profile.ID().String(),
		Name:              profile.Name(),
		State:             profile.State(),
		Version:           profile.Version(),
		LatestRevision:    profile.LatestRevision(),
		DraftRevision:     profile.DraftRevision(),
		PublishedRevision: profile.PublishedRevision(),
		Revision:          revision.Number(),
		Digest:            revision.Digest(),
		UpdatedAt:         profile.UpdatedAt(),
	}
}
