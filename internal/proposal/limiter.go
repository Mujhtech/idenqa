package proposal

import (
	"context"
	"sync"

	proposalv1 "github.com/Mujhtech/idenqa/contracts/proposal/v1"
)

// InMemoryLimiter is a bounded in-memory rate limiter for tests.
// It allows up to limit proposals per tenant+verification per kind per hour.
type InMemoryLimiter struct {
	mu     sync.Mutex
	counts map[string]int
	limit  int
}

// NewInMemoryLimiter returns a bounded in-memory rate limiter. A non-positive
// limit defaults to 100 allowed proposals per tenant+verification per kind.
func NewInMemoryLimiter(limit int) *InMemoryLimiter {
	if limit <= 0 {
		limit = 100
	}
	return &InMemoryLimiter{counts: make(map[string]int), limit: limit}
}

func limiterKey(tenantID, verificationID string, kind proposalv1.ActionKind) string {
	return tenantID + ":" + verificationID + ":" + string(kind)
}

// Allow reports whether one more proposal of kind is permitted.
func (limiter *InMemoryLimiter) Allow(_ context.Context, tenantID string, verificationID string, kind proposalv1.ActionKind) (bool, error) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	key := limiterKey(tenantID, verificationID, kind)
	if limiter.counts[key] >= limiter.limit {
		return false, nil
	}
	limiter.counts[key]++
	return true, nil
}

// Reset clears all recorded allowance counts.
func (limiter *InMemoryLimiter) Reset() {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	limiter.counts = make(map[string]int)
}

var _ CostLimiter = (*InMemoryLimiter)(nil)
