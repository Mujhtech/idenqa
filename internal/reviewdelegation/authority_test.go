package reviewdelegation

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestVerifierAcceptsExactBoundAuthority(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	verifier := verifierFixture(t, public, now)
	envelope := signedEnvelope(t, private, now)

	claims, err := verifier.Verify(envelope)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if claims.ActorID != "usr_1" || claims.TargetID != "rev_1" || claims.Operation != "claim" {
		t.Fatalf("Verify() claims = %#v", claims)
	}
}

func TestVerifierRejectsTamperingAndBindingFailures(t *testing.T) {
	now := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	tests := []struct {
		name   string
		mutate func(*Envelope)
		want   error
	}{
		{name: "tampered actor", mutate: func(envelope *Envelope) { envelope.Claims.ActorID = "usr_2" }, want: ErrForbidden},
		{name: "wrong deployment", mutate: func(envelope *Envelope) { envelope.Claims.Scope.DeploymentID = "dep_2" }, want: ErrForbidden},
		{name: "permission does not match operation", mutate: func(envelope *Envelope) { envelope.Claims.Permission = "review:find" }, want: ErrForbidden},
		{name: "expired", mutate: func(envelope *Envelope) { envelope.Claims.ExpiresAt = now }, want: ErrExpired},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			envelope := signedEnvelope(t, private, now)
			test.mutate(&envelope)
			_, err := verifierFixture(t, public, now).Verify(envelope)
			if !errors.Is(err, test.want) {
				t.Fatalf("Verify() error = %v, want %v", err, test.want)
			}
		})
	}
}

func verifierFixture(t *testing.T, public ed25519.PublicKey, now time.Time) *Verifier {
	t.Helper()
	verifier, err := NewVerifier(map[string]string{"cloud_key_1": base64.StdEncoding.EncodeToString(public)}, Binding{
		OrganisationID: "org_1", EnvironmentID: "env_1", DeploymentID: "dep_1", Region: "eu-west-1",
	}, time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewVerifier() error = %v", err)
	}
	return verifier
}

func signedEnvelope(t *testing.T, private ed25519.PrivateKey, now time.Time) Envelope {
	t.Helper()
	requestDigest := sha256.Sum256([]byte("request"))
	claims := Claims{Version: Version, Audience: Audience, Intent: Intent{
		CommandID: "rcm_1", Scope: Scope{
			OrganisationID: "org_1", TenantID: "ten_1", EnvironmentID: "env_1",
			DeploymentID: "dep_1", Region: "eu-west-1",
		}, ActorID: "usr_1", Permission: "review:claim", Purpose: "review-case-operation",
		TargetKind: "review_case", TargetID: "rev_1", Operation: "claim", ExpectedVersion: 1,
		ReasonCode: "queue_assignment", RequestDigest: hex.EncodeToString(requestDigest[:]),
		IssuedAt: now, ExpiresAt: now.Add(30 * time.Second),
	}}
	encoded, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	digest := sha256.Sum256(encoded)
	return Envelope{
		KeyID: "cloud_key_1", Algorithm: "Ed25519", Claims: claims,
		Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(private, digest[:])),
	}
}
