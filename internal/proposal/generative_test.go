package proposal_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/proposal"
)

type generationStub struct {
	response proposal.GenerationResponse
	err      error
	request  proposal.GenerationRequest
}

func (stub *generationStub) Generate(_ context.Context, request proposal.GenerationRequest) (proposal.GenerationResponse, error) {
	stub.request = request
	return stub.response, stub.err
}

func TestGenerativeModelPropose(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	stub := &generationStub{response: proposal.GenerationResponse{
		Model: "model-2026-09", RequestID: "provider-request-1", InputTokens: 20, OutputTokens: 8, EstimatedCostMicros: 12, UsageReported: true,
		Output: json.RawMessage(`{"actions":[{"kind":"review.copilot.summarize","args":{"summary":"bounded review summary"}}],"reason":"assist reviewer"}`),
	}}
	usage := &proposal.InMemoryGenerationUsageRecorder{}
	model := newGenerativeModel(t, stub, now, usage)
	request := generationProposalRequest(now)
	result, err := model.Propose(t.Context(), request)
	if err != nil {
		t.Fatalf("Propose() error = %v", err)
	}
	if result.TenantID != request.TenantID || result.VerificationID != request.VerificationID || result.ModelVersion != request.ModelVersion {
		t.Fatalf("Propose() changed authoritative request fields: %+v", result)
	}
	if len(result.Actions) != 1 || string(result.Actions[0].Args) != `{"summary":"bounded review summary"}` {
		t.Fatalf("Propose() actions = %s", result.Actions[0].Args)
	}
	if stub.request.ModelID != request.ModelID || stub.request.Model != request.ModelVersion || stub.request.PromptVersion != request.PromptVersion || len(stub.request.Schema) == 0 {
		t.Fatalf("Generate() request = %+v", stub.request)
	}
	if len(usage.Receipts) != 1 || usage.Receipts[0].ProviderRequestID != "provider-request-1" || usage.Receipts[0].EstimatedCostMicros != 12 {
		t.Fatalf("usage receipts = %+v", usage.Receipts)
	}
}

func TestGenerativeModelRejectsChangedOrMalformedOutput(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		model    string
		output   string
		upstream error
	}{
		{name: "changed model", model: "other-model", output: `{"actions":[],"reason":""}`},
		{name: "added action", model: "model-2026-09", output: `{"actions":[{"kind":"routing.adaptive.suggest","args":{"route":"other"}}],"reason":""}`},
		{name: "unknown argument", model: "model-2026-09", output: `{"actions":[{"kind":"review.copilot.summarize","args":{"raw_evidence":"bad"}}],"reason":""}`},
		{name: "duplicate field", model: "model-2026-09", output: `{"actions":[],"actions":[],"reason":""}`},
		{name: "upstream error", model: "model-2026-09", upstream: proposal.ErrModelUnavailable},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stub := &generationStub{response: proposal.GenerationResponse{Model: test.model, Output: json.RawMessage(test.output)}, err: test.upstream}
			model := newGenerativeModel(t, stub, now)
			_, err := model.Propose(t.Context(), generationProposalRequest(now))
			if err == nil {
				t.Fatal("Propose() error = nil")
			}
		})
	}
}

func TestGenerativeModelFailsClosedWhenUsageReceiptCannotPersist(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	stub := &generationStub{response: proposal.GenerationResponse{
		Model:  "model-2026-09",
		Output: json.RawMessage(`{"actions":[{"kind":"review.copilot.summarize","args":{"summary":"bounded"}}],"reason":"assist"}`),
	}}
	recorder := &proposal.InMemoryGenerationUsageRecorder{Err: errors.New("storage unavailable")}
	model := newGenerativeModel(t, stub, now, recorder)
	if _, err := model.Propose(t.Context(), generationProposalRequest(now)); err == nil {
		t.Fatal("Propose() error = nil")
	}
}

func TestGenerativeModelRecordsFailedAndInvalidOutputAttempts(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name          string
		response      proposal.GenerationResponse
		upstream      error
		wantOutcome   proposal.GenerationOutcome
		wantReported  bool
		wantInput     int64
		wantOutput    int64
		wantErrorCode string
	}{
		{
			name: "provider failure without usage", upstream: proposal.ErrModelUnavailable,
			wantOutcome: proposal.GenerationOutcomeFailed, wantErrorCode: "model_unavailable",
		},
		{
			name: "invalid provider output with usage",
			response: proposal.GenerationResponse{
				Model: "model-2026-09", Output: json.RawMessage(`{"actions":[]}`),
				InputTokens: 21, OutputTokens: 4, UsageReported: true,
			},
			wantOutcome: proposal.GenerationOutcomeInvalidOutput, wantReported: true,
			wantInput: 21, wantOutput: 4, wantErrorCode: "invalid_output",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			recorder := &proposal.InMemoryGenerationUsageRecorder{}
			model := newGenerativeModel(t, &generationStub{response: test.response, err: test.upstream}, now, recorder)
			if _, err := model.Propose(t.Context(), generationProposalRequest(now)); err == nil {
				t.Fatal("Propose() error = nil")
			}
			if len(recorder.Receipts) != 1 {
				t.Fatalf("receipt count = %d, want 1", len(recorder.Receipts))
			}
			receipt := recorder.Receipts[0]
			if receipt.Outcome != test.wantOutcome || receipt.UsageReported != test.wantReported ||
				receipt.InputTokens != test.wantInput || receipt.OutputTokens != test.wantOutput || receipt.ErrorCode != test.wantErrorCode {
				t.Fatalf("receipt = %+v", receipt)
			}
		})
	}
}

func TestGenerationUsageReportAggregatesOutcomes(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	tenantID := testScope(t).ID()
	proposalID, err := id.ParseProposal("prp_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	recorder := &proposal.InMemoryGenerationUsageRecorder{}
	for index, outcome := range []proposal.GenerationOutcome{proposal.GenerationOutcomeSucceeded, proposal.GenerationOutcomeFailed, proposal.GenerationOutcomeRejected, proposal.GenerationOutcomeInvalidOutput} {
		usage := proposal.GenerationUsage{
			TenantID: tenantID, ProposalID: proposalID, ModelID: "ai.review", ModelVersion: "v1", PromptVersion: "p1",
			InputTokens: int64(index + 1), OutputTokens: 2, EstimatedCostMicros: 3,
			Outcome: outcome, UsageReported: index != 1, RecordedAt: now.Add(time.Duration(index) * time.Minute),
		}
		if err := recorder.RecordGenerationUsage(t.Context(), usage); err != nil {
			t.Fatal(err)
		}
	}
	report, err := recorder.GetGenerationUsageReport(t.Context(), tenantID, now, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("GetGenerationUsageReport() error = %v", err)
	}
	if report.Attempts != 4 || report.Succeeded != 1 || report.Failed != 1 || report.Rejected != 1 ||
		report.InvalidOutput != 1 || report.UnreportedUsage != 1 || report.InputTokens != 10 ||
		report.OutputTokens != 8 || report.EstimatedCostMicros != 12 {
		t.Fatalf("report = %+v", report)
	}
}

func TestGeneratorRouterUsesExactRoute(t *testing.T) {
	t.Parallel()
	first := &generationStub{response: proposal.GenerationResponse{Model: "v1"}}
	first.response.InputTokens = 1250
	first.response.OutputTokens = 500
	router, err := proposal.NewGeneratorRouter([]proposal.GenerationRoute{{
		ModelID: "ai.review", ModelVersion: "v1", PromptVersion: "p1", Instructions: "Return bounded output.", Generator: first,
		InputMicrosPerMillion: 2000, OutputMicrosPerMillion: 8000,
	}})
	if err != nil {
		t.Fatalf("NewGeneratorRouter() error = %v", err)
	}
	result, err := router.Generate(t.Context(), proposal.GenerationRequest{ModelID: "ai.review", Model: "v1", PromptVersion: "p1"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if first.request.Instructions != "Return bounded output." || result.EstimatedCostMicros != 7 {
		t.Fatalf("Generate() = request instructions %q, cost %d", first.request.Instructions, result.EstimatedCostMicros)
	}
	if _, err := router.Generate(t.Context(), proposal.GenerationRequest{ModelID: "ai.review", Model: "v2", PromptVersion: "p1"}); !errors.Is(err, proposal.ErrModelUnavailable) {
		t.Fatalf("Generate() error = %v, want unavailable", err)
	}
	if _, err := router.Generate(t.Context(), proposal.GenerationRequest{ModelID: "ai.review", Model: "v1", PromptVersion: "p2"}); !errors.Is(err, proposal.ErrModelUnavailable) {
		t.Fatalf("Generate() error = %v, want unavailable for changed prompt", err)
	}
}

func newGenerativeModel(t *testing.T, generator proposal.Generator, now time.Time, recorders ...proposal.GenerationUsageRecorder) *proposal.GenerativeModel {
	t.Helper()
	clk := testClock{now: now}
	ids, err := id.NewGenerator(clk, &deterministicReader{})
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	var recorder proposal.GenerationUsageRecorder
	if len(recorders) > 0 {
		recorder = recorders[0]
	}
	model, err := proposal.NewGenerativeModel(proposal.GenerativeModelConfig{
		Generator: generator, GeneratorIDs: ids, Clock: clk, UsageRecorder: recorder,
	})
	if err != nil {
		t.Fatalf("NewGenerativeModel() error = %v", err)
	}
	return model
}

func generationProposalRequest(now time.Time) proposalv1.ProposalRequest {
	return proposalv1.ProposalRequest{
		TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWK", VerificationID: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWK",
		Mode:    proposalv1.AutomationModeAssist,
		Actions: []proposalv1.BoundedAction{{Kind: proposalv1.ActionReviewCopilotSummarize, Args: json.RawMessage(`{}`)}},
		ModelID: "ai.review", ModelVersion: "model-2026-09", PromptVersion: "prompt-1",
		ContextDigest: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		ExpiresAt:     now.Add(time.Hour),
	}
}
