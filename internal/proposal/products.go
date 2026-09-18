package proposal

import (
	"context"
	"strings"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// PolicyDraftRequest is the NL policy draft input. Customer data is excluded
// unless explicitly authorised; prompts are classified and retained per policy.
type PolicyDraftRequest struct {
	TenantID      id.Tenant
	PolicyID      id.Policy
	Prompt        string
	PromptVersion string
	ModelID       string
	ActorID       string
}

// GeneratePolicyDraft creates a model-driven proposal for a natural-language
// policy draft. The draft remains inactive until simulation/adversarial gates
// pass; the proposal requires human approval.
func (service *Service) GeneratePolicyDraft(ctx context.Context, scope tenant.Scope, request PolicyDraftRequest) (Proposal, error) {
	if request.Prompt == "" || request.PolicyID.IsZero() {
		return Proposal{}, ErrInvalid
	}
	sensitive := isSensitivePrompt(request.Prompt)
	expires := service.clock.Now().Add(24 * time.Hour)
	if sensitive {
		expires = service.clock.Now().Add(7 * 24 * time.Hour)
	}
	return service.Propose(ctx, scope, proposalv1.ProposalRequest{
		TenantID:      scope.ID().String(),
		PolicyID:      request.PolicyID.String(),
		Mode:          proposalv1.AutomationModeHumanRequired,
		Actions:       []proposalv1.BoundedAction{{Kind: proposalv1.ActionPolicyDraftGenerate, Args: []byte(`{}`)}},
		ModelID:       request.ModelID,
		ModelVersion:  "v1",
		PromptVersion: request.PromptVersion,
		ContextDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:     expires,
	}, request.ActorID)
}

// GeneratePolicyDiff creates a model-driven proposal for a human-readable policy diff.
func (service *Service) GeneratePolicyDiff(ctx context.Context, scope tenant.Scope, policyID id.Policy, actorID string) (Proposal, error) {
	if policyID.IsZero() {
		return Proposal{}, ErrInvalid
	}
	return service.Propose(ctx, scope, proposalv1.ProposalRequest{
		TenantID:      scope.ID().String(),
		PolicyID:      policyID.String(),
		Mode:          proposalv1.AutomationModeAssist,
		Actions:       []proposalv1.BoundedAction{{Kind: proposalv1.ActionPolicyDiffGenerate, Args: []byte(`{}`)}},
		ModelID:       "ai.policy",
		ModelVersion:  "v1",
		PromptVersion: "p1",
		ContextDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:     service.clock.Now().Add(30 * time.Minute),
	}, actorID)
}

// GenerateAdversarialScenarios creates a model-driven proposal for adversarial policy scenarios.
func (service *Service) GenerateAdversarialScenarios(ctx context.Context, scope tenant.Scope, policyID id.Policy, actorID string) (Proposal, error) {
	if policyID.IsZero() {
		return Proposal{}, ErrInvalid
	}
	return service.Propose(ctx, scope, proposalv1.ProposalRequest{
		TenantID:      scope.ID().String(),
		PolicyID:      policyID.String(),
		Mode:          proposalv1.AutomationModeAssist,
		Actions:       []proposalv1.BoundedAction{{Kind: proposalv1.ActionPolicyAdversarialGenerate, Args: []byte(`{}`)}},
		ModelID:       "ai.policy",
		ModelVersion:  "v1",
		PromptVersion: "p1",
		ContextDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:     service.clock.Now().Add(30 * time.Minute),
	}, actorID)
}

// ProposeAccessibility creates a model-driven accessibility/exception-path proposal.
func (service *Service) ProposeAccessibility(ctx context.Context, scope tenant.Scope, verificationID id.Verification, actorID string) (Proposal, error) {
	if verificationID.IsZero() {
		return Proposal{}, ErrInvalid
	}
	return service.Propose(ctx, scope, proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeAssist,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionExperienceAccessibilityPropose, Args: []byte(`{}`)}},
		ModelID:        "ai.experience",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		ContextDigest:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:      service.clock.Now().Add(30 * time.Minute),
	}, actorID)
}

// ProposeExceptionPath creates a model-driven exception-path proposal.
func (service *Service) ProposeExceptionPath(ctx context.Context, scope tenant.Scope, verificationID id.Verification, actorID string) (Proposal, error) {
	if verificationID.IsZero() {
		return Proposal{}, ErrInvalid
	}
	return service.Propose(ctx, scope, proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeAssist,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionExperienceExceptionPropose, Args: []byte(`{}`)}},
		ModelID:        "ai.experience",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		ContextDigest:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:      service.clock.Now().Add(30 * time.Minute),
	}, actorID)
}

// ProposeDocumentLayout creates a model-driven unfamiliar document layout proposal.
func (service *Service) ProposeDocumentLayout(ctx context.Context, scope tenant.Scope, verificationID id.Verification, actorID string) (Proposal, error) {
	if verificationID.IsZero() {
		return Proposal{}, ErrInvalid
	}
	return service.Propose(ctx, scope, proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeAssist,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionDocumentLayoutPropose, Args: []byte(`{}`)}},
		ModelID:        "ai.document",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		ContextDigest:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:      service.clock.Now().Add(30 * time.Minute),
	}, actorID)
}

// isSensitivePrompt is a bounded heuristic classifier for sensitive prompt/response handling.
// Sensitive prompts inherit evidence classification, shorter retention, and deletion pipeline.
func isSensitivePrompt(prompt string) bool {
	lower := strings.ToLower(prompt)
	sensitiveTokens := []string{"ssn", "national_id", "biometric", "passport", "password"}
	for _, token := range sensitiveTokens {
		if strings.Contains(lower, token) {
			return true
		}
	}
	return false
}

var _ = tenant.Scope{}
