// Package local provides a root-confined filesystem ciphertext store for
// development, conformance tests, and simple self-hosted deployments.
package local

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"sync"

	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
)

const versionEntropySize = 16

var (
	// ErrUnavailable reports a local-store operation failure without exposing
	// the configured filesystem path.
	ErrUnavailable = objectstore.ErrUnavailable
	// ErrObjectTooLarge reports that ciphertext exceeded the configured bound.
	ErrObjectTooLarge = objectstore.ErrObjectTooLarge
	// ErrIntegrity reports that stored ciphertext no longer matches its exact reference.
	ErrIntegrity = objectstore.ErrIntegrity
)

// Config contains the explicit local ciphertext-store limits.
type Config struct {
	Directory      string
	MaxObjectBytes int64
}

// Store keeps immutable ciphertext versions beneath one os.Root.
type Store struct {
	lifecycle sync.RWMutex
	root      *os.Root
	maximum   int64
	random    io.Reader
	randomMu  sync.Mutex
}

// Open validates and opens an existing root directory.
func Open(config Config) (*Store, error) {
	return open(config, rand.Reader)
}

func open(config Config, random io.Reader) (*Store, error) {
	if config.Directory == "" || config.MaxObjectBytes < 1 || random == nil {
		return nil, ErrUnavailable
	}
	root, err := os.OpenRoot(config.Directory)
	if err != nil {
		return nil, ErrUnavailable
	}

	return &Store{root: root, maximum: config.MaxObjectBytes, random: random}, nil
}

// Put writes one immutable ciphertext version and removes every partial file
// when encryption, size validation, flushing, or commit fails.
func (store *Store) Put(
	ctx context.Context,
	key objectstore.Key,
	write func(io.Writer) error,
) (objectstore.Object, error) {
	if store == nil {
		return objectstore.Object{}, ErrUnavailable
	}
	store.lifecycle.RLock()
	defer store.lifecycle.RUnlock()
	if ctx == nil || key == "" || write == nil || store == nil || store.root == nil {
		return objectstore.Object{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return objectstore.Object{}, fmt.Errorf("write local ciphertext: %w", err)
	}
	version, err := store.newVersion()
	if err != nil {
		return objectstore.Object{}, err
	}
	directory := string(key)
	if err := store.root.MkdirAll(directory, 0o750); err != nil {
		return objectstore.Object{}, ErrUnavailable
	}
	temporaryName := path.Join(directory, ".pending-"+version)
	finalName := path.Join(directory, version)
	file, err := store.root.OpenFile(temporaryName, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return objectstore.Object{}, ErrUnavailable
	}
	committed := false
	defer func() {
		if !committed {
			_ = store.root.Remove(temporaryName)
		}
	}()

	digest := sha256.New()
	writer := &boundedWriter{
		ctxErr:  ctx.Err,
		writer:  io.MultiWriter(file, digest),
		maximum: store.maximum,
	}
	if err := write(writer); err != nil {
		_ = file.Close()
		return objectstore.Object{}, fmt.Errorf("produce ciphertext object: %w", err)
	}
	if err := ctx.Err(); err != nil {
		_ = file.Close()
		return objectstore.Object{}, fmt.Errorf("write local ciphertext: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return objectstore.Object{}, ErrUnavailable
	}
	if err := file.Close(); err != nil {
		return objectstore.Object{}, ErrUnavailable
	}
	if err := store.root.Link(temporaryName, finalName); err != nil {
		return objectstore.Object{}, ErrUnavailable
	}
	if err := store.root.Remove(temporaryName); err != nil {
		_ = store.root.Remove(finalName)
		return objectstore.Object{}, ErrUnavailable
	}
	if err := syncDirectory(store.root, directory); err != nil {
		_ = store.root.Remove(finalName)
		return objectstore.Object{}, ErrUnavailable
	}
	committed = true

	return objectstore.NewObject(objectstore.ObjectRecord{
		Key:      string(key),
		Version:  version,
		Size:     writer.written,
		Checksum: digestValue(digest),
	})
}

// Open returns a reader that verifies the exact size and SHA-256 checksum at EOF.
func (store *Store) Open(ctx context.Context, object objectstore.Object) (io.ReadCloser, error) {
	if store == nil {
		return nil, ErrUnavailable
	}
	store.lifecycle.RLock()
	defer store.lifecycle.RUnlock()
	if ctx == nil || store == nil || store.root == nil || object.IsZero() {
		return nil, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("open local ciphertext: %w", err)
	}
	record := object.Record()
	file, err := store.root.Open(path.Join(record.Key, record.Version))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrUnavailable
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != record.Size {
		_ = file.Close()
		return nil, ErrIntegrity
	}

	return &verifyingReader{
		file:     file,
		digest:   sha256.New(),
		ctxErr:   ctx.Err,
		expected: record.Checksum,
		size:     record.Size,
	}, nil
}

// Delete removes only the exact immutable ciphertext version. Absence is success.
func (store *Store) Delete(ctx context.Context, object objectstore.Object) error {
	if store == nil {
		return ErrUnavailable
	}
	store.lifecycle.RLock()
	defer store.lifecycle.RUnlock()
	if ctx == nil || store == nil || store.root == nil || object.IsZero() {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete local ciphertext: %w", err)
	}
	record := object.Record()
	name := path.Join(record.Key, record.Version)
	if err := store.root.Remove(name); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return ErrUnavailable
	}
	if err := syncDirectory(store.root, record.Key); err != nil {
		return ErrUnavailable
	}

	return nil
}

// ListInventory returns one deterministic page of exact physical objects below
// a caller-owned logical prefix. Partial files and non-regular entries are excluded.
func (store *Store) ListInventory(
	ctx context.Context,
	prefix objectstore.Key,
	cursor string,
	limit int,
) (objectstore.InventoryPage, error) {
	if store == nil {
		return objectstore.InventoryPage{}, ErrUnavailable
	}
	store.lifecycle.RLock()
	defer store.lifecycle.RUnlock()
	if ctx == nil || store.root == nil || prefix == "" || !objectstore.ValidInventoryCursor(cursor) ||
		limit < 1 || limit > objectstore.MaxInventoryPageSize {
		return objectstore.InventoryPage{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return objectstore.InventoryPage{}, fmt.Errorf("list local ciphertext inventory: %w", err)
	}

	objects := make([]objectstore.InventoryObject, 0, limit)
	next := ""
	err := fs.WalkDir(store.root.FS(), string(prefix), func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() || strings.HasPrefix(path.Base(name), ".pending-") || name <= cursor {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		key, isValid := inventoryKey(path.Dir(name))
		if !isValid {
			return nil
		}
		object, isValid := inventoryObject(objectstore.InventoryRecord{
			Key: string(key), Version: path.Base(name), Size: info.Size(), ModifiedAt: info.ModTime(),
		})
		if !isValid {
			return nil
		}
		if len(objects) == limit {
			next = path.Join(objects[len(objects)-1].Record().Key, objects[len(objects)-1].Record().Version)
			return fs.SkipAll
		}
		objects = append(objects, object)
		return nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return objectstore.NewInventoryPage(nil, "")
	}
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return objectstore.InventoryPage{}, fmt.Errorf("list local ciphertext inventory: %w", contextErr)
		}
		return objectstore.InventoryPage{}, ErrUnavailable
	}
	page, err := objectstore.NewInventoryPage(objects, next)
	if err != nil {
		return objectstore.InventoryPage{}, ErrUnavailable
	}
	return page, nil
}

func inventoryKey(value string) (objectstore.Key, bool) {
	key, err := objectstore.NewKey(value)
	return key, err == nil
}

func inventoryObject(record objectstore.InventoryRecord) (objectstore.InventoryObject, bool) {
	object, err := objectstore.NewInventoryObject(record)
	return object, err == nil
}

// DeleteInventory removes an exact provider-discovered physical object. It
// intentionally accepts no checksum and must only be used after authoritative
// orphan classification.
func (store *Store) DeleteInventory(ctx context.Context, object objectstore.InventoryObject) error {
	if store == nil {
		return ErrUnavailable
	}
	store.lifecycle.RLock()
	defer store.lifecycle.RUnlock()
	if ctx == nil || store.root == nil || object.IsZero() {
		return ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("delete local ciphertext inventory object: %w", err)
	}
	record := object.Record()
	if err := store.root.Remove(path.Join(record.Key, record.Version)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return ErrUnavailable
	}
	if err := syncDirectory(store.root, record.Key); err != nil {
		return ErrUnavailable
	}

	return nil
}

// Close releases the root directory handle.
func (store *Store) Close() error {
	if store == nil {
		return nil
	}
	store.lifecycle.Lock()
	defer store.lifecycle.Unlock()
	if store.root == nil {
		return nil
	}
	err := store.root.Close()
	store.root = nil
	if err != nil {
		return ErrUnavailable
	}

	return nil
}

func (store *Store) newVersion() (string, error) {
	value := make([]byte, versionEntropySize)
	store.randomMu.Lock()
	_, err := io.ReadFull(store.random, value)
	store.randomMu.Unlock()
	if err != nil {
		return "", ErrUnavailable
	}

	return hex.EncodeToString(value), nil
}

func syncDirectory(root *os.Root, name string) error {
	directory, err := root.Open(name)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()

	return directory.Sync()
}

func digestValue(digest hash.Hash) string {
	return "sha256:" + hex.EncodeToString(digest.Sum(nil))
}

type boundedWriter struct {
	ctxErr  func() error
	writer  io.Writer
	maximum int64
	written int64
}

func (writer *boundedWriter) Write(value []byte) (int, error) {
	if err := writer.ctxErr(); err != nil {
		return 0, err
	}
	if int64(len(value)) > writer.maximum-writer.written {
		return 0, ErrObjectTooLarge
	}
	written, err := writer.writer.Write(value)
	writer.written += int64(written)

	return written, err
}

type verifyingReader struct {
	file     *os.File
	digest   hash.Hash
	ctxErr   func() error
	expected string
	size     int64
	read     int64
	verified bool
}

func (reader *verifyingReader) Read(value []byte) (int, error) {
	if err := reader.ctxErr(); err != nil {
		return 0, err
	}
	read, err := reader.file.Read(value)
	if read > 0 {
		_, _ = reader.digest.Write(value[:read])
		reader.read += int64(read)
	}
	if errors.Is(err, io.EOF) && !reader.verified {
		reader.verified = true
		actual := digestValue(reader.digest)
		if reader.read != reader.size || actual != reader.expected {
			return read, ErrIntegrity
		}
	}

	return read, err
}

func (reader *verifyingReader) Close() error {
	if err := reader.file.Close(); err != nil {
		return ErrUnavailable
	}

	return nil
}
