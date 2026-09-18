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

// Advance performs one bounded async operation without extending its pinned deadline.
func (server *ProviderServer) Advance(ctx context.Context, wire *runnerv1.ProviderRunnerServiceAdvanceRequest) (*runnerv1.ProviderRunnerServiceAdvanceResponse, error) {
	request, err := providerRequestFromProto(wire.GetRequest())
	if err != nil || !request.Deadline.After(time.Now()) {
		return nil, status.Error(codes.InvalidArgument, "provider operation is invalid or expired")
	}
	adapter, ok := server.adapter.(providerv1.Advancer)
	if !ok {
		return nil, status.Error(codes.Unimplemented, "async provider unavailable")
	}
	ctx, cancel := boundedContext(ctx, minDeadline(request.Deadline, time.Now().Add(30*time.Second)))
	defer cancel()
	progress, err := adapter.Advance(ctx, request, wire.GetResume())
	if err != nil {
		return nil, executionStatus(ctx, "provider operation unavailable")
	}
	if progress.ValidateForRequest(request) != nil {
		return nil, status.Error(codes.Internal, "provider progress invalid")
	}
	response := &runnerv1.ProviderRunnerServiceAdvanceResponse{ProviderJobId: progress.ProviderJobID}
	if progress.Result != nil {
		response.Result = providerResultToProto(*progress.Result)
	}
	if proto.Size(response) > providerv1.MaxResultBytes || server.validator.Validate(response) != nil {
		return nil, status.Error(codes.Internal, "provider progress invalid")
	}
	return response, nil
}

// Advance uses short RPCs while preserving the longer durable operation deadline.
func (client *ProviderClient) Advance(ctx context.Context, request providerv1.Request, resume bool) (providerv1.Progress, error) {
	if request.Validate() != nil || !request.Deadline.After(time.Now()) {
		return providerv1.Progress{}, errors.New("provider operation invalid or expired")
	}
	ctx, cancel := boundedContext(ctx, minDeadline(request.Deadline, time.Now().Add(30*time.Second)))
	defer cancel()
	response, err := client.client.Advance(ctx, &runnerv1.ProviderRunnerServiceAdvanceRequest{Request: providerRequestToProto(request), Resume: resume})
	if err != nil {
		return providerv1.Progress{}, errors.New("provider operation unavailable")
	}
	if proto.Size(response) > providerv1.MaxResultBytes {
		return providerv1.Progress{}, errors.New("provider progress exceeds limit")
	}
	progress := providerv1.Progress{ProviderJobID: response.GetProviderJobId()}
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
func minDeadline(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
