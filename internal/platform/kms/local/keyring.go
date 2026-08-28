// Package local provides the open-source file-backed key-encryption-key provider.
package local

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"github.com/Mujhtech/idenqa/internal/platform/kms"
)

const (
	keyringSchemaVersion = 1
	keySize              = 32
	identifierSize       = 16
	maximumFileSize      = 1024 * 1024
	maximumKeys          = 128
	maximumKeysetSize    = 60 * 1024
	maximumContextSize   = 32 * 1024
	provider             = "local.file"
	algorithm            = "AES256_GCM"
	wrappingDomain       = "idenqa.local-kek-wrap.v1"
)

var (
	// ErrInvalidKeyring reports malformed or insecure keyring state without
	// exposing its configured path or key material.
	ErrInvalidKeyring = errors.New("local kms: invalid keyring")
	// ErrWrapFailed reports that a key could not be protected.
	ErrWrapFailed = errors.New("local kms: key wrapping failed")
	// ErrUnwrapFailed reports every unavailable-key or authentication failure.
	ErrUnwrapFailed = errors.New("local kms: key unwrapping failed")
)

type diskKeyring struct {
	SchemaVersion int               `json:"schema_version"`
	KeyringID     string            `json:"keyring_id"`
	ActiveVersion string            `json:"active_version"`
	Keys          map[string]string `json:"keys"`
}

// Keyring wraps small content keysets with versioned 256-bit local KEKs.
// Call Close when the process releases it so retained key copies are cleared.
type Keyring struct {
	mutex    sync.RWMutex
	randomMu sync.Mutex
	path     string
	id       string
	active   string
	keys     map[string][]byte
	random   io.Reader
}

// Create atomically initialises a keyring without replacing an existing file.
func Create(path string) (*Keyring, error) {
	return create(path, rand.Reader)
}

func create(path string, random io.Reader) (*Keyring, error) {
	if random == nil {
		return nil, ErrInvalidKeyring
	}
	identifier := make([]byte, identifierSize)
	key := make([]byte, keySize)
	if _, err := io.ReadFull(random, identifier); err != nil {
		return nil, ErrInvalidKeyring
	}
	if _, err := io.ReadFull(random, key); err != nil {
		clear(identifier)
		return nil, ErrInvalidKeyring
	}
	defer clear(identifier)

	document := diskKeyring{
		SchemaVersion: keyringSchemaVersion,
		KeyringID:     "local-" + hex.EncodeToString(identifier),
		ActiveVersion: "v1",
		Keys: map[string]string{
			"v1": base64.StdEncoding.EncodeToString(key),
		},
	}
	if err := writeKeyring(path, document, false); err != nil {
		clear(key)
		return nil, err
	}

	return &Keyring{
		path:   filepath.Clean(path),
		id:     document.KeyringID,
		active: document.ActiveVersion,
		keys:   map[string][]byte{"v1": key},
		random: random,
	}, nil
}

// Open validates and loads an existing owner-only keyring file.
func Open(path string) (*Keyring, error) {
	return open(path, rand.Reader)
}

func open(path string, random io.Reader) (*Keyring, error) {
	if random == nil {
		return nil, ErrInvalidKeyring
	}
	document, keys, err := readKeyring(path)
	if err != nil {
		return nil, err
	}

	return &Keyring{
		path:   filepath.Clean(path),
		id:     document.KeyringID,
		active: document.ActiveVersion,
		keys:   keys,
		random: random,
	}, nil
}

// Wrap protects one small content key or keyset under the active KEK.
func (keyring *Keyring) Wrap(
	ctx context.Context,
	purpose kms.Purpose,
	plaintext []byte,
	authenticatedContext []byte,
) (kms.WrappedKey, error) {
	if err := validateOperation(ctx, purpose, plaintext, authenticatedContext); err != nil {
		return kms.WrappedKey{}, ErrWrapFailed
	}

	keyring.mutex.RLock()
	version := keyring.active
	key := append([]byte(nil), keyring.keys[version]...)
	reference := keyring.id
	keyring.mutex.RUnlock()
	defer clear(key)

	block, err := aes.NewCipher(key)
	if err != nil {
		return kms.WrappedKey{}, ErrWrapFailed
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return kms.WrappedKey{}, ErrWrapFailed
	}
	nonce := make([]byte, aead.NonceSize())
	keyring.randomMu.Lock()
	_, randomErr := io.ReadFull(keyring.random, nonce)
	keyring.randomMu.Unlock()
	if randomErr != nil {
		return kms.WrappedKey{}, ErrWrapFailed
	}

	ciphertext := aead.Seal(nonce, nonce, plaintext, wrappingAAD(purpose, authenticatedContext))
	wrapped, err := kms.NewWrappedKey(kms.WrappedKeyRecord{
		Provider:   provider,
		Reference:  reference,
		Version:    version,
		Algorithm:  algorithm,
		Ciphertext: ciphertext,
	})
	clear(ciphertext)
	if err != nil {
		return kms.WrappedKey{}, ErrWrapFailed
	}

	return wrapped, nil
}

// Unwrap releases a key only for its exact provider, keyring, version,
// purpose, and authenticated context. It never falls back to another version.
func (keyring *Keyring) Unwrap(
	ctx context.Context,
	purpose kms.Purpose,
	wrapped kms.WrappedKey,
	authenticatedContext []byte,
) ([]byte, error) {
	if err := validateOperation(ctx, purpose, []byte{1}, authenticatedContext); err != nil || wrapped.IsZero() {
		return nil, ErrUnwrapFailed
	}
	record := wrapped.Record()
	if record.Provider != provider || record.Reference == "" || record.Version == "" || record.Algorithm != algorithm {
		return nil, ErrUnwrapFailed
	}

	keyring.mutex.RLock()
	if record.Reference != keyring.id {
		keyring.mutex.RUnlock()
		return nil, ErrUnwrapFailed
	}
	key := append([]byte(nil), keyring.keys[record.Version]...)
	keyring.mutex.RUnlock()
	defer clear(key)
	if len(key) != keySize {
		return nil, ErrUnwrapFailed
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrUnwrapFailed
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || len(record.Ciphertext) < aead.NonceSize()+aead.Overhead() {
		return nil, ErrUnwrapFailed
	}
	nonce := record.Ciphertext[:aead.NonceSize()]
	plaintext, err := aead.Open(nil, nonce, record.Ciphertext[aead.NonceSize():], wrappingAAD(purpose, authenticatedContext))
	if err != nil {
		return nil, ErrUnwrapFailed
	}

	return plaintext, nil
}

// Rotate atomically adds and activates the next KEK version. Older versions
// remain readable until an audited rewrap and recovery check permits removal.
func (keyring *Keyring) Rotate(ctx context.Context) (string, error) {
	if ctx == nil {
		return "", ErrInvalidKeyring
	}
	if err := ctx.Err(); err != nil {
		return "", ErrInvalidKeyring
	}
	keyring.mutex.Lock()
	defer keyring.mutex.Unlock()

	nextNumber, err := strconv.Atoi(strings.TrimPrefix(keyring.active, "v"))
	if err != nil || nextNumber < 1 || len(keyring.keys) >= maximumKeys {
		return "", ErrInvalidKeyring
	}
	nextVersion := "v" + strconv.Itoa(nextNumber+1)
	if _, exists := keyring.keys[nextVersion]; exists {
		return "", ErrInvalidKeyring
	}
	key := make([]byte, keySize)
	keyring.randomMu.Lock()
	_, randomErr := io.ReadFull(keyring.random, key)
	keyring.randomMu.Unlock()
	if randomErr != nil {
		clear(key)
		return "", ErrInvalidKeyring
	}

	keys := cloneKeys(keyring.keys)
	keys[nextVersion] = key
	document := encodeKeyring(keyring.id, nextVersion, keys)
	if err := writeKeyring(keyring.path, document, true); err != nil {
		clearKeys(keys)
		return "", err
	}
	clearKeys(keyring.keys)
	keyring.keys = keys
	keyring.active = nextVersion

	return nextVersion, nil
}

// Close clears retained in-memory key copies. The keyring must not be reused.
func (keyring *Keyring) Close() error {
	keyring.mutex.Lock()
	defer keyring.mutex.Unlock()
	clearKeys(keyring.keys)
	keyring.keys = nil
	keyring.active = ""
	keyring.id = ""

	return nil
}

func validateOperation(ctx context.Context, purpose kms.Purpose, value, authenticatedContext []byte) error {
	if ctx == nil || len(value) == 0 || len(value) > maximumKeysetSize ||
		len(authenticatedContext) == 0 || len(authenticatedContext) > maximumContextSize {
		return ErrInvalidKeyring
	}
	if err := ctx.Err(); err != nil {
		return ErrInvalidKeyring
	}
	if _, err := kms.NewPurpose(string(purpose)); err != nil {
		return ErrInvalidKeyring
	}

	return nil
}

func wrappingAAD(purpose kms.Purpose, authenticatedContext []byte) []byte {
	aad := make([]byte, 0, len(wrappingDomain)+len(purpose)+len(authenticatedContext)+24)
	aad = binary.AppendUvarint(aad, uint64(len(wrappingDomain)))
	aad = append(aad, wrappingDomain...)
	aad = binary.AppendUvarint(aad, uint64(len(purpose)))
	aad = append(aad, purpose...)
	aad = binary.AppendUvarint(aad, uint64(len(authenticatedContext)))
	aad = append(aad, authenticatedContext...)

	return aad
}

func readKeyring(path string) (diskKeyring, map[string][]byte, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return diskKeyring{}, nil, ErrInvalidKeyring
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maximumFileSize {
		return diskKeyring{}, nil, ErrInvalidKeyring
	}
	decoder := json.NewDecoder(io.LimitReader(file, maximumFileSize+1))
	decoder.DisallowUnknownFields()
	var document diskKeyring
	if err := decoder.Decode(&document); err != nil {
		return diskKeyring{}, nil, ErrInvalidKeyring
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return diskKeyring{}, nil, ErrInvalidKeyring
	}
	keys, err := decodeKeyring(document)
	if err != nil {
		return diskKeyring{}, nil, err
	}

	return document, keys, nil
}

func decodeKeyring(document diskKeyring) (map[string][]byte, error) {
	if document.SchemaVersion != keyringSchemaVersion || !validKeyringID(document.KeyringID) ||
		!validVersion(document.ActiveVersion) || len(document.Keys) == 0 || len(document.Keys) > maximumKeys {
		return nil, ErrInvalidKeyring
	}
	keys := make(map[string][]byte, len(document.Keys))
	for version, encoded := range document.Keys {
		if !validVersion(version) {
			clearKeys(keys)
			return nil, ErrInvalidKeyring
		}
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil || len(key) != keySize {
			clear(key)
			clearKeys(keys)
			return nil, ErrInvalidKeyring
		}
		keys[version] = key
	}
	if _, exists := keys[document.ActiveVersion]; !exists {
		clearKeys(keys)
		return nil, ErrInvalidKeyring
	}

	return keys, nil
}

func validKeyringID(value string) bool {
	const prefix = "local-"
	if len(value) != len(prefix)+(identifierSize*2) || !strings.HasPrefix(value, prefix) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, prefix))

	return err == nil
}

func validVersion(value string) bool {
	if len(value) < 2 || value[0] != 'v' || value[1] == '0' {
		return false
	}
	number, err := strconv.Atoi(value[1:])

	return err == nil && number > 0 && "v"+strconv.Itoa(number) == value
}

func encodeKeyring(identifier, active string, keys map[string][]byte) diskKeyring {
	encoded := make(map[string]string, len(keys))
	for version, key := range keys {
		encoded[version] = base64.StdEncoding.EncodeToString(key)
	}

	return diskKeyring{SchemaVersion: keyringSchemaVersion, KeyringID: identifier, ActiveVersion: active, Keys: encoded}
}

func writeKeyring(path string, document diskKeyring, replace bool) error {
	cleanPath := filepath.Clean(path)
	directory := filepath.Dir(cleanPath)
	temporary, err := os.CreateTemp(directory, ".idenqa-keyring-*")
	if err != nil {
		return ErrInvalidKeyring
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return ErrInvalidKeyring
	}
	encoder := json.NewEncoder(temporary)
	if err := encoder.Encode(document); err != nil {
		_ = temporary.Close()
		return ErrInvalidKeyring
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return ErrInvalidKeyring
	}
	if err := temporary.Close(); err != nil {
		return ErrInvalidKeyring
	}

	if replace {
		info, err := os.Lstat(cleanPath)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalidKeyring
		}
		if err := os.Rename(temporaryPath, cleanPath); err != nil {
			return ErrInvalidKeyring
		}
	} else {
		if err := os.Link(temporaryPath, cleanPath); err != nil {
			return ErrInvalidKeyring
		}
		if err := os.Remove(temporaryPath); err != nil {
			return ErrInvalidKeyring
		}
	}
	// The directory is derived only from the operator-configured keyring path
	// and is opened solely to durably sync the atomic replacement.
	directoryFile, err := os.Open(directory) //nolint:gosec // trusted configuration path, not request input
	if err != nil {
		return ErrInvalidKeyring
	}
	defer func() { _ = directoryFile.Close() }()
	if err := directoryFile.Sync(); err != nil {
		return ErrInvalidKeyring
	}

	return nil
}

func cloneKeys(source map[string][]byte) map[string][]byte {
	cloned := make(map[string][]byte, len(source))
	for version, key := range source {
		cloned[version] = append([]byte(nil), key...)
	}

	return cloned
}

func clearKeys(keys map[string][]byte) {
	for version, key := range keys {
		clear(key)
		delete(keys, version)
	}
}
