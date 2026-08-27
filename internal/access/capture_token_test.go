package access_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestCaptureTokenSignerDeterministicRoundTripAndRedaction(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	credential := captureCredentialFixture(t, now, now.Add(30*time.Minute))
	keys, err := access.NewCaptureTokenKeyring(7, map[access.CaptureTokenKeyVersion][]byte{
		7: bytes.Repeat([]byte{0x41}, 32),
	})
	if err != nil {
		t.Fatalf("NewCaptureTokenKeyring() error = %v", err)
	}
	signer, err := access.NewCaptureTokenSigner(keys, captureTokenClock{now: now})
	if err != nil {
		t.Fatalf("NewCaptureTokenSigner() error = %v", err)
	}
	first, err := signer.Sign(credential)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	second, err := signer.Sign(credential)
	if err != nil {
		t.Fatalf("Sign(second) error = %v", err)
	}
	if first.Reveal() == "" || first.Reveal() != second.Reveal() {
		t.Fatalf("deterministic tokens = %q and %q", first.Reveal(), second.Reveal())
	}
	if first.String() != "[REDACTED]" {
		t.Fatalf("String() = %q", first.String())
	}
	encoded, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), first.Reveal()) {
		t.Fatal("JSON encoding exposed capture bearer token")
	}
	claims, err := signer.Verify(first.Reveal())
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if claims.TokenID.String() != credential.ID().String() ||
		claims.TenantID.String() != credential.TenantID().String() ||
		claims.VerificationID.String() != credential.VerificationID().String() ||
		claims.KeyVersion != credential.KeyVersion() ||
		!claims.IssuedAt.Equal(credential.IssuedAt()) || !claims.ExpiresAt.Equal(credential.ExpiresAt()) {
		t.Fatalf("claims = %+v", claims)
	}
}

func TestCaptureTokenSignerRejectsUntrustedTokens(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	credential := captureCredentialFixture(t, now, now.Add(time.Minute))
	keys, err := access.NewCaptureTokenKeyring(7, map[access.CaptureTokenKeyVersion][]byte{
		7: bytes.Repeat([]byte{0x42}, 32),
	})
	if err != nil {
		t.Fatalf("NewCaptureTokenKeyring() error = %v", err)
	}
	signer, err := access.NewCaptureTokenSigner(keys, captureTokenClock{now: now})
	if err != nil {
		t.Fatalf("NewCaptureTokenSigner() error = %v", err)
	}
	presented, err := signer.Sign(credential)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	tampered := presented.Reveal()[:len(presented.Reveal())-1] + "A"

	tests := []struct {
		name    string
		signer  *access.CaptureTokenSigner
		encoded string
	}{
		{name: "empty", signer: signer},
		{name: "malformed", signer: signer, encoded: "idq_cap_v1.invalid"},
		{name: "tampered", signer: signer, encoded: tampered},
		{name: "expired", signer: captureTokenSignerAt(t, keys, now.Add(time.Minute)), encoded: presented.Reveal()},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := test.signer.Verify(test.encoded); !errors.Is(err, access.ErrInvalidCaptureToken) {
				t.Fatalf("Verify() error = %v, want ErrInvalidCaptureToken", err)
			}
		})
	}
}

func TestCaptureCredentialRevocationIsIrreversible(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	credential := captureCredentialFixture(t, now, now.Add(time.Hour))
	if !credential.IsUsableAt(now) {
		t.Fatal("new credential is not usable")
	}
	if err := credential.Revoke(now.Add(time.Minute)); err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if credential.IsUsableAt(now.Add(2 * time.Minute)) {
		t.Fatal("revoked credential remains usable")
	}
	if err := credential.Revoke(now.Add(3 * time.Minute)); !errors.Is(err, access.ErrInvalidCaptureToken) {
		t.Fatalf("second Revoke() error = %v", err)
	}
}

type captureTokenClock struct{ now time.Time }

func (source captureTokenClock) Now() time.Time { return source.now }

func captureTokenSignerAt(
	t *testing.T,
	keys *access.CaptureTokenKeyring,
	now time.Time,
) *access.CaptureTokenSigner {
	t.Helper()

	signer, err := access.NewCaptureTokenSigner(keys, captureTokenClock{now: now})
	if err != nil {
		t.Fatalf("NewCaptureTokenSigner() error = %v", err)
	}

	return signer
}

func captureCredentialFixture(t *testing.T, issuedAt, expiresAt time.Time) access.CaptureCredential {
	t.Helper()

	generator, err := id.NewGenerator(
		captureTokenClock{now: issuedAt},
		bytes.NewReader(bytes.Repeat([]byte{0x31}, 256)),
	)
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	tenantID, err := generator.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	verificationID, err := generator.NewVerification()
	if err != nil {
		t.Fatalf("NewVerification() error = %v", err)
	}
	tokenID, err := generator.NewCaptureToken()
	if err != nil {
		t.Fatalf("NewCaptureToken() error = %v", err)
	}
	credential, err := access.NewCaptureCredential(
		tokenID,
		tenantID,
		verificationID,
		7,
		issuedAt,
		expiresAt,
	)
	if err != nil {
		t.Fatalf("NewCaptureCredential() error = %v", err)
	}

	return credential
}
