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
	"github.com/jackc/pgx/v5/pgconn"
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
	var modelID *string
	if config.ModelID != nil {
		value := config.ModelID.String()
		modelID = &value
	}
	var modelVersion, promptVersion, activationRevision *int64
	if config.ModelID != nil {
		modelVersion = &config.ModelVersion
		promptVersion = &config.PromptVersion
		activationRevision = &config.ActivationRevision
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
(tenant_id, workflow, mode, allow_list_version, allowed_kinds, cost_daily_limit, model_registry_id, model_registry_version, prompt_id, prompt_registry_version, activation_revision, version, created_at, updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
				config.TenantID.String(), config.Workflow, config.Mode.String(), config.AllowListVersion, string(kindsJSON),
				config.CostDailyLimit, modelID, modelVersion, promptID, promptVersion, activationRevision,
				config.Version, config.CreatedAt, config.UpdatedAt)
			return err
		case err != nil:
			return err
		}
		if config.Version != existingVersion+1 {
			return proposal.ErrConflict
		}
		result, err := tx.Exec(ctx, `UPDATE idenqa.proposal_mode_configs
SET mode=$3, allow_list_version=$4, allowed_kinds=$5, cost_daily_limit=$6, model_registry_id=$7, model_registry_version=$8, prompt_id=$9, prompt_registry_version=$10, activation_revision=$11, version=$12, updated_at=$13
WHERE tenant_id=$1 AND workflow=$2 AND version=$14`,
			config.TenantID.String(), config.Workflow, config.Mode.String(), config.AllowListVersion, string(kindsJSON),
			config.CostDailyLimit, modelID, modelVersion, promptID, promptVersion, activationRevision,
			config.Version, config.UpdatedAt, existingVersion)
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
		var modelID, promptID *string
		var modelVersion, promptVersion, activationRevision *int64
		var version int64
		var createdAt, updatedAt time.Time
		err := tx.QueryRow(ctx, `SELECT mode, allow_list_version, allowed_kinds, cost_daily_limit, model_registry_id, model_registry_version, prompt_id, prompt_registry_version, activation_revision, version, created_at, updated_at
FROM idenqa.proposal_mode_configs WHERE tenant_id=$1 AND workflow=$2`, tenantID.String(), workflow).Scan(
			&mode, &allowListVersion, &kindsJSON, &costDailyLimit, &modelID, &modelVersion, &promptID, &promptVersion, &activationRevision, &version, &createdAt, &updatedAt)
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
		if modelID != nil {
			parsed, err := id.ParseModel(*modelID)
			if err != nil || modelVersion == nil || promptVersion == nil || activationRevision == nil {
				return proposal.ErrInvalid
			}
			result.ModelID = &parsed
			result.ModelVersion = *modelVersion
			result.PromptVersion = *promptVersion
			result.ActivationRevision = *activationRevision
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
	if err := record.Validate(); err != nil {
		return err
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
	if err := record.Validate(); err != nil {
		return err
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, record.TenantID.String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.generative_model_registry
(tenant_id, model_id, version, logical_model_id, digest, created_at, actor_id)
VALUES ($1,$2,$3,$4,$5,$6,$7)`,
			record.TenantID.String(), record.ID.String(), record.Version, record.ModelID, record.Digest, record.CreatedAt, record.ActorID)
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
		var logicalModelID *string
		var digest, actorID string
		var createdAt time.Time
		err := tx.QueryRow(ctx, `SELECT logical_model_id, digest, created_at, actor_id FROM idenqa.generative_model_registry WHERE tenant_id=$1 AND model_id=$2 AND version=$3`, tenantID.String(), modelID.String(), version).Scan(
			&logicalModelID, &digest, &createdAt, &actorID)
		if errors.Is(err, pgx.ErrNoRows) {
			return proposal.ErrNotFound
		}
		if err != nil {
			return err
		}
		result = proposal.GenerativeModelRecord{ID: modelID, TenantID: tenantID, Version: version, Digest: digest, CreatedAt: createdAt.UTC(), ActorID: actorID}
		if logicalModelID != nil {
			result.ModelID = *logicalModelID
		}
		return nil
	})
	if err != nil {
		return proposal.GenerativeModelRecord{}, err
	}
	return result, nil
}

// PutActivation atomically updates the current workflow route and appends its audit history.
func (store *RegistryStore) PutActivation(ctx context.Context, activation proposal.GenerationActivation, expectedRevision int64) error {
	if err := activation.Validate(); err != nil || expectedRevision < 0 || activation.Revision != expectedRevision+1 {
		return proposal.ErrInvalid
	}
	return store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, activation.TenantID.String()); err != nil {
			return err
		}
		var currentRevision int64
		err := tx.QueryRow(ctx, `SELECT revision FROM idenqa.proposal_generation_activations WHERE tenant_id=$1 AND workflow=$2 FOR UPDATE`, activation.TenantID.String(), activation.Workflow).Scan(&currentRevision)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			if expectedRevision != 0 {
				return proposal.ErrConflict
			}
			var sourceRevision *int64
			if activation.SourceRevision > 0 {
				sourceRevision = &activation.SourceRevision
			}
			_, err = tx.Exec(ctx, `INSERT INTO idenqa.proposal_generation_activations
(tenant_id,workflow,revision,state,model_registry_id,model_registry_version,prompt_registry_id,prompt_registry_version,logical_model_id,upstream_model_version,prompt_version,action,source_revision,updated_at,actor_id,reason)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, activation.TenantID.String(), activation.Workflow,
				activation.Revision, string(activation.State), activation.ModelRegistryID.String(), activation.ModelRegistryVersion,
				activation.PromptRegistryID.String(), activation.PromptRegistryVersion, activation.ModelID, activation.ModelVersion,
				activation.PromptVersion, activation.Action, sourceRevision, activation.OccurredAt, activation.ActorID, activation.Reason)
			if err != nil {
				if isUniqueViolation(err) {
					return proposal.ErrConflict
				}
				return fmt.Errorf("insert proposal generation activation: %w", err)
			}
		case err != nil:
			return err
		default:
			if currentRevision != expectedRevision {
				return proposal.ErrConflict
			}
			var sourceRevision *int64
			if activation.SourceRevision > 0 {
				sourceRevision = &activation.SourceRevision
			}
			result, err := tx.Exec(ctx, `UPDATE idenqa.proposal_generation_activations SET
revision=$3,state=$4,model_registry_id=$5,model_registry_version=$6,prompt_registry_id=$7,prompt_registry_version=$8,logical_model_id=$9,upstream_model_version=$10,prompt_version=$11,action=$12,source_revision=$13,updated_at=$14,actor_id=$15,reason=$16
WHERE tenant_id=$1 AND workflow=$2 AND revision=$17`, activation.TenantID.String(), activation.Workflow,
				activation.Revision, string(activation.State), activation.ModelRegistryID.String(), activation.ModelRegistryVersion,
				activation.PromptRegistryID.String(), activation.PromptRegistryVersion, activation.ModelID, activation.ModelVersion,
				activation.PromptVersion, activation.Action, sourceRevision, activation.OccurredAt, activation.ActorID, activation.Reason, expectedRevision)
			if err != nil {
				return fmt.Errorf("update proposal generation activation: %w", err)
			}
			if result.RowsAffected() != 1 {
				return proposal.ErrConflict
			}
		}
		var sourceRevision *int64
		if activation.SourceRevision > 0 {
			sourceRevision = &activation.SourceRevision
		}
		_, err = tx.Exec(ctx, `INSERT INTO idenqa.proposal_generation_activation_history
(tenant_id,workflow,revision,state,model_registry_id,model_registry_version,prompt_registry_id,prompt_registry_version,logical_model_id,upstream_model_version,prompt_version,action,source_revision,reason,actor_id,occurred_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`, activation.TenantID.String(), activation.Workflow,
			activation.Revision, string(activation.State), activation.ModelRegistryID.String(), activation.ModelRegistryVersion,
			activation.PromptRegistryID.String(), activation.PromptRegistryVersion, activation.ModelID, activation.ModelVersion,
			activation.PromptVersion, activation.Action, sourceRevision, activation.Reason, activation.ActorID, activation.OccurredAt)
		if err != nil {
			if isUniqueViolation(err) {
				return proposal.ErrConflict
			}
			return fmt.Errorf("insert proposal generation activation history: %w", err)
		}
		return nil
	})
}

func isUniqueViolation(err error) bool {
	var databaseError *pgconn.PgError
	return errors.As(err, &databaseError) && databaseError.Code == "23505"
}

// GetActivation retrieves the current workflow activation under tenant scope.
func (store *RegistryStore) GetActivation(ctx context.Context, tenantID id.Tenant, workflow string) (proposal.GenerationActivation, error) {
	if tenantID.IsZero() || workflow == "" {
		return proposal.GenerationActivation{}, proposal.ErrInvalid
	}
	var result proposal.GenerationActivation
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return err
		}
		return scanActivation(tx.QueryRow(ctx, `SELECT revision,state,model_registry_id,model_registry_version,prompt_registry_id,prompt_registry_version,logical_model_id,upstream_model_version,prompt_version,action,source_revision,reason,actor_id,updated_at
FROM idenqa.proposal_generation_activations WHERE tenant_id=$1 AND workflow=$2`, tenantID.String(), workflow), tenantID, workflow, &result)
	})
	return result, err
}

// GetActivationRevision retrieves one immutable workflow lifecycle revision.
func (store *RegistryStore) GetActivationRevision(ctx context.Context, tenantID id.Tenant, workflow string, revision int64) (proposal.GenerationActivation, error) {
	if tenantID.IsZero() || workflow == "" || revision < 1 {
		return proposal.GenerationActivation{}, proposal.ErrInvalid
	}
	var result proposal.GenerationActivation
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return err
		}
		return scanActivation(tx.QueryRow(ctx, `SELECT revision,state,model_registry_id,model_registry_version,prompt_registry_id,prompt_registry_version,logical_model_id,upstream_model_version,prompt_version,action,source_revision,reason,actor_id,occurred_at
FROM idenqa.proposal_generation_activation_history WHERE tenant_id=$1 AND workflow=$2 AND revision=$3`, tenantID.String(), workflow, revision), tenantID, workflow, &result)
	})
	return result, err
}

// ListActivationHistory returns newest lifecycle revisions first.
func (store *RegistryStore) ListActivationHistory(ctx context.Context, tenantID id.Tenant, workflow string, limit int) ([]proposal.GenerationActivation, error) {
	if tenantID.IsZero() || workflow == "" || limit < 1 || limit > 100 {
		return nil, proposal.ErrInvalid
	}
	result := make([]proposal.GenerationActivation, 0, limit)
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT revision,state,model_registry_id,model_registry_version,prompt_registry_id,prompt_registry_version,logical_model_id,upstream_model_version,prompt_version,action,source_revision,reason,actor_id,occurred_at
FROM idenqa.proposal_generation_activation_history WHERE tenant_id=$1 AND workflow=$2 ORDER BY revision DESC LIMIT $3`, tenantID.String(), workflow, limit)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var activation proposal.GenerationActivation
			if err := scanActivation(rows, tenantID, workflow, &activation); err != nil {
				return err
			}
			result = append(result, activation)
		}
		return rows.Err()
	})
	return result, err
}

type activationScanner interface{ Scan(...any) error }

func scanActivation(scanner activationScanner, tenantID id.Tenant, workflow string, result *proposal.GenerationActivation) error {
	var state, modelRegistryID, promptRegistryID string
	var sourceRevision *int64
	if err := scanner.Scan(&result.Revision, &state, &modelRegistryID, &result.ModelRegistryVersion, &promptRegistryID, &result.PromptRegistryVersion,
		&result.ModelID, &result.ModelVersion, &result.PromptVersion, &result.Action, &sourceRevision, &result.Reason, &result.ActorID, &result.OccurredAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return proposal.ErrNotFound
		}
		return err
	}
	modelID, err := id.ParseModel(modelRegistryID)
	if err != nil {
		return err
	}
	promptID, err := id.ParsePrompt(promptRegistryID)
	if err != nil {
		return err
	}
	result.TenantID, result.Workflow, result.State = tenantID, workflow, proposal.GenerationActivationState(state)
	result.ModelRegistryID, result.PromptRegistryID = modelID, promptID
	result.OccurredAt = result.OccurredAt.UTC()
	if sourceRevision != nil {
		result.SourceRevision = *sourceRevision
	}
	return nil
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

// GetImpact returns one immutable tenant-scoped impact assessment.
func (store *RegistryStore) GetImpact(ctx context.Context, tenantID id.Tenant, assessmentID string) (proposal.ImpactAssessment, error) {
	if tenantID.IsZero() || assessmentID == "" {
		return proposal.ImpactAssessment{}, proposal.ErrInvalid
	}
	var result proposal.ImpactAssessment
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return err
		}
		var kind string
		err := tx.QueryRow(ctx, `SELECT id,kind,assessment,risk_level,created_at,actor_id FROM idenqa.impact_assessments WHERE tenant_id=$1 AND id=$2`, tenantID.String(), assessmentID).
			Scan(&result.ID, &kind, &result.Assessment, &result.RiskLevel, &result.CreatedAt, &result.ActorID)
		if errors.Is(err, pgx.ErrNoRows) {
			return proposal.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("find impact assessment: %w", err)
		}
		result.TenantID, result.Kind = tenantID, proposalv1.ActionKind(kind)
		result.CreatedAt = result.CreatedAt.UTC()
		return nil
	})
	return result, err
}

// ListImpacts returns a bounded newest-first page of immutable assessments.
func (store *RegistryStore) ListImpacts(ctx context.Context, tenantID id.Tenant, before time.Time, limit int) ([]proposal.ImpactAssessment, error) {
	if tenantID.IsZero() || limit < 1 || limit > 100 {
		return nil, proposal.ErrInvalid
	}
	result := make([]proposal.ImpactAssessment, 0, limit)
	err := store.pool.WithinTransaction(ctx, platformpostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx platformpostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id', $1, true)`, tenantID.String()); err != nil {
			return err
		}
		rows, err := tx.Query(ctx, `SELECT id,kind,assessment,risk_level,created_at,actor_id FROM idenqa.impact_assessments WHERE tenant_id=$1 AND ($2::timestamptz IS NULL OR created_at < $2) ORDER BY created_at DESC,id DESC LIMIT $3`, tenantID.String(), nullableImpactBefore(before), limit)
		if err != nil {
			return fmt.Errorf("list impact assessments: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var value proposal.ImpactAssessment
			var kind string
			if err := rows.Scan(&value.ID, &kind, &value.Assessment, &value.RiskLevel, &value.CreatedAt, &value.ActorID); err != nil {
				return fmt.Errorf("scan impact assessment: %w", err)
			}
			value.TenantID, value.Kind, value.CreatedAt = tenantID, proposalv1.ActionKind(kind), value.CreatedAt.UTC()
			result = append(result, value)
		}
		return rows.Err()
	})
	return result, err
}

func nullableImpactBefore(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value.UTC()
}

var _ proposal.ModeStore = (*ModeStore)(nil)
var _ proposal.RegistryStore = (*RegistryStore)(nil)
