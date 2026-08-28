package local

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/kms"
)

func TestKeyringRotationRetainsExactOldVersion(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "evidence-keyring.json")
	random := bytes.NewReader(bytes.Repeat([]byte{0x35, 0xa7, 0x19, 0xc2}, 256))
	keyring, err := create(path, random)
	if err != nil {
		t.Fatalf("create() error = %v", err)
	}
	t.Cleanup(func() { _ = keyring.Close() })
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("keyring mode = %o, want 600", got)
	}

	purpose := mustPurpose(t, "idenqa.evidence.content")
	authenticatedContext := []byte(`{"tenant_id":"ten_1","evidence_id":"evd_1"}`)
	first, err := keyring.Wrap(context.Background(), purpose, []byte("first-keyset"), authenticatedContext)
	if err != nil {
		t.Fatalf("Wrap(first) error = %v", err)
	}
	if got := first.Record().Version; got != "v1" {
		t.Fatalf("first key version = %q, want v1", got)
	}
	version, err := keyring.Rotate(context.Background())
	if err != nil {
		t.Fatalf("Rotate() error = %v", err)
	}
	if version != "v2" {
		t.Fatalf("Rotate() version = %q, want v2", version)
	}
	second, err := keyring.Wrap(context.Background(), purpose, []byte("second-keyset"), authenticatedContext)
	if err != nil {
		t.Fatalf("Wrap(second) error = %v", err)
	}
	if got := second.Record().Version; got != "v2" {
		t.Fatalf("second key version = %q, want v2", got)
	}

	reopened, err := open(path, bytes.NewReader(bytes.Repeat([]byte{0x91}, 128)))
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	assertUnwrap(t, reopened, purpose, first, authenticatedContext, "first-keyset")
	assertUnwrap(t, reopened, purpose, second, authenticatedContext, "second-keyset")
	if _, err := reopened.Unwrap(context.Background(), purpose, first, []byte("wrong-context")); !errors.Is(err, ErrUnwrapFailed) {
		t.Fatalf("Unwrap(wrong context) error = %v, want ErrUnwrapFailed", err)
	}
}

func TestKeyringRejectsInsecureOrDestructiveFileOperations(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "evidence-keyring.json")
	keyring, err := create(path, bytes.NewReader(bytes.Repeat([]byte{0x42}, 128)))
	if err != nil {
		t.Fatalf("create() error = %v", err)
	}
	if err := keyring.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	original, err := os.ReadFile(path) //nolint:gosec // test-owned temporary path
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if _, err := create(path, bytes.NewReader(bytes.Repeat([]byte{0x84}, 128))); !errors.Is(err, ErrInvalidKeyring) {
		t.Fatalf("duplicate create error = %v, want ErrInvalidKeyring", err)
	}
	after, err := os.ReadFile(path) //nolint:gosec // test-owned temporary path
	if err != nil {
		t.Fatalf("ReadFile(after duplicate) error = %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("duplicate create replaced the existing keyring")
	}
	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // deliberately verifies rejection of insecure mode
		t.Fatalf("Chmod() error = %v", err)
	}
	if _, err := Open(path); !errors.Is(err, ErrInvalidKeyring) {
		t.Fatalf("Open(insecure mode) error = %v, want ErrInvalidKeyring", err)
	}
}

func TestKeyringFailsClosedForMetadataAndCiphertextChanges(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "evidence-keyring.json")
	keyring, err := create(path, bytes.NewReader(bytes.Repeat([]byte{0x63}, 128)))
	if err != nil {
		t.Fatalf("create() error = %v", err)
	}
	t.Cleanup(func() { _ = keyring.Close() })
	purpose := mustPurpose(t, "idenqa.evidence.content")
	wrapped, err := keyring.Wrap(context.Background(), purpose, []byte("keyset"), []byte("context"))
	if err != nil {
		t.Fatalf("Wrap() error = %v", err)
	}
	record := wrapped.Record()
	record.Ciphertext[len(record.Ciphertext)-1] ^= 0xff
	tampered, err := kms.NewWrappedKey(record)
	if err != nil {
		t.Fatalf("NewWrappedKey() error = %v", err)
	}
	if _, err := keyring.Unwrap(context.Background(), purpose, tampered, []byte("context")); !errors.Is(err, ErrUnwrapFailed) {
		t.Fatalf("Unwrap(tampered) error = %v, want ErrUnwrapFailed", err)
	}
	otherPurpose := mustPurpose(t, "idenqa.capture.token")
	if _, err := keyring.Unwrap(context.Background(), otherPurpose, wrapped, []byte("context")); !errors.Is(err, ErrUnwrapFailed) {
		t.Fatalf("Unwrap(other purpose) error = %v, want ErrUnwrapFailed", err)
	}
}

func assertUnwrap(
	t *testing.T,
	keyring *Keyring,
	purpose kms.Purpose,
	wrapped kms.WrappedKey,
	authenticatedContext []byte,
	want string,
) {
	t.Helper()
	plaintext, err := keyring.Unwrap(context.Background(), purpose, wrapped, authenticatedContext)
	if err != nil {
		t.Fatalf("Unwrap() error = %v", err)
	}
	defer clear(plaintext)
	if string(plaintext) != want {
		t.Fatalf("Unwrap() = %q, want %q", plaintext, want)
	}
}

func mustPurpose(t *testing.T, value string) kms.Purpose {
	t.Helper()
	purpose, err := kms.NewPurpose(value)
	if err != nil {
		t.Fatalf("NewPurpose() error = %v", err)
	}

	return purpose
}
