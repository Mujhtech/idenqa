package runner

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const (
	authorizationMetadata = "authorization"
	credentialPrefix      = "idq_wrk_v1_" // #nosec G101 -- public credential format marker, not secret material.
	credentialSecretBytes = 32
)

var errInvalidCredential = errors.New("runner credential is invalid")

// CredentialSet holds digests for the currently accepted rotation window.
// Full credentials are retained only by the gRPC client's per-RPC credential.
type CredentialSet struct {
	digests [][sha256.Size]byte
}

// NewCredentialSet validates and hashes one or more active runner credentials.
func NewCredentialSet(credentials ...string) (*CredentialSet, error) {
	if len(credentials) == 0 || len(credentials) > 2 {
		return nil, errors.New("runner credential set must contain one or two credentials")
	}

	set := &CredentialSet{digests: make([][sha256.Size]byte, 0, len(credentials))}
	for _, credential := range credentials {
		if err := validateCredential(credential); err != nil {
			return nil, err
		}
		digest := sha256.Sum256([]byte(credential))
		for _, existing := range set.digests {
			if subtle.ConstantTimeCompare(existing[:], digest[:]) == 1 {
				return nil, errors.New("runner credential set contains a duplicate")
			}
		}
		set.digests = append(set.digests, digest)
	}

	return set, nil
}

func validateCredential(credential string) error {
	if !strings.HasPrefix(credential, credentialPrefix) {
		return errInvalidCredential
	}
	encoded := strings.TrimPrefix(credential, credentialPrefix)
	secret, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(secret) != credentialSecretBytes {
		return errInvalidCredential
	}
	return nil
}

func (set *CredentialSet) matches(credential string) bool {
	if set == nil || validateCredential(credential) != nil {
		return false
	}
	digest := sha256.Sum256([]byte(credential))
	matched := 0
	for _, accepted := range set.digests {
		matched |= subtle.ConstantTimeCompare(accepted[:], digest[:])
	}
	return matched == 1
}

func (set *CredentialSet) unaryInterceptor(
	ctx context.Context,
	request any,
	_ *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	values := metadata.ValueFromIncomingContext(ctx, authorizationMetadata)
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") ||
		!set.matches(strings.TrimPrefix(values[0], "Bearer ")) {
		return nil, status.Error(codes.Unauthenticated, "runner authentication failed")
	}
	return handler(ctx, request)
}

// BearerCredential attaches one display-once runner credential to each RPC.
// gRPC refuses to send it over a connection without transport security.
type BearerCredential struct {
	credential string
}

var _ credentials.PerRPCCredentials = BearerCredential{}

// NewBearerCredential constructs a TLS-required per-RPC credential.
func NewBearerCredential(credential string) (BearerCredential, error) {
	if err := validateCredential(credential); err != nil {
		return BearerCredential{}, err
	}
	return BearerCredential{credential: credential}, nil
}

// GetRequestMetadata returns the standard bearer metadata.
func (credential BearerCredential) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{authorizationMetadata: "Bearer " + credential.credential}, nil
}

// RequireTransportSecurity prevents the credential crossing plaintext TCP.
func (BearerCredential) RequireTransportSecurity() bool { return true }
