package crypto

import (
	"errors"
	"math"
	"testing"
)

func TestMaximumCiphertextBytesRejectsUnsupportedAndOverflow(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name      string
		format    uint32
		algorithm string
		size      int64
	}{{"version", 2, "AES256_GCM_HKDF_1MB", 1}, {"algorithm", 1, "unknown", 1}, {"negative", 1, "AES256_GCM_HKDF_1MB", -1}, {"overflow", 1, "AES256_GCM_HKDF_1MB", math.MaxInt64}, {"nonce range", 1, "AES256_GCM_HKDF_1MB", 1 << 60}} {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := MaximumCiphertextBytes(tt.format, tt.algorithm, tt.size); !errors.Is(err, ErrCiphertextSize) {
				t.Fatalf("unsafe bound: %v", err)
			}
		})
	}
}
