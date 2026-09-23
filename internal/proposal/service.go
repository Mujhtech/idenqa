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

// Service owns proposal lifecycle, guardrail orchestration, mode configuration,
// prompt/model registries, and deterministic accepted-command execution.
type Service struct {
	ids       *id.Generator
	clock     clock.Clock
	proposals Repository
	commands  CommandStore
	modes     ModeStore
	registry  RegistryStore
	usage     GenerationUsageStore
	model     proposalv1.ProposalModel
	binding   GenerationBindingChecker
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
	Usage     GenerationUsageStore
	Model     proposalv1.ProposalModel
	Binding   GenerationBindingChecker
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
		usage:     config.Usage,
		model:     config.Model,
		binding:   config.Binding,
		evidence:  config.Evidence,
		authority: config.Authority,
		region:    config.Region,
		limiter:   config.Limiter,
		audit:     config.Audit,
	}, nil
}

// GetGenerationUsageReport returns bounded tenant operational accounting.
func (service *Service) GetGenerationUsageReport(ctx context.Context, scope tenant.Scope, from, to time.Time) (GenerationUsageReport, error) {
	if service.usage == nil || scope.ID().IsZero() {
		return GenerationUsageReport{}, ErrNotFound
	}
	return service.usage.GetGenerationUsageReport(ctx, scope.ID(), from, to)
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
	return service.persistPending(ctx, scope, proposal, modeConfig, actorID, false)
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
	if service.binding != nil {
		if err := service.binding.ValidateGenerationBinding(ctx, scope.ID(), modeConfig, request); err != nil {
			return Proposal{}, err
		}
	}
	if err := service.preflightGeneration(ctx, scope, request, modeConfig); err != nil {
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
	return service.persistPending(ctx, scope, proposal, modeConfig, actorID, true)
}

// preflightGeneration runs every guard that can be decided from the request
// before bounded context leaves Core. Human-approval requirements are allowed
// here because the generated proposal remains pending; all other denials stop
// the provider call. The cost limiter is charged exactly once for generation.
func (service *Service) preflightGeneration(ctx context.Context, scope tenant.Scope, request proposalv1.ProposalRequest, modeConfig ModeConfig) error {
	preflight := Proposal{
		TenantID:      scope.ID(),
		Mode:          request.Mode,
		Actions:       request.Actions,
		EvidenceRefs:  request.EvidenceRefs,
		SignalRefs:    request.SignalRefs,
		ContextDigest: request.ContextDigest,
	}
	if request.VerificationID != "" {
		verificationID, err := id.ParseVerification(request.VerificationID)
		if err != nil {
			return ErrInvalid
		}
		preflight.VerificationID = verificationID
	} else {
		policyID, err := id.ParsePolicy(request.PolicyID)
		if err != nil {
			return ErrInvalid
		}
		preflight.PolicyID = &policyID
	}
	result, err := ValidateGuardrails(ctx, GuardrailInput{
		Proposal: preflight, ModeConfig: modeConfig, Evidence: service.evidence,
		Authority: service.authority, Region: service.region, CostLimiter: service.limiter,
	})
	if err != nil {
		return err
	}
	if result.Allowed || result.Reason == "human approval required" || result.Reason == "high-risk requires human approval" {
		return nil
	}
	if result.Reason == "rate limited" {
		return ErrRateLimited
	}
	return fmt.Errorf("%w: generation preflight: %s", ErrNotAllowed, result.Reason)
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
func (service *Service) persistPending(ctx context.Context, scope tenant.Scope, proposal Proposal, modeConfig ModeConfig, actorID string, costPrechecked bool) (Proposal, error) {
	if err := proposal.Validate(); err != nil {
		return Proposal{}, err
	}
	var limiter CostLimiter
	if !costPrechecked {
		limiter = service.limiter
	}
	guardInput := GuardrailInput{
		Proposal:      proposal,
		ModeConfig:    modeConfig,
		Evidence:      service.evidence,
		Authority:     service.authority,
		Region:        service.region,
		CostLimiter:   limiter,
		HumanApproved: false,
	}
	result, err := ValidateGuardrails(ctx, guardInput)
	if err != nil {
		return Proposal{}, err
	}
	if !result.Allowed {
		// Hard failures are rejected at creation; human-approval requirements
		// defer to the approval step.
		if result.Reason == "rate limited" || result.Reason == "unknown evidence_ref" || result.Reason == "unknown signal_ref" ||
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
func (service *Service) PutModeConfig(ctx context.Context, scope tenant.Scope, workflow string, mode proposalv1.AutomationMode, allowedKinds []proposalv1.ActionKind, pins ModePins, version int64, actorID string) (ModeConfig, error) {
	if scope.ID().IsZero() || workflow == "" || actorID == "" {
		return ModeConfig{}, ErrInvalid
	}
	if mode == proposalv1.AutomationModeUnknown {
		return ModeConfig{}, ErrInvalid
	}
	if service.binding != nil && mode != proposalv1.AutomationModeDisabled {
		if pins.ModelID == nil || pins.PromptID == nil || pins.ModelVersion < 1 || pins.PromptVersion < 1 || pins.ActivationRevision < 1 || service.registry == nil {
			return ModeConfig{}, ErrInvalid
		}
		activation, err := service.registry.GetActivation(ctx, scope.ID(), workflow)
		if err != nil {
			return ModeConfig{}, err
		}
		if activation.State != GenerationActivationActive || activation.Revision != pins.ActivationRevision ||
			activation.ModelRegistryID != *pins.ModelID || activation.ModelRegistryVersion != pins.ModelVersion ||
			activation.PromptRegistryID != *pins.PromptID || activation.PromptRegistryVersion != pins.PromptVersion {
			return ModeConfig{}, ErrNotAllowed
		}
	}
	now := service.clock.Now().UTC()
	existing, err := service.modes.Get(ctx, scope.ID(), workflow)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			return ModeConfig{}, err
		}
		if version != 0 {
			return ModeConfig{}, ErrConflict
		}
		version = 1
	} else {
		if version != existing.Version {
			return ModeConfig{}, ErrConflict
		}
		version = existing.Version + 1
	}
	config := ModeConfig{
		TenantID:     scope.ID(),
		Workflow:     workflow,
		Mode:         mode,
		AllowedKinds: allowedKinds,
		ModelID:      pins.ModelID, ModelVersion: pins.ModelVersion,
		PromptID: pins.PromptID, PromptVersion: pins.PromptVersion,
		ActivationRevision: pins.ActivationRevision,
		Version:            version,
		CreatedAt:          now,
		UpdatedAt:          now,
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
	if scope.ID().IsZero() || content == "" || !validGenerationToken(modelID) || actorID == "" {
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

// CreateModelRecord creates an immutable tenant-owned generative-model revision.
func (service *Service) CreateModelRecord(ctx context.Context, scope tenant.Scope, logicalModelID, digest, actorID string) (GenerativeModelRecord, error) {
	if scope.ID().IsZero() || !validGenerationToken(logicalModelID) || !isSHA256(digest) || actorID == "" {
		return GenerativeModelRecord{}, ErrInvalid
	}
	if service.registry == nil {
		return GenerativeModelRecord{}, ErrNotFound
	}
	modelID, err := service.ids.NewModel()
	if err != nil {
		return GenerativeModelRecord{}, err
	}
	record := GenerativeModelRecord{ID: modelID, TenantID: scope.ID(), Version: 1, ModelID: logicalModelID, Digest: digest, CreatedAt: service.clock.Now().UTC(), ActorID: actorID}
	if err := service.registry.CreateModel(ctx, record); err != nil {
		return GenerativeModelRecord{}, err
	}
	return record, nil
}

// GetModelRecord retrieves an immutable tenant-owned model revision.
func (service *Service) GetModelRecord(ctx context.Context, scope tenant.Scope, modelID id.Model, version int64) (GenerativeModelRecord, error) {
	if service.registry == nil || scope.ID().IsZero() {
		return GenerativeModelRecord{}, ErrNotFound
	}
	return service.registry.GetModel(ctx, scope.ID(), modelID, version)
}

// ActivateGenerationRoute publishes one exact registry/runtime route revision.
func (service *Service) ActivateGenerationRoute(ctx context.Context, scope tenant.Scope, activation GenerationActivation, expectedRevision int64, actorID string) (GenerationActivation, error) {
	if service.registry == nil || scope.ID().IsZero() || actorID == "" || expectedRevision < 0 {
		return GenerationActivation{}, ErrInvalid
	}
	model, err := service.registry.GetModel(ctx, scope.ID(), activation.ModelRegistryID, activation.ModelRegistryVersion)
	if err != nil {
		return GenerationActivation{}, err
	}
	prompt, err := service.registry.GetPrompt(ctx, scope.ID(), activation.PromptRegistryID, activation.PromptRegistryVersion)
	if err != nil {
		return GenerationActivation{}, err
	}
	if model.ModelID == "" || prompt.ModelID != model.ModelID {
		return GenerationActivation{}, ErrNotAllowed
	}
	activation.TenantID = scope.ID()
	activation.ModelID = model.ModelID
	activation.Revision = expectedRevision + 1
	activation.State = GenerationActivationActive
	activation.Action = "activated"
	activation.ActorID = actorID
	activation.OccurredAt = service.clock.Now().UTC()
	if err := service.registry.PutActivation(ctx, activation, expectedRevision); err != nil {
		return GenerationActivation{}, err
	}
	return activation, nil
}

// RetireGenerationRoute disables the current workflow route without deleting history.
func (service *Service) RetireGenerationRoute(ctx context.Context, scope tenant.Scope, workflow string, expectedRevision int64, reason, actorID string) (GenerationActivation, error) {
	if service.registry == nil || scope.ID().IsZero() || workflow == "" || expectedRevision < 1 || actorID == "" {
		return GenerationActivation{}, ErrInvalid
	}
	current, err := service.registry.GetActivation(ctx, scope.ID(), workflow)
	if err != nil {
		return GenerationActivation{}, err
	}
	if current.Revision != expectedRevision {
		return GenerationActivation{}, ErrConflict
	}
	current.Revision++
	current.State = GenerationActivationRetired
	current.Action = "retired"
	current.SourceRevision = 0
	current.Reason, current.ActorID, current.OccurredAt = reason, actorID, service.clock.Now().UTC()
	if err := service.registry.PutActivation(ctx, current, expectedRevision); err != nil {
		return GenerationActivation{}, err
	}
	return current, nil
}

// RollbackGenerationRoute republishes a prior active route as a new revision.
func (service *Service) RollbackGenerationRoute(ctx context.Context, scope tenant.Scope, workflow string, expectedRevision, targetRevision int64, reason, actorID string) (GenerationActivation, error) {
	if service.registry == nil || scope.ID().IsZero() || workflow == "" || expectedRevision < 1 || targetRevision < 1 || actorID == "" {
		return GenerationActivation{}, ErrInvalid
	}
	current, err := service.registry.GetActivation(ctx, scope.ID(), workflow)
	if err != nil {
		return GenerationActivation{}, err
	}
	if current.Revision != expectedRevision {
		return GenerationActivation{}, ErrConflict
	}
	target, err := service.registry.GetActivationRevision(ctx, scope.ID(), workflow, targetRevision)
	if err != nil {
		return GenerationActivation{}, err
	}
	if target.State != GenerationActivationActive {
		return GenerationActivation{}, ErrNotAllowed
	}
	target.Revision = expectedRevision + 1
	target.State = GenerationActivationActive
	target.Action = "rolled_back"
	target.SourceRevision = targetRevision
	target.Reason, target.ActorID, target.OccurredAt = reason, actorID, service.clock.Now().UTC()
	if err := service.registry.PutActivation(ctx, target, expectedRevision); err != nil {
		return GenerationActivation{}, err
	}
	return target, nil
}

// GetGenerationActivation returns the current workflow activation.
func (service *Service) GetGenerationActivation(ctx context.Context, scope tenant.Scope, workflow string) (GenerationActivation, error) {
	if service.registry == nil || scope.ID().IsZero() || workflow == "" {
		return GenerationActivation{}, ErrInvalid
	}
	return service.registry.GetActivation(ctx, scope.ID(), workflow)
}

// ListGenerationActivationHistory returns newest lifecycle revisions first.
func (service *Service) ListGenerationActivationHistory(ctx context.Context, scope tenant.Scope, workflow string, limit int) ([]GenerationActivation, error) {
	if service.registry == nil || scope.ID().IsZero() || workflow == "" {
		return nil, ErrInvalid
	}
	return service.registry.ListActivationHistory(ctx, scope.ID(), workflow, limit)
}

// CreateImpactRecord stores an AI impact assessment.
func (service *Service) CreateImpactRecord(ctx context.Context, scope tenant.Scope, kind proposalv1.ActionKind, assessment, riskLevel, actorID string) (ImpactAssessment, error) {
	if scope.ID().IsZero() || !proposalv1.IsAllowedKind(kind) || assessment == "" || len(assessment) > 8192 ||
		(riskLevel != "low" && riskLevel != "medium" && riskLevel != "high" && riskLevel != "critical") || actorID == "" {
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

// GetImpactRecord returns one immutable AI impact assessment.
func (service *Service) GetImpactRecord(ctx context.Context, scope tenant.Scope, assessmentID string) (ImpactAssessment, error) {
	if service.registry == nil || scope.ID().IsZero() || assessmentID == "" {
		return ImpactAssessment{}, ErrInvalid
	}
	return service.registry.GetImpact(ctx, scope.ID(), assessmentID)
}

// ListImpactRecords returns a bounded newest-first page of assessments.
func (service *Service) ListImpactRecords(ctx context.Context, scope tenant.Scope, before time.Time, limit int) ([]ImpactAssessment, error) {
	if service.registry == nil || scope.ID().IsZero() || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	return service.registry.ListImpacts(ctx, scope.ID(), before, limit)
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
