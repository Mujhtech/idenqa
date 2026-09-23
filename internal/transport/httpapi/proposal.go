package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/proposal"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// ProposalService is the application capability exposed via HTTP.
type ProposalService interface {
	Propose(ctx context.Context, scope tenant.Scope, request proposalv1.ProposalRequest, actorID string) (proposal.Proposal, error)
	GetProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal) (proposal.Proposal, error)
	ApproveProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, actorID string, humanApproved bool) (proposal.Proposal, []proposal.AcceptedCommandRecord, error)
	RejectProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, actorID string, reason string) (proposal.Proposal, error)
	CancelProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, actorID string) (proposal.Proposal, error)
	ExecuteCommand(ctx context.Context, scope tenant.Scope, commandID id.AcceptedCommand, executor proposalv1.CommandExecutor) error
	PutModeConfig(ctx context.Context, scope tenant.Scope, workflow string, mode proposalv1.AutomationMode, allowedKinds []proposalv1.ActionKind, pins proposal.ModePins, version int64, actorID string) (proposal.ModeConfig, error)
	GetModeConfig(ctx context.Context, scope tenant.Scope, workflow string) (proposal.ModeConfig, error)
	CreatePromptRecord(ctx context.Context, scope tenant.Scope, content, modelID, actorID string) (proposal.PromptRecord, error)
	GetPromptRecord(ctx context.Context, scope tenant.Scope, promptID id.Prompt, version int64) (proposal.PromptRecord, error)
	CreateModelRecord(ctx context.Context, scope tenant.Scope, logicalModelID, digest, actorID string) (proposal.GenerativeModelRecord, error)
	GetModelRecord(ctx context.Context, scope tenant.Scope, modelID id.Model, version int64) (proposal.GenerativeModelRecord, error)
	ActivateGenerationRoute(ctx context.Context, scope tenant.Scope, activation proposal.GenerationActivation, expectedRevision int64, actorID string) (proposal.GenerationActivation, error)
	RetireGenerationRoute(ctx context.Context, scope tenant.Scope, workflow string, expectedRevision int64, reason, actorID string) (proposal.GenerationActivation, error)
	RollbackGenerationRoute(ctx context.Context, scope tenant.Scope, workflow string, expectedRevision, targetRevision int64, reason, actorID string) (proposal.GenerationActivation, error)
	GetGenerationActivation(ctx context.Context, scope tenant.Scope, workflow string) (proposal.GenerationActivation, error)
	ListGenerationActivationHistory(ctx context.Context, scope tenant.Scope, workflow string, limit int) ([]proposal.GenerationActivation, error)
	GetGenerationUsageReport(ctx context.Context, scope tenant.Scope, from, to time.Time) (proposal.GenerationUsageReport, error)
}

type proposalImpactService interface {
	CreateImpactRecord(ctx context.Context, scope tenant.Scope, kind proposalv1.ActionKind, assessment, riskLevel, actorID string) (proposal.ImpactAssessment, error)
	GetImpactRecord(ctx context.Context, scope tenant.Scope, assessmentID string) (proposal.ImpactAssessment, error)
	ListImpactRecords(ctx context.Context, scope tenant.Scope, before time.Time, limit int) ([]proposal.ImpactAssessment, error)
}

// ProposalRoutes exposes proposal lifecycle via HTTP.
type ProposalRoutes struct {
	handlerBase
	access  *AccessMiddleware
	service ProposalService
}

// NewProposalRoutes constructs the proposal HTTP boundary.
func NewProposalRoutes(accessMiddleware *AccessMiddleware, service ProposalService, logger *slog.Logger) (*ProposalRoutes, error) {
	if accessMiddleware == nil || service == nil || logger == nil {
		return nil, errors.New("proposal route dependencies are required")
	}
	return &ProposalRoutes{
		access:      accessMiddleware,
		service:     service,
		handlerBase: newHandlerBase(logger, "proposal"),
	}, nil
}

// Register adds proposal routes.
func (routes *ProposalRoutes) Register(router chi.Router) {
	read := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionProposalsRead)}
	write := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionProposalsWrite)}
	approve := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionProposalsApprove)}
	configure := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionProposalsConfigure)}
	promptWrite := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionPromptsWrite)}
	promptRead := []func(http.Handler) http.Handler{routes.access.Authorize(access.PermissionPromptsRead)}

	router.With(write...).Post("/proposals", routes.create)
	router.With(read...).Get("/proposals/{proposalID}", routes.get)
	router.With(approve...).Post("/proposals/{proposalID}/approve", routes.approve)
	router.With(approve...).Post("/proposals/{proposalID}/reject", routes.reject)
	router.With(write...).Post("/proposals/{proposalID}/cancel", routes.cancel)
	router.With(approve...).Post("/accepted-commands/{commandID}/execute", routes.execute)
	router.With(configure...).Put("/proposal-modes/{workflow}", routes.putMode)
	router.With(configure...).Get("/proposal-modes/{workflow}", routes.getMode)
	router.With(promptWrite...).Post("/prompts", routes.createPrompt)
	router.With(promptRead...).Get("/prompts/{promptID}/{version}", routes.getPrompt)
	router.With(configure...).Post("/proposal-models", routes.createModel)
	router.With(configure...).Get("/proposal-models/{modelID}/{version}", routes.getModel)
	router.With(configure...).Put("/proposal-activations/{workflow}", routes.activateRoute)
	router.With(configure...).Get("/proposal-activations/{workflow}", routes.getActivation)
	router.With(configure...).Get("/proposal-activations/{workflow}/history", routes.listActivationHistory)
	router.With(configure...).Post("/proposal-activations/{workflow}/retire", routes.retireRoute)
	router.With(configure...).Post("/proposal-activations/{workflow}/rollback", routes.rollbackRoute)
	router.With(read...).Get("/proposal-usage", routes.getUsageReport)
	router.With(configure...).Post("/proposal-impact-assessments", routes.createImpact)
	router.With(read...).Get("/proposal-impact-assessments", routes.listImpacts)
	router.With(read...).Get("/proposal-impact-assessments/{assessmentID}", routes.getImpact)
}

type createProposalRequest struct {
	VerificationID string                     `json:"verification_id"`
	PolicyID       string                     `json:"policy_id"`
	Mode           string                     `json:"mode"`
	Actions        []proposalv1.BoundedAction `json:"actions"`
	EvidenceRefs   []string                   `json:"evidence_refs,omitempty"`
	SignalRefs     []string                   `json:"signal_refs,omitempty"`
	ModelID        string                     `json:"model_id"`
	ModelVersion   string                     `json:"model_version"`
	PromptVersion  string                     `json:"prompt_version"`
	ContextDigest  string                     `json:"context_digest"`
	ExpiresAt      string                     `json:"expires_at"`
	Supersedes     *string                    `json:"supersedes,omitempty"`
}

type approveProposalRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
	HumanApproved   bool  `json:"human_approved"`
}

type versionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

type proposalResource struct {
	ProposalID     string                     `json:"proposal_id"`
	TenantID       string                     `json:"tenant_id"`
	VerificationID string                     `json:"verification_id,omitempty"`
	PolicyID       string                     `json:"policy_id,omitempty"`
	Mode           string                     `json:"mode"`
	Status         string                     `json:"status"`
	Actions        []proposalv1.BoundedAction `json:"actions"`
	EvidenceRefs   []string                   `json:"evidence_refs,omitempty"`
	SignalRefs     []string                   `json:"signal_refs,omitempty"`
	ModelID        string                     `json:"model_id"`
	ModelVersion   string                     `json:"model_version"`
	PromptVersion  string                     `json:"prompt_version"`
	ContextDigest  string                     `json:"context_digest"`
	ExpiresAt      string                     `json:"expires_at"`
	CreatedAt      string                     `json:"created_at"`
	Version        int64                      `json:"version"`
	Reason         string                     `json:"reason,omitempty"`
}

type createImpactRequest struct {
	Kind       proposalv1.ActionKind `json:"kind"`
	Assessment string                `json:"assessment"`
	RiskLevel  string                `json:"risk_level"`
}

type impactResource struct {
	ID         string                `json:"id"`
	Kind       proposalv1.ActionKind `json:"kind"`
	Assessment string                `json:"assessment"`
	RiskLevel  string                `json:"risk_level"`
	CreatedAt  string                `json:"created_at"`
	ActorID    string                `json:"actor_id"`
}

func (routes *ProposalRoutes) create(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[createProposalRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	var verificationID id.Verification
	if body.VerificationID != "" {
		verificationID, err = id.ParseVerification(body.VerificationID)
		if err != nil {
			routes.problem(writer, request, proposal.ErrInvalid)
			return
		}
	}
	var policyID *id.Policy
	if body.PolicyID != "" {
		parsed, err := id.ParsePolicy(body.PolicyID)
		if err != nil {
			routes.problem(writer, request, proposal.ErrInvalid)
			return
		}
		policyID = &parsed
	}
	mode, ok := proposalv1.ParseAutomationMode(body.Mode)
	if !ok {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	expiresAt, err := parseTime(body.ExpiresAt)
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	var supersedes *string
	if body.Supersedes != nil {
		supersedes = body.Supersedes
	}
	req := proposalv1.ProposalRequest{
		TenantID:       authority.TenantScope().ID().String(),
		VerificationID: verificationID.String(),
		Mode:           mode,
		Actions:        body.Actions,
		EvidenceRefs:   body.EvidenceRefs,
		SignalRefs:     body.SignalRefs,
		ModelID:        body.ModelID,
		ModelVersion:   body.ModelVersion,
		PromptVersion:  body.PromptVersion,
		ContextDigest:  body.ContextDigest,
		ExpiresAt:      expiresAt,
		Supersedes:     supersedes,
	}
	if policyID != nil {
		req.PolicyID = policyID.String()
	}
	proposal, err := routes.service.Propose(request.Context(), authority.TenantScope(), req, authority.Principal().KeyID().String())
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeProposal(writer, request, http.StatusCreated, proposal)
}

func (routes *ProposalRoutes) get(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	proposalID, err := id.ParseProposal(chi.URLParam(request, "proposalID"))
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	proposal, err := routes.service.GetProposal(request.Context(), authority.TenantScope(), proposalID)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeProposal(writer, request, http.StatusOK, proposal)
}

func (routes *ProposalRoutes) approve(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	proposalID, err := id.ParseProposal(chi.URLParam(request, "proposalID"))
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[approveProposalRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	proposal, _, err := routes.service.ApproveProposal(request.Context(), authority.TenantScope(), proposalID, body.ExpectedVersion, authority.Principal().KeyID().String(), body.HumanApproved)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeProposal(writer, request, http.StatusOK, proposal)
}

func (routes *ProposalRoutes) reject(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	proposalID, err := id.ParseProposal(chi.URLParam(request, "proposalID"))
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[versionRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	proposal, err := routes.service.RejectProposal(request.Context(), authority.TenantScope(), proposalID, body.ExpectedVersion, authority.Principal().KeyID().String(), "rejected")
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeProposal(writer, request, http.StatusOK, proposal)
}

func (routes *ProposalRoutes) cancel(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	proposalID, err := id.ParseProposal(chi.URLParam(request, "proposalID"))
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[versionRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	proposal, err := routes.service.CancelProposal(request.Context(), authority.TenantScope(), proposalID, body.ExpectedVersion, authority.Principal().KeyID().String())
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeProposal(writer, request, http.StatusOK, proposal)
}

func (routes *ProposalRoutes) execute(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	commandID, err := id.ParseAcceptedCommand(chi.URLParam(request, "commandID"))
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	if err := routes.service.ExecuteCommand(request.Context(), authority.TenantScope(), commandID, proposal.NewReferenceExecutor()); err != nil {
		routes.problem(writer, request, err)
		return
	}
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(http.StatusNoContent)
}

type putModeRequest struct {
	Mode                  string   `json:"mode"`
	AllowedKinds          []string `json:"allowed_kinds,omitempty"`
	ExpectedVersion       int64    `json:"expected_version"`
	ModelRegistryID       string   `json:"model_registry_id,omitempty"`
	ModelRegistryVersion  int64    `json:"model_registry_version,omitempty"`
	PromptID              string   `json:"prompt_id,omitempty"`
	PromptRegistryVersion int64    `json:"prompt_registry_version,omitempty"`
	ActivationRevision    int64    `json:"activation_revision,omitempty"`
}

type modeResource struct {
	Workflow              string   `json:"workflow"`
	Mode                  string   `json:"mode"`
	AllowedKinds          []string `json:"allowed_kinds"`
	Version               int64    `json:"version"`
	ModelRegistryID       string   `json:"model_registry_id,omitempty"`
	ModelRegistryVersion  int64    `json:"model_registry_version,omitempty"`
	PromptID              string   `json:"prompt_id,omitempty"`
	PromptRegistryVersion int64    `json:"prompt_registry_version,omitempty"`
	ActivationRevision    int64    `json:"activation_revision,omitempty"`
}

type createModelRequest struct {
	ModelID string `json:"model_id"`
	Digest  string `json:"digest"`
}
type modelResource struct {
	ModelRegistryID string `json:"model_registry_id"`
	Version         int64  `json:"version"`
	ModelID         string `json:"model_id"`
	Digest          string `json:"digest"`
	CreatedAt       string `json:"created_at"`
}
type activationRequest struct {
	ModelRegistryID       string `json:"model_registry_id"`
	ModelRegistryVersion  int64  `json:"model_registry_version"`
	PromptRegistryID      string `json:"prompt_registry_id"`
	PromptRegistryVersion int64  `json:"prompt_registry_version"`
	ModelVersion          string `json:"model_version"`
	PromptVersion         string `json:"prompt_version"`
	ExpectedRevision      int64  `json:"expected_revision"`
	Reason                string `json:"reason,omitempty"`
}
type lifecycleRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	TargetRevision   int64  `json:"target_revision,omitempty"`
	Reason           string `json:"reason,omitempty"`
}
type activationResource struct {
	Workflow              string `json:"workflow"`
	Revision              int64  `json:"revision"`
	State                 string `json:"state"`
	ModelRegistryID       string `json:"model_registry_id"`
	ModelRegistryVersion  int64  `json:"model_registry_version"`
	PromptRegistryID      string `json:"prompt_registry_id"`
	PromptRegistryVersion int64  `json:"prompt_registry_version"`
	ModelID               string `json:"model_id"`
	ModelVersion          string `json:"model_version"`
	PromptVersion         string `json:"prompt_version"`
	Action                string `json:"action"`
	SourceRevision        int64  `json:"source_revision,omitempty"`
	Reason                string `json:"reason,omitempty"`
	ActorID               string `json:"actor_id"`
	OccurredAt            string `json:"occurred_at"`
}
type usageReportResource struct {
	From                string `json:"from"`
	To                  string `json:"to"`
	Attempts            int64  `json:"attempts"`
	Succeeded           int64  `json:"succeeded"`
	Failed              int64  `json:"failed"`
	Rejected            int64  `json:"rejected"`
	InvalidOutput       int64  `json:"invalid_output"`
	UnreportedUsage     int64  `json:"unreported_usage"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	EstimatedCostMicros int64  `json:"estimated_cost_micros"`
}

type createPromptRequest struct {
	Content string `json:"content"`
	ModelID string `json:"model_id"`
}

type promptResource struct {
	PromptID string `json:"prompt_id"`
	Version  int64  `json:"version"`
	Content  string `json:"content"`
	Digest   string `json:"digest"`
	ModelID  string `json:"model_id"`
}

func (routes *ProposalRoutes) putMode(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	workflow := chi.URLParam(request, "workflow")
	if workflow == "" {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	body, err := decodeJSONBody[putModeRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	mode, ok := proposalv1.ParseAutomationMode(body.Mode)
	if !ok {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	var allowed []proposalv1.ActionKind
	for _, kindStr := range body.AllowedKinds {
		kind := proposalv1.ActionKind(kindStr)
		if !proposalv1.IsAllowedKind(kind) {
			routes.problem(writer, request, proposal.ErrUnknownKind)
			return
		}
		allowed = append(allowed, kind)
	}
	var pins proposal.ModePins
	if body.ModelRegistryID != "" || body.PromptID != "" || body.ActivationRevision != 0 {
		modelID, parseErr := id.ParseModel(body.ModelRegistryID)
		if parseErr != nil {
			routes.problem(writer, request, proposal.ErrInvalid)
			return
		}
		promptID, parseErr := id.ParsePrompt(body.PromptID)
		if parseErr != nil {
			routes.problem(writer, request, proposal.ErrInvalid)
			return
		}
		pins = proposal.ModePins{ModelID: &modelID, ModelVersion: body.ModelRegistryVersion, PromptID: &promptID, PromptVersion: body.PromptRegistryVersion, ActivationRevision: body.ActivationRevision}
	}
	config, err := routes.service.PutModeConfig(request.Context(), authority.TenantScope(), workflow, mode, allowed, pins, body.ExpectedVersion, authority.Principal().KeyID().String())
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	resource := modeResource{
		Workflow:     config.Workflow,
		Mode:         config.Mode.String(),
		Version:      config.Version,
		AllowedKinds: make([]string, 0, len(config.AllowedKinds)),
	}
	populateModePins(&resource, config)
	for _, kind := range config.AllowedKinds {
		resource.AllowedKinds = append(resource.AllowedKinds, string(kind))
	}
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, http.StatusOK, resource); err != nil {
		routes.logger.ErrorContext(request.Context(), "write mode response")
	}
}

func (routes *ProposalRoutes) getMode(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	workflow := chi.URLParam(request, "workflow")
	if workflow == "" {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	config, err := routes.service.GetModeConfig(request.Context(), authority.TenantScope(), workflow)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	resource := modeResource{
		Workflow:     config.Workflow,
		Mode:         config.Mode.String(),
		Version:      config.Version,
		AllowedKinds: make([]string, 0, len(config.AllowedKinds)),
	}
	populateModePins(&resource, config)
	for _, kind := range config.AllowedKinds {
		resource.AllowedKinds = append(resource.AllowedKinds, string(kind))
	}
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, http.StatusOK, resource); err != nil {
		routes.logger.ErrorContext(request.Context(), "write mode response")
	}
}

func (routes *ProposalRoutes) createPrompt(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[createPromptRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	record, err := routes.service.CreatePromptRecord(request.Context(), authority.TenantScope(), body.Content, body.ModelID, authority.Principal().KeyID().String())
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	resource := promptResource{
		PromptID: record.ID.String(),
		Version:  record.Version,
		Content:  record.Content,
		Digest:   record.Digest,
		ModelID:  record.ModelID,
	}
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, http.StatusCreated, resource); err != nil {
		routes.logger.ErrorContext(request.Context(), "write prompt response")
	}
}

func (routes *ProposalRoutes) getPrompt(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	promptID, err := id.ParsePrompt(chi.URLParam(request, "promptID"))
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	versionStr := chi.URLParam(request, "version")
	version, err := strconv.ParseInt(versionStr, 10, 64)
	if err != nil || version < 1 {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	record, err := routes.service.GetPromptRecord(request.Context(), authority.TenantScope(), promptID, version)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	resource := promptResource{
		PromptID: record.ID.String(),
		Version:  record.Version,
		Content:  record.Content,
		Digest:   record.Digest,
		ModelID:  record.ModelID,
	}
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, http.StatusOK, resource); err != nil {
		routes.logger.ErrorContext(request.Context(), "write prompt response")
	}
}

func populateModePins(resource *modeResource, config proposal.ModeConfig) {
	if config.ModelID == nil || config.PromptID == nil {
		return
	}
	resource.ModelRegistryID, resource.ModelRegistryVersion = config.ModelID.String(), config.ModelVersion
	resource.PromptID, resource.PromptRegistryVersion = config.PromptID.String(), config.PromptVersion
	resource.ActivationRevision = config.ActivationRevision
}

func (routes *ProposalRoutes) createModel(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[createModelRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	record, err := routes.service.CreateModelRecord(request.Context(), authority.TenantScope(), body.ModelID, body.Digest, authority.Principal().KeyID().String())
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeModel(writer, request, http.StatusCreated, record)
}

func (routes *ProposalRoutes) getModel(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	modelID, err := id.ParseModel(chi.URLParam(request, "modelID"))
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	version, err := strconv.ParseInt(chi.URLParam(request, "version"), 10, 64)
	if err != nil || version < 1 {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	record, err := routes.service.GetModelRecord(request.Context(), authority.TenantScope(), modelID, version)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeModel(writer, request, http.StatusOK, record)
}

func (routes *ProposalRoutes) writeModel(writer http.ResponseWriter, request *http.Request, status int, record proposal.GenerativeModelRecord) {
	resource := modelResource{ModelRegistryID: record.ID.String(), Version: record.Version, ModelID: record.ModelID, Digest: record.Digest, CreatedAt: record.CreatedAt.Format(time.RFC3339Nano)}
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, status, resource); err != nil {
		routes.logger.ErrorContext(request.Context(), "write proposal model response")
	}
}

func (routes *ProposalRoutes) activateRoute(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[activationRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	modelID, err := id.ParseModel(body.ModelRegistryID)
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	promptID, err := id.ParsePrompt(body.PromptRegistryID)
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	activation := proposal.GenerationActivation{Workflow: chi.URLParam(request, "workflow"), ModelRegistryID: modelID, ModelRegistryVersion: body.ModelRegistryVersion, PromptRegistryID: promptID, PromptRegistryVersion: body.PromptRegistryVersion, ModelVersion: body.ModelVersion, PromptVersion: body.PromptVersion, Reason: body.Reason}
	result, err := routes.service.ActivateGenerationRoute(request.Context(), authority.TenantScope(), activation, body.ExpectedRevision, authority.Principal().KeyID().String())
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeActivation(writer, request, result)
}

func (routes *ProposalRoutes) getActivation(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	result, err := routes.service.GetGenerationActivation(request.Context(), authority.TenantScope(), chi.URLParam(request, "workflow"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeActivation(writer, request, result)
}

func (routes *ProposalRoutes) listActivationHistory(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			routes.problem(writer, request, proposal.ErrInvalid)
			return
		}
		limit = parsed
	}
	history, err := routes.service.ListGenerationActivationHistory(request.Context(), authority.TenantScope(), chi.URLParam(request, "workflow"), limit)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	resources := make([]activationResource, 0, len(history))
	for _, item := range history {
		resources = append(resources, activationProjection(item))
	}
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, http.StatusOK, map[string]any{"activations": resources}); err != nil {
		routes.logger.ErrorContext(request.Context(), "write proposal activation history response")
	}
}

func (routes *ProposalRoutes) retireRoute(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[lifecycleRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	result, err := routes.service.RetireGenerationRoute(request.Context(), authority.TenantScope(), chi.URLParam(request, "workflow"), body.ExpectedRevision, body.Reason, authority.Principal().KeyID().String())
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeActivation(writer, request, result)
}

func (routes *ProposalRoutes) rollbackRoute(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[lifecycleRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	result, err := routes.service.RollbackGenerationRoute(request.Context(), authority.TenantScope(), chi.URLParam(request, "workflow"), body.ExpectedRevision, body.TargetRevision, body.Reason, authority.Principal().KeyID().String())
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeActivation(writer, request, result)
}

func activationProjection(value proposal.GenerationActivation) activationResource {
	return activationResource{Workflow: value.Workflow, Revision: value.Revision, State: string(value.State), ModelRegistryID: value.ModelRegistryID.String(), ModelRegistryVersion: value.ModelRegistryVersion, PromptRegistryID: value.PromptRegistryID.String(), PromptRegistryVersion: value.PromptRegistryVersion, ModelID: value.ModelID, ModelVersion: value.ModelVersion, PromptVersion: value.PromptVersion, Action: value.Action, SourceRevision: value.SourceRevision, Reason: value.Reason, ActorID: value.ActorID, OccurredAt: value.OccurredAt.Format(time.RFC3339Nano)}
}

func (routes *ProposalRoutes) writeActivation(writer http.ResponseWriter, request *http.Request, value proposal.GenerationActivation) {
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, http.StatusOK, activationProjection(value)); err != nil {
		routes.logger.ErrorContext(request.Context(), "write proposal activation response")
	}
}

func (routes *ProposalRoutes) getUsageReport(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	from, err := parseTime(request.URL.Query().Get("from"))
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	to, err := parseTime(request.URL.Query().Get("to"))
	if err != nil {
		routes.problem(writer, request, proposal.ErrInvalid)
		return
	}
	report, err := routes.service.GetGenerationUsageReport(request.Context(), authority.TenantScope(), from, to)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	resource := usageReportResource{From: report.From.Format(time.RFC3339Nano), To: report.To.Format(time.RFC3339Nano), Attempts: report.Attempts, Succeeded: report.Succeeded, Failed: report.Failed, Rejected: report.Rejected, InvalidOutput: report.InvalidOutput, UnreportedUsage: report.UnreportedUsage, InputTokens: report.InputTokens, OutputTokens: report.OutputTokens, EstimatedCostMicros: report.EstimatedCostMicros}
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, http.StatusOK, resource); err != nil {
		routes.logger.ErrorContext(request.Context(), "write proposal usage response")
	}
}

func (routes *ProposalRoutes) createImpact(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	body, err := decodeJSONBody[createImpactRequest](request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	service, ok := routes.service.(proposalImpactService)
	if !ok {
		routes.problem(writer, request, proposal.ErrNotFound)
		return
	}
	value, err := service.CreateImpactRecord(request.Context(), authority.TenantScope(), body.Kind, body.Assessment, body.RiskLevel, authority.Principal().KeyID().String())
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeImpact(writer, request, http.StatusCreated, value)
}

func (routes *ProposalRoutes) getImpact(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	service, ok := routes.service.(proposalImpactService)
	if !ok {
		routes.problem(writer, request, proposal.ErrNotFound)
		return
	}
	value, err := service.GetImpactRecord(request.Context(), authority.TenantScope(), chi.URLParam(request, "assessmentID"))
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	routes.writeImpact(writer, request, http.StatusOK, value)
}

func (routes *ProposalRoutes) listImpacts(writer http.ResponseWriter, request *http.Request) {
	authority, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	query := request.URL.Query()
	for key, values := range query {
		if (key != "before" && key != "limit") || len(values) != 1 {
			routes.problem(writer, request, invalidRequest(errors.New("impact assessment query is invalid")))
			return
		}
	}
	limit := 25
	if raw := query.Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			routes.problem(writer, request, invalidRequest(errors.New("impact assessment limit must be from 1 to 100")))
			return
		}
		limit = parsed
	}
	var before time.Time
	if raw := query.Get("before"); raw != "" {
		var err error
		before, err = time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			routes.problem(writer, request, invalidRequest(errors.New("impact assessment cursor must be RFC3339")))
			return
		}
	}
	service, ok := routes.service.(proposalImpactService)
	if !ok {
		routes.problem(writer, request, proposal.ErrNotFound)
		return
	}
	values, err := service.ListImpactRecords(request.Context(), authority.TenantScope(), before, limit)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	data := make([]impactResource, 0, len(values))
	for _, value := range values {
		data = append(data, impactResponse(value))
	}
	if err := respond.JSON(writer, request, http.StatusOK, struct {
		Data []impactResource `json:"data"`
	}{Data: data}); err != nil {
		routes.logger.ErrorContext(request.Context(), "write impact assessment list response")
	}
}

func (routes *ProposalRoutes) writeImpact(writer http.ResponseWriter, request *http.Request, status int, value proposal.ImpactAssessment) {
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, status, impactResponse(value)); err != nil {
		routes.logger.ErrorContext(request.Context(), "write impact assessment response")
	}
}

func impactResponse(value proposal.ImpactAssessment) impactResource {
	return impactResource{ID: value.ID, Kind: value.Kind, Assessment: value.Assessment, RiskLevel: value.RiskLevel, CreatedAt: value.CreatedAt.Format(time.RFC3339Nano), ActorID: value.ActorID}
}

func (routes *ProposalRoutes) writeProposal(writer http.ResponseWriter, request *http.Request, status int, p proposal.Proposal) {
	resource := proposalResource{
		ProposalID:     p.ID.String(),
		TenantID:       p.TenantID.String(),
		VerificationID: p.VerificationID.String(),
		Mode:           p.Mode.String(),
		Status:         p.Status.String(),
		Actions:        p.Actions,
		EvidenceRefs:   p.EvidenceRefs,
		SignalRefs:     p.SignalRefs,
		ModelID:        p.ModelID,
		ModelVersion:   p.ModelVersion,
		PromptVersion:  p.PromptVersion,
		ContextDigest:  p.ContextDigest,
		ExpiresAt:      p.ExpiresAt.Format("2006-01-02T15:04:05.000000Z"),
		CreatedAt:      p.CreatedAt.Format("2006-01-02T15:04:05.000000Z"),
		Version:        p.Version,
		Reason:         p.Reason,
	}
	if p.PolicyID != nil {
		resource.PolicyID = p.PolicyID.String()
	}
	writer.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(writer, request, status, resource); err != nil {
		routes.logger.ErrorContext(request.Context(), "write proposal response")
	}
}

func parseTime(value string) (parsed time.Time, err error) {
	parsed, err = time.Parse(time.RFC3339Nano, value)
	if err == nil {
		return parsed.UTC(), nil
	}
	parsed, err = time.Parse("2006-01-02T15:04:05.000000Z", value)
	if err == nil {
		return parsed.UTC(), nil
	}
	return time.Time{}, err
}
