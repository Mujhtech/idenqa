package task

import (
	"encoding/binary"
	"hash/fnv"
	"time"
)

// Backoff returns a bounded, deterministic delay for the next attempt.
// Attempt is one-based and seed should be the stable task identifier.
func (policy RetryPolicy) Backoff(attempt uint32, seed string) time.Duration {
	if policy.Validate() != nil || attempt == 0 {
		return 0
	}

	delay := policy.InitialBackoff
	for current := uint32(1); current < attempt && delay < policy.MaximumBackoff; current++ {
		if delay > policy.MaximumBackoff/2 {
			delay = policy.MaximumBackoff
			break
		}
		delay *= 2
	}
	if policy.JitterPercent == 0 {
		return delay
	}

	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(seed))
	var encodedAttempt [4]byte
	binary.BigEndian.PutUint32(encodedAttempt[:], attempt)
	_, _ = hasher.Write(encodedAttempt[:])
	// The stable fraction is in [-1000, 1000]. This makes every driver and
	// replay compute the same jitter without process-global randomness.
	fraction := int64(hasher.Sum64() % 2001)
	fraction -= 1000
	adjustment := (delay / 1000) * time.Duration(policy.JitterPercent) * time.Duration(fraction) / 100
	result := delay + adjustment
	if result < 0 {
		return 0
	}
	if result > policy.MaximumBackoff {
		return policy.MaximumBackoff
	}
	return result
}
