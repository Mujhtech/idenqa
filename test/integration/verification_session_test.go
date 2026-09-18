//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/realtime"
	realtimepostgres "github.com/Mujhtech/idenqa/internal/realtime/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

func TestVerificationSessionAtomicSnapshotReplayAndIsolation(t *testing.T) {
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
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("new identifier generator: %v", err)
	}
	tenantStore, err := tenantpostgres.New(adminPool)
	if err != nil {
		t.Fatalf("new tenant store: %v", err)
	}
	tenantAdmin, err := tenant.NewAdmin(tenantStore, generator, clock.System{})
	if err != nil {
		t.Fatalf("new tenant admin: %v", err)
	}
	action := tenant.AdminAction{Actor: "integration-operator", Reason: "verify session creation"}
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
	firstScope, err := tenant.NewScope(firstTenant.ID())
	if err != nil {
		t.Fatalf("new first tenant scope: %v", err)
	}
	secondScope, err := tenant.NewScope(secondTenant.ID())
	if err != nil {
		t.Fatalf("new second tenant scope: %v", err)
	}
	accessStore, err := accesspostgres.New(runtimePool)
	if err != nil {
		t.Fatalf("new access store: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	policyID := seedIntegrationPolicy(t, adminPool, firstTenant.ID(), generator, now)
	actor := newIntegrationKey(t, generator, firstTenant.ID(), now, id.APIKey{})
	if err := accessStore.Create(ctx, firstScope, actor); err != nil {
		t.Fatalf("create actor key: %v", err)
	}

	registry, err := evidence.BuiltInRegistry()
	if err != nil {
		t.Fatalf("built-in registry: %v", err)
	}
	catalog, err := evidence.NewCatalog(registry)
	if err != nil {
		t.Fatalf("new registry catalog: %v", err)
	}
	profileStore, err := verificationpostgres.New(runtimePool, catalog)
	if err != nil {
		t.Fatalf("new profile store: %v", err)
	}
	profileID, err := generator.NewProfile()
	if err != nil {
		t.Fatalf("new profile id: %v", err)
	}
	profile, draft, err := verification.NewCaptureProfile(
		profileID,
		firstTenant.ID(),
		"Session profile",
		integrationProfileDocument(t, registry, evidence.MethodLiveCamera),
		registry,
		now,
	)
	if err != nil {
		t.Fatalf("new capture profile: %v", err)
	}
	createProfileRequest := integrationIdempotencyRequest(
		t,
		firstTenant.ID(),
		actor.ID(),
		verification.OperationCreateProfile,
		"session-profile-create",
		[]byte(`{"name":"Session profile"}`),
		now,
	)
	if _, err := profileStore.Apply(ctx, firstScope, verification.Mutation{
		Kind:        verification.MutationCreate,
		Profile:     profile,
		Revision:    draft,
		Actor:       actor.ID(),
		Idempotency: createProfileRequest,
		Result:      integrationMutationResult(profile, draft),
		Status:      201,
	}); err != nil {
		t.Fatalf("create capture profile: %v", err)
	}
	publishedProfile, publishedRevision, _, err := profile.PublishDraft(1, draft, nil, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("publish draft: %v", err)
	}
	publishRequest := integrationIdempotencyRequest(
		t,
		firstTenant.ID(),
		actor.ID(),
		verification.OperationPublishProfile,
		"session-profile-publish",
		[]byte(`{"expected_version":1}`),
		now.Add(time.Minute),
	)
	if _, err := profileStore.Apply(ctx, firstScope, verification.Mutation{
		Kind:            verification.MutationPublish,
		Profile:         publishedProfile,
		Revision:        publishedRevision,
		ExpectedVersion: 1,
		Actor:           actor.ID(),
		Idempotency:     publishRequest,
		Result:          integrationMutationResult(publishedProfile, publishedRevision),
		Status:          200,
	}); err != nil {
		t.Fatalf("publish capture profile: %v", err)
	}

	sessionStore, err := verificationpostgres.NewSessionStore(runtimePool, integrationProtector{}, catalog)
	if err != nil {
		t.Fatalf("new session store: %v", err)
	}
	mutation := newIntegrationSessionMutation(t, generator, firstTenant.ID(), actor.ID(), profileID, policyID, now.Add(2*time.Minute), "session-create")
	created, err := sessionStore.Create(ctx, firstScope, mutation)
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	replayed, err := sessionStore.Create(ctx, firstScope, mutation)
	if err != nil {
		t.Fatalf("Create(replay) error = %v", err)
	}
	if replayed.Session.ID().String() != created.Session.ID().String() ||
		replayed.Credential.ID().String() != created.Credential.ID().String() ||
		replayed.OutcomeCredential.ID().String() != created.OutcomeCredential.ID().String() {
		t.Fatalf("replay identities = %s/%s/%s, want %s/%s/%s",
			replayed.Session.ID(), replayed.Credential.ID(), replayed.OutcomeCredential.ID(),
			created.Session.ID(), created.Credential.ID(), created.OutcomeCredential.ID())
	}
	conflictingMutation := mutation
	conflictingMutation.Idempotency = integrationIdempotencyRequest(
		t,
		firstTenant.ID(),
		actor.ID(),
		verification.OperationCreateVerification,
		"session-create",
		[]byte(`{"capture_profile_id":"`+profileID.String()+`","verification_ttl_seconds":3600,"capture_token_ttl_seconds":600,"region":"local"}`),
		mutation.CreatedAt.Add(time.Second),
	)
	if _, err := sessionStore.Create(ctx, firstScope, conflictingMutation); !errors.Is(err, idempotency.ErrConflict) {
		t.Fatalf("Create(conflicting replay) error = %v, want ErrConflict", err)
	}
	if created.Session.ProfileRevision() != 1 ||
		created.Session.ProfileDigest() != publishedRevision.Digest() ||
		created.Session.Region() != "local" {
		t.Fatalf("session snapshot source = %d/%s", created.Session.ProfileRevision(), created.Session.ProfileDigest())
	}
	if _, err := sessionStore.FindSession(ctx, secondScope, created.Session.ID()); !errors.Is(err, verification.ErrSessionNotFound) {
		t.Fatalf("cross-tenant FindSession() error = %v, want ErrSessionNotFound", err)
	}

	keyring, err := access.NewCaptureTokenKeyring(1, map[access.CaptureTokenKeyVersion][]byte{
		1: bytes.Repeat([]byte{7}, 32),
	})
	if err != nil {
		t.Fatalf("new capture-token keyring: %v", err)
	}
	signer, err := access.NewCaptureTokenSigner(keyring, fixedIntegrationClock{now: mutation.CreatedAt})
	if err != nil {
		t.Fatalf("new capture-token signer: %v", err)
	}
	firstToken, err := signer.Sign(created.Credential)
	if err != nil {
		t.Fatalf("sign created credential: %v", err)
	}
	replayedToken, err := signer.Sign(replayed.Credential)
	if err != nil {
		t.Fatalf("sign replayed credential: %v", err)
	}
	if firstToken.Reveal() != replayedToken.Reveal() {
		t.Fatal("replay did not reconstruct the exact capture token")
	}
	outcomeKeyring, err := access.NewOutcomeTokenKeyring(1, map[access.OutcomeTokenKeyVersion][]byte{
		1: bytes.Repeat([]byte{8}, 32),
	})
	if err != nil {
		t.Fatalf("new outcome-token keyring: %v", err)
	}
	outcomeSigner, err := access.NewOutcomeTokenSigner(
		outcomeKeyring,
		fixedIntegrationClock{now: mutation.SessionExpiresAt.Add(time.Minute)},
	)
	if err != nil {
		t.Fatalf("new outcome-token signer: %v", err)
	}
	outcomeToken, err := outcomeSigner.Sign(created.OutcomeCredential)
	if err != nil {
		t.Fatalf("sign outcome credential: %v", err)
	}
	replayedOutcomeToken, err := outcomeSigner.Sign(replayed.OutcomeCredential)
	if err != nil || replayedOutcomeToken.Reveal() != outcomeToken.Reveal() {
		t.Fatalf("replay did not reconstruct the exact outcome token: %v", err)
	}
	outcomeAuthenticator, err := verification.NewOutcomeAuthenticator(
		sessionStore,
		outcomeSigner,
		fixedIntegrationClock{now: mutation.SessionExpiresAt.Add(time.Minute)},
	)
	if err != nil {
		t.Fatalf("new outcome authenticator: %v", err)
	}
	if authenticated, err := outcomeAuthenticator.Authenticate(ctx, outcomeToken.Reveal()); err != nil ||
		authenticated.VerificationID().String() != created.Session.ID().String() {
		t.Fatalf("outcome authentication after session expiry = %s, %v", authenticated.VerificationID(), err)
	}
	expiredCaptureSigner, err := access.NewCaptureTokenSigner(
		keyring,
		fixedIntegrationClock{now: mutation.SessionExpiresAt.Add(time.Minute)},
	)
	if err != nil {
		t.Fatalf("new expired capture-token signer: %v", err)
	}
	expiredCaptureAuthenticator, err := verification.NewCaptureAuthenticator(
		sessionStore,
		expiredCaptureSigner,
		fixedIntegrationClock{now: mutation.SessionExpiresAt.Add(time.Minute)},
	)
	if err != nil {
		t.Fatalf("new expired capture authenticator: %v", err)
	}
	if _, err := expiredCaptureAuthenticator.Authenticate(ctx, firstToken.Reveal()); !errors.Is(err, access.ErrInvalidCaptureToken) {
		t.Fatalf("Authenticate(expired capture token) error = %v, want invalid capture token", err)
	}
	claims, err := signer.Verify(firstToken.Reveal())
	if err != nil {
		t.Fatalf("verify capture token: %v", err)
	}
	credential, err := sessionStore.FindCaptureCredential(ctx, firstScope, claims)
	if err != nil || credential.ID().String() != created.Credential.ID().String() {
		t.Fatalf("FindCaptureCredential() = %s, %v", credential.ID(), err)
	}
	nativeBoundAt := mutation.CreatedAt.Add(time.Second)
	if err := sessionStore.BindNativeApplication(ctx, firstScope, created.Credential.ID(), "dev.idenqa.fixture", strings.Repeat("a", 64), nativeBoundAt); err != nil {
		t.Fatalf("BindNativeApplication() error = %v", err)
	}
	if err := sessionStore.BindNativeApplication(ctx, firstScope, created.Credential.ID(), "dev.idenqa.fixture", strings.Repeat("a", 64), nativeBoundAt); !errors.Is(err, access.ErrInvalidCaptureToken) {
		t.Fatalf("BindNativeApplication(replay) error = %v, want invalid token", err)
	}
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		var applicationID, keyDigest string
		var boundAt time.Time
		if err := tx.QueryRow(ctx, `SELECT native_application_id,native_proof_key_digest,native_bound_at
			FROM idenqa.capture_tokens WHERE tenant_id=$1 AND id=$2`, firstTenant.ID().String(), created.Credential.ID().String()).Scan(&applicationID, &keyDigest, &boundAt); err != nil {
			return err
		}
		if applicationID != "dev.idenqa.fixture" || keyDigest != strings.Repeat("a", 64) || !boundAt.Equal(nativeBoundAt) {
			t.Fatalf("native binding = %q/%q/%s", applicationID, keyDigest, boundAt)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read native binding: %v", err)
	}
	captureAuthenticator, err := verification.NewCaptureAuthenticator(
		sessionStore,
		signer,
		fixedIntegrationClock{now: mutation.CreatedAt},
	)
	if err != nil {
		t.Fatalf("new capture authenticator: %v", err)
	}
	captureContext, err := captureAuthenticator.Authenticate(ctx, firstToken.Reveal())
	if err != nil || captureContext.Session().ID().String() != created.Session.ID().String() {
		t.Fatalf("Authenticate(capture) session = %s, error = %v", captureContext.Session().ID(), err)
	}
	ticketStore, err := realtimepostgres.New(runtimePool)
	if err != nil {
		t.Fatalf("new realtime ticket store: %v", err)
	}
	ticketGenerator, err := realtime.NewTicketGenerator(bytes.NewReader(bytes.Repeat([]byte{9}, 256)))
	if err != nil {
		t.Fatalf("new realtime ticket generator: %v", err)
	}
	ticketService, err := realtime.NewService(
		ticketStore,
		generator,
		ticketGenerator,
		fixedIntegrationClock{now: mutation.CreatedAt},
		realtime.DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("new realtime ticket service: %v", err)
	}
	binding, err := realtime.NewBrowserBinding("https://capture.example.com")
	if err != nil {
		t.Fatalf("new realtime browser binding: %v", err)
	}
	issuedTicket, err := ticketService.Issue(ctx, realtime.IssueInput{
		Scope: firstScope, VerificationID: created.Session.ID(), CaptureTokenID: created.Credential.ID(),
		Binding: binding, Region: "lagos-1", SessionExpiresAt: created.Session.ExpiresAt(),
	})
	if err != nil {
		t.Fatalf("Issue(connection ticket) error = %v", err)
	}
	wrongBinding, err := realtime.NewBrowserBinding("https://other.example.com")
	if err != nil {
		t.Fatalf("new wrong browser binding: %v", err)
	}
	if _, err := ticketService.Redeem(
		ctx, issuedTicket.Credential.Reveal(), wrongBinding, "lagos-1", realtime.SubprotocolV1,
	); !errors.Is(err, realtime.ErrInvalidTicket) {
		t.Fatalf("Redeem(wrong origin) error = %v, want ErrInvalidTicket", err)
	}
	encodedTicket := issuedTicket.Credential.Reveal()
	tenantPayloadStart := len("idq_wst_v1_")
	tamperedTenantTicket := encodedTicket[:tenantPayloadStart] + secondTenant.ID().String()[4:] +
		encodedTicket[tenantPayloadStart+26:]
	if _, err := ticketService.Redeem(
		ctx, tamperedTenantTicket, binding, "lagos-1", realtime.SubprotocolV1,
	); !errors.Is(err, realtime.ErrInvalidTicket) {
		t.Fatalf("Redeem(cross-tenant hint) error = %v, want ErrInvalidTicket", err)
	}
	secretStart := len(encodedTicket) - 43
	wrongSecretCharacter := "A"
	if encodedTicket[secretStart] == 'A' {
		wrongSecretCharacter = "B"
	}
	wrongSecretTicket := encodedTicket[:secretStart] + wrongSecretCharacter + encodedTicket[secretStart+1:]
	if _, err := ticketService.Redeem(
		ctx, wrongSecretTicket, binding, "lagos-1", realtime.SubprotocolV1,
	); !errors.Is(err, realtime.ErrInvalidTicket) {
		t.Fatalf("Redeem(wrong secret) error = %v, want ErrInvalidTicket", err)
	}
	if _, err := ticketService.Redeem(
		ctx, encodedTicket, binding, "abuja-1", realtime.SubprotocolV1,
	); !errors.Is(err, realtime.ErrInvalidTicket) {
		t.Fatalf("Redeem(wrong region) error = %v, want ErrInvalidTicket", err)
	}
	type redemptionResult struct {
		ticket realtime.Ticket
		err    error
	}
	results := make(chan redemptionResult, 2)
	for range 2 {
		go func() {
			ticket, redeemErr := ticketService.Redeem(
				ctx, encodedTicket, binding, "lagos-1", realtime.SubprotocolV1,
			)
			results <- redemptionResult{ticket: ticket, err: redeemErr}
		}()
	}
	var redemptionSuccesses, redemptionRejections int
	var redeemedTicket realtime.Ticket
	for range 2 {
		result := <-results
		switch {
		case result.err == nil:
			redemptionSuccesses++
			redeemedTicket = result.ticket
		case errors.Is(result.err, realtime.ErrInvalidTicket):
			redemptionRejections++
		default:
			t.Fatalf("concurrent Redeem() error = %v", result.err)
		}
	}
	if redemptionSuccesses != 1 || redemptionRejections != 1 || redeemedTicket.ConnectionID().IsZero() {
		t.Fatalf("concurrent redemption successes/rejections = %d/%d", redemptionSuccesses, redemptionRejections)
	}
	authority, err := ticketStore.LoadSessionAuthority(ctx, redeemedTicket, mutation.CreatedAt)
	if err != nil {
		t.Fatalf("LoadSessionAuthority(current) error = %v", err)
	}
	if authority.Version() != 1 || !authority.ExpiresAt().Equal(created.Session.ExpiresAt()) {
		t.Fatalf("LoadSessionAuthority(current) = version %d, expiry %s", authority.Version(), authority.ExpiresAt())
	}
	replayStore, err := realtimepostgres.NewReplayStore(runtimePool)
	if err != nil {
		t.Fatalf("new realtime replay store: %v", err)
	}
	firstReplayID, err := generator.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	replayExpiresAt := mutation.CreatedAt.Add(5 * time.Second)
	firstReplayIntent, err := realtime.NewEventIntent(
		firstReplayID, firstScope.ID(), created.Session.ID(), id.Command{}, id.Message{}, id.Message{},
		mutation.CreatedAt, replayExpiresAt, realtime.CaptureProgress{CompletedSteps: 1, TotalSteps: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	firstReplayEvent, err := replayStore.Append(ctx, firstReplayIntent)
	if err != nil || firstReplayEvent.Cursor() != 1 {
		t.Fatalf("Append(first realtime event) = %#v, %v", firstReplayEvent, err)
	}
	replayedEvent, err := replayStore.Append(ctx, firstReplayIntent)
	if err != nil || replayedEvent.Cursor() != firstReplayEvent.Cursor() {
		t.Fatalf("Append(idempotent realtime event) = %#v, %v", replayedEvent, err)
	}
	conflictingIntent, err := realtime.NewEventIntent(
		firstReplayID, firstScope.ID(), created.Session.ID(), id.Command{}, id.Message{}, id.Message{},
		mutation.CreatedAt, replayExpiresAt, realtime.CaptureProgress{CompletedSteps: 2, TotalSteps: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replayStore.Append(ctx, conflictingIntent); !errors.Is(err, realtime.ErrDurableEventConflict) {
		t.Fatalf("Append(conflicting realtime event) error = %v", err)
	}
	secondReplayID, err := generator.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	secondReplayIntent, err := realtime.NewEventIntent(
		secondReplayID, firstScope.ID(), created.Session.ID(), id.Command{}, id.Message{}, id.Message{},
		mutation.CreatedAt.Add(time.Second), replayExpiresAt,
		realtime.SessionStateChanged{State: "processing", SessionVersion: 2},
	)
	if err != nil {
		t.Fatal(err)
	}
	if second, appendErr := replayStore.Append(ctx, secondReplayIntent); appendErr != nil || second.Cursor() != 2 {
		t.Fatalf("Append(second realtime event) = %#v, %v", second, appendErr)
	}
	firstPage, err := replayStore.Replay(ctx, redeemedTicket, 0, 1, mutation.CreatedAt.Add(2*time.Second))
	if err != nil || firstPage.Gap() || !firstPage.HasMore() || len(firstPage.Events()) != 1 ||
		firstPage.Events()[0].Cursor() != 1 {
		t.Fatalf("Replay(first page) = %#v, %v", firstPage, err)
	}
	secondPage, err := replayStore.Replay(ctx, redeemedTicket, 1, 2, mutation.CreatedAt.Add(2*time.Second))
	if err != nil || secondPage.HasMore() || len(secondPage.Events()) != 1 || secondPage.Events()[0].Cursor() != 2 {
		t.Fatalf("Replay(second page) = %#v, %v", secondPage, err)
	}
	otherNodeStore, err := realtimepostgres.NewReplayStore(runtimePool)
	if err != nil {
		t.Fatal(err)
	}
	otherNodePage, err := otherNodeStore.Replay(
		ctx, redeemedTicket, 1, 2, mutation.CreatedAt.Add(2*time.Second),
	)
	if err != nil || len(otherNodePage.Events()) != 1 || otherNodePage.Events()[0].Cursor() != 2 {
		t.Fatalf("Replay(other API node) = %#v, %v", otherNodePage, err)
	}
	if err := replayStore.Acknowledge(ctx, redeemedTicket, 2, mutation.CreatedAt.Add(2*time.Second)); err != nil {
		t.Fatalf("Acknowledge(realtime cursor) error = %v", err)
	}
	if err := replayStore.Acknowledge(ctx, redeemedTicket, 1, mutation.CreatedAt.Add(3*time.Second)); err != nil {
		t.Fatalf("Acknowledge(stale realtime cursor) error = %v", err)
	}
	if err := replayStore.Acknowledge(ctx, redeemedTicket, 3, mutation.CreatedAt.Add(3*time.Second)); !errors.Is(err, realtime.ErrReplayCursorAhead) {
		t.Fatalf("Acknowledge(ahead realtime cursor) error = %v", err)
	}
	replayCleanup, err := realtime.NewReplayCleanupService(
		replayStore,
		fixedIntegrationClock{now: replayExpiresAt},
	)
	if err != nil {
		t.Fatal(err)
	}
	if deleted, cleanupErr := replayCleanup.CleanupExpiredEvents(ctx, firstScope, 100); cleanupErr != nil || deleted != 2 {
		t.Fatalf("CleanupExpiredEvents() = %d, %v", deleted, cleanupErr)
	}
	gap, err := replayStore.Replay(ctx, redeemedTicket, 0, 10, replayExpiresAt)
	if err != nil || !gap.Gap() || gap.RetainedFrom() != 3 || gap.Latest() != 2 {
		t.Fatalf("Replay(expired gap) = %#v, %v", gap, err)
	}
	commandStore, err := realtimepostgres.NewCommandStore(runtimePool, catalog)
	if err != nil {
		t.Fatalf("new realtime command store: %v", err)
	}
	commandService, err := realtime.NewCommandService(
		commandStore,
		fixedIntegrationClock{now: mutation.CreatedAt},
	)
	if err != nil {
		t.Fatalf("new realtime command service: %v", err)
	}
	commandID, err := generator.NewCommand()
	if err != nil {
		t.Fatalf("new realtime command id: %v", err)
	}
	acceptedMessage := integrationCaptureStepMessage(
		t, generator, redeemedTicket, commandID, mutation.CreatedAt,
		realtime.CaptureStepUpdate{
			State: realtime.StepStarted, RequirementKey: "selfie",
			Artefact:          string(evidence.ArtefactSelfieImage),
			AcquisitionMethod: string(evidence.MethodLiveCamera),
		},
	)
	accepted, err := commandService.HandleClientCommand(ctx, redeemedTicket, acceptedMessage)
	if err != nil || accepted.Disposition != realtime.CommandDispositionAccepted {
		t.Fatalf("HandleClientCommand(accepted) = %#v, %v", accepted, err)
	}
	replayMessage := integrationCaptureStepMessage(
		t, generator, redeemedTicket, commandID, mutation.CreatedAt.Add(time.Second),
		acceptedMessage.Payload().(realtime.CaptureStepUpdate),
	)
	replayedCommand, err := commandService.HandleClientCommand(ctx, redeemedTicket, replayMessage)
	if err != nil || replayedCommand.Disposition != realtime.CommandDispositionAccepted {
		t.Fatalf("HandleClientCommand(replay) = %#v, %v", replayedCommand, err)
	}
	conflictingMessage := integrationCaptureStepMessage(
		t, generator, redeemedTicket, commandID, mutation.CreatedAt.Add(time.Second),
		realtime.CaptureStepUpdate{
			State: realtime.StepFailed, RequirementKey: "selfie",
			Artefact:          string(evidence.ArtefactSelfieImage),
			AcquisitionMethod: string(evidence.MethodLiveCamera), Code: "camera_unavailable",
		},
	)
	conflictedCommand, err := commandService.HandleClientCommand(ctx, redeemedTicket, conflictingMessage)
	if err != nil || conflictedCommand.Code != string(realtime.CommandStateConflict) {
		t.Fatalf("HandleClientCommand(conflict) = %#v, %v", conflictedCommand, err)
	}
	policyCommandID, err := generator.NewCommand()
	if err != nil {
		t.Fatalf("new policy-conflict command id: %v", err)
	}
	policyConflict, err := commandService.HandleClientCommand(
		ctx,
		redeemedTicket,
		integrationCaptureStepMessage(
			t, generator, redeemedTicket, policyCommandID, mutation.CreatedAt,
			realtime.CaptureStepUpdate{
				State: realtime.StepStarted, RequirementKey: "selfie",
				Artefact:          string(evidence.ArtefactDocumentFront),
				AcquisitionMethod: string(evidence.MethodLiveCamera),
			},
		),
	)
	if err != nil || policyConflict.Code != string(realtime.CommandPolicyConflict) {
		t.Fatalf("HandleClientCommand(policy conflict) = %#v, %v", policyConflict, err)
	}
	if _, err := ticketStore.LoadSessionAuthority(
		ctx,
		redeemedTicket,
		mutation.CaptureTokenExpiry,
	); !errors.Is(err, realtime.ErrSessionUnavailable) {
		t.Fatalf("LoadSessionAuthority(at capture-token expiry) error = %v, want ErrSessionUnavailable", err)
	}
	expiringTicket, err := ticketService.Issue(ctx, realtime.IssueInput{
		Scope: firstScope, VerificationID: created.Session.ID(), CaptureTokenID: created.Credential.ID(),
		Binding: binding, Region: "lagos-1", SessionExpiresAt: created.Session.ExpiresAt(),
	})
	if err != nil {
		t.Fatalf("Issue(expiring connection ticket) error = %v", err)
	}
	expiredService, err := realtime.NewService(
		ticketStore,
		generator,
		ticketGenerator,
		fixedIntegrationClock{now: expiringTicket.Ticket.ExpiresAt()},
		realtime.DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("new expiry-bound ticket service: %v", err)
	}
	if _, err := expiredService.Redeem(
		ctx, expiringTicket.Credential.Reveal(), binding, "lagos-1", realtime.SubprotocolV1,
	); !errors.Is(err, realtime.ErrInvalidTicket) {
		t.Fatalf("Redeem(at expiry) error = %v, want ErrInvalidTicket", err)
	}
	cleanupService, err := realtime.NewCleanupService(
		ticketStore,
		fixedIntegrationClock{now: expiringTicket.Ticket.ExpiresAt()},
	)
	if err != nil {
		t.Fatalf("new realtime cleanup service: %v", err)
	}
	deletedTickets, err := cleanupService.CleanupExpired(ctx, firstScope, 1)
	if err != nil || deletedTickets != 1 {
		t.Fatalf("CleanupExpired() = %d, %v, want 1", deletedTickets, err)
	}
	nearTokenExpiryService, err := realtime.NewService(
		ticketStore,
		generator,
		ticketGenerator,
		fixedIntegrationClock{now: mutation.CaptureTokenExpiry.Add(-20 * time.Second)},
		realtime.DefaultLimits(),
	)
	if err != nil {
		t.Fatalf("new capture-token-expiry service: %v", err)
	}
	if _, err := nearTokenExpiryService.Issue(ctx, realtime.IssueInput{
		Scope: firstScope, VerificationID: created.Session.ID(), CaptureTokenID: created.Credential.ID(),
		Binding: binding, Region: "lagos-1", SessionExpiresAt: created.Session.ExpiresAt(),
	}); !errors.Is(err, realtime.ErrTicketUnavailable) {
		t.Fatalf("Issue(beyond capture-token expiry) error = %v, want ErrTicketUnavailable", err)
	}
	var storedTicketDigest []byte
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		return tx.QueryRow(
			ctx,
			"SELECT digest FROM idenqa.websocket_connection_tickets WHERE tenant_id = $1 AND id = $2",
			firstTenant.ID().String(), issuedTicket.Ticket.ID().String(),
		).Scan(&storedTicketDigest)
	})
	if err != nil {
		t.Fatalf("inspect stored connection-ticket digest: %v", err)
	}
	if len(storedTicketDigest) != 32 || bytes.Contains(storedTicketDigest, []byte(encodedTicket)) ||
		strings.Contains(string(storedTicketDigest), "idq_wst_v1_") {
		t.Fatal("database contains invalid or exposed connection-ticket material")
	}
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(
			ctx,
			"UPDATE idenqa.websocket_connection_tickets SET redeemed_at = NULL, connection_id = NULL WHERE id = $1",
			issuedTicket.Ticket.ID().String(),
		)

		return err
	})
	if err == nil {
		t.Fatal("database allowed connection-ticket redemption reversal")
	}
	var recordedCommands int
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		return tx.QueryRow(
			ctx,
			"SELECT count(*) FROM idenqa.realtime_client_commands WHERE tenant_id = $1",
			firstTenant.ID().String(),
		).Scan(&recordedCommands)
	})
	if err != nil || recordedCommands != 2 {
		t.Fatalf("recorded realtime commands = %d, %v, want 2", recordedCommands, err)
	}
	err = runtimePool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('idenqa.tenant_id', $1, true)", secondTenant.ID().String()); err != nil {
			return err
		}

		return tx.QueryRow(
			ctx,
			"SELECT count(*) FROM idenqa.realtime_client_commands WHERE command_id = $1",
			commandID.String(),
		).Scan(&recordedCommands)
	})
	if err != nil || recordedCommands != 0 {
		t.Fatalf("cross-tenant realtime command count = %d, %v, want 0", recordedCommands, err)
	}
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(
			ctx,
			"UPDATE idenqa.realtime_client_commands SET disposition = 'rejected', rejection_code = 'policy_conflict' WHERE command_id = $1",
			commandID.String(),
		)

		return err
	})
	if err == nil {
		t.Fatal("database allowed realtime command record mutation")
	}
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(
			ctx,
			"UPDATE idenqa.verification_sessions SET requirements = '{}'::jsonb WHERE id = $1",
			created.Session.ID().String(),
		)

		return err
	})
	if err == nil {
		t.Fatal("database allowed verification-session snapshot mutation")
	}
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(
			ctx,
			"UPDATE idenqa.verification_sessions SET region = 'other-region' WHERE id = $1",
			created.Session.ID().String(),
		)

		return err
	})
	if err == nil {
		t.Fatal("database allowed verification-session processing-region mutation")
	}
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(
			ctx,
			"UPDATE idenqa.outcome_tokens SET revoked_at = $2 WHERE id = $1",
			created.OutcomeCredential.ID().String(),
			mutation.CreatedAt.Add(time.Second),
		)

		return err
	})
	if err != nil {
		t.Fatalf("revoke outcome token record: %v", err)
	}
	if _, err := outcomeAuthenticator.Authenticate(ctx, outcomeToken.Reveal()); !errors.Is(err, access.ErrInvalidOutcomeToken) {
		t.Fatalf("Authenticate(revoked outcome) error = %v, want invalid outcome token", err)
	}
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(
			ctx,
			"UPDATE idenqa.outcome_tokens SET revoked_at = NULL WHERE id = $1",
			created.OutcomeCredential.ID().String(),
		)

		return err
	})
	if err == nil {
		t.Fatal("database allowed outcome-token revocation reversal")
	}
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(
			ctx,
			"UPDATE idenqa.capture_tokens SET revoked_at = $2 WHERE id = $1",
			created.Credential.ID().String(),
			mutation.CreatedAt.Add(time.Second),
		)

		return err
	})
	if err != nil {
		t.Fatalf("revoke capture token record: %v", err)
	}
	authorityCommandID, err := generator.NewCommand()
	if err != nil {
		t.Fatalf("new unavailable-authority command id: %v", err)
	}
	authorityUnavailable, err := commandService.HandleClientCommand(
		ctx,
		redeemedTicket,
		integrationCaptureStepMessage(
			t, generator, redeemedTicket, authorityCommandID, mutation.CreatedAt.Add(2*time.Second),
			acceptedMessage.Payload().(realtime.CaptureStepUpdate),
		),
	)
	if err != nil || authorityUnavailable.Code != string(realtime.CommandAuthorityUnavailable) {
		t.Fatalf("HandleClientCommand(authority unavailable) = %#v, %v", authorityUnavailable, err)
	}
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(
			ctx,
			"UPDATE idenqa.capture_tokens SET revoked_at = NULL WHERE id = $1",
			created.Credential.ID().String(),
		)

		return err
	})
	if err == nil {
		t.Fatal("database allowed capture-token revocation reversal")
	}

	suppressedMutation := newIntegrationSessionMutation(
		t,
		generator,
		firstTenant.ID(),
		actor.ID(),
		profileID,
		policyID,
		now.Add(150*time.Second),
		"session-create-concurrent",
	)
	lockHeld := make(chan struct{})
	releaseLock := make(chan struct{})
	lockDone := make(chan error, 1)
	go func() {
		lockDone <- adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
			if _, err := tx.Exec(
				ctx,
				`SELECT pg_advisory_xact_lock(hashtextextended(concat_ws(E'\x1f', $1::text, $2::text, $3::text, $4::text), 0))`,
				suppressedMutation.Idempotency.TenantID().String(),
				suppressedMutation.Idempotency.Principal().String(),
				suppressedMutation.Idempotency.Operation(),
				suppressedMutation.Idempotency.Key(),
			); err != nil {
				return err
			}
			close(lockHeld)
			<-releaseLock

			return nil
		})
	}()
	<-lockHeld
	if _, err := sessionStore.Create(ctx, firstScope, suppressedMutation); !errors.Is(err, idempotency.ErrInProgress) {
		t.Fatalf("Create(concurrent duplicate) error = %v, want ErrInProgress", err)
	}
	close(releaseLock)
	if err := <-lockDone; err != nil {
		t.Fatalf("hold idempotency lock: %v", err)
	}
	if _, err := sessionStore.FindSession(ctx, firstScope, suppressedMutation.SessionID); !errors.Is(err, verification.ErrSessionNotFound) {
		t.Fatalf("suppressed session lookup error = %v, want ErrSessionNotFound", err)
	}

	failedMutation := newIntegrationSessionMutation(t, generator, firstTenant.ID(), actor.ID(), profileID, policyID, now.Add(3*time.Minute), "session-create-rollback")
	failedMutation.EventID = mutation.EventID
	if _, err := sessionStore.Create(ctx, firstScope, failedMutation); err == nil {
		t.Fatal("Create() with duplicate outbox event unexpectedly succeeded")
	}
	if _, err := sessionStore.FindSession(ctx, firstScope, failedMutation.SessionID); !errors.Is(err, verification.ErrSessionNotFound) {
		t.Fatalf("rolled-back session lookup error = %v, want ErrSessionNotFound", err)
	}
	deactivatedProfile, _, err := publishedProfile.Deactivate(2, nil, now.Add(5*time.Minute))
	if err != nil {
		t.Fatalf("deactivate source profile: %v", err)
	}
	deactivateRequest := integrationIdempotencyRequest(
		t,
		firstTenant.ID(),
		actor.ID(),
		verification.OperationDeactivateProfile,
		"session-profile-deactivate",
		[]byte(`{"expected_version":2}`),
		now.Add(5*time.Minute),
	)
	if _, err := profileStore.Apply(ctx, firstScope, verification.Mutation{
		Kind:            verification.MutationDeactivate,
		Profile:         deactivatedProfile,
		Revision:        publishedRevision,
		ExpectedVersion: 2,
		Actor:           actor.ID(),
		Idempotency:     deactivateRequest,
		Result:          integrationMutationResult(deactivatedProfile, publishedRevision),
		Status:          200,
	}); err != nil {
		t.Fatalf("persist source profile deactivation: %v", err)
	}
	unchanged, err := sessionStore.FindSession(ctx, firstScope, created.Session.ID())
	if err != nil {
		t.Fatalf("FindSession(after profile deactivation) error = %v", err)
	}
	if unchanged.ProfileDigest() != created.Session.ProfileDigest() ||
		unchanged.ProfileRevision() != created.Session.ProfileRevision() {
		t.Fatal("session snapshot changed after source profile deactivation")
	}

	var sessions, tokens, outcomeTokens, audits, events int
	var replayBody []byte
	err = adminPool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.verification_sessions WHERE tenant_id = $1", firstTenant.ID().String()).Scan(&sessions); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.capture_tokens WHERE tenant_id = $1", firstTenant.ID().String()).Scan(&tokens); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.outcome_tokens WHERE tenant_id = $1", firstTenant.ID().String()).Scan(&outcomeTokens); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.verification_session_audit WHERE tenant_id = $1", firstTenant.ID().String()).Scan(&audits); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.outbox_events WHERE tenant_id = $1", firstTenant.ID().String()).Scan(&events); err != nil {
			return err
		}

		return tx.QueryRow(
			ctx,
			"SELECT result::text FROM idenqa.idempotency_records WHERE tenant_id = $1 AND operation = $2 AND idempotency_key = $3",
			firstTenant.ID().String(),
			verification.OperationCreateVerification,
			"session-create",
		).Scan(&replayBody)
	})
	if err != nil {
		t.Fatalf("inspect committed verification records: %v", err)
	}
	if sessions != 1 || tokens != 1 || outcomeTokens != 1 || audits != 1 || events != 1 {
		t.Fatalf("session/capture/outcome/audit/outbox counts = %d/%d/%d/%d/%d, want 1/1/1/1/1", sessions, tokens, outcomeTokens, audits, events)
	}
	if bytes.Contains(replayBody, []byte("idq_cap_v1")) || bytes.Contains(replayBody, []byte(firstToken.Reveal())) ||
		bytes.Contains(replayBody, []byte("idq_out_v1")) || bytes.Contains(replayBody, []byte(outcomeToken.Reveal())) {
		t.Fatalf("idempotency result contains bearer token: %s", replayBody)
	}

	transitionAt := mutation.CreatedAt.Add(time.Second)
	lifecycle, err := verificationpostgres.NewLifecycleStore(runtimePool, integrationProtector{}, fixedIntegrationClock{now: transitionAt})
	if err != nil {
		t.Fatal(err)
	}
	transitionID, err := generator.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.Apply(ctx, firstScope, verification.LifecycleCommand{
		EventID: transitionID, VerificationID: created.Session.ID(), ExpectedVersion: 1,
		Target: verification.SessionStateProcessing, ActorID: actor.ID().String(), OccurredAt: transitionAt,
	}); err != nil {
		t.Fatal(err)
	}
	afterTransition, err := sessionStore.Create(ctx, firstScope, mutation)
	if err != nil || afterTransition.Session.State() != created.Session.State() ||
		afterTransition.Session.Version() != created.Session.Version() ||
		!afterTransition.Session.UpdatedAt().Equal(created.Session.UpdatedAt()) {
		t.Fatalf("creation replay after transition changed its original result: %+v, %v", afterTransition, err)
	}
	current, err := sessionStore.FindSession(ctx, firstScope, created.Session.ID())
	if err != nil || current.State() != verification.SessionStateProcessing || current.Version() != 2 {
		t.Fatalf("current session must still expose its transition: %+v, %v", current, err)
	}
}

func newIntegrationSessionMutation(
	t *testing.T,
	generator *id.Generator,
	tenantID id.Tenant,
	actor id.APIKey,
	profileID id.Profile,
	policyID id.Policy,
	now time.Time,
	idempotencyKey string,
	regions ...string,
) verification.SessionCreateMutation {
	t.Helper()
	region := "local"
	if len(regions) > 0 {
		region = regions[0]
	}
	sessionID, err := generator.NewVerification()
	if err != nil {
		t.Fatalf("new verification id: %v", err)
	}
	tokenID, err := generator.NewCaptureToken()
	if err != nil {
		t.Fatalf("new capture-token id: %v", err)
	}
	outcomeTokenID, err := generator.NewOutcomeToken()
	if err != nil {
		t.Fatalf("new outcome-token id: %v", err)
	}
	eventID, err := generator.NewEvent()
	if err != nil {
		t.Fatalf("new event id: %v", err)
	}
	decisionID, err := generator.NewDecision()
	if err != nil {
		t.Fatalf("new decision id: %v", err)
	}
	request := integrationIdempotencyRequest(
		t,
		tenantID,
		actor,
		verification.OperationCreateVerification,
		idempotencyKey,
		[]byte(`{"capture_profile_id":"`+profileID.String()+`","policy_id":"`+policyID.String()+`","verification_ttl_seconds":86400,"capture_token_ttl_seconds":1800,"outcome_token_post_expiry_ttl_seconds":86400,"region":"`+region+`"}`),
		now,
	)

	return verification.SessionCreateMutation{
		SessionID:          sessionID,
		CaptureTokenID:     tokenID,
		OutcomeTokenID:     outcomeTokenID,
		EventID:            eventID,
		ProfileID:          profileID,
		PolicyID:           policyID,
		DecisionID:         decisionID,
		Region:             region,
		Actor:              actor,
		CaptureKeyVersion:  1,
		OutcomeKeyVersion:  1,
		CreatedAt:          now,
		SessionExpiresAt:   now.Add(24 * time.Hour),
		CaptureTokenExpiry: now.Add(30 * time.Minute),
		OutcomeTokenExpiry: now.Add(48 * time.Hour),
		Idempotency:        request,
	}
}

func seedIntegrationPolicy(t *testing.T, pool *idenqapostgres.Pool, tenantID id.Tenant, generator *id.Generator, now time.Time) id.Policy {
	t.Helper()
	policyID, err := generator.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.WithinTransaction(t.Context(), idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.policies (tenant_id,id,created_at,updated_at) VALUES ($1,$2,$3,$3)`, tenantID.String(), policyID.String(), now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return policyID
}

type fixedIntegrationClock struct{ now time.Time }

func (clock fixedIntegrationClock) Now() time.Time { return clock.now }

func integrationCaptureStepMessage(
	t *testing.T,
	generator *id.Generator,
	ticket realtime.Ticket,
	commandID id.Command,
	occurredAt time.Time,
	payload realtime.CaptureStepUpdate,
) realtime.Message {
	t.Helper()
	messageID, err := generator.NewMessage()
	if err != nil {
		t.Fatalf("new realtime message id: %v", err)
	}
	message, err := realtime.NewMessage(realtime.MessageInput{
		Version: realtime.ProtocolVersionV1, ID: messageID, VerificationID: ticket.VerificationID(),
		ConnectionID: ticket.ConnectionID(), Sequence: 2, CommandID: commandID,
		OccurredAt: occurredAt.UTC(), Payload: payload,
	})
	if err != nil {
		t.Fatalf("new realtime capture-step message: %v", err)
	}

	return message
}
