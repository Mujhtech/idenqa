//go:build integration

package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
	"github.com/Mujhtech/idenqa/internal/verification/synthetic"
	verificationtask "github.com/Mujhtech/idenqa/internal/verification/task"
)

func TestVerificationExecutionDurabilityIsolationInboxAndReconciliation(t *testing.T) {
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
	headgateMigrator, err := taskheadgate.OpenMigrator(ctx, database.url, "headgate", 5*time.Second, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := headgateMigrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := headgateMigrator.Close(ctx); err != nil {
		t.Fatal(err)
	}

	adminPool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	firstTenant, firstVerification := seedExecutionVerification(t, adminPool, generator, now)
	secondTenant, _ := seedExecutionVerification(t, adminPool, generator, now)

	runtimeConfig := poolConfig(database.url)
	runtimeRole := database.createRuntimeRole(t)
	database.grantHeadgateRuntime(t, runtimeRole)
	runtimeConfig.Role = runtimeRole
	runtimePool, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimePool.Close()
	store, err := verificationpostgres.NewCheckStore(runtimePool, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	progressListener, err := runtimePool.OpenNotificationListener(
		ctx, verificationpostgres.CheckProgressNotificationChannel,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer progressListener.Close()
	firstScope, _ := tenant.NewScope(firstTenant)
	secondScope, _ := tenant.NewScope(secondTenant)

	checkID, _ := generator.NewCheck()
	check, err := verification.NewCheck(checkID, firstTenant, firstVerification, "document.authenticity", now)
	if err != nil {
		t.Fatal(err)
	}
	createEvent, _ := generator.NewEvent()
	if err := store.CreateCheck(ctx, firstScope, check, createEvent); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FindCheck(ctx, secondScope, checkID); !errors.Is(err, verification.ErrCheckNotFound) {
		t.Fatalf("cross-tenant check lookup = %v", err)
	}

	attempt := newExecutionAttempt(t, generator, 1, 7, now.Add(time.Second))
	if err := check.BeginAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	startEvent, _ := generator.NewEvent()
	if duplicate, err := store.SaveCheck(ctx, firstScope, verification.CheckCommit{Check: check, ExpectedVersion: 1, EventID: startEvent}); err != nil || duplicate {
		t.Fatalf("start attempt = %t, %v", duplicate, err)
	}

	completedAt := now.Add(2 * time.Second)
	result, err := (synthetic.Provider{Scenario: synthetic.Success, Now: func() time.Time { return completedAt }}).
		Execute(ctx, providerv1.Request{Contract: providerv1.CurrentVersion, AttemptID: attempt.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	fingerprint, err := verification.ProviderResultFingerprint(result)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := verification.NewResultReceipt(attempt.ID, fingerprint, completedAt)
	if err != nil {
		t.Fatal(err)
	}
	progress := &integrationProgressRecorder{}
	reconciliation := &integrationReconciliationRecorder{}
	service, err := verification.NewExecutionService(store, generator, progress, reconciliation)
	if err != nil {
		t.Fatal(err)
	}
	completed, disposition, err := service.ApplyResult(ctx, firstScope, checkID, receipt, func(current *verification.Check) (string, error) {
		return verification.ApplyProviderResult(current, result, generator, attempt.Fence)
	})
	if err != nil || disposition != "applied" || completed.State != verification.CheckCompleted || completed.Outcome != verification.CheckPassed {
		t.Fatalf("complete = state %s outcome %s disposition %s error %v", completed.State, completed.Outcome, disposition, err)
	}
	versionAfterCompletion := completed.Version
	replayedReceipt, _ := verification.NewResultReceipt(attempt.ID, fingerprint, completedAt.Add(time.Second))
	replayed, disposition, err := service.ApplyResult(ctx, firstScope, checkID, replayedReceipt, func(current *verification.Check) (string, error) {
		return verification.ApplyProviderResult(current, result, generator, attempt.Fence)
	})
	if err != nil || disposition != "duplicate" || replayed.Version != versionAfterCompletion {
		t.Fatalf("duplicate = version %d disposition %s error %v", replayed.Version, disposition, err)
	}
	reloaded, err := store.FindCheck(ctx, firstScope, checkID)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Attempts()) != 1 || len(reloaded.Attempts()[0].Observations) != 1 || len(reloaded.Diagnostics()) != 1 ||
		reloaded.Diagnostics()[0].Code != "result_inbox_replay" {
		t.Fatalf("restored history = attempts %d observations %d diagnostics %v", len(reloaded.Attempts()), len(reloaded.Attempts()[0].Observations), reloaded.Diagnostics())
	}

	retryCheckID, _ := generator.NewCheck()
	retryCheck, _ := verification.NewCheck(retryCheckID, firstTenant, firstVerification, "document.semantic-retry", now)
	retryCreateEvent, _ := generator.NewEvent()
	if err := store.CreateCheck(ctx, firstScope, retryCheck, retryCreateEvent); err != nil {
		t.Fatal(err)
	}
	retryAttempt := newExecutionAttempt(t, generator, 1, 21, now.Add(time.Second))
	if err := retryCheck.BeginAttempt(retryAttempt); err != nil {
		t.Fatal(err)
	}
	retryStartEvent, _ := generator.NewEvent()
	if _, err := store.SaveCheck(ctx, firstScope, verification.CheckCommit{Check: retryCheck, ExpectedVersion: 1, EventID: retryStartEvent}); err != nil {
		t.Fatal(err)
	}
	queue, err := taskheadgate.NewPostgres(runtimePool.Native(), taskheadgate.DefaultConfig("idenqa-semantic-retry"))
	if err != nil {
		t.Fatal(err)
	}
	handler, err := verificationtask.NewExecuteHandler(store, generator,
		synthetic.Provider{Scenario: synthetic.Unavailable, Now: func() time.Time { return now.Add(2 * time.Second) }},
		synthetic.Model{Scenario: synthetic.Success, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if err := handler.WithSemanticRetries(generator, queue); err != nil {
		t.Fatal(err)
	}
	executeIntent, err := verificationtask.NewExecuteIntent(generator, firstScope,
		verificationtask.ExecutePayload{CheckID: retryCheckID, AttemptID: retryAttempt.ID},
		verificationtask.IntentMetadata{ScheduledAt: retryAttempt.StartedAt, Deadline: retryAttempt.Deadline})
	if err != nil {
		t.Fatal(err)
	}
	work, prepared := handler.Prepare(ctx, platformtask.Delivery{Intent: executeIntent, Attempt: 1, Fence: 1})
	if prepared.Outcome != platformtask.OutcomeComplete || work == nil {
		t.Fatalf("semantic retry prepare = %+v", prepared)
	}
	if err := runtimePool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		result := work(ctx, tx)
		if result.Outcome != platformtask.OutcomeComplete {
			return result.Err
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	retried, err := store.FindCheck(ctx, firstScope, retryCheckID)
	if err != nil {
		t.Fatal(err)
	}
	retryAttempts := retried.Attempts()
	if len(retryAttempts) != 2 || retryAttempts[0].State != verification.AttemptFailed ||
		retryAttempts[1].State != verification.AttemptRunning || retryAttempts[1].Number != 2 || retryAttempts[1].Fence != 22 {
		t.Fatalf("semantic retry attempts = %+v", retryAttempts)
	}

	rollbackCheckID, _ := generator.NewCheck()
	rollbackCheck, _ := verification.NewCheck(rollbackCheckID, firstTenant, firstVerification, "document.rollback", now)
	rollbackCreateEvent, _ := generator.NewEvent()
	if err := store.CreateCheck(ctx, firstScope, rollbackCheck, rollbackCreateEvent); err != nil {
		t.Fatal(err)
	}
	rollbackAttempt := newExecutionAttempt(t, generator, 1, 8, now.Add(time.Second))
	if err := rollbackCheck.BeginAttempt(rollbackAttempt); err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveCheck(ctx, firstScope, verification.CheckCommit{Check: rollbackCheck, ExpectedVersion: 1, EventID: rollbackCreateEvent}); err == nil {
		t.Fatal("duplicate outbox identity did not roll back")
	}
	rolledBack, err := store.FindCheck(ctx, firstScope, rollbackCheckID)
	if err != nil || rolledBack.State != verification.CheckQueued || len(rolledBack.Attempts()) != 0 {
		t.Fatalf("rollback state = %s attempts %d error %v", rolledBack.State, len(rolledBack.Attempts()), err)
	}

	staleCheckID, _ := generator.NewCheck()
	staleCheck, _ := verification.NewCheck(staleCheckID, firstTenant, firstVerification, "document.stale", now)
	staleCreateEvent, _ := generator.NewEvent()
	if err := store.CreateCheck(ctx, firstScope, staleCheck, staleCreateEvent); err != nil {
		t.Fatal(err)
	}
	firstAttempt := newExecutionAttempt(t, generator, 1, 10, now.Add(time.Second))
	if err := staleCheck.BeginAttempt(firstAttempt); err != nil {
		t.Fatal(err)
	}
	firstStartEvent, _ := generator.NewEvent()
	if _, err := store.SaveCheck(ctx, firstScope, verification.CheckCommit{Check: staleCheck, ExpectedVersion: 1, EventID: firstStartEvent}); err != nil {
		t.Fatal(err)
	}
	if _, err := staleCheck.FailAttempt(firstAttempt.ID, firstAttempt.Fence,
		verification.Failure{Class: "unavailable", Code: "synthetic_unavailable", Retry: verification.RetryBackoff}, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	firstFailEvent, _ := generator.NewEvent()
	if _, err := store.SaveCheck(ctx, firstScope, verification.CheckCommit{Check: staleCheck, ExpectedVersion: 2, EventID: firstFailEvent}); err != nil {
		t.Fatal(err)
	}
	secondAttempt := newExecutionAttempt(t, generator, 2, 11, now.Add(3*time.Second))
	if err := staleCheck.BeginAttempt(secondAttempt); err != nil {
		t.Fatal(err)
	}
	secondStartEvent, _ := generator.NewEvent()
	if _, err := store.SaveCheck(ctx, firstScope, verification.CheckCommit{Check: staleCheck, ExpectedVersion: 3, EventID: secondStartEvent}); err != nil {
		t.Fatal(err)
	}
	lateAt := now.Add(4 * time.Second)
	lateResult := providerv1.Result{Contract: providerv1.CurrentVersion, AttemptID: firstAttempt.ID.String(), Outcome: providerv1.ResultOutcomeCompleted,
		Signals: []providerv1.Signal{{Name: "synthetic.document", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: lateAt}
	lateFingerprint, _ := verification.ProviderResultFingerprint(lateResult)
	lateReceipt, _ := verification.NewResultReceipt(firstAttempt.ID, lateFingerprint, lateAt)
	_, disposition, err = service.ApplyResult(ctx, firstScope, staleCheckID, lateReceipt, func(current *verification.Check) (string, error) {
		return verification.ApplyProviderResult(current, lateResult, generator, firstAttempt.Fence)
	})
	if !errors.Is(err, verification.ErrStaleAttempt) || disposition != "stale" || reconciliation.calls != 1 {
		t.Fatalf("late result = %s, %v, hooks %d", disposition, err, reconciliation.calls)
	}
	due, err := store.ListDueReconciliations(ctx, lateAt.Add(time.Second), 100)
	if err != nil || len(due) != 1 || due[0].TenantID.String() != firstTenant.String() ||
		due[0].CheckID.String() != staleCheckID.String() || due[0].AttemptID.String() != firstAttempt.ID.String() {
		t.Fatalf("due reconciliations = %+v, %v", due, err)
	}
	claimToken, _ := generator.NewTask()
	claim, err := store.ClaimReconciliationForAttempt(
		ctx, firstScope, staleCheckID, firstAttempt.ID, claimToken, lateAt.Add(time.Second), time.Minute,
	)
	if err != nil || claim.AttemptID.String() != firstAttempt.ID.String() {
		t.Fatalf("claim = %+v, %v", claim, err)
	}
	wrongToken, _ := generator.NewTask()
	wrongClaim := claim
	wrongClaim.ClaimToken = wrongToken
	if err := store.ResolveReconciliation(ctx, firstScope, wrongClaim, lateAt.Add(2*time.Second)); !errors.Is(err, verification.ErrStaleAttempt) {
		t.Fatalf("wrong claim resolution = %v", err)
	}
	if err := store.ResolveReconciliation(ctx, firstScope, claim, lateAt.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}

	notificationContext, cancelNotification := context.WithTimeout(ctx, time.Second)
	defer cancelNotification()
	notifiedTenant, err := progressListener.Wait(notificationContext)
	if err != nil || notifiedTenant != firstTenant.String() {
		t.Fatalf("check progress notification = %q, %v", notifiedTenant, err)
	}
	pendingTenants, err := store.ListPendingProgressTenants(ctx, 100)
	if err != nil || len(pendingTenants) != 1 || pendingTenants[0].String() != firstTenant.String() {
		t.Fatalf("pending progress tenants = %v, %v", pendingTenants, err)
	}
	projected, err := store.ProjectCheckProgress(ctx, firstScope, lateAt.Add(3*time.Second), 100)
	if err != nil || projected < 1 {
		t.Fatalf("project check progress = %d, %v", projected, err)
	}
	if repeated, err := store.ProjectCheckProgress(ctx, firstScope, lateAt.Add(4*time.Second), 100); err != nil || repeated != 0 {
		t.Fatalf("repeat check progress projection = %d, %v", repeated, err)
	}
	var realtimeCount, unsafePayloads, pendingOutbox int
	if err := adminPool.Native().QueryRow(ctx, `SELECT count(*),
        count(*) FILTER (WHERE payload ?| ARRAY['outcome','signals','reason_codes','provider_data','evidence'])
        FROM idenqa.realtime_events
        WHERE tenant_id = $1 AND message_type = 'verification.check.progress'`, firstTenant.String()).
		Scan(&realtimeCount, &unsafePayloads); err != nil {
		t.Fatal(err)
	}
	if err := adminPool.Native().QueryRow(ctx, `SELECT count(*) FROM idenqa.outbox_events
        WHERE tenant_id = $1 AND event_type = 'verification.check.progress.v1' AND published_at IS NULL`, firstTenant.String()).
		Scan(&pendingOutbox); err != nil {
		t.Fatal(err)
	}
	if realtimeCount != projected || unsafePayloads != 0 || pendingOutbox != 0 {
		t.Fatalf("projected realtime=%d unsafe=%d pending=%d want=%d/0/0", realtimeCount, unsafePayloads, pendingOutbox, projected)
	}

	tamperTx, err := runtimePool.Native().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tamperTx.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := tamperTx.Exec(ctx, "SELECT set_config('idenqa.tenant_id', $1, true)", firstTenant.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := tamperTx.Exec(ctx, "UPDATE idenqa.verification_observations SET signal_name = 'tampered' WHERE tenant_id = $1", firstTenant.String()); err == nil {
		t.Fatal("append-only observation accepted mutation")
	}
	if _, err := adminPool.Native().Exec(ctx, "DELETE FROM idenqa.verification_attempts WHERE tenant_id = $1 AND id = $2", firstTenant.String(), secondAttempt.ID.String()); err == nil {
		t.Fatal("immutable attempt accepted deletion")
	}
}

type integrationProgressRecorder struct{ calls int }

func (recorder *integrationProgressRecorder) PublishCheckProgress(context.Context, tenant.Scope, verification.CheckProgress) error {
	recorder.calls++
	return nil
}

type integrationReconciliationRecorder struct{ calls int }

func (recorder *integrationReconciliationRecorder) RequestReconciliation(context.Context, tenant.Scope, id.Check, id.Attempt, string) error {
	recorder.calls++
	return nil
}

func seedExecutionVerification(t *testing.T, pool *idenqapostgres.Pool, generator *id.Generator, now time.Time) (id.Tenant, id.Verification) {
	t.Helper()
	tenantID, _ := generator.NewTenant()
	profileID, _ := generator.NewProfile()
	verificationID, _ := generator.NewVerification()
	tx, err := pool.Native().Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := tx.Exec(t.Context(), "INSERT INTO idenqa.tenants (id, state, version, created_at, updated_at) VALUES ($1, 'active', 1, $2, $2)", tenantID.String(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `INSERT INTO idenqa.capture_profiles
        (id, tenant_id, name, state, version, latest_revision, draft_revision, published_revision, created_at, updated_at)
        VALUES ($1, $2, 'Execution fixture', 'active', 1, 1, NULL, 1, $3, $3)`, profileID.String(), tenantID.String(), now); err != nil {
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
	return tenantID, verificationID
}

func newExecutionAttempt(t *testing.T, generator *id.Generator, number uint32, fence uint64, now time.Time) verification.Attempt {
	t.Helper()
	attemptID, err := generator.NewAttempt()
	if err != nil {
		t.Fatal(err)
	}
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	return verification.Attempt{ID: attemptID, Number: number, Fence: fence, RunnerKind: verification.RunnerProvider, State: verification.AttemptRunning,
		Provenance: verification.Provenance{RunnerID: "synthetic.runner", RunnerVersion: "1.0.0", PackageDigest: digest,
			ContractMajor: 1, RequestDigest: digest, Configuration: digest}, StartedAt: now, Deadline: now.Add(time.Minute)}
}
