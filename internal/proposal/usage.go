package proposal

import (
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const maxProviderRequestIDBytes = 256

// GenerationOutcome classifies one provider invocation without provider text.
type GenerationOutcome string

const (
	// GenerationOutcomeSucceeded means a valid bounded proposal was produced.
	GenerationOutcomeSucceeded GenerationOutcome = "succeeded"
	// GenerationOutcomeFailed means the provider invocation failed.
	GenerationOutcomeFailed GenerationOutcome = "failed"
	// GenerationOutcomeRejected means the provider or a local rate limit rejected the invocation.
	GenerationOutcomeRejected GenerationOutcome = "rejected"
	// GenerationOutcomeInvalidOutput means provider output failed local validation.
	GenerationOutcomeInvalidOutput GenerationOutcome = "invalid_output"
)

// GenerationUsage is one provider-neutral, content-free accounting receipt.
// It contains no prompt, response, evidence, credential, or subject data.
type GenerationUsage struct {
	TenantID            id.Tenant
	ProposalID          id.Proposal
	ModelID             string
	ModelVersion        string
	PromptVersion       string
	ProviderRequestID   string
	InputTokens         int64
	OutputTokens        int64
	EstimatedCostMicros int64
	Outcome             GenerationOutcome
	UsageReported       bool
	ErrorCode           string
	RecordedAt          time.Time
}

// Validate checks the bounded accounting receipt.
func (usage GenerationUsage) Validate() error {
	if usage.TenantID.IsZero() || usage.ProposalID.IsZero() || usage.ModelID == "" ||
		usage.ModelVersion == "" || usage.PromptVersion == "" || usage.RecordedAt.IsZero() ||
		usage.InputTokens < 0 || usage.InputTokens > maxReportedTokens ||
		usage.OutputTokens < 0 || usage.OutputTokens > maxReportedTokens ||
		usage.EstimatedCostMicros < 0 || !validGenerationOutcome(usage.Outcome) || len(usage.ErrorCode) > 64 ||
		!utf8.ValidString(usage.ErrorCode) || strings.ContainsAny(usage.ErrorCode, "\r\n\x00") ||
		len(usage.ProviderRequestID) > maxProviderRequestIDBytes ||
		!utf8.ValidString(usage.ProviderRequestID) || strings.ContainsAny(usage.ProviderRequestID, "\r\n\x00") {
		return ErrInvalid
	}
	return nil
}

func validGenerationOutcome(outcome GenerationOutcome) bool {
	return outcome == GenerationOutcomeSucceeded || outcome == GenerationOutcomeFailed ||
		outcome == GenerationOutcomeRejected || outcome == GenerationOutcomeInvalidOutput
}

// GenerationUsageReport is a bounded aggregate over content-free receipts.
type GenerationUsageReport struct {
	From                time.Time
	To                  time.Time
	Attempts            int64
	Succeeded           int64
	Failed              int64
	Rejected            int64
	InvalidOutput       int64
	UnreportedUsage     int64
	InputTokens         int64
	OutputTokens        int64
	EstimatedCostMicros int64
}

// GenerationUsageRecorder durably records successful external generation
// accounting before the proposal is returned to the application service.
type GenerationUsageRecorder interface {
	RecordGenerationUsage(context.Context, GenerationUsage) error
}

// GenerationUsageStore records and reports tenant-scoped generation usage.
type GenerationUsageStore interface {
	GenerationUsageRecorder
	GetGenerationUsageReport(context.Context, id.Tenant, time.Time, time.Time) (GenerationUsageReport, error)
}

// InMemoryGenerationUsageRecorder is a deterministic test recorder.
type InMemoryGenerationUsageRecorder struct {
	Receipts []GenerationUsage
	Err      error
}

// RecordGenerationUsage validates and appends one receipt.
func (recorder *InMemoryGenerationUsageRecorder) RecordGenerationUsage(_ context.Context, usage GenerationUsage) error {
	if recorder == nil {
		return ErrInvalid
	}
	if err := usage.Validate(); err != nil {
		return err
	}
	if recorder.Err != nil {
		return recorder.Err
	}
	recorder.Receipts = append(recorder.Receipts, usage)
	return nil
}

// GetGenerationUsageReport aggregates receipts in [from,to).
func (recorder *InMemoryGenerationUsageRecorder) GetGenerationUsageReport(_ context.Context, tenantID id.Tenant, from, to time.Time) (GenerationUsageReport, error) {
	if recorder == nil || tenantID.IsZero() || from.IsZero() || !to.After(from) {
		return GenerationUsageReport{}, ErrInvalid
	}
	report := GenerationUsageReport{From: from.UTC(), To: to.UTC()}
	for _, usage := range recorder.Receipts {
		if usage.TenantID != tenantID || usage.RecordedAt.Before(from) || !usage.RecordedAt.Before(to) {
			continue
		}
		report.Attempts++
		switch usage.Outcome {
		case GenerationOutcomeSucceeded:
			report.Succeeded++
		case GenerationOutcomeFailed:
			report.Failed++
		case GenerationOutcomeRejected:
			report.Rejected++
		case GenerationOutcomeInvalidOutput:
			report.InvalidOutput++
		}
		if !usage.UsageReported {
			report.UnreportedUsage++
		}
		report.InputTokens += usage.InputTokens
		report.OutputTokens += usage.OutputTokens
		report.EstimatedCostMicros += usage.EstimatedCostMicros
	}
	return report, nil
}

var _ GenerationUsageRecorder = (*InMemoryGenerationUsageRecorder)(nil)
var _ GenerationUsageStore = (*InMemoryGenerationUsageRecorder)(nil)
