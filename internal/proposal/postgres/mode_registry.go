package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/proposal"
	"github.com/jackc/pgx/v5"
)

// ModeStore adapts automation-mode configuration persistence to PostgreSQL.
type ModeStore struct {
	pool transactionRunner
}

// NewModeStore constructs a mode-configuration adapter.
func NewModeStore(pool transactionRunner) *ModeStore {
	return &ModeStore{pool: pool}
}

// Put persists a versioned automation-mode configuration under tenant scope.
func (store *ModeStore) Put(ctx context.Context, config proposal.ModeConfig) error {
	if config.TenantID.IsZero() {
		return proposal.ErrInvalid
	}
	if err := config.Validate(); err != nil {
		return err
	}
	kindsJSON, err := json.Marshal(kindStrings(config.AllowedKinds))
	if err != nil {
		return err
	}
	var promptID *string
	if config.PromptID != nil {
		value := config.PromptID.String()
		promptID = &value
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, config.TenantID.String()); err != nil {
			return err
		}
		var existingVersion int64
		err := tx.QueryRow(ctx, `SELECT version FROM idenqa.proposal_mode_configs WHERE tenant_id=$1 AND workflow=$2`, config.TenantID.String(), config.Workflow).Scan(&existingVersion)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if config.Version != 1 {
				return proposal.ErrConflict
			}
			_, err = tx.Exec(ctx, `INSERT INTO idenqa.proposal_mode_configs
(tenant_id, workflow, mode, allow_list_version, allowed_kinds, cost_daily_limit, prompt_id, version, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
				config.TenantID.String(), config.Workflow, config.Mode.String(), config.AllowListVersion, string(kindsJSON),
				config.CostDailyLimit, promptID, config.Version, config.CreatedAt, config.UpdatedAt)
			return err
		case err != nil:
			return err
		}
		if config.Version != existingVersion+1 {
			return proposal.ErrConflict
		}
		result, err := tx.Exec(ctx, `UPDATE idenqa.proposal_mode_configs
SET mode=$3, allow_list_version=$4, allowed_kinds=$5, cost_daily_limit=$6, prompt_id=$7, version=$8, updated_at=$9
WHERE tenant_id=$1 AND workflow=$2 AND version=$10`,
			config.TenantID.String(), config.Workflow, config.Mode.String(), config.AllowListVersion, string(kindsJSON),
			config.CostDailyLimit, promptID, config.Version, config.UpdatedAt, existingVersion)
		if err != nil {
			return err
		}
		if result.RowsAffected() != 1 {
			return proposal.ErrConflict
		}
		return nil
	})
}

// Get retrieves a mode configuration, or ErrNotFound when unconfigured.
func (store *ModeStore) Get(ctx context.Context, tenantID id.Tenant, workflow string) (proposal.ModeConfig, error) {
	if tenantID.IsZero() || workflow == "" {
		return proposal.ModeConfig{}, proposal.ErrInvalid
	}
	var result proposal.ModeConfig
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return err
		}
		var mode, allowListVersion string
		var kindsJSON []byte
		var costDailyLimit int
		var promptID *string
		var version int64
		var createdAt, updatedAt time.Time
		err := tx.QueryRow(ctx, `SELECT mode, allow_list_version, allowed_kinds, cost_daily_limit, prompt_id, version, created_at, updated_at
FROM idenqa.proposal_mode_configs WHERE tenant_id=$1 AND workflow=$2`, tenantID.String(), workflow).Scan(
			&mode, &allowListVersion, &kindsJSON, &costDailyLimit, &promptID, &version, &createdAt, &updatedAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return proposal.ErrNotFound
		}
		if err != nil {
			return err
		}
		parsedMode, ok := proposalv1.ParseAutomationMode(mode)
		if !ok {
			return proposal.ErrInvalid
		}
		var kinds []string
		if err := json.Unmarshal(kindsJSON, &kinds); err != nil {
			return err
		}
		allowed := make([]proposalv1.ActionKind, 0, len(kinds))
		for _, kind := range kinds {
			allowed = append(allowed, proposalv1.ActionKind(kind))
		}
		result = proposal.ModeConfig{
			TenantID:         tenantID,
			Workflow:         workflow,
			Mode:             parsedMode,
			AllowListVersion: allowListVersion,
			AllowedKinds:     allowed,
			CostDailyLimit:   costDailyLimit,
			Version:          version,
			CreatedAt:        createdAt.UTC(),
			UpdatedAt:        updatedAt.UTC(),
		}
		if promptID != nil {
			parsed, err := id.ParsePrompt(*promptID)
			if err != nil {
				return err
			}
			result.PromptID = &parsed
		}
		return nil
	})
	if err != nil {
		return proposal.ModeConfig{}, err
	}
	return result, nil
}

func kindStrings(kinds []proposalv1.ActionKind) []string {
	values := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		values = append(values, string(kind))
	}
	return values
}

// RegistryStore adapts prompt, generative-model, and impact-assessment
// persistence to PostgreSQL.
type RegistryStore struct {
	pool transactionRunner
}

// NewRegistryStore constructs a registry adapter.
func NewRegistryStore(pool transactionRunner) *RegistryStore {
	return &RegistryStore{pool: pool}
}

// CreatePrompt persists an immutable prompt version under tenant scope.
func (store *RegistryStore) CreatePrompt(ctx context.Context, record proposal.PromptRecord) error {
	if record.ID.IsZero() || record.TenantID.IsZero() || record.Version < 1 || record.Content == "" || record.Digest == "" {
		return proposal.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, record.TenantID.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.prompt_registry
(tenant_id, prompt_id, version, content, digest, model_id, sensitive, created_at, actor_id)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			record.TenantID.String(), record.ID.String(), record.Version, record.Content, record.Digest, record.ModelID, record.Sensitive, record.CreatedAt, record.ActorID)
		if err != nil {
			return fmt.Errorf("insert prompt registry: %w", err)
		}
		return nil
	})
}

// GetPrompt retrieves a prompt version under tenant scope.
func (store *RegistryStore) GetPrompt(ctx context.Context, tenantID id.Tenant, promptID id.Prompt, version int64) (proposal.PromptRecord, error) {
	if tenantID.IsZero() || promptID.IsZero() || version < 1 {
		return proposal.PromptRecord{}, proposal.ErrInvalid
	}
	var result proposal.PromptRecord
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return err
		}
		var content, digest, modelID, actorID string
		var sensitive bool
		var createdAt time.Time
		err := tx.QueryRow(ctx, `SELECT content, digest, model_id, sensitive, created_at, actor_id
FROM idenqa.prompt_registry WHERE tenant_id=$1 AND prompt_id=$2 AND version=$3`, tenantID.String(), promptID.String(), version).Scan(
			&content, &digest, &modelID, &sensitive, &createdAt, &actorID)
		if errors.Is(err, pgx.ErrNoRows) {
			return proposal.ErrNotFound
		}
		if err != nil {
			return err
		}
		result = proposal.PromptRecord{ID: promptID, TenantID: tenantID, Version: version, Content: content, Digest: digest, ModelID: modelID, Sensitive: sensitive, CreatedAt: createdAt.UTC(), ActorID: actorID}
		return nil
	})
	if err != nil {
		return proposal.PromptRecord{}, err
	}
	return result, nil
}

// CreateModel persists an immutable generative-model version under tenant scope.
func (store *RegistryStore) CreateModel(ctx context.Context, record proposal.GenerativeModelRecord) error {
	if record.ID.IsZero() || record.TenantID.IsZero() || record.Version < 1 || record.ModelID == "" || record.Digest == "" {
		return proposal.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, record.TenantID.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.generative_model_registry
(tenant_id, model_id, version, digest, created_at, actor_id)
VALUES ($1,$2,$3,$4,$5,$6)`,
			record.TenantID.String(), record.ID.String(), record.Version, record.Digest, record.CreatedAt, record.ActorID)
		if err != nil {
			return fmt.Errorf("insert generative model registry: %w", err)
		}
		return nil
	})
}

// GetModel retrieves a generative-model version under tenant scope.
func (store *RegistryStore) GetModel(ctx context.Context, tenantID id.Tenant, modelID id.Model, version int64) (proposal.GenerativeModelRecord, error) {
	if tenantID.IsZero() || modelID.IsZero() || version < 1 {
		return proposal.GenerativeModelRecord{}, proposal.ErrInvalid
	}
	var result proposal.GenerativeModelRecord
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return err
		}
		var digest, actorID string
		var createdAt time.Time
		err := tx.QueryRow(ctx, `SELECT digest, created_at, actor_id FROM idenqa.generative_model_registry WHERE tenant_id=$1 AND model_id=$2 AND version=$3`, tenantID.String(), modelID.String(), version).Scan(
			&digest, &createdAt, &actorID)
		if errors.Is(err, pgx.ErrNoRows) {
			return proposal.ErrNotFound
		}
		if err != nil {
			return err
		}
		result = proposal.GenerativeModelRecord{ID: modelID, TenantID: tenantID, Version: version, Digest: digest, CreatedAt: createdAt.UTC(), ActorID: actorID}
		return nil
	})
	if err != nil {
		return proposal.GenerativeModelRecord{}, err
	}
	return result, nil
}

// CreateImpact persists an AI impact assessment under tenant scope.
func (store *RegistryStore) CreateImpact(ctx context.Context, assessment proposal.ImpactAssessment) error {
	if assessment.ID == "" || assessment.TenantID.IsZero() || assessment.Assessment == "" {
		return proposal.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, assessment.TenantID.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.impact_assessments
(id, tenant_id, kind, assessment, risk_level, created_at, actor_id)
VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			assessment.ID, assessment.TenantID.String(), string(assessment.Kind), assessment.Assessment, assessment.RiskLevel, assessment.CreatedAt, assessment.ActorID)
		if err != nil {
			return fmt.Errorf("insert impact assessment: %w", err)
		}
		return nil
	})
}

var _ proposal.ModeStore = (*ModeStore)(nil)
var _ proposal.RegistryStore = (*RegistryStore)(nil)
