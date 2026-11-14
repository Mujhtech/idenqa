package postgres

import (
	"errors"
	"math"
	"strconv"
	"testing"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
	"github.com/Mujhtech/idenqa/internal/experience"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestVersionStorageBoundary(t *testing.T) {
	t.Parallel()
	for _, version := range []uint32{0, 1, contract.MaxVersion, contract.MaxVersion + 1, math.MaxInt32 + 1, math.MaxUint32} {
		t.Run(strconv.FormatUint(uint64(version), 10), func(t *testing.T) {
			stored, err := versionInt32(version)
			if version > contract.MaxVersion {
				if !errors.Is(err, experience.ErrInvalid) {
					t.Fatalf("version=%d error=%v", stored, err)
				}
				// Reject invalid input before it can address a wrapped revision
				// or insert a pin. Nil dependencies must never be reached.
				if _, err := loadRevision(t.Context(), nil, tenant.Scope{}, id.Experience{}, version); !errors.Is(err, experience.ErrInvalid) {
					t.Fatalf("loadRevision error=%v", err)
				}
				if _, err := (&Store{}).SavePin(t.Context(), tenant.Scope{}, experience.Pin{Version: version}); !errors.Is(err, experience.ErrInvalid) {
					t.Fatalf("SavePin error=%v", err)
				}
				return
			}
			if err != nil || int64(stored) != int64(version) {
				t.Fatalf("version=%d error=%v", stored, err)
			}
		})
	}
}
