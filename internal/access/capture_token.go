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
	captureTokenFormat        = "idq_cap_v1"                  //nolint:gosec // public wire-format marker, not credential material
	captureTokenDomain        = "idq-capture-token\x00v1\x00" //nolint:gosec // public domain separator, not a secret
	captureTokenKeyByteLength = 32
	maximumCaptureTokenLength = 2048
	captureTokenRedacted      = "[REDACTED]"
)

var (
	// ErrInvalidCaptureToken deliberately covers malformed, tampered, expired,
	// revoked, and incorrectly bound capture credentials.
	ErrInvalidCaptureToken = errors.New("access: invalid capture token")
)

// CaptureTokenKeyVersion identifies one deployed capture-token signing key.
type CaptureTokenKeyVersion uint16

// CaptureTokenKeyring is an immutable purpose-specific signing-key set.
type CaptureTokenKeyring struct {
	active CaptureTokenKeyVersion
	keys   map[CaptureTokenKeyVersion][]byte
}

// NewCaptureTokenKeyring validates and snapshots capture-token signing keys.
func NewCaptureTokenKeyring(
	active CaptureTokenKeyVersion,
	keys map[CaptureTokenKeyVersion][]byte,
) (*CaptureTokenKeyring, error) {
	if active == 0 || len(keys) == 0 {
		return nil, errors.New("access: active capture-token key version and keys are required")
	}
	cloned := make(map[CaptureTokenKeyVersion][]byte, len(keys))
	for version, material := range keys {
		if version == 0 || len(material) != captureTokenKeyByteLength {
			return nil, fmt.Errorf("access: capture-token key version %d must contain 32 bytes", version)
		}
		cloned[version] = append([]byte(nil), material...)
	}
	if _, exists := cloned[active]; !exists {
		return nil, errors.New("access: active capture-token key version is not configured")
	}

	return &CaptureTokenKeyring{active: active, keys: cloned}, nil
}

// CaptureCredential is the durable, non-secret revocation record for one
// deterministic signed capture bearer token.
type CaptureCredential struct {
	id             id.CaptureToken
	tenantID       id.Tenant
	verificationID id.Verification
	keyVersion     CaptureTokenKeyVersion
	issuedAt       time.Time
	expiresAt      time.Time
	revokedAt      *time.Time
}

// NewCaptureCredential creates an active capture credential record.
func NewCaptureCredential(
	identifier id.CaptureToken,
	tenantID id.Tenant,
	verificationID id.Verification,
	keyVersion CaptureTokenKeyVersion,
	issuedAt time.Time,
	expiresAt time.Time,
) (CaptureCredential, error) {
	return RestoreCaptureCredential(
		identifier,
		tenantID,
		verificationID,
		keyVersion,
		issuedAt,
		expiresAt,
		nil,
	)
}

// RestoreCaptureCredential validates a durable capture credential record.
func RestoreCaptureCredential(
	identifier id.CaptureToken,
	tenantID id.Tenant,
	verificationID id.Verification,
	keyVersion CaptureTokenKeyVersion,
	issuedAt time.Time,
	expiresAt time.Time,
	revokedAt *time.Time,
) (CaptureCredential, error) {
	issuedAt = time.Unix(issuedAt.UTC().Unix(), 0).UTC()
	expiresAt = time.Unix(expiresAt.UTC().Unix(), 0).UTC()
	revokedAt = utcTimeCopy(revokedAt)
	isIdentityInvalid := identifier.IsZero() || tenantID.IsZero() || verificationID.IsZero() || keyVersion == 0
	isTimeInvalid := issuedAt.IsZero() || !expiresAt.After(issuedAt)
	if isIdentityInvalid || isTimeInvalid {
		return CaptureCredential{}, errors.New("access: capture credential identity and lifetime are invalid")
	}
	if revokedAt != nil && revokedAt.Before(issuedAt) {
		return CaptureCredential{}, errors.New("access: capture credential revocation precedes issuance")
	}

	return CaptureCredential{
		id:             identifier,
		tenantID:       tenantID,
		verificationID: verificationID,
		keyVersion:     keyVersion,
		issuedAt:       issuedAt,
		expiresAt:      expiresAt,
		revokedAt:      revokedAt,
	}, nil
}

// ID returns the non-secret capture-token record identifier.
func (credential CaptureCredential) ID() id.CaptureToken { return credential.id }

// TenantID returns the owning tenant identifier.
func (credential CaptureCredential) TenantID() id.Tenant { return credential.tenantID }

// VerificationID returns the bound verification-session identifier.
func (credential CaptureCredential) VerificationID() id.Verification {
	return credential.verificationID
}

// KeyVersion returns the signing key version required for reconstruction.
func (credential CaptureCredential) KeyVersion() CaptureTokenKeyVersion {
	return credential.keyVersion
}

// IssuedAt returns the credential issuance time.
func (credential CaptureCredential) IssuedAt() time.Time { return credential.issuedAt }

// ExpiresAt returns the credential expiry time.
func (credential CaptureCredential) ExpiresAt() time.Time { return credential.expiresAt }

// RevokedAt returns a defensive copy of the revocation time, if revoked.
func (credential CaptureCredential) RevokedAt() *time.Time { return utcTimeCopy(credential.revokedAt) }

// IsUsableAt reports whether the durable record permits capture authentication.
func (credential CaptureCredential) IsUsableAt(now time.Time) bool {
	return credential.revokedAt == nil && now.UTC().Before(credential.expiresAt)
}

// Revoke irreversibly marks the credential unusable.
func (credential *CaptureCredential) Revoke(now time.Time) error {
	if credential == nil || credential.id.IsZero() || credential.revokedAt != nil {
		return ErrInvalidCaptureToken
	}
	now = now.UTC()
	if now.Before(credential.issuedAt) {
		return errors.New("access: capture credential revocation precedes issuance")
	}
	credential.revokedAt = &now

	return nil
}

// CaptureTokenClaims is the authenticated non-secret bearer-token payload.
type CaptureTokenClaims struct {
	TokenID        id.CaptureToken
	TenantID       id.Tenant
	VerificationID id.Verification
	KeyVersion     CaptureTokenKeyVersion
	IssuedAt       time.Time
	ExpiresAt      time.Time
}

type captureTokenPayload struct {
	TokenID        string `json:"token_id"`
	TenantID       string `json:"tenant_id"`
	VerificationID string `json:"verification_id"`
	IssuedAt       int64  `json:"issued_at"`
	ExpiresAt      int64  `json:"expires_at"`
}

// PresentedCaptureToken is a display-once signed bearer credential. Generic
// formatting and text marshaling never reveal it.
type PresentedCaptureToken struct{ encoded string }

// Reveal returns the credential at an explicitly authorised delivery boundary.
func (token PresentedCaptureToken) Reveal() string { return token.encoded }

// IsZero reports whether no credential was issued.
func (token PresentedCaptureToken) IsZero() bool { return token.encoded == "" }

// String redacts the bearer credential.
func (PresentedCaptureToken) String() string { return captureTokenRedacted }

// GoString redacts the bearer credential in %#v formatting.
func (PresentedCaptureToken) GoString() string { return captureTokenRedacted }

// MarshalText prevents generic encoders from exposing the bearer credential.
func (PresentedCaptureToken) MarshalText() ([]byte, error) {
	return []byte(captureTokenRedacted), nil
}

// CaptureTokenSigner signs and verifies deterministic capture credentials.
type CaptureTokenSigner struct {
	keys  *CaptureTokenKeyring
	clock clock.Clock
}

// NewCaptureTokenSigner constructs a capture-token signer.
func NewCaptureTokenSigner(keys *CaptureTokenKeyring, source clock.Clock) (*CaptureTokenSigner, error) {
	if keys == nil || source == nil {
		return nil, errors.New("access: capture-token keyring and clock are required")
	}

	return &CaptureTokenSigner{keys: keys, clock: source}, nil
}

// ActiveVersion returns the key version used for newly issued credentials.
func (signer *CaptureTokenSigner) ActiveVersion() CaptureTokenKeyVersion {
	if signer == nil || signer.keys == nil {
		return 0
	}

	return signer.keys.active
}

// Sign deterministically constructs a bearer token from a durable record.
func (signer *CaptureTokenSigner) Sign(credential CaptureCredential) (PresentedCaptureToken, error) {
	if signer == nil || signer.keys == nil || credential.id.IsZero() {
		return PresentedCaptureToken{}, errors.New("access: capture-token signer is not initialised")
	}
	key, exists := signer.keys.keys[credential.keyVersion]
	if !exists {
		return PresentedCaptureToken{}, errors.New("access: capture-token signing key is unavailable")
	}
	encodedPayload, err := json.Marshal(captureTokenPayload{
		TokenID:        credential.id.String(),
		TenantID:       credential.tenantID.String(),
		VerificationID: credential.verificationID.String(),
		IssuedAt:       credential.issuedAt.Unix(),
		ExpiresAt:      credential.expiresAt.Unix(),
	})
	if err != nil {
		return PresentedCaptureToken{}, fmt.Errorf("access: encode capture-token payload: %w", err)
	}
	payloadText := base64.RawURLEncoding.EncodeToString(encodedPayload)
	signature, err := captureTokenMAC(key, credential.keyVersion, payloadText)
	if err != nil {
		return PresentedCaptureToken{}, err
	}
	encoded := strings.Join([]string{
		captureTokenFormat,
		strconv.FormatUint(uint64(credential.keyVersion), 10),
		payloadText,
		base64.RawURLEncoding.EncodeToString(signature),
	}, ".")

	return PresentedCaptureToken{encoded: encoded}, nil
}

// Verify authenticates the token payload and enforces its signed expiry.
// Database-backed revocation and session-state checks remain mandatory after it returns.
func (signer *CaptureTokenSigner) Verify(encoded string) (CaptureTokenClaims, error) {
	if signer == nil || signer.keys == nil || len(encoded) == 0 || len(encoded) > maximumCaptureTokenLength {
		return CaptureTokenClaims{}, ErrInvalidCaptureToken
	}
	parts := strings.Split(encoded, ".")
	if len(parts) != 4 || parts[0] != captureTokenFormat {
		return CaptureTokenClaims{}, ErrInvalidCaptureToken
	}
	versionValue, err := strconv.ParseUint(parts[1], 10, 16)
	if err != nil || versionValue == 0 {
		return CaptureTokenClaims{}, ErrInvalidCaptureToken
	}
	version := CaptureTokenKeyVersion(versionValue)
	key, exists := signer.keys.keys[version]
	if !exists {
		return CaptureTokenClaims{}, ErrInvalidCaptureToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil || len(signature) != sha256.Size {
		return CaptureTokenClaims{}, ErrInvalidCaptureToken
	}
	expected, err := captureTokenMAC(key, version, parts[2])
	if err != nil || !hmac.Equal(signature, expected) {
		return CaptureTokenClaims{}, ErrInvalidCaptureToken
	}
	encodedPayload, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return CaptureTokenClaims{}, ErrInvalidCaptureToken
	}
	var payload captureTokenPayload
	decoder := json.NewDecoder(bytes.NewReader(encodedPayload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return CaptureTokenClaims{}, ErrInvalidCaptureToken
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return CaptureTokenClaims{}, ErrInvalidCaptureToken
	}
	claims, err := parseCaptureTokenClaims(payload, version)
	if err != nil || !claims.ExpiresAt.After(signer.clock.Now().UTC()) {
		return CaptureTokenClaims{}, ErrInvalidCaptureToken
	}

	return claims, nil
}

func parseCaptureTokenClaims(
	payload captureTokenPayload,
	version CaptureTokenKeyVersion,
) (CaptureTokenClaims, error) {
	tokenID, err := id.ParseCaptureToken(payload.TokenID)
	if err != nil {
		return CaptureTokenClaims{}, err
	}
	tenantID, err := id.ParseTenant(payload.TenantID)
	if err != nil {
		return CaptureTokenClaims{}, err
	}
	verificationID, err := id.ParseVerification(payload.VerificationID)
	if err != nil {
		return CaptureTokenClaims{}, err
	}
	issuedAt := time.Unix(payload.IssuedAt, 0).UTC()
	expiresAt := time.Unix(payload.ExpiresAt, 0).UTC()
	if issuedAt.IsZero() || !expiresAt.After(issuedAt) {
		return CaptureTokenClaims{}, errors.New("access: invalid capture-token lifetime")
	}

	return CaptureTokenClaims{
		TokenID:        tokenID,
		TenantID:       tenantID,
		VerificationID: verificationID,
		KeyVersion:     version,
		IssuedAt:       issuedAt,
		ExpiresAt:      expiresAt,
	}, nil
}

func captureTokenMAC(key []byte, version CaptureTokenKeyVersion, payload string) ([]byte, error) {
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write([]byte(captureTokenDomain)); err != nil {
		return nil, fmt.Errorf("access: sign capture token: %w", err)
	}
	if _, err := mac.Write([]byte(strconv.FormatUint(uint64(version), 10))); err != nil {
		return nil, fmt.Errorf("access: sign capture token: %w", err)
	}
	if _, err := mac.Write([]byte{'\x00'}); err != nil {
		return nil, fmt.Errorf("access: sign capture token: %w", err)
	}
	if _, err := mac.Write([]byte(payload)); err != nil {
		return nil, fmt.Errorf("access: sign capture token: %w", err)
	}

	return mac.Sum(nil), nil
}

func utcTimeCopy(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()

	return &cloned
}
