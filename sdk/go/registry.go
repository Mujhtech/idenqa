package idenqa

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
)

// ModelHistoryOptions selects an exclusive registry-version cursor; zero starts at the latest receipt.
type ModelHistoryOptions struct {
	Limit  int
	Before int64
}

// ModelRevisionKind selects one independently numbered immutable revision family.
type ModelRevisionKind string

const (
	// ModelRevision selects registration history.
	ModelRevision ModelRevisionKind = "model"
	// ThresholdRevision selects threshold history.
	ThresholdRevision ModelRevisionKind = "threshold"
)

var modelRegistryName = regexp.MustCompile("^[a-z][a-z0-9._-]{0,63}$")

func callModelResource[T any](ctx context.Context, client *Client, name string, request resourceRequest) (*Response[T], error) {
	if !modelRegistryName.MatchString(name) {
		return nil, errors.New("idenqa: invalid model registry name")
	}
	request.Path = "v1/models/" + url.PathEscape(name) + request.Path
	return callResource[T](ctx, client, request)
}

// GetModel calls the published GET /v1/models/{modelName} operation.
// This registry is evaluation-only; it does not activate a production model.
func (client *Client) GetModel(ctx context.Context, modelName string) (*Response[ModelRegistryState], error) {
	return callModelResource[ModelRegistryState](ctx, client, modelName, resourceRequest{
		Method: "GET", Path: "",
	})
}

// GetModelRevision calls the published GET /v1/models/{modelName}/revisions/{kind}/{revision} operation.
// This registry is evaluation-only; it does not activate a production model.
func (client *Client) GetModelRevision(ctx context.Context, modelName string, kind ModelRevisionKind, revision int64) (*Response[ModelRegistryRevision], error) {
	if (kind != ModelRevision && kind != ThresholdRevision) || revision < 1 || revision > 9007199254740991 {
		return nil, errors.New("idenqa: invalid model revision")
	}
	return callModelResource[ModelRegistryRevision](ctx, client, modelName, resourceRequest{
		Method: "GET", Path: fmt.Sprintf("/revisions/%s/%d", kind, revision),
	})
}

// ListModelHistory calls the published GET /v1/models/{modelName}/history operation.
// This registry is evaluation-only; it does not activate a production model.
func (client *Client) ListModelHistory(ctx context.Context, modelName string, options ModelHistoryOptions) (*Response[[]ModelRegistryReceipt], error) {
	if options.Before < 0 || options.Before > 9007199254740990 {
		return nil, errors.New("idenqa: invalid model history cursor")
	}
	return callModelResource[[]ModelRegistryReceipt](ctx, client, modelName, resourceRequest{
		Method: "GET", Path: "/history",

		Query: queryValues(options.Limit, map[string]string{"before": strconv.FormatInt(options.Before, 10)}),
	})
}

// RegisterModel calls the published POST /v1/models/{modelName}/register operation.
// This registry is evaluation-only; it does not activate a production model.
func (client *Client) RegisterModel(ctx context.Context, modelName string, input ModelRegistrationWrite, options MutationOptions) (*Response[ModelRegistryReceipt], error) {
	return callModelResource[ModelRegistryReceipt](ctx, client, modelName, resourceRequest{
		Method: "POST", Path: "/register",

		Input:          input,
		IdempotencyKey: options.IdempotencyKey, RequireKey: true,
	})
}

// SetModelThreshold calls the published POST /v1/models/{modelName}/threshold operation.
// This registry is evaluation-only; it does not activate a production model.
func (client *Client) SetModelThreshold(ctx context.Context, modelName string, input ModelThresholdWrite, options MutationOptions) (*Response[ModelRegistryReceipt], error) {
	return callModelResource[ModelRegistryReceipt](ctx, client, modelName, resourceRequest{
		Method: "POST", Path: "/threshold",

		Input:          input,
		IdempotencyKey: options.IdempotencyKey, RequireKey: true,
	})
}

// ActivateModel calls the published POST /v1/models/{modelName}/activate operation.
// This registry is evaluation-only; it does not activate a production model.
func (client *Client) ActivateModel(ctx context.Context, modelName string, input ModelDeploymentWrite, options MutationOptions) (*Response[ModelRegistryReceipt], error) {
	return callModelResource[ModelRegistryReceipt](ctx, client, modelName, resourceRequest{
		Method: "POST", Path: "/activate",

		Input:          input,
		IdempotencyKey: options.IdempotencyKey, RequireKey: true,
	})
}

// RollbackModel calls the published POST /v1/models/{modelName}/rollback operation.
// This registry is evaluation-only; it does not activate a production model.
func (client *Client) RollbackModel(ctx context.Context, modelName string, input ModelDeploymentWrite, options MutationOptions) (*Response[ModelRegistryReceipt], error) {
	return callModelResource[ModelRegistryReceipt](ctx, client, modelName, resourceRequest{
		Method: "POST", Path: "/rollback",

		Input:          input,
		IdempotencyKey: options.IdempotencyKey, RequireKey: true,
	})
}

// RetireModel calls the published POST /v1/models/{modelName}/retire operation.
// This registry is evaluation-only; it does not activate a production model.
func (client *Client) RetireModel(ctx context.Context, modelName string, input ModelRetirementWrite, options MutationOptions) (*Response[ModelRegistryReceipt], error) {
	return callModelResource[ModelRegistryReceipt](ctx, client, modelName, resourceRequest{
		Method: "POST", Path: "/retire",

		Input:          input,
		IdempotencyKey: options.IdempotencyKey, RequireKey: true,
	})
}

// ValidateModel calls the published POST /v1/models/{modelName}/validate operation.
// This registry is evaluation-only; it does not activate a production model.
func (client *Client) ValidateModel(ctx context.Context, modelName string, input ModelValidationRequest) (*Response[ModelValidationReport], error) {
	return callModelResource[ModelValidationReport](ctx, client, modelName, resourceRequest{
		Method: "POST", Path: "/validate",

		Input: input,
	})
}

// ListProviderRegistrations calls the published GET /v1/providers operation.
func (client *Client) ListProviderRegistrations(ctx context.Context, options PaginationOptions) (*Response[ProviderRegistrationList], error) {
	return callResource[ProviderRegistrationList](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/providers",

		Query: options.values(),
	})
}

// CreateProviderRegistration calls the published POST /v1/providers operation.
func (client *Client) CreateProviderRegistration(ctx context.Context, input ProviderRegistrationCreate, options MutationOptions) (*Response[ProviderRegistrationReceipt], error) {
	return callResource[ProviderRegistrationReceipt](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/providers",

		Input:          input,
		IdempotencyKey: options.IdempotencyKey, RequireKey: true,
	})
}

// GetProviderRegistration calls the published GET /v1/providers/{providerID} operation.
func (client *Client) GetProviderRegistration(ctx context.Context, providerID string) (*Response[ProviderRegistration], error) {
	return callResource[ProviderRegistration](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/providers/%s",
		Identifier: providerID, Prefix: "pvr",
	})
}

// UpdateProviderRegistration calls the published PUT /v1/providers/{providerID} operation.
func (client *Client) UpdateProviderRegistration(ctx context.Context, providerID string, input ProviderRegistrationUpdate) (*Response[ProviderRegistrationReceipt], error) {
	return callResource[ProviderRegistrationReceipt](ctx, client, resourceRequest{
		Method: "PUT", Path: "v1/providers/%s",
		Identifier: providerID, Prefix: "pvr",
		Input: input,
	})
}

// ValidateProviderRegistration calls the published POST /v1/providers/{providerID}/validate operation.
func (client *Client) ValidateProviderRegistration(ctx context.Context, providerID string, input ProviderRegistrationWrite) (*Response[ProviderRegistrationValidationReport], error) {
	return callResource[ProviderRegistrationValidationReport](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/providers/%s/validate",
		Identifier: providerID, Prefix: "pvr",
		Input: input,
	})
}

// EnableProviderRegistration calls the published POST /v1/providers/{providerID}/enable operation.
func (client *Client) EnableProviderRegistration(ctx context.Context, providerID string, input ProviderRegistrationToggle) (*Response[ProviderRegistrationReceipt], error) {
	return callResource[ProviderRegistrationReceipt](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/providers/%s/enable",
		Identifier: providerID, Prefix: "pvr",
		Input: input,
	})
}

// DisableProviderRegistration calls the published POST /v1/providers/{providerID}/disable operation.
func (client *Client) DisableProviderRegistration(ctx context.Context, providerID string, input ProviderRegistrationToggle) (*Response[ProviderRegistrationReceipt], error) {
	return callResource[ProviderRegistrationReceipt](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/providers/%s/disable",
		Identifier: providerID, Prefix: "pvr",
		Input: input,
	})
}

// RotateProviderRegistrationCredential calls the published POST /v1/providers/{providerID}/rotate-credential operation.
func (client *Client) RotateProviderRegistrationCredential(ctx context.Context, providerID string, input ProviderCredentialRotationRequest) (*Response[ProviderRegistrationReceipt], error) {
	return callResource[ProviderRegistrationReceipt](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/providers/%s/rotate-credential",
		Identifier: providerID, Prefix: "pvr",
		Input: input,
	})
}

// GetProviderRegistrationHealth calls the published GET /v1/providers/{providerID}/health operation.
func (client *Client) GetProviderRegistrationHealth(ctx context.Context, providerID string) (*Response[ProviderRegistrationHealth], error) {
	return callResource[ProviderRegistrationHealth](ctx, client, resourceRequest{
		Method: "GET", Path: "v1/providers/%s/health",
		Identifier: providerID, Prefix: "pvr",
	})
}

// SimulateProviderFailure calls the published POST /v1/providers/{providerID}/failure-simulations operation.
func (client *Client) SimulateProviderFailure(ctx context.Context, providerID string, input ProviderFailureSimulationRequest) (*Response[ProviderFailureSimulation], error) {
	return callResource[ProviderFailureSimulation](ctx, client, resourceRequest{
		Method: "POST", Path: "v1/providers/%s/failure-simulations",
		Identifier: providerID, Prefix: "pvr",
		Input: input,
	})
}
