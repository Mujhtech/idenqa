package proposal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// ReferenceModel is a deterministic, dependency-free ProposalModel. It produces
// a bounded proposal from a redacted context without contacting an external
// generative model, so the proposal contract, guardrails, and accepted-command
// replay can be exercised end to end. A real generative-model adapter remains a
// later external gate and must be selected separately.
type ReferenceModel struct {
	ids   *id.Generator
	clock clock.Clock
}

// NewReferenceModel constructs a deterministic reference proposal model.
func NewReferenceModel(ids *id.Generator, source clock.Clock) (*ReferenceModel, error) {
	if ids == nil || source == nil {
		return nil, errors.New("proposal reference model: generator and clock are required")
	}
	return &ReferenceModel{ids: ids, clock: source}, nil
}

// Propose deterministically derives bounded arguments for each requested action
// kind and returns a pending, immutable AgentProposal. Raw evidence never
// enters the envelope.
func (model *ReferenceModel) Propose(_ context.Context, request proposalv1.ProposalRequest) (proposalv1.AgentProposal, error) {
	if err := proposalv1.ValidateProposalRequest(request); err != nil {
		return proposalv1.AgentProposal{}, fmt.Errorf("%w: validation: %w", ErrInvalid, err)
	}
	proposalID, err := model.ids.NewProposal()
	if err != nil {
		return proposalv1.AgentProposal{}, err
	}
	now := model.clock.Now().UTC()
	actions := make([]proposalv1.BoundedAction, 0, len(request.Actions))
	for _, action := range request.Actions {
		args, err := deriveReferenceArgs(action.Kind)
		if err != nil {
			return proposalv1.AgentProposal{}, err
		}
		actions = append(actions, proposalv1.BoundedAction{Kind: action.Kind, Args: args})
	}
	return proposalv1.AgentProposal{
		ProposalID:     proposalID.String(),
		TenantID:       request.TenantID,
		VerificationID: request.VerificationID,
		PolicyID:       request.PolicyID,
		Mode:           request.Mode.String(),
		Status:         proposalv1.ProposalStatusPending.String(),
		Actions:        actions,
		EvidenceRefs:   request.EvidenceRefs,
		SignalRefs:     request.SignalRefs,
		ModelID:        request.ModelID,
		ModelVersion:   request.ModelVersion,
		PromptVersion:  request.PromptVersion,
		ContextDigest:  request.ContextDigest,
		ExpiresAt:      request.ExpiresAt,
		CreatedAt:      now,
	}, nil
}

// deriveReferenceArgs produces a deterministic, bounded argument document for
// each allowed action kind. It exists only to prove the model contract; a real
// model produces content that passes the same closed-schema validation.
func deriveReferenceArgs(kind proposalv1.ActionKind) (json.RawMessage, error) {
	switch kind {
	case proposalv1.ActionReviewCopilotSummarize:
		return json.Marshal(map[string]string{"summary": "reference review summary"})
	case proposalv1.ActionRoutingAdaptiveSuggest:
		return json.Marshal(map[string]string{"route": "suggested_equivalent_provider"})
	case proposalv1.ActionPolicyDraftGenerate:
		return json.Marshal(map[string]string{"draft": "reference inactive policy draft"})
	case proposalv1.ActionPolicyDiffGenerate:
		return json.Marshal(map[string]string{"diff": "reference human-readable policy diff"})
	case proposalv1.ActionPolicyAdversarialGenerate:
		return json.Marshal(map[string]string{"scenarios": "reference adversarial scenarios"})
	case proposalv1.ActionExperienceAccessibilityPropose:
		return json.Marshal(map[string]string{"accessibility": "reference accessibility proposal"})
	case proposalv1.ActionExperienceExceptionPropose:
		return json.Marshal(map[string]string{"exception": "reference exception proposal"})
	case proposalv1.ActionDocumentLayoutPropose:
		return json.Marshal(map[string]string{"layout": "reference document layout proposal"})
	default:
		return nil, ErrUnknownKind
	}
}

var _ proposalv1.ProposalModel = (*ReferenceModel)(nil)

// ReferenceExecutor is a deterministic, no-op CommandExecutor. It records the
// execution boundary for accepted commands without applying per-action-kind
// side effects; real per-kind executors (e.g. attaching a review summary,
// applying a suggested route) are external integrations behind the same port.
type ReferenceExecutor struct{}

// NewReferenceExecutor returns a no-op reference executor.
func NewReferenceExecutor() *ReferenceExecutor {
	return &ReferenceExecutor{}
}

// Execute accepts the validated command and applies no side effect.
func (*ReferenceExecutor) Execute(context.Context, proposalv1.AcceptedCommand) error {
	return nil
}

var _ proposalv1.CommandExecutor = (*ReferenceExecutor)(nil)
