package runner

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"

	"google.golang.org/grpc/credentials"
)

// RotatingServerTLS serves the current validated server identity through a
// certificate callback. Rotation affects only new handshakes: an established
// connection keeps the identity it negotiated, so no in-flight request is
// dropped while a replacement certificate is published.
type RotatingServerTLS struct {
	mutex     sync.RWMutex
	identity  *tls.Certificate
	transport credentials.TransportCredentials
}

// NewRotatingServerTLSCredentials returns transport credentials that always
// serve the current identity. An uninitialised identity is permitted so the
// caller can prime the identity from a secret reference before serving; a
// handshake before the first rotation fails closed.
func NewRotatingServerTLSCredentials(identity tls.Certificate) (*RotatingServerTLS, error) {
	rotating := &RotatingServerTLS{}
	if len(identity.Certificate) > 0 || identity.PrivateKey != nil {
		if err := rotating.Rotate(identity); err != nil {
			return nil, err
		}
	}
	rotating.transport = credentials.NewTLS(&tls.Config{
		MinVersion: tls.VersionTLS12,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			rotating.mutex.RLock()
			current := rotating.identity
			rotating.mutex.RUnlock()
			if current == nil {
				return nil, errors.New("runner server TLS identity is unavailable")
			}
			return current, nil
		},
	})

	return rotating, nil
}

// Credentials returns the transport credentials consumed by NewServer.
func (rotating *RotatingServerTLS) Credentials() credentials.TransportCredentials {
	if rotating == nil {
		return nil
	}
	return rotating.transport
}

// Rotate publishes a replacement identity for subsequent handshakes.
func (rotating *RotatingServerTLS) Rotate(identity tls.Certificate) error {
	if rotating == nil || len(identity.Certificate) == 0 || identity.PrivateKey == nil {
		return errors.New("runner server TLS identity is incomplete")
	}
	rotating.mutex.Lock()
	rotating.identity = &identity
	rotating.mutex.Unlock()

	return nil
}

// RotatableBearerCredential is a gRPC per-RPC credential whose value can be
// replaced without reconnecting, so a rotated credential takes effect on the
// next call.
type RotatableBearerCredential struct {
	mutex      sync.RWMutex
	credential string
}

var _ credentials.PerRPCCredentials = (*RotatableBearerCredential)(nil)

// NewRotatableBearerCredential validates the initial credential.
func NewRotatableBearerCredential(credential string) (*RotatableBearerCredential, error) {
	if err := validateCredential(credential); err != nil {
		return nil, err
	}
	return &RotatableBearerCredential{credential: credential}, nil
}

// Replace validates and publishes a replacement credential.
func (credential *RotatableBearerCredential) Replace(value string) error {
	if credential == nil || validateCredential(value) != nil {
		return errInvalidCredential
	}
	credential.mutex.Lock()
	credential.credential = value
	credential.mutex.Unlock()

	return nil
}

// GetRequestMetadata returns the standard bearer metadata for the current value.
func (credential *RotatableBearerCredential) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	if credential == nil {
		return nil, errInvalidCredential
	}
	credential.mutex.RLock()
	value := credential.credential
	credential.mutex.RUnlock()
	if validateCredential(value) != nil {
		return nil, errInvalidCredential
	}
	return map[string]string{authorizationMetadata: "Bearer " + value}, nil
}

// RequireTransportSecurity prevents the credential crossing plaintext TCP.
func (*RotatableBearerCredential) RequireTransportSecurity() bool { return true }
