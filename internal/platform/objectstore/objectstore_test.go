package objectstore_test

import (
	"testing"
	"time"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
)

func TestKeyRejectsUnsafeLocalPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "absolute", value: "/ciphertext"},
		{name: "parent", value: "tenant/../ciphertext"},
		{name: "backslash", value: `tenant\ciphertext`},
		{name: "empty segment", value: "tenant//ciphertext"},
		{name: "control character", value: "tenant/ciphertext\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := objectstore.NewKey(test.value); err == nil {
				t.Errorf("NewKey(%q) error = nil", test.value)
			}
		})
	}
}

func TestObjectRequiresExactVersionAndChecksum(t *testing.T) {
	t.Parallel()

	record := objectstore.ObjectRecord{
		Key: "ten/evd/content", Version: "version-1", Size: 12,
		Checksum: string(platformcrypto.Sum([]byte("ciphertext"))),
	}
	object, err := objectstore.NewObject(record)
	if err != nil {
		t.Fatalf("NewObject() error = %v", err)
	}
	if object.Record() != record {
		t.Fatalf("Record() = %+v, want %+v", object.Record(), record)
	}
}

func TestObjectRejectsUnsafeVersion(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"", ".", "..", "../peer", "version/peer", "version\\peer"} {
		_, err := objectstore.NewObject(objectstore.ObjectRecord{
			Key: "tenant/evidence/content", Version: version, Size: 1,
			Checksum: string(platformcrypto.Sum([]byte("ciphertext"))),
		})
		if err == nil {
			t.Errorf("NewObject(version=%q) error = nil", version)
		}
	}
}

func TestInventoryObjectRequiresExactProviderMetadata(t *testing.T) {
	t.Parallel()

	modifiedAt := time.Date(2026, time.August, 29, 10, 0, 0, 0, time.FixedZone("test", 3600))
	record := objectstore.InventoryRecord{
		Key: "tenants/ten_1/evidence/evd_1/content/1", Version: "version-1",
		Size: 12, ModifiedAt: modifiedAt,
	}
	object, err := objectstore.NewInventoryObject(record)
	if err != nil {
		t.Fatalf("NewInventoryObject() error = %v", err)
	}
	if got := object.Record().ModifiedAt; !got.Equal(modifiedAt) || got.Location() != time.UTC {
		t.Fatalf("ModifiedAt = %v, want UTC %v", got, modifiedAt.UTC())
	}
	page, err := objectstore.NewInventoryPage([]objectstore.InventoryObject{object}, "next-token")
	if err != nil {
		t.Fatalf("NewInventoryPage() error = %v", err)
	}
	objects := page.Objects()
	objects[0] = objectstore.InventoryObject{}
	if page.Objects()[0].IsZero() || page.Next() != "next-token" {
		t.Fatal("InventoryPage exposed mutable state or lost its cursor")
	}
}

func TestInventoryObjectRejectsIncompleteMetadata(t *testing.T) {
	t.Parallel()

	valid := objectstore.InventoryRecord{
		Key: "tenants/ten_1/evidence/evd_1/content/1", Version: "version-1",
		Size: 12, ModifiedAt: time.Now(),
	}
	tests := []struct {
		name   string
		change func(*objectstore.InventoryRecord)
	}{
		{name: "unsafe key", change: func(record *objectstore.InventoryRecord) { record.Key = "../peer" }},
		{name: "unsafe version", change: func(record *objectstore.InventoryRecord) { record.Version = "../peer" }},
		{name: "negative size", change: func(record *objectstore.InventoryRecord) { record.Size = -1 }},
		{name: "missing modification time", change: func(record *objectstore.InventoryRecord) { record.ModifiedAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := valid
			test.change(&record)
			if _, err := objectstore.NewInventoryObject(record); err == nil {
				t.Fatal("NewInventoryObject() error = nil")
			}
		})
	}
}
