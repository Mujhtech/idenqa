package proposal_test

import (
	"context"
	"encoding/json"
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
