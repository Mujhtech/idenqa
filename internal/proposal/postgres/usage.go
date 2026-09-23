package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/proposal"
	"github.com/jackc/pgx/v5"
)

// RecordGenerationUsage durably stores one content-free generation receipt.
func (store *Store) RecordGenerationUsage(ctx context.Context, usage proposal.GenerationUsage) error {
	if store == nil {
		return proposal.ErrInvalid
	}
	if err := usage.Validate(); err != nil {
		return err
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, usage.TenantID.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.proposal_generation_usage
(tenant_id, proposal_id, model_id, model_version, prompt_version, provider_request_id, input_tokens, output_tokens, estimated_cost_micros, outcome, usage_reported, error_code, recorded_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)`,
			usage.TenantID.String(), usage.ProposalID.String(), usage.ModelID, usage.ModelVersion,
			usage.PromptVersion, usage.ProviderRequestID, usage.InputTokens, usage.OutputTokens,
			usage.EstimatedCostMicros, string(usage.Outcome), usage.UsageReported, usage.ErrorCode, usage.RecordedAt)
		if err != nil {
			return fmt.Errorf("insert proposal generation usage: %w", err)
		}
		return nil
	})
}

// GetGenerationUsage retrieves one receipt under tenant scope.
func (store *Store) GetGenerationUsage(ctx context.Context, tenantID id.Tenant, proposalID id.Proposal) (proposal.GenerationUsage, error) {
	if store == nil || tenantID.IsZero() || proposalID.IsZero() {
		return proposal.GenerationUsage{}, proposal.ErrInvalid
	}
	var usage proposal.GenerationUsage
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return err
		}
		var recordedAt time.Time
		var outcome string
		err := tx.QueryRow(ctx, `SELECT model_id, model_version, prompt_version, provider_request_id, input_tokens, output_tokens, estimated_cost_micros, outcome, usage_reported, error_code, recorded_at
FROM idenqa.proposal_generation_usage WHERE tenant_id=$1 AND proposal_id=$2`, tenantID.String(), proposalID.String()).Scan(
			&usage.ModelID, &usage.ModelVersion, &usage.PromptVersion, &usage.ProviderRequestID,
			&usage.InputTokens, &usage.OutputTokens, &usage.EstimatedCostMicros, &outcome, &usage.UsageReported, &usage.ErrorCode, &recordedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return proposal.ErrNotFound
		}
		if err != nil {
			return err
		}
		usage.TenantID = tenantID
		usage.ProposalID = proposalID
		usage.Outcome = proposal.GenerationOutcome(outcome)
		usage.RecordedAt = recordedAt.UTC()
		return usage.Validate()
	})
	if err != nil {
		return proposal.GenerationUsage{}, err
	}
	return usage, nil
}

// GetGenerationUsageReport aggregates tenant receipts in [from,to).
func (store *Store) GetGenerationUsageReport(ctx context.Context, tenantID id.Tenant, from, to time.Time) (proposal.GenerationUsageReport, error) {
	if store == nil || tenantID.IsZero() || from.IsZero() || !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		return proposal.GenerationUsageReport{}, proposal.ErrInvalid
	}
	report := proposal.GenerationUsageReport{From: from.UTC(), To: to.UTC()}
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return err
		}
		return tx.QueryRow(ctx, `SELECT
count(*),count(*) FILTER (WHERE outcome='succeeded'),count(*) FILTER (WHERE outcome='failed'),
count(*) FILTER (WHERE outcome='rejected'),count(*) FILTER (WHERE outcome='invalid_output'),
count(*) FILTER (WHERE usage_reported=false),coalesce(sum(input_tokens),0),coalesce(sum(output_tokens),0),coalesce(sum(estimated_cost_micros),0)
FROM idenqa.proposal_generation_usage WHERE tenant_id=$1 AND recorded_at >= $2 AND recorded_at < $3`, tenantID.String(), from.UTC(), to.UTC()).Scan(
			&report.Attempts, &report.Succeeded, &report.Failed, &report.Rejected, &report.InvalidOutput,
			&report.UnreportedUsage, &report.InputTokens, &report.OutputTokens, &report.EstimatedCostMicros)
	})
	return report, err
}

var _ proposal.GenerationUsageRecorder = (*Store)(nil)
var _ proposal.GenerationUsageStore = (*Store)(nil)
