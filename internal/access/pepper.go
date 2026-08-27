package access

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
)

const hmacDomain = "idq-api-key\x00v1\x00"

// PepperVersion identifies deployed API-key HMAC material.
type PepperVersion uint16

// Digest is stored API-key verification material.
type Digest struct {
	value [sha256.Size]byte
}

// ParseDigest validates stored API-key verification material.
func ParseDigest(value []byte) (Digest, error) {
	if len(value) != sha256.Size {
		return Digest{}, errors.New("API key digest must contain 32 bytes")
	}

	var digest Digest
	copy(digest.value[:], value)

	return digest, nil
}

// Bytes returns a defensive copy suitable for persistence.
func (digest Digest) Bytes() []byte {
	result := make([]byte, len(digest.value))
	copy(result, digest.value[:])

	return result
}

// String redacts verification material.
func (Digest) String() string {
	return redactedKey
}

// GoString redacts verification material in %#v formatting.
func (Digest) GoString() string {
	return redactedKey
}

// PepperSet is an immutable collection of versioned HMAC keys.
type PepperSet struct {
	active  PepperVersion
	peppers map[PepperVersion][]byte
}

// NewPepperSet validates and defensively copies deployment pepper material.
func NewPepperSet(active PepperVersion, peppers map[PepperVersion][]byte) (*PepperSet, error) {
	if active == 0 {
		return nil, errors.New("active API key pepper version is required")
	}
	if len(peppers) == 0 {
		return nil, errors.New("API key peppers are required")
	}

	cloned := make(map[PepperVersion][]byte, len(peppers))
	for version, pepper := range peppers {
		if version == 0 {
			return nil, errors.New("API key pepper version must be greater than zero")
		}
		if len(pepper) != secretByteLength {
			return nil, fmt.Errorf("API key pepper version %d must contain 32 bytes", version)
		}
		cloned[version] = append([]byte(nil), pepper...)
	}
	if _, exists := cloned[active]; !exists {
		return nil, errors.New("active API key pepper version is not configured")
	}

	return &PepperSet{active: active, peppers: cloned}, nil
}

// ActiveVersion returns the version used for newly issued credentials.
func (set *PepperSet) ActiveVersion() PepperVersion {
	if set == nil {
		return 0
	}

	return set.active
}

// Digest computes persistence material with the active pepper.
func (set *PepperSet) Digest(key PresentedKey) (Digest, PepperVersion, error) {
	if set == nil {
		return Digest{}, 0, errors.New("API key pepper set is not initialised")
	}
	if key.IsZero() {
		return Digest{}, 0, errors.New("presented API key is required")
	}

	pepper, exists := set.peppers[set.active]
	if !exists {
		return Digest{}, 0, errors.New("active API key pepper version is not configured")
	}

	digest, err := computeDigest(pepper, key)
	if err != nil {
		return Digest{}, 0, err
	}

	return digest, set.active, nil
}

// Verify performs constant-time comparison using the credential's stored pepper version.
func (set *PepperSet) Verify(key PresentedKey, version PepperVersion, expected Digest) (bool, error) {
	if set == nil {
		return false, errors.New("API key pepper set is not initialised")
	}
	if key.IsZero() {
		return false, errors.New("presented API key is required")
	}
	pepper, exists := set.peppers[version]
	if !exists {
		return false, errors.New("API key pepper version is not configured")
	}

	actual, err := computeDigest(pepper, key)
	if err != nil {
		return false, err
	}

	return subtle.ConstantTimeCompare(actual.value[:], expected.value[:]) == 1, nil
}

// DummyVerify performs the same HMAC and constant-time comparison primitives
// used for a real credential without requiring a stored key record. Callers use
// it before returning authentication failure for malformed or unknown keys.
func (set *PepperSet) DummyVerify(key PresentedKey) error {
	if set == nil {
		return errors.New("API key pepper set is not initialised")
	}
	pepper, exists := set.peppers[set.active]
	if !exists {
		return errors.New("active API key pepper version is not configured")
	}

	var actual Digest
	var err error
	if key.IsZero() {
		actual, err = computeDummyDigest(pepper)
	} else {
		actual, err = computeDigest(pepper, key)
	}
	if err != nil {
		return err
	}

	var expected Digest
	_ = subtle.ConstantTimeCompare(actual.value[:], expected.value[:])

	return nil
}

func computeDigest(pepper []byte, key PresentedKey) (Digest, error) {
	mac := hmac.New(sha256.New, pepper)
	message := make([]byte, 0, len(hmacDomain)+len(key.tenant.String())+len(key.key.String())+2+len(key.secret))
	message = append(message, hmacDomain...)
	message = append(message, key.tenant.String()...)
	message = append(message, 0)
	message = append(message, key.key.String()...)
	message = append(message, 0)
	message = append(message, key.secret[:]...)
	if _, err := mac.Write(message); err != nil {
		return Digest{}, fmt.Errorf("compute API key digest: %w", err)
	}

	var digest Digest
	copy(digest.value[:], mac.Sum(nil))

	return digest, nil
}

func computeDummyDigest(pepper []byte) (Digest, error) {
	mac := hmac.New(sha256.New, pepper)
	// Match the byte length of the version, tenant, key, and secret message used
	// by computeDigest. The content is deliberately fixed and carries no hint.
	message := make([]byte, len(hmacDomain)+len("ten_")+ulidPayloadLength+1+len("key_")+ulidPayloadLength+1+secretByteLength)
	copy(message, hmacDomain)
	if _, err := mac.Write(message); err != nil {
		return Digest{}, fmt.Errorf("compute dummy API key digest: %w", err)
	}

	var digest Digest
	copy(digest.value[:], mac.Sum(nil))

	return digest, nil
}
