package credentialbridge

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestCredentialEnvelopeCanOnlyBeOpenedByDeliveryKey(t *testing.T) {
	t.Parallel()

	privateKey, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	publicKey := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey().Bytes())
	sealer, err := newCredentialSealer(publicKey, "command-1")
	if err != nil {
		t.Fatalf("newCredentialSealer() error = %v", err)
	}
	envelope, err := sealer.Seal("idq_v1_display_once")
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if envelope.Algorithm != envelopeAlgorithm || envelope.Ciphertext == "idq_v1_display_once" {
		t.Fatal("credential envelope is not opaque")
	}
	ephemeralBytes, err := base64.RawURLEncoding.DecodeString(envelope.EphemeralPublicKey)
	if err != nil {
		t.Fatalf("decode ephemeral key: %v", err)
	}
	ephemeralKey, err := ecdh.P256().NewPublicKey(ephemeralBytes)
	if err != nil {
		t.Fatalf("parse ephemeral key: %v", err)
	}
	sharedSecret, err := privateKey.ECDH(ephemeralKey)
	if err != nil {
		t.Fatalf("ECDH() error = %v", err)
	}
	key, err := hkdf.Key(sha256.New, sharedSecret, nil, "idenqa-credential-envelope-v1\x00command-1", 32)
	if err != nil {
		t.Fatalf("hkdf.Key() error = %v", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("NewGCM() error = %v", err)
	}
	nonce, err := base64.RawURLEncoding.DecodeString(envelope.Nonce)
	if err != nil {
		t.Fatalf("decode nonce: %v", err)
	}
	ciphertext, err := base64.RawURLEncoding.DecodeString(envelope.Ciphertext)
	if err != nil {
		t.Fatalf("decode ciphertext: %v", err)
	}
	plaintext, err := aead.Open(nil, nonce, ciphertext, []byte("command-1"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if string(plaintext) != "idq_v1_display_once" {
		t.Fatalf("plaintext = %q", plaintext)
	}
}
