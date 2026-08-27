package cursor_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/cursor"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

func TestCodecRoundTripAndBindings(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	tenantID := mustTenant(t, 1)
	codec := mustCodec(t, now)
	position := json.RawMessage(`{"created_at":"2026-08-27T11:00:00Z","id":"prf_01K3P4NQF00000000000000000"}`)
	encoded, err := codec.Encode(tenantID, "capture_profiles:list", position)
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	claims, err := codec.Decode(encoded, tenantID, "capture_profiles:list")
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if !bytes.Equal(claims.Position, position) || !claims.Expires.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("Decode() = %+v", claims)
	}

	otherTenant := mustTenant(t, 2)
	for _, test := range []struct {
		name   string
		token  string
		tenant id.Tenant
		query  string
	}{
		{name: "tenant", token: encoded, tenant: otherTenant, query: "capture_profiles:list"},
		{name: "query", token: encoded, tenant: tenantID, query: "capture_profiles:list?state=active"},
		{name: "tampered", token: strings.Replace(encoded, "cur.", "cur.x", 1), tenant: tenantID, query: "capture_profiles:list"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := codec.Decode(test.token, test.tenant, test.query); !errors.Is(err, cursor.ErrInvalid) {
				t.Fatalf("Decode() error = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestCodecRejectsExpiredAndUnknownKey(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	tenantID := mustTenant(t, 3)
	codec := mustCodec(t, now)
	encoded, err := codec.Encode(tenantID, "capture_profiles:list", json.RawMessage(`{"id":"one"}`))
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	expired := mustCodec(t, now.Add(15*time.Minute))
	if _, err := expired.Decode(encoded, tenantID, "capture_profiles:list"); !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("expired Decode() error = %v, want ErrInvalid", err)
	}
	parts := strings.Split(encoded, ".")
	parts[2] = "2"
	if _, err := codec.Decode(strings.Join(parts, "."), tenantID, "capture_profiles:list"); !errors.Is(err, cursor.ErrInvalid) {
		t.Fatalf("unknown key Decode() error = %v, want ErrInvalid", err)
	}
}

func TestNewKeyringRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		active cursor.KeyVersion
		keys   map[cursor.KeyVersion][]byte
	}{
		{name: "empty"},
		{name: "missing active", active: 2, keys: map[cursor.KeyVersion][]byte{1: bytes.Repeat([]byte{1}, 32)}},
		{name: "short key", active: 1, keys: map[cursor.KeyVersion][]byte{1: bytes.Repeat([]byte{1}, 31)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := cursor.NewKeyring(test.active, test.keys); err == nil {
				t.Fatal("NewKeyring() error = nil")
			}
		})
	}
}

func mustCodec(t *testing.T, now time.Time) *cursor.Codec {
	t.Helper()

	keys, err := cursor.NewKeyring(1, map[cursor.KeyVersion][]byte{1: bytes.Repeat([]byte{9}, 32)})
	if err != nil {
		t.Fatalf("NewKeyring() error = %v", err)
	}
	codec, err := cursor.New(keys, fixedClock{now: now}, 15*time.Minute)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	return codec
}

func mustTenant(t *testing.T, entropy byte) id.Tenant {
	t.Helper()

	generator, err := id.NewGenerator(
		fixedClock{now: time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)},
		bytes.NewReader(bytes.Repeat([]byte{entropy}, 32)),
	)
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	tenantID, err := generator.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}

	return tenantID
}
