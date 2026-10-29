package crypto

import (
	"errors"
	"math"
)

// ErrCiphertextSize rejects an unsupported format or unrepresentable stream.
var ErrCiphertextSize = errors.New("crypto: unsupported ciphertext size bound")

// MaximumCiphertextBytes bounds the complete immutable ciphertext object for
// the selected format. Wrapped keyset metadata is stored separately in PostgreSQL.
// Format1 AES256_GCM_HKDF_1MB uses a RAW stream with a 40-byte header, 16-byte
// tags and 1MiB ciphertext segments. The first segment also contains the header.
// Native adapter boundary tests bind this owned calculation to the pinned Tink
// implementation; another encryption format must define its own bound.
func MaximumCiphertextBytes(format uint32, algorithm string, plaintext int64) (int64, error) {
	if format != 1 || algorithm != "AES256_GCM_HKDF_1MB" || plaintext < 0 || plaintext > math.MaxInt64-40 {
		return 0, ErrCiphertextSize
	}
	const payloadPerSegment = int64((1 << 20) - 16)
	offset := plaintext + 40
	segments := offset / payloadPerSegment
	if offset%payloadPerSegment != 0 {
		segments++
	}
	if segments < 1 {
		segments = 1
	}
	if segments > math.MaxUint32 || segments > (math.MaxInt64-offset)/16 {
		return 0, ErrCiphertextSize
	}
	return offset + segments*16, nil
}
