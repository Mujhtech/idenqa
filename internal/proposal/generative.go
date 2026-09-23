package proposal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"unicode/utf8"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	defaultGenerationTokens = 2048
	maxGenerationTokens     = 8192
	maxGenerationInputBytes = 64 * 1024
	maxReportedTokens       = 1_000_000_000
	maxMicrosPerMillion     = 1_000_000_000
)

var (
	// ErrModelUnavailable identifies a selected generative-model route or
	// upstream that cannot currently serve the request.
	ErrModelUnavailable = errors.New("proposal: model unavailable")
	// ErrModelRejected identifies an upstream refusal that must not be retried
	// as if it were a transient transport failure.
	ErrModelRejected = errors.New("proposal: model request rejected")
	// ErrModelOutput identifies malformed, unbounded, or semantically changed
	// provider output.
	ErrModelOutput = errors.New("proposal: invalid model output")
)

// GenerationRequest is the provider-neutral, bounded request consumed by a
// generative-model adapter. Input contains redacted context and references,
// never raw evidence or credentials.
type GenerationRequest struct {
	ModelID         string
	Model           string
	PromptVersion   string
	Instructions    string
	Input           json.RawMessage
	Schema          json.RawMessage
	MaxOutputTokens int
}

// GenerationResponse is the normalized result returned by every provider
// adapter. Output must satisfy the exact schema supplied in the request.
type GenerationResponse struct {
	Output        json.RawMessage
	Model         string
	RequestID     string
	InputTokens   int64
	OutputTokens  int64
	UsageReported bool
	// EstimatedCostMicros is calculated locally from the pinned route rates.
	EstimatedCostMicros int64
}

// Generator is the consuming-side port implemented by provider protocol
// adapters. It has no authority to persist proposals or execute actions.
type Generator interface {
	Generate(ctx context.Context, request GenerationRequest) (GenerationResponse, error)
}

// ValidateGenerationRequest checks the common adapter input contract.
func ValidateGenerationRequest(request GenerationRequest) error {
	if request.ModelID == "" || request.Model == "" || request.PromptVersion == "" || request.Instructions == "" ||
		len(request.Instructions) > 16*1024 || !utf8.ValidString(request.Instructions) ||
		len(request.Input) == 0 || len(request.Input) > maxGenerationInputBytes || !json.Valid(request.Input) ||
		len(request.Schema) == 0 || len(request.Schema) > maxGenerationInputBytes || !json.Valid(request.Schema) ||
		request.MaxOutputTokens < 1 || request.MaxOutputTokens > maxGenerationTokens {
		return ErrInvalid
	}
	return nil
}

// GenerationRoute binds one logical registry model and exact upstream model
// version to one protocol adapter.
type GenerationRoute struct {
	ModelID                string
	ModelVersion           string
	PromptVersion          string
	Instructions           string
	Generator              Generator
	MaxOutputTokens        int
	InputMicrosPerMillion  int64
	OutputMicrosPerMillion int64
}

// GeneratorRouter selects an exact immutable route. It never falls back to a
// different model or provider because that would change proposal provenance.
type GeneratorRouter struct {
	routes map[string]GenerationRoute
}

// NewGeneratorRouter validates and snapshots a closed route set.
func NewGeneratorRouter(routes []GenerationRoute) (*GeneratorRouter, error) {
	if len(routes) == 0 || len(routes) > 32 {
		return nil, fmt.Errorf("%w: generation routes", ErrInvalid)
	}
	result := &GeneratorRouter{routes: make(map[string]GenerationRoute, len(routes))}
	for _, route := range routes {
		if route.ModelID == "" || route.ModelVersion == "" || route.PromptVersion == "" || route.Generator == nil ||
			route.Instructions == "" || len(route.Instructions) > 16*1024 || !utf8.ValidString(route.Instructions) ||
			route.MaxOutputTokens < 0 || route.MaxOutputTokens > maxGenerationTokens ||
			route.InputMicrosPerMillion < 0 || route.InputMicrosPerMillion > maxMicrosPerMillion ||
			route.OutputMicrosPerMillion < 0 || route.OutputMicrosPerMillion > maxMicrosPerMillion {
			return nil, fmt.Errorf("%w: generation route", ErrInvalid)
		}
		key := generationRouteKey(route.ModelID, route.ModelVersion, route.PromptVersion)
		if _, exists := result.routes[key]; exists {
			return nil, fmt.Errorf("%w: duplicate generation route", ErrConflict)
		}
		result.routes[key] = route
	}
	return result, nil
}

// Generate dispatches only to the exact configured model route.
func (router *GeneratorRouter) Generate(ctx context.Context, request GenerationRequest) (GenerationResponse, error) {
	if router == nil {
		return GenerationResponse{}, ErrModelUnavailable
	}
	route, ok := router.routes[generationRouteKey(request.ModelID, request.Model, request.PromptVersion)]
	if !ok {
		return GenerationResponse{}, ErrModelUnavailable
	}
	if route.MaxOutputTokens > 0 {
		request.MaxOutputTokens = route.MaxOutputTokens
	}
	request.Instructions = route.Instructions
	response, err := route.Generator.Generate(ctx, request)
	if response.InputTokens < 0 || response.InputTokens > maxReportedTokens ||
		response.OutputTokens < 0 || response.OutputTokens > maxReportedTokens {
		return GenerationResponse{}, ErrModelOutput
	}
	response.EstimatedCostMicros = estimatedCostMicros(
		response.InputTokens,
		response.OutputTokens,
		route.InputMicrosPerMillion,
		route.OutputMicrosPerMillion,
	)
	return response, err
}

func generationRouteKey(modelID, modelVersion, promptVersion string) string {
	return modelID + "\x00" + modelVersion + "\x00" + promptVersion
}

func estimatedCostMicros(inputTokens, outputTokens, inputRate, outputRate int64) int64 {
	total := inputTokens*inputRate + outputTokens*outputRate
	if total == 0 {
		return 0
	}
	return (total + 999_999) / 1_000_000
}

// GenerativeModelConfig contains explicit dependencies for the model-agnostic
// ProposalModel implementation.
type GenerativeModelConfig struct {
	Generator       Generator
	GeneratorIDs    *id.Generator
	Clock           clock.Clock
	UsageRecorder   GenerationUsageRecorder
	MaxOutputTokens int
}

// GenerativeModel turns untrusted provider output into the existing bounded
// proposal envelope. All identifiers and provenance are authored locally from
// the validated request, never copied from provider output.
type GenerativeModel struct {
	generator       Generator
	ids             *id.Generator
	clock           clock.Clock
	usage           GenerationUsageRecorder
	maxOutputTokens int
}

// NewGenerativeModel constructs the provider-neutral proposal model.
func NewGenerativeModel(config GenerativeModelConfig) (*GenerativeModel, error) {
	if config.Generator == nil || config.GeneratorIDs == nil || config.Clock == nil {
		return nil, errors.New("proposal generative model: generator, ids, and clock are required")
	}
	tokens := config.MaxOutputTokens
	if tokens == 0 {
		tokens = defaultGenerationTokens
	}
	if tokens < 1 || tokens > maxGenerationTokens {
		return nil, errors.New("proposal generative model: output token limit is invalid")
	}
	return &GenerativeModel{
		generator: config.Generator, ids: config.GeneratorIDs, clock: config.Clock,
		usage: config.UsageRecorder, maxOutputTokens: tokens,
	}, nil
}

type generationInput struct {
	ContextDigest string                     `json:"context_digest"`
	Actions       []proposalv1.BoundedAction `json:"actions"`
	EvidenceRefs  []string                   `json:"evidence_refs,omitempty"`
	SignalRefs    []string                   `json:"signal_refs,omitempty"`
}

type generatedProposal struct {
	Actions []proposalv1.BoundedAction `json:"actions"`
	Reason  string                     `json:"reason,omitempty"`
}

// Propose invokes one exact adapter route and rebuilds an authoritative local
// envelope around its schema-constrained action arguments.
func (model *GenerativeModel) Propose(ctx context.Context, request proposalv1.ProposalRequest) (proposalv1.AgentProposal, error) {
	if err := proposalv1.ValidateProposalRequest(request); err != nil {
		return proposalv1.AgentProposal{}, fmt.Errorf("%w: validation: %w", ErrInvalid, err)
	}
	input, err := json.Marshal(generationInput{
		ContextDigest: request.ContextDigest,
		Actions:       request.Actions,
		EvidenceRefs:  request.EvidenceRefs,
		SignalRefs:    request.SignalRefs,
	})
	if err != nil || len(input) > maxGenerationInputBytes {
		return proposalv1.AgentProposal{}, ErrInvalid
	}
	schema, err := generationSchema(request.Actions)
	if err != nil {
		return proposalv1.AgentProposal{}, err
	}
	proposalID, err := model.ids.NewProposal()
	if err != nil {
		return proposalv1.AgentProposal{}, err
	}
	response, err := model.generator.Generate(ctx, GenerationRequest{
		ModelID: request.ModelID, Model: request.ModelVersion, PromptVersion: request.PromptVersion,
		Input: input, Schema: schema,
		MaxOutputTokens: model.maxOutputTokens,
	})
	if err != nil {
		outcome, code := GenerationOutcomeFailed, "model_unavailable"
		switch {
		case errors.Is(err, ErrModelOutput):
			outcome, code = GenerationOutcomeInvalidOutput, "invalid_output"
		case errors.Is(err, ErrRateLimited):
			outcome, code = GenerationOutcomeRejected, "rate_limited"
		case errors.Is(err, ErrModelRejected):
			outcome, code = GenerationOutcomeRejected, "model_rejected"
		}
		if recordErr := model.recordUsage(ctx, request, proposalID, response, outcome, code); recordErr != nil {
			return proposalv1.AgentProposal{}, errors.Join(fmt.Errorf("generate proposal: %w", err), recordErr)
		}
		return proposalv1.AgentProposal{}, fmt.Errorf("generate proposal: %w", err)
	}
	if response.Model != request.ModelVersion {
		if err := model.recordUsage(ctx, request, proposalID, response, GenerationOutcomeInvalidOutput, "model_version_changed"); err != nil {
			return proposalv1.AgentProposal{}, err
		}
		return proposalv1.AgentProposal{}, fmt.Errorf("%w: model version changed", ErrModelOutput)
	}
	generated, err := parseGeneratedProposal(response.Output, request.Actions)
	if err != nil {
		if recordErr := model.recordUsage(ctx, request, proposalID, response, GenerationOutcomeInvalidOutput, "invalid_output"); recordErr != nil {
			return proposalv1.AgentProposal{}, errors.Join(err, recordErr)
		}
		return proposalv1.AgentProposal{}, err
	}
	now := model.clock.Now().UTC()
	if err := model.recordUsage(ctx, request, proposalID, response, GenerationOutcomeSucceeded, ""); err != nil {
		return proposalv1.AgentProposal{}, err
	}
	return proposalv1.AgentProposal{
		ProposalID: proposalID.String(), TenantID: request.TenantID,
		VerificationID: request.VerificationID, PolicyID: request.PolicyID,
		Mode: request.Mode.String(), Status: proposalv1.ProposalStatusPending.String(),
		Actions: generated.Actions, EvidenceRefs: request.EvidenceRefs, SignalRefs: request.SignalRefs,
		ModelID: request.ModelID, ModelVersion: request.ModelVersion, PromptVersion: request.PromptVersion,
		ContextDigest: request.ContextDigest, ExpiresAt: request.ExpiresAt,
		Supersedes: request.Supersedes, CreatedAt: now, Reason: generated.Reason,
	}, nil
}

func (model *GenerativeModel) recordUsage(ctx context.Context, request proposalv1.ProposalRequest, proposalID id.Proposal, response GenerationResponse, outcome GenerationOutcome, errorCode string) error {
	if model.usage == nil {
		return nil
	}
	tenantID, err := id.ParseTenant(request.TenantID)
	if err != nil {
		return ErrInvalid
	}
	usage := GenerationUsage{
		TenantID: tenantID, ProposalID: proposalID,
		ModelID: request.ModelID, ModelVersion: request.ModelVersion, PromptVersion: request.PromptVersion,
		ProviderRequestID: response.RequestID, InputTokens: response.InputTokens, OutputTokens: response.OutputTokens,
		EstimatedCostMicros: response.EstimatedCostMicros, Outcome: outcome, UsageReported: response.UsageReported,
		ErrorCode: errorCode, RecordedAt: model.clock.Now().UTC(),
	}
	if err := model.usage.RecordGenerationUsage(ctx, usage); err != nil {
		return fmt.Errorf("record generation usage: %w", err)
	}
	return nil
}

func generationSchema(actions []proposalv1.BoundedAction) (json.RawMessage, error) {
	variants := make([]any, 0, len(actions))
	for _, action := range actions {
		key, ok := generatedArgumentKey(action.Kind)
		if !ok {
			return nil, ErrUnknownKind
		}
		variants = append(variants, map[string]any{
			"type": "object", "additionalProperties": false,
			"required": []string{"kind", "args"},
			"properties": map[string]any{
				"kind": map[string]any{"type": "string", "const": string(action.Kind)},
				"args": map[string]any{
					"type": "object", "additionalProperties": false, "required": []string{key},
					"properties": map[string]any{key: map[string]any{"type": "string"}},
				},
			},
		})
	}
	schema := map[string]any{
		"type": "object", "additionalProperties": false,
		"required": []string{"actions", "reason"},
		"properties": map[string]any{
			"actions": map[string]any{"type": "array", "items": map[string]any{"anyOf": variants}},
			"reason":  map[string]any{"type": "string"},
		},
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, fmt.Errorf("encode generation schema: %w", err)
	}
	return encoded, nil
}

func parseGeneratedProposal(raw []byte, requested []proposalv1.BoundedAction) (generatedProposal, error) {
	if len(raw) == 0 || len(raw) > proposalv1.MaxProposalBytes || !utf8.Valid(raw) {
		return generatedProposal{}, ErrModelOutput
	}
	if err := rejectDuplicateJSON(raw); err != nil {
		return generatedProposal{}, ErrModelOutput
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var generated generatedProposal
	if err := decoder.Decode(&generated); err != nil {
		return generatedProposal{}, ErrModelOutput
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return generatedProposal{}, ErrModelOutput
	}
	if len(generated.Reason) > proposalv1.MaxReasonBytes || !utf8.ValidString(generated.Reason) || len(generated.Actions) != len(requested) {
		return generatedProposal{}, ErrModelOutput
	}
	expected := make(map[proposalv1.ActionKind]struct{}, len(requested))
	for _, action := range requested {
		expected[action.Kind] = struct{}{}
	}
	for _, action := range generated.Actions {
		if _, ok := expected[action.Kind]; !ok {
			return generatedProposal{}, ErrModelOutput
		}
		delete(expected, action.Kind)
		if err := validateGeneratedArgs(action.Kind, action.Args); err != nil {
			return generatedProposal{}, ErrModelOutput
		}
	}
	if len(expected) != 0 {
		return generatedProposal{}, ErrModelOutput
	}
	return generated, nil
}

func validateGeneratedArgs(kind proposalv1.ActionKind, raw []byte) error {
	if len(raw) == 0 || len(raw) > proposalv1.MaxArgBytes || !json.Valid(raw) {
		return ErrModelOutput
	}
	key, ok := generatedArgumentKey(kind)
	if !ok {
		return ErrUnknownKind
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	value := map[string]string{}
	if err := decoder.Decode(&value); err != nil || len(value) != 1 || value[key] == "" || !utf8.ValidString(value[key]) {
		return ErrModelOutput
	}
	return nil
}

func generatedArgumentKey(kind proposalv1.ActionKind) (string, bool) {
	switch kind {
	case proposalv1.ActionReviewCopilotSummarize:
		return "summary", true
	case proposalv1.ActionRoutingAdaptiveSuggest:
		return "route", true
	case proposalv1.ActionPolicyDraftGenerate:
		return "draft", true
	case proposalv1.ActionPolicyDiffGenerate:
		return "diff", true
	case proposalv1.ActionPolicyAdversarialGenerate:
		return "scenarios", true
	case proposalv1.ActionExperienceAccessibilityPropose:
		return "accessibility", true
	case proposalv1.ActionExperienceExceptionPropose:
		return "exception", true
	case proposalv1.ActionDocumentLayoutPropose:
		return "layout", true
	default:
		return "", false
	}
}

func rejectDuplicateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var visit func() error
	visit = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]struct{}{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return ErrModelOutput
				}
				if _, duplicate := seen[key]; duplicate {
					return ErrModelOutput
				}
				seen[key] = struct{}{}
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		case '[':
			for decoder.More() {
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		default:
			return ErrModelOutput
		}
	}
	return visit()
}

var _ proposalv1.ProposalModel = (*GenerativeModel)(nil)
var _ Generator = (*GeneratorRouter)(nil)
