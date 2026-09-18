package proposal

import (
	"context"
	"errors"
	"time"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// ModeConfig is the versioned tenant/workflow automation configuration.
// It is pinned at session creation and travels with the session.
type ModeConfig struct {
	TenantID         id.Tenant
	Workflow         string
	Mode             proposalv1.AutomationMode
	AllowListVersion string
	AllowedKinds     []proposalv1.ActionKind
	CostDailyLimit   int
	PromptID         *id.Prompt
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Validate checks immutable mode invariants.
func (config ModeConfig) Validate() error {
	if config.TenantID.IsZero() {
		return ErrInvalid
	}
	if config.Workflow == "" || len(config.Workflow) > 64 {
		return ErrInvalid
	}
	if config.Mode == proposalv1.AutomationModeUnknown {
		return ErrInvalid
	}
	if config.Version < 1 {
		return ErrInvalid
	}
	for _, kind := range config.AllowedKinds {
		if !proposalv1.IsAllowedKind(kind) {
			return ErrUnknownKind
		}
	}
	if config.CostDailyLimit < 0 || config.CostDailyLimit > 100000 {
		return ErrInvalid
	}
	if config.CreatedAt.IsZero() || config.UpdatedAt.IsZero() {
		return ErrInvalid
	}
	return nil
}

// ModeStore is the consumer-owned persistence port for mode configurations.
type ModeStore interface {
	Get(ctx context.Context, tenant id.Tenant, workflow string) (ModeConfig, error)
	Put(ctx context.Context, config ModeConfig) error
}

// InMemoryModeStore is a deterministic in-memory implementation for tests.
type InMemoryModeStore struct {
	configs map[string]ModeConfig
}

// NewInMemoryModeStore returns an empty deterministic in-memory mode store.
func NewInMemoryModeStore() *InMemoryModeStore {
	return &InMemoryModeStore{configs: make(map[string]ModeConfig)}
}

func modeKey(tenant id.Tenant, workflow string) string {
	return tenant.String() + ":" + workflow
}

// Get retrieves a mode configuration, or ErrNotFound when unconfigured.
func (store *InMemoryModeStore) Get(_ context.Context, tenant id.Tenant, workflow string) (ModeConfig, error) {
	key := modeKey(tenant, workflow)
	config, ok := store.configs[key]
	if !ok {
		return ModeConfig{}, ErrNotFound
	}
	return config, nil
}

// Put stores a validated mode configuration with sequential version enforcement.
func (store *InMemoryModeStore) Put(_ context.Context, config ModeConfig) error {
	if err := config.Validate(); err != nil {
		return err
	}
	key := modeKey(config.TenantID, config.Workflow)
	if existing, ok := store.configs[key]; ok {
		if config.Version != existing.Version+1 {
			return ErrConflict
		}
	}
	store.configs[key] = config
	return nil
}

// Ensure import for errors used.
var _ = errors.New
