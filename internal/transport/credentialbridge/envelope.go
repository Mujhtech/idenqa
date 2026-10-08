package credentialbridge

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/access"
)

const credentialEnvelopeAlgorithm = "P256-HKDF-SHA256-AES-256-GCM"

type credentialSealer struct {
	aead               cipher.AEAD
	nonce              []byte
	ephemeralPublicKey string
	commandID          string
}

func newCredentialSealer(publicKeyText, commandID string) (*credentialSealer, error) {
	encodedPublicKey, err := base64.RawURLEncoding.DecodeString(publicKeyText)
	if err != nil {
		return nil, errors.New("decode credential delivery public key")
	}
	curve := ecdh.P256()
	publicKey, err := curve.NewPublicKey(encodedPublicKey)
	if err != nil {
		return nil, errors.New("parse credential delivery public key")
	}
	ephemeralPrivateKey, err := curve.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate credential envelope key: %w", err)
	}
	sharedSecret, err := ephemeralPrivateKey.ECDH(publicKey)
	if err != nil {
		return nil, errors.New("derive credential envelope secret")
	}
	defer clear(sharedSecret)
	key, err := hkdf.Key(sha256.New, sharedSecret, nil, "idenqa-credential-envelope-v1\x00"+commandID, 32)
	if err != nil {
		return nil, fmt.Errorf("derive credential envelope key: %w", err)
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("construct credential envelope cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("construct credential envelope AEAD: %w", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate credential envelope nonce: %w", err)
	}
	return &credentialSealer{
		aead: aead, nonce: nonce, commandID: commandID,
		ephemeralPublicKey: base64.RawURLEncoding.EncodeToString(ephemeralPrivateKey.PublicKey().Bytes()),
	}, nil
}

// NewSealer constructs the standard retry-safe credential envelope sealer for
// another bounded local administration workflow.
func NewSealer(publicKeyText, commandID string) (access.CredentialSealer, error) {
	return newCredentialSealer(publicKeyText, commandID)
}

func (sealer *credentialSealer) Seal(credential string) (access.CredentialEnvelope, error) {
	ciphertext := sealer.aead.Seal(nil, sealer.nonce, []byte(credential), []byte(sealer.commandID))
	return access.CredentialEnvelope{
		Algorithm:          credentialEnvelopeAlgorithm,
		EphemeralPublicKey: sealer.ephemeralPublicKey,
		Nonce:              base64.RawURLEncoding.EncodeToString(sealer.nonce),
		Ciphertext:         base64.RawURLEncoding.EncodeToString(ciphertext),
	}, nil
}
