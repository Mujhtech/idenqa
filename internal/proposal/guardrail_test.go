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

func guardrailProposal(t *testing.T) proposal.Proposal {
	t.Helper()
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	verificationID, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	proposalID, _ := id.ParseProposal("prp_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	now := time.Now().UTC().Truncate(time.Microsecond)
	return proposal.Proposal{
		ID:             proposalID,
		TenantID:       tenantID,
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
}

func TestGuardrailRegionAndAuthorityFailClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p := guardrailProposal(t)
	mode := proposal.ModeConfig{AllowedKinds: []proposalv1.ActionKind{proposalv1.ActionReviewCopilotSummarize}}

	// Authority denied fails closed.
	result, err := proposal.ValidateGuardrails(ctx, proposal.GuardrailInput{
		Proposal:   p,
		ModeConfig: mode,
		Authority:  proposal.NewInMemoryAuthorityChecker(false),
		Region:     proposal.NewInMemoryRegionValidator(true),
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if result.Allowed {
		t.Fatal("denied authority must fail closed")
	}

	// Region denied fails closed.
	result, err = proposal.ValidateGuardrails(ctx, proposal.GuardrailInput{
		Proposal:   p,
		ModeConfig: mode,
		Authority:  proposal.NewInMemoryAuthorityChecker(true),
		Region:     proposal.NewInMemoryRegionValidator(false),
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if result.Allowed {
		t.Fatal("denied region must fail closed")
	}

	// Both allowed passes.
	result, err = proposal.ValidateGuardrails(ctx, proposal.GuardrailInput{
		Proposal:   p,
		ModeConfig: mode,
		Authority:  proposal.NewInMemoryAuthorityChecker(true),
		Region:     proposal.NewInMemoryRegionValidator(true),
	})
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !result.Allowed {
		t.Fatalf("allowed authority+region rejected: %s", result.Reason)
	}
}

func TestGuardrailValidatesEvidenceAndSignalReferences(t *testing.T) {
	t.Parallel()
	p := guardrailProposal(t)
	p.EvidenceRefs = []string{"evd_ref_1"}
	p.SignalRefs = []string{"sig_ref_missing"}
	result, err := proposal.ValidateGuardrails(t.Context(), proposal.GuardrailInput{
		Proposal: p, ModeConfig: proposal.ModeConfig{AllowedKinds: []proposalv1.ActionKind{proposalv1.ActionReviewCopilotSummarize}},
		Evidence: proposal.NewInMemoryEvidenceChecker([]string{"evd_ref_1"}),
	})
	if err != nil {
		t.Fatalf("ValidateGuardrails() error = %v", err)
	}
	if result.Allowed || result.Reason != "unknown signal_ref" {
		t.Fatalf("ValidateGuardrails() = %+v, want unknown signal_ref", result)
	}
}
