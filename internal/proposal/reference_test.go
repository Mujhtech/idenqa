package proposal_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/proposal"
)

func TestReferenceModelProposeThroughService(t *testing.T) {
	t.Parallel()
	clk := testClock{now: time.Now().UTC().Truncate(time.Microsecond)}
	gen, err := id.NewGenerator(clk, &deterministicReader{})
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope(t)
	verificationID := testVerificationID(t)
	modes := proposal.NewInMemoryModeStore()
	model, err := proposal.NewReferenceModel(gen, clk)
	if err != nil {
		t.Fatal(err)
	}
	service, err := proposal.NewService(proposal.ServiceConfig{
		Generator: gen,
		Clock:     clk,
		Proposals: proposal.NewInMemoryProposalStore(),
		Commands:  proposal.NewInMemoryCommandStore(),
		Modes:     modes,
		Model:     model,
		Evidence:  proposal.NewInMemoryEvidenceChecker(nil),
		Authority: proposal.NewInMemoryAuthorityChecker(true),
		Region:    proposal.NewInMemoryRegionValidator(true),
		Limiter:   proposal.NewInMemoryLimiter(100),
		Audit:     proposal.NewInMemoryAuditRecorder(),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := modes.Put(ctx, proposal.ModeConfig{
		TenantID: scope.ID(), Workflow: "default", Mode: proposalv1.AutomationModeAssist, Version: 1,
		CreatedAt: clk.Now(), UpdatedAt: clk.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// Propose with no model must fail closed.
	noModel, _ := proposal.NewService(proposal.ServiceConfig{
		Generator: gen, Clock: clk,
		Proposals: proposal.NewInMemoryProposalStore(),
		Commands:  proposal.NewInMemoryCommandStore(),
		Modes:     modes,
	})
	if _, err := noModel.Propose(ctx, scope, proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeAssist,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionReviewCopilotSummarize, Args: json.RawMessage(`{}`)}},
		ModelID:        "model.test", ModelVersion: "v1", PromptVersion: "p1",
		ContextDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:     clk.Now().Add(time.Hour),
	}, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV"); err == nil {
		t.Fatal("Propose without a model must fail closed")
	}

	created, err := service.Propose(ctx, scope, proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeAssist,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionReviewCopilotSummarize, Args: json.RawMessage(`{}`)}},
		ModelID:        "model.test", ModelVersion: "v1", PromptVersion: "p1",
		ContextDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:     clk.Now().Add(time.Hour),
	}, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("Propose: %v", err)
	}
	if created.Status != proposalv1.ProposalStatusPending {
		t.Fatalf("proposed status = %s", created.Status)
	}
	// model-derived args must be present (not the empty placeholder)
	if len(created.Actions) != 1 || len(created.Actions[0].Args) == 0 {
		t.Fatal("model did not produce bounded args")
	}
	// approve with human approval produces an accepted command, then replay executes idempotently
	_, commands, err := service.ApproveProposal(ctx, scope, created.ID, 1, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV", true)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if len(commands) != 1 {
		t.Fatalf("expected 1 accepted command, got %d", len(commands))
	}
	if err := service.ExecuteCommand(ctx, scope, commands[0].ID, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if err := service.ExecuteCommand(ctx, scope, commands[0].ID, nil); err != nil {
		t.Fatalf("replay execute: %v", err)
	}
}

func TestPolicyScopedProposalSkipsSessionGuardrails(t *testing.T) {
	t.Parallel()
	clk := testClock{now: time.Now().UTC().Truncate(time.Microsecond)}
	gen, err := id.NewGenerator(clk, &deterministicReader{})
	if err != nil {
		t.Fatal(err)
	}
	scope := testScope(t)
	modes := proposal.NewInMemoryModeStore()
	model, err := proposal.NewReferenceModel(gen, clk)
	if err != nil {
		t.Fatal(err)
	}
	service, err := proposal.NewService(proposal.ServiceConfig{
		Generator: gen,
		Clock:     clk,
		Proposals: proposal.NewInMemoryProposalStore(),
		Commands:  proposal.NewInMemoryCommandStore(),
		Modes:     modes,
		Model:     model,
		Evidence:  proposal.NewInMemoryEvidenceChecker(nil),
		Authority: proposal.NewInMemoryAuthorityChecker(false), // session authority denied
		Region:    proposal.NewInMemoryRegionValidator(false),  // session region denied
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := modes.Put(ctx, proposal.ModeConfig{
		TenantID: scope.ID(), Workflow: "default", Mode: proposalv1.AutomationModeHumanRequired, Version: 1,
		CreatedAt: clk.Now(), UpdatedAt: clk.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	created, err := service.GeneratePolicyDraft(ctx, scope, proposal.PolicyDraftRequest{
		PolicyID:      policyID,
		Prompt:        "draft a verification policy for NIN",
		PromptVersion: "p1",
		ModelID:       "model.test",
		ActorID:       "key_01ARZ3NDEKTSV4RRFFQ69G5FAV",
	})
	if err != nil {
		t.Fatalf("policy-scoped propose: %v", err)
	}
	if created.PolicyID == nil || created.PolicyID.String() != policyID.String() {
		t.Fatalf("policy anchor not preserved: %+v", created.PolicyID)
	}
	if !created.VerificationID.IsZero() {
		t.Fatal("policy-scoped proposal must have no verification anchor")
	}
	// human_required mode: approval requires human approval.
	if _, _, err := service.ApproveProposal(ctx, scope, created.ID, 1, "key_01ARZ3NDEKTSV4RRFFQ69G5FAV", true); err != nil {
		t.Fatalf("approve policy-scoped proposal: %v", err)
	}
}
