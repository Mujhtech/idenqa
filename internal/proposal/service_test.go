package proposal_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/proposal"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func testScope(t *testing.T) tenant.Scope {
	t.Helper()
	tenantID, err := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}

func testVerificationID(t *testing.T) id.Verification {
	t.Helper()
	verificationID, err := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	return verificationID
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

func newTestService(t *testing.T) (*proposal.Service, *proposal.InMemoryModeStore) {
	t.Helper()
	clk := testClock{now: time.Now().UTC().Truncate(time.Microsecond)}
	gen, err := id.NewGenerator(clk, &deterministicReader{})
	if err != nil {
		t.Fatal(err)
	}
	modes := proposal.NewInMemoryModeStore()
	service, err := proposal.NewService(proposal.ServiceConfig{
		Generator: gen,
		Clock:     clk,
		Proposals: proposal.NewInMemoryProposalStore(),
		Commands:  proposal.NewInMemoryCommandStore(),
		Modes:     modes,
		Evidence:  proposal.NewInMemoryEvidenceChecker([]string{"evd_ref_1", "sig_ref_1"}),
		Authority: proposal.NewInMemoryAuthorityChecker(true),
		Region:    proposal.NewInMemoryRegionValidator(true),
		Limiter:   proposal.NewInMemoryLimiter(100),
		Audit:     proposal.NewInMemoryAuditRecorder(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, modes
}

type deterministicReader struct{ counter byte }

func (r *deterministicReader) Read(p []byte) (int, error) {
	for i := range p {
		r.counter++
		p[i] = r.counter
	}
	return len(p), nil
}

type countingProposalModel struct {
	delegate proposalv1.ProposalModel
	calls    int
}

type rejectingGenerationBinding struct{ calls int }

func (binding *rejectingGenerationBinding) ValidateGenerationBinding(
	_ context.Context,
	_ id.Tenant,
	_ proposal.ModeConfig,
	_ proposalv1.ProposalRequest,
) error {
	binding.calls++
	return proposal.ErrNotAllowed
}

func (model *countingProposalModel) Propose(ctx context.Context, request proposalv1.ProposalRequest) (proposalv1.AgentProposal, error) {
	model.calls++
	return model.delegate.Propose(ctx, request)
}

func TestProposeRateLimitsBeforeModelInvocation(t *testing.T) {
	t.Parallel()
	clk := testClock{now: time.Now().UTC().Truncate(time.Microsecond)}
	identifiers, err := id.NewGenerator(clk, &deterministicReader{})
	if err != nil {
		t.Fatal(err)
	}
	reference, err := proposal.NewReferenceModel(identifiers, clk)
	if err != nil {
		t.Fatal(err)
	}
	model := &countingProposalModel{delegate: reference}
	modes := proposal.NewInMemoryModeStore()
	service, err := proposal.NewService(proposal.ServiceConfig{
		Generator: identifiers, Clock: clk, Proposals: proposal.NewInMemoryProposalStore(),
		Commands: proposal.NewInMemoryCommandStore(), Modes: modes, Model: model,
		Evidence:  proposal.NewInMemoryEvidenceChecker([]string{"evd_ref_1"}),
		Authority: proposal.NewInMemoryAuthorityChecker(true), Region: proposal.NewInMemoryRegionValidator(true),
		Limiter: proposal.NewInMemoryLimiter(1),
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope(t)
	if err := modes.Put(t.Context(), proposal.ModeConfig{
		TenantID: scope.ID(), Workflow: "default", Mode: proposalv1.AutomationModeAssist,
		AllowedKinds: []proposalv1.ActionKind{proposalv1.ActionReviewCopilotSummarize},
		Version:      1, CreatedAt: clk.now, UpdatedAt: clk.now,
	}); err != nil {
		t.Fatal(err)
	}
	request := proposalv1.ProposalRequest{
		TenantID: scope.ID().String(), VerificationID: testVerificationID(t).String(), Mode: proposalv1.AutomationModeAssist,
		Actions:      []proposalv1.BoundedAction{{Kind: proposalv1.ActionReviewCopilotSummarize, Args: json.RawMessage(`{"summary":"bounded"}`)}},
		EvidenceRefs: []string{"evd_ref_1"}, ModelID: "model.test", ModelVersion: "v1", PromptVersion: "p1",
		ContextDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", ExpiresAt: clk.now.Add(time.Hour),
	}
	if _, err := service.Propose(t.Context(), scope, request, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV"); err != nil {
		t.Fatalf("first Propose() error = %v", err)
	}
	if _, err := service.Propose(t.Context(), scope, request, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV"); !errors.Is(err, proposal.ErrRateLimited) {
		t.Fatalf("second Propose() error = %v, want rate limited", err)
	}
	if model.calls != 1 {
		t.Fatalf("model calls = %d, want 1", model.calls)
	}
}

func TestGenerationRouteLifecycleAndModePins(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	identifiers, err := id.NewGenerator(testClock{now: now}, &deterministicReader{})
	if err != nil {
		t.Fatal(err)
	}
	registry := proposal.NewInMemoryRegistry()
	modes := proposal.NewInMemoryModeStore()
	service, err := proposal.NewService(proposal.ServiceConfig{
		Generator: identifiers,
		Clock:     testClock{now: now},
		Proposals: proposal.NewInMemoryProposalStore(),
		Commands:  proposal.NewInMemoryCommandStore(),
		Modes:     modes,
		Registry:  registry,
		Binding:   &rejectingGenerationBinding{},
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope(t)
	actorID := "key_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	model, err := service.CreateModelRecord(t.Context(), scope, "ai.review", "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", actorID)
	if err != nil {
		t.Fatalf("CreateModelRecord() error = %v", err)
	}
	prompt, err := service.CreatePromptRecord(t.Context(), scope, "Return bounded review actions.", "ai.review", actorID)
	if err != nil {
		t.Fatalf("CreatePromptRecord() error = %v", err)
	}
	active, err := service.ActivateGenerationRoute(t.Context(), scope, proposal.GenerationActivation{
		Workflow: "default", ModelRegistryID: model.ID, ModelRegistryVersion: model.Version,
		PromptRegistryID: prompt.ID, PromptRegistryVersion: prompt.Version,
		ModelVersion: "model-2026-09", PromptVersion: "review-p1", Reason: "evaluation approved",
	}, 0, actorID)
	if err != nil {
		t.Fatalf("ActivateGenerationRoute() error = %v", err)
	}
	if active.Revision != 1 || active.Action != "activated" || active.State != proposal.GenerationActivationActive {
		t.Fatalf("activation = %+v", active)
	}
	if _, err := service.PutModeConfig(t.Context(), scope, "default", proposalv1.AutomationModeAssist, nil, proposal.ModePins{}, 0, actorID); !errors.Is(err, proposal.ErrInvalid) {
		t.Fatalf("unpinned PutModeConfig() error = %v, want invalid", err)
	}
	if _, err := service.PutModeConfig(t.Context(), scope, "default", proposalv1.AutomationModeAssist, nil, proposal.ModePins{
		ModelID: &model.ID, ModelVersion: model.Version, PromptID: &prompt.ID, PromptVersion: prompt.Version, ActivationRevision: active.Revision,
	}, 0, actorID); err != nil {
		t.Fatalf("pinned PutModeConfig() error = %v", err)
	}
	if _, err := service.PutModeConfig(t.Context(), scope, "default", proposalv1.AutomationModeAssist, nil, proposal.ModePins{
		ModelID: &model.ID, ModelVersion: model.Version, PromptID: &prompt.ID, PromptVersion: prompt.Version, ActivationRevision: active.Revision,
	}, 0, actorID); !errors.Is(err, proposal.ErrConflict) {
		t.Fatalf("stale PutModeConfig() error = %v, want conflict", err)
	}
	retired, err := service.RetireGenerationRoute(t.Context(), scope, "default", 1, "provider contract ended", actorID)
	if err != nil {
		t.Fatalf("RetireGenerationRoute() error = %v", err)
	}
	if retired.Revision != 2 || retired.State != proposal.GenerationActivationRetired || retired.Action != "retired" {
		t.Fatalf("retired activation = %+v", retired)
	}
	if _, err := service.RollbackGenerationRoute(t.Context(), scope, "default", 1, 1, "stale", actorID); !errors.Is(err, proposal.ErrConflict) {
		t.Fatalf("stale RollbackGenerationRoute() error = %v, want conflict", err)
	}
	rolledBack, err := service.RollbackGenerationRoute(t.Context(), scope, "default", 2, 1, "restore approved route", actorID)
	if err != nil {
		t.Fatalf("RollbackGenerationRoute() error = %v", err)
	}
	if rolledBack.Revision != 3 || rolledBack.Action != "rolled_back" || rolledBack.SourceRevision != 1 {
		t.Fatalf("rolled back activation = %+v", rolledBack)
	}
	history, err := service.ListGenerationActivationHistory(t.Context(), scope, "default", 10)
	if err != nil {
		t.Fatalf("ListGenerationActivationHistory() error = %v", err)
	}
	if len(history) != 3 || history[0].Revision != 3 || history[1].Revision != 2 || history[2].Revision != 1 {
		t.Fatalf("history = %+v", history)
	}
}

func TestProposeRejectsUnapprovedGenerationBindingBeforeModelInvocation(t *testing.T) {
	t.Parallel()
	clk := testClock{now: time.Now().UTC().Truncate(time.Microsecond)}
	identifiers, err := id.NewGenerator(clk, &deterministicReader{})
	if err != nil {
		t.Fatal(err)
	}
	reference, err := proposal.NewReferenceModel(identifiers, clk)
	if err != nil {
		t.Fatal(err)
	}
	model := &countingProposalModel{delegate: reference}
	binding := &rejectingGenerationBinding{}
	modes := proposal.NewInMemoryModeStore()
	service, err := proposal.NewService(proposal.ServiceConfig{
		Generator: identifiers, Clock: clk, Proposals: proposal.NewInMemoryProposalStore(),
		Commands: proposal.NewInMemoryCommandStore(), Modes: modes, Model: model, Binding: binding,
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope(t)
	if err := modes.Put(t.Context(), proposal.ModeConfig{
		TenantID: scope.ID(), Workflow: "default", Mode: proposalv1.AutomationModeAssist,
		AllowedKinds: []proposalv1.ActionKind{proposalv1.ActionReviewCopilotSummarize},
		Version:      1, CreatedAt: clk.now, UpdatedAt: clk.now,
	}); err != nil {
		t.Fatal(err)
	}
	request := proposalv1.ProposalRequest{
		TenantID: scope.ID().String(), VerificationID: testVerificationID(t).String(), Mode: proposalv1.AutomationModeAssist,
		Actions: []proposalv1.BoundedAction{{Kind: proposalv1.ActionReviewCopilotSummarize, Args: json.RawMessage(`{}`)}},
		ModelID: "model.test", ModelVersion: "v1", PromptVersion: "p1",
		ContextDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", ExpiresAt: clk.now.Add(time.Hour),
	}
	if _, err := service.Propose(t.Context(), scope, request, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV"); !errors.Is(err, proposal.ErrNotAllowed) {
		t.Fatalf("Propose() error = %v, want not allowed", err)
	}
	if binding.calls != 1 || model.calls != 0 {
		t.Fatalf("binding calls = %d, model calls = %d", binding.calls, model.calls)
	}
}

func TestCreateProposal_ValidAndGuardrail(t *testing.T) {
	t.Parallel()
	service, modes := newTestService(t)
	scope := testScope(t)
	verificationID := testVerificationID(t)
	ctx := context.Background()

	fixedNow := time.Now().UTC()
	if err := modes.Put(ctx, proposal.ModeConfig{
		TenantID:     scope.ID(),
		Workflow:     "default",
		Mode:         proposalv1.AutomationModeAssist,
		AllowedKinds: []proposalv1.ActionKind{proposalv1.ActionReviewCopilotSummarize},
		Version:      1,
		CreatedAt:    fixedNow,
		UpdatedAt:    fixedNow,
	}); err != nil {
		t.Fatalf("put mode: %v", err)
	}

	request := proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeAssist,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionReviewCopilotSummarize, Args: json.RawMessage(`{"summary":true}`)}},
		EvidenceRefs:   []string{"evd_ref_1"},
		ModelID:        "model.test",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		ContextDigest:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:      time.Now().Add(time.Hour),
	}
	proposal, err := service.CreateProposal(ctx, scope, request, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("create proposal: %v", err)
	}
	if proposal.Status != proposalv1.ProposalStatusPending {
		t.Fatal("expected pending")
	}
	request.Actions[0].Kind = "unknown.kind"
	if _, err := service.CreateProposal(ctx, scope, request, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV"); err == nil {
		t.Fatal("unknown kind must be rejected")
	}
}

func TestAutomationMode_FailClosedWhenDisabled(t *testing.T) {
	t.Parallel()
	service, _ := newTestService(t)
	scope := testScope(t)
	verificationID := testVerificationID(t)
	ctx := context.Background()
	request := proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeAssist,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionReviewCopilotSummarize, Args: json.RawMessage(`{"summary":true}`)}},
		ModelID:        "model.test",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		ContextDigest:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:      time.Now().Add(time.Hour),
	}
	if _, err := service.CreateProposal(ctx, scope, request, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV"); err == nil {
		t.Fatal("disabled mode must be fail-closed")
	}
}

func TestProposalApproval_HumanRequired(t *testing.T) {
	t.Parallel()
	service, modes := newTestService(t)
	scope := testScope(t)
	verificationID := testVerificationID(t)
	ctx := context.Background()
	fixedNow2 := time.Now().UTC()

	if err := modes.Put(ctx, proposal.ModeConfig{
		TenantID:  scope.ID(),
		Workflow:  "default",
		Mode:      proposalv1.AutomationModeHumanRequired,
		Version:   1,
		CreatedAt: fixedNow2,
		UpdatedAt: fixedNow2,
	}); err != nil {
		t.Fatalf("put mode: %v", err)
	}

	request := proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeHumanRequired,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionReviewCopilotSummarize, Args: json.RawMessage(`{"summary":true}`)}},
		ModelID:        "model.test",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		ContextDigest:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:      time.Now().Add(time.Hour),
	}
	created, err := service.CreateProposal(ctx, scope, request, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, _, err := service.ApproveProposal(ctx, scope, created.ID, 1, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV", false); err == nil {
		t.Fatal("human approval required")
	}
	approved, commands, err := service.ApproveProposal(ctx, scope, created.ID, 1, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV", true)
	if err != nil {
		t.Fatalf("approve with human: %v", err)
	}
	if approved.Status != proposalv1.ProposalStatusApproved {
		t.Fatal("expected approved")
	}
	if len(commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(commands))
	}
	if err := service.ExecuteCommand(ctx, scope, commands[0].ID, nil); err != nil {
		t.Fatalf("first execute: %v", err)
	}
	if err := service.ExecuteCommand(ctx, scope, commands[0].ID, nil); err != nil {
		t.Fatalf("replay execute: %v", err)
	}
	fixedNow3 := time.Now().UTC()
	if err := modes.Put(ctx, proposal.ModeConfig{
		TenantID:  scope.ID(),
		Workflow:  "default",
		Mode:      proposalv1.AutomationModeAssist,
		Version:   2,
		CreatedAt: fixedNow3,
		UpdatedAt: fixedNow3,
	}); err != nil {
		t.Fatalf("put mode: %v", err)
	}
	draftReq := proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeAssist,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionPolicyDraftGenerate, Args: json.RawMessage(`{"prompt":"test"}`)}},
		ModelID:        "model.test",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		ContextDigest:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:      time.Now().Add(time.Hour),
	}
	created2, err := service.CreateProposal(ctx, scope, draftReq, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Logf("create draft in assist mode: %v", err)
	} else {
		if _, _, err := service.ApproveProposal(ctx, scope, created2.ID, 1, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV", true); err == nil {
			t.Fatal("high-risk in assist without proper mode should not approve")
		}
	}
}

func TestProposalLifecycle_ExpirySupersession(t *testing.T) {
	t.Parallel()
	service, modes := newTestService(t)
	scope := testScope(t)
	verificationID := testVerificationID(t)
	ctx := context.Background()
	fixedNow4 := time.Now().UTC()
	if err := modes.Put(ctx, proposal.ModeConfig{
		TenantID:  scope.ID(),
		Workflow:  "default",
		Mode:      proposalv1.AutomationModeAssist,
		Version:   1,
		CreatedAt: fixedNow4,
		UpdatedAt: fixedNow4,
	}); err != nil {
		t.Fatalf("put mode: %v", err)
	}

	request := proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeAssist,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionReviewCopilotSummarize, Args: json.RawMessage(`{"summary":true}`)}},
		ModelID:        "model.test",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		ContextDigest:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:      time.Now().Add(time.Hour),
	}
	created, err := service.CreateProposal(ctx, scope, request, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	cancelled, err := service.CancelProposal(ctx, scope, created.ID, 1, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if cancelled.Status != proposalv1.ProposalStatusCancelled {
		t.Fatal("expected cancelled")
	}
	if _, err := service.CancelProposal(ctx, scope, created.ID, 2, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV"); err == nil {
		t.Fatal("terminal state must conflict")
	}
}

func TestPromptRegistry_AndImpactAssessment(t *testing.T) {
	t.Parallel()
	registry := proposal.NewInMemoryRegistry()
	ctx := context.Background()
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	promptID, _ := id.ParsePrompt("prm_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	now := time.Now().UTC()
	record := proposal.PromptRecord{
		ID:        promptID,
		TenantID:  tenantID,
		Version:   1,
		Content:   "summarize case {{evidence}}",
		Digest:    proposal.DigestPrompt("summarize case {{evidence}}"),
		ModelID:   "model.test",
		CreatedAt: now,
		ActorID:   "key_01ARZ3NDEKTSV4RRFFQ69G5FAV",
	}
	if err := registry.CreatePrompt(ctx, record); err != nil {
		t.Fatalf("create prompt: %v", err)
	}
	fetched, err := registry.GetPrompt(ctx, tenantID, promptID, 1)
	if err != nil {
		t.Fatalf("get prompt: %v", err)
	}
	if fetched.Digest != record.Digest {
		t.Fatal("digest mismatch")
	}
	if err := registry.CreatePrompt(ctx, record); err == nil {
		t.Fatal("duplicate prompt must conflict")
	}
	impact := proposal.ImpactAssessment{
		ID:         "imp_01",
		TenantID:   tenantID,
		Kind:       proposalv1.ActionPolicyDraftGenerate,
		Assessment: `{"risk":"high"}`,
		RiskLevel:  "high",
		CreatedAt:  now,
		ActorID:    "key_01ARZ3NDEKTSV4RRFFQ69G5FAV",
	}
	if err := registry.CreateImpact(ctx, impact); err != nil {
		t.Fatalf("create impact: %v", err)
	}
}
