// Package ed25519signer adapts the deployment-owned Ed25519 keyring to the
// experience signing and verification ports. Key ids are derived from the
// versioned keyring and never supplied by callers.
package ed25519signer

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"sort"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/experience"
)

type keyID string

func keyIDFor(version uint16) keyID { return keyID(fmt.Sprintf("expkey_v%d", version)) }

// Keyring is an immutable snapshot of versioned Ed25519 signing keys.
type Keyring struct {
	active  uint16
	secrets map[uint16]ed25519.PrivateKey
	public  map[keyID]ed25519.PublicKey
}

// New validates and snapshots key material. Each value is a 32-byte seed or a
// 64-byte Ed25519 private key.
func New(active uint16, values map[uint16][]byte) (*Keyring, error) {
	if active == 0 || len(values) == 0 {
		return nil, errors.New("experience signer: active version and keys are required")
	}
	secrets := make(map[uint16]ed25519.PrivateKey, len(values))
	public := make(map[keyID]ed25519.PublicKey, len(values))
	for version, material := range values {
		if version == 0 {
			return nil, errors.New("experience signer: key version zero is invalid")
		}
		var private ed25519.PrivateKey
		switch len(material) {
		case ed25519.SeedSize:
			private = ed25519.NewKeyFromSeed(material)
		case ed25519.PrivateKeySize:
			private = append(ed25519.PrivateKey(nil), material...)
		default:
			return nil, errors.New("experience signer: key material must be a 32-byte seed or 64-byte private key")
		}
		secrets[version] = private
		public[keyIDFor(version)] = private.Public().(ed25519.PublicKey)
	}
	if _, exists := secrets[active]; !exists {
		return nil, errors.New("experience signer: active version is not configured")
	}
	return &Keyring{active: active, secrets: secrets, public: public}, nil
}

// ActiveKeyID reports the key id clients must trust for new documents.
func (keyring *Keyring) ActiveKeyID() string { return string(keyIDFor(keyring.active)) }

// Versions returns the configured key versions in stable order.
func (keyring *Keyring) Versions() []uint16 {
	versions := make([]uint16, 0, len(keyring.secrets))
	for version := range keyring.secrets {
		versions = append(versions, version)
	}
	sort.Slice(versions, func(left, right int) bool { return versions[left] < versions[right] })
	return versions
}

// Sign signs canonical experience bytes with the active key.
func (keyring *Keyring) Sign(_ context.Context, canonical []byte) (experience.Signature, error) {
	private, exists := keyring.secrets[keyring.active]
	if !exists || len(canonical) == 0 {
		return experience.Signature{}, experience.ErrInvalid
	}
	return experience.Signature{
		KeyID: string(keyIDFor(keyring.active)), Algorithm: contract.SignatureAlgorithm,
		Bytes: ed25519.Sign(private, canonical),
	}, nil
}

// Verify verifies canonical bytes against a trusted configured key id.
func (keyring *Keyring) Verify(_ context.Context, identifier string, canonical []byte, signature []byte) error {
	public, exists := keyring.public[keyID(identifier)]
	if !exists || len(public) != ed25519.PublicKeySize || len(canonical) == 0 || len(signature) != ed25519.SignatureSize {
		return experience.ErrSignature
	}
	if !ed25519.Verify(public, canonical, signature) {
		return experience.ErrSignature
	}
	return nil
}

// Fingerprint returns a defensive copy of one public key for distribution.
func (keyring *Keyring) Fingerprint(identifier string) (ed25519.PublicKey, bool) {
	public, exists := keyring.public[keyID(identifier)]
	if !exists {
		return nil, false
	}
	return append(ed25519.PublicKey(nil), public...), true
}

var (
	_ experience.Signer   = (*Keyring)(nil)
	_ experience.Verifier = (*Keyring)(nil)
)
