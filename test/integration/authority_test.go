//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/authority"
	authoritypostgres "github.com/Mujhtech/idenqa/internal/authority/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	tinkcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto/tink"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	objectlocal "github.com/Mujhtech/idenqa/internal/platform/objectstore/local"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestAuthorityPersistenceReplayIsolationAndImmutability(t *testing.T) {
	runAuthorityPersistence(t, nil)
}

// The optional exercise reuses the authorised fixture with two required steps.
func runAuthorityPersistence(t *testing.T, exercise func(captureAcceptanceFixture)) {
	runAuthorityPersistenceInRegion(t, exercise, "local", "tenant.region.ng")
}

func runAuthorityPersistenceInRegion(t *testing.T, exercise func(captureAcceptanceFixture), sessionRegion, evidenceRegion string, customise ...func(*verification.Profile)) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("open migrator: %v", err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("migrate authority database: %v", err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}

	adminPool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()
	now := time.Date(2026, time.September, 5, 12, 0, 0, 0, time.UTC)
	generator, err := id.NewGenerator(fixedIntegrationClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{17}, 8192)))
	if err != nil {
		t.Fatalf("new identifier generator: %v", err)
	}
	tenantStore, err := tenantpostgres.New(adminPool)
	if err != nil {
		t.Fatalf("new tenant store: %v", err)
	}
	tenantAdmin, err := tenant.NewAdmin(tenantStore, generator, fixedIntegrationClock{now: now})
	if err != nil {
		t.Fatalf("new tenant admin: %v", err)
	}
	action := tenant.AdminAction{Actor: "authority-integration", Reason: "verify authority persistence"}
	owner, err := tenantAdmin.Create(ctx, action)
	if err != nil {
		t.Fatalf("create owner tenant: %v", err)
	}
	other, err := tenantAdmin.Create(ctx, action)
	if err != nil {
		t.Fatalf("create other tenant: %v", err)
	}

	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtimePool, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer runtimePool.Close()
	ownerScope, err := tenant.NewScope(owner.ID())
	if err != nil {
		t.Fatalf("new owner scope: %v", err)
	}
	otherScope, err := tenant.NewScope(other.ID())
	if err != nil {
		t.Fatalf("new other scope: %v", err)
	}
	policyID := seedIntegrationPolicy(t, adminPool, owner.ID(), generator, now)
	actor := newIntegrationKey(t, generator, owner.ID(), now, id.APIKey{})
	accessStore, err := accesspostgres.New(runtimePool)
	if err != nil {
		t.Fatalf("new access store: %v", err)
	}
	if err := accessStore.Create(ctx, ownerScope, actor); err != nil {
		t.Fatalf("create authority actor: %v", err)
	}

	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("built-in registry: %v", err)
	}
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatalf("new catalog: %v", err)
	}
	profileStore, err := verificationpostgres.New(runtimePool, catalog)
	if err != nil {
		t.Fatalf("new profile store: %v", err)
	}
	profileID, err := generator.NewProfile()
	if err != nil {
		t.Fatalf("new profile id: %v", err)
	}
	document := integrationProfileDocument(t, registry, evidence.MethodLiveCamera)
	if exercise != nil {
		second := document.Requirements[0]
		second.Key = "selfie_secondary"
		document.Requirements = append(document.Requirements, second)
	}
	for _, apply := range customise {
		apply(&document)
	}
	profile, draft, err := verification.NewCaptureProfile(
		profileID, owner.ID(), "Authority profile",
		document, registry, now,
	)
	if err != nil {
		t.Fatalf("new profile: %v", err)
	}
	createProfile := integrationIdempotencyRequest(
		t, owner.ID(), actor.ID(), verification.OperationCreateProfile,
		"authority-profile-create", []byte(`{"name":"Authority profile"}`), now,
	)
	if _, err := profileStore.Apply(ctx, ownerScope, verification.Mutation{
		Kind: verification.MutationCreate, Profile: profile, Revision: draft, Actor: actor.ID(),
		Idempotency: createProfile, Result: integrationMutationResult(profile, draft), Status: 201,
	}); err != nil {
		t.Fatalf("create authority profile: %v", err)
	}
	published, revision, _, err := profile.PublishDraft(1, draft, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("publish authority profile: %v", err)
	}
	publishProfile := integrationIdempotencyRequest(
		t, owner.ID(), actor.ID(), verification.OperationPublishProfile,
		"authority-profile-publish", []byte(`{"expected_version":1}`), now.Add(time.Minute),
	)
	if _, err := profileStore.Apply(ctx, ownerScope, verification.Mutation{
		Kind: verification.MutationPublish, Profile: published, Revision: revision,
		ExpectedVersion: 1, Actor: actor.ID(), Idempotency: publishProfile,
		Result: integrationMutationResult(published, revision), Status: 200,
	}); err != nil {
		t.Fatalf("persist published authority profile: %v", err)
	}

	sessionStore, err := verificationpostgres.NewSessionStore(runtimePool, integrationProtector{}, catalog)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	sessionMutation := newIntegrationSessionMutation(
		t, generator, owner.ID(), actor.ID(), profileID, policyID, now.Add(2*time.Minute), "authority-session-create", sessionRegion,
	)
	created, err := sessionStore.Create(ctx, ownerScope, sessionMutation)
	if err != nil {
		t.Fatalf("create authority session: %v", err)
	}

	store, err := authoritypostgres.NewWithClock(runtimePool, integrationProtector{}, fixedIntegrationClock{now: now.Add(8 * time.Minute)})
	if err != nil {
		t.Fatalf("new authority store: %v", err)
	}
	noticeID, _ := generator.NewNotice()
	noticeEventID, _ := generator.NewEvent()
	notice, err := authority.NewNotice(authority.NoticeRecord{
		ID: noticeID, TenantID: owner.ID(), Key: "tenant.notice.identity_verification",
		Locale: "en-NG", Controller: "Example Controller", Recipient: "Example Recipient",
		Copy: authority.NoticeCopy{Title: "Identity verification", Summary: "We verify identity.",
			Purpose: "Identity verification only.", Consequences: "Collection stops if refused."},
		EffectiveAt: now, CreatedAt: now.Add(3 * time.Minute), CreatedBy: actor.ID(),
	})
	if err != nil {
		t.Fatalf("new notice: %v", err)
	}
	noticeRequest := integrationIdempotencyRequest(
		t, owner.ID(), actor.ID(), "notices.create", "authority-notice-create",
		[]byte(`{"key":"tenant.notice.identity_verification"}`), notice.CreatedAt(),
	)
	mutation := authority.NoticeMutation{Notice: notice, EventID: noticeEventID, Idempotency: noticeRequest}
	storedNotice, err := store.CreateNotice(ctx, ownerScope, mutation)
	if err != nil {
		t.Fatalf("create notice: %v", err)
	}
	replayedNotice, err := store.CreateNotice(ctx, ownerScope, mutation)
	if err != nil || replayedNotice.ID().String() != storedNotice.ID().String() {
		t.Fatalf("replay notice = %s, %v", replayedNotice.ID(), err)
	}
	if _, err := store.FindNotice(ctx, otherScope, notice.ID()); !errors.Is(err, authority.ErrNotFound) {
		t.Fatalf("cross-tenant notice lookup = %v, want ErrNotFound", err)
	}

	requirements := created.Session.Requirements().Requirements
	if len(requirements) != len(document.Requirements) {
		t.Fatalf("requirement count = %d, want %d", len(requirements), len(document.Requirements))
	}
	authorityID, _ := generator.NewAuthority()
	subjectID, _ := generator.NewSubject()
	authorityEventID, _ := generator.NewEvent()
	declaration, err := authority.New(authority.Record{
		ID: authorityID, TenantID: owner.ID(), SubjectID: subjectID,
		VerificationID: created.Session.ID(), NoticeID: notice.ID(),
		Category: "tenant.authority.customer_declared", Purpose: string(requirements[0].Purpose),
		Jurisdiction: "tenant.jurisdiction.ng", PolicyPack: "tenant.policy.identity_v1",
		IsConsentRequired: true, RequirementPurposes: []string{string(requirements[0].Purpose)},
		EvidenceTypes:      []string{string(requirements[0].EvidenceType)},
		RecipientReference: "tenant.recipient.primary", RecipientDisplayName: notice.Recipient(),
		Regions: []string{evidenceRegion}, RetentionReference: "tenant.retention.identity_v1",
		ValidFrom: now, ExpiresAt: created.Session.ExpiresAt(), CreatedAt: now.Add(4 * time.Minute),
		CreatedBy: actor.ID(),
	})
	if err != nil {
		t.Fatalf("new declaration: %v", err)
	}
	declarationRequest := integrationIdempotencyRequest(
		t, owner.ID(), actor.ID(), "authorities.declare", "authority-declare",
		[]byte(`{"verification":"`+created.Session.ID().String()+`"}`), declaration.Record().CreatedAt,
	)
	storedAuthority, err := store.Declare(ctx, ownerScope, authority.DeclarationMutation{
		Authority: declaration, EventID: authorityEventID, Idempotency: declarationRequest,
	})
	if err != nil {
		t.Fatalf("declare authority: %v", err)
	}
	if _, err := store.FindByVerification(ctx, otherScope, created.Session.ID()); !errors.Is(err, authority.ErrNotFound) {
		t.Fatalf("cross-tenant authority lookup = %v, want ErrNotFound", err)
	}

	responseID, _ := generator.NewAcknowledgement()
	responseEventID, _ := generator.NewEvent()
	response, err := authority.NewResponse(authority.ResponseRecord{
		ID: responseID, TenantID: owner.ID(), AuthorityID: storedAuthority.ID(), NoticeID: notice.ID(),
		SubjectID: storedAuthority.SubjectID(), VerificationID: created.Session.ID(),
		CaptureTokenID: created.Credential.ID(), Action: authority.ResponseConsent,
		Locale: notice.Locale(), RenderedExperienceVersion: "capture.notice.v1", RecordedAt: now.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatalf("new response: %v", err)
	}
	responseRequest, err := idempotency.NewRequest(
		owner.ID(), created.Credential.ID(), "authorities.respond", "authority-consent",
		[]byte(`{"action":"consent"}`), response.Record().RecordedAt, 24*time.Hour,
	)
	if err != nil {
		t.Fatalf("new response idempotency request: %v", err)
	}
	storedResponse, err := store.AppendResponse(ctx, ownerScope, authority.ResponseMutation{
		Response: response, EventID: responseEventID, Idempotency: responseRequest,
	})
	if err != nil {
		t.Fatalf("append response: %v", err)
	}
	snapshot, err := store.CaptureSnapshot(ctx, ownerScope, created.Session.ID())
	if err != nil || snapshot.Response == nil || snapshot.Response.Record().ID.String() != storedResponse.Record().ID.String() {
		t.Fatalf("capture snapshot response = %+v, error = %v", snapshot.Response, err)
	}
	if err := authority.Evaluate(storedAuthority, storedNotice, snapshot.Response, authority.GrantRequest{
		TenantID: owner.ID(), VerificationID: created.Session.ID(), SubjectID: storedAuthority.SubjectID(),
		Purpose: string(requirements[0].Purpose), EvidenceType: string(requirements[0].EvidenceType),
		RecipientReference: "tenant.recipient.primary", Region: evidenceRegion,
	}, now.Add(6*time.Minute)); err != nil {
		t.Fatalf("evaluate consented authority: %v", err)
	}

	if exercise != nil {
		exercise(captureAcceptanceFixture{
			admin: adminPool, runtime: runtimePool, database: database, scope: ownerScope, authorities: store,
			creation: created, registry: registry, catalog: catalog,
			creationMutation: sessionMutation,
			ids:              generator, declaration: storedAuthority, now: now.Add(8 * time.Minute),
		})
		return
	}
	evidenceStore, err := evidencepostgres.NewWithClock(runtimePool, integrationProtector{}, catalog, fixedIntegrationClock{now: now.Add(8 * time.Minute)})
	if err != nil {
		t.Fatalf("new evidence store: %v", err)
	}
	uploadHarness := assertEvidenceUploadPersistence(
		t, adminPool, store, evidenceStore, sessionStore, generator, ownerScope, otherScope,
		created, registry, requirements[0], now.Add(6*time.Minute),
	)
	keyring, err := localkms.Create(filepath.Join(t.TempDir(), "evidence-keyring.json"))
	if err != nil {
		t.Fatalf("create evidence keyring: %v", err)
	}
	defer func() {
		if err := keyring.Close(); err != nil {
			t.Errorf("close evidence keyring: %v", err)
		}
	}()
	purpose, err := kms.NewPurpose("idenqa.evidence.content")
	if err != nil {
		t.Fatalf("new evidence key purpose: %v", err)
	}
	streaming, err := tinkcrypto.NewStreaming(keyring, keyring, purpose)
	if err != nil {
		t.Fatalf("new evidence streaming crypto: %v", err)
	}
	objects, err := objectlocal.Open(objectlocal.Config{
		Directory: t.TempDir(), MaxObjectBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("open evidence object store: %v", err)
	}
	defer func() {
		if err := objects.Close(); err != nil {
			t.Errorf("close evidence object store: %v", err)
		}
	}()
	protector, err := evidence.NewProtector(streaming, objects, evidenceStore, registry, time.Second)
	if err != nil {
		t.Fatalf("new evidence protector: %v", err)
	}
	assertEvidenceUploadAcceptance(
		t, adminPool, store, evidenceStore, generator, protector, streaming, objects, uploadHarness,
		ownerScope, created, registry, requirements[0], now.Add(6*time.Minute),
	)
	evidenceID, _ := generator.NewEvidence()
	protected, err := protector.Protect(ctx, ownerScope, evidence.ProtectionInput{
		ID: evidenceID, SubjectID: storedAuthority.SubjectID(), VerificationID: created.Session.ID(),
		RequirementKey: requirements[0].Key, EvidenceType: evidence.EvidenceSelfieImage,
		Artefact: evidence.ArtefactSelfieImage, AcquisitionMethod: evidence.MethodLiveCamera,
		Region: "tenant.region.ng", RetentionClass: "tenant.retention.identity_v1",
		ContentRevision: 1, MediaType: "image/jpeg", Plaintext: bytes.NewReader([]byte("private selfie")),
		CreatedAt: now.Add(6 * time.Minute),
	})
	if err != nil {
		t.Fatalf("protect evidence: %v", err)
	}
	foundEvidence, err := evidenceStore.Find(ctx, ownerScope, protected.ID())
	if err != nil || foundEvidence.Content().Object().Record() != protected.Content().Object().Record() {
		t.Fatalf("find protected evidence = %+v, error = %v", foundEvidence.Record(), err)
	}
	ciphertext, err := objects.Open(ctx, foundEvidence.Content().Object())
	if err != nil {
		t.Fatalf("open persisted evidence ciphertext: %v", err)
	}
	var opened bytes.Buffer
	authenticatedContext, err := evidence.AuthenticatedContext(foundEvidence.Record())
	if err != nil {
		t.Fatalf("restore persisted evidence context: %v", err)
	}
	if err := streaming.Open(ctx, &opened, ciphertext, foundEvidence.Content().Envelope(), authenticatedContext); err != nil {
		_ = ciphertext.Close()
		t.Fatalf("decrypt persisted evidence: %v", err)
	}
	if err := ciphertext.Close(); err != nil {
		t.Fatalf("close persisted evidence ciphertext: %v", err)
	}
	if opened.String() != "private selfie" {
		t.Fatalf("opened evidence = %q, want original plaintext", opened.String())
	}
	if _, err := evidenceStore.Find(ctx, otherScope, protected.ID()); !errors.Is(err, evidence.ErrNotFound) {
		t.Fatalf("cross-tenant evidence lookup = %v, want ErrNotFound", err)
	}
	beforeRewrap := protected.Record()
	if version, err := keyring.Rotate(ctx); err != nil || version != "v2" {
		t.Fatalf("rotate evidence keyring = %q, %v", version, err)
	}
	rewrapper, err := evidence.NewRewrapper(evidenceStore, evidenceStore, keyring, keyring)
	if err != nil {
		t.Fatalf("new evidence rewrapper: %v", err)
	}
	rewrapAttribution := evidence.CommandAttribution{
		Principal:   evidence.Actor{Type: "idenqa.actor.operator", ID: "rotation-operator"},
		TenantActor: evidence.Actor{Type: "idenqa.actor.tenant_admin", ID: "tenant-admin"},
		Reason:      "scheduled evidence key rotation",
	}
	protected, err = rewrapper.Rewrap(
		ctx, ownerScope, protected.ID(), protected.Version(), rewrapAttribution,
		now.Add(6*time.Minute+30*time.Second),
	)
	if err != nil {
		t.Fatalf("rewrap evidence key: %v", err)
	}
	afterRewrap := protected.Record()
	if afterRewrap.Version != beforeRewrap.Version+1 ||
		afterRewrap.Content.Object != beforeRewrap.Content.Object ||
		afterRewrap.ContentRevision != beforeRewrap.ContentRevision ||
		afterRewrap.State != beforeRewrap.State ||
		afterRewrap.Content.Envelope.WrappedKey.Version != "v2" {
		t.Fatalf("persisted evidence rewrap changed unexpected state: %+v", afterRewrap)
	}
	if _, err := rewrapper.Rewrap(
		ctx, ownerScope, protected.ID(), beforeRewrap.Version, rewrapAttribution,
		now.Add(6*time.Minute+45*time.Second),
	); !errors.Is(err, evidence.ErrVersionConflict) {
		t.Fatalf("stale evidence rewrap = %v, want ErrVersionConflict", err)
	}
	assertEvidenceRewrapAudit(t, adminPool, protected, beforeRewrap, rewrapAttribution)
	assertEvidenceGrantPersistence(
		t, adminPool, evidenceStore, generator, ownerScope, otherScope,
		protected, storedAuthority, storedResponse, now.Add(6*time.Minute),
	)
	policyQuarantine, err := protected.Quarantine(
		protected.Version(), "policy.subject_request", now.Add(7*time.Minute),
	)
	if err != nil {
		t.Fatalf("construct policy quarantine: %v", err)
	}
	if err := evidenceStore.QuarantineIntegrity(
		ctx, ownerScope, policyQuarantine, protected.Version(),
	); !errors.Is(err, evidence.ErrConflict) {
		t.Fatalf("integrity port accepted policy quarantine: %v", err)
	}
	quarantined, err := protected.FailIntegrity(
		protected.Version(), "evidence.integrity.synthetic", now.Add(8*time.Minute),
	)
	if err != nil {
		t.Fatalf("quarantine evidence: %v", err)
	}
	if err := evidenceStore.QuarantineIntegrity(ctx, ownerScope, quarantined, protected.Version()); err != nil {
		t.Fatalf("persist evidence quarantine: %v", err)
	}
	reloadedQuarantine, err := evidenceStore.Find(ctx, ownerScope, protected.ID())
	if err != nil {
		t.Fatalf("reload integrity quarantine: %v", err)
	}
	reloadedRecord := reloadedQuarantine.Record()
	if reloadedRecord.State != evidence.StateQuarantined ||
		reloadedRecord.Integrity != evidence.IntegrityFailed || reloadedQuarantine.CanRead() ||
		reloadedRecord.QuarantineReason != "evidence.integrity.synthetic" {
		t.Fatalf("reloaded integrity quarantine = %+v", reloadedRecord)
	}
	if err := evidenceStore.UpdateLifecycle(ctx, ownerScope, quarantined, protected.Version()); !errors.Is(err, evidence.ErrVersionConflict) {
		t.Fatalf("stale evidence transition = %v, want ErrVersionConflict", err)
	}
	trackingObjects := &integrationTrackingObjects{Store: objects}
	cleanupProtector, err := evidence.NewProtector(streaming, trackingObjects, evidenceStore, registry, time.Second)
	if err != nil {
		t.Fatalf("new cleanup evidence protector: %v", err)
	}
	orphanEvidenceID, _ := generator.NewEvidence()
	orphanSubjectID, _ := generator.NewSubject()
	_, err = cleanupProtector.Protect(ctx, ownerScope, evidence.ProtectionInput{
		ID: orphanEvidenceID, SubjectID: orphanSubjectID, VerificationID: created.Session.ID(),
		RequirementKey: requirements[0].Key, EvidenceType: evidence.EvidenceSelfieImage,
		Artefact: evidence.ArtefactSelfieImage, AcquisitionMethod: evidence.MethodLiveCamera,
		Region: "tenant.region.ng", RetentionClass: "tenant.retention.identity_v1",
		ContentRevision: 1, MediaType: "image/jpeg", Plaintext: bytes.NewReader([]byte("must be cleaned")),
		CreatedAt: now.Add(7 * time.Minute),
	})
	if err == nil {
		t.Fatal("protect evidence with absent subject succeeded")
	}
	if _, err := objects.Open(ctx, trackingObjects.object); !errors.Is(err, objectlocal.ErrUnavailable) {
		t.Fatalf("open compensated evidence object = %v, want ErrUnavailable", err)
	}
	assertEvidenceHistory(t, adminPool, quarantined)

	assertAuthorityHistoryImmutable(t, adminPool, storedNotice, storedResponse)
	withdrawn := storedAuthority
	if err := withdrawn.Withdraw(now.Add(7 * time.Minute)); err != nil {
		t.Fatalf("withdraw authority: %v", err)
	}
	transitionEventID, _ := generator.NewEvent()
	transitionRequest := integrationIdempotencyRequest(
		t, owner.ID(), actor.ID(), "authorities.withdrawn", "authority-withdraw",
		[]byte(`{"state":"withdrawn"}`), now.Add(7*time.Minute),
	)
	withdrawn, err = store.Transition(ctx, ownerScope, authority.TransitionMutation{
		Authority: withdrawn, ExpectedVersion: 1, Actor: actor.ID(), EventID: transitionEventID,
		Action: authority.StateWithdrawn, Idempotency: transitionRequest,
	})
	if err != nil {
		t.Fatalf("withdraw persisted authority: %v", err)
	}
	if err := authority.Evaluate(withdrawn, storedNotice, snapshot.Response, authority.GrantRequest{
		TenantID: owner.ID(), VerificationID: created.Session.ID(), SubjectID: withdrawn.SubjectID(),
		Purpose: string(requirements[0].Purpose), EvidenceType: string(requirements[0].EvidenceType),
		RecipientReference: "tenant.recipient.primary", Region: "tenant.region.ng",
	}, now.Add(8*time.Minute)); !errors.Is(err, authority.ErrProcessingNotPermitted) {
		t.Fatalf("withdrawn authority evaluation = %v, want blocked", err)
	}
}

type integrationUploadHarness struct {
	service        *authority.UploadService
	captureContext verification.CaptureContext
}

func assertEvidenceUploadPersistence(
	t *testing.T,
	adminPool *idenqapostgres.Pool,
	authorityStore *authoritypostgres.Store,
	evidenceStore *evidencepostgres.Store,
	sessionStore *verificationpostgres.SessionStore,
	generator *id.Generator,
	ownerScope tenant.Scope,
	otherScope tenant.Scope,
	creation verification.SessionCreation,
	registry evidence.Registry,
	requirement verification.Requirement,
	base time.Time,
) integrationUploadHarness {
	t.Helper()
	keyring, err := access.NewCaptureTokenKeyring(1, map[access.CaptureTokenKeyVersion][]byte{
		1: bytes.Repeat([]byte{13}, 32),
	})
	if err != nil {
		t.Fatalf("new upload capture keyring: %v", err)
	}
	signer, err := access.NewCaptureTokenSigner(keyring, fixedIntegrationClock{now: base})
	if err != nil {
		t.Fatalf("new upload capture signer: %v", err)
	}
	presented, err := signer.Sign(creation.Credential)
	if err != nil {
		t.Fatalf("sign upload capture credential: %v", err)
	}
	authenticator, err := verification.NewCaptureAuthenticator(
		sessionStore, signer, fixedIntegrationClock{now: base},
	)
	if err != nil {
		t.Fatalf("new upload capture authenticator: %v", err)
	}
	captureContext, err := authenticator.Authenticate(t.Context(), presented.Reveal())
	if err != nil {
		t.Fatalf("authenticate upload capture token: %v", err)
	}
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatalf("new upload registry catalog: %v", err)
	}
	service, err := authority.NewUploadService(
		authorityStore, evidenceStore, generator, fixedIntegrationClock{now: base},
		catalog, evidence.DefaultUploadPolicy(), 24*time.Hour,
	)
	if err != nil {
		t.Fatalf("new upload service: %v", err)
	}
	plaintext := []byte("private selfie")
	request := authority.UploadRequest{
		RequirementKey: requirement.Key, Artefact: requirement.Artefacts[0],
		AcquisitionMethod: requirement.Acquisition.Methods[0], ExpectedBytes: int64(len(plaintext)),
		ExpectedDigest: string(platformcrypto.Sum(plaintext)), MediaType: evidence.MediaTypeJPEG,
		Region: "tenant.region.ng",
	}
	created, err := service.Issue(t.Context(), captureContext, "evidence-upload-create", request)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	replayed, err := service.Issue(t.Context(), captureContext, "evidence-upload-create", request)
	if err != nil || replayed.ID() != created.ID() {
		t.Fatalf("Issue(replay) = %s, %v", replayed.ID(), err)
	}
	conflict := request
	conflict.MediaType = evidence.MediaTypePNG
	if _, err := service.Issue(
		t.Context(), captureContext, "evidence-upload-create", conflict,
	); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("Issue(conflict) error = %v, want ErrConflict", err)
	}
	if _, err := evidenceStore.FindUpload(t.Context(), otherScope, created.ID()); !errors.Is(err, evidence.ErrUploadNotFound) {
		t.Fatalf("FindUpload(cross tenant) error = %v, want ErrUploadNotFound", err)
	}
	preflight, err := evidence.NewUploadPreflight(
		evidenceStore, fixedIntegrationClock{now: base.Add(time.Minute)},
	)
	if err != nil {
		t.Fatalf("NewUploadPreflight() error = %v", err)
	}
	metadata := evidence.UploadMetadata{
		ExpectedVersion: created.Version(), ContentLength: request.ExpectedBytes,
		MediaType: request.MediaType, Digest: platformcrypto.Sum(plaintext),
	}

	wrongPrincipal, err := generator.NewCaptureToken()
	if err != nil {
		t.Fatalf("new wrong capture principal: %v", err)
	}
	if _, err := preflight.Begin(
		t.Context(), evidence.UploadPrincipal{
			Scope: ownerScope, CaptureTokenID: wrongPrincipal,
			VerificationID: creation.Session.ID(),
		}, created.ID(), metadata,
	); !errors.Is(err, evidence.ErrUploadNotFound) {
		t.Fatalf("Begin(wrong principal) error = %v, want ErrUploadNotFound", err)
	}

	type claimResult struct {
		upload evidence.Upload
		err    error
	}
	start := make(chan struct{})
	results := make(chan claimResult, 2)
	for range 2 {
		go func() {
			<-start
			claimed, claimErr := preflight.Begin(
				t.Context(), evidence.UploadPrincipal{
					Scope: ownerScope, CaptureTokenID: creation.Credential.ID(),
					VerificationID: creation.Session.ID(),
				}, created.ID(), metadata,
			)
			results <- claimResult{upload: claimed, err: claimErr}
		}()
	}
	close(start)
	var claimed evidence.Upload
	var successes, conflicts int
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			successes++
			claimed = result.upload
		case errors.Is(result.err, evidence.ErrUploadVersionConflict),
			errors.Is(result.err, evidence.ErrUploadConflict):
			conflicts++
		default:
			t.Fatalf("concurrent Begin() error = %v", result.err)
		}
	}
	if successes != 1 || conflicts != 1 || claimed.Attempt() != 1 || claimed.Version() != 2 {
		t.Fatalf("concurrent claims successes=%d conflicts=%d attempt=%d version=%d",
			successes, conflicts, claimed.Attempt(), claimed.Version())
	}
	retryable, err := evidenceStore.FailUploadAttempt(
		t.Context(), ownerScope, creation.Credential.ID(), claimed.ID(),
		claimed.Version(), claimed.Attempt(), base.Add(2*time.Minute),
	)
	if err != nil {
		t.Fatalf("FailUploadAttempt() error = %v", err)
	}
	if retryable.State() != evidence.UploadStateIssued || retryable.Version() != 3 || retryable.Attempt() != 1 {
		t.Fatalf("retryable upload state=%s version=%d attempt=%d",
			retryable.State(), retryable.Version(), retryable.Attempt())
	}
	if _, err := evidenceStore.FailUploadAttempt(
		t.Context(), ownerScope, creation.Credential.ID(), claimed.ID(),
		claimed.Version(), claimed.Attempt(), base.Add(2*time.Minute),
	); !errors.Is(err, evidence.ErrUploadVersionConflict) {
		t.Fatalf("FailUploadAttempt(stale) error = %v, want ErrUploadVersionConflict", err)
	}
	second, err := evidenceStore.ClaimUploadAttempt(
		t.Context(), ownerScope, creation.Credential.ID(), retryable.ID(),
		retryable.Version(), base.Add(3*time.Minute),
	)
	if err != nil {
		t.Fatalf("ClaimUploadAttempt(retry) error = %v", err)
	}
	expired, err := evidenceStore.FailUploadAttempt(
		t.Context(), ownerScope, creation.Credential.ID(), second.ID(),
		second.Version(), second.Attempt(), second.Record().ExpiresAt,
	)
	if err != nil || expired.State() != evidence.UploadStateExpired {
		t.Fatalf("FailUploadAttempt(expire) state=%s error=%v", expired.State(), err)
	}
	restored, err := evidenceStore.FindUpload(t.Context(), ownerScope, expired.ID())
	if err != nil || restored.ID() != expired.ID() || restored.State() != expired.State() ||
		restored.Version() != expired.Version() || restored.Attempt() != expired.Attempt() {
		t.Fatalf("FindUpload() = %+v, error = %v", restored.Record(), err)
	}
	if _, err := evidenceStore.ClaimUploadAttempt(
		t.Context(), ownerScope, creation.Credential.ID(), restored.ID(),
		restored.Version(), restored.Record().ExpiresAt,
	); !errors.Is(err, evidence.ErrUploadExpired) {
		t.Fatalf("ClaimUploadAttempt(expired) error = %v, want ErrUploadExpired", err)
	}

	var auditCount int
	var principalID, auditAction string
	var auditReason *string
	err = adminPool.WithinTransaction(
		t.Context(), idenqapostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			if err := tx.QueryRow(
				ctx,
				"SELECT count(*) FROM idenqa.evidence_upload_intent_audit WHERE upload_id = $1",
				restored.ID().String(),
			).Scan(&auditCount); err != nil {
				return err
			}
			return tx.QueryRow(
				ctx,
				"SELECT principal_id, action, reason FROM idenqa.evidence_upload_intent_audit WHERE upload_id = $1 ORDER BY aggregate_version DESC LIMIT 1",
				restored.ID().String(),
			).Scan(&principalID, &auditAction, &auditReason)
		},
	)
	if err != nil || auditCount != 5 || principalID != creation.Credential.ID().String() ||
		auditAction != "expire" || auditReason == nil || *auditReason != "evidence.upload.expired" {
		t.Fatalf("upload audit count=%d principal=%q action=%q reason=%v error=%v",
			auditCount, principalID, auditAction, auditReason, err)
	}
	err = adminPool.WithinTransaction(
		t.Context(), idenqapostgres.TransactionOptions{},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			_, err := tx.Exec(
				ctx,
				"UPDATE idenqa.evidence_upload_intents SET expected_bytes = expected_bytes + 1 WHERE id = $1",
				restored.ID().String(),
			)
			return err
		},
	)
	if err == nil {
		t.Fatal("database allowed immutable upload binding update")
	}
	err = adminPool.WithinTransaction(
		t.Context(), idenqapostgres.TransactionOptions{},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			_, err := tx.Exec(
				ctx,
				"DELETE FROM idenqa.evidence_upload_intent_audit WHERE upload_id = $1",
				restored.ID().String(),
			)
			return err
		},
	)
	if err == nil {
		t.Fatal("database allowed upload audit deletion")
	}

	return integrationUploadHarness{service: service, captureContext: captureContext}
}

func assertEvidenceUploadAcceptance(
	t *testing.T,
	adminPool *idenqapostgres.Pool,
	authorityStore *authoritypostgres.Store,
	evidenceStore *evidencepostgres.Store,
	generator *id.Generator,
	protector *evidence.Protector,
	streaming *tinkcrypto.Streaming,
	objects *objectlocal.Store,
	harness integrationUploadHarness,
	ownerScope tenant.Scope,
	creation verification.SessionCreation,
	registry evidence.Registry,
	requirement verification.Requirement,
	base time.Time,
) {
	t.Helper()
	body := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte("private selfie"), 8)...)
	prepare := func(key string) (evidence.Upload, evidence.PreparedEvidence, evidence.ValidatedBody) {
		request := authority.UploadRequest{
			RequirementKey: requirement.Key, Artefact: requirement.Artefacts[0],
			AcquisitionMethod: requirement.Acquisition.Methods[0], ExpectedBytes: int64(len(body)),
			ExpectedDigest: string(platformcrypto.Sum(body)), MediaType: evidence.MediaTypeJPEG,
			Region: "tenant.region.ng",
		}
		issued, err := harness.service.Issue(t.Context(), harness.captureContext, key, request)
		if err != nil {
			t.Fatalf("Issue(%s) error = %v", key, err)
		}
		preflight, err := evidence.NewUploadPreflight(
			evidenceStore, fixedIntegrationClock{now: base.Add(time.Minute)},
		)
		if err != nil {
			t.Fatalf("NewUploadPreflight(%s) error = %v", key, err)
		}
		claimed, err := preflight.Begin(t.Context(), evidence.UploadPrincipal{
			Scope: ownerScope, CaptureTokenID: creation.Credential.ID(),
			VerificationID: creation.Session.ID(),
		}, issued.ID(), evidence.UploadMetadata{
			ExpectedVersion: issued.Version(), ContentLength: int64(len(body)),
			MediaType: evidence.MediaTypeJPEG, Digest: platformcrypto.Sum(body),
		})
		if err != nil {
			t.Fatalf("Begin(%s) error = %v", key, err)
		}
		reader, err := evidence.NewUploadReader(claimed, bytes.NewReader(body))
		if err != nil {
			t.Fatalf("NewUploadReader(%s) error = %v", key, err)
		}
		record := claimed.Record()
		prepared, err := protector.Prepare(t.Context(), ownerScope, evidence.ProtectionInput{
			ID: record.EvidenceID, SubjectID: record.SubjectID, VerificationID: record.VerificationID,
			RequirementKey: record.RequirementKey, EvidenceType: record.EvidenceType,
			Artefact: record.Artefact, AcquisitionMethod: record.AcquisitionMethod,
			Assurances: record.Assurances, Region: record.Region, RetentionClass: record.RetentionClass,
			ContentRevision: 1, MediaType: record.MediaType, Plaintext: reader,
			CreatedAt: base.Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("Prepare(%s) error = %v", key, err)
		}
		validated, err := reader.Result()
		if err != nil {
			t.Fatalf("Result(%s) error = %v", key, err)
		}
		if err := evidenceStore.CreateObjectReconciliation(
			t.Context(), ownerScope, claimed, prepared, base.Add(time.Minute),
		); err != nil {
			t.Fatalf("CreateObjectReconciliation(%s) error = %v", key, err)
		}

		return claimed, prepared, validated
	}

	claimed, prepared, validated := prepare("evidence-upload-accept")
	eventID, err := generator.NewEvent()
	if err != nil {
		t.Fatalf("new evidence-ready event id: %v", err)
	}
	accepted, err := evidenceStore.AcceptUpload(t.Context(), ownerScope, evidence.UploadAcceptance{
		UploadID: claimed.ID(), CaptureTokenID: creation.Credential.ID(),
		ExpectedVersion: claimed.Version(), Attempt: claimed.Attempt(), Asset: prepared.Asset(),
		PlaintextBytes: validated.Bytes, EventID: eventID, OccurredAt: base.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("AcceptUpload() error = %v", err)
	}
	if accepted.State() != evidence.UploadStateAccepted || accepted.Version() != claimed.Version()+1 {
		t.Fatalf("accepted upload state=%s version=%d", accepted.State(), accepted.Version())
	}
	if _, err := evidenceStore.Find(t.Context(), ownerScope, prepared.Asset().ID()); err != nil {
		t.Fatalf("Find(accepted evidence) error = %v", err)
	}

	var assetAudits, uploadAudits, eventCount int
	var payload []byte
	err = adminPool.WithinTransaction(
		t.Context(), idenqapostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			if err := tx.QueryRow(ctx,
				"SELECT count(*) FROM idenqa.evidence_asset_audit WHERE evidence_id = $1",
				prepared.Asset().ID().String(),
			).Scan(&assetAudits); err != nil {
				return err
			}
			if err := tx.QueryRow(ctx,
				"SELECT count(*) FROM idenqa.evidence_upload_intent_audit WHERE upload_id = $1 AND action = 'accept'",
				claimed.ID().String(),
			).Scan(&uploadAudits); err != nil {
				return err
			}
			return tx.QueryRow(ctx,
				"SELECT count(*), min(payload::text)::bytea FROM idenqa.outbox_events WHERE id = $1 AND event_type = $2",
				eventID.String(), evidence.EventEvidenceReady,
			).Scan(&eventCount, &payload)
		},
	)
	if err != nil || assetAudits != 1 || uploadAudits != 1 || eventCount != 1 ||
		bytes.Contains(payload, []byte("digest")) || bytes.Contains(payload, body) {
		t.Fatalf("acceptance evidence asset_audits=%d upload_audits=%d events=%d payload=%q error=%v",
			assetAudits, uploadAudits, eventCount, payload, err)
	}

	rollbackClaimed, rollbackPrepared, rollbackValidated := prepare("evidence-upload-rollback")
	rollbackObject := rollbackPrepared.Asset().Content().Object()
	_, err = evidenceStore.AcceptUpload(t.Context(), ownerScope, evidence.UploadAcceptance{
		UploadID: rollbackClaimed.ID(), CaptureTokenID: creation.Credential.ID(),
		ExpectedVersion: rollbackClaimed.Version(), Attempt: rollbackClaimed.Attempt(),
		Asset: rollbackPrepared.Asset(), PlaintextBytes: rollbackValidated.Bytes,
		EventID: eventID, OccurredAt: base.Add(2 * time.Minute),
	})
	if err == nil {
		t.Fatal("AcceptUpload(duplicate event) succeeded")
	}
	if _, err := evidenceStore.Find(
		t.Context(), ownerScope, rollbackPrepared.Asset().ID(),
	); !errors.Is(err, evidence.ErrNotFound) {
		t.Fatalf("Find(rolled back evidence) error = %v, want ErrNotFound", err)
	}
	restored, err := evidenceStore.FindUpload(t.Context(), ownerScope, rollbackClaimed.ID())
	if err != nil || restored.State() != evidence.UploadStateUploading ||
		restored.Version() != rollbackClaimed.Version() {
		t.Fatalf("rolled back upload state=%s version=%d error=%v", restored.State(), restored.Version(), err)
	}
	recoveryAt := rollbackClaimed.Record().LeaseExpiresAt.Add(time.Second)
	reconciler, err := evidence.NewReconciliationService(
		evidenceStore, evidenceStore, evidenceStore, objects,
		fixedIntegrationClock{now: recoveryAt}, time.Minute, time.Minute, time.Second,
	)
	if err != nil {
		t.Fatalf("NewReconciliationService() error = %v", err)
	}
	disposition, err := reconciler.ReconcileNext(t.Context(), ownerScope)
	if err != nil || disposition != evidence.ReconciliationDeleted {
		t.Fatalf("ReconcileNext() disposition=%q error=%v", disposition, err)
	}
	if _, err := objects.Open(t.Context(), rollbackObject); !errors.Is(err, objectlocal.ErrUnavailable) {
		t.Fatalf("Open(discarded rollback object) error = %v, want ErrUnavailable", err)
	}

	orchestratedBody := append([]byte{0xff, 0xd8, 0xff, 0xe0}, bytes.Repeat([]byte("orchestrated"), 8)...)
	issueOrchestrated := func(key string) evidence.Upload {
		issued, err := harness.service.Issue(t.Context(), harness.captureContext, key, authority.UploadRequest{
			RequirementKey: requirement.Key, Artefact: requirement.Artefacts[0],
			AcquisitionMethod: requirement.Acquisition.Methods[0],
			ExpectedBytes:     int64(len(orchestratedBody)),
			ExpectedDigest:    string(platformcrypto.Sum(orchestratedBody)),
			MediaType:         evidence.MediaTypeJPEG,
			Region:            "tenant.region.ng",
		})
		if err != nil {
			t.Fatalf("Issue(%s) error = %v", key, err)
		}

		return issued
	}
	preflight, err := evidence.NewUploadPreflight(
		evidenceStore, fixedIntegrationClock{now: base.Add(time.Minute)},
	)
	if err != nil {
		t.Fatalf("NewUploadPreflight(orchestrated) error = %v", err)
	}
	trackingObjects := &integrationTrackingObjects{Store: objects}
	trackingProtector, err := evidence.NewProtector(
		streaming, trackingObjects, evidenceStore, registry, time.Second,
	)
	if err != nil {
		t.Fatalf("NewProtector(orchestrated) error = %v", err)
	}
	orchestratedEventID, err := generator.NewEvent()
	if err != nil {
		t.Fatalf("NewEvent(orchestrated) error = %v", err)
	}
	acceptanceService, err := authority.NewUploadAcceptanceService(
		authorityStore, preflight, trackingProtector, evidenceStore, evidenceStore, evidenceStore,
		integrationEventGenerator{event: orchestratedEventID},
		fixedIntegrationClock{now: base.Add(2 * time.Minute)},
	)
	if err != nil {
		t.Fatalf("NewUploadAcceptanceService() error = %v", err)
	}
	issued := issueOrchestrated("evidence-upload-orchestrated")
	acceptedUpload, err := acceptanceService.Accept(
		t.Context(), harness.captureContext, issued.ID(), evidence.UploadMetadata{
			ExpectedVersion: issued.Version(), ContentLength: int64(len(orchestratedBody)),
			MediaType: evidence.MediaTypeJPEG, Digest: platformcrypto.Sum(orchestratedBody),
		}, bytes.NewReader(orchestratedBody),
	)
	if err != nil || acceptedUpload.State() != evidence.UploadStateAccepted {
		t.Fatalf("Accept(orchestrated) state=%s error=%v", acceptedUpload.State(), err)
	}
	if _, err := evidenceStore.Find(t.Context(), ownerScope, issued.Record().EvidenceID); err != nil {
		t.Fatalf("Find(orchestrated evidence) error = %v", err)
	}
	assertEvidenceReconciliationState(
		t, adminPool, ownerScope.ID(), issued.ID(), acceptedUpload.Attempt(), "retained",
	)
	acceptedAsset, err := evidenceStore.Find(t.Context(), ownerScope, issued.Record().EvidenceID)
	if err != nil {
		t.Fatalf("Find(accepted object reference) error = %v", err)
	}
	acceptedObject := acceptedAsset.Content().Object()
	referenced, err := evidenceStore.IsObjectReferenced(
		t.Context(), ownerScope, acceptedObject.Key(), acceptedObject.Record().Version,
	)
	if err != nil || !referenced {
		t.Fatalf("IsObjectReferenced(accepted) referenced=%t error=%v", referenced, err)
	}

	rolledBack := issueOrchestrated("evidence-upload-orchestrated-rollback")
	_, err = acceptanceService.Accept(
		t.Context(), harness.captureContext, rolledBack.ID(), evidence.UploadMetadata{
			ExpectedVersion: rolledBack.Version(), ContentLength: int64(len(orchestratedBody)),
			MediaType: evidence.MediaTypeJPEG, Digest: platformcrypto.Sum(orchestratedBody),
		}, bytes.NewReader(orchestratedBody),
	)
	if err == nil {
		t.Fatal("Accept(orchestrated duplicate event) succeeded")
	}
	if _, err := objects.Open(t.Context(), trackingObjects.object); !errors.Is(err, objectlocal.ErrUnavailable) {
		t.Fatalf("Open(orchestrated compensated object) error = %v, want ErrUnavailable", err)
	}
	if _, err := evidenceStore.Find(
		t.Context(), ownerScope, rolledBack.Record().EvidenceID,
	); !errors.Is(err, evidence.ErrNotFound) {
		t.Fatalf("Find(orchestrated rolled back evidence) error = %v, want ErrNotFound", err)
	}
	assertEvidenceReconciliationState(
		t, adminPool, ownerScope.ID(), rolledBack.ID(), 1, "deleted",
	)
	referenced, err = evidenceStore.IsObjectReferenced(
		t.Context(), ownerScope, trackingObjects.object.Key(), trackingObjects.object.Record().Version,
	)
	if err != nil || referenced {
		t.Fatalf("IsObjectReferenced(deleted reconciliation) referenced=%t error=%v", referenced, err)
	}
	retryableUpload, err := evidenceStore.FindUpload(t.Context(), ownerScope, rolledBack.ID())
	if err != nil || retryableUpload.State() != evidence.UploadStateIssued ||
		retryableUpload.Attempt() != 1 || retryableUpload.Version() != rolledBack.Version()+2 {
		t.Fatalf("rolled-back upload state=%q attempt=%d version=%d error=%v",
			retryableUpload.State(), retryableUpload.Attempt(), retryableUpload.Version(), err)
	}

	rejected := issueOrchestrated("evidence-upload-orchestrated-signature")
	_, err = acceptanceService.Accept(
		t.Context(), harness.captureContext, rejected.ID(), evidence.UploadMetadata{
			ExpectedVersion: rejected.Version(), ContentLength: int64(len(orchestratedBody)),
			MediaType: evidence.MediaTypeJPEG, Digest: platformcrypto.Sum(orchestratedBody),
		}, bytes.NewReader([]byte("not-a-jpeg")),
	)
	if !errors.Is(err, evidence.ErrUploadSignature) {
		t.Fatalf("Accept(orchestrated invalid signature) error=%v", err)
	}
	rejectedUpload, err := evidenceStore.FindUpload(t.Context(), ownerScope, rejected.ID())
	if err != nil || rejectedUpload.State() != evidence.UploadStateRejected ||
		rejectedUpload.Record().RejectionReason != "evidence.upload.signature_mismatch" ||
		rejectedUpload.Attempt() != 1 || rejectedUpload.Version() != rejected.Version()+2 {
		t.Fatalf("rejected upload state=%q attempt=%d version=%d reason=%q error=%v",
			rejectedUpload.State(), rejectedUpload.Attempt(), rejectedUpload.Version(),
			rejectedUpload.Record().RejectionReason, err)
	}

	orphanEvidenceID, err := generator.NewEvidence()
	if err != nil {
		t.Fatalf("NewEvidence(orphan inventory) error = %v", err)
	}
	orphanKey, err := objectstore.NewKey(
		"tenants/" + ownerScope.ID().String() + "/evidence/" + orphanEvidenceID.String() + "/content/1",
	)
	if err != nil {
		t.Fatalf("NewKey(orphan inventory) error = %v", err)
	}
	orphanObject, err := objects.Put(t.Context(), orphanKey, func(writer io.Writer) error {
		_, err := writer.Write([]byte("unreferenced ciphertext"))
		return err
	})
	if err != nil {
		t.Fatalf("Put(orphan inventory) error = %v", err)
	}
	discovery, err := evidence.NewOrphanDiscoveryService(
		objects, evidenceStore,
		fixedIntegrationClock{now: time.Now().UTC().Add(evidence.MinimumOrphanAge)},
		evidence.MinimumOrphanAge, time.Second,
	)
	if err != nil {
		t.Fatalf("NewOrphanDiscoveryService() error = %v", err)
	}
	discoveryResult, err := discovery.DiscoverPage(t.Context(), ownerScope, "", 1000)
	if err != nil || discoveryResult.Deleted != 1 || discoveryResult.Referenced < 1 {
		t.Fatalf("DiscoverPage() result=%+v error=%v", discoveryResult, err)
	}
	if _, err := objects.Open(t.Context(), orphanObject); !errors.Is(err, objectlocal.ErrUnavailable) {
		t.Fatalf("Open(discovered orphan) error = %v, want ErrUnavailable", err)
	}
	if _, err := objects.Open(t.Context(), acceptedObject); err != nil {
		t.Fatalf("Open(referenced object after discovery) error = %v", err)
	}
}

type integrationEventGenerator struct{ event id.Event }

func (generator integrationEventGenerator) NewEvent() (id.Event, error) { return generator.event, nil }

type integrationTrackingObjects struct {
	*objectlocal.Store
	object objectstore.Object
}

func (objects *integrationTrackingObjects) Put(
	ctx context.Context,
	key objectstore.Key,
	write func(io.Writer) error,
) (objectstore.Object, error) {
	object, err := objects.Store.Put(ctx, key, write)
	objects.object = object

	return object, err
}

func assertEvidenceGrantPersistence(
	t *testing.T,
	adminPool *idenqapostgres.Pool,
	store *evidencepostgres.Store,
	generator *id.Generator,
	ownerScope tenant.Scope,
	otherScope tenant.Scope,
	asset evidence.Asset,
	processingAuthority authority.Authority,
	response authority.Response,
	base time.Time,
) {
	t.Helper()
	ctx := t.Context()
	runner := evidence.Runner{Identity: "runner.integration", WorkloadVersion: "workload.v1"}
	attribution := evidence.CommandAttribution{
		Principal:   evidence.Actor{Type: "internal.service", ID: "workflow.integration"},
		TenantActor: evidence.Actor{Type: "tenant.service", ID: "tenant.integration"},
		Reason:      "integration evidence grant lifecycle",
	}
	newGrant := func(maximumUses uint32) evidence.Grant {
		grantID, err := generator.NewGrant()
		if err != nil {
			t.Fatalf("new grant id: %v", err)
		}
		grant, err := evidence.NewGrant(evidence.GrantRecord{
			ID: grantID, TenantID: ownerScope.ID(), SubjectID: asset.Record().SubjectID,
			VerificationID: asset.Record().VerificationID, EvidenceID: asset.ID(),
			RequirementKey: asset.Record().RequirementKey,
			AuthorityID:    processingAuthority.ID(), ResponseID: response.Record().ID,
			CheckReference: "check.selfie.match", Runner: runner,
			Purpose: evidence.PurposeIdentityVerification, Operation: evidence.OperationPlaintextRead,
			PermittedVariants: []string{evidence.VariantOriginal}, Region: asset.Record().Region,
			RecipientReference: "tenant.recipient.primary",
			OutputDestination:  "workflow.result.normalized",
			PolicyReference:    processingAuthority.Record().PolicyPack,
			MaximumUses:        maximumUses, CreatedAt: base, ExpiresAt: base.Add(30 * time.Minute),
		})
		if err != nil {
			t.Fatalf("new processing grant: %v", err)
		}
		if err := store.CreateGrant(ctx, ownerScope, grant, attribution); err != nil {
			t.Fatalf("create processing grant: %v", err)
		}
		return grant
	}
	newRedemptionID := func() id.Redemption {
		identifier, err := generator.NewRedemption()
		if err != nil {
			t.Fatalf("new redemption id: %v", err)
		}
		return identifier
	}

	grant := newGrant(2)
	firstID := newRedemptionID()
	first, err := store.ClaimGrant(ctx, ownerScope, grant.ID(), firstID, runner, base.Add(time.Minute))
	if err != nil || first.Grant().Uses() != 1 {
		t.Fatalf("first grant claim uses = %d, error = %v", first.Grant().Uses(), err)
	}
	replayed, err := store.ClaimGrant(ctx, ownerScope, grant.ID(), firstID, runner, base.Add(2*time.Minute))
	if err != nil || replayed.Grant().Uses() != 1 {
		t.Fatalf("pending claim replay uses = %d, error = %v", replayed.Grant().Uses(), err)
	}
	forgedRecord := first.Grant().Record()
	forgedRecord.Uses = 2
	forgedGrant, err := evidence.RestoreGrant(forgedRecord)
	if err != nil {
		t.Fatalf("restore forged claim fixture: %v", err)
	}
	forgedRedemption, err := evidence.NewRedemption(firstID, forgedGrant)
	if err != nil {
		t.Fatalf("new forged redemption fixture: %v", err)
	}
	if err := store.RecordGrantOutcome(
		ctx, ownerScope, forgedRedemption, evidence.GrantOutcomeSucceeded,
		base.Add(2*time.Minute),
	); !errors.Is(err, evidence.ErrGrantDenied) {
		t.Fatalf("forged claimed-use outcome = %v, want ErrGrantDenied", err)
	}
	if err := store.RecordGrantOutcome(
		ctx, ownerScope, first, evidence.GrantOutcomeSucceeded, base.Add(2*time.Minute),
	); err != nil {
		t.Fatalf("record successful redemption outcome: %v", err)
	}
	if err := store.RecordGrantOutcome(
		ctx, ownerScope, first, evidence.GrantOutcomeSucceeded, base.Add(3*time.Minute),
	); err != nil {
		t.Fatalf("replay successful redemption outcome: %v", err)
	}
	if err := store.RecordGrantOutcome(
		ctx, ownerScope, first, evidence.GrantOutcomeFailed, base.Add(3*time.Minute),
	); !errors.Is(err, evidence.ErrGrantConflict) {
		t.Fatalf("conflicting redemption outcome = %v, want ErrGrantConflict", err)
	}

	secondID := newRedemptionID()
	second, err := store.ClaimGrant(ctx, ownerScope, grant.ID(), secondID, runner, base.Add(3*time.Minute))
	if err != nil || second.Grant().Uses() != 2 {
		t.Fatalf("second grant claim uses = %d, error = %v", second.Grant().Uses(), err)
	}
	revoked, err := store.RevokeGrant(
		ctx, ownerScope, grant.ID(), attribution, base.Add(4*time.Minute),
	)
	if err != nil || revoked.Record().RevokedAt == nil {
		t.Fatalf("revoke processing grant = %+v, error = %v", revoked.Record(), err)
	}
	terminalReplay, err := store.ClaimGrant(
		ctx, ownerScope, grant.ID(), firstID, runner, base.Add(31*time.Minute),
	)
	if err != nil {
		t.Fatalf("terminal replay after revocation and expiry: %v", err)
	}
	if outcome, terminal := terminalReplay.TerminalOutcome(); !terminal || outcome != evidence.GrantOutcomeSucceeded {
		t.Fatalf("terminal replay outcome = %q, terminal = %t", outcome, terminal)
	}
	pendingReplay, err := store.ClaimGrant(
		ctx, ownerScope, grant.ID(), secondID, runner, base.Add(5*time.Minute),
	)
	if err != nil || pendingReplay.Grant().Uses() != 2 {
		t.Fatalf("pending replay after revocation uses = %d, error = %v", pendingReplay.Grant().Uses(), err)
	}
	deniedID := newRedemptionID()
	if _, err := store.ClaimGrant(
		ctx, ownerScope, grant.ID(), deniedID, runner, base.Add(5*time.Minute),
	); !errors.Is(err, evidence.ErrGrantDenied) {
		t.Fatalf("claim revoked grant = %v, want ErrGrantDenied", err)
	}
	if _, err := store.ClaimGrant(
		ctx, ownerScope, grant.ID(), deniedID, runner, base.Add(6*time.Minute),
	); !errors.Is(err, evidence.ErrGrantDenied) {
		t.Fatalf("replay denied claim = %v, want ErrGrantDenied", err)
	}
	if _, err := store.ClaimGrant(
		ctx, otherScope, grant.ID(), newRedemptionID(), runner, base.Add(time.Minute),
	); !errors.Is(err, evidence.ErrGrantDenied) {
		t.Fatalf("cross-tenant grant claim = %v, want ErrGrantDenied", err)
	}

	otherGrant := newGrant(1)
	if _, err := store.ClaimGrant(
		ctx, ownerScope, otherGrant.ID(), firstID, runner, base.Add(time.Minute),
	); !errors.Is(err, evidence.ErrGrantDenied) {
		t.Fatalf("reuse redemption against another grant = %v, want ErrGrantDenied", err)
	}
	otherClaim, err := store.ClaimGrant(
		ctx, ownerScope, otherGrant.ID(), newRedemptionID(), runner, base.Add(time.Minute),
	)
	if err != nil || otherClaim.Grant().Uses() != 1 {
		t.Fatalf("other grant claim after mismatched replay uses = %d, error = %v", otherClaim.Grant().Uses(), err)
	}

	expiringGrant := newGrant(1)
	if _, err := store.ClaimGrant(
		ctx, ownerScope, expiringGrant.ID(), newRedemptionID(), runner,
		base.Add(30*time.Minute),
	); !errors.Is(err, evidence.ErrGrantDenied) {
		t.Fatalf("claim at exact grant expiry = %v, want ErrGrantDenied", err)
	}
	wrongRunnerGrant := newGrant(1)
	wrongRunner := evidence.Runner{Identity: "runner.other", WorkloadVersion: runner.WorkloadVersion}
	if _, err := store.ClaimGrant(
		ctx, ownerScope, wrongRunnerGrant.ID(), newRedemptionID(), wrongRunner,
		base.Add(time.Minute),
	); !errors.Is(err, evidence.ErrGrantDenied) {
		t.Fatalf("wrong-runner grant claim = %v, want ErrGrantDenied", err)
	}
	correctRunnerClaim, err := store.ClaimGrant(
		ctx, ownerScope, wrongRunnerGrant.ID(), newRedemptionID(), runner,
		base.Add(time.Minute),
	)
	if err != nil || correctRunnerClaim.Grant().Uses() != 1 {
		t.Fatalf("correct claim after wrong runner uses = %d, error = %v", correctRunnerClaim.Grant().Uses(), err)
	}

	assertConcurrentRedemptionClaims(t, store, generator, ownerScope, newGrant, runner, base)
	assertRevokeClaimSerialization(
		t, store, generator, ownerScope, newGrant, runner, attribution, base,
	)
	assertEvidenceGrantRowsImmutable(t, adminPool, grant, firstID, secondID, deniedID)
}

func assertConcurrentRedemptionClaims(
	t *testing.T,
	store *evidencepostgres.Store,
	generator *id.Generator,
	scope tenant.Scope,
	newGrant func(uint32) evidence.Grant,
	runner evidence.Runner,
	base time.Time,
) {
	t.Helper()
	grant := newGrant(1)
	redemptionID := mustNewRedemption(t, generator)
	results := make(chan error, 2)
	for range 2 {
		go func() {
			claimed, err := store.ClaimGrant(
				t.Context(), scope, grant.ID(), redemptionID, runner, base.Add(time.Minute),
			)
			if err == nil && claimed.Grant().Uses() != 1 {
				err = errors.New("concurrent redemption replay consumed multiple uses")
			}
			results <- err
		}()
	}
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("same-redemption concurrent claim: %v", err)
		}
	}
	if _, err := store.ClaimGrant(
		t.Context(), scope, grant.ID(), mustNewRedemption(t, generator), runner,
		base.Add(2*time.Minute),
	); !errors.Is(err, evidence.ErrGrantDenied) {
		t.Fatalf("claim after concurrent replay = %v, want exhausted denial", err)
	}

	tracingGrant := newGrant(1)
	distinctResults := make(chan error, 2)
	for range 2 {
		identifier := mustNewRedemption(t, generator)
		go func() {
			_, err := store.ClaimGrant(
				t.Context(), scope, tracingGrant.ID(), identifier, runner,
				base.Add(time.Minute),
			)
			distinctResults <- err
		}()
	}
	succeeded := 0
	denied := 0
	for range 2 {
		err := <-distinctResults
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, evidence.ErrGrantDenied):
			denied++
		default:
			t.Fatalf("distinct concurrent claim: %v", err)
		}
	}
	if succeeded != 1 || denied != 1 {
		t.Fatalf("distinct concurrent claims succeeded = %d, denied = %d", succeeded, denied)
	}

	firstGrant := newGrant(1)
	secondGrant := newGrant(1)
	sharedRedemption := mustNewRedemption(t, generator)
	type crossGrantResult struct {
		grant evidence.Grant
		err   error
	}
	crossGrantResults := make(chan crossGrantResult, 2)
	for _, candidate := range []evidence.Grant{firstGrant, secondGrant} {
		go func() {
			_, err := store.ClaimGrant(
				t.Context(), scope, candidate.ID(), sharedRedemption, runner,
				base.Add(time.Minute),
			)
			crossGrantResults <- crossGrantResult{grant: candidate, err: err}
		}()
	}
	var winner, loser evidence.Grant
	for range 2 {
		result := <-crossGrantResults
		switch {
		case result.err == nil:
			winner = result.grant
		case errors.Is(result.err, evidence.ErrGrantDenied):
			loser = result.grant
		default:
			t.Fatalf("same-redemption cross-grant claim: %v", result.err)
		}
	}
	if winner.ID().IsZero() || loser.ID().IsZero() {
		t.Fatalf("same-redemption cross-grant winner=%s loser=%s", winner.ID(), loser.ID())
	}
	if _, err := store.ClaimGrant(
		t.Context(), scope, loser.ID(), mustNewRedemption(t, generator), runner,
		base.Add(2*time.Minute),
	); err != nil {
		t.Fatalf("cross-grant losing grant consumed a use: %v", err)
	}
	if _, err := store.ClaimGrant(
		t.Context(), scope, winner.ID(), mustNewRedemption(t, generator), runner,
		base.Add(2*time.Minute),
	); !errors.Is(err, evidence.ErrGrantDenied) {
		t.Fatalf("cross-grant winner additional claim = %v, want exhausted denial", err)
	}
}

func mustNewRedemption(t *testing.T, generator *id.Generator) id.Redemption {
	t.Helper()
	identifier, err := generator.NewRedemption()
	if err != nil {
		t.Fatalf("new redemption id: %v", err)
	}
	return identifier
}

func assertRevokeClaimSerialization(
	t *testing.T,
	store *evidencepostgres.Store,
	generator *id.Generator,
	scope tenant.Scope,
	newGrant func(uint32) evidence.Grant,
	runner evidence.Runner,
	attribution evidence.CommandAttribution,
	base time.Time,
) {
	t.Helper()
	grant := newGrant(1)
	redemptionID := mustNewRedemption(t, generator)
	results := make(chan error, 2)
	go func() {
		_, err := store.ClaimGrant(
			t.Context(), scope, grant.ID(), redemptionID, runner,
			base.Add(time.Minute),
		)
		results <- err
	}()
	go func() {
		_, err := store.RevokeGrant(
			t.Context(), scope, grant.ID(), attribution, base.Add(time.Minute),
		)
		results <- err
	}()
	var (
		succeeded int
		denied    int
	)
	for range 2 {
		err := <-results
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, evidence.ErrGrantDenied):
			denied++
		default:
			t.Fatalf("revoke/claim race: %v", err)
		}
	}
	if succeeded < 1 || succeeded+denied != 2 {
		t.Fatalf("revoke/claim race succeeded = %d, denied = %d", succeeded, denied)
	}
	if _, err := store.ClaimGrant(
		t.Context(), scope, grant.ID(), mustNewRedemption(t, generator), runner,
		base.Add(2*time.Minute),
	); !errors.Is(err, evidence.ErrGrantDenied) {
		t.Fatalf("claim after revoke/claim race = %v, want ErrGrantDenied", err)
	}
}

func assertEvidenceGrantRowsImmutable(
	t *testing.T,
	pool *idenqapostgres.Pool,
	grant evidence.Grant,
	terminalID id.Redemption,
	pendingID id.Redemption,
	deniedID id.Redemption,
) {
	t.Helper()
	var (
		uses           int32
		grantAudits    int
		redemptions    int
		outcomes       int
		denialReason   *string
		terminalValue  string
		accessAttempts int
		principalType  string
		auditReason    string
	)
	err := pool.WithinTransaction(
		t.Context(),
		idenqapostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			if err := tx.QueryRow(
				ctx,
				"SELECT uses FROM idenqa.evidence_processing_grants WHERE id = $1",
				grant.ID().String(),
			).Scan(&uses); err != nil {
				return err
			}
			if err := tx.QueryRow(
				ctx,
				"SELECT count(*) FROM idenqa.evidence_processing_grant_audit WHERE grant_id = $1",
				grant.ID().String(),
			).Scan(&grantAudits); err != nil {
				return err
			}
			if err := tx.QueryRow(
				ctx,
				"SELECT principal_type, reason FROM idenqa.evidence_processing_grant_audit WHERE grant_id = $1 AND action = 'create'",
				grant.ID().String(),
			).Scan(&principalType, &auditReason); err != nil {
				return err
			}
			if err := tx.QueryRow(
				ctx,
				"SELECT count(*) FROM idenqa.evidence_grant_access_attempt_audit WHERE tenant_id = $1 AND presented_grant_id = $2",
				grant.TenantID().String(), grant.ID().String(),
			).Scan(&accessAttempts); err != nil {
				return err
			}
			if err := tx.QueryRow(
				ctx,
				"SELECT count(*) FROM idenqa.evidence_grant_redemptions WHERE grant_id = $1",
				grant.ID().String(),
			).Scan(&redemptions); err != nil {
				return err
			}
			if err := tx.QueryRow(
				ctx,
				"SELECT count(*) FROM idenqa.evidence_grant_redemption_outcomes WHERE grant_id = $1",
				grant.ID().String(),
			).Scan(&outcomes); err != nil {
				return err
			}
			if err := tx.QueryRow(
				ctx,
				"SELECT outcome FROM idenqa.evidence_grant_redemption_outcomes WHERE redemption_id = $1",
				terminalID.String(),
			).Scan(&terminalValue); err != nil {
				return err
			}
			return tx.QueryRow(
				ctx,
				"SELECT denial_reason FROM idenqa.evidence_grant_redemptions WHERE id = $1",
				deniedID.String(),
			).Scan(&denialReason)
		},
	)
	if err != nil || uses != 2 || grantAudits != 2 || accessAttempts != 7 ||
		principalType != "internal.service" || auditReason == "" ||
		redemptions != 3 || outcomes != 2 ||
		terminalValue != string(evidence.GrantOutcomeSucceeded) ||
		denialReason == nil || *denialReason != "revoked" {
		t.Fatalf(
			"grant rows uses=%d audits=%d access=%d redemptions=%d outcomes=%d terminal=%q denial=%v error=%v",
			uses, grantAudits, accessAttempts, redemptions, outcomes, terminalValue, denialReason, err,
		)
	}
	err = pool.WithinTransaction(
		t.Context(),
		idenqapostgres.TransactionOptions{},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			_, err := tx.Exec(
				ctx,
				"UPDATE idenqa.evidence_processing_grants SET purpose = 'changed.scope' WHERE id = $1",
				grant.ID().String(),
			)
			return err
		},
	)
	if err == nil {
		t.Fatal("database allowed immutable grant scope update")
	}
	err = pool.WithinTransaction(
		t.Context(),
		idenqapostgres.TransactionOptions{},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			_, err := tx.Exec(
				ctx,
				"UPDATE idenqa.evidence_grant_redemption_outcomes SET outcome = 'failed' WHERE redemption_id = $1",
				terminalID.String(),
			)
			return err
		},
	)
	if err == nil {
		t.Fatal("database allowed immutable redemption outcome update")
	}
	err = pool.WithinTransaction(
		t.Context(),
		idenqapostgres.TransactionOptions{},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			_, err := tx.Exec(
				ctx,
				"INSERT INTO idenqa.evidence_grant_redemption_outcomes (tenant_id, redemption_id, grant_id, outcome, occurred_at) VALUES ($1, $2, $3, 'failed', $4)",
				grant.TenantID().String(), pendingID.String(), grant.ID().String(),
				grant.Record().CreatedAt,
			)
			return err
		},
	)
	if err == nil {
		t.Fatal("database allowed a redemption outcome before its attempt")
	}
	err = pool.WithinTransaction(
		t.Context(),
		idenqapostgres.TransactionOptions{},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			_, err := tx.Exec(
				ctx,
				"INSERT INTO idenqa.evidence_grant_redemptions (id, tenant_id, grant_id, runner_identity, workload_version, denial_reason, attempted_at) VALUES ('rdm_01ARZ3NDEKTSV4RRFFQ69G5FB9', $1, $2, 'bad runner', 'workload.v1', 'exhausted', $3)",
				grant.TenantID().String(), grant.ID().String(), grant.Record().CreatedAt,
			)
			return err
		},
	)
	if err == nil {
		t.Fatal("database allowed malformed redemption runner attribution")
	}
	err = pool.WithinTransaction(
		t.Context(),
		idenqapostgres.TransactionOptions{},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			_, err := tx.Exec(
				ctx,
				"DELETE FROM idenqa.evidence_grant_access_attempt_audit WHERE tenant_id = $1 AND presented_grant_id = $2",
				grant.TenantID().String(), grant.ID().String(),
			)
			return err
		},
	)
	if err == nil {
		t.Fatal("database allowed access-attempt audit deletion")
	}
}

func assertEvidenceHistory(t *testing.T, pool *idenqapostgres.Pool, asset evidence.Asset) {
	t.Helper()
	var (
		auditCount int
		action     string
		reason     *string
	)
	err := pool.WithinTransaction(t.Context(), idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.evidence_asset_audit WHERE evidence_id = $1", asset.ID().String()).Scan(&auditCount); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT action, reason FROM idenqa.evidence_asset_audit WHERE evidence_id = $1 ORDER BY aggregate_version DESC LIMIT 1", asset.ID().String()).Scan(&action, &reason)
	})
	if err != nil || auditCount != 3 || action != "integrity_failed" ||
		reason == nil || *reason != asset.Record().QuarantineReason {
		t.Fatalf("evidence audit count = %d, action = %q, reason = %v, error = %v", auditCount, action, reason, err)
	}
	err = pool.WithinTransaction(t.Context(), idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(ctx, "UPDATE idenqa.evidence_assets SET object_key = 'changed' WHERE id = $1", asset.ID().String())
		return err
	})
	if err == nil {
		t.Fatal("database allowed immutable evidence metadata update")
	}
}

func assertEvidenceRewrapAudit(
	t *testing.T,
	pool *idenqapostgres.Pool,
	asset evidence.Asset,
	before evidence.Record,
	attribution evidence.CommandAttribution,
) {
	t.Helper()
	var (
		previousVersion string
		newVersion      string
		principalType   string
		principalID     string
		tenantActorType string
		tenantActorID   string
		reason          string
	)
	err := pool.WithinTransaction(
		t.Context(), idenqapostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			return tx.QueryRow(
				ctx,
				`SELECT previous_key_version, new_key_version, principal_type, principal_id,
                        tenant_actor_type, tenant_actor_id, reason
                   FROM idenqa.evidence_key_rewrap_audit
                  WHERE tenant_id = $1 AND evidence_id = $2 AND aggregate_version = $3`,
				asset.Record().TenantID.String(), asset.ID().String(), asset.Version(),
			).Scan(
				&previousVersion, &newVersion, &principalType, &principalID,
				&tenantActorType, &tenantActorID, &reason,
			)
		},
	)
	if err != nil || previousVersion != before.Content.Envelope.WrappedKey.Version ||
		newVersion != asset.Record().Content.Envelope.WrappedKey.Version ||
		principalType != attribution.Principal.Type || principalID != attribution.Principal.ID ||
		tenantActorType != attribution.TenantActor.Type || tenantActorID != attribution.TenantActor.ID ||
		reason != attribution.Reason {
		t.Fatalf("evidence rewrap audit mismatch: previous=%q new=%q attribution=%q/%q %q/%q reason=%q error=%v",
			previousVersion, newVersion, principalType, principalID, tenantActorType, tenantActorID, reason, err)
	}
	err = pool.WithinTransaction(
		t.Context(), idenqapostgres.TransactionOptions{},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			_, err := tx.Exec(
				ctx, "DELETE FROM idenqa.evidence_key_rewrap_audit WHERE evidence_id = $1",
				asset.ID().String(),
			)

			return err
		},
	)
	if err == nil {
		t.Fatal("database allowed evidence key rewrap audit deletion")
	}
}

func assertEvidenceReconciliationState(
	t *testing.T,
	pool *idenqapostgres.Pool,
	tenantID id.Tenant,
	uploadID id.Upload,
	attempt uint32,
	want string,
) {
	t.Helper()
	var state string
	err := pool.WithinTransaction(
		t.Context(),
		idenqapostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, tx idenqapostgres.Transaction) error {
			if _, err := tx.Exec(ctx, "SELECT set_config('idenqa.tenant_id', $1, true)", tenantID.String()); err != nil {
				return err
			}
			return tx.QueryRow(
				ctx,
				`SELECT state FROM idenqa.evidence_object_reconciliations
                 WHERE tenant_id = $1 AND upload_id = $2 AND upload_attempt = $3`,
				tenantID.String(), uploadID.String(), int64(attempt),
			).Scan(&state)
		},
	)
	if err != nil {
		t.Fatalf("read evidence reconciliation state: %v", err)
	}
	if state != want {
		t.Fatalf("evidence reconciliation state = %q, want %q", state, want)
	}
}

func assertAuthorityHistoryImmutable(
	t *testing.T,
	pool *idenqapostgres.Pool,
	notice authority.Notice,
	response authority.Response,
) {
	t.Helper()
	err := pool.WithinTransaction(t.Context(), idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(ctx, "UPDATE idenqa.notice_versions SET title = 'changed' WHERE id = $1", notice.ID().String())
		return err
	})
	if err == nil {
		t.Fatal("database allowed immutable notice update")
	}
	err = pool.WithinTransaction(t.Context(), idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(ctx, "DELETE FROM idenqa.subject_responses WHERE id = $1", response.Record().ID.String())
		return err
	})
	if err == nil {
		t.Fatal("database allowed append-only response deletion")
	}
}
