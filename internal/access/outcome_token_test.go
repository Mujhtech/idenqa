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

func TestOutcomeTokenSignerRoundTripRedactionAndExpiry(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	credential := outcomeCredentialFixture(t, now, now.Add(25*time.Hour))
	keys, err := access.NewOutcomeTokenKeyring(3, map[access.OutcomeTokenKeyVersion][]byte{
		3: bytes.Repeat([]byte{0x51}, 32),
	})
	if err != nil {
		t.Fatalf("NewOutcomeTokenKeyring() error = %v", err)
	}
	signer, err := access.NewOutcomeTokenSigner(keys, captureTokenClock{now: now})
	if err != nil {
		t.Fatalf("NewOutcomeTokenSigner() error = %v", err)
	}
	first, err := signer.Sign(credential)
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	second, err := signer.Sign(credential)
	if err != nil || first.Reveal() != second.Reveal() {
		t.Fatalf("deterministic Sign() = %q, %q, %v", first.Reveal(), second.Reveal(), err)
	}
	if first.String() != "[REDACTED]" {
		t.Fatalf("String() = %q", first.String())
	}
	encoded, err := json.Marshal(first)
	if err != nil || strings.Contains(string(encoded), first.Reveal()) {
		t.Fatalf("JSON exposed outcome token: %s, %v", encoded, err)
	}
	claims, err := signer.Verify(first.Reveal())
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if claims.TokenID.String() != credential.ID().String() ||
		claims.VerificationID.String() != credential.VerificationID().String() ||
		claims.TenantID.String() != credential.TenantID().String() ||
		!claims.ExpiresAt.Equal(credential.ExpiresAt()) {
		t.Fatalf("claims = %+v", claims)
	}
	expiredSigner, err := access.NewOutcomeTokenSigner(
		keys,
		captureTokenClock{now: credential.ExpiresAt()},
	)
	if err != nil {
		t.Fatalf("NewOutcomeTokenSigner(expired) error = %v", err)
	}
	if _, err := expiredSigner.Verify(first.Reveal()); !errors.Is(err, access.ErrInvalidOutcomeToken) {
		t.Fatalf("expired Verify() error = %v", err)
	}
}

func TestOutcomeAndCaptureTokensAreNotInterchangeable(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 14, 12, 0, 0, 0, time.UTC)
	outcomeCredential := outcomeCredentialFixture(t, now, now.Add(time.Hour))
	outcomeKeys, _ := access.NewOutcomeTokenKeyring(3, map[access.OutcomeTokenKeyVersion][]byte{
		3: bytes.Repeat([]byte{0x52}, 32),
	})
	outcomeSigner, _ := access.NewOutcomeTokenSigner(outcomeKeys, captureTokenClock{now: now})
	outcomeToken, _ := outcomeSigner.Sign(outcomeCredential)

	captureCredential := captureCredentialFixture(t, now, now.Add(time.Hour))
	captureKeys, _ := access.NewCaptureTokenKeyring(7, map[access.CaptureTokenKeyVersion][]byte{
		7: bytes.Repeat([]byte{0x52}, 32),
	})
	captureSigner, _ := access.NewCaptureTokenSigner(captureKeys, captureTokenClock{now: now})
	captureToken, _ := captureSigner.Sign(captureCredential)

	if _, err := outcomeSigner.Verify(captureToken.Reveal()); !errors.Is(err, access.ErrInvalidOutcomeToken) {
		t.Fatalf("outcome signer accepted capture token: %v", err)
	}
	if _, err := captureSigner.Verify(outcomeToken.Reveal()); !errors.Is(err, access.ErrInvalidCaptureToken) {
		t.Fatalf("capture signer accepted outcome token: %v", err)
	}
}

func outcomeCredentialFixture(t *testing.T, issuedAt, expiresAt time.Time) access.OutcomeCredential {
	t.Helper()

	generator, err := id.NewGenerator(
		captureTokenClock{now: issuedAt},
		bytes.NewReader(bytes.Repeat([]byte{0x41}, 256)),
	)
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	tenantID, _ := generator.NewTenant()
	verificationID, _ := generator.NewVerification()
	tokenID, _ := generator.NewOutcomeToken()
	credential, err := access.NewOutcomeCredential(
		tokenID, tenantID, verificationID, 3, issuedAt, expiresAt,
	)
	if err != nil {
		t.Fatalf("NewOutcomeCredential() error = %v", err)
	}

	return credential
}
