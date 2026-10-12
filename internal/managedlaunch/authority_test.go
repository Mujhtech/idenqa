package managedlaunch

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestVerifierAcceptsBoundShortLivedAuthority(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	binding := Binding{OrganisationID: "org_1", EnvironmentID: "env_sandbox", DeploymentID: "dep_1", Region: "eu-west-1"}
	verifier, err := NewVerifier(map[string]string{"preview-key-1": base64.StdEncoding.EncodeToString(public)}, binding, "https://cloud.example.test", time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	claims := validClaims(now, binding)
	envelope := signedEnvelope(t, private, claims)

	verified, err := verifier.Verify(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if verified.CommandID != claims.CommandID || verified.WebhookMode != WebhookMode || verified.ProviderMode != ProviderMode {
		t.Fatalf("unexpected verified claims: %#v", verified)
	}
}

func TestVerifierRejectsTamperingWrongBindingAndExpiry(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	binding := Binding{OrganisationID: "org_1", EnvironmentID: "env_sandbox", DeploymentID: "dep_1", Region: "eu-west-1"}
	verifier, err := NewVerifier(map[string]string{"preview-key-1": base64.StdEncoding.EncodeToString(public)}, binding, "https://cloud.example.test", time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}

	tampered := signedEnvelope(t, private, validClaims(now, binding))
	tampered.Claims.ProfileRevision++
	if _, err := verifier.Verify(tampered); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected tampered signature to be forbidden, got %v", err)
	}

	wrongBinding := validClaims(now, binding)
	wrongBinding.Scope.DeploymentID = "dep_other"
	if _, err := verifier.Verify(signedEnvelope(t, private, wrongBinding)); !errors.Is(err, ErrForbidden) {
		t.Fatalf("expected wrong deployment binding to be forbidden, got %v", err)
	}

	expired := validClaims(now.Add(-2*time.Minute), binding)
	expired.ExpiresAt = now.Add(-time.Minute)
	if _, err := verifier.Verify(signedEnvelope(t, private, expired)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected expired authority, got %v", err)
	}
}

func TestVerifierAllowsOnlyBoundedIssuerClockSkew(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	binding := Binding{OrganisationID: "org_1", EnvironmentID: "env_sandbox", DeploymentID: "dep_1", Region: "eu-west-1"}
	verifier, err := NewVerifier(map[string]string{"preview-key-1": base64.StdEncoding.EncodeToString(public)}, binding, "https://cloud.example.test", time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	within := validClaims(now.Add(4*time.Second), binding)
	if _, err := verifier.Verify(signedEnvelope(t, private, within)); err != nil {
		t.Fatalf("bounded clock skew was rejected: %v", err)
	}
	beyond := validClaims(now.Add(6*time.Second), binding)
	if _, err := verifier.Verify(signedEnvelope(t, private, beyond)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expected excessive future issuance to expire, got %v", err)
	}
}

func validClaims(now time.Time, binding Binding) Claims {
	return Claims{
		Version: Version, Issuer: "https://cloud.example.test", Audience: Audience,
		CommandID: "pln_launch123", WorkloadID: "wld_preview123",
		Scope:        Scope{OrganisationID: binding.OrganisationID, TenantID: "tenant_1", EnvironmentID: binding.EnvironmentID, DeploymentID: binding.DeploymentID, Region: binding.Region},
		CoreTenantID: "ten_01HZZZZZZZZZZZZZZZZZZZZZZZ", ActorID: "usr_operator", Purpose: Purpose,
		ProfileID: "prf_01HZZZZZZZZZZZZZZZZZZZZZZZ", ProfileRevision: 7,
		PolicyID: "pol_01HZZZZZZZZZZZZZZZZZZZZZZZ", Origin: "https://capture.example.test", Locale: "en",
		ProviderMode: ProviderMode, WebhookMode: WebhookMode, RequestDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		IssuedAt: now, ExpiresAt: now.Add(30 * time.Second),
	}
}

func signedEnvelope(t *testing.T, private ed25519.PrivateKey, claims Claims) Envelope {
	t.Helper()
	encoded, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	return Envelope{KeyID: "preview-key-1", Algorithm: "Ed25519", Claims: claims, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, digest[:]))}
}
