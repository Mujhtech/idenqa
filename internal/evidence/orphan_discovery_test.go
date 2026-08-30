package evidence

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestOrphanDiscoveryServiceDeletesOnlyOldUnreferencedOwnedObjects(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	scope, _ := tenant.NewScope(tenantID)
	oldOrphan := inventoryFixture(t, tenantID.String(), "evd_01ARZ3NDEKTSV4RRFFQ69G5FAW", now.Add(-20*time.Minute))
	referenced := inventoryFixture(t, tenantID.String(), "evd_01ARZ3NDEKTSV4RRFFQ69G5FAX", now.Add(-20*time.Minute))
	recent := inventoryFixture(t, tenantID.String(), "evd_01ARZ3NDEKTSV4RRFFQ69G5FAY", now.Add(-time.Minute))
	ignored := inventoryFixture(t, tenantID.String(), "not-an-evidence-id", now.Add(-20*time.Minute))
	page, _ := objectstore.NewInventoryPage(
		[]objectstore.InventoryObject{oldOrphan, referenced, recent, ignored}, "next-page",
	)
	inventory := &orphanInventoryStub{page: page}
	references := &orphanReferenceStub{referenced: map[string]bool{
		inventoryAddress(referenced): true,
	}}
	service, err := NewOrphanDiscoveryService(
		inventory, references, reconciliationClock{now: now},
		MinimumOrphanAge, time.Minute,
	)
	if err != nil {
		t.Fatalf("NewOrphanDiscoveryService() error = %v", err)
	}
	result, err := service.DiscoverPage(context.Background(), scope, "", 10)
	if err != nil {
		t.Fatalf("DiscoverPage() error = %v", err)
	}
	if result.Examined != 4 || result.Deleted != 1 || result.Referenced != 1 ||
		result.Deferred != 1 || result.Ignored != 1 || result.Next != "next-page" {
		t.Fatalf("result = %+v", result)
	}
	if len(inventory.deleted) != 1 || inventory.deleted[0].Record() != oldOrphan.Record() {
		t.Fatalf("deleted = %+v", inventory.deleted)
	}
	if inventory.prefix != objectstore.Key("tenants/"+tenantID.String()+"/evidence") {
		t.Fatalf("inventory prefix = %q", inventory.prefix)
	}
}

func TestOrphanDiscoveryServiceReplaysPageAfterClassificationOrDeleteFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	scope, _ := tenant.NewScope(tenantID)
	object := inventoryFixture(t, tenantID.String(), "evd_01ARZ3NDEKTSV4RRFFQ69G5FAW", now.Add(-20*time.Minute))
	page, _ := objectstore.NewInventoryPage([]objectstore.InventoryObject{object}, "next-page")
	wantErr := errors.New("database unavailable")
	inventory := &orphanInventoryStub{page: page}
	references := &orphanReferenceStub{err: wantErr}
	service, err := NewOrphanDiscoveryService(
		inventory, references, reconciliationClock{now: now},
		MinimumOrphanAge, time.Minute,
	)
	if err != nil {
		t.Fatalf("NewOrphanDiscoveryService() error = %v", err)
	}
	result, err := service.DiscoverPage(context.Background(), scope, "current-page", 10)
	if !errors.Is(err, wantErr) || result.Next != "current-page" || len(inventory.deleted) != 0 {
		t.Fatalf("classification failure result=%+v deleted=%d error=%v", result, len(inventory.deleted), err)
	}

	wantErr = errors.New("storage unavailable")
	inventory.deleteErr = wantErr
	references.err = nil
	result, err = service.DiscoverPage(context.Background(), scope, "current-page", 10)
	if !errors.Is(err, wantErr) || result.Next != "current-page" || result.Deleted != 0 {
		t.Fatalf("delete failure result=%+v error=%v", result, err)
	}
}

func TestNewOrphanDiscoveryServiceRequiresLeaseSafeMinimumAge(t *testing.T) {
	t.Parallel()

	_, err := NewOrphanDiscoveryService(
		&orphanInventoryStub{}, &orphanReferenceStub{}, reconciliationClock{now: time.Now()},
		MinimumOrphanAge-time.Second, time.Minute,
	)
	if err == nil {
		t.Fatal("NewOrphanDiscoveryService(unsafe age) error = nil")
	}
}

func inventoryFixture(t *testing.T, tenantID, evidenceID string, modifiedAt time.Time) objectstore.InventoryObject {
	t.Helper()
	object, err := objectstore.NewInventoryObject(objectstore.InventoryRecord{
		Key:     "tenants/" + tenantID + "/evidence/" + evidenceID + "/content/1",
		Version: "0123456789abcdef0123456789abcdef", Size: 42, ModifiedAt: modifiedAt,
	})
	if err != nil {
		t.Fatalf("NewInventoryObject() error = %v", err)
	}
	return object
}

func inventoryAddress(object objectstore.InventoryObject) string {
	record := object.Record()
	return record.Key + "/" + record.Version
}

type orphanInventoryStub struct {
	page      objectstore.InventoryPage
	prefix    objectstore.Key
	deleted   []objectstore.InventoryObject
	deleteErr error
}

func (stub *orphanInventoryStub) ListInventory(
	_ context.Context,
	prefix objectstore.Key,
	_ string,
	_ int,
) (objectstore.InventoryPage, error) {
	stub.prefix = prefix
	return stub.page, nil
}

func (stub *orphanInventoryStub) DeleteInventory(
	_ context.Context,
	object objectstore.InventoryObject,
) error {
	if stub.deleteErr != nil {
		return stub.deleteErr
	}
	stub.deleted = append(stub.deleted, object)
	return nil
}

type orphanReferenceStub struct {
	referenced map[string]bool
	err        error
}

func (stub *orphanReferenceStub) IsObjectReferenced(
	_ context.Context,
	_ tenant.Scope,
	key objectstore.Key,
	version string,
) (bool, error) {
	return stub.referenced[string(key)+"/"+version], stub.err
}
