package httpapi

import (
	"context"
	"encoding/json"
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
	CreateProposal(ctx context.Context, scope tenant.Scope, request proposalv1.ProposalRequest, actorID string) (proposal.Proposal, error)
	GetProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal) (proposal.Proposal, error)
	ApproveProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, actorID string, humanApproved bool) (proposal.Proposal, []proposal.AcceptedCommandRecord, error)
	RejectProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, actorID string, reason string) (proposal.Proposal, error)
	CancelProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal, expectedVersion int64, actorID string) (proposal.Proposal, error)
	ExecuteCommand(ctx context.Context, scope tenant.Scope, commandID id.AcceptedCommand, executor proposalv1.CommandExecutor) error
	PutModeConfig(ctx context.Context, scope tenant.Scope, workflow string, mode proposalv1.AutomationMode, allowedKinds []proposalv1.ActionKind, version int64, actorID string) (proposal.ModeConfig, error)
	GetModeConfig(ctx context.Context, scope tenant.Scope, workflow string) (proposal.ModeConfig, error)
	CreatePromptRecord(ctx context.Context, scope tenant.Scope, content, modelID, actorID string) (proposal.PromptRecord, error)
	GetPromptRecord(ctx context.Context, scope tenant.Scope, promptID id.Prompt, version int64) (proposal.PromptRecord, error)
}

// ProposalRoutes exposes proposal lifecycle via HTTP.
type ProposalRoutes struct {
	access  *AccessMiddleware
	service ProposalService
	logger  *slog.Logger
}

// NewProposalRoutes constructs the proposal HTTP boundary.
func NewProposalRoutes(accessMiddleware *AccessMiddleware, service ProposalService, logger *slog.Logger) (*ProposalRoutes, error) {
	if accessMiddleware == nil || service == nil || logger == nil {
		return nil, errors.New("proposal route dependencies are required")
	}
	return &ProposalRoutes{access: accessMiddleware, service: service, logger: logger}, nil
}

// Register adds proposal routes.
func (routes *ProposalRoutes) Register(router chi.Router) {
	read := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionProposalsRead)}
	write := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionProposalsWrite)}
	approve := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionProposalsApprove)}
	configure := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionProposalsConfigure)}
	promptWrite := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionPromptsWrite)}
	promptRead := []func(http.Handler) http.Handler{routes.access.Authenticate, routes.access.Require(access.PermissionPromptsRead)}

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
	proposal, err := routes.service.CreateProposal(request.Context(), authority.TenantScope(), req, authority.Principal().KeyID().String())
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
	Mode            string   `json:"mode"`
	AllowedKinds    []string `json:"allowed_kinds,omitempty"`
	ExpectedVersion int64    `json:"expected_version"`
}

type modeResource struct {
	Workflow     string   `json:"workflow"`
	Mode         string   `json:"mode"`
	AllowedKinds []string `json:"allowed_kinds"`
	Version      int64    `json:"version"`
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
	config, err := routes.service.PutModeConfig(request.Context(), authority.TenantScope(), workflow, mode, allowed, body.ExpectedVersion, authority.Principal().KeyID().String())
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

func (routes *ProposalRoutes) problem(writer http.ResponseWriter, request *http.Request, err error) {
	if writeErr := respond.WriteProblem(writer, request, err, requestIDString(request.Context())); writeErr != nil {
		routes.logger.ErrorContext(request.Context(), "write proposal failure response")
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

// Ensure proposal service can be used for prompt/mode config as well via type assertion if needed.
var _ = json.Marshal
