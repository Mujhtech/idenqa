package proposal

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// GenerationBindingRoute binds one deployed route to tenant-owned immutable
// prompt and model registry records.
type GenerationBindingRoute struct {
	ModelID               string
	ModelVersion          string
	PromptVersion         string
	ModelRegistryID       id.Model
	ModelRegistryVersion  int64
	ModelDigest           string
	PromptRegistryID      id.Prompt
	PromptRegistryVersion int64
	PromptDigest          string
	Instructions          string
}

// GenerationBindingValidator checks durable registry records before any model
// request leaves Core.
type GenerationBindingValidator struct {
	registry RegistryStore
	routes   map[string]GenerationBindingRoute
}

// GenerationBindingChecker is the consuming-side registry port used by the
// proposal service before invoking a configured model.
type GenerationBindingChecker interface {
	ValidateGenerationBinding(context.Context, id.Tenant, ModeConfig, proposalv1.ProposalRequest) error
}

// NewGenerationBindingValidator validates and snapshots a closed binding set.
func NewGenerationBindingValidator(registry RegistryStore, routes []GenerationBindingRoute) (*GenerationBindingValidator, error) {
	if registry == nil || len(routes) == 0 || len(routes) > 32 {
		return nil, errors.New("proposal generation bindings: registry and routes are required")
	}
	validator := &GenerationBindingValidator{registry: registry, routes: make(map[string]GenerationBindingRoute, len(routes))}
	for _, route := range routes {
		if route.ModelID == "" || route.ModelVersion == "" || route.PromptVersion == "" ||
			route.ModelRegistryID.IsZero() || route.ModelRegistryVersion < 1 || !isSHA256(route.ModelDigest) ||
			route.PromptRegistryID.IsZero() || route.PromptRegistryVersion < 1 || !isSHA256(route.PromptDigest) ||
			DigestPrompt(route.Instructions) != route.PromptDigest {
			return nil, errors.New("proposal generation bindings: invalid route")
		}
		key := generationRouteKey(route.ModelID, route.ModelVersion, route.PromptVersion)
		if _, duplicate := validator.routes[key]; duplicate {
			return nil, errors.New("proposal generation bindings: duplicate route")
		}
		validator.routes[key] = route
	}
	return validator, nil
}

func isSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}

// ValidateGenerationBinding requires exact deployed and tenant-owned registry
// records. Missing, mismatched, or stale bindings fail closed.
func (validator *GenerationBindingValidator) ValidateGenerationBinding(
	ctx context.Context,
	tenantID id.Tenant,
	mode ModeConfig,
	request proposalv1.ProposalRequest,
) error {
	if validator == nil || tenantID.IsZero() {
		return ErrNotAllowed
	}
	route, ok := validator.routes[generationRouteKey(request.ModelID, request.ModelVersion, request.PromptVersion)]
	if !ok {
		return ErrModelUnavailable
	}
	if mode.ModelID == nil || mode.PromptID == nil || mode.ActivationRevision < 1 ||
		*mode.ModelID != route.ModelRegistryID || mode.ModelVersion != route.ModelRegistryVersion ||
		*mode.PromptID != route.PromptRegistryID || mode.PromptVersion != route.PromptRegistryVersion {
		return fmt.Errorf("%w: mode pins do not match deployed route", ErrNotAllowed)
	}
	activation, err := validator.registry.GetActivation(ctx, tenantID, mode.Workflow)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: generation route is not active", ErrNotAllowed)
		}
		return fmt.Errorf("load generation activation: %w", err)
	}
	if activation.State != GenerationActivationActive || activation.Revision != mode.ActivationRevision ||
		activation.ModelRegistryID != route.ModelRegistryID || activation.ModelRegistryVersion != route.ModelRegistryVersion ||
		activation.PromptRegistryID != route.PromptRegistryID || activation.PromptRegistryVersion != route.PromptRegistryVersion ||
		activation.ModelID != route.ModelID || activation.ModelVersion != route.ModelVersion || activation.PromptVersion != route.PromptVersion {
		return fmt.Errorf("%w: active generation route does not match mode pins", ErrNotAllowed)
	}
	prompt, err := validator.registry.GetPrompt(ctx, tenantID, route.PromptRegistryID, route.PromptRegistryVersion)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: prompt route is not registered", ErrNotAllowed)
		}
		return fmt.Errorf("load prompt approval: %w", err)
	}
	if prompt.Digest != route.PromptDigest || DigestPrompt(prompt.Content) != route.PromptDigest || prompt.ModelID != route.ModelID {
		return fmt.Errorf("%w: prompt registry record does not match deployed route", ErrNotAllowed)
	}
	model, err := validator.registry.GetModel(ctx, tenantID, route.ModelRegistryID, route.ModelRegistryVersion)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return fmt.Errorf("%w: model route is not registered", ErrNotAllowed)
		}
		return fmt.Errorf("load model approval: %w", err)
	}
	if model.Digest != route.ModelDigest || model.ModelID != route.ModelID {
		return fmt.Errorf("%w: model registry record does not match deployed route", ErrNotAllowed)
	}
	return nil
}

var _ GenerationBindingChecker = (*GenerationBindingValidator)(nil)
