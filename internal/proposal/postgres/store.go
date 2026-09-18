// Package postgres adapts proposal persistence to PostgreSQL with tenant
// row-level security, optimistic concurrency, and accepted-command replay.
package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/proposal"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

type transactionRunner interface {
	WithinTransaction(context.Context, platformpostgres.TransactionOptions, func(context.Context, platformpostgres.Transaction) error) error
}

// Store implements proposal persistence with tenant isolation and optimistic concurrency.
type Store struct {
	pool  transactionRunner
	clock clock.Clock
}

// New creates a PostgreSQL adapter with explicit clock.
func New(pool transactionRunner, source clock.Clock) (*Store, error) {
	if pool == nil || source == nil {
		return nil, errors.New("proposal postgres: pool and clock are required")
	}
	return &Store{pool: pool, clock: source}, nil
}

// Create persists a pending proposal with expected version 1.
func (store *Store) Create(ctx context.Context, scope tenant.Scope, p proposal.Proposal) error {
	if scope.ID().IsZero() || p.TenantID != scope.ID() {
		return proposal.ErrInvalid
	}
	if p.Version != 1 {
		return proposal.ErrConflict
	}
	if err := p.Validate(); err != nil {
		return err
	}
	actionsJSON, err := json.Marshal(p.Actions)
	if err != nil {
		return err
	}
	evidenceJSON, err := json.Marshal(p.EvidenceRefs)
	if err != nil {
		return err
	}
	signalJSON, err := json.Marshal(p.SignalRefs)
	if err != nil {
		return err
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, scope.ID().String()); err != nil {
			return err
		}
		var supersedes *string
		if p.Supersedes != nil {
			value := p.Supersedes.String()
			supersedes = &value
		}
		var verificationID, policyID *string
		if !p.VerificationID.IsZero() {
			value := p.VerificationID.String()
			verificationID = &value
		}
		if p.PolicyID != nil {
			value := p.PolicyID.String()
			policyID = &value
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.proposals
(id, tenant_id, verification_id, policy_id, mode, status, actions, evidence_refs, signal_refs, model_id, model_version, prompt_version, context_digest, expires_at, supersedes, created_at, updated_at, version, actor_id, reason)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`,
			p.ID.String(), p.TenantID.String(), verificationID, policyID, p.Mode.String(), p.Status.String(),
			string(actionsJSON), string(evidenceJSON), string(signalJSON),
			p.ModelID, p.ModelVersion, p.PromptVersion, p.ContextDigest,
			p.ExpiresAt, supersedes, p.CreatedAt, p.UpdatedAt, p.Version, p.ActorID, p.Reason)
		if err != nil {
			return fmt.Errorf("insert proposal: %w", err)
		}
		return nil
	})
}

// Get retrieves a proposal with tenant scope.
func (store *Store) Get(ctx context.Context, scope tenant.Scope, proposalID id.Proposal) (proposal.Proposal, error) {
	if scope.ID().IsZero() || proposalID.IsZero() {
		return proposal.Proposal{}, proposal.ErrInvalid
	}
	var result proposal.Proposal
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, scope.ID().String()); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `SELECT id, tenant_id, verification_id, policy_id, mode, status, actions, evidence_refs, signal_refs, model_id, model_version, prompt_version, context_digest, expires_at, supersedes, created_at, updated_at, version, actor_id, reason
FROM idenqa.proposals WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), proposalID.String())
		var idStr, tenantStr, modeStr, statusStr string
		var verificationStr, policyStr *string
		var actionsJSON, evidenceJSON, signalJSON []byte
		var modelID, modelVersion, promptVersion, contextDigest, actorID string
		var reason *string
		var supersedesStr *string
		var expiresAt, createdAt, updatedAt time.Time
		var version int64
		err := row.Scan(&idStr, &tenantStr, &verificationStr, &policyStr, &modeStr, &statusStr, &actionsJSON, &evidenceJSON, &signalJSON, &modelID, &modelVersion, &promptVersion, &contextDigest, &expiresAt, &supersedesStr, &createdAt, &updatedAt, &version, &actorID, &reason)
		if errors.Is(err, pgx.ErrNoRows) {
			return proposal.ErrNotFound
		}
		if err != nil {
			return err
		}
		parsedID, err := id.ParseProposal(idStr)
		if err != nil {
			return err
		}
		parsedTenant, err := id.ParseTenant(tenantStr)
		if err != nil {
			return err
		}
		var parsedVerification id.Verification
		if verificationStr != nil {
			parsedVerification, err = id.ParseVerification(*verificationStr)
			if err != nil {
				return err
			}
		}
		var parsedPolicy *id.Policy
		if policyStr != nil {
			policy, err := id.ParsePolicy(*policyStr)
			if err != nil {
				return err
			}
			parsedPolicy = &policy
		}
		mode, ok := proposalv1.ParseAutomationMode(modeStr)
		if !ok {
			return proposal.ErrInvalid
		}
		status, ok := proposalv1.ParseProposalStatus(statusStr)
		if !ok {
			return proposal.ErrInvalid
		}
		var actions []proposalv1.BoundedAction
		if err := json.Unmarshal(actionsJSON, &actions); err != nil {
			return err
		}
		var evidenceRefs []string
		if err := json.Unmarshal(evidenceJSON, &evidenceRefs); err != nil {
			return err
		}
		var signalRefs []string
		if err := json.Unmarshal(signalJSON, &signalRefs); err != nil {
			return err
		}
		result = proposal.Proposal{
			ID:             parsedID,
			TenantID:       parsedTenant,
			VerificationID: parsedVerification,
			PolicyID:       parsedPolicy,
			Mode:           mode,
			Status:         status,
			Actions:        actions,
			EvidenceRefs:   evidenceRefs,
			SignalRefs:     signalRefs,
			ModelID:        modelID,
			ModelVersion:   modelVersion,
			PromptVersion:  promptVersion,
			ContextDigest:  contextDigest,
			ExpiresAt:      expiresAt.UTC(),
			CreatedAt:      createdAt.UTC(),
			UpdatedAt:      updatedAt.UTC(),
			Version:        version,
			ActorID:        actorID,
			Reason:         "",
		}
		if reason != nil {
			result.Reason = *reason
		}
		if supersedesStr != nil {
			parsedSupersedes, err := id.ParseProposal(*supersedesStr)
			if err != nil {
				return err
			}
			result.Supersedes = &parsedSupersedes
		}
		return nil
	})
	if err != nil {
		return proposal.Proposal{}, err
	}
	return result, nil
}

// Update applies an optimistic-concurrency transition.
func (store *Store) Update(ctx context.Context, scope tenant.Scope, p proposal.Proposal, expectedVersion int64) error {
	if scope.ID().IsZero() || p.TenantID != scope.ID() {
		return proposal.ErrInvalid
	}
	if p.Version != expectedVersion+1 {
		return proposal.ErrConflict
	}
	if err := p.Validate(); err != nil {
		return err
	}
	actionsJSON, err := json.Marshal(p.Actions)
	if err != nil {
		return err
	}
	evidenceJSON, err := json.Marshal(p.EvidenceRefs)
	if err != nil {
		return err
	}
	signalJSON, err := json.Marshal(p.SignalRefs)
	if err != nil {
		return err
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, scope.ID().String()); err != nil {
			return err
		}
		var supersedes *string
		if p.Supersedes != nil {
			value := p.Supersedes.String()
			supersedes = &value
		}
		result, err := tx.Exec(ctx, `UPDATE idenqa.proposals SET status=$1, actions=$2, evidence_refs=$3, signal_refs=$4, model_id=$5, model_version=$6, prompt_version=$7, context_digest=$8, expires_at=$9, supersedes=$10, updated_at=$11, version=$12, actor_id=$13, reason=$14
WHERE tenant_id=$15 AND id=$16 AND version=$17`,
			p.Status.String(), string(actionsJSON), string(evidenceJSON), string(signalJSON),
			p.ModelID, p.ModelVersion, p.PromptVersion, p.ContextDigest,
			p.ExpiresAt, supersedes, p.UpdatedAt, p.Version, p.ActorID, p.Reason,
			scope.ID().String(), p.ID.String(), expectedVersion)
		if err != nil {
			return fmt.Errorf("update proposal: %w", err)
		}
		if result.RowsAffected() != 1 {
			return proposal.ErrConflict
		}
		return nil
	})
}

// GetByVerification lists proposals for a verification.
func (store *Store) GetByVerification(ctx context.Context, scope tenant.Scope, verificationID id.Verification) ([]proposal.Proposal, error) {
	if scope.ID().IsZero() || verificationID.IsZero() {
		return nil, proposal.ErrInvalid
	}
	var proposals []proposal.Proposal
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, scope.ID().String()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id, tenant_id, verification_id, mode, status, actions, evidence_refs, signal_refs, model_id, model_version, prompt_version, context_digest, expires_at, supersedes, created_at, updated_at, version, actor_id, reason
FROM idenqa.proposals WHERE tenant_id=$1 AND verification_id=$2 ORDER BY created_at`, scope.ID().String(), verificationID.String())
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var idStr, tenantStr, verificationStr, modeStr, statusStr *string
			var actionsJSON, evidenceJSON, signalJSON []byte
			var modelID, modelVersion, promptVersion, contextDigest, actorID, reason *string
			var supersedesStr *string
			var expiresAt, createdAt, updatedAt time.Time
			var version int64
			if err := rows.Scan(&idStr, &tenantStr, &verificationStr, &modeStr, &statusStr, &actionsJSON, &evidenceJSON, &signalJSON, &modelID, &modelVersion, &promptVersion, &contextDigest, &expiresAt, &supersedesStr, &createdAt, &updatedAt, &version, &actorID, &reason); err != nil {
				return err
			}
			parsedID, _ := id.ParseProposal(*idStr)
			parsedTenant, _ := id.ParseTenant(*tenantStr)
			parsedVerification, _ := id.ParseVerification(*verificationStr)
			mode, _ := proposalv1.ParseAutomationMode(*modeStr)
			status, _ := proposalv1.ParseProposalStatus(*statusStr)
			var actions []proposalv1.BoundedAction
			_ = json.Unmarshal(actionsJSON, &actions)
			var evidenceRefs []string
			_ = json.Unmarshal(evidenceJSON, &evidenceRefs)
			var signalRefs []string
			_ = json.Unmarshal(signalJSON, &signalRefs)
			p := proposal.Proposal{
				ID:             parsedID,
				TenantID:       parsedTenant,
				VerificationID: parsedVerification,
				Mode:           mode,
				Status:         status,
				Actions:        actions,
				EvidenceRefs:   evidenceRefs,
				SignalRefs:     signalRefs,
				ModelID:        *modelID,
				ModelVersion:   *modelVersion,
				PromptVersion:  *promptVersion,
				ContextDigest:  *contextDigest,
				ExpiresAt:      expiresAt.UTC(),
				CreatedAt:      createdAt.UTC(),
				UpdatedAt:      updatedAt.UTC(),
				Version:        version,
				ActorID:        *actorID,
			}
			if reason != nil {
				p.Reason = *reason
			}
			if supersedesStr != nil {
				parsedSupersedes, _ := id.ParseProposal(*supersedesStr)
				p.Supersedes = &parsedSupersedes
			}
			proposals = append(proposals, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return proposals, nil
}

// CommandStore methods — same Store handles accepted commands for simplicity.

// CreateCommand persists an accepted command.
func (store *Store) CreateCommand(ctx context.Context, scope tenant.Scope, record proposal.AcceptedCommandRecord) error {
	if scope.ID().IsZero() || record.TenantID != scope.ID() {
		return proposal.ErrInvalid
	}
	argsJSON, err := json.Marshal(record.Args)
	if err != nil {
		return err
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, scope.ID().String()); err != nil {
			return err
		}
		var verificationID, policyID *string
		if !record.VerificationID.IsZero() {
			value := record.VerificationID.String()
			verificationID = &value
		}
		if record.PolicyID != nil {
			value := record.PolicyID.String()
			policyID = &value
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.accepted_commands
(id, proposal_id, tenant_id, verification_id, policy_id, kind, args, model_id, model_version, prompt_version, created_at, executed_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
			record.ID.String(), record.ProposalID.String(), record.TenantID.String(), verificationID, policyID,
			string(record.Kind), string(argsJSON), record.ModelID, record.ModelVersion, record.PromptVersion,
			record.CreatedAt, record.ExecutedAt)
		if err != nil {
			return fmt.Errorf("insert accepted command: %w", err)
		}
		return nil
	})
}

// GetCommand retrieves a command.
func (store *Store) GetCommand(ctx context.Context, scope tenant.Scope, commandID id.AcceptedCommand) (proposal.AcceptedCommandRecord, error) {
	if scope.ID().IsZero() || commandID.IsZero() {
		return proposal.AcceptedCommandRecord{}, proposal.ErrInvalid
	}
	var result proposal.AcceptedCommandRecord
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, scope.ID().String()); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `SELECT id, proposal_id, tenant_id, verification_id, policy_id, kind, args, model_id, model_version, prompt_version, created_at, executed_at
FROM idenqa.accepted_commands WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), commandID.String())
		var idStr, proposalStr, tenantStr, kindStr *string
		var verificationStr, policyStr *string
		var argsJSON []byte
		var modelID, modelVersion, promptVersion *string
		var createdAt time.Time
		var executedAt *time.Time
		if err := row.Scan(&idStr, &proposalStr, &tenantStr, &verificationStr, &policyStr, &kindStr, &argsJSON, &modelID, &modelVersion, &promptVersion, &createdAt, &executedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return proposal.ErrNotFound
			}
			return err
		}
		parsedID, _ := id.ParseAcceptedCommand(*idStr)
		parsedProposal, _ := id.ParseProposal(*proposalStr)
		parsedTenant, _ := id.ParseTenant(*tenantStr)
		result = proposal.AcceptedCommandRecord{
			ID:            parsedID,
			ProposalID:    parsedProposal,
			TenantID:      parsedTenant,
			Kind:          proposalv1.ActionKind(*kindStr),
			ModelID:       *modelID,
			ModelVersion:  *modelVersion,
			PromptVersion: *promptVersion,
			CreatedAt:     createdAt.UTC(),
			ExecutedAt:    executedAt,
		}
		if verificationStr != nil {
			result.VerificationID, _ = id.ParseVerification(*verificationStr)
		}
		if policyStr != nil {
			policy, _ := id.ParsePolicy(*policyStr)
			result.PolicyID = &policy
		}
		if executedAt != nil {
			utc := executedAt.UTC()
			result.ExecutedAt = &utc
		}
		_ = json.Unmarshal(argsJSON, &result.Args)
		return nil
	})
	if err != nil {
		return proposal.AcceptedCommandRecord{}, err
	}
	return result, nil
}

// GetByProposal retrieves the accepted command for a proposal.
func (store *Store) GetByProposal(ctx context.Context, scope tenant.Scope, proposalID id.Proposal) (proposal.AcceptedCommandRecord, error) {
	if scope.ID().IsZero() || proposalID.IsZero() {
		return proposal.AcceptedCommandRecord{}, proposal.ErrInvalid
	}
	var result proposal.AcceptedCommandRecord
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, scope.ID().String()); err != nil {
			return err
		}
		row := tx.QueryRow(ctx, `SELECT id, proposal_id, tenant_id, verification_id, policy_id, kind, args, model_id, model_version, prompt_version, created_at, executed_at
FROM idenqa.accepted_commands WHERE tenant_id=$1 AND proposal_id=$2 LIMIT 1`, scope.ID().String(), proposalID.String())
		var idStr, proposalStr, tenantStr, kindStr *string
		var verificationStr, policyStr *string
		var argsJSON []byte
		var modelID, modelVersion, promptVersion *string
		var createdAt time.Time
		var executedAt *time.Time
		if err := row.Scan(&idStr, &proposalStr, &tenantStr, &verificationStr, &policyStr, &kindStr, &argsJSON, &modelID, &modelVersion, &promptVersion, &createdAt, &executedAt); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return proposal.ErrNotFound
			}
			return err
		}
		parsedID, _ := id.ParseAcceptedCommand(*idStr)
		parsedProposal, _ := id.ParseProposal(*proposalStr)
		parsedTenant, _ := id.ParseTenant(*tenantStr)
		result = proposal.AcceptedCommandRecord{
			ID:            parsedID,
			ProposalID:    parsedProposal,
			TenantID:      parsedTenant,
			Kind:          proposalv1.ActionKind(*kindStr),
			ModelID:       *modelID,
			ModelVersion:  *modelVersion,
			PromptVersion: *promptVersion,
			CreatedAt:     createdAt.UTC(),
			ExecutedAt:    executedAt,
		}
		if verificationStr != nil {
			result.VerificationID, _ = id.ParseVerification(*verificationStr)
		}
		if policyStr != nil {
			policy, _ := id.ParsePolicy(*policyStr)
			result.PolicyID = &policy
		}
		if executedAt != nil {
			utc := executedAt.UTC()
			result.ExecutedAt = &utc
		}
		_ = json.Unmarshal(argsJSON, &result.Args)
		return nil
	})
	if err != nil {
		return proposal.AcceptedCommandRecord{}, err
	}
	return result, nil
}

// MarkExecuted records the deterministic execution time of a command idempotently.
func (store *Store) MarkExecuted(ctx context.Context, scope tenant.Scope, commandID id.AcceptedCommand, at time.Time) error {
	if scope.ID().IsZero() || commandID.IsZero() || at.IsZero() {
		return proposal.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, scope.ID().String()); err != nil {
			return err
		}
		result, err := tx.Exec(ctx, `UPDATE idenqa.accepted_commands SET executed_at=$1 WHERE tenant_id=$2 AND id=$3 AND executed_at IS NULL`, at, scope.ID().String(), commandID.String())
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			// already executed is idempotent success
			return nil
		}
		return nil
	})
}

// Ensure interfaces
var _ proposal.Repository = (*Store)(nil)
var _ proposal.CommandStore = (*Store)(nil)
