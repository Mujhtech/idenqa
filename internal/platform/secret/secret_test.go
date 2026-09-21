package secret

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseReference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		value    string
		provider string
		path     string
		version  string
		wantErr  bool
	}{
		{name: "provider path", value: "secret://aws/prod/idenqa/runner", provider: "aws", path: "/prod/idenqa/runner"},
		{name: "versioned", value: "secret://aws/prod/runner?version=AWSCURRENT", provider: "aws", path: "/prod/runner", version: "AWSCURRENT"},
		{name: "file path", value: "secret://file/run/secrets/credential", provider: "file", path: "/run/secrets/credential"},
		{name: "empty", value: "", wantErr: true},
		{name: "wrong scheme", value: "kms://aws/key", wantErr: true},
		{name: "missing path", value: "secret://aws", wantErr: true},
		{name: "trailing slash", value: "secret://aws/prod/", wantErr: true},
		{name: "traversal", value: "secret://file/run/../etc/passwd", wantErr: true},
		{name: "duplicate separators", value: "secret://aws//prod", wantErr: true},
		{name: "user info", value: "secret://user@aws/prod", wantErr: true},
		{name: "unknown query", value: "secret://aws/prod?stage=current", wantErr: true},
		{name: "duplicate version", value: "secret://aws/prod?version=a&version=b", wantErr: true},
		{name: "uppercase provider", value: "secret://AWS/prod", wantErr: true},
		{name: "padded", value: " secret://aws/prod", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reference, err := ParseReference(test.value)
			if test.wantErr {
				if !errors.Is(err, ErrInvalid) {
					t.Fatalf("ParseReference(%q) error = %v, want ErrInvalid", test.value, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseReference(%q) error = %v", test.value, err)
			}
			if reference.Provider() != test.provider || reference.Path() != test.path ||
				reference.Version() != test.version || reference.String() != test.value ||
				reference.IsZero() {
				t.Fatalf("ParseReference(%q) = %+v", test.value, reference)
			}
		})
	}
}

func TestValueBoundsTextAndJSON(t *testing.T) {
	t.Parallel()

	reference, err := ParseReference("secret://aws/prod/runner")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	value, err := NewValue(reference, "v1", []byte("idq_wrk_v1_secret"))
	if err != nil {
		t.Fatalf("NewValue() error = %v", err)
	}
	text, err := value.Text()
	if err != nil || text != "idq_wrk_v1_secret" {
		t.Fatalf("Text() = %q, %v", text, err)
	}
	if got := string(value.Data()); got != "idq_wrk_v1_secret" {
		t.Fatalf("Data() = %q", got)
	}

	if _, err := NewValue(reference, "", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewValue(empty) error = %v, want ErrInvalid", err)
	}
	oversized := strings.Repeat("x", MaxPayloadBytes+1)
	if _, err := NewValue(reference, "", []byte(oversized)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("NewValue(oversized) error = %v, want ErrInvalid", err)
	}

	multiline, err := NewValue(reference, "", []byte("first\nsecond"))
	if err != nil {
		t.Fatalf("NewValue(multiline) error = %v", err)
	}
	if _, err := multiline.Text(); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Text(multiline) error = %v, want ErrInvalid", err)
	}

	type configuration struct {
		Endpoint string `json:"endpoint"`
		Token    string `json:"token"`
	}
	document, err := NewValue(reference, "", []byte(`{"endpoint":"https://example.test","token":"abc"}`))
	if err != nil {
		t.Fatalf("NewValue(json) error = %v", err)
	}
	var decoded configuration
	if err := document.JSON(&decoded); err != nil || decoded.Endpoint != "https://example.test" {
		t.Fatalf("JSON() = %+v, %v", decoded, err)
	}
	unknown, err := NewValue(reference, "", []byte(`{"endpoint":"x","unknown":1}`))
	if err != nil {
		t.Fatalf("NewValue(unknown) error = %v", err)
	}
	if err := unknown.JSON(&decoded); !errors.Is(err, ErrInvalid) {
		t.Fatalf("JSON(unknown field) error = %v, want ErrInvalid", err)
	}
}

type countingResolver struct {
	calls int
	value []byte
	err   error
}

func (resolver *countingResolver) Resolve(_ context.Context, reference Reference) (Value, error) {
	resolver.calls++
	if resolver.err != nil {
		return Value{}, resolver.err
	}
	return NewValue(reference, "v1", resolver.value)
}

func TestCacheExpiresAndInvalidates(t *testing.T) {
	t.Parallel()

	reference, err := ParseReference("secret://aws/prod/runner")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	source := &countingResolver{value: []byte("first")}
	now := time.Unix(1_700_000_000, 0).UTC()
	cache, err := NewCache(source, 30*time.Second, func() time.Time { return now })
	if err != nil {
		t.Fatalf("NewCache() error = %v", err)
	}

	first, err := cache.Resolve(context.Background(), reference)
	if err != nil || string(first.Data()) != "first" || source.calls != 1 {
		t.Fatalf("Resolve() calls=%d error=%v", source.calls, err)
	}
	if _, err := cache.Resolve(context.Background(), reference); err != nil || source.calls != 1 {
		t.Fatalf("cached Resolve() calls=%d error=%v", source.calls, err)
	}

	source.value = []byte("second")
	now = now.Add(31 * time.Second)
	second, err := cache.Resolve(context.Background(), reference)
	if err != nil || string(second.Data()) != "second" || source.calls != 2 {
		t.Fatalf("expired Resolve() calls=%d error=%v", source.calls, err)
	}

	cache.Invalidate(reference)
	if _, err := cache.Resolve(context.Background(), reference); err != nil || source.calls != 3 {
		t.Fatalf("invalidated Resolve() calls=%d error=%v", source.calls, err)
	}

	source.err = ErrNotFound
	cache.InvalidateAll()
	if _, err := cache.Resolve(context.Background(), reference); !errors.Is(err, ErrNotFound) || source.calls != 4 {
		t.Fatalf("failed Resolve() calls=%d error=%v", source.calls, err)
	}
	// The failed resolution must not leave a usable entry behind.
	if _, err := cache.Resolve(context.Background(), reference); !errors.Is(err, ErrNotFound) || source.calls != 5 {
		t.Fatalf("failed Resolve() replay calls=%d error=%v", source.calls, err)
	}
}

type rotatingResolver struct {
	value  []byte
	failAt int
	calls  int
}

func (resolver *rotatingResolver) Resolve(_ context.Context, reference Reference) (Value, error) {
	resolver.calls++
	if resolver.failAt > 0 && resolver.calls == resolver.failAt {
		return Value{}, ErrNotFound
	}
	return NewValue(reference, "v1", resolver.value)
}

func TestReloaderOverlapsAndFailsClosed(t *testing.T) {
	t.Parallel()

	reference, err := ParseReference("secret://aws/prod/credential")
	if err != nil {
		t.Fatalf("ParseReference() error = %v", err)
	}
	source := &rotatingResolver{value: []byte("first")}
	var applied [][2]string
	reloader, err := NewReloader(source, []Reference{reference}, time.Minute, func(_ context.Context, previous, next Snapshot) error {
		old := ""
		if value, ok := previous.Value(reference); ok {
			old = string(value.Data())
		}
		value, ok := next.Value(reference)
		if !ok {
			return ErrInvalid
		}
		applied = append(applied, [2]string{old, string(value.Data())})
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewReloader() error = %v", err)
	}

	if err := reloader.Prime(context.Background()); err != nil || len(applied) != 1 || applied[0] != [2]string{"", "first"} {
		t.Fatalf("Prime() applied=%v error=%v", applied, err)
	}
	previous, err := reloader.Reload(context.Background(), Snapshot{})
	if err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	source.value = []byte("second")
	next, err := reloader.Reload(context.Background(), previous)
	if err != nil || len(applied) != 3 || applied[2] != [2]string{"first", "second"} {
		t.Fatalf("rotated Reload() applied=%v error=%v", applied, err)
	}
	value, ok := next.Value(reference)
	if !ok || string(value.Data()) != "second" {
		t.Fatalf("rotated snapshot = %v", value)
	}

	source.failAt = source.calls + 1
	kept, err := reloader.Reload(context.Background(), next)
	if !errors.Is(err, ErrNotFound) || len(applied) != 3 {
		t.Fatalf("failed Reload() error=%v applied=%v", err, applied)
	}
	if value, ok := kept.Value(reference); !ok || string(value.Data()) != "second" {
		t.Fatalf("failed Reload() did not keep the previous snapshot")
	}
}

func TestReloaderValidation(t *testing.T) {
	t.Parallel()

	source := &countingResolver{value: []byte("value")}
	reference, _ := ParseReference("secret://aws/prod/runner")
	tests := []struct {
		name       string
		resolver   Resolver
		references []Reference
		interval   time.Duration
		apply      func(context.Context, Snapshot, Snapshot) error
		wantErr    bool
	}{
		{name: "valid", resolver: source, references: []Reference{reference}, interval: time.Second, apply: func(context.Context, Snapshot, Snapshot) error { return nil }},
		{name: "nil resolver", references: []Reference{reference}, interval: time.Second, apply: func(context.Context, Snapshot, Snapshot) error { return nil }, wantErr: true},
		{name: "no references", resolver: source, interval: time.Second, apply: func(context.Context, Snapshot, Snapshot) error { return nil }, wantErr: true},
		{name: "short interval", resolver: source, references: []Reference{reference}, interval: time.Millisecond, apply: func(context.Context, Snapshot, Snapshot) error { return nil }, wantErr: true},
		{name: "nil apply", resolver: source, references: []Reference{reference}, interval: time.Second, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := NewReloader(test.resolver, test.references, test.interval, test.apply, nil)
			if (err != nil) != test.wantErr {
				t.Fatalf("NewReloader() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}
