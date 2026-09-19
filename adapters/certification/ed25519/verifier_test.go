package certification_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	certification "github.com/Mujhtech/idenqa/adapters/certification/ed25519"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/review"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type fixedClock struct{ at time.Time }

func (clock fixedClock) Now() time.Time { return clock.at }

type assertionPayload struct {
	Issuer      string    `json:"issuer"`
	KeyID       string    `json:"key_id"`
	ReviewerID  string    `json:"reviewer_id"`
	Certificate string    `json:"certificate"`
	Region      string    `json:"region"`
	IssuedAt    time.Time `json:"issued_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

func signAssertion(t *testing.T, private ed25519.PrivateKey, payload any) string {
	t.Helper()
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(private, encoded)
	return certification.SchemePrefix + "." + base64.RawURLEncoding.EncodeToString(encoded) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func generateKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return private
}

func TestVerifierAcceptsBoundSignedAssertion(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	private := generateKey(t)
	verifier, err := certification.NewVerifier(certification.IssuerKeys{
		"issuer.registry": {"2026-09": private.Public().(ed25519.PublicKey)},
	}, fixedClock{now})
	if err != nil {
		t.Fatal(err)
	}
	token := signAssertion(t, private, assertionPayload{
		Issuer: "issuer.registry", KeyID: "2026-09", ReviewerID: "operator-1",
		Certificate: "document.level2", Region: "tenant.region.ng",
		IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	})
	assertion := review.CertificateAssertion{Token: token, ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}
	scope := mustTenant(t, "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	if err := verifier.Verify(t.Context(), scope, assertion); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestVerifierRejectsDeniedAssertions(t *testing.T) {
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	private, other := generateKey(t), generateKey(t)
	valid := assertionPayload{
		Issuer: "issuer.registry", KeyID: "2026-09", ReviewerID: "operator-1",
		Certificate: "document.level2", Region: "tenant.region.ng",
		IssuedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	}
	bound := review.CertificateAssertion{ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}
	scope := mustTenant(t, "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	configured := func() map[string]ed25519.PublicKey {
		return map[string]ed25519.PublicKey{"2026-09": private.Public().(ed25519.PublicKey)}
	}
	validToken := signAssertion(t, private, valid)
	for _, test := range []struct {
		name      string
		issuers   certification.IssuerKeys
		assertion review.CertificateAssertion
	}{
		{name: "wrong_key", issuers: certification.IssuerKeys{"issuer.registry": {"2026-09": other.Public().(ed25519.PublicKey)}}, assertion: bound},
		{name: "unknown_issuer", issuers: certification.IssuerKeys{"issuer.other": configured()}, assertion: bound},
		{name: "unknown_key", issuers: certification.IssuerKeys{"issuer.registry": {"2027-01": private.Public().(ed25519.PublicKey)}}, assertion: bound},
	} {
		t.Run(test.name, func(t *testing.T) {
			assertion := test.assertion
			assertion.Token = validToken
			verifier, err := certification.NewVerifier(test.issuers, fixedClock{now})
			if err != nil {
				t.Fatal(err)
			}
			if err := verifier.Verify(t.Context(), scope, assertion); !errors.Is(err, review.ErrForbidden) {
				t.Fatalf("Verify() error = %v, want ErrForbidden", err)
			}
		})
	}

	tampered := signAssertion(t, private, func() assertionPayload {
		value := valid
		value.ReviewerID = "operator-2"
		return value
	}())
	unknownField := func() string {
		encoded, err := json.Marshal(map[string]any{
			"issuer": "issuer.registry", "key_id": "2026-09", "reviewer_id": "operator-1",
			"certificate": "document.level2", "region": "tenant.region.ng",
			"issued_at": now.Add(-time.Hour), "expires_at": now.Add(time.Hour), "scope": "all",
		})
		if err != nil {
			t.Fatal(err)
		}
		signature := ed25519.Sign(private, encoded)
		return certification.SchemePrefix + "." + base64.RawURLEncoding.EncodeToString(encoded) + "." + base64.RawURLEncoding.EncodeToString(signature)
	}()
	signatureBytes, err := base64.RawURLEncoding.DecodeString(strings.Split(validToken, ".")[2])
	if err != nil {
		t.Fatal(err)
	}
	signatureBytes[0] ^= 0xff
	tamperedSignature := certification.SchemePrefix + "." + strings.Split(validToken, ".")[1] + "." + base64.RawURLEncoding.EncodeToString(signatureBytes)
	for _, test := range []struct {
		name      string
		assertion review.CertificateAssertion
	}{
		{name: "tampered_payload", assertion: review.CertificateAssertion{Token: tampered, ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}},
		{name: "tampered_signature", assertion: review.CertificateAssertion{Token: tamperedSignature, ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}},
		{name: "wrong_reviewer", assertion: review.CertificateAssertion{Token: validToken, ReviewerID: "operator-2", Certificate: "document.level2", Region: "tenant.region.ng"}},
		{name: "wrong_certificate", assertion: review.CertificateAssertion{Token: validToken, ReviewerID: "operator-1", Certificate: "document.level3", Region: "tenant.region.ng"}},
		{name: "wrong_region", assertion: review.CertificateAssertion{Token: validToken, ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.gh"}},
		{name: "unknown_payload_field", assertion: review.CertificateAssertion{Token: unknownField, ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}},
		{name: "missing_token", assertion: review.CertificateAssertion{ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}},
		{name: "wrong_scheme", assertion: review.CertificateAssertion{Token: "v2." + strings.Split(validToken, ".")[1] + "." + strings.Split(validToken, ".")[2], ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}},
		{name: "malformed_token", assertion: review.CertificateAssertion{Token: "v1.not-base64", ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}}, // #nosec G101 -- deliberately malformed public test input.
		{name: "oversized_token", assertion: review.CertificateAssertion{Token: strings.Repeat("a", 4097), ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifier, err := certification.NewVerifier(certification.IssuerKeys{"issuer.registry": configured()}, fixedClock{now})
			if err != nil {
				t.Fatal(err)
			}
			if err := verifier.Verify(t.Context(), scope, test.assertion); !errors.Is(err, review.ErrForbidden) {
				t.Fatalf("Verify() error = %v, want ErrForbidden", err)
			}
		})
	}

	for _, test := range []struct {
		name    string
		payload assertionPayload
	}{
		{name: "expired", payload: assertionPayload{Issuer: "issuer.registry", KeyID: "2026-09", ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng", IssuedAt: now.Add(-2 * time.Hour), ExpiresAt: now}},
		{name: "future", payload: assertionPayload{Issuer: "issuer.registry", KeyID: "2026-09", ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng", IssuedAt: now.Add(time.Microsecond), ExpiresAt: now.Add(time.Hour)}},
		{name: "expires_before_issued", payload: assertionPayload{Issuer: "issuer.registry", KeyID: "2026-09", ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng", IssuedAt: now, ExpiresAt: now.Add(-time.Second)}},
		{name: "missing_validity", payload: assertionPayload{Issuer: "issuer.registry", KeyID: "2026-09", ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifier, err := certification.NewVerifier(certification.IssuerKeys{"issuer.registry": configured()}, fixedClock{now})
			if err != nil {
				t.Fatal(err)
			}
			token := signAssertion(t, private, test.payload)
			assertion := review.CertificateAssertion{Token: token, ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}
			if err := verifier.Verify(t.Context(), scope, assertion); !errors.Is(err, review.ErrForbidden) {
				t.Fatalf("Verify() error = %v, want ErrForbidden", err)
			}
		})
	}
}

// TestVerifierMicrosecondValidityBoundary proves UTC microsecond comparisons:
// one microsecond inside the window is accepted, the exact expiry is not.
func TestVerifierMicrosecondValidityBoundary(t *testing.T) {
	issuedAt := time.Date(2026, time.September, 19, 12, 0, 0, 123456000, time.UTC)
	expiresAt := issuedAt.Add(time.Hour)
	private := generateKey(t)
	token := signAssertion(t, private, assertionPayload{
		Issuer: "issuer.registry", KeyID: "2026-09", ReviewerID: "operator-1",
		Certificate: "document.level2", Region: "tenant.region.ng", IssuedAt: issuedAt, ExpiresAt: expiresAt,
	})
	assertion := review.CertificateAssertion{Token: token, ReviewerID: "operator-1", Certificate: "document.level2", Region: "tenant.region.ng"}
	scope := mustTenant(t, "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	for _, test := range []struct {
		name  string
		at    time.Time
		valid bool
	}{
		{name: "issued_boundary", at: issuedAt, valid: true},
		{name: "issued_minus_microsecond", at: issuedAt.Add(-time.Microsecond), valid: false},
		{name: "expires_minus_microsecond", at: expiresAt.Add(-time.Microsecond), valid: true},
		{name: "expires_boundary", at: expiresAt, valid: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifier, err := certification.NewVerifier(certification.IssuerKeys{"issuer.registry": {"2026-09": private.Public().(ed25519.PublicKey)}}, fixedClock{test.at})
			if err != nil {
				t.Fatal(err)
			}
			err = verifier.Verify(t.Context(), scope, assertion)
			if test.valid && err != nil {
				t.Fatalf("Verify() error = %v", err)
			}
			if !test.valid && !errors.Is(err, review.ErrForbidden) {
				t.Fatalf("Verify() error = %v, want ErrForbidden", err)
			}
		})
	}
}

func TestNewVerifierRejectsInvalidIssuerKeys(t *testing.T) {
	private := generateKey(t)
	public := private.Public().(ed25519.PublicKey)
	now := fixedClock{time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)}
	for _, test := range []struct {
		name    string
		issuers certification.IssuerKeys
	}{
		{name: "empty", issuers: certification.IssuerKeys{}},
		{name: "empty_key_set", issuers: certification.IssuerKeys{"issuer.registry": {}}},
		{name: "short_key", issuers: certification.IssuerKeys{"issuer.registry": {"2026-09": public[:16]}}},
		{name: "invalid_issuer", issuers: certification.IssuerKeys{"issuer registry": {"2026-09": public}}},
		{name: "invalid_key_id", issuers: certification.IssuerKeys{"issuer.registry": {"2026 09": public}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := certification.NewVerifier(test.issuers, now); !errors.Is(err, review.ErrInvalid) {
				t.Fatalf("NewVerifier() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func mustTenant(t *testing.T, value string) tenant.Scope {
	t.Helper()
	tenantID, err := id.ParseTenant(value)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
