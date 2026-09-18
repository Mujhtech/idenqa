//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/proposal"
	proposalpostgres "github.com/Mujhtech/idenqa/internal/proposal/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
)

func TestProposalPersistenceIsolationAndReplay(t *testing.T) {
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
	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("new identifier generator: %v", err)
	}
	tenantAdmin, err := tenant.NewAdmin(tenantStore, identifiers, clock.System{})
	if err != nil {
		t.Fatalf("new tenant admin: %v", err)
	}
	action := tenant.AdminAction{Actor: "integration-operator", Reason: "verify proposal isolation"}
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
	store, err := proposalpostgres.New(runtimePool, clock.System{})
	if err != nil {
		t.Fatalf("new proposal store: %v", err)
	}
	firstScope, err := tenant.NewScope(firstTenant.ID())
	if err != nil {
		t.Fatalf("new first scope: %v", err)
	}
	secondScope, err := tenant.NewScope(secondTenant.ID())
	if err != nil {
		t.Fatalf("new second scope: %v", err)
	}
	verificationID, err := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("parse verification id: %v", err)
	}

	// We must insert a verification session and a proposal. The proposals table
	// references idenqa.verification_sessions, so seed one under tenant scope.
	if err := seedVerificationForProposal(ctx, runtimePool, firstTenant.ID(), verificationID); err != nil {
		t.Fatalf("seed verification session: %v", err)
	}

	proposalID, err := identifiers.NewProposal()
	if err != nil {
		t.Fatalf("new proposal id: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	created := proposal.Proposal{
		ID:             proposalID,
		TenantID:       firstTenant.ID(),
		VerificationID: verificationID,
		Mode:           proposalv1.AutomationModeAssist,
		Status:         proposalv1.ProposalStatusPending,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionReviewCopilotSummarize, Args: json.RawMessage(`{"summary":true}`)}},
		ModelID:        "model.test",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		ContextDigest:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:      now.Add(time.Hour),
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
		ActorID:        "key_01ARZ3NDEKTSV4RRFFQ69G5FAV",
	}
	if err := store.Create(ctx, firstScope, created); err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	// cross-tenant read must not find it
	if _, err := store.Get(ctx, secondScope, proposalID); !errors.Is(err, proposal.ErrNotFound) {
		t.Fatalf("cross-tenant Get() error = %v, want ErrNotFound", err)
	}
	// own read returns it
	got, err := store.Get(ctx, firstScope, proposalID)
	if err != nil {
		t.Fatalf("get own proposal: %v", err)
	}
	if got.Status != proposalv1.ProposalStatusPending || got.Version != 1 {
		t.Fatalf("unexpected proposal state/version = %s/%d", got.Status, got.Version)
	}

	// transition pending -> cancelled via optimistic update
	next, err := proposal.Advance(got, proposal.Command{
		ProposalID:      proposalID,
		ExpectedVersion: 1,
		Target:          proposalv1.ProposalStatusCancelled,
		ActorID:         "key_01ARZ3NDEKTSV4RRFFQ69G5FAV",
		OccurredAt:      now.Add(time.Second),
	})
	if err != nil {
		t.Fatalf("advance proposal: %v", err)
	}
	if err := store.Update(ctx, firstScope, next, 1); err != nil {
		t.Fatalf("update proposal: %v", err)
	}
	// stale update must conflict
	if err := store.Update(ctx, firstScope, next, 1); !errors.Is(err, proposal.ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}
	cancelled, err := store.Get(ctx, firstScope, proposalID)
	if err != nil {
		t.Fatalf("get cancelled proposal: %v", err)
	}
	if cancelled.Status != proposalv1.ProposalStatusCancelled || cancelled.Version != 2 {
		t.Fatalf("cancelled state/version = %s/%d", cancelled.Status, cancelled.Version)
	}

	// accepted command: create and execute once (idempotent)
	commandID, err := identifiers.NewAcceptedCommand()
	if err != nil {
		t.Fatalf("new command id: %v", err)
	}
	record := proposal.AcceptedCommandRecord{
		ID:             commandID,
		ProposalID:     proposalID,
		TenantID:       firstTenant.ID(),
		VerificationID: verificationID,
		Kind:           proposalv1.ActionReviewCopilotSummarize,
		Args:           json.RawMessage(`{"summary":true}`),
		ModelID:        "model.test",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		CreatedAt:      now,
	}
	if err := store.CreateCommand(ctx, firstScope, record); err != nil {
		t.Fatalf("create command: %v", err)
	}
	if _, err := store.GetCommand(ctx, secondScope, commandID); !errors.Is(err, proposal.ErrNotFound) {
		t.Fatalf("cross-tenant GetCommand() error = %v, want ErrNotFound", err)
	}
	fetched, err := store.GetCommand(ctx, firstScope, commandID)
	if err != nil {
		t.Fatalf("get command: %v", err)
	}
	if fetched.ExecutedAt != nil {
		t.Fatal("command unexpectedly marked executed")
	}
	if err := store.MarkExecuted(ctx, firstScope, commandID, now.Add(time.Minute)); err != nil {
		t.Fatalf("mark executed: %v", err)
	}
	executed, err := store.GetCommand(ctx, firstScope, commandID)
	if err != nil {
		t.Fatalf("get executed command: %v", err)
	}
	if executed.ExecutedAt == nil {
		t.Fatal("command was not marked executed")
	}
	// idempotent mark-executed (no-op when already executed)
	if err := store.MarkExecuted(ctx, firstScope, commandID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("re-mark executed: %v", err)
	}
}

func seedVerificationForProposal(ctx context.Context, pool *idenqapostgres.Pool, tenantID id.Tenant, verificationID id.Verification) error {
	now := time.Now().UTC().Truncate(time.Microsecond)
	digest := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	profileID, err := id.NewSystemGenerator()
	if err != nil {
		return err
	}
	profile, err := profileID.NewProfile()
	if err != nil {
		return err
	}
	return pool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.capture_profiles
(id, tenant_id, name, state, version, latest_revision, draft_revision, published_revision, created_at, updated_at)
VALUES ($1, $2, 'Proposal fixture', 'active', 1, 1, NULL, 1, $3, $3)`, profile.String(), tenantID.String(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.capture_profile_revisions
(tenant_id, profile_id, revision, state, schema_version, registry_schema_version, registry_revision, registry_digest, document, digest, created_at, updated_at, published_at)
VALUES ($1, $2, 1, 'published', 1, 1, 1, $3, '{}'::jsonb, $3, $4, $4, $4)`, tenantID.String(), profile.String(), digest, now); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.verification_sessions
(id, tenant_id, state, version, source_profile_id, source_profile_revision, source_profile_digest, requirements, created_at, updated_at, expires_at)
VALUES ($1, $2, 'collecting', 1, $3, 1, $4, '{}'::jsonb, $5, $5, $6)`,
			verificationID.String(), tenantID.String(), profile.String(), digest, now, now.Add(time.Hour))
		return err
	})
}

func TestProposalModeRegistryAndGuardrailPersistence(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("OpenMigrator() error = %v", err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	_ = migrator.Close()
	adminPool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()
	tenantStore, err := tenantpostgres.New(adminPool)
	if err != nil {
		t.Fatalf("new tenant store: %v", err)
	}
	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("new identifier generator: %v", err)
	}
	tenantAdmin, err := tenant.NewAdmin(tenantStore, identifiers, clock.System{})
	if err != nil {
		t.Fatalf("new tenant admin: %v", err)
	}
	action := tenant.AdminAction{Actor: "integration-operator", Reason: "verify proposal mode/registry"}
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
	now := time.Now().UTC().Truncate(time.Microsecond)

	// ModeStore: put/get, version CAS, cross-tenant non-disclosure.
	modeStore := proposalpostgres.NewModeStore(runtimePool)
	modeConfig := proposal.ModeConfig{
		TenantID: firstTenant.ID(), Workflow: "default", Mode: proposalv1.AutomationModeAssist,
		AllowedKinds: []proposalv1.ActionKind{proposalv1.ActionReviewCopilotSummarize},
		Version:      1, CreatedAt: now, UpdatedAt: now,
	}
	if err := modeStore.Put(ctx, modeConfig); err != nil {
		t.Fatalf("put mode: %v", err)
	}
	got, err := modeStore.Get(ctx, firstTenant.ID(), "default")
	if err != nil {
		t.Fatalf("get mode: %v", err)
	}
	if got.Mode != proposalv1.AutomationModeAssist || got.Version != 1 || len(got.AllowedKinds) != 1 {
		t.Fatalf("mode config mismatch: %+v", got)
	}
	if _, err := modeStore.Get(ctx, secondTenant.ID(), "default"); !errors.Is(err, proposal.ErrNotFound) {
		t.Fatalf("cross-tenant mode Get() error = %v, want ErrNotFound", err)
	}
	stale := modeConfig
	stale.Version = 3
	if err := modeStore.Put(ctx, stale); !errors.Is(err, proposal.ErrConflict) {
		t.Fatalf("stale mode put error = %v, want ErrConflict", err)
	}

	// RegistryStore: prompt (with sensitive classification), model, impact.
	registryStore := proposalpostgres.NewRegistryStore(runtimePool)
	promptID, err := identifiers.NewPrompt()
	if err != nil {
		t.Fatalf("new prompt id: %v", err)
	}
	content := "summarize the applicant passport"
	promptRecord := proposal.PromptRecord{
		ID: promptID, TenantID: firstTenant.ID(), Version: 1, Content: content,
		Digest: proposal.DigestPrompt(content), ModelID: "model.test", Sensitive: true,
		CreatedAt: now, ActorID: "key_01ARZ3NDEKTSV4RRFFQ69G5FAV",
	}
	if err := registryStore.CreatePrompt(ctx, promptRecord); err != nil {
		t.Fatalf("create prompt: %v", err)
	}
	fetchedPrompt, err := registryStore.GetPrompt(ctx, firstTenant.ID(), promptID, 1)
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if !fetchedPrompt.Sensitive || fetchedPrompt.Digest != proposal.DigestPrompt(content) {
		t.Fatalf("prompt sensitive/digest mismatch: %+v", fetchedPrompt)
	}
	if _, err := registryStore.GetPrompt(ctx, secondTenant.ID(), promptID, 1); !errors.Is(err, proposal.ErrNotFound) {
		t.Fatalf("cross-tenant prompt Get() error = %v, want ErrNotFound", err)
	}
	modelID, err := identifiers.NewModel()
	if err != nil {
		t.Fatalf("new model id: %v", err)
	}
	modelRecord := proposal.GenerativeModelRecord{
		ID: modelID, TenantID: firstTenant.ID(), Version: 1, ModelID: "model.test",
		Digest:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		CreatedAt: now, ActorID: "key_01ARZ3NDEKTSV4RRFFQ69G5FAV",
	}
	if err := registryStore.CreateModel(ctx, modelRecord); err != nil {
		t.Fatalf("create model: %v", err)
	}
	if _, err := registryStore.GetModel(ctx, firstTenant.ID(), modelID, 1); err != nil {
		t.Fatalf("get model: %v", err)
	}
	impact := proposal.ImpactAssessment{
		ID: "imp_integration_01", TenantID: firstTenant.ID(), Kind: proposalv1.ActionPolicyDraftGenerate,
		Assessment: `{"risk":"high"}`, RiskLevel: "high", CreatedAt: now, ActorID: "key_01ARZ3NDEKTSV4RRFFQ69G5FAV",
	}
	if err := registryStore.CreateImpact(ctx, impact); err != nil {
		t.Fatalf("create impact: %v", err)
	}

	// Guardrail checkers fail closed (false, not error) when no authority/region/evidence exists.
	authorityChecker := proposalpostgres.NewAuthorityChecker(runtimePool, clock.System{})
	regionValidator := proposalpostgres.NewRegionValidator(runtimePool, clock.System{})
	evidenceChecker := proposalpostgres.NewEvidenceChecker(runtimePool)
	missing := "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if allowed, err := authorityChecker.Allowed(ctx, firstTenant.ID().String(), missing); err != nil || allowed {
		t.Fatalf("authority checker for missing session = %v, %v", allowed, err)
	}
	if allowed, err := regionValidator.Allowed(ctx, firstTenant.ID().String(), missing); err != nil || allowed {
		t.Fatalf("region checker for missing session = %v, %v", allowed, err)
	}
	if exists, err := evidenceChecker.Exists(ctx, firstTenant.ID().String(), missing, "evd_01ARZ3NDEKTSV4RRFFQ69G5FAV"); err != nil || exists {
		t.Fatalf("evidence checker for missing session = %v, %v", exists, err)
	}
}
