package runner

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// ClientConfig owns secure runner-client transport composition.
type ClientConfig struct {
	Credential     BearerCredential
	TLS            credentials.TransportCredentials
	TracerProvider trace.TracerProvider
	Propagator     propagation.TextMapPropagator
}

// DialOptions returns bounded, TLS-authenticated runner client options.
func DialOptions(configuration ClientConfig) ([]grpc.DialOption, error) {
	if configuration.TLS == nil {
		return nil, errors.New("runner client TLS credentials are required")
	}
	if err := validateCredential(configuration.Credential.credential); err != nil {
		return nil, errors.New("runner client bearer credential is required")
	}
	telemetryOptions := make([]otelgrpc.Option, 0, 2)
	if configuration.TracerProvider != nil {
		telemetryOptions = append(telemetryOptions, otelgrpc.WithTracerProvider(configuration.TracerProvider))
	}
	if configuration.Propagator != nil {
		telemetryOptions = append(telemetryOptions, otelgrpc.WithPropagators(configuration.Propagator))
	}

	return []grpc.DialOption{
		grpc.WithTransportCredentials(configuration.TLS),
		grpc.WithPerRPCCredentials(configuration.Credential),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler(telemetryOptions...)),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(MaxWireBytes),
			grpc.MaxCallSendMsgSize(MaxWireBytes),
		),
	}, nil
}

// LoadServerTLSIdentity loads a complete certificate and key pair from mounted
// files without constructing transport credentials.
func LoadServerTLSIdentity(certificateFile, keyFile string) (tls.Certificate, error) {
	if certificateFile == "" || keyFile == "" {
		return tls.Certificate{}, errors.New("runner server certificate and key are required")
	}
	pair, err := tls.LoadX509KeyPair(certificateFile, keyFile)
	if err != nil {
		return tls.Certificate{}, errors.New("load runner server TLS identity")
	}

	return pair, nil
}

// ParseServerTLSIdentity parses a PEM certificate and key pair supplied by a
// secret provider.
func ParseServerTLSIdentity(certificatePEM, keyPEM []byte) (tls.Certificate, error) {
	if len(certificatePEM) == 0 || len(keyPEM) == 0 {
		return tls.Certificate{}, errors.New("runner server certificate and key are required")
	}
	pair, err := tls.X509KeyPair(certificatePEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, errors.New("parse runner server TLS identity")
	}

	return pair, nil
}

// ServerTLSCredentials loads a complete certificate and key pair and enforces TLS 1.2+.
func ServerTLSCredentials(certificateFile, keyFile string) (credentials.TransportCredentials, error) {
	pair, err := LoadServerTLSIdentity(certificateFile, keyFile)
	if err != nil {
		return nil, err
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion:   tls.VersionTLS12,
		Certificates: []tls.Certificate{pair},
	}), nil
}

// ClientTLSCredentials loads an explicit trust root and expected server name.
func ClientTLSCredentials(certificateAuthorityFile, serverName string) (credentials.TransportCredentials, error) {
	if certificateAuthorityFile == "" || serverName == "" {
		return nil, errors.New("runner client CA and server name are required")
	}
	contents, err := os.ReadFile(certificateAuthorityFile) // #nosec G304 -- operator-selected CA path is this adapter's explicit input.
	if err != nil {
		return nil, errors.New("read runner certificate authority")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(contents) {
		return nil, errors.New("parse runner certificate authority")
	}
	return credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS12,
		RootCAs:    roots,
		ServerName: serverName,
	}), nil
}
