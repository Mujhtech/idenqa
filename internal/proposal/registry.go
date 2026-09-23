package proposal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// GenerationActivationState is the current operational state of one workflow route.
type GenerationActivationState string

const (
	// GenerationActivationActive permits generation through the exact pinned route.
	GenerationActivationActive GenerationActivationState = "active"
	// GenerationActivationRetired disables generation while retaining route history.
	GenerationActivationRetired GenerationActivationState = "retired"
)

// GenerationActivation is one versioned tenant/workflow route selection.
type GenerationActivation struct {
	TenantID              id.Tenant
	Workflow              string
	Revision              int64
	State                 GenerationActivationState
	ModelRegistryID       id.Model
	ModelRegistryVersion  int64
	PromptRegistryID      id.Prompt
	PromptRegistryVersion int64
	ModelID               string
	ModelVersion          string
	PromptVersion         string
	Action                string
	SourceRevision        int64
	Reason                string
	ActorID               string
	OccurredAt            time.Time
}

// Validate checks the bounded activation and audit-history contract.
func (activation GenerationActivation) Validate() error {
	validAction := activation.Action == "activated" || activation.Action == "retired" || activation.Action == "rolled_back"
	if activation.TenantID.IsZero() || activation.Workflow == "" || len(activation.Workflow) > 64 || activation.Revision < 1 ||
		(activation.State != GenerationActivationActive && activation.State != GenerationActivationRetired) ||
		activation.ModelRegistryID.IsZero() || activation.ModelRegistryVersion < 1 ||
		activation.PromptRegistryID.IsZero() || activation.PromptRegistryVersion < 1 ||
		!validGenerationToken(activation.ModelID) || !validRouteVersion(activation.ModelVersion) || !validRouteVersion(activation.PromptVersion) ||
		!validAction || !validAuditText(activation.Reason, 256, true) ||
		!validAuditText(activation.ActorID, 200, false) || activation.OccurredAt.IsZero() ||
		(activation.Action == "rolled_back" && activation.SourceRevision < 1) ||
		(activation.Action != "rolled_back" && activation.SourceRevision != 0) {
		return ErrInvalid
	}
	return nil
}

func validGenerationToken(value string) bool {
	if len(value) < 1 || len(value) > 64 || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	for _, character := range value[1:] {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

func validRouteVersion(value string) bool {
	return validAuditText(value, 64, false)
}

func validAuditText(value string, maximum int, emptyAllowed bool) bool {
	if (!emptyAllowed && value == "") || len(value) > maximum || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

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

// Validate checks one immutable prompt registry revision.
func (record PromptRecord) Validate() error {
	if record.ID.IsZero() || record.TenantID.IsZero() || record.Version < 1 || record.Content == "" ||
		len(record.Content) > 16*1024 || !utf8.ValidString(record.Content) || record.Digest != DigestPrompt(record.Content) ||
		!validGenerationToken(record.ModelID) || record.CreatedAt.IsZero() || !validAuditText(record.ActorID, 200, false) {
		return ErrInvalid
	}
	return nil
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

// Validate checks one immutable generative-model registry revision.
func (record GenerativeModelRecord) Validate() error {
	if record.ID.IsZero() || record.TenantID.IsZero() || record.Version < 1 ||
		!validGenerationToken(record.ModelID) || !isSHA256(record.Digest) || record.CreatedAt.IsZero() ||
		!validAuditText(record.ActorID, 200, false) {
		return ErrInvalid
	}
	return nil
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
	PutActivation(ctx context.Context, activation GenerationActivation, expectedRevision int64) error
	GetActivation(ctx context.Context, tenant id.Tenant, workflow string) (GenerationActivation, error)
	GetActivationRevision(ctx context.Context, tenant id.Tenant, workflow string, revision int64) (GenerationActivation, error)
	ListActivationHistory(ctx context.Context, tenant id.Tenant, workflow string, limit int) ([]GenerationActivation, error)
	CreateImpact(ctx context.Context, assessment ImpactAssessment) error
	GetImpact(ctx context.Context, tenant id.Tenant, assessmentID string) (ImpactAssessment, error)
	ListImpacts(ctx context.Context, tenant id.Tenant, before time.Time, limit int) ([]ImpactAssessment, error)
}

// InMemoryRegistry is a deterministic test implementation.
type InMemoryRegistry struct {
	prompts           map[string]PromptRecord
	models            map[string]GenerativeModelRecord
	impacts           map[string]ImpactAssessment
	activations       map[string]GenerationActivation
	activationHistory map[string][]GenerationActivation
}

// NewInMemoryRegistry returns an empty deterministic prompt/model/impact store.
func NewInMemoryRegistry() *InMemoryRegistry {
	return &InMemoryRegistry{
		prompts:           make(map[string]PromptRecord),
		models:            make(map[string]GenerativeModelRecord),
		impacts:           make(map[string]ImpactAssessment),
		activations:       make(map[string]GenerationActivation),
		activationHistory: make(map[string][]GenerationActivation),
	}
}

// PutActivation applies one CAS-protected lifecycle transition and appends history.
func (registry *InMemoryRegistry) PutActivation(_ context.Context, activation GenerationActivation, expectedRevision int64) error {
	if err := activation.Validate(); err != nil {
		return err
	}
	key := modeKey(activation.TenantID, activation.Workflow)
	current, exists := registry.activations[key]
	if (!exists && expectedRevision != 0) || (exists && current.Revision != expectedRevision) || activation.Revision != expectedRevision+1 {
		return ErrConflict
	}
	registry.activations[key] = activation
	registry.activationHistory[key] = append(registry.activationHistory[key], activation)
	return nil
}

// GetActivation returns the current workflow activation.
func (registry *InMemoryRegistry) GetActivation(_ context.Context, tenantID id.Tenant, workflow string) (GenerationActivation, error) {
	activation, ok := registry.activations[modeKey(tenantID, workflow)]
	if !ok {
		return GenerationActivation{}, ErrNotFound
	}
	return activation, nil
}

// GetActivationRevision returns one immutable lifecycle revision.
func (registry *InMemoryRegistry) GetActivationRevision(_ context.Context, tenantID id.Tenant, workflow string, revision int64) (GenerationActivation, error) {
	for _, activation := range registry.activationHistory[modeKey(tenantID, workflow)] {
		if activation.Revision == revision {
			return activation, nil
		}
	}
	return GenerationActivation{}, ErrNotFound
}

// ListActivationHistory returns newest lifecycle revisions first.
func (registry *InMemoryRegistry) ListActivationHistory(_ context.Context, tenantID id.Tenant, workflow string, limit int) ([]GenerationActivation, error) {
	if limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	history := registry.activationHistory[modeKey(tenantID, workflow)]
	result := make([]GenerationActivation, 0, min(limit, len(history)))
	for index := len(history) - 1; index >= 0 && len(result) < limit; index-- {
		result = append(result, history[index])
	}
	return result, nil
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
	if err := record.Validate(); err != nil {
		return err
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
	if err := record.Validate(); err != nil {
		return err
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

// GetImpact returns one immutable tenant impact assessment.
func (registry *InMemoryRegistry) GetImpact(_ context.Context, tenantID id.Tenant, assessmentID string) (ImpactAssessment, error) {
	assessment, ok := registry.impacts[assessmentID]
	if !ok || assessment.TenantID != tenantID {
		return ImpactAssessment{}, ErrNotFound
	}
	return assessment, nil
}

// ListImpacts returns a bounded newest-first page of immutable assessments.
func (registry *InMemoryRegistry) ListImpacts(_ context.Context, tenantID id.Tenant, before time.Time, limit int) ([]ImpactAssessment, error) {
	if tenantID.IsZero() || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	values := make([]ImpactAssessment, 0, len(registry.impacts))
	for _, assessment := range registry.impacts {
		if assessment.TenantID == tenantID && (before.IsZero() || assessment.CreatedAt.Before(before)) {
			values = append(values, assessment)
		}
	}
	slices.SortFunc(values, func(left, right ImpactAssessment) int {
		if compared := right.CreatedAt.Compare(left.CreatedAt); compared != 0 {
			return compared
		}
		return strings.Compare(right.ID, left.ID)
	})
	if len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}
