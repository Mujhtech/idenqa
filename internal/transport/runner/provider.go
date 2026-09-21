package runner

import (
	"context"
	"errors"
	"time"

	"buf.build/go/protovalidate"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const runnerControlTimeout = 5 * time.Second

// ProviderServer adapts a transport-independent provider to gRPC.
type ProviderServer struct {
	runnerv1.UnimplementedProviderRunnerServiceServer
	adapter   providerv1.Adapter
	validator protovalidate.Validator
}

// NewProviderServer constructs a provider service adapter.
func NewProviderServer(adapter providerv1.Adapter) (*ProviderServer, error) {
	if adapter == nil {
		return nil, errors.New("provider runner adapter is required")
	}
	validator, err := protovalidate.New()
	if err != nil {
		return nil, errors.New("construct provider runner validator")
	}
	return &ProviderServer{adapter: adapter, validator: validator}, nil
}

// Manifest returns the validated immutable provider manifest.
func (server *ProviderServer) Manifest(
	ctx context.Context,
	_ *runnerv1.ProviderRunnerServiceManifestRequest,
) (*runnerv1.ProviderRunnerServiceManifestResponse, error) {
	manifest, err := server.adapter.Manifest(ctx)
	if err != nil || manifest.Validate() != nil {
		return nil, status.Error(codes.Internal, "provider manifest unavailable")
	}
	response := &runnerv1.ProviderRunnerServiceManifestResponse{Manifest: providerManifestToProto(manifest)}
	if err := server.validator.Validate(response); err != nil {
		return nil, status.Error(codes.Internal, "provider manifest unavailable")
	}
	return response, nil
}

// ValidateConfiguration validates a reference without returning adapter details.
func (server *ProviderServer) ValidateConfiguration(
	ctx context.Context,
	request *runnerv1.ProviderRunnerServiceValidateConfigurationRequest,
) (*runnerv1.ProviderRunnerServiceValidateConfigurationResponse, error) {
	configuration, err := providerConfigurationFromProto(request.GetConfiguration())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "provider configuration reference is invalid")
	}
	result := &runnerv1.ValidationResult{Valid: true}
	if err := server.adapter.ValidateConfiguration(ctx, configuration); err != nil {
		result.Valid = false
		result.Code = "invalid_configuration"
	}
	return &runnerv1.ProviderRunnerServiceValidateConfigurationResponse{Validation: result}, nil
}

// Execute validates, executes, binds, and size-checks one provider result.
func (server *ProviderServer) Execute(
	ctx context.Context,
	request *runnerv1.ProviderRunnerServiceExecuteRequest,
) (*runnerv1.ProviderRunnerServiceExecuteResponse, error) {
	domainRequest, err := providerRequestFromProto(request.GetRequest())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "provider request is invalid")
	}
	executionContext, cancel, err := exactExecutionContext(ctx, domainRequest.Deadline)
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := server.adapter.Execute(executionContext, domainRequest)
	if err != nil {
		return nil, executionStatus(executionContext, "provider execution failed")
	}
	if err := result.ValidateForRequest(domainRequest); err != nil {
		return nil, status.Error(codes.Internal, "provider result is invalid")
	}
	response := &runnerv1.ProviderRunnerServiceExecuteResponse{Result: providerResultToProto(result)}
	if proto.Size(response.GetResult()) > providerv1.MaxResultBytes {
		return nil, status.Error(codes.ResourceExhausted, "provider result exceeds limit")
	}
	if err := server.validator.Validate(response); err != nil {
		return nil, status.Error(codes.Internal, "provider result is invalid")
	}
	return response, nil
}

// Health returns only the safe public health classification.
func (server *ProviderServer) Health(
	ctx context.Context,
	_ *runnerv1.ProviderRunnerServiceHealthRequest,
) (*runnerv1.ProviderRunnerServiceHealthResponse, error) {
	health, err := server.adapter.Health(ctx)
	if err != nil || health.Validate() != nil {
		return nil, status.Error(codes.Unavailable, "provider health unavailable")
	}
	return &runnerv1.ProviderRunnerServiceHealthResponse{Health: providerHealthToProto(health)}, nil
}

// ProviderClient adapts the generated client back to the public provider port.
type ProviderClient struct {
	client runnerv1.ProviderRunnerServiceClient
}

var _ providerv1.Adapter = (*ProviderClient)(nil)

// NewProviderClient constructs a transport-independent provider client.
func NewProviderClient(client runnerv1.ProviderRunnerServiceClient) (*ProviderClient, error) {
	if client == nil {
		return nil, errors.New("provider runner client is required")
	}
	return &ProviderClient{client: client}, nil
}

// Manifest requests the provider manifest under a bounded control deadline.
func (client *ProviderClient) Manifest(ctx context.Context) (providerv1.Manifest, error) {
	callContext, cancel := boundedContext(ctx, time.Now().Add(runnerControlTimeout))
	defer cancel()
	response, err := client.client.Manifest(callContext, &runnerv1.ProviderRunnerServiceManifestRequest{})
	if err != nil {
		return providerv1.Manifest{}, errors.New("provider runner manifest failed")
	}
	manifest, err := providerManifestFromProto(response.GetManifest())
	if err != nil {
		return providerv1.Manifest{}, errors.New("provider runner manifest is invalid")
	}
	return manifest, nil
}

// ValidateConfiguration validates a provider reference remotely.
func (client *ProviderClient) ValidateConfiguration(ctx context.Context, configuration providerv1.ConfigurationReference) error {
	if err := configuration.Validate(); err != nil {
		return err
	}
	callContext, cancel := boundedContext(ctx, time.Now().Add(runnerControlTimeout))
	defer cancel()
	response, err := client.client.ValidateConfiguration(callContext, &runnerv1.ProviderRunnerServiceValidateConfigurationRequest{
		Configuration: providerConfigurationToProto(configuration),
	})
	if err != nil || response.GetValidation() == nil || !response.GetValidation().GetValid() {
		return errors.New("provider runner rejected configuration")
	}
	return nil
}

// Execute sends one validated request under its exact deadline.
func (client *ProviderClient) Execute(ctx context.Context, request providerv1.Request) (providerv1.Result, error) {
	if err := request.Validate(); err != nil {
		return providerv1.Result{}, err
	}
	callContext, cancel := boundedContext(ctx, request.Deadline)
	defer cancel()
	response, err := client.client.Execute(callContext, &runnerv1.ProviderRunnerServiceExecuteRequest{
		Request: providerRequestToProto(request),
	})
	if err != nil {
		return providerv1.Result{}, errors.New("provider runner execution failed")
	}
	if proto.Size(response.GetResult()) > providerv1.MaxResultBytes {
		return providerv1.Result{}, errors.New("provider runner result exceeds limit")
	}
	result, err := providerResultFromProto(response.GetResult())
	if err != nil || result.ValidateForRequest(request) != nil {
		return providerv1.Result{}, errors.New("provider runner result is invalid")
	}
	return result, nil
}

// Health requests safe provider health under a bounded control deadline.
func (client *ProviderClient) Health(ctx context.Context) (providerv1.Health, error) {
	callContext, cancel := boundedContext(ctx, time.Now().Add(runnerControlTimeout))
	defer cancel()
	response, err := client.client.Health(callContext, &runnerv1.ProviderRunnerServiceHealthRequest{})
	if err != nil {
		return providerv1.Health{}, errors.New("provider runner health failed")
	}
	health, err := providerHealthFromProto(response.GetHealth())
	if err != nil {
		return providerv1.Health{}, errors.New("provider runner health is invalid")
	}
	return health, nil
}

func providerManifestToProto(manifest providerv1.Manifest) *runnerv1.ProviderManifest {
	capabilities := make([]*runnerv1.ProviderCapability, len(manifest.Capabilities))
	for index, capability := range manifest.Capabilities {
		capabilities[index] = providerCapabilityToProto(capability)
	}
	return &runnerv1.ProviderManifest{
		Package: &runnerv1.ProviderPackageProvenance{
			AdapterId: manifest.Package.AdapterID, AdapterVersion: manifest.Package.AdapterVersion,
			PackageDigest: manifest.Package.PackageDigest, Contract: versionToProto(manifest.Package.Contract.Major, manifest.Package.Contract.Minor),
		},
		Configuration: &runnerv1.ProviderConfigurationSchema{Id: manifest.Configuration.ID, Digest: manifest.Configuration.Digest},
		Capabilities:  capabilities,
		Restrictions:  providerRestrictionsToProto(manifest.Restrictions),
	}
}

func providerManifestFromProto(message *runnerv1.ProviderManifest) (providerv1.Manifest, error) {
	if message == nil || message.GetPackage() == nil || message.GetConfiguration() == nil || message.GetRestrictions() == nil {
		return providerv1.Manifest{}, errors.New("provider manifest is incomplete")
	}
	capabilities := make([]providerv1.Capability, len(message.GetCapabilities()))
	for index, capability := range message.GetCapabilities() {
		capabilities[index] = providerCapabilityFromProto(capability)
	}
	manifest := providerv1.Manifest{
		Package: providerv1.PackageProvenance{
			AdapterID: message.GetPackage().GetAdapterId(), AdapterVersion: message.GetPackage().GetAdapterVersion(),
			PackageDigest: message.GetPackage().GetPackageDigest(), Contract: providerVersionFromProto(message.GetPackage().GetContract()),
		},
		Configuration: providerv1.ConfigurationSchema{ID: message.GetConfiguration().GetId(), Digest: message.GetConfiguration().GetDigest()},
		Capabilities:  capabilities,
		Restrictions:  providerRestrictionsFromProto(message.GetRestrictions()),
	}
	return manifest, manifest.Validate()
}

func providerCapabilityToProto(capability providerv1.Capability) *runnerv1.ProviderCapability {
	return &runnerv1.ProviderCapability{
		Check: capability.Check, AcceptedEvidence: capability.AcceptedEvidence, AcceptedInputs: capability.AcceptedInputs,
		AcceptedAssurances: capability.AcceptedAssurances, ProcessingRegions: capability.ProcessingRegions,
		SupportsIdempotency: capability.SupportsIdempotency, SupportsCancellation: capability.SupportsCancellation,
	}
}

func providerCapabilityFromProto(message *runnerv1.ProviderCapability) providerv1.Capability {
	if message == nil {
		return providerv1.Capability{}
	}
	return providerv1.Capability{
		Check: message.GetCheck(), AcceptedEvidence: message.GetAcceptedEvidence(), AcceptedInputs: message.GetAcceptedInputs(),
		AcceptedAssurances: message.GetAcceptedAssurances(), ProcessingRegions: message.GetProcessingRegions(),
		SupportsIdempotency: message.GetSupportsIdempotency(), SupportsCancellation: message.GetSupportsCancellation(),
	}
}

func providerRestrictionsToProto(restrictions providerv1.Restrictions) *runnerv1.ProviderRestrictions {
	return &runnerv1.ProviderRestrictions{
		NetworkRequired: restrictions.NetworkRequired, MaximumGrants: uint32(restrictions.MaximumGrants),
		MaximumResultSize: restrictions.MaximumResultSize, MaximumDuration: durationpb.New(restrictions.MaximumDuration),
	}
}

func providerRestrictionsFromProto(message *runnerv1.ProviderRestrictions) providerv1.Restrictions {
	if message == nil {
		return providerv1.Restrictions{}
	}
	return providerv1.Restrictions{
		NetworkRequired: message.GetNetworkRequired(), MaximumGrants: boundedUint16(message.GetMaximumGrants()),
		MaximumResultSize: message.GetMaximumResultSize(), MaximumDuration: message.GetMaximumDuration().AsDuration(),
	}
}

func providerConfigurationToProto(configuration providerv1.ConfigurationReference) *runnerv1.ProviderConfigurationReference {
	return &runnerv1.ProviderConfigurationReference{
		ProviderId: configuration.ProviderID, SchemaDigest: configuration.SchemaDigest,
		SecretReference: configuration.SecretReference, CredentialVersion: configuration.CredentialVersion,
	}
}

func providerConfigurationFromProto(message *runnerv1.ProviderConfigurationReference) (providerv1.ConfigurationReference, error) {
	if message == nil {
		return providerv1.ConfigurationReference{}, errors.New("provider configuration is missing")
	}
	configuration := providerv1.ConfigurationReference{
		ProviderID: message.GetProviderId(), SchemaDigest: message.GetSchemaDigest(),
		SecretReference: message.GetSecretReference(), CredentialVersion: message.GetCredentialVersion(),
	}
	return configuration, configuration.Validate()
}

func providerRequestToProto(request providerv1.Request) *runnerv1.ProviderRequest {
	evidence := make([]*runnerv1.EvidenceGrantReference, len(request.Evidence))
	for index, grant := range request.Evidence {
		evidence[index] = providerGrantToProto(grant)
	}
	inputs := make([]*runnerv1.ProviderInputReference, len(request.Inputs))
	for index, input := range request.Inputs {
		inputs[index] = &runnerv1.ProviderInputReference{Name: input.Name, Reference: input.Reference}
	}
	return &runnerv1.ProviderRequest{
		Contract: versionToProto(request.Contract.Major, request.Contract.Minor), AttemptId: request.AttemptID,
		ProviderId: request.ProviderID, TenantId: request.TenantID, VerificationId: request.VerificationID,
		Check: request.Check, IdempotencyKey: request.IdempotencyKey, CallbackReference: request.CallbackReference,
		Adapter:    providerManifestToProto(providerv1.Manifest{Package: request.Adapter}).GetPackage(),
		Capability: providerCapabilityToProto(request.Capability), Restrictions: providerRestrictionsToProto(request.Restrictions),
		Configuration: providerConfigurationToProto(request.Configuration), Inputs: inputs, Evidence: evidence,
		Deadline: timestamppb.New(request.Deadline), Trace: &runnerv1.TraceContext{Traceparent: request.Trace.Traceparent, Tracestate: request.Trace.Tracestate},
	}
}

func providerRequestFromProto(message *runnerv1.ProviderRequest) (providerv1.Request, error) {
	if message == nil || message.GetAdapter() == nil || message.GetCapability() == nil || message.GetRestrictions() == nil ||
		message.GetConfiguration() == nil || message.GetDeadline() == nil || message.GetTrace() == nil {
		return providerv1.Request{}, errors.New("provider request is incomplete")
	}
	configuration, _ := providerConfigurationFromProto(message.GetConfiguration())
	evidence := make([]providerv1.EvidenceGrantReference, len(message.GetEvidence()))
	for index, grant := range message.GetEvidence() {
		evidence[index] = providerGrantFromProto(grant)
	}
	inputs := make([]providerv1.InputReference, len(message.GetInputs()))
	for index, input := range message.GetInputs() {
		inputs[index] = providerv1.InputReference{Name: input.GetName(), Reference: input.GetReference()}
	}
	request := providerv1.Request{
		Contract: providerVersionFromProto(message.GetContract()), AttemptID: message.GetAttemptId(), ProviderID: message.GetProviderId(),
		TenantID: message.GetTenantId(), VerificationID: message.GetVerificationId(), Check: message.GetCheck(), IdempotencyKey: message.GetIdempotencyKey(),
		CallbackReference: message.GetCallbackReference(),
		Adapter: providerv1.PackageProvenance{
			AdapterID: message.GetAdapter().GetAdapterId(), AdapterVersion: message.GetAdapter().GetAdapterVersion(),
			PackageDigest: message.GetAdapter().GetPackageDigest(), Contract: providerVersionFromProto(message.GetAdapter().GetContract()),
		},
		Capability: providerCapabilityFromProto(message.GetCapability()), Restrictions: providerRestrictionsFromProto(message.GetRestrictions()),
		Configuration: configuration, Inputs: inputs, Evidence: evidence, Deadline: message.GetDeadline().AsTime(),
		Trace: providerv1.TraceContext{Traceparent: message.GetTrace().GetTraceparent(), Tracestate: message.GetTrace().GetTracestate()},
	}
	return request, request.Validate()
}

func providerGrantToProto(grant providerv1.EvidenceGrantReference) *runnerv1.EvidenceGrantReference {
	return &runnerv1.EvidenceGrantReference{
		GrantId: grant.GrantID, RedemptionId: grant.RedemptionID, EvidenceId: grant.EvidenceID,
		Purpose: grant.Purpose, Variant: grant.Variant, ExpiresAt: timestamppb.New(grant.ExpiresAt),
	}
}

func providerGrantFromProto(message *runnerv1.EvidenceGrantReference) providerv1.EvidenceGrantReference {
	if message == nil {
		return providerv1.EvidenceGrantReference{}
	}
	return providerv1.EvidenceGrantReference{
		GrantID: message.GetGrantId(), RedemptionID: message.GetRedemptionId(), EvidenceID: message.GetEvidenceId(),
		Purpose: message.GetPurpose(), Variant: message.GetVariant(), ExpiresAt: message.GetExpiresAt().AsTime(),
	}
}

func providerResultToProto(result providerv1.Result) *runnerv1.ProviderResult {
	signals := make([]*runnerv1.Signal, len(result.Signals))
	for index, signal := range result.Signals {
		signals[index] = &runnerv1.Signal{Name: signal.Name, Outcome: signalOutcomeToProto(string(signal.Outcome)), ReasonCodes: signal.ReasonCodes}
	}
	return &runnerv1.ProviderResult{
		Contract: versionToProto(result.Contract.Major, result.Contract.Minor), AttemptId: result.AttemptID,
		Outcome: resultOutcomeToProto(string(result.Outcome)), Signals: signals,
		Failure: providerFailureToProto(result.Failure), CompletedAt: timestamppb.New(result.CompletedAt),
		Document: providerDocumentToProto(result.Document),
	}
}

func providerResultFromProto(message *runnerv1.ProviderResult) (providerv1.Result, error) {
	if message == nil || message.GetCompletedAt() == nil {
		return providerv1.Result{}, errors.New("provider result is incomplete")
	}
	signals := make([]providerv1.Signal, len(message.GetSignals()))
	for index, signal := range message.GetSignals() {
		signals[index] = providerv1.Signal{Name: signal.GetName(), Outcome: providerv1.SignalOutcome(signalOutcomeFromProto(signal.GetOutcome())), ReasonCodes: signal.GetReasonCodes()}
	}
	document, err := providerDocumentFromProto(message.GetDocument())
	if err != nil {
		return providerv1.Result{}, err
	}
	result := providerv1.Result{
		Contract: providerVersionFromProto(message.GetContract()), AttemptID: message.GetAttemptId(),
		Outcome: providerv1.ResultOutcome(resultOutcomeFromProto(message.GetOutcome())), Signals: signals,
		Failure: providerFailureFromProto(message.GetFailure()), Document: document, CompletedAt: message.GetCompletedAt().AsTime(),
	}
	return result, result.Validate()
}

func providerDocumentToProto(observation *providerv1.DocumentObservation) *runnerv1.ProviderDocumentObservation {
	if observation == nil {
		return nil
	}
	fields := make([]*runnerv1.ProviderDocumentField, len(observation.Fields))
	for index, field := range observation.Fields {
		fields[index] = &runnerv1.ProviderDocumentField{Name: field.Name, Value: field.Value}
	}
	return &runnerv1.ProviderDocumentObservation{
		MrzLines: observation.MRZLines, BarcodePayload: observation.BarcodePayload, Fields: fields,
	}
}

func providerDocumentFromProto(message *runnerv1.ProviderDocumentObservation) (*providerv1.DocumentObservation, error) {
	if message == nil {
		return nil, nil
	}
	observation := &providerv1.DocumentObservation{
		MRZLines: message.GetMrzLines(), BarcodePayload: message.GetBarcodePayload(),
	}
	for _, field := range message.GetFields() {
		if field == nil {
			return nil, errors.New("provider document field is missing")
		}
		observation.Fields = append(observation.Fields, providerv1.DocumentField{Name: field.GetName(), Value: field.GetValue()})
	}
	if err := observation.Validate(); err != nil {
		return nil, err
	}
	return observation, nil
}

func providerFailureToProto(failure *providerv1.Failure) *runnerv1.Failure {
	if failure == nil {
		return nil
	}
	return &runnerv1.Failure{Class: string(failure.Class), Code: failure.Code, Retry: retryToProto(string(failure.Retry)), RetryAfter: durationpb.New(failure.RetryAfter)}
}

func providerFailureFromProto(message *runnerv1.Failure) *providerv1.Failure {
	if message == nil {
		return nil
	}
	return &providerv1.Failure{Class: providerv1.FailureClass(message.GetClass()), Code: message.GetCode(), Retry: providerv1.RetryDisposition(retryFromProto(message.GetRetry())), RetryAfter: message.GetRetryAfter().AsDuration()}
}

func providerHealthToProto(health providerv1.Health) *runnerv1.Health {
	return &runnerv1.Health{State: healthToProto(string(health.State)), Code: health.Code, CheckedAt: timestamppb.New(health.CheckedAt)}
}

func providerHealthFromProto(message *runnerv1.Health) (providerv1.Health, error) {
	if message == nil || message.GetCheckedAt() == nil {
		return providerv1.Health{}, errors.New("provider health is incomplete")
	}
	health := providerv1.Health{State: providerv1.HealthState(healthFromProto(message.GetState())), Code: message.GetCode(), CheckedAt: message.GetCheckedAt().AsTime()}
	return health, health.Validate()
}

func providerVersionFromProto(message *runnerv1.Version) providerv1.Version {
	if message == nil {
		return providerv1.Version{}
	}
	return providerv1.Version{Major: boundedUint16(message.GetMajor()), Minor: boundedUint16(message.GetMinor())}
}

func boundedContext(ctx context.Context, deadline time.Time) (context.Context, context.CancelFunc) {
	if existing, ok := ctx.Deadline(); ok && existing.Before(deadline) {
		return context.WithDeadline(ctx, existing)
	}
	return context.WithDeadline(ctx, deadline)
}

func exactExecutionContext(ctx context.Context, requested time.Time) (context.Context, context.CancelFunc, error) {
	transportDeadline, ok := ctx.Deadline()
	if !ok || !requested.After(time.Now()) || requested.After(transportDeadline.Add(time.Second)) {
		return nil, nil, status.Error(codes.InvalidArgument, "runner execution deadline is invalid")
	}
	executionContext, cancel := boundedContext(ctx, requested)
	return executionContext, cancel, nil
}

func executionStatus(ctx context.Context, fallback string) error {
	switch ctx.Err() {
	case context.Canceled:
		return status.Error(codes.Canceled, "runner execution cancelled")
	case context.DeadlineExceeded:
		return status.Error(codes.DeadlineExceeded, "runner execution deadline exceeded")
	default:
		return status.Error(codes.Internal, fallback)
	}
}
