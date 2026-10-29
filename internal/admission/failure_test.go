package admission

import (
	"errors"
	"testing"
)

func TestQuotaClassificationPreservesQuotaIdentity(t *testing.T) {
	for _, test := range []struct {
		kind       string
		impossible bool
		want       error
	}{
		{"rate", false, ErrPressure}, {"concurrency", false, ErrPressure},
		{"count", false, ErrCeiling}, {"storage", false, ErrCeiling},
		{"usage", false, ErrUsage}, {"capacity", false, ErrCapacity},
		{"concurrency", true, ErrCeiling},
	} {
		got := QuotaFailure(test.kind, test.impossible)
		if !errors.Is(got, test.want) || !errors.Is(got, ErrQuota) {
			t.Fatalf("%s/%t: %v", test.kind, test.impossible, got)
		}
	}
}
