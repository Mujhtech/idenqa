package experience

import (
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// SignatureAlgorithm is the only v1 manifest signature algorithm.
const SignatureAlgorithm = "ed25519"

// SignDocument canonicalises and signs one validated document. Private keys
// never leave the owned signing boundary.
func SignDocument(document Document, keyID string, privateKey ed25519.PrivateKey) (Manifest, error) {
	if len(privateKey) != ed25519.PrivateKeySize || !keyIDPattern.MatchString(keyID) {
		return Manifest{}, ErrInvalid
	}
	canonical, err := CanonicalDocument(document)
	if err != nil {
		return Manifest{}, err
	}
	manifest := Manifest{
		Document:  document,
		Digest:    digestBytes(canonical),
		KeyID:     keyID,
		Algorithm: SignatureAlgorithm,
		Signature: hex.EncodeToString(ed25519.Sign(privateKey, canonical)),
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// VerifyManifest recomputes the canonical digest and verifies the Ed25519
// signature against the trusted key for the manifest key id. Wrong keys,
// tampered documents, and unknown key ids fail closed.
func VerifyManifest(manifest Manifest, keys map[string]ed25519.PublicKey) (Document, error) {
	if err := ValidateManifest(manifest); err != nil {
		return Document{}, err
	}
	canonical, err := CanonicalDocument(manifest.Document)
	if err != nil {
		return Document{}, err
	}
	if subtle.ConstantTimeCompare([]byte(digestBytes(canonical)), []byte(manifest.Digest)) != 1 {
		return Document{}, fmt.Errorf("%w: digest", ErrSignature)
	}
	publicKey, ok := keys[manifest.KeyID]
	if !ok || len(publicKey) != ed25519.PublicKeySize {
		return Document{}, fmt.Errorf("%w: key_id", ErrUnknownKey)
	}
	signature, err := hex.DecodeString(manifest.Signature)
	if err != nil || !ed25519.Verify(publicKey, canonical, signature) {
		return Document{}, ErrSignature
	}
	return manifest.Document, nil
}
