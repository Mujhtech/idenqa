package crypto_test

import (
	"bytes"
	"testing"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
)

func TestContextSnapshotsAuthenticatedData(t *testing.T) {
	t.Parallel()

	data := []byte(`{"tenant_id":"ten_1"}`)
	context, err := platformcrypto.NewContext(1, data)
	if err != nil {
		t.Fatalf("NewContext() error = %v", err)
	}
	wantDigest := platformcrypto.Sum(data)
	data[0] = 'X'
	if context.Digest() != wantDigest {
		t.Fatalf("Digest() = %q, want %q", context.Digest(), wantDigest)
	}
	returned := context.Data()
	returned[0] = 'Y'
	if bytes.Equal(returned, context.Data()) {
		t.Fatal("Data() returned mutable authenticated data")
	}
}

func TestEnvelopeValidatesAndCopiesWrappedKey(t *testing.T) {
	t.Parallel()

	context, err := platformcrypto.NewContext(1, []byte("context"))
	if err != nil {
		t.Fatalf("NewContext() error = %v", err)
	}
	record := platformcrypto.EnvelopeRecord{
		FormatVersion: 1, ContentAlgorithm: "AES256_GCM_HKDF_1MB", Purpose: "idenqa.evidence.content",
		WrappedKey: kms.WrappedKeyRecord{
			Provider: "local.file", Reference: "keyring://evidence", Version: "v1",
			Algorithm: "AES256_GCM", Ciphertext: []byte("wrapped-keyset"),
		},
		ContextSchemaVersion: context.SchemaVersion(), ContextDigest: string(context.Digest()),
	}
	envelope, err := platformcrypto.NewEnvelope(record)
	if err != nil {
		t.Fatalf("NewEnvelope() error = %v", err)
	}
	record.WrappedKey.Ciphertext[0] = 'X'
	if bytes.Equal(record.WrappedKey.Ciphertext, envelope.Record().WrappedKey.Ciphertext) {
		t.Fatal("NewEnvelope() retained mutable wrapped key")
	}
}

func TestDigestRejectsNonCanonicalValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "sha1:00", "sha256:ABC", "sha256:00",
		"sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if _, err := platformcrypto.NewDigest(value); err == nil {
			t.Errorf("NewDigest(%q) error = nil", value)
		}
	}
}
