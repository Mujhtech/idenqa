package kms_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/kms"
)

func TestWrappedKeyDefensiveCopyAndRedaction(t *testing.T) {
	t.Parallel()

	ciphertext := []byte("wrapped-secret")
	key, err := kms.NewWrappedKey(kms.WrappedKeyRecord{
		Provider: "local.file", Reference: "keyring://evidence", Version: "v1",
		Algorithm: "AES256_GCM", Ciphertext: ciphertext,
	})
	if err != nil {
		t.Fatalf("NewWrappedKey() error = %v", err)
	}
	ciphertext[0] = 'X'
	first := key.Record()
	if bytes.Equal(first.Ciphertext, ciphertext) {
		t.Fatal("NewWrappedKey() retained caller ciphertext")
	}
	first.Ciphertext[0] = 'Y'
	if bytes.Equal(first.Ciphertext, key.Record().Ciphertext) {
		t.Fatal("Record() returned mutable ciphertext")
	}
	if strings.Contains(key.Redacted(), "wrapped-secret") {
		t.Fatal("Redacted() disclosed wrapped key ciphertext")
	}
}

func TestPurposeRequiresNamespace(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"", "evidence", "Evidence.content", "evidence content"} {
		if _, err := kms.NewPurpose(value); err == nil {
			t.Errorf("NewPurpose(%q) error = nil", value)
		}
	}
	if _, err := kms.NewPurpose("idenqa.evidence.content"); err != nil {
		t.Fatalf("NewPurpose(valid) error = %v", err)
	}
}
