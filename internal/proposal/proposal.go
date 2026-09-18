package proposal

import (
	"encoding/json"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// Proposal is the immutable domain aggregate for one bounded AI proposal.
// Exactly one of VerificationID or PolicyID anchors the proposal.
type Proposal struct {
	ID             id.Proposal
	TenantID       id.Tenant
	VerificationID id.Verification
	PolicyID       *id.Policy
	Mode           proposalv1.AutomationMode
	Status         proposalv1.ProposalStatus
	Actions        []proposalv1.BoundedAction
	EvidenceRefs   []string
	SignalRefs     []string
	ModelID        string
	ModelVersion   string
	PromptVersion  string
	ContextDigest  string
	ExpiresAt      time.Time
	Supersedes     *id.Proposal
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Version        int64
	ActorID        string
	Reason         string
}

// Command represents an immutable lifecycle transition request.
type Command struct {
	ProposalID      id.Proposal
	ExpectedVersion int64
	Target          proposalv1.ProposalStatus
	ActorID         string
	OccurredAt      time.Time
	Reason          string
	AcceptedID      id.AcceptedCommand // for approved proposals, the accepted command identifier
}

// Receipt is the original committed transition.
type Receipt struct {
	ProposalID id.Proposal
	From       proposalv1.ProposalStatus
	To         proposalv1.ProposalStatus
	Version    int64
	OccurredAt time.Time
	ActorID    string
}

func isTerminalStatus(status proposalv1.ProposalStatus) bool {
	return status == proposalv1.ProposalStatusApproved || status == proposalv1.ProposalStatusRejected ||
		status == proposalv1.ProposalStatusExpired || status == proposalv1.ProposalStatusCancelled ||
		status == proposalv1.ProposalStatusSuperseded
}

// Validate checks immutable proposal invariants.
func (proposal Proposal) Validate() error {
	if proposal.ID.IsZero() || proposal.TenantID.IsZero() {
		return ErrInvalid
	}
	if proposal.VerificationID.IsZero() == (proposal.PolicyID == nil) {
		return ErrInvalid
	}
	if proposal.Mode == proposalv1.AutomationModeUnknown || proposal.Status == proposalv1.ProposalStatusUnknown {
		return ErrInvalid
	}
	if len(proposal.Actions) == 0 || len(proposal.Actions) > proposalv1.MaxActions {
		return ErrInvalid
	}
	for _, action := range proposal.Actions {
		if !proposalv1.IsAllowedKind(action.Kind) {
			return ErrUnknownKind
		}
		if len(action.Args) == 0 || len(action.Args) > proposalv1.MaxArgBytes {
			return ErrInvalid
		}
		if !json.Valid(action.Args) {
			return ErrInvalid
		}
	}
	if proposal.CreatedAt.IsZero() || proposal.ExpiresAt.IsZero() || !proposal.ExpiresAt.After(proposal.CreatedAt) {
		return ErrInvalid
	}
	if proposal.Version < 1 {
		return ErrInvalid
	}
	if proposal.ActorID == "" {
		return ErrInvalid
	}
	// contract-level validation for references and digests
	agent := proposalv1.AgentProposal{
		ProposalID:     proposal.ID.String(),
		TenantID:       proposal.TenantID.String(),
		VerificationID: proposal.VerificationID.String(),
		Mode:           proposal.Mode.String(),
		Status:         proposal.Status.String(),
		EvidenceRefs:   proposal.EvidenceRefs,
		SignalRefs:     proposal.SignalRefs,
		ModelID:        proposal.ModelID,
		ModelVersion:   proposal.ModelVersion,
		PromptVersion:  proposal.PromptVersion,
		ContextDigest:  proposal.ContextDigest,
		ExpiresAt:      proposal.ExpiresAt,
		CreatedAt:      proposal.CreatedAt,
		Reason:         proposal.Reason,
	}
	if proposal.PolicyID != nil {
		agent.PolicyID = proposal.PolicyID.String()
	}
	agent.Actions = proposal.Actions
	if proposal.Supersedes != nil {
		value := proposal.Supersedes.String()
		agent.Supersedes = &value
	}
	if err := proposalv1.ValidateProposal(agent); err != nil {
		return ErrInvalid
	}
	return nil
}

// Advance applies deterministic lifecycle graph.
func Advance(current Proposal, command Command) (Proposal, error) {
	if command.ProposalID.IsZero() || command.ProposalID != current.ID {
		return Proposal{}, ErrConflict
	}
	if command.ExpectedVersion != current.Version {
		return Proposal{}, ErrConflict
	}
	if isTerminalStatus(current.Status) {
		return Proposal{}, ErrConflict
	}
	if command.OccurredAt.IsZero() || command.OccurredAt.Before(current.UpdatedAt) {
		return Proposal{}, ErrInvalid
	}
	if !current.ExpiresAt.IsZero() && command.OccurredAt.After(current.ExpiresAt) && command.Target != proposalv1.ProposalStatusExpired {
		return Proposal{}, ErrExpired
	}
	if !validEdge(current.Status, command.Target) {
		return Proposal{}, ErrConflict
	}
	next := current
	next.Status = command.Target
	next.Version++
	next.UpdatedAt = command.OccurredAt
	next.ActorID = command.ActorID
	if command.Reason != "" {
		next.Reason = command.Reason
	}
	return next, nil
}

func validEdge(from, to proposalv1.ProposalStatus) bool {
	switch from {
	case proposalv1.ProposalStatusPending:
		return to == proposalv1.ProposalStatusApproved || to == proposalv1.ProposalStatusRejected ||
			to == proposalv1.ProposalStatusExpired || to == proposalv1.ProposalStatusCancelled ||
			to == proposalv1.ProposalStatusSuperseded
	default:
		return false
	}
}
