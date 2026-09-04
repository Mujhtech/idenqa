package verification

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestNativeBootstrapBindsVerifiedProofOnce(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 4, 12, 0, 0, 0, time.UTC)
	identifiers, _ := id.NewSystemGenerator()
	tenantID, _ := identifiers.NewTenant()
	tokenID, _ := identifiers.NewCaptureToken()
	scope, _ := tenant.NewScope(tenantID)
	repository := &nativeBindingStub{}
	service, err := NewNativeBootstrapService(repository, nativeClock{now}, []string{"dev.idenqa.fixture"})
	if err != nil {
		t.Fatal(err)
	}
	privateKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	encodedKey, _ := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	capabilities := NativeCapabilities{Platform: "ios", SDKVersion: "0.1.0", ImplementedMethods: []string{"camera"}, CurrentlyAvailableMethods: []string{"camera"}}
	message := nativeProofMessage("dev.idenqa.fixture", "capture-token", now.Unix(), capabilities)
	digest := sha256.Sum256(message)
	signature, _ := ecdsa.SignASN1(rand.Reader, privateKey, digest[:])
	request := NativeBootstrapRequest{ApplicationID: "dev.idenqa.fixture", ProofKey: base64.RawURLEncoding.EncodeToString(encodedKey), ProofCreatedAt: now.Unix(), ProofAlgorithm: "ES256", ProofFormat: "der", Proof: base64.RawURLEncoding.EncodeToString(signature), Capabilities: capabilities}
	attested := request
	attested.Attestation = "unverified-attestation"
	if _, err := service.Bind(t.Context(), CaptureContext{scope: scope, tokenID: tokenID}, "capture-token", attested); !errors.Is(err, ErrSessionNotFound) || repository.calls != 0 {
		t.Fatalf("unsupported attestation error=%v calls=%d", err, repository.calls)
	}
	_, err = service.Bind(t.Context(), CaptureContext{scope: scope, tokenID: tokenID}, "capture-token", request)
	if err != nil || repository.calls != 1 || repository.applicationID != "dev.idenqa.fixture" {
		t.Fatalf("Bind() error=%v calls=%d app=%q", err, repository.calls, repository.applicationID)
	}
	repository.err = access.ErrInvalidCaptureToken
	if _, err := service.Bind(t.Context(), CaptureContext{scope: scope, tokenID: tokenID}, "capture-token", request); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("replay error = %v", err)
	}
}

type nativeClock struct{ now time.Time }

func (clock nativeClock) Now() time.Time { return clock.now }

type nativeBindingStub struct {
	calls         int
	applicationID string
	err           error
}

func (stub *nativeBindingStub) BindNativeApplication(_ context.Context, _ tenant.Scope, _ id.CaptureToken, applicationID, _ string, _ time.Time) error {
	stub.calls++
	stub.applicationID = applicationID
	return stub.err
}
