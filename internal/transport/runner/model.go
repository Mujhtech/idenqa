package runner

import (
	"context"
	"errors"
	"time"

	"buf.build/go/protovalidate"
	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ModelServer adapts a transport-independent model to gRPC.
type ModelServer struct {
	runnerv1.UnimplementedModelRunnerServiceServer
	adapter   modelv1.Adapter
	validator protovalidate.Validator
}

// NewModelServer constructs a model service adapter.
func NewModelServer(adapter modelv1.Adapter) (*ModelServer, error) {
	if adapter == nil {
		return nil, errors.New("model runner adapter is required")
	}
	validator, err := protovalidate.New()
	if err != nil {
		return nil, errors.New("construct model runner validator")
	}
	return &ModelServer{adapter: adapter, validator: validator}, nil
}

// Manifest returns the validated immutable model manifest.
func (server *ModelServer) Manifest(
	ctx context.Context,
	_ *runnerv1.ModelRunnerServiceManifestRequest,
) (*runnerv1.ModelRunnerServiceManifestResponse, error) {
	manifest, err := server.adapter.Manifest(ctx)
	if err != nil || manifest.Validate() != nil {
		return nil, status.Error(codes.Internal, "model manifest unavailable")
	}
	response := &runnerv1.ModelRunnerServiceManifestResponse{Manifest: modelManifestToProto(manifest)}
	if err := server.validator.Validate(response); err != nil {
		return nil, status.Error(codes.Internal, "model manifest unavailable")
	}
	return response, nil
}

// ValidateConfiguration validates a reference without returning adapter details.
func (server *ModelServer) ValidateConfiguration(
	ctx context.Context,
	request *runnerv1.ModelRunnerServiceValidateConfigurationRequest,
) (*runnerv1.ModelRunnerServiceValidateConfigurationResponse, error) {
	configuration, err := modelConfigurationFromProto(request.GetConfiguration())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "model configuration reference is invalid")
	}
	result := &runnerv1.ValidationResult{Valid: true}
	if err := server.adapter.ValidateConfiguration(ctx, configuration); err != nil {
		result.Valid = false
		result.Code = "invalid_configuration"
	}
	return &runnerv1.ModelRunnerServiceValidateConfigurationResponse{Validation: result}, nil
}

// Execute validates, executes, binds, and size-checks one model result.
func (server *ModelServer) Execute(
	ctx context.Context,
	request *runnerv1.ModelRunnerServiceExecuteRequest,
) (*runnerv1.ModelRunnerServiceExecuteResponse, error) {
	domainRequest, err := modelRequestFromProto(request.GetRequest())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "model request is invalid")
	}
	executionContext, cancel, err := exactExecutionContext(ctx, domainRequest.Deadline)
	if err != nil {
		return nil, err
	}
	defer cancel()
	result, err := server.adapter.Execute(executionContext, domainRequest)
	if err != nil {
		return nil, executionStatus(executionContext, "model execution failed")
	}
	if err := result.ValidateForRequest(domainRequest); err != nil {
		return nil, status.Error(codes.Internal, "model result is invalid")
	}
	response := &runnerv1.ModelRunnerServiceExecuteResponse{Result: modelResultToProto(result)}
	if proto.Size(response.GetResult()) > modelv1.MaxResultBytes {
		return nil, status.Error(codes.ResourceExhausted, "model result exceeds limit")
	}
	if err := server.validator.Validate(response); err != nil {
		return nil, status.Error(codes.Internal, "model result is invalid")
	}
	return response, nil
}

// Health returns only the safe public health classification.
func (server *ModelServer) Health(
	ctx context.Context,
	_ *runnerv1.ModelRunnerServiceHealthRequest,
) (*runnerv1.ModelRunnerServiceHealthResponse, error) {
	health, err := server.adapter.Health(ctx)
	if err != nil || health.Validate() != nil {
		return nil, status.Error(codes.Unavailable, "model health unavailable")
	}
	return &runnerv1.ModelRunnerServiceHealthResponse{Health: modelHealthToProto(health)}, nil
}

// ModelClient adapts the generated client back to the public model port.
type ModelClient struct {
	client runnerv1.ModelRunnerServiceClient
}

var _ modelv1.Adapter = (*ModelClient)(nil)

// NewModelClient constructs a transport-independent model client.
func NewModelClient(client runnerv1.ModelRunnerServiceClient) (*ModelClient, error) {
	if client == nil {
		return nil, errors.New("model runner client is required")
	}
	return &ModelClient{client: client}, nil
}

// Manifest requests the model manifest under a bounded control deadline.
func (client *ModelClient) Manifest(ctx context.Context) (modelv1.Manifest, error) {
	callContext, cancel := boundedContext(ctx, time.Now().Add(runnerControlTimeout))
	defer cancel()
	response, err := client.client.Manifest(callContext, &runnerv1.ModelRunnerServiceManifestRequest{})
	if err != nil {
		return modelv1.Manifest{}, errors.New("model runner manifest failed")
	}
	manifest, err := modelManifestFromProto(response.GetManifest())
	if err != nil {
		return modelv1.Manifest{}, errors.New("model runner manifest is invalid")
	}
	return manifest, nil
}

// ValidateConfiguration validates a model reference remotely.
func (client *ModelClient) ValidateConfiguration(ctx context.Context, configuration modelv1.ConfigurationReference) error {
	if err := configuration.Validate(); err != nil {
		return err
	}
	callContext, cancel := boundedContext(ctx, time.Now().Add(runnerControlTimeout))
	defer cancel()
	response, err := client.client.ValidateConfiguration(callContext, &runnerv1.ModelRunnerServiceValidateConfigurationRequest{
		Configuration: modelConfigurationToProto(configuration),
	})
	if err != nil || response.GetValidation() == nil || !response.GetValidation().GetValid() {
		return errors.New("model runner rejected configuration")
	}
	return nil
}

// Execute sends one validated request under its exact deadline.
func (client *ModelClient) Execute(ctx context.Context, request modelv1.Request) (modelv1.Result, error) {
	if err := request.Validate(); err != nil {
		return modelv1.Result{}, err
	}
	callContext, cancel := boundedContext(ctx, request.Deadline)
	defer cancel()
	response, err := client.client.Execute(callContext, &runnerv1.ModelRunnerServiceExecuteRequest{Request: modelRequestToProto(request)})
	if err != nil {
		return modelv1.Result{}, errors.New("model runner execution failed")
	}
	if proto.Size(response.GetResult()) > modelv1.MaxResultBytes {
		return modelv1.Result{}, errors.New("model runner result exceeds limit")
	}
	result, err := modelResultFromProto(response.GetResult())
	if err != nil || result.ValidateForRequest(request) != nil {
		return modelv1.Result{}, errors.New("model runner result is invalid")
	}
	return result, nil
}

// Health requests safe model health under a bounded control deadline.
func (client *ModelClient) Health(ctx context.Context) (modelv1.Health, error) {
	callContext, cancel := boundedContext(ctx, time.Now().Add(runnerControlTimeout))
	defer cancel()
	response, err := client.client.Health(callContext, &runnerv1.ModelRunnerServiceHealthRequest{})
	if err != nil {
		return modelv1.Health{}, errors.New("model runner health failed")
	}
	health, err := modelHealthFromProto(response.GetHealth())
	if err != nil {
		return modelv1.Health{}, errors.New("model runner health is invalid")
	}
	return health, nil
}

func modelManifestToProto(manifest modelv1.Manifest) *runnerv1.ModelManifest {
	capabilities := make([]*runnerv1.ModelCapability, len(manifest.Capabilities))
	for index, capability := range manifest.Capabilities {
		capabilities[index] = modelCapabilityToProto(capability)
	}
	return &runnerv1.ModelManifest{
		Provenance: modelProvenanceToProto(manifest.Provenance), Capabilities: capabilities,
		Restrictions: modelRestrictionsToProto(manifest.Restrictions),
	}
}

func modelManifestFromProto(message *runnerv1.ModelManifest) (modelv1.Manifest, error) {
	if message == nil || message.GetProvenance() == nil || message.GetRestrictions() == nil {
		return modelv1.Manifest{}, errors.New("model manifest is incomplete")
	}
	capabilities := make([]modelv1.Capability, len(message.GetCapabilities()))
	for index, capability := range message.GetCapabilities() {
		capabilities[index] = modelCapabilityFromProto(capability)
	}
	manifest := modelv1.Manifest{
		Provenance: modelProvenanceFromProto(message.GetProvenance()), Capabilities: capabilities,
		Restrictions: modelRestrictionsFromProto(message.GetRestrictions()),
	}
	return manifest, manifest.Validate()
}

func modelProvenanceToProto(provenance modelv1.Provenance) *runnerv1.ModelProvenance {
	return &runnerv1.ModelProvenance{
		ModelId: provenance.ModelID, ModelVersion: provenance.ModelVersion, ModelDigest: provenance.ModelDigest,
		RuntimeDigest: provenance.RuntimeDigest, PreprocessingDigest: provenance.PreprocessingDigest,
		OutputSchemaDigest: provenance.OutputSchemaDigest, Contract: versionToProto(provenance.Contract.Major, provenance.Contract.Minor),
	}
}

func modelProvenanceFromProto(message *runnerv1.ModelProvenance) modelv1.Provenance {
	if message == nil {
		return modelv1.Provenance{}
	}
	return modelv1.Provenance{
		ModelID: message.GetModelId(), ModelVersion: message.GetModelVersion(), ModelDigest: message.GetModelDigest(),
		RuntimeDigest: message.GetRuntimeDigest(), PreprocessingDigest: message.GetPreprocessingDigest(),
		OutputSchemaDigest: message.GetOutputSchemaDigest(), Contract: modelVersionFromProto(message.GetContract()),
	}
}

func modelCapabilityToProto(capability modelv1.Capability) *runnerv1.ModelCapability {
	return &runnerv1.ModelCapability{
		Evaluation: capability.Evaluation, AcceptedEvidence: capability.AcceptedEvidence,
		RequiredAssurances: capability.RequiredAssurances, OutputSignals: capability.OutputSignals,
	}
}

func modelCapabilityFromProto(message *runnerv1.ModelCapability) modelv1.Capability {
	if message == nil {
		return modelv1.Capability{}
	}
	return modelv1.Capability{
		Evaluation: message.GetEvaluation(), AcceptedEvidence: message.GetAcceptedEvidence(),
		RequiredAssurances: message.GetRequiredAssurances(), OutputSignals: message.GetOutputSignals(),
	}
}

func modelRestrictionsToProto(restrictions modelv1.Restrictions) *runnerv1.ModelRestrictions {
	return &runnerv1.ModelRestrictions{
		NetworkAllowed: restrictions.NetworkAllowed, MaximumGrants: uint32(restrictions.MaximumGrants),
		MaximumInputBytes: restrictions.MaximumInputBytes, MaximumResultSize: restrictions.MaximumResultSize,
		MaximumDuration: durationpb.New(restrictions.MaximumDuration),
	}
}

func modelRestrictionsFromProto(message *runnerv1.ModelRestrictions) modelv1.Restrictions {
	if message == nil {
		return modelv1.Restrictions{}
	}
	return modelv1.Restrictions{
		NetworkAllowed: message.GetNetworkAllowed(), MaximumGrants: boundedUint16(message.GetMaximumGrants()),
		MaximumInputBytes: message.GetMaximumInputBytes(), MaximumResultSize: message.GetMaximumResultSize(),
		MaximumDuration: message.GetMaximumDuration().AsDuration(),
	}
}

func modelConfigurationToProto(configuration modelv1.ConfigurationReference) *runnerv1.ModelConfigurationReference {
	return &runnerv1.ModelConfigurationReference{
		ModelRegistrationId: configuration.ModelID, ConfigurationDigest: configuration.ConfigurationDigest,
		ConfigurationReference: configuration.ConfigurationRef,
	}
}

func modelConfigurationFromProto(message *runnerv1.ModelConfigurationReference) (modelv1.ConfigurationReference, error) {
	if message == nil {
		return modelv1.ConfigurationReference{}, errors.New("model configuration is missing")
	}
	configuration := modelv1.ConfigurationReference{
		ModelID: message.GetModelRegistrationId(), ConfigurationDigest: message.GetConfigurationDigest(),
		ConfigurationRef: message.GetConfigurationReference(),
	}
	return configuration, configuration.Validate()
}

func modelRequestToProto(request modelv1.Request) *runnerv1.ModelRequest {
	evidence := make([]*runnerv1.EvidenceGrantReference, len(request.Evidence))
	for index, grant := range request.Evidence {
		evidence[index] = modelGrantToProto(grant)
	}
	return &runnerv1.ModelRequest{
		Contract: versionToProto(request.Contract.Major, request.Contract.Minor), AttemptId: request.AttemptID,
		ModelRegistrationId: request.ModelID, TenantId: request.TenantID, VerificationId: request.VerificationID,
		Evaluation: request.Evaluation, IdempotencyKey: request.IdempotencyKey, Provenance: modelProvenanceToProto(request.Provenance),
		Capability: modelCapabilityToProto(request.Capability), Restrictions: modelRestrictionsToProto(request.Restrictions),
		Configuration: modelConfigurationToProto(request.Configuration), Evidence: evidence,
		Deadline: timestamppb.New(request.Deadline), Trace: &runnerv1.TraceContext{Traceparent: request.Trace.Traceparent, Tracestate: request.Trace.Tracestate},
	}
}

func modelRequestFromProto(message *runnerv1.ModelRequest) (modelv1.Request, error) {
	if message == nil || message.GetProvenance() == nil || message.GetCapability() == nil || message.GetRestrictions() == nil ||
		message.GetConfiguration() == nil || message.GetDeadline() == nil || message.GetTrace() == nil {
		return modelv1.Request{}, errors.New("model request is incomplete")
	}
	configuration, _ := modelConfigurationFromProto(message.GetConfiguration())
	evidence := make([]modelv1.EvidenceGrantReference, len(message.GetEvidence()))
	for index, grant := range message.GetEvidence() {
		evidence[index] = modelGrantFromProto(grant)
	}
	request := modelv1.Request{
		Contract: modelVersionFromProto(message.GetContract()), AttemptID: message.GetAttemptId(), ModelID: message.GetModelRegistrationId(),
		TenantID: message.GetTenantId(), VerificationID: message.GetVerificationId(), Evaluation: message.GetEvaluation(), IdempotencyKey: message.GetIdempotencyKey(),
		Provenance: modelProvenanceFromProto(message.GetProvenance()), Capability: modelCapabilityFromProto(message.GetCapability()),
		Restrictions: modelRestrictionsFromProto(message.GetRestrictions()), Configuration: configuration, Evidence: evidence,
		Deadline: message.GetDeadline().AsTime(), Trace: modelv1.TraceContext{Traceparent: message.GetTrace().GetTraceparent(), Tracestate: message.GetTrace().GetTracestate()},
	}
	return request, request.Validate()
}

func modelGrantToProto(grant modelv1.EvidenceGrantReference) *runnerv1.EvidenceGrantReference {
	return &runnerv1.EvidenceGrantReference{
		GrantId: grant.GrantID, RedemptionId: grant.RedemptionID, EvidenceId: grant.EvidenceID,
		Purpose: grant.Purpose, Variant: grant.Variant, ExpiresAt: timestamppb.New(grant.ExpiresAt),
	}
}

func modelGrantFromProto(message *runnerv1.EvidenceGrantReference) modelv1.EvidenceGrantReference {
	if message == nil {
		return modelv1.EvidenceGrantReference{}
	}
	return modelv1.EvidenceGrantReference{
		GrantID: message.GetGrantId(), RedemptionID: message.GetRedemptionId(), EvidenceID: message.GetEvidenceId(),
		Purpose: message.GetPurpose(), Variant: message.GetVariant(), ExpiresAt: message.GetExpiresAt().AsTime(),
	}
}

func modelResultToProto(result modelv1.Result) *runnerv1.ModelResult {
	signals := make([]*runnerv1.Signal, len(result.Signals))
	for index, signal := range result.Signals {
		signals[index] = &runnerv1.Signal{Name: signal.Name, Outcome: signalOutcomeToProto(string(signal.Outcome)), ReasonCodes: signal.ReasonCodes}
	}
	return &runnerv1.ModelResult{
		Contract: versionToProto(result.Contract.Major, result.Contract.Minor), AttemptId: result.AttemptID,
		Outcome: resultOutcomeToProto(string(result.Outcome)), Signals: signals,
		Failure: modelFailureToProto(result.Failure), CompletedAt: timestamppb.New(result.CompletedAt),
	}
}

func modelResultFromProto(message *runnerv1.ModelResult) (modelv1.Result, error) {
	if message == nil || message.GetCompletedAt() == nil {
		return modelv1.Result{}, errors.New("model result is incomplete")
	}
	signals := make([]modelv1.Signal, len(message.GetSignals()))
	for index, signal := range message.GetSignals() {
		signals[index] = modelv1.Signal{Name: signal.GetName(), Outcome: modelv1.SignalOutcome(signalOutcomeFromProto(signal.GetOutcome())), ReasonCodes: signal.GetReasonCodes()}
	}
	result := modelv1.Result{
		Contract: modelVersionFromProto(message.GetContract()), AttemptID: message.GetAttemptId(),
		Outcome: modelv1.ResultOutcome(resultOutcomeFromProto(message.GetOutcome())), Signals: signals,
		Failure: modelFailureFromProto(message.GetFailure()), CompletedAt: message.GetCompletedAt().AsTime(),
	}
	return result, result.Validate()
}

func modelFailureToProto(failure *modelv1.Failure) *runnerv1.Failure {
	if failure == nil {
		return nil
	}
	return &runnerv1.Failure{Class: string(failure.Class), Code: failure.Code, Retry: retryToProto(string(failure.Retry)), RetryAfter: durationpb.New(failure.RetryAfter)}
}

func modelFailureFromProto(message *runnerv1.Failure) *modelv1.Failure {
	if message == nil {
		return nil
	}
	return &modelv1.Failure{Class: modelv1.FailureClass(message.GetClass()), Code: message.GetCode(), Retry: modelv1.RetryDisposition(retryFromProto(message.GetRetry())), RetryAfter: message.GetRetryAfter().AsDuration()}
}

func modelHealthToProto(health modelv1.Health) *runnerv1.Health {
	return &runnerv1.Health{State: healthToProto(string(health.State)), Code: health.Code, CheckedAt: timestamppb.New(health.CheckedAt)}
}

func modelHealthFromProto(message *runnerv1.Health) (modelv1.Health, error) {
	if message == nil || message.GetCheckedAt() == nil {
		return modelv1.Health{}, errors.New("model health is incomplete")
	}
	health := modelv1.Health{State: modelv1.HealthState(healthFromProto(message.GetState())), Code: message.GetCode(), CheckedAt: message.GetCheckedAt().AsTime()}
	return health, health.Validate()
}

func modelVersionFromProto(message *runnerv1.Version) modelv1.Version {
	if message == nil {
		return modelv1.Version{}
	}
	return modelv1.Version{Major: boundedUint16(message.GetMajor()), Minor: boundedUint16(message.GetMinor())}
}
