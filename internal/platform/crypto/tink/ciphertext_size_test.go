package tink_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"testing"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/kms/local"
)

func TestNativeCiphertextSizeBoundAcrossFirstAndLaterSegmentEdges(t *testing.T) {
	t.Parallel()
	keyring, err := local.Create(filepath.Join(t.TempDir(), "keyring.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = keyring.Close() })
	streaming := mustStreaming(t, keyring, "idenqa.evidence.content")
	authenticated := mustContext(t, []byte(`{"tenant_id":"tenant","evidence_id":"evidence"}`))
	const first = (1 << 20) - 16 - 40
	const next = (1 << 20) - 16
	for _, size := range []int64{0, 1, 8, first - 1, first, first + 1, first + next - 1, first + next, first + next + 1, 3 * next} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var ciphertext bytes.Buffer
			if _, err := streaming.Seal(context.Background(), &ciphertext, io.LimitReader(repeatingBytes{}, size), authenticated); err != nil {
				t.Fatal(err)
			}
			bound, err := platformcrypto.MaximumCiphertextBytes(1, "AES256_GCM_HKDF_1MB", size)
			if err != nil || int64(ciphertext.Len()) != bound {
				t.Fatalf("actual=%d bound=%d error=%v", ciphertext.Len(), bound, err)
			}
		})
	}
}

type repeatingBytes struct{}

func (repeatingBytes) Read(p []byte) (int, error) { clear(p); return len(p), nil }
