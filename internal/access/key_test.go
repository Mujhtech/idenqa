package access_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

type keyClock struct{}

func (keyClock) Now() time.Time {
	return time.Date(2026, time.August, 27, 14, 0, 0, 0, time.UTC)
}

func TestPresentedKeyGenerationParsingAndRedaction(t *testing.T) {
	t.Parallel()

	tenant, key := testKeyIDs(t)
	generator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0xff}, 32)))
	if err != nil {
		t.Fatalf("NewKeyGenerator() error = %v", err)
	}
	presented, err := generator.Generate(tenant, key)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	raw := presented.Reveal()
	if len(raw) != 104 {
		t.Fatalf("Reveal() length = %d, want 104", len(raw))
	}
	if !strings.Contains(raw, "_") {
		t.Fatal("test credential did not exercise Base64URL underscores")
	}

	parsed, err := access.ParsePresentedKey(raw)
	if err != nil {
		t.Fatalf("ParsePresentedKey() error = %v", err)
	}
	if parsed.TenantHint().String() != tenant.String() || parsed.ID().String() != key.String() {
		t.Fatalf("parsed lookup hints = (%q, %q), want (%q, %q)", parsed.TenantHint(), parsed.ID(), tenant, key)
	}
	if parsed.Reveal() != raw {
		t.Fatal("parsed credential did not round trip")
	}

	formatted := fmt.Sprintf("%s|%v|%#v", presented, presented, presented)
	if strings.Contains(formatted, raw) || formatted != "[REDACTED]|[REDACTED]|[REDACTED]" {
		t.Fatalf("formatted credential = %q", formatted)
	}
	encoded, err := json.Marshal(presented)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), raw) || string(encoded) != `"[REDACTED]"` {
		t.Fatalf("JSON credential = %s", encoded)
	}
}

func TestParsePresentedKeyRejectsMalformedValues(t *testing.T) {
	t.Parallel()

	raw := testPresentedKey(t, 0xff).Reveal()
	tests := map[string]string{
		"empty":            "",
		"wrong length":     raw[:len(raw)-1],
		"wrong prefix":     "bad_v1_" + raw[7:],
		"tenant separator": raw[:33] + "-" + raw[34:],
		"key separator":    raw[:60] + "-" + raw[61:],
		"tenant ULID":      raw[:7] + "I" + raw[8:],
		"key ULID":         raw[:34] + "I" + raw[35:],
		"secret alphabet":  raw[:103] + "+",
		"padded secret":    raw[:103] + "=",
	}

	for name, value := range tests {
		name, value := name, value
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := access.ParsePresentedKey(value); err == nil {
				t.Errorf("ParsePresentedKey(%q) error = nil", name)
			}
		})
	}
}

func TestKeyGeneratorRejectsInvalidInputsAndEntropyFailure(t *testing.T) {
	t.Parallel()

	if _, err := access.NewKeyGenerator(nil); err == nil {
		t.Error("NewKeyGenerator(nil) error = nil")
	}
	var nilGenerator *access.KeyGenerator
	if _, err := nilGenerator.Generate(id.Tenant{}, id.APIKey{}); err == nil {
		t.Error("nil Generate() error = nil")
	}

	tenant, key := testKeyIDs(t)
	generator, err := access.NewKeyGenerator(failedKeyReader{})
	if err != nil {
		t.Fatalf("NewKeyGenerator() error = %v", err)
	}
	if _, err := generator.Generate(id.Tenant{}, key); err == nil {
		t.Error("Generate(zero tenant) error = nil")
	}
	if _, err := generator.Generate(tenant, id.APIKey{}); err == nil {
		t.Error("Generate(zero key) error = nil")
	}
	if _, err := generator.Generate(tenant, key); err == nil {
		t.Error("Generate(failed entropy) error = nil")
	}
}

func FuzzParsePresentedKey(f *testing.F) {
	f.Add("")
	f.Add("idq_v1_invalid")

	f.Fuzz(func(t *testing.T, value string) {
		parsed, err := access.ParsePresentedKey(value)
		if err == nil && parsed.Reveal() != value {
			t.Errorf("successful parse did not round trip")
		}
	})
}

func testPresentedKey(t *testing.T, entropy byte) access.PresentedKey {
	t.Helper()

	tenant, key := testKeyIDs(t)
	generator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{entropy}, 32)))
	if err != nil {
		t.Fatalf("NewKeyGenerator() error = %v", err)
	}
	presented, err := generator.Generate(tenant, key)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	return presented
}

func testKeyIDs(t *testing.T) (id.Tenant, id.APIKey) {
	t.Helper()

	generator, err := id.NewGenerator(keyClock{}, bytes.NewReader(bytes.Repeat([]byte{1}, 64)))
	if err != nil {
		t.Fatalf("id.NewGenerator() error = %v", err)
	}
	tenant, err := generator.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	key, err := generator.NewAPIKey()
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}

	return tenant, key
}

type failedKeyReader struct{}

func (failedKeyReader) Read([]byte) (int, error) {
	return 0, errors.New("entropy failed")
}
