package verification

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const nativeProofLifetime = 2 * time.Minute

// NativeCapabilities is a bounded SDK capability advertisement, never proof of assurance.
type NativeCapabilities struct {
	Platform, SDKVersion                          string
	ImplementedMethods, CurrentlyAvailableMethods []string
}

// NativeBootstrapRequest is the signed first-redemption input.
type NativeBootstrapRequest struct {
	ApplicationID, ProofKey, ProofAlgorithm, ProofFormat, Proof, Attestation string
	ProofCreatedAt                                                           int64
	Capabilities                                                             NativeCapabilities
}

// NativeBindingRepository atomically consumes one unbound capture token.
type NativeBindingRepository interface {
	BindNativeApplication(context.Context, tenant.Scope, id.CaptureToken, string, string, time.Time) error
}

// NativeAttestationVerifier optionally validates a platform attestation against
// the exact application and proof key. No verifier means attestation is unsupported.
type NativeAttestationVerifier interface {
	VerifyNativeAttestation(context.Context, string, string, string, string) error
}

// NativeBootstrapService verifies proof-of-possession before durable binding.
type NativeBootstrapService struct {
	repository  NativeBindingRepository
	clock       clock.Clock
	allowed     map[string]struct{}
	attestation NativeAttestationVerifier
}

// NewNativeBootstrapService constructs the selected native identity boundary.
func NewNativeBootstrapService(repository NativeBindingRepository, source clock.Clock, allowedApplications []string, attestation ...NativeAttestationVerifier) (*NativeBootstrapService, error) {
	if repository == nil || source == nil || len(allowedApplications) == 0 || len(allowedApplications) > 256 || len(attestation) > 1 || (len(attestation) == 1 && attestation[0] == nil) {
		return nil, errors.New("verification: native bootstrap dependencies are invalid")
	}
	allowed := make(map[string]struct{}, len(allowedApplications))
	for _, applicationID := range allowedApplications {
		if !validNativeToken(applicationID, 255) {
			return nil, errors.New("verification: native application identity is invalid")
		}
		if _, duplicate := allowed[applicationID]; duplicate {
			return nil, errors.New("verification: native application identities contain duplicates")
		}
		allowed[applicationID] = struct{}{}
	}
	service := &NativeBootstrapService{repository: repository, clock: source, allowed: allowed}
	if len(attestation) == 1 {
		service.attestation = attestation[0]
	}
	return service, nil
}

// Bind verifies the single-use request and returns its already authenticated session.
func (service *NativeBootstrapService) Bind(ctx context.Context, authority CaptureContext, presentedToken string, request NativeBootstrapRequest) (Session, error) {
	if service == nil || authority.scope.ID().IsZero() || authority.tokenID.IsZero() || presentedToken == "" {
		return Session{}, ErrSessionNotFound
	}
	if len(presentedToken) > 4096 || len(request.ProofKey) < 40 || len(request.ProofKey) > 512 ||
		len(request.Proof) < 40 || len(request.Proof) > 256 || len(request.Attestation) > 16*1024 {
		return Session{}, ErrSessionNotFound
	}
	if _, ok := service.allowed[request.ApplicationID]; !ok || request.ProofAlgorithm != "ES256" || request.ProofFormat != "der" {
		return Session{}, ErrSessionNotFound
	}
	createdAt := time.Unix(request.ProofCreatedAt, 0).UTC()
	now := service.clock.Now().UTC()
	if createdAt.After(now.Add(30*time.Second)) || createdAt.Before(now.Add(-nativeProofLifetime)) || validateNativeCapabilities(request.Capabilities) != nil {
		return Session{}, ErrSessionNotFound
	}
	publicKeyBytes, err := base64.RawURLEncoding.DecodeString(request.ProofKey)
	if err != nil || len(publicKeyBytes) > 512 {
		return Session{}, ErrSessionNotFound
	}
	publicKey, err := parseP256PublicKey(publicKeyBytes)
	if err != nil {
		return Session{}, ErrSessionNotFound
	}
	signature, err := base64.RawURLEncoding.DecodeString(request.Proof)
	if err != nil || len(signature) > 256 {
		return Session{}, ErrSessionNotFound
	}
	message := nativeProofMessage(request.ApplicationID, presentedToken, request.ProofCreatedAt, request.Capabilities)
	digest := sha256.Sum256(message)
	if !ecdsa.VerifyASN1(publicKey, digest[:], signature) {
		return Session{}, ErrSessionNotFound
	}
	keyDigest := sha256.Sum256(publicKeyBytes)
	encodedKeyDigest := hex.EncodeToString(keyDigest[:])
	if request.Attestation != "" {
		if service.attestation == nil || service.attestation.VerifyNativeAttestation(ctx, request.Capabilities.Platform, request.ApplicationID, encodedKeyDigest, request.Attestation) != nil {
			return Session{}, ErrSessionNotFound
		}
	}
	if err := service.repository.BindNativeApplication(ctx, authority.scope, authority.tokenID, request.ApplicationID, encodedKeyDigest, now); err != nil {
		if errors.Is(err, access.ErrInvalidCaptureToken) {
			return Session{}, ErrSessionNotFound
		}
		return Session{}, fmt.Errorf("bind native capture application: %w", err)
	}
	return authority.session, nil
}

func nativeProofMessage(applicationID, token string, created int64, capabilities NativeCapabilities) []byte {
	tokenDigest := sha256.Sum256([]byte(token))
	capabilityDigest := sha256.Sum256([]byte(canonicalCapabilities(capabilities)))
	return []byte(fmt.Sprintf("idq-native-bootstrap\x00v1\x00%s\x00POST\x00/v1/capture/native/bootstrap\x00%d\x00%s\x00%s", applicationID, created, hex.EncodeToString(tokenDigest[:]), hex.EncodeToString(capabilityDigest[:])))
}

func canonicalCapabilities(value NativeCapabilities) string {
	implemented := append([]string(nil), value.ImplementedMethods...)
	available := append([]string(nil), value.CurrentlyAvailableMethods...)
	slices.Sort(implemented)
	slices.Sort(available)
	return value.Platform + "\x00" + value.SDKVersion + "\x00" + strings.Join(implemented, "\x1f") + "\x00" + strings.Join(available, "\x1f")
}

func validateNativeCapabilities(value NativeCapabilities) error {
	if (value.Platform != "ios" && value.Platform != "android") || !validNativeToken(value.SDKVersion, 64) || len(value.ImplementedMethods) > 64 || len(value.CurrentlyAvailableMethods) > 64 {
		return ErrSessionNotFound
	}
	implemented := make(map[string]struct{}, len(value.ImplementedMethods))
	for _, method := range value.ImplementedMethods {
		if !validNativeToken(method, 128) {
			return ErrSessionNotFound
		}
		implemented[method] = struct{}{}
	}
	if len(implemented) != len(value.ImplementedMethods) {
		return ErrSessionNotFound
	}
	available := make(map[string]struct{}, len(value.CurrentlyAvailableMethods))
	for _, method := range value.CurrentlyAvailableMethods {
		if _, exists := implemented[method]; !exists {
			return ErrSessionNotFound
		}
		available[method] = struct{}{}
	}
	if len(available) != len(value.CurrentlyAvailableMethods) {
		return ErrSessionNotFound
	}
	return nil
}

func validNativeToken(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character > unicode.MaxASCII || (!unicode.IsLetter(character) && !unicode.IsDigit(character) && !strings.ContainsRune("._-", character)) {
			return false
		}
	}
	return true
}

func parseP256PublicKey(encoded []byte) (*ecdsa.PublicKey, error) {
	if parsed, err := x509.ParsePKIXPublicKey(encoded); err == nil {
		if key, ok := parsed.(*ecdsa.PublicKey); ok && key.Curve == elliptic.P256() {
			return key, nil
		}
	}
	key, err := ecdsa.ParseUncompressedPublicKey(elliptic.P256(), encoded)
	if err != nil {
		return nil, errors.New("verification: invalid native proof key")
	}
	return key, nil
}
