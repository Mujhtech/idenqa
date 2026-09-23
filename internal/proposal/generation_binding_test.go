package proposal_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/proposal"
)

func TestGenerationBindingValidatorRequiresExactTenantRegistryRecords(t *testing.T) {
	t.Parallel()
	registry := proposal.NewInMemoryRegistry()
	tenantID := testScope(t).ID()
	promptID, err := id.ParsePrompt("prm_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	modelRegistryID, err := id.ParseModel("mdl_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	instructions := "Return bounded review actions."
	promptDigest := proposal.DigestPrompt(instructions)
	modelDigest := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	now := time.Now().UTC()
	if err := registry.CreatePrompt(t.Context(), proposal.PromptRecord{
		ID: promptID, TenantID: tenantID, Version: 2, Content: instructions,
		Digest: promptDigest, ModelID: "ai.review", CreatedAt: now, ActorID: "operator",
	}); err != nil {
		t.Fatal(err)
	}
	if err := registry.CreateModel(t.Context(), proposal.GenerativeModelRecord{
		ID: modelRegistryID, TenantID: tenantID, Version: 3, ModelID: "ai.review",
		Digest: modelDigest, CreatedAt: now, ActorID: "operator",
	}); err != nil {
		t.Fatal(err)
	}
	validator, err := proposal.NewGenerationBindingValidator(registry, []proposal.GenerationBindingRoute{{
		ModelID: "ai.review", ModelVersion: "model-snapshot", PromptVersion: "review-p2",
		ModelRegistryID: modelRegistryID, ModelRegistryVersion: 3, ModelDigest: modelDigest,
		PromptRegistryID: promptID, PromptRegistryVersion: 2, PromptDigest: promptDigest, Instructions: instructions,
	}})
	if err != nil {
		t.Fatal(err)
	}
	activation := proposal.GenerationActivation{
		TenantID: tenantID, Workflow: "default", Revision: 1, State: proposal.GenerationActivationActive,
		ModelRegistryID: modelRegistryID, ModelRegistryVersion: 3, PromptRegistryID: promptID, PromptRegistryVersion: 2,
		ModelID: "ai.review", ModelVersion: "model-snapshot", PromptVersion: "review-p2",
		Action: "activated", ActorID: "operator", OccurredAt: now,
	}
	if err := registry.PutActivation(t.Context(), activation, 0); err != nil {
		t.Fatal(err)
	}
	mode := proposal.ModeConfig{Workflow: "default", ModelID: &modelRegistryID, ModelVersion: 3, PromptID: &promptID, PromptVersion: 2, ActivationRevision: 1}
	request := generationProposalRequest(now)
	request.ModelID = "ai.review"
	request.ModelVersion = "model-snapshot"
	request.PromptVersion = "review-p2"
	if err := validator.ValidateGenerationBinding(t.Context(), tenantID, mode, request); err != nil {
		t.Fatalf("ValidateGenerationBinding() error = %v", err)
	}
	request.PromptVersion = "other"
	if err := validator.ValidateGenerationBinding(t.Context(), tenantID, mode, request); !errors.Is(err, proposal.ErrModelUnavailable) {
		t.Fatalf("changed prompt error = %v, want model unavailable", err)
	}
	otherTenant, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	request.PromptVersion = "review-p2"
	if err := validator.ValidateGenerationBinding(t.Context(), otherTenant, mode, request); !errors.Is(err, proposal.ErrNotAllowed) {
		t.Fatalf("unapproved tenant error = %v, want not allowed", err)
	}
	wrongPrompt, _ := id.ParsePrompt("prm_01ARZ3NDEKTSV4RRFFQ69G5FAW")
	wrongMode := mode
	wrongMode.PromptID = &wrongPrompt
	if err := validator.ValidateGenerationBinding(t.Context(), tenantID, wrongMode, request); !errors.Is(err, proposal.ErrNotAllowed) {
		t.Fatalf("mode prompt mismatch error = %v, want not allowed", err)
	}
	retired := activation
	retired.Revision = 2
	retired.State = proposal.GenerationActivationRetired
	retired.Action = "retired"
	retired.Reason = "provider contract ended"
	if err := registry.PutActivation(t.Context(), retired, 1); err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateGenerationBinding(t.Context(), tenantID, mode, request); !errors.Is(err, proposal.ErrNotAllowed) {
		t.Fatalf("stale activation pin error = %v, want not allowed", err)
	}
	mode.ActivationRevision = 2
	if err := validator.ValidateGenerationBinding(t.Context(), tenantID, mode, request); !errors.Is(err, proposal.ErrNotAllowed) {
		t.Fatalf("retired activation error = %v, want not allowed", err)
	}
}
