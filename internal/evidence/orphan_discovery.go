package evidence

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

const (
	maximumOrphanDeleteTimeout = 15 * time.Minute
	orphanClockSkewAllowance   = 5 * time.Minute
)

// MinimumOrphanAge is the shortest safe provider-inventory age: the longest
// upload lease plus an allowance for provider and application clock skew.
const MinimumOrphanAge = MaximumUploadAttemptTimeout + orphanClockSkewAllowance

// OrphanDiscoveryResult reports one bounded provider inventory page.
type OrphanDiscoveryResult struct {
	Examined   int
	Deferred   int
	Referenced int
	Ignored    int
	Deleted    int
	Next       string
}

// OrphanDiscoveryService removes old Idenqa-owned ciphertext versions only
// after authoritative PostgreSQL state proves that no durable reference protects them.
type OrphanDiscoveryService struct {
	inventory     ObjectInventory
	references    ObjectReferenceChecker
	clock         clock.Clock
	minimumAge    time.Duration
	deleteTimeout time.Duration
}

// NewOrphanDiscoveryService constructs tenant-scoped provider inventory recovery.
func NewOrphanDiscoveryService(
	inventory ObjectInventory,
	references ObjectReferenceChecker,
	source clock.Clock,
	minimumAge time.Duration,
	deleteTimeout time.Duration,
) (*OrphanDiscoveryService, error) {
	if inventory == nil || references == nil || source == nil ||
		minimumAge < MinimumOrphanAge || deleteTimeout <= 0 ||
		deleteTimeout > maximumOrphanDeleteTimeout {
		return nil, errors.New("evidence: orphan discovery dependencies and bounds are required")
	}

	return &OrphanDiscoveryService{
		inventory: inventory, references: references, clock: source,
		minimumAge: minimumAge, deleteTimeout: deleteTimeout,
	}, nil
}

// DiscoverPage classifies and deletes at most one provider inventory page.
// On failure it returns the original cursor so replay rechecks the whole page.
func (service *OrphanDiscoveryService) DiscoverPage(
	ctx context.Context,
	scope tenant.Scope,
	cursor string,
	limit int,
) (OrphanDiscoveryResult, error) {
	result := OrphanDiscoveryResult{Next: cursor}
	if service == nil || ctx == nil || scope.ID().IsZero() ||
		!objectstore.ValidInventoryCursor(cursor) || limit < 1 ||
		limit > objectstore.MaxInventoryPageSize {
		return result, errors.New("evidence: orphan discovery request is invalid")
	}
	prefix, err := objectstore.NewKey("tenants/" + scope.ID().String() + "/evidence")
	if err != nil {
		return result, errors.New("evidence: orphan discovery prefix is invalid")
	}
	page, err := service.inventory.ListInventory(ctx, prefix, cursor, limit)
	if err != nil {
		return result, fmt.Errorf("list evidence orphan inventory: %w", err)
	}
	cutoff := service.clock.Now().UTC().Add(-service.minimumAge)
	for _, object := range page.Objects() {
		result.Examined++
		if !ownedInventoryObject(scope, object) {
			result.Ignored++
			continue
		}
		if object.Record().ModifiedAt.After(cutoff) {
			result.Deferred++
			continue
		}
		referenced, err := service.references.IsObjectReferenced(
			ctx, scope, object.Key(), object.Record().Version,
		)
		if err != nil {
			return result, fmt.Errorf("classify evidence inventory object: %w", err)
		}
		if referenced {
			result.Referenced++
			continue
		}
		deleteContext, cancel := context.WithTimeout(ctx, service.deleteTimeout)
		err = service.inventory.DeleteInventory(deleteContext, object)
		cancel()
		if err != nil {
			return result, fmt.Errorf("delete evidence inventory orphan: %w", err)
		}
		result.Deleted++
	}
	result.Next = page.Next()

	return result, nil
}

func ownedInventoryObject(scope tenant.Scope, object objectstore.InventoryObject) bool {
	if object.IsZero() {
		return false
	}
	record := object.Record()
	segments := strings.Split(record.Key, "/")
	if len(segments) != 6 || segments[0] != "tenants" || segments[1] != scope.ID().String() ||
		segments[2] != "evidence" || segments[4] != "content" {
		return false
	}
	if _, err := id.ParseEvidence(segments[3]); err != nil {
		return false
	}
	revision, err := strconv.ParseUint(segments[5], 10, 64)
	if err != nil || revision == 0 {
		return false
	}
	version, err := hex.DecodeString(record.Version)
	return err == nil && len(version) == versionEntropyBytes && hex.EncodeToString(version) == record.Version
}

const versionEntropyBytes = 16
