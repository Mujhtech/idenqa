package runner

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"testing"
	"time"
)

func TestCredentialSetReplacePreservesOverlapWindow(t *testing.T) {
	t.Parallel()

	first := testCredential(1)
	second := testCredential(2)
	third := testCredential(3)
	set, err := NewCredentialSet(first)
	if err != nil {
		t.Fatalf("NewCredentialSet() error = %v", err)
	}
	if !set.matches(first) || set.matches(second) {
		t.Fatal("initial credential window is wrong")
	}

	// A valid replacement that keeps the previous credential open models the
	// documented rotation overlap.
	if err := set.Replace(first, second); err != nil {
		t.Fatalf("Replace(overlap) error = %v", err)
	}
	if !set.matches(first) || !set.matches(second) || set.matches(third) {
		t.Fatal("overlap credential window is wrong")
	}
	if err := set.Replace(second); err != nil {
		t.Fatalf("Replace(second) error = %v", err)
	}
	if set.matches(first) || !set.matches(second) {
		t.Fatal("narrowed credential window is wrong")
	}

	// An invalid replacement must not disturb the accepted window.
	if err := set.Replace("invalid"); err == nil {
		t.Fatal("Replace(invalid) must fail")
	}
	if err := set.Replace(third, third); err == nil {
		t.Fatal("Replace(duplicate) must fail")
	}
	if set.matches(first) || !set.matches(second) {
		t.Fatal("failed Replace disturbed the accepted window")
	}
	empty := NewEmptyCredentialSet()
	if empty.matches(second) {
		t.Fatal("empty credential set accepted a credential before priming")
	}
	if err := empty.Replace(second); err != nil || !empty.matches(second) {
		t.Fatalf("primed empty credential set error = %v", err)
	}
}

func TestRotatingServerTLSRequiresAndPublishesIdentity(t *testing.T) {
	t.Parallel()

	rotating, err := NewRotatingServerTLSCredentials(tls.Certificate{})
	if err != nil {
		t.Fatalf("NewRotatingServerTLSCredentials() error = %v", err)
	}
	if rotating.Credentials() == nil {
		t.Fatal("Credentials() is nil")
	}
	if _, err := rotating.rotateForTest(); err == nil {
		t.Fatal("unprimed identity must fail closed")
	}
	identity := testIdentity(t, 1)
	if err := rotating.Rotate(identity); err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	current, err := rotating.rotateForTest()
	if err != nil || len(current.Certificate) != 1 {
		t.Fatalf("primed identity = %+v, %v", current, err)
	}
	if err := rotating.Rotate(tls.Certificate{}); err == nil {
		t.Fatal("Rotate(incomplete) must fail")
	}
}

func TestRotatableBearerCredentialReplacesInPlace(t *testing.T) {
	t.Parallel()

	first := testCredential(4)
	second := testCredential(5)
	credential, err := NewRotatableBearerCredential(first)
	if err != nil {
		t.Fatalf("NewRotatableBearerCredential() error = %v", err)
	}
	metadata, err := credential.GetRequestMetadata(context.Background())
	if err != nil || metadata[authorizationMetadata] != "Bearer "+first {
		t.Fatalf("GetRequestMetadata() = %v, %v", metadata, err)
	}
	if !credential.RequireTransportSecurity() {
		t.Fatal("rotatable credential must require transport security")
	}
	if err := credential.Replace("invalid"); !errors.Is(err, errInvalidCredential) {
		t.Fatalf("Replace(invalid) error = %v", err)
	}
	if err := credential.Replace(second); err != nil {
		t.Fatalf("Replace(second) error = %v", err)
	}
	metadata, err = credential.GetRequestMetadata(context.Background())
	if err != nil || metadata[authorizationMetadata] != "Bearer "+second {
		t.Fatalf("rotated GetRequestMetadata() = %v, %v", metadata, err)
	}
}

// rotateForTest exposes the certificate callback result for the unexported
// rotating identity.
func (rotating *RotatingServerTLS) rotateForTest() (*tls.Certificate, error) {
	rotating.mutex.RLock()
	identity := rotating.identity
	rotating.mutex.RUnlock()
	if identity == nil {
		return nil, errors.New("runner server TLS identity is unavailable")
	}
	return identity, nil
}

func testIdentity(t *testing.T, serial int64) tls.Certificate {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "runner.test"},
		DNSNames: []string{"runner.test"}, NotBefore: time.Now().Add(-time.Minute),
		NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("CreateCertificate() error = %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("ParseCertificate() error = %v", err)
	}

	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: certificate}
}
