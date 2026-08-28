// Package kms owns provider-neutral key-wrapping metadata and ports.
package kms

import (
	"errors"
	"fmt"
	"strings"
)

const (
	maxLabelLength      = 512
	maxWrappedKeyLength = 64 * 1024
)

// Purpose separates key use across data classes and cryptographic operations.
type Purpose string

// NewPurpose validates a stable namespaced key purpose.
func NewPurpose(value string) (Purpose, error) {
	if !validName(value, 200) {
		return "", errors.New("kms: purpose must be a bounded namespaced value")
	}

	return Purpose(value), nil
}

// WrappedKeyRecord is the durable provider-neutral representation of one
// wrapped content key or keyset. Ciphertext contains no plaintext key bytes.
type WrappedKeyRecord struct {
	Provider   string
	Reference  string
	Version    string
	Algorithm  string
	Ciphertext []byte
}

// WrappedKey is validated immutable wrapped-key metadata.
type WrappedKey struct{ record WrappedKeyRecord }

// NewWrappedKey validates and snapshots provider output.
func NewWrappedKey(record WrappedKeyRecord) (WrappedKey, error) {
	if !validLabel(record.Provider) || !validLabel(record.Reference) ||
		!validLabel(record.Version) || !validLabel(record.Algorithm) {
		return WrappedKey{}, errors.New("kms: wrapped key metadata is invalid")
	}
	if len(record.Ciphertext) == 0 || len(record.Ciphertext) > maxWrappedKeyLength {
		return WrappedKey{}, errors.New("kms: wrapped key ciphertext is invalid")
	}
	record.Ciphertext = append([]byte(nil), record.Ciphertext...)

	return WrappedKey{record: record}, nil
}

// Record returns a defensive copy suitable for durable envelope metadata.
func (key WrappedKey) Record() WrappedKeyRecord {
	record := key.record
	record.Ciphertext = append([]byte(nil), key.record.Ciphertext...)
	return record
}

// IsZero reports whether the wrapped key has not been initialised.
func (key WrappedKey) IsZero() bool { return len(key.record.Ciphertext) == 0 }

func validName(value string, limit int) bool {
	if value == "" || len(value) > limit || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	hasSeparator := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		if character == '.' || character == '_' || character == '-' {
			hasSeparator = true
			continue
		}
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') {
			return false
		}
	}
	return hasSeparator
}

func validLabel(value string) bool {
	if strings.TrimSpace(value) != value || value == "" || len(value) > maxLabelLength {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character == 0x7f {
			return false
		}
	}
	return true
}

// Redacted returns safe diagnostic text without wrapped key material.
func (key WrappedKey) Redacted() string {
	return fmt.Sprintf("wrapped key provider=%s reference=%s version=%s algorithm=%s", key.record.Provider,
		key.record.Reference, key.record.Version, key.record.Algorithm)
}
