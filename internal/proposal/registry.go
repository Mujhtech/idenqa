package proposal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// PromptRecord is an immutable prompt version. Sensitive prompts carry a
// classification flag so retention and deletion can apply shorter lifetimes.
type PromptRecord struct {
	ID        id.Prompt
	TenantID  id.Tenant
	Version   int64
	Content   string
	Digest    string
	ModelID   string
	Sensitive bool
	CreatedAt time.Time
	ActorID   string
}

// GenerativeModelRecord is an immutable generative model version.
type GenerativeModelRecord struct {
	ID        id.Model
	TenantID  id.Tenant
	Version   int64
	ModelID   string
	Digest    string
	CreatedAt time.Time
	ActorID   string
}

// ImpactAssessment records the AI impact assessment for a proposal kind.
type ImpactAssessment struct {
	ID         string
	TenantID   id.Tenant
	Kind       proposalv1.ActionKind
	Assessment string
	RiskLevel  string
	CreatedAt  time.Time
	ActorID    string
}

// RegistryStore owns prompt, model, and impact persistence.
type RegistryStore interface {
	CreatePrompt(ctx context.Context, record PromptRecord) error
	GetPrompt(ctx context.Context, tenant id.Tenant, promptID id.Prompt, version int64) (PromptRecord, error)
	CreateModel(ctx context.Context, record GenerativeModelRecord) error
	GetModel(ctx context.Context, tenant id.Tenant, modelID id.Model, version int64) (GenerativeModelRecord, error)
	CreateImpact(ctx context.Context, assessment ImpactAssessment) error
}

// InMemoryRegistry is a deterministic test implementation.
type InMemoryRegistry struct {
	prompts map[string]PromptRecord
	models  map[string]GenerativeModelRecord
	impacts map[string]ImpactAssessment
}

// NewInMemoryRegistry returns an empty deterministic prompt/model/impact store.
func NewInMemoryRegistry() *InMemoryRegistry {
	return &InMemoryRegistry{
		prompts: make(map[string]PromptRecord),
		models:  make(map[string]GenerativeModelRecord),
		impacts: make(map[string]ImpactAssessment),
	}
}

func promptKey(tenant id.Tenant, prompt id.Prompt, version int64) string {
	return fmt.Sprintf("%s:%s:%d", tenant.String(), prompt.String(), version)
}

func modelKey(tenant id.Tenant, model id.Model, version int64) string {
	return fmt.Sprintf("%s:%s:%d", tenant.String(), model.String(), version)
}

// DigestPrompt computes lowercase hex SHA-256 of prompt content.
func DigestPrompt(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// CreatePrompt persists an immutable prompt version.
func (registry *InMemoryRegistry) CreatePrompt(_ context.Context, record PromptRecord) error {
	if record.ID.IsZero() || record.TenantID.IsZero() || record.Version < 1 || record.Content == "" {
		return ErrInvalid
	}
	if record.Digest != DigestPrompt(record.Content) {
		return ErrInvalid
	}
	key := promptKey(record.TenantID, record.ID, record.Version)
	if _, exists := registry.prompts[key]; exists {
		return ErrConflict
	}
	registry.prompts[key] = record
	return nil
}

// GetPrompt retrieves a prompt version.
func (registry *InMemoryRegistry) GetPrompt(_ context.Context, tenant id.Tenant, promptID id.Prompt, version int64) (PromptRecord, error) {
	key := promptKey(tenant, promptID, version)
	record, ok := registry.prompts[key]
	if !ok {
		return PromptRecord{}, ErrNotFound
	}
	return record, nil
}

// CreateModel persists an immutable generative-model version.
func (registry *InMemoryRegistry) CreateModel(_ context.Context, record GenerativeModelRecord) error {
	if record.ID.IsZero() || record.TenantID.IsZero() || record.Version < 1 || record.ModelID == "" {
		return ErrInvalid
	}
	key := modelKey(record.TenantID, record.ID, record.Version)
	if _, exists := registry.models[key]; exists {
		return ErrConflict
	}
	registry.models[key] = record
	return nil
}

// GetModel retrieves a generative-model version.
func (registry *InMemoryRegistry) GetModel(_ context.Context, tenant id.Tenant, modelID id.Model, version int64) (GenerativeModelRecord, error) {
	key := modelKey(tenant, modelID, version)
	record, ok := registry.models[key]
	if !ok {
		return GenerativeModelRecord{}, ErrNotFound
	}
	return record, nil
}

// CreateImpact persists an AI impact assessment.
func (registry *InMemoryRegistry) CreateImpact(_ context.Context, assessment ImpactAssessment) error {
	if assessment.ID == "" || assessment.TenantID.IsZero() || assessment.Assessment == "" {
		return ErrInvalid
	}
	if _, ok := registry.impacts[assessment.ID]; ok {
		return ErrConflict
	}
	if len(assessment.Assessment) > 8192 {
		return ErrInvalid
	}
	registry.impacts[assessment.ID] = assessment
	return nil
}
