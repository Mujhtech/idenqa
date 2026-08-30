// Package runner adapts the public provider and model contracts to an isolated,
// authenticated gRPC transport. Domain packages do not import this package.
package runner

import (
	"context"
	"errors"
	"time"

	"buf.build/go/protovalidate"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	// MaxWireBytes bounds all runner control messages. Results have a separate,
	// stricter 256 KiB public-contract limit checked before transmission.
	MaxWireBytes       = 320 * 1024
	maximumRPCDeadline = 10 * time.Minute
)

// ServerConfig contains transport-owned runner policy.
type ServerConfig struct {
	Credentials     *CredentialSet
	TLS             credentials.TransportCredentials
	MaximumDeadline time.Duration
	TracerProvider  trace.TracerProvider
	Propagator      propagation.TextMapPropagator
}

// NewServer constructs a bounded runner server with mandatory TLS credentials.
func NewServer(configuration ServerConfig) (*grpc.Server, error) {
	if configuration.Credentials == nil {
		return nil, errors.New("runner server credentials are required")
	}
	if configuration.TLS == nil {
		return nil, errors.New("runner server TLS credentials are required")
	}
	if configuration.MaximumDeadline <= 0 || configuration.MaximumDeadline > maximumRPCDeadline {
		return nil, errors.New("runner maximum deadline must be within zero and ten minutes")
	}

	validator, err := protovalidate.New()
	if err != nil {
		return nil, errors.New("construct runner protocol validator")
	}
	serverOptions := []grpc.ServerOption{
		grpc.Creds(configuration.TLS),
		grpc.MaxRecvMsgSize(MaxWireBytes),
		grpc.MaxSendMsgSize(MaxWireBytes),
		grpc.ChainUnaryInterceptor(
			configuration.Credentials.unaryInterceptor,
			deadlineInterceptor(configuration.MaximumDeadline),
			validationInterceptor(validator),
		),
	}
	telemetryOptions := make([]otelgrpc.Option, 0, 2)
	if configuration.TracerProvider != nil {
		telemetryOptions = append(telemetryOptions, otelgrpc.WithTracerProvider(configuration.TracerProvider))
	}
	if configuration.Propagator != nil {
		telemetryOptions = append(telemetryOptions, otelgrpc.WithPropagators(configuration.Propagator))
	}
	serverOptions = append(serverOptions, grpc.StatsHandler(otelgrpc.NewServerHandler(telemetryOptions...)))
	return grpc.NewServer(serverOptions...), nil
}

func deadlineInterceptor(maximum time.Duration) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "runner deadline is required")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return nil, status.Error(codes.DeadlineExceeded, "runner deadline exceeded")
		}
		if remaining > maximum+time.Second {
			return nil, status.Error(codes.InvalidArgument, "runner deadline exceeds policy")
		}
		return handler(ctx, request)
	}
}

func validationInterceptor(validator protovalidate.Validator) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		request any,
		_ *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		message, ok := request.(proto.Message)
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "runner request is not a protocol message")
		}
		if err := validator.Validate(message); err != nil {
			return nil, status.Error(codes.InvalidArgument, "runner request is invalid")
		}
		return handler(ctx, request)
	}
}
