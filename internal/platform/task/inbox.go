package task

import (
	"context"
	"fmt"
	"sync"
)

// MemoryInbox is a deterministic inbox model for duplicate-effect tests. A
// production inbox remains PostgreSQL-owned and transaction-coupled to effects.
type MemoryInbox struct {
	mutex   sync.Mutex
	claimed map[string]uint64
}

// NewMemoryInbox constructs an empty deterministic inbox.
func NewMemoryInbox() *MemoryInbox {
	return &MemoryInbox{claimed: make(map[string]uint64)}
}

// Claim records the first effect and rejects stale fences. Replays at the same
// or a newer fence return false because the semantic effect already happened.
func (inbox *MemoryInbox) Claim(ctx context.Context, effect Effect) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if inbox == nil || effect.TaskID.IsZero() || effect.Key == "" ||
		!validMetadata(effect.Key, true) || effect.Fence == 0 {
		return false, fmt.Errorf("%w: inbox effect", ErrInvalid)
	}
	key := effect.TaskID.String() + "\x00" + effect.Key
	inbox.mutex.Lock()
	defer inbox.mutex.Unlock()
	claimedFence, exists := inbox.claimed[key]
	if exists {
		if effect.Fence < claimedFence {
			return false, ErrLeaseLost
		}
		return false, nil
	}
	inbox.claimed[key] = effect.Fence
	return true, nil
}

var _ Deduplicator = (*MemoryInbox)(nil)
