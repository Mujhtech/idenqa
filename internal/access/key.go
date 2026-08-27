package access

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

const (
	presentedKeyPrefix = "idq_v1_"
	ulidPayloadLength  = 26
	secretByteLength   = 32
	secretTextLength   = 43
	presentedKeyLength = len(presentedKeyPrefix) + ulidPayloadLength + 1 + ulidPayloadLength + 1 + secretTextLength
	redactedKey        = "[REDACTED]"
)

// PresentedKey is display-once API-key material. String formatting and text
// marshaling are deliberately redacted; Reveal must be called explicitly at
// the one authorised delivery boundary.
type PresentedKey struct {
	tenant id.Tenant
	key    id.APIKey
	secret [secretByteLength]byte
}

// ParsePresentedKey strictly parses the selected idq_v1 credential format.
// Fixed offsets avoid treating Base64URL underscores as structural separators.
func ParsePresentedKey(encoded string) (PresentedKey, error) {
	if len(encoded) != presentedKeyLength {
		return PresentedKey{}, errors.New("API key has an invalid length")
	}
	if encoded[:len(presentedKeyPrefix)] != presentedKeyPrefix {
		return PresentedKey{}, errors.New("API key has an invalid version prefix")
	}

	tenantEnd := len(presentedKeyPrefix) + ulidPayloadLength
	keyStart := tenantEnd + 1
	keyEnd := keyStart + ulidPayloadLength
	secretStart := keyEnd + 1
	if encoded[tenantEnd] != '_' || encoded[keyEnd] != '_' {
		return PresentedKey{}, errors.New("API key has an invalid structure")
	}

	tenant, err := id.ParseTenant("ten_" + encoded[len(presentedKeyPrefix):tenantEnd])
	if err != nil {
		return PresentedKey{}, errors.New("API key has an invalid tenant hint")
	}
	key, err := id.ParseAPIKey("key_" + encoded[keyStart:keyEnd])
	if err != nil {
		return PresentedKey{}, errors.New("API key has an invalid key identifier")
	}
	secret, err := base64.RawURLEncoding.DecodeString(encoded[secretStart:])
	if err != nil || len(secret) != secretByteLength {
		return PresentedKey{}, errors.New("API key has invalid secret material")
	}

	var secretArray [secretByteLength]byte
	copy(secretArray[:], secret)

	return PresentedKey{tenant: tenant, key: key, secret: secretArray}, nil
}

// KeyGenerator creates display-once API-key material from injected entropy.
type KeyGenerator struct {
	entropy io.Reader
}

// NewKeyGenerator builds an API-key generator with an explicit entropy source.
func NewKeyGenerator(entropy io.Reader) (*KeyGenerator, error) {
	if entropy == nil {
		return nil, errors.New("API key entropy is required")
	}

	return &KeyGenerator{entropy: entropy}, nil
}

// NewSystemKeyGenerator builds the production generator using cryptographic entropy.
func NewSystemKeyGenerator() (*KeyGenerator, error) {
	return NewKeyGenerator(rand.Reader)
}

// Generate creates a credential for existing tenant and key record identifiers.
func (generator *KeyGenerator) Generate(tenant id.Tenant, key id.APIKey) (PresentedKey, error) {
	if generator == nil || generator.entropy == nil {
		return PresentedKey{}, errors.New("API key generator is not initialised")
	}
	if tenant.IsZero() {
		return PresentedKey{}, errors.New("API key tenant is required")
	}
	if key.IsZero() {
		return PresentedKey{}, errors.New("API key identifier is required")
	}

	presented := PresentedKey{tenant: tenant, key: key}
	if _, err := io.ReadFull(generator.entropy, presented.secret[:]); err != nil {
		return PresentedKey{}, fmt.Errorf("generate API key secret: %w", err)
	}

	return presented, nil
}

// TenantHint returns the untrusted tenant lookup hint embedded in the key.
func (key PresentedKey) TenantHint() id.Tenant {
	return key.tenant
}

// ID returns the public API-key record identifier embedded in the key.
func (key PresentedKey) ID() id.APIKey {
	return key.key
}

// Reveal returns the complete credential for its single authorised display.
func (key PresentedKey) Reveal() string {
	if key.IsZero() {
		return ""
	}

	return presentedKeyPrefix + key.tenant.String()[4:] + "_" + key.key.String()[4:] + "_" +
		base64.RawURLEncoding.EncodeToString(key.secret[:])
}

// IsZero reports whether the credential has not been initialised.
func (key PresentedKey) IsZero() bool {
	return key.tenant.IsZero() || key.key.IsZero()
}

// String always redacts credential material.
func (PresentedKey) String() string {
	return redactedKey
}

// GoString always redacts credential material in %#v formatting.
func (PresentedKey) GoString() string {
	return redactedKey
}

// MarshalText prevents generic text encoders from exposing credential material.
func (PresentedKey) MarshalText() ([]byte, error) {
	return []byte(redactedKey), nil
}
