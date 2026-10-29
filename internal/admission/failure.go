package admission

import "fmt"

var (
	// ErrPressure denotes temporary rate or concurrency exhaustion.
	ErrPressure = fmt.Errorf("%w: temporary pressure", ErrQuota)
	// ErrCeiling denotes a configured resource or per-operation hard ceiling.
	ErrCeiling = fmt.Errorf("%w: configured ceiling", ErrQuota)
	// ErrUsage denotes exhaustion of an explicitly configured usage interval.
	ErrUsage = fmt.Errorf("%w: usage interval", ErrQuota)
	// ErrCapacity denotes unavailable capacity in the operation's region.
	ErrCapacity = fmt.Errorf("%w: regional capacity", ErrQuota)
	// ErrSafety is reserved for an independently established platform safety limit.
	ErrSafety = fmt.Errorf("%w: platform safety", ErrQuota)
)

// QuotaFailure classifies exhaustion without disclosing keys or other tenants.
// Impossible single-operation quantities cannot be cured by waiting for a slot.
func QuotaFailure(kind string, impossible bool) error {
	if impossible {
		return ErrCeiling
	}
	switch kind {
	case "rate", "concurrency":
		return ErrPressure
	case "usage":
		return ErrUsage
	case "capacity":
		return ErrCapacity
	default:
		return ErrCeiling
	}
}
