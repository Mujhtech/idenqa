package proposal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Repository is the owning persistence port for proposals. Implementations
// must enforce tenant scoping and optimistic concurrency.
type Repository interface {
	Create(ctx context.Context, scope tenant.Scope, proposal Proposal) error
	Get(ctx context.Context, scope tenant.Scope, proposalID id.Proposal) (Proposal, error)
	Update(ctx context.Context, scope tenant.Scope, proposal Proposal, expectedVersion int64) error
	GetByVerification(ctx context.Context, scope tenant.Scope, verificationID id.Verification) ([]Proposal, error)
}

// CommandStore owns accepted commands.
type CommandStore interface {
	CreateCommand(ctx context.Context, scope tenant.Scope, command AcceptedCommandRecord) error
	GetCommand(ctx context.Context, scope tenant.Scope, commandID id.AcceptedCommand) (AcceptedCommandRecord, error)
	GetByProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal) (AcceptedCommandRecord, error)
	MarkExecuted(ctx context.Context, scope tenant.Scope, commandID id.AcceptedCommand, at time.Time) error
}

// AcceptedCommandRecord is the stored deterministic command.
type AcceptedCommandRecord struct {
	ID             id.AcceptedCommand
	ProposalID     id.Proposal
	TenantID       id.Tenant
	VerificationID id.Verification
	PolicyID       *id.Policy
	Kind           proposalv1.ActionKind
	Args           json.RawMessage
	ModelID        string
	ModelVersion   string
	PromptVersion  string
	CreatedAt      time.Time
	ExecutedAt     *time.Time
}

// AuditRecorder appends immutable audit events.
type AuditRecorder interface {
	Append(ctx context.Context, scope tenant.Scope, eventType string, aggregateID string, actorID string, digest string, occurredAt time.Time) error
}

// Service owns proposal lifecycle and guardrail orchestration.
// Service owns proposal lifecycle, guardrail orchestration, mode configuration,
// prompt/model registries, and deterministic accepted-command execution.
type Service struct {
	ids       *id.Generator
	clock     clock.Clock
	proposals Repository
	commands  CommandStore
	modes     ModeStore
	registry  RegistryStore
	model     proposalv1.ProposalModel
	evidence  EvidenceChecker
	authority AuthorityChecker
	region    RegionValidator
	limiter   CostLimiter
	audit     AuditRecorder
}

// ServiceConfig holds explicit dependencies (manual constructor injection).
type ServiceConfig struct {
	Generator *id.Generator
	Clock     clock.Clock
	Proposals Repository
	Commands  CommandStore
	Modes     ModeStore
	Registry  RegistryStore
	Model     proposalv1.ProposalModel
	Evidence  EvidenceChecker
	Authority AuthorityChecker
	Region    RegionValidator
	Limiter   CostLimiter
	Audit     AuditRecorder
}

// NewService constructs a service with explicit dependencies.
func NewService(config ServiceConfig) (*Service, error) {
	if config.Generator == nil || config.Clock == nil || config.Proposals == nil || config.Commands == nil || config.Modes == nil {
		return nil, errors.New("proposal service: generator, clock, proposals, commands, and modes are required")
	}
	// registry, evidence, authority, region, limiter, audit are optional for core lifecycle
	return &Service{
		ids:       config.Generator,
		clock:     config.Clock,
		proposals: config.Proposals,
		commands:  config.Commands,
		modes:     config.Modes,
		registry:  config.Registry,
		model:     config.Model,
		evidence:  config.Evidence,
		authority: config.Authority,
		region:    config.Region,
		limiter:   config.Limiter,
		audit:     config.Audit,
	}, nil
}

// GetProposal retrieves a proposal with tenant scope.
func (service *Service) GetProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal) (Proposal, error) {
	return service.proposals.Get(ctx, scope, proposalID)
}

// CreateProposal validates the request, checks mode, runs initial guardrails (without human approval),
// and persists a pending proposal. Raw evidence bytes are never accepted — only references.
func (service *Service) CreateProposal(ctx context.Context, scope tenant.Scope, request proposalv1.ProposalRequest, actorID string) (Proposal, error) {
	if scope.ID().IsZero() || actorID == "" {
		return Proposal{}, ErrInvalid
	}
	if err := proposalv1.ValidateProposalRequest(request); err != nil {
		return Proposal{}, fmt.Errorf("%w: validation: %w", ErrInvalid, err)
	}
	requestTenant, err := id.ParseTenant(request.TenantID)
	if err != nil || requestTenant != scope.ID() {
		return Proposal{}, ErrNotAllowed
	}
	modeConfig, err := service.resolveModeConfig(ctx, scope, request.Mode)
	if err != nil {
		return Proposal{}, err
	}
	proposalID, err := service.ids.NewProposal()
	if err != nil {
		return Proposal{}, err
	}
	now := service.clock.Now().UTC()
	verificationID, err := id.ParseVerification(request.VerificationID)
	if err != nil {
		return Proposal{}, ErrInvalid
	}
	proposal := Proposal{
		ID:             proposalID,
		TenantID:       scope.ID(),
		VerificationID: verificationID,
		Mode:           request.Mode,
		Status:         proposalv1.ProposalStatusPending,
		Actions:        request.Actions,
		EvidenceRefs:   request.EvidenceRefs,
		SignalRefs:     request.SignalRefs,
		ModelID:        request.ModelID,
		ModelVersion:   request.ModelVersion,
		PromptVersion:  request.PromptVersion,
		ContextDigest:  request.ContextDigest,
		ExpiresAt:      request.ExpiresAt,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
		ActorID:        actorID,
	}
	if request.Supersedes != nil {
		superseded, err := id.ParseProposal(*request.Supersedes)
		if err != nil {
			return Proposal{}, ErrInvalid
		}
		proposal.Supersedes = &superseded
	}
	return service.persistPending(ctx, scope, proposal, modeConfig, actorID)
}

// Propose invokes the configured ProposalModel to produce a bounded proposal,
// then runs the same deterministic guardrails and persistence as CreateProposal.
// It returns ErrNotFound when no proposal model is configured.
func (service *Service) Propose(ctx context.Context, scope tenant.Scope, request proposalv1.ProposalRequest, actorID string) (Proposal, error) {
	if scope.ID().IsZero() || actorID == "" {
		return Proposal{}, ErrInvalid
	}
	if service.model == nil {
		return Proposal{}, ErrNotFound
	}
	if err := proposalv1.ValidateProposalRequest(request); err != nil {
		return Proposal{}, fmt.Errorf("%w: validation: %w", ErrInvalid, err)
	}
	requestTenant, err := id.ParseTenant(request.TenantID)
	if err != nil || requestTenant != scope.ID() {
		return Proposal{}, ErrNotAllowed
	}
	modeConfig, err := service.resolveModeConfig(ctx, scope, request.Mode)
	if err != nil {
		return Proposal{}, err
	}
	agent, err := service.model.Propose(ctx, request)
	if err != nil {
		return Proposal{}, err
	}
	if err := proposalv1.ValidateProposal(agent); err != nil {
		return Proposal{}, fmt.Errorf("%w: model output: %w", ErrInvalid, err)
	}
	if agent.TenantID != scope.ID().String() {
		return Proposal{}, ErrNotAllowed
	}
	proposal, err := proposalFromAgent(agent, actorID)
	if err != nil {
		return Proposal{}, err
	}
	return service.persistPending(ctx, scope, proposal, modeConfig, actorID)
}

// resolveModeConfig loads the stored automation mode and fails closed when the
// mode is disabled, missing, or mismatched with the request.
func (service *Service) resolveModeConfig(ctx context.Context, scope tenant.Scope, requested proposalv1.AutomationMode) (ModeConfig, error) {
	modeConfig, err := service.modes.Get(ctx, scope.ID(), "default")
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return ModeConfig{}, ErrModeDisabled
		}
		return ModeConfig{}, err
	}
	if modeConfig.Mode == proposalv1.AutomationModeDisabled {
		return ModeConfig{}, ErrModeDisabled
	}
	if requested != modeConfig.Mode {
		return ModeConfig{}, fmt.Errorf("%w: mode mismatch", ErrInvalid)
	}
	return modeConfig, nil
}

// persistPending runs guardrails and stores a validated pending proposal.
func (service *Service) persistPending(ctx context.Context, scope tenant.Scope, proposal Proposal, modeConfig ModeConfig, actorID string) (Proposal, error) {
	if err := proposal.Validate(); err != nil {
		return Proposal{}, err
	}
	guardInput := GuardrailInput{
		Proposal:      proposal,
		ModeConfig:    modeConfig,
		Evidence:      service.evidence,
		Authority:     service.authority,
		Region:        service.region,
		CostLimiter:   service.limiter,
		HumanApproved: false,
	}
	result, err := ValidateGuardrails(ctx, guardInput)
	if err != nil {
		return Proposal{}, err
	}
	if !result.Allowed {
		// Hard failures are rejected at creation; human-approval requirements
		// defer to the approval step.
		if result.Reason == "rate limited" || result.Reason == "unknown evidence_ref" ||
			result.Reason == "raw evidence in args" || result.Reason == "processing authority not permitted" ||
			result.Reason == "region not allowed" {
			return Proposal{}, fmt.Errorf("%w: %s", ErrNotAllowed, result.Reason)
		}
	}
	if err := service.proposals.Create(ctx, scope, proposal); err != nil {
		return Proposal{}, err
	}
	if service.audit != nil {
		_ = service.audit.Append(ctx, scope, "proposal.created.v1", proposal.ID.String(), actorID, proposal.ContextDigest, proposal.CreatedAt)
	}
	return proposal, nil
}

func proposalFromAgent(agent proposalv1.AgentProposal, actorID string) (Proposal, error) {
	proposalID, err := id.ParseProposal(agent.ProposalID)
	if err != nil {
		return Proposal{}, ErrInvalid
	}
	tenantID, err := id.ParseTenant(agent.TenantID)
	if err != nil {
		return Proposal{}, ErrInvalid
	}
	var verificationID id.Verification
	if agent.VerificationID != "" {
		verificationID, err = id.ParseVerification(agent.VerificationID)
		if err != nil {
			return Proposal{}, ErrInvalid
		}
	}
	var policyID *id.Policy
	if agent.PolicyID != "" {
		parsed, err := id.ParsePolicy(agent.PolicyID)
		if err != nil {
			return Proposal{}, ErrInvalid
		}
		policyID = &parsed
	}
	mode, ok := proposalv1.ParseAutomationMode(agent.Mode)
	if !ok {
		return Proposal{}, ErrInvalid
	}
	status, ok := proposalv1.ParseProposalStatus(agent.Status)
	if !ok {
		return Proposal{}, ErrInvalid
	}
	proposal := Proposal{
		ID:             proposalID,
		TenantID:       tenantID,
		VerificationID: verificationID,
		PolicyID:       policyID,
		Mode:           mode,
		Status:         status,
		Actions:        agent.Actions,
		EvidenceRefs:   agent.EvidenceRefs,
		SignalRefs:     agent.SignalRefs,
		ModelID:        agent.ModelID,
		ModelVersion:   agent.ModelVersion,
		PromptVersion:  agent.PromptVersion,
		ContextDigest:  agent.ContextDigest,
		ExpiresAt:      agent.ExpiresAt,
		CreatedAt:      agent.CreatedAt,
		UpdatedAt:      agent.CreatedAt,
		Version:        1,
		ActorID:        actorID,
		Reason:         agent.Reason,
	}
	if agent.Supersedes != nil {
		superseded, err := id.ParseProposal(*agent.Supersedes)
		if err != nil {
			return Proposal{}, ErrInvalid
		}
		proposal.Supersedes = &superseded
	}
	return proposal, nil
}

// ApproveProposal runs full guardrails with human approval flag, creates an AcceptedCommand per action, and transitions to approved.
func (service *Service) ApproveProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, actorID string, humanApproved bool) (Proposal, []AcceptedCommandRecord, error) {
	if scope.ID().IsZero() || proposalID.IsZero() || actorID == "" {
		return Proposal{}, nil, ErrInvalid
	}
	proposal, err := service.proposals.Get(ctx, scope, proposalID)
	if err != nil {
		return Proposal{}, nil, err
	}
	if proposal.Status != proposalv1.ProposalStatusPending {
		return Proposal{}, nil, ErrConflict
	}
	modeConfig, err := service.modes.Get(ctx, scope.ID(), "default")
	if err != nil {
		return Proposal{}, nil, err
	}
	guardInput := GuardrailInput{
		Proposal:      proposal,
		ModeConfig:    modeConfig,
		Evidence:      service.evidence,
		Authority:     service.authority,
		Region:        service.region,
		CostLimiter:   service.limiter,
		HumanApproved: humanApproved,
	}
	result, err := ValidateGuardrails(ctx, guardInput)
	if err != nil {
		return Proposal{}, nil, err
	}
	if !result.Allowed {
		if result.Reason == "human approval required" || result.Reason == "high-risk requires human approval" || result.Reason == "mode does not allow kind" {
			return Proposal{}, nil, ErrApprovalNeeded
		}
		return Proposal{}, nil, fmt.Errorf("%w: %s", ErrNotAllowed, result.Reason)
	}
	// guardrail passed — create accepted commands deterministically, one per action
	now := service.clock.Now().UTC()
	var commands []AcceptedCommandRecord
	for _, action := range proposal.Actions {
		cmdID, err := service.ids.NewAcceptedCommand()
		if err != nil {
			return Proposal{}, nil, err
		}
		record := AcceptedCommandRecord{
			ID:             cmdID,
			ProposalID:     proposalID,
			TenantID:       scope.ID(),
			VerificationID: proposal.VerificationID,
			PolicyID:       proposal.PolicyID,
			Kind:           action.Kind,
			Args:           action.Args,
			ModelID:        proposal.ModelID,
			ModelVersion:   proposal.ModelVersion,
			PromptVersion:  proposal.PromptVersion,
			CreatedAt:      now,
		}
		if err := service.commands.CreateCommand(ctx, scope, record); err != nil {
			return Proposal{}, nil, err
		}
		commands = append(commands, record)
	}
	approveCmd := Command{
		ProposalID:      proposalID,
		ExpectedVersion: expectedVersion,
		Target:          proposalv1.ProposalStatusApproved,
		ActorID:         actorID,
		OccurredAt:      now,
	}
	approved, err := Advance(proposal, approveCmd)
	if err != nil {
		return Proposal{}, nil, err
	}
	if err := service.proposals.Update(ctx, scope, approved, expectedVersion); err != nil {
		return Proposal{}, nil, err
	}
	if service.audit != nil {
		_ = service.audit.Append(ctx, scope, "proposal.approved.v1", proposalID.String(), actorID, approved.ContextDigest, now)
		for _, cmd := range commands {
			_ = service.audit.Append(ctx, scope, "proposal.command.created.v1", cmd.ID.String(), actorID, string(cmd.Args), now)
		}
	}
	return approved, commands, nil
}

// ExecuteCommand replays the stored AcceptedCommand deterministically without re-querying the model.
func (service *Service) ExecuteCommand(ctx context.Context, scope tenant.Scope, commandID id.AcceptedCommand, executor proposalv1.CommandExecutor) error {
	if scope.ID().IsZero() || commandID.IsZero() {
		return ErrInvalid
	}
	record, err := service.commands.GetCommand(ctx, scope, commandID)
	if err != nil {
		return err
	}
	if record.ExecutedAt != nil {
		// replay is idempotent — already executed, return success without re-executing
		return nil
	}
	// deterministic execution via the provided executor (or a no-op when nil)
	if executor != nil {
		cmd := proposalv1.AcceptedCommand{
			CommandID:      record.ID.String(),
			ProposalID:     record.ProposalID.String(),
			TenantID:       record.TenantID.String(),
			VerificationID: record.VerificationID.String(),
			Kind:           record.Kind,
			Args:           record.Args,
			ModelID:        record.ModelID,
			ModelVersion:   record.ModelVersion,
			PromptVersion:  record.PromptVersion,
			CreatedAt:      record.CreatedAt,
		}
		if record.PolicyID != nil {
			cmd.PolicyID = record.PolicyID.String()
		}
		if err := proposalv1.ValidateAcceptedCommand(cmd); err != nil {
			return ErrInvalid
		}
		if err := executor.Execute(ctx, cmd); err != nil {
			return err
		}
	}
	// mark executed
	now := service.clock.Now().UTC()
	return service.commands.MarkExecuted(ctx, scope, commandID, now)
}

// RejectProposal transitions a pending proposal to rejected.
func (service *Service) RejectProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, actorID string, reason string) (Proposal, error) {
	return service.transition(ctx, scope, proposalID, expectedVersion, proposalv1.ProposalStatusRejected, actorID, reason)
}

// CancelProposal transitions a pending proposal to cancelled.
func (service *Service) CancelProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, actorID string) (Proposal, error) {
	return service.transition(ctx, scope, proposalID, expectedVersion, proposalv1.ProposalStatusCancelled, actorID, "")
}

// ExpireProposal transitions a pending proposal to expired.
func (service *Service) ExpireProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, actorID string) (Proposal, error) {
	return service.transition(ctx, scope, proposalID, expectedVersion, proposalv1.ProposalStatusExpired, actorID, "expired")
}

// SupersedeProposal transitions a pending proposal to superseded.
func (service *Service) SupersedeProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, actorID string) (Proposal, error) {
	return service.transition(ctx, scope, proposalID, expectedVersion, proposalv1.ProposalStatusSuperseded, actorID, "superseded")
}
func (service *Service) transition(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, target proposalv1.ProposalStatus, actorID string, reason string) (Proposal, error) {
	if scope.ID().IsZero() || proposalID.IsZero() || actorID == "" {
		return Proposal{}, ErrInvalid
	}
	proposal, err := service.proposals.Get(ctx, scope, proposalID)
	if err != nil {
		return Proposal{}, err
	}
	cmd := Command{
		ProposalID:      proposalID,
		ExpectedVersion: expectedVersion,
		Target:          target,
		ActorID:         actorID,
		OccurredAt:      service.clock.Now().UTC(),
		Reason:          reason,
	}
	next, err := Advance(proposal, cmd)
	if err != nil {
		return Proposal{}, err
	}
	if err := service.proposals.Update(ctx, scope, next, expectedVersion); err != nil {
		return Proposal{}, err
	}
	if service.audit != nil {
		_ = service.audit.Append(ctx, scope, "proposal."+target.String()+".v1", proposalID.String(), actorID, next.ContextDigest, cmd.OccurredAt)
	}
	return next, nil
}

// PutModeConfig stores a versioned automation-mode configuration with expectedVersion CAS.
func (service *Service) PutModeConfig(ctx context.Context, scope tenant.Scope, workflow string, mode proposalv1.AutomationMode, allowedKinds []proposalv1.ActionKind, version int64, actorID string) (ModeConfig, error) {
	if scope.ID().IsZero() || workflow == "" || actorID == "" {
		return ModeConfig{}, ErrInvalid
	}
	if mode == proposalv1.AutomationModeUnknown {
		return ModeConfig{}, ErrInvalid
	}
	now := service.clock.Now().UTC()
	existing, err := service.modes.Get(ctx, scope.ID(), workflow)
	var existingVersion int64
	var existingFound bool
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return ModeConfig{}, err
		}
	} else {
		existingFound = true
		existingVersion = existing.Version
		if version != 0 && version != existingVersion+1 {
			return ModeConfig{}, ErrConflict
		}
		if version == 0 {
			version = existingVersion + 1
		}
	}
	if !existingFound {
		if version == 0 {
			version = 1
		} else if version != 1 {
			return ModeConfig{}, ErrConflict
		}
	}
	config := ModeConfig{
		TenantID:     scope.ID(),
		Workflow:     workflow,
		Mode:         mode,
		AllowedKinds: allowedKinds,
		Version:      version,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if existing.Version != 0 {
		config.CreatedAt = existing.CreatedAt
	}
	if err := config.Validate(); err != nil {
		return ModeConfig{}, err
	}
	if err := service.modes.Put(ctx, config); err != nil {
		return ModeConfig{}, err
	}
	if service.audit != nil {
		_ = service.audit.Append(ctx, scope, "proposal.mode.configured.v1", workflow, actorID, mode.String(), now)
	}
	return config, nil
}

// GetModeConfig retrieves the mode configuration for a workflow.
func (service *Service) GetModeConfig(ctx context.Context, scope tenant.Scope, workflow string) (ModeConfig, error) {
	if scope.ID().IsZero() || workflow == "" {
		return ModeConfig{}, ErrInvalid
	}
	return service.modes.Get(ctx, scope.ID(), workflow)
}

// CreatePromptRecord creates an immutable prompt version.
func (service *Service) CreatePromptRecord(ctx context.Context, scope tenant.Scope, content, modelID, actorID string) (PromptRecord, error) {
	if scope.ID().IsZero() || content == "" || modelID == "" || actorID == "" {
		return PromptRecord{}, ErrInvalid
	}
	if service.registry == nil {
		return PromptRecord{}, ErrNotFound
	}
	promptID, err := service.ids.NewPrompt()
	if err != nil {
		return PromptRecord{}, err
	}
	now := service.clock.Now().UTC()
	record := PromptRecord{
		ID:        promptID,
		TenantID:  scope.ID(),
		Version:   1,
		Content:   content,
		Digest:    DigestPrompt(content),
		ModelID:   modelID,
		Sensitive: isSensitivePrompt(content),
		CreatedAt: now,
		ActorID:   actorID,
	}
	if err := service.registry.CreatePrompt(ctx, record); err != nil {
		return PromptRecord{}, err
	}
	if service.audit != nil {
		_ = service.audit.Append(ctx, scope, "prompt.created.v1", promptID.String(), actorID, record.Digest, now)
	}
	return record, nil
}

// GetPromptRecord retrieves a prompt version.
func (service *Service) GetPromptRecord(ctx context.Context, scope tenant.Scope, promptID id.Prompt, version int64) (PromptRecord, error) {
	if service.registry == nil {
		return PromptRecord{}, ErrNotFound
	}
	return service.registry.GetPrompt(ctx, scope.ID(), promptID, version)
}

// CreateImpactRecord stores an AI impact assessment.
func (service *Service) CreateImpactRecord(ctx context.Context, scope tenant.Scope, kind proposalv1.ActionKind, assessment, riskLevel, actorID string) (ImpactAssessment, error) {
	if scope.ID().IsZero() || assessment == "" || actorID == "" {
		return ImpactAssessment{}, ErrInvalid
	}
	if service.registry == nil {
		return ImpactAssessment{}, ErrNotFound
	}
	now := service.clock.Now().UTC()
	record := ImpactAssessment{
		ID:         fmt.Sprintf("imp_%d", now.UnixNano()),
		TenantID:   scope.ID(),
		Kind:       kind,
		Assessment: assessment,
		RiskLevel:  riskLevel,
		CreatedAt:  now,
		ActorID:    actorID,
	}
	if err := service.registry.CreateImpact(ctx, record); err != nil {
		return ImpactAssessment{}, err
	}
	if service.audit != nil {
		_ = service.audit.Append(ctx, scope, "impact.created.v1", record.ID, actorID, string(kind), now)
	}
	return record, nil
}

// ProposeReviewCopilot creates a model-driven review-copilot summary proposal.
func (service *Service) ProposeReviewCopilot(ctx context.Context, scope tenant.Scope, verificationID id.Verification, evidenceRefs []string, actorID string) (Proposal, error) {
	if verificationID.IsZero() {
		return Proposal{}, ErrInvalid
	}
	return service.Propose(ctx, scope, proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeAssist,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionReviewCopilotSummarize, Args: []byte(`{}`)}},
		EvidenceRefs:   evidenceRefs,
		ModelID:        "ai.copilot",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		ContextDigest:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:      service.clock.Now().Add(30 * time.Minute),
	}, actorID)
}

// ProposeAdaptiveRoute creates a model-driven adaptive-routing proposal.
func (service *Service) ProposeAdaptiveRoute(ctx context.Context, scope tenant.Scope, verificationID id.Verification, actorID string) (Proposal, error) {
	if verificationID.IsZero() {
		return Proposal{}, ErrInvalid
	}
	return service.Propose(ctx, scope, proposalv1.ProposalRequest{
		TenantID:       scope.ID().String(),
		VerificationID: verificationID.String(),
		Mode:           proposalv1.AutomationModeRecommend,
		Actions:        []proposalv1.BoundedAction{{Kind: proposalv1.ActionRoutingAdaptiveSuggest, Args: []byte(`{}`)}},
		ModelID:        "ai.router",
		ModelVersion:   "v1",
		PromptVersion:  "p1",
		ContextDigest:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:      service.clock.Now().Add(15 * time.Minute),
	}, actorID)
}
