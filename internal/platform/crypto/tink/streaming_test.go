package tink_test

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"testing"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	tinkcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto/tink"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/platform/kms/local"
)

func TestStreamingRoundTripAcrossSegmentsAndKeyRotation(t *testing.T) {
	t.Parallel()

	keyring, err := local.Create(filepath.Join(t.TempDir(), "keyring.json"))
	if err != nil {
		t.Fatalf("local.Create() error = %v", err)
	}
	t.Cleanup(func() { _ = keyring.Close() })
	streaming := mustStreaming(t, keyring, "idenqa.evidence.content")
	authenticatedContext := mustContext(t, []byte(`{"tenant_id":"ten_1","evidence_id":"evd_1"}`))
	firstPlaintext := bytes.Repeat([]byte("first authenticated evidence segment\n"), 70_000)
	firstCiphertext, firstEnvelope := seal(t, streaming, firstPlaintext, authenticatedContext)
	if bytes.Contains(firstCiphertext, firstPlaintext[:128]) {
		t.Fatal("Seal() left a plaintext prefix in ciphertext")
	}
	if firstEnvelope.Record().WrappedKey.Version != "v1" {
		t.Fatalf("first envelope version = %q, want v1", firstEnvelope.Record().WrappedKey.Version)
	}

	if _, err := keyring.Rotate(context.Background()); err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	secondPlaintext := []byte("evidence encrypted after KEK rotation")
	secondCiphertext, secondEnvelope := seal(t, streaming, secondPlaintext, authenticatedContext)
	if secondEnvelope.Record().WrappedKey.Version != "v2" {
		t.Fatalf("second envelope version = %q, want v2", secondEnvelope.Record().WrappedKey.Version)
	}
	assertDistinctKeysets(t, keyring, firstEnvelope, secondEnvelope, authenticatedContext)
	assertOpen(t, streaming, firstCiphertext, firstEnvelope, authenticatedContext, firstPlaintext)
	assertOpen(t, streaming, secondCiphertext, secondEnvelope, authenticatedContext, secondPlaintext)
}

func TestStreamingFailsClosedForContextPurposeTamperingAndTruncation(t *testing.T) {
	t.Parallel()

	keyring, err := local.Create(filepath.Join(t.TempDir(), "keyring.json"))
	if err != nil {
		t.Fatalf("local.Create() error = %v", err)
	}
	t.Cleanup(func() { _ = keyring.Close() })
	streaming := mustStreaming(t, keyring, "idenqa.evidence.content")
	authenticatedContext := mustContext(t, []byte(`{"tenant_id":"ten_1","evidence_id":"evd_1"}`))
	plaintext := bytes.Repeat([]byte("authenticated evidence\n"), 100_000)
	ciphertext, envelope := seal(t, streaming, plaintext, authenticatedContext)

	wrongContext := mustContext(t, []byte(`{"tenant_id":"ten_2","evidence_id":"evd_1"}`))
	if err := streaming.Open(context.Background(), &bytes.Buffer{}, bytes.NewReader(ciphertext), envelope, wrongContext); !errors.Is(err, tinkcrypto.ErrOpenFailed) || errors.Is(err, platformcrypto.ErrCiphertextIntegrity) {
		t.Fatalf("Open(wrong context) error = %v, want ErrOpenFailed", err)
	}
	otherPurpose := mustStreaming(t, keyring, "idenqa.capture.token")
	if err := otherPurpose.Open(context.Background(), &bytes.Buffer{}, bytes.NewReader(ciphertext), envelope, authenticatedContext); !errors.Is(err, tinkcrypto.ErrOpenFailed) || errors.Is(err, platformcrypto.ErrCiphertextIntegrity) {
		t.Fatalf("Open(wrong purpose) error = %v, want ErrOpenFailed", err)
	}

	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)/2] ^= 0xff
	if err := streaming.Open(context.Background(), &bytes.Buffer{}, bytes.NewReader(tampered), envelope, authenticatedContext); !errors.Is(err, platformcrypto.ErrCiphertextIntegrity) {
		t.Fatalf("Open(tampered) error = %v, want ErrCiphertextIntegrity", err)
	}
	truncated := ciphertext[:len(ciphertext)-1]
	if err := streaming.Open(context.Background(), &bytes.Buffer{}, bytes.NewReader(truncated), envelope, authenticatedContext); !errors.Is(err, platformcrypto.ErrCiphertextIntegrity) {
		t.Fatalf("Open(truncated) error = %v, want ErrCiphertextIntegrity", err)
	}
}

func seal(
	t *testing.T,
	streaming *tinkcrypto.Streaming,
	plaintext []byte,
	authenticatedContext platformcrypto.Context,
) ([]byte, platformcrypto.Envelope) {
	t.Helper()
	var ciphertext bytes.Buffer
	envelope, err := streaming.Seal(context.Background(), &ciphertext, bytes.NewReader(plaintext), authenticatedContext)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	return ciphertext.Bytes(), envelope
}

func assertOpen(
	t *testing.T,
	streaming *tinkcrypto.Streaming,
	ciphertext []byte,
	envelope platformcrypto.Envelope,
	authenticatedContext platformcrypto.Context,
	want []byte,
) {
	t.Helper()
	var plaintext bytes.Buffer
	if err := streaming.Open(context.Background(), &plaintext, bytes.NewReader(ciphertext), envelope, authenticatedContext); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if !bytes.Equal(plaintext.Bytes(), want) {
		t.Fatalf("Open() plaintext length = %d, want %d", plaintext.Len(), len(want))
	}
}

func mustStreaming(t *testing.T, keyring *local.Keyring, purposeValue string) *tinkcrypto.Streaming {
	t.Helper()
	purpose, err := kms.NewPurpose(purposeValue)
	if err != nil {
		t.Fatalf("kms.NewPurpose() error = %v", err)
	}
	streaming, err := tinkcrypto.NewStreaming(keyring, keyring, purpose)
	if err != nil {
		t.Fatalf("tinkcrypto.NewStreaming() error = %v", err)
	}

	return streaming
}

func mustContext(t *testing.T, data []byte) platformcrypto.Context {
	t.Helper()
	authenticatedContext, err := platformcrypto.NewContext(1, data)
	if err != nil {
		t.Fatalf("platformcrypto.NewContext() error = %v", err)
	}

	return authenticatedContext
}

func assertDistinctKeysets(
	t *testing.T,
	keyring *local.Keyring,
	first platformcrypto.Envelope,
	second platformcrypto.Envelope,
	authenticatedContext platformcrypto.Context,
) {
	t.Helper()
	purpose, err := kms.NewPurpose("idenqa.evidence.content")
	if err != nil {
		t.Fatalf("kms.NewPurpose() error = %v", err)
	}
	firstWrapped, err := kms.NewWrappedKey(first.Record().WrappedKey)
	if err != nil {
		t.Fatalf("kms.NewWrappedKey(first) error = %v", err)
	}
	secondWrapped, err := kms.NewWrappedKey(second.Record().WrappedKey)
	if err != nil {
		t.Fatalf("kms.NewWrappedKey(second) error = %v", err)
	}
	firstKeyset, err := keyring.Unwrap(context.Background(), purpose, firstWrapped, authenticatedContext.Data())
	if err != nil {
		t.Fatalf("Unwrap(first keyset) error = %v", err)
	}
	defer clear(firstKeyset)
	secondKeyset, err := keyring.Unwrap(context.Background(), purpose, secondWrapped, authenticatedContext.Data())
	if err != nil {
		t.Fatalf("Unwrap(second keyset) error = %v", err)
	}
	defer clear(secondKeyset)
	if bytes.Equal(firstKeyset, secondKeyset) {
		t.Fatal("Seal() reused a per-object Tink keyset")
	}
}
