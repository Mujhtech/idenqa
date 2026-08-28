package local

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
)

func TestStoreWritesReadsAndDeletesExactVersions(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store, err := open(
		Config{Directory: directory, MaxObjectBytes: 1024},
		bytes.NewReader(append(bytes.Repeat([]byte{0x41}, 16), bytes.Repeat([]byte{0x42}, 16)...)),
	)
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := mustKey(t, "tenants/ten_1/evidence/evd_1/content/1")
	firstBytes := []byte("first encrypted object")
	first := putBytes(t, store, key, firstBytes)
	secondBytes := []byte("second encrypted object")
	second := putBytes(t, store, key, secondBytes)
	if first.Record().Version == second.Record().Version {
		t.Fatal("Put() reused an immutable object version")
	}
	if got := first.Record().Checksum; got != string(platformcrypto.Sum(firstBytes)) {
		t.Fatalf("first checksum = %q, want %q", got, platformcrypto.Sum(firstBytes))
	}
	assertRead(t, store, first, firstBytes)
	assertRead(t, store, second, secondBytes)
	if err := store.Delete(context.Background(), first); err != nil {
		t.Fatalf("Delete(first) error = %v", err)
	}
	if err := store.Delete(context.Background(), first); err != nil {
		t.Fatalf("Delete(first again) error = %v", err)
	}
	if _, err := store.Open(context.Background(), first); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open(deleted first) error = %v, want ErrUnavailable", err)
	}
	assertRead(t, store, second, secondBytes)
}

func TestStoreRemovesPartialAndOversizedObjects(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store, err := open(
		Config{Directory: directory, MaxObjectBytes: 8},
		bytes.NewReader(bytes.Repeat([]byte{0x52}, 64)),
	)
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := mustKey(t, "tenants/ten_1/evidence/evd_1/content/1")
	producerError := errors.New("encryption failed")
	if _, err := store.Put(context.Background(), key, func(writer io.Writer) error {
		if _, err := writer.Write([]byte("partial")); err != nil {
			return err
		}
		return producerError
	}); !errors.Is(err, producerError) {
		t.Fatalf("Put(producer failure) error = %v, want producer error", err)
	}
	if _, err := store.Put(context.Background(), key, func(writer io.Writer) error {
		_, err := writer.Write([]byte("too-large"))
		return err
	}); !errors.Is(err, ErrObjectTooLarge) {
		t.Fatalf("Put(oversized) error = %v, want ErrObjectTooLarge", err)
	}
	assertNoStoredFiles(t, directory)
}

func TestStoreDetectsCiphertextModification(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store, err := open(
		Config{Directory: directory, MaxObjectBytes: 1024},
		bytes.NewReader(bytes.Repeat([]byte{0x63}, 32)),
	)
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	key := mustKey(t, "tenants/ten_1/evidence/evd_1/content/1")
	object := putBytes(t, store, key, []byte("authenticated ciphertext"))
	record := object.Record()
	filePath := filepath.Join(directory, filepath.FromSlash(record.Key), record.Version)
	tampered, err := os.ReadFile(filePath) //nolint:gosec // test-owned temporary path
	if err != nil {
		t.Fatalf("ReadFile(tamper) error = %v", err)
	}
	tampered[0] ^= 0xff
	if err := os.WriteFile(filePath, tampered, 0o600); err != nil { //nolint:gosec // test-owned temporary path
		t.Fatalf("WriteFile(tamper) error = %v", err)
	}
	reader, err := store.Open(context.Background(), object)
	if err != nil {
		t.Fatalf("Open(tampered same-size object) error = %v", err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := io.ReadAll(reader); !errors.Is(err, ErrIntegrity) {
		t.Fatalf("ReadAll(tampered) error = %v, want ErrIntegrity", err)
	}
}

func TestStoreInventoriesAndDeletesExactPhysicalObjects(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	store, err := open(
		Config{Directory: directory, MaxObjectBytes: 1024},
		bytes.NewReader(append(
			append(bytes.Repeat([]byte{0x74}, 16), bytes.Repeat([]byte{0x75}, 16)...),
			bytes.Repeat([]byte{0x76}, 16)...,
		)),
	)
	if err != nil {
		t.Fatalf("open() error = %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	prefix := mustKey(t, "tenants/ten_1/evidence")
	first := putBytes(t, store, mustKey(t, string(prefix)+"/evd_1/content/1"), []byte("first"))
	second := putBytes(t, store, mustKey(t, string(prefix)+"/evd_2/content/1"), []byte("second"))
	_ = putBytes(t, store, mustKey(t, "tenants/ten_2/evidence/evd_3/content/1"), []byte("peer"))

	page, err := store.ListInventory(context.Background(), prefix, "", 1)
	if err != nil {
		t.Fatalf("ListInventory(first page) error = %v", err)
	}
	if len(page.Objects()) != 1 || page.Next() == "" {
		t.Fatalf("first page objects=%d next=%q", len(page.Objects()), page.Next())
	}
	next, err := store.ListInventory(context.Background(), prefix, page.Next(), 1)
	if err != nil {
		t.Fatalf("ListInventory(second page) error = %v", err)
	}
	if len(next.Objects()) != 1 || next.Next() != "" {
		t.Fatalf("second page objects=%d next=%q", len(next.Objects()), next.Next())
	}
	discovered := append(page.Objects(), next.Objects()...)
	if discovered[0].Record().Key != first.Record().Key ||
		discovered[1].Record().Key != second.Record().Key {
		t.Fatalf("inventory keys = %q, %q", discovered[0].Record().Key, discovered[1].Record().Key)
	}
	if err := store.DeleteInventory(context.Background(), discovered[0]); err != nil {
		t.Fatalf("DeleteInventory() error = %v", err)
	}
	if err := store.DeleteInventory(context.Background(), discovered[0]); err != nil {
		t.Fatalf("DeleteInventory(replay) error = %v", err)
	}
	if _, err := store.Open(context.Background(), first); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Open(deleted inventory object) error = %v", err)
	}
	assertRead(t, store, second, []byte("second"))
}

func putBytes(t *testing.T, store *Store, key objectstore.Key, value []byte) objectstore.Object {
	t.Helper()
	object, err := store.Put(context.Background(), key, func(writer io.Writer) error {
		_, err := writer.Write(value)
		return err
	})
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	return object
}

func assertRead(t *testing.T, store *Store, object objectstore.Object, want []byte) {
	t.Helper()
	reader, err := store.Open(context.Background(), object)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = reader.Close() }()
	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadAll() = %q, want %q", got, want)
	}
}

func assertNoStoredFiles(t *testing.T, directory string) {
	t.Helper()
	err := filepath.WalkDir(directory, func(filePath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			t.Errorf("partial object remains at %s", filepath.Base(filePath))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir() error = %v", err)
	}
}

func mustKey(t *testing.T, value string) objectstore.Key {
	t.Helper()
	key, err := objectstore.NewKey(value)
	if err != nil {
		t.Fatalf("NewKey() error = %v", err)
	}

	return key
}
