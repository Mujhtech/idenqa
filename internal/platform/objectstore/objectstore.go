// Package objectstore owns versioned ciphertext-object references and storage ports.
package objectstore

import (
	"errors"
	"path"
	"strings"
	"time"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
)

const (
	maxKeyLength    = 1024
	maxCursorLength = 4096
)

// MaxInventoryPageSize is the common upper bound for provider inventory pages.
const MaxInventoryPageSize = 1000

var (
	// ErrUnavailable identifies an object-store availability failure.
	ErrUnavailable = platformcrypto.ErrCiphertextUnavailable
	// ErrIntegrity identifies a stored ciphertext size or checksum failure.
	ErrIntegrity = platformcrypto.ErrCiphertextIntegrity
	// ErrObjectTooLarge identifies ciphertext that exceeded an adapter's configured bound.
	ErrObjectTooLarge = errors.New("objectstore: object too large")
)

// Key is an opaque, relative object-store key safe for the local adapter.
type Key string

// NewKey validates an internally generated object key.
func NewKey(value string) (Key, error) {
	if value == "" || len(value) > maxKeyLength || strings.HasPrefix(value, "/") ||
		strings.Contains(value, "\\") || path.Clean(value) != value || value == "." {
		return "", errors.New("objectstore: invalid object key")
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == "" || segment == ".." || !validSegment(segment) {
			return "", errors.New("objectstore: invalid object key")
		}
	}

	return Key(value), nil
}

func validSegment(value string) bool {
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') &&
			(character < '0' || character > '9') && character != '_' && character != '-' && character != '.' {
			return false
		}
	}
	return true
}

// ObjectRecord is the durable exact reference to one ciphertext version.
type ObjectRecord struct {
	Key      string
	Version  string
	Size     int64
	Checksum string
}

// Object is a validated immutable ciphertext-object reference.
type Object struct {
	record   ObjectRecord
	key      Key
	checksum platformcrypto.Digest
}

// NewObject validates an exact object version returned by a storage adapter.
func NewObject(record ObjectRecord) (Object, error) {
	key, err := NewKey(record.Key)
	if err != nil {
		return Object{}, err
	}
	if !validVersion(record.Version) || record.Size < 0 {
		return Object{}, errors.New("objectstore: invalid object metadata")
	}
	checksum, err := platformcrypto.NewDigest(record.Checksum)
	if err != nil {
		return Object{}, err
	}
	record.Key = string(key)
	record.Checksum = string(checksum)

	return Object{record: record, key: key, checksum: checksum}, nil
}

// Record returns the durable object metadata.
func (object Object) Record() ObjectRecord { return object.record }

// Key returns the validated object key.
func (object Object) Key() Key { return object.key }

// IsZero reports whether the object reference has not been initialised.
func (object Object) IsZero() bool { return object.key == "" }

// InventoryRecord identifies one exact physical object without claiming its
// content checksum. Inventory references are only suitable for old-orphan
// discovery and exact deletion, never for reads or evidence persistence.
type InventoryRecord struct {
	Key        string
	Version    string
	Size       int64
	ModifiedAt time.Time
}

// InventoryObject is a validated exact physical object discovered by an adapter.
type InventoryObject struct {
	record InventoryRecord
	key    Key
}

// NewInventoryObject validates provider inventory metadata.
func NewInventoryObject(record InventoryRecord) (InventoryObject, error) {
	key, err := NewKey(record.Key)
	if err != nil {
		return InventoryObject{}, err
	}
	if !validVersion(record.Version) || record.Size < 0 || record.ModifiedAt.IsZero() {
		return InventoryObject{}, errors.New("objectstore: invalid inventory metadata")
	}
	record.Key = string(key)
	record.ModifiedAt = record.ModifiedAt.UTC()

	return InventoryObject{record: record, key: key}, nil
}

// Record returns the exact provider inventory metadata.
func (object InventoryObject) Record() InventoryRecord { return object.record }

// Key returns the validated logical object key.
func (object InventoryObject) Key() Key { return object.key }

// IsZero reports whether the inventory reference is uninitialised.
func (object InventoryObject) IsZero() bool { return object.key == "" }

// InventoryPage is one bounded provider inventory page and its opaque cursor.
type InventoryPage struct {
	objects []InventoryObject
	next    string
}

// NewInventoryPage validates a bounded inventory page.
func NewInventoryPage(objects []InventoryObject, next string) (InventoryPage, error) {
	if len(objects) > MaxInventoryPageSize || len(next) > maxCursorLength {
		return InventoryPage{}, errors.New("objectstore: invalid inventory page")
	}
	for _, object := range objects {
		if object.IsZero() {
			return InventoryPage{}, errors.New("objectstore: invalid inventory page")
		}
	}

	return InventoryPage{objects: append([]InventoryObject(nil), objects...), next: next}, nil
}

// Objects returns a caller-owned copy of the discovered objects.
func (page InventoryPage) Objects() []InventoryObject {
	return append([]InventoryObject(nil), page.objects...)
}

// Next returns the provider's opaque continuation cursor, or an empty string.
func (page InventoryPage) Next() string { return page.next }

// ValidInventoryCursor reports whether a provider cursor is within the owned bound.
func ValidInventoryCursor(cursor string) bool { return len(cursor) <= maxCursorLength }

func validVersion(version string) bool {
	return strings.TrimSpace(version) == version && version != "" && len(version) <= 200 &&
		validSegment(version) && version != "." && version != ".."
}
