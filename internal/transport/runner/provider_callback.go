package runner

import (
	"context"
	"errors"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	runnerv1 "github.com/Mujhtech/idenqa/internal/gen/proto/runner/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// VerifyProviderCallback authenticates one bounded raw provider callback
// against the exact persisted request. Adapter-owned crypto stays behind
// CallbackVerifier; this transport returns only normalised progress or a
// bounded rejection code.
func (server *ProviderServer) VerifyProviderCallback(
	ctx context.Context,
	wire *runnerv1.ProviderRunnerServiceVerifyProviderCallbackRequest,
) (*runnerv1.ProviderRunnerServiceVerifyProviderCallbackResponse, error) {
	request, err := providerRequestFromProto(wire.GetRequest())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "provider operation is invalid")
	}
	verifier, ok := server.adapter.(providerv1.CallbackVerifier)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "provider callback verification unavailable")
	}
	callback, err := providerCallbackFromProto(wire.GetCallback())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "provider callback is invalid")
	}
	callContext, cancel := boundedContext(ctx, time.Now().Add(runnerControlTimeout))
	defer cancel()
	progress, err := verifier.VerifyCallback(callContext, request, callback)
	if err != nil {
		rejection, rejected := providerv1.AsCallbackRejection(err)
		if !rejected {
			return nil, executionStatus(callContext, "provider callback verification unavailable")
		}
		return &runnerv1.ProviderRunnerServiceVerifyProviderCallbackResponse{
			RejectionCode: string(rejection.Code),
		}, nil
	}
	if progress.ValidateForRequest(request) != nil {
		return nil, status.Error(codes.Internal, "provider callback progress invalid")
	}
	response := &runnerv1.ProviderRunnerServiceVerifyProviderCallbackResponse{
		ProviderJobId: progress.ProviderJobID,
		ReplayId:      progress.ReplayID,
	}
	if progress.Result != nil {
		response.Result = providerResultToProto(*progress.Result)
	}
	if proto.Size(response) > providerv1.MaxResultBytes || server.validator.Validate(response) != nil {
		return nil, status.Error(codes.Internal, "provider callback progress invalid")
	}
	return response, nil
}

// VerifyCallback authenticates a raw callback remotely and returns normalised
// progress or a typed rejection. It never receives or returns raw payloads.
func (client *ProviderClient) VerifyCallback(
	ctx context.Context,
	request providerv1.Request,
	callback providerv1.CallbackEnvelope,
) (providerv1.Progress, error) {
	if request.Validate() != nil || callback.Validate() != nil {
		return providerv1.Progress{}, providerv1.Reject(providerv1.CallbackRejectionMalformed, "callback")
	}
	callContext, cancel := boundedContext(ctx, time.Now().Add(runnerControlTimeout))
	defer cancel()
	response, err := client.client.VerifyProviderCallback(callContext, &runnerv1.ProviderRunnerServiceVerifyProviderCallbackRequest{
		Request:  providerRequestToProto(request),
		Callback: providerCallbackToProto(callback),
	})
	if err != nil {
		return providerv1.Progress{}, errors.New("provider callback verification unavailable")
	}
	if code := response.GetRejectionCode(); code != "" {
		return providerv1.Progress{}, &providerv1.CallbackRejection{Code: providerv1.CallbackRejectionCode(code)}
	}
	if proto.Size(response) > providerv1.MaxResultBytes {
		return providerv1.Progress{}, errors.New("provider callback progress exceeds limit")
	}
	progress := providerv1.Progress{ProviderJobID: response.GetProviderJobId(), ReplayID: response.GetReplayId()}
	if response.GetResult() != nil {
		result, err := providerResultFromProto(response.GetResult())
		if err != nil {
			return providerv1.Progress{}, err
		}
		progress.Result = &result
	}
	if err := progress.ValidateForRequest(request); err != nil {
		return providerv1.Progress{}, err
	}
	return progress, nil
}

func providerCallbackToProto(callback providerv1.CallbackEnvelope) *runnerv1.ProviderCallback {
	headers := make([]*runnerv1.ProviderCallbackHeader, len(callback.Headers))
	for index, header := range callback.Headers {
		headers[index] = &runnerv1.ProviderCallbackHeader{Name: header.Name, Value: header.Value}
	}
	return &runnerv1.ProviderCallback{Method: callback.Method, Headers: headers, Body: string(callback.Body)}
}

func providerCallbackFromProto(message *runnerv1.ProviderCallback) (providerv1.CallbackEnvelope, error) {
	if message == nil {
		return providerv1.CallbackEnvelope{}, errors.New("provider callback is missing")
	}
	headers := make([]providerv1.CallbackHeader, len(message.GetHeaders()))
	for index, header := range message.GetHeaders() {
		if header == nil {
			return providerv1.CallbackEnvelope{}, errors.New("provider callback header is missing")
		}
		headers[index] = providerv1.CallbackHeader{Name: header.GetName(), Value: header.GetValue()}
	}
	envelope := providerv1.CallbackEnvelope{
		Method:  message.GetMethod(),
		Headers: headers,
		Body:    []byte(message.GetBody()),
	}
	return envelope, envelope.Validate()
}
