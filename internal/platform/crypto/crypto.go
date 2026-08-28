// Package crypto owns authenticated streaming-encryption metadata and ports.
package crypto

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/Mujhtech/idenqa/internal/platform/kms"
)

var (
	// ErrCiphertextIntegrity identifies an exact stored-ciphertext checksum or
	// size failure without exposing storage details.
	ErrCiphertextIntegrity = errors.New("crypto: ciphertext integrity failure")
	// ErrCiphertextUnavailable identifies an availability failure while reading ciphertext.
	ErrCiphertextUnavailable = errors.New("crypto: ciphertext unavailable")
)

const maxContextLength = 32 * 1024

// Digest is a validated SHA-256 digest encoded for durable metadata.
type Digest string

// NewDigest validates a canonical sha256:<lowercase-hex> digest.
func NewDigest(value string) (Digest, error) {
	const prefix = "sha256:"
	if len(value) != len(prefix)+(sha256.Size*2) || value[:len(prefix)] != prefix {
		return "", errors.New("crypto: invalid sha256 digest")
	}
	decoded, err := hex.DecodeString(value[len(prefix):])
	if err != nil || len(decoded) != sha256.Size || hex.EncodeToString(decoded) != value[len(prefix):] {
		return "", errors.New("crypto: invalid sha256 digest")
	}

	return Digest(value), nil
}

// Sum returns the SHA-256 digest of value.
func Sum(value []byte) Digest {
	digest := sha256.Sum256(value)
	return Digest("sha256:" + hex.EncodeToString(digest[:]))
}

// Context is immutable non-secret authenticated data for a ciphertext.
type Context struct {
	schemaVersion uint32
	data          []byte
	digest        Digest
}

// NewContext validates and snapshots canonical authenticated data.
func NewContext(schemaVersion uint32, data []byte) (Context, error) {
	if schemaVersion == 0 || len(data) == 0 || len(data) > maxContextLength {
		return Context{}, errors.New("crypto: authenticated context is invalid")
	}
	cloned := append([]byte(nil), data...)

	return Context{schemaVersion: schemaVersion, data: cloned, digest: Sum(cloned)}, nil
}

// SchemaVersion returns the authenticated-context schema version.
func (context Context) SchemaVersion() uint32 { return context.schemaVersion }

// Data returns a defensive copy of the canonical authenticated data.
func (context Context) Data() []byte { return append([]byte(nil), context.data...) }

// Digest returns the canonical context digest.
func (context Context) Digest() Digest { return context.digest }

// EnvelopeRecord is the durable non-plaintext metadata needed to open a
// ciphertext with its exact format, wrapped key, and authenticated context.
type EnvelopeRecord struct {
	FormatVersion        uint32
	ContentAlgorithm     string
	Purpose              string
	WrappedKey           kms.WrappedKeyRecord
	ContextSchemaVersion uint32
	ContextDigest        string
}

// Envelope is validated immutable ciphertext metadata.
type Envelope struct {
	record     EnvelopeRecord
	wrappedKey kms.WrappedKey
}

// NewEnvelope validates metadata produced by a streaming sealer.
func NewEnvelope(record EnvelopeRecord) (Envelope, error) {
	if record.FormatVersion == 0 || !validLabel(record.ContentAlgorithm) || record.ContextSchemaVersion == 0 {
		return Envelope{}, errors.New("crypto: envelope metadata is invalid")
	}
	if _, err := kms.NewPurpose(record.Purpose); err != nil {
		return Envelope{}, errors.New("crypto: envelope purpose is invalid")
	}
	digest, err := NewDigest(record.ContextDigest)
	if err != nil {
		return Envelope{}, err
	}
	wrappedKey, err := kms.NewWrappedKey(record.WrappedKey)
	if err != nil {
		return Envelope{}, err
	}
	record.ContextDigest = string(digest)
	record.WrappedKey = wrappedKey.Record()

	return Envelope{record: record, wrappedKey: wrappedKey}, nil
}

func validLabel(value string) bool {
	if value == "" || len(value) > 200 {
		return false
	}
	for _, character := range value {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') &&
			character != '_' && character != '-' && character != '.' {
			return false
		}
	}
	return true
}

// Record returns a defensive copy suitable for persistence.
func (envelope Envelope) Record() EnvelopeRecord {
	record := envelope.record
	record.WrappedKey = envelope.wrappedKey.Record()
	return record
}

// IsZero reports whether the envelope has not been initialised.
func (envelope Envelope) IsZero() bool { return envelope.record.FormatVersion == 0 }

// KeyWrapper protects plaintext content keys using a provider-managed KEK.
// It is defined here because the streaming encryption implementation consumes it.
type KeyWrapper interface {
	Wrap(context.Context, kms.Purpose, []byte, []byte) (kms.WrappedKey, error)
}

// KeyUnwrapper releases a content key only for the exact purpose and
// authenticated context used when it was wrapped.
type KeyUnwrapper interface {
	Unwrap(context.Context, kms.Purpose, kms.WrappedKey, []byte) ([]byte, error)
}
