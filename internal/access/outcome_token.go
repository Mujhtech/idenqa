package access

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	outcomeTokenFormat        = "idq_out_v1"
	outcomeTokenDomain        = "idq-outcome-token\x00v1\x00" //nolint:gosec // public domain separator, not a secret
	outcomeTokenKeyByteLength = 32
	maximumOutcomeTokenLength = 2048
	outcomeTokenRedacted      = "[REDACTED]"
)

var (
	// ErrInvalidOutcomeToken deliberately covers malformed, tampered, expired,
	// revoked, and incorrectly bound outcome credentials.
	ErrInvalidOutcomeToken = errors.New("access: invalid outcome token")
)

// OutcomeTokenKeyVersion identifies one deployed outcome-token signing key.
type OutcomeTokenKeyVersion uint16

// OutcomeTokenKeyring is an immutable purpose-specific signing-key set.
type OutcomeTokenKeyring struct {
	active OutcomeTokenKeyVersion
	keys   map[OutcomeTokenKeyVersion][]byte
}

// NewOutcomeTokenKeyring validates and snapshots outcome-token signing keys.
func NewOutcomeTokenKeyring(
	active OutcomeTokenKeyVersion,
	keys map[OutcomeTokenKeyVersion][]byte,
) (*OutcomeTokenKeyring, error) {
	if active == 0 || len(keys) == 0 {
		return nil, errors.New("access: active outcome-token key version and keys are required")
	}
	cloned := make(map[OutcomeTokenKeyVersion][]byte, len(keys))
	for version, material := range keys {
		if version == 0 || len(material) != outcomeTokenKeyByteLength {
			return nil, fmt.Errorf("access: outcome-token key version %d must contain 32 bytes", version)
		}
		cloned[version] = append([]byte(nil), material...)
	}
	if _, exists := cloned[active]; !exists {
		return nil, errors.New("access: active outcome-token key version is not configured")
	}

	return &OutcomeTokenKeyring{active: active, keys: cloned}, nil
}

// OutcomeCredential is the durable, non-secret revocation record for one
// deterministic signed read-only outcome bearer token.
type OutcomeCredential struct {
	id             id.OutcomeToken
	tenantID       id.Tenant
	verificationID id.Verification
	keyVersion     OutcomeTokenKeyVersion
	issuedAt       time.Time
	expiresAt      time.Time
	revokedAt      *time.Time
}

// NewOutcomeCredential creates an active outcome credential record.
func NewOutcomeCredential(
	identifier id.OutcomeToken,
	tenantID id.Tenant,
	verificationID id.Verification,
	keyVersion OutcomeTokenKeyVersion,
	issuedAt time.Time,
	expiresAt time.Time,
) (OutcomeCredential, error) {
	return RestoreOutcomeCredential(
		identifier,
		tenantID,
		verificationID,
		keyVersion,
		issuedAt,
		expiresAt,
		nil,
	)
}

// RestoreOutcomeCredential validates a durable outcome credential record.
func RestoreOutcomeCredential(
	identifier id.OutcomeToken,
	tenantID id.Tenant,
	verificationID id.Verification,
	keyVersion OutcomeTokenKeyVersion,
	issuedAt time.Time,
	expiresAt time.Time,
	revokedAt *time.Time,
) (OutcomeCredential, error) {
	issuedAt = time.Unix(issuedAt.UTC().Unix(), 0).UTC()
	expiresAt = time.Unix(expiresAt.UTC().Unix(), 0).UTC()
	revokedAt = utcTimeCopy(revokedAt)
	if identifier.IsZero() || tenantID.IsZero() || verificationID.IsZero() || keyVersion == 0 ||
		issuedAt.IsZero() || !expiresAt.After(issuedAt) {
		return OutcomeCredential{}, errors.New("access: outcome credential identity and lifetime are invalid")
	}
	if revokedAt != nil && revokedAt.Before(issuedAt) {
		return OutcomeCredential{}, errors.New("access: outcome credential revocation precedes issuance")
	}

	return OutcomeCredential{
		id:             identifier,
		tenantID:       tenantID,
		verificationID: verificationID,
		keyVersion:     keyVersion,
		issuedAt:       issuedAt,
		expiresAt:      expiresAt,
		revokedAt:      revokedAt,
	}, nil
}

// ID returns the non-secret outcome-token record identifier.
func (credential OutcomeCredential) ID() id.OutcomeToken { return credential.id }

// TenantID returns the owning tenant identifier.
func (credential OutcomeCredential) TenantID() id.Tenant { return credential.tenantID }

// VerificationID returns the bound verification-session identifier.
func (credential OutcomeCredential) VerificationID() id.Verification {
	return credential.verificationID
}

// KeyVersion returns the signing key version required for reconstruction.
func (credential OutcomeCredential) KeyVersion() OutcomeTokenKeyVersion {
	return credential.keyVersion
}

// IssuedAt returns the credential issuance time.
func (credential OutcomeCredential) IssuedAt() time.Time { return credential.issuedAt }

// ExpiresAt returns the credential expiry time.
func (credential OutcomeCredential) ExpiresAt() time.Time { return credential.expiresAt }

// RevokedAt returns a defensive copy of the revocation time, if revoked.
func (credential OutcomeCredential) RevokedAt() *time.Time { return utcTimeCopy(credential.revokedAt) }

// IsUsableAt reports whether the durable record permits outcome authentication.
func (credential OutcomeCredential) IsUsableAt(now time.Time) bool {
	return credential.revokedAt == nil && now.UTC().Before(credential.expiresAt)
}

// Revoke irreversibly marks the credential unusable.
func (credential *OutcomeCredential) Revoke(now time.Time) error {
	if credential == nil || credential.id.IsZero() || credential.revokedAt != nil {
		return ErrInvalidOutcomeToken
	}
	now = now.UTC()
	if now.Before(credential.issuedAt) {
		return errors.New("access: outcome credential revocation precedes issuance")
	}
	credential.revokedAt = &now

	return nil
}

// OutcomeTokenClaims is the authenticated non-secret bearer-token payload.
type OutcomeTokenClaims struct {
	TokenID        id.OutcomeToken
	TenantID       id.Tenant
	VerificationID id.Verification
	KeyVersion     OutcomeTokenKeyVersion
	IssuedAt       time.Time
	ExpiresAt      time.Time
}

type outcomeTokenPayload struct {
	TokenID        string `json:"token_id"`
	TenantID       string `json:"tenant_id"`
	VerificationID string `json:"verification_id"`
	IssuedAt       int64  `json:"issued_at"`
	ExpiresAt      int64  `json:"expires_at"`
}

// PresentedOutcomeToken is a display-once signed read-only bearer credential.
// Generic formatting and text marshaling never reveal it.
type PresentedOutcomeToken struct{ encoded string }

// Reveal returns the credential at an explicitly authorised delivery boundary.
func (token PresentedOutcomeToken) Reveal() string { return token.encoded }

// IsZero reports whether no credential was issued.
func (token PresentedOutcomeToken) IsZero() bool { return token.encoded == "" }

// String redacts the bearer credential.
func (PresentedOutcomeToken) String() string { return outcomeTokenRedacted }

// GoString redacts the bearer credential in %#v formatting.
func (PresentedOutcomeToken) GoString() string { return outcomeTokenRedacted }

// MarshalText prevents generic encoders from exposing the bearer credential.
func (PresentedOutcomeToken) MarshalText() ([]byte, error) {
	return []byte(outcomeTokenRedacted), nil
}

// OutcomeTokenSigner signs and verifies deterministic read-only outcome credentials.
type OutcomeTokenSigner struct {
	keys  *OutcomeTokenKeyring
	clock clock.Clock
}

// NewOutcomeTokenSigner constructs an outcome-token signer.
func NewOutcomeTokenSigner(keys *OutcomeTokenKeyring, source clock.Clock) (*OutcomeTokenSigner, error) {
	if keys == nil || source == nil {
		return nil, errors.New("access: outcome-token keyring and clock are required")
	}

	return &OutcomeTokenSigner{keys: keys, clock: source}, nil
}

// ActiveVersion returns the key version used for newly issued credentials.
func (signer *OutcomeTokenSigner) ActiveVersion() OutcomeTokenKeyVersion {
	if signer == nil || signer.keys == nil {
		return 0
	}

	return signer.keys.active
}

// Sign deterministically constructs a bearer token from a durable record.
func (signer *OutcomeTokenSigner) Sign(credential OutcomeCredential) (PresentedOutcomeToken, error) {
	if signer == nil || signer.keys == nil || credential.id.IsZero() {
		return PresentedOutcomeToken{}, errors.New("access: outcome-token signer is not initialised")
	}
	key, exists := signer.keys.keys[credential.keyVersion]
	if !exists {
		return PresentedOutcomeToken{}, errors.New("access: outcome-token signing key is unavailable")
	}
	encodedPayload, err := json.Marshal(outcomeTokenPayload{
		TokenID:        credential.id.String(),
		TenantID:       credential.tenantID.String(),
		VerificationID: credential.verificationID.String(),
		IssuedAt:       credential.issuedAt.Unix(),
		ExpiresAt:      credential.expiresAt.Unix(),
	})
	if err != nil {
		return PresentedOutcomeToken{}, fmt.Errorf("access: encode outcome-token payload: %w", err)
	}
	payloadText := base64.RawURLEncoding.EncodeToString(encodedPayload)
	signature, err := outcomeTokenMAC(key, credential.keyVersion, payloadText)
	if err != nil {
		return PresentedOutcomeToken{}, err
	}
	encoded := strings.Join([]string{
		outcomeTokenFormat,
		strconv.FormatUint(uint64(credential.keyVersion), 10),
		payloadText,
		base64.RawURLEncoding.EncodeToString(signature),
	}, ".")

	return PresentedOutcomeToken{encoded: encoded}, nil
}

// Verify authenticates the token payload and enforces its signed expiry.
// Database-backed revocation and binding checks remain mandatory after it returns.
func (signer *OutcomeTokenSigner) Verify(encoded string) (OutcomeTokenClaims, error) {
	if signer == nil || signer.keys == nil || len(encoded) == 0 || len(encoded) > maximumOutcomeTokenLength {
		return OutcomeTokenClaims{}, ErrInvalidOutcomeToken
	}
	parts := strings.Split(encoded, ".")
	if len(parts) != 4 || parts[0] != outcomeTokenFormat {
		return OutcomeTokenClaims{}, ErrInvalidOutcomeToken
	}
	versionValue, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil || versionValue == 0 {
		return OutcomeTokenClaims{}, ErrInvalidOutcomeToken
	}
	version := OutcomeTokenKeyVersion(versionValue)
	key, exists := signer.keys.keys[version]
	if !exists {
		return OutcomeTokenClaims{}, ErrInvalidOutcomeToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(signature) != sha256.Size {
		return OutcomeTokenClaims{}, ErrInvalidOutcomeToken
	}
	expected, err := outcomeTokenMAC(key, version, parts[2])
	if err != nil || !hmac.Equal(signature, expected) {
		return OutcomeTokenClaims{}, ErrInvalidOutcomeToken
	}
	encodedPayload, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return OutcomeTokenClaims{}, ErrInvalidOutcomeToken
	}
	var payload outcomeTokenPayload
	decoder := json.NewDecoder(bytes.NewReader(encodedPayload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return OutcomeTokenClaims{}, ErrInvalidOutcomeToken
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return OutcomeTokenClaims{}, ErrInvalidOutcomeToken
	}
	claims, err := parseOutcomeTokenClaims(payload, version)
	if err != nil || !claims.ExpiresAt.After(signer.clock.Now().UTC()) {
		return OutcomeTokenClaims{}, ErrInvalidOutcomeToken
	}

	return claims, nil
}

func parseOutcomeTokenClaims(
	payload outcomeTokenPayload,
	version OutcomeTokenKeyVersion,
) (OutcomeTokenClaims, error) {
	tokenID, err := id.ParseOutcomeToken(payload.TokenID)
	if err != nil {
		return OutcomeTokenClaims{}, err
	}
	tenantID, err := id.ParseTenant(payload.TenantID)
	if err != nil {
		return OutcomeTokenClaims{}, err
	}
	verificationID, err := id.ParseVerification(payload.VerificationID)
	if err != nil {
		return OutcomeTokenClaims{}, err
	}
	issuedAt := time.Unix(payload.IssuedAt, 0).UTC()
	expiresAt := time.Unix(payload.ExpiresAt, 0).UTC()
	if issuedAt.IsZero() || !expiresAt.After(issuedAt) {
		return OutcomeTokenClaims{}, errors.New("access: invalid outcome-token lifetime")
	}

	return OutcomeTokenClaims{
		TokenID:        tokenID,
		TenantID:       tenantID,
		VerificationID: verificationID,
		KeyVersion:     version,
		IssuedAt:       issuedAt,
		ExpiresAt:      expiresAt,
	}, nil
}

func outcomeTokenMAC(key []byte, version OutcomeTokenKeyVersion, payload string) ([]byte, error) {
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write([]byte(outcomeTokenDomain)); err != nil {
		return nil, fmt.Errorf("access: sign outcome token: %w", err)
	}
	if _, err := mac.Write([]byte(strconv.FormatUint(uint64(version), 10))); err != nil {
		return nil, fmt.Errorf("access: sign outcome token: %w", err)
	}
	if _, err := mac.Write([]byte{'\x00'}); err != nil {
		return nil, fmt.Errorf("access: sign outcome token: %w", err)
	}
	if _, err := mac.Write([]byte(payload)); err != nil {
		return nil, fmt.Errorf("access: sign outcome token: %w", err)
	}

	return mac.Sum(nil), nil
}
