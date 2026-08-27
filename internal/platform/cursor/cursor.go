// Package cursor signs and verifies short-lived opaque pagination cursors.
package cursor

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
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
	formatVersion  = "v1"
	hmacDomain     = "idq-cursor\x00v1\x00"
	keyByteLength  = 32
	maximumEncoded = 4096
)

var (
	// ErrInvalid deliberately covers malformed, tampered, expired, and
	// incorrectly-bound cursors without revealing which validation failed.
	ErrInvalid = errors.New("cursor: invalid")
)

// KeyVersion identifies deployed cursor HMAC material.
type KeyVersion uint16

// Keyring is an immutable collection of versioned cursor HMAC keys.
type Keyring struct {
	active KeyVersion
	keys   map[KeyVersion][]byte
}

// NewKeyring validates and defensively copies cursor key material.
func NewKeyring(active KeyVersion, keys map[KeyVersion][]byte) (*Keyring, error) {
	if active == 0 || len(keys) == 0 {
		return nil, errors.New("cursor: active key version and keys are required")
	}

	cloned := make(map[KeyVersion][]byte, len(keys))
	for version, material := range keys {
		if version == 0 {
			return nil, errors.New("cursor: key version must be greater than zero")
		}
		if len(material) != keyByteLength {
			return nil, fmt.Errorf("cursor: key version %d must contain 32 bytes", version)
		}
		cloned[version] = append([]byte(nil), material...)
	}
	if _, exists := cloned[active]; !exists {
		return nil, errors.New("cursor: active key version is not configured")
	}

	return &Keyring{active: active, keys: cloned}, nil
}

// Claims is the verified cursor payload. Position is resource-specific JSON;
// tenant and query bindings prevent replay in another listing context.
type Claims struct {
	TenantID id.Tenant
	Query    string
	Position json.RawMessage
	Expires  time.Time
}

type payload struct {
	TenantID string          `json:"tenant_id"`
	Query    string          `json:"query"`
	Position json.RawMessage `json:"position"`
	Expires  int64           `json:"expires_at"`
}

// Codec creates and verifies cursors using an injected clock.
type Codec struct {
	keys  *Keyring
	clock clock.Clock
	ttl   time.Duration
}

// New constructs a cursor codec.
func New(keys *Keyring, source clock.Clock, ttl time.Duration) (*Codec, error) {
	if keys == nil || source == nil || ttl <= 0 {
		return nil, errors.New("cursor: keyring, clock, and positive TTL are required")
	}

	return &Codec{keys: keys, clock: source, ttl: ttl}, nil
}

// Encode signs a tenant- and query-bound resource position.
func (codec *Codec) Encode(tenantID id.Tenant, query string, position json.RawMessage) (string, error) {
	if codec == nil || codec.keys == nil {
		return "", errors.New("cursor: codec is not initialised")
	}
	if tenantID.IsZero() || !validQuery(query) || !validPosition(position) {
		return "", ErrInvalid
	}

	encodedPayload, err := json.Marshal(payload{
		TenantID: tenantID.String(),
		Query:    query,
		Position: append(json.RawMessage(nil), position...),
		Expires:  codec.clock.Now().Add(codec.ttl).UTC().Unix(),
	})
	if err != nil {
		return "", fmt.Errorf("cursor: encode payload: %w", err)
	}
	version := codec.keys.active
	payloadText := base64.RawURLEncoding.EncodeToString(encodedPayload)
	message := signingMessage(version, payloadText)
	signature, err := sign(codec.keys.keys[version], message)
	if err != nil {
		return "", err
	}

	return strings.Join([]string{
		"cur",
		formatVersion,
		strconv.FormatUint(uint64(version), 10),
		payloadText,
		base64.RawURLEncoding.EncodeToString(signature),
	}, "."), nil
}

// Decode verifies and returns a cursor only when its tenant and query match.
func (codec *Codec) Decode(encoded string, tenantID id.Tenant, query string) (Claims, error) {
	if codec == nil || codec.keys == nil || len(encoded) == 0 || len(encoded) > maximumEncoded ||
		tenantID.IsZero() || !validQuery(query) {
		return Claims{}, ErrInvalid
	}

	parts := strings.Split(encoded, ".")
	if len(parts) != 5 || parts[0] != "cur" || parts[1] != formatVersion {
		return Claims{}, ErrInvalid
	}
	versionValue, err := strconv.ParseUint(parts[2], 10, 16)
	if err != nil || versionValue == 0 {
		return Claims{}, ErrInvalid
	}
	version := KeyVersion(versionValue)
	key, exists := codec.keys.keys[version]
	if !exists {
		return Claims{}, ErrInvalid
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[4])
	if err != nil || len(signature) != sha256.Size {
		return Claims{}, ErrInvalid
	}
	expected, err := sign(key, signingMessage(version, parts[3]))
	if err != nil || subtle.ConstantTimeCompare(signature, expected) != 1 {
		return Claims{}, ErrInvalid
	}

	encodedPayload, err := base64.RawURLEncoding.DecodeString(parts[3])
	if err != nil {
		return Claims{}, ErrInvalid
	}
	var decoded payload
	decoder := json.NewDecoder(bytes.NewReader(encodedPayload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil || !validPosition(decoded.Position) {
		return Claims{}, ErrInvalid
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Claims{}, ErrInvalid
	}
	parsedTenant, err := id.ParseTenant(decoded.TenantID)
	if err != nil || parsedTenant.String() != tenantID.String() || decoded.Query != query {
		return Claims{}, ErrInvalid
	}
	expires := time.Unix(decoded.Expires, 0).UTC()
	if !expires.After(codec.clock.Now()) {
		return Claims{}, ErrInvalid
	}

	return Claims{
		TenantID: parsedTenant,
		Query:    decoded.Query,
		Position: append(json.RawMessage(nil), decoded.Position...),
		Expires:  expires,
	}, nil
}

func signingMessage(version KeyVersion, encodedPayload string) []byte {
	return []byte(hmacDomain + strconv.FormatUint(uint64(version), 10) + "\x00" + encodedPayload)
}

func sign(key, message []byte) ([]byte, error) {
	mac := hmac.New(sha256.New, key)
	if _, err := mac.Write(message); err != nil {
		return nil, fmt.Errorf("cursor: compute signature: %w", err)
	}

	return mac.Sum(nil), nil
}

func validQuery(query string) bool {
	return query != "" && len(query) <= 256 && strings.TrimSpace(query) == query
}

func validPosition(position json.RawMessage) bool {
	return len(position) > 0 && len(position) <= 1024 && json.Valid(position) && string(position) != "null"
}
