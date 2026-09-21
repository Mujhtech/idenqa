package keycustody

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type memoryRepository struct {
	versions map[string][]Version
	material map[string]map[int64][]byte
	active   map[string]int64
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{versions: map[string][]Version{}, material: map[string]map[int64][]byte{}, active: map[string]int64{}}
}

func (repository *memoryRepository) Execute(_ context.Context, _ tenant.Scope, command Command) (Result, error) {
	state := repository.versions[command.Domain]
	switch command.Operation {
	case "create":
		if len(state) != 0 || command.ExpectedVersion != 0 {
			return Result{}, ErrConflict
		}
		repository.add(command.Domain, 1)
		repository.active[command.Domain] = 1

		return Result{Domain: command.Domain, ActiveVersion: 1, Generation: 1, Enabled: true}, nil
	case "rotate":
		generation := int64(len(state))
		if generation != command.ExpectedVersion {
			return Result{}, ErrConflict
		}
		version := generation + 1
		repository.add(command.Domain, version)
		repository.active[command.Domain] = version

		return Result{Domain: command.Domain, ActiveVersion: version, Generation: version, Enabled: true}, nil
	case "disable", "retire":
		for index := range state {
			if state[index].Version != command.ExpectedVersion {
				continue
			}
			if command.Operation == "disable" {
				if state[index].State != StateActive {
					return Result{}, ErrConflict
				}
				state[index].State = StateDisabled
				if repository.active[command.Domain] == command.ExpectedVersion {
					delete(repository.active, command.Domain)
				}
			} else {
				if state[index].State == StateActive {
					return Result{}, ErrReferenced
				}
				if state[index].State != StateDisabled {
					return Result{}, ErrConflict
				}
				state[index].State = StateRetired
			}
			return Result{Domain: command.Domain, ActiveVersion: repository.active[command.Domain], Generation: int64(len(state))}, nil
		}
		return Result{}, ErrNotFound
	default:
		return Result{}, ErrInvalid
	}
}

func (repository *memoryRepository) add(domain string, version int64) {
	repository.versions[domain] = append(repository.versions[domain], Version{
		ID: "hmk_test", Domain: domain, Version: version, State: StateActive, CreatedAt: time.Unix(0, 0).UTC(),
	})
	if repository.material[domain] == nil {
		repository.material[domain] = map[int64][]byte{}
	}
	material := make([]byte, KeySize)
	for index := range material {
		material[index] = byte(version) //nolint:gosec // version is bounded to small positive values in this fixture.
	}
	repository.material[domain][version] = material
}

func (repository *memoryRepository) Read(_ context.Context, _ tenant.Scope, domain string) (Result, error) {
	versions, ok := repository.versions[domain]
	if !ok {
		return Result{}, ErrNotFound
	}

	return Result{Domain: domain, ActiveVersion: repository.active[domain], Generation: int64(len(versions)), Versions: slices.Clone(versions)}, nil
}

func (repository *memoryRepository) Version(_ context.Context, _ tenant.Scope, domain string, version int64) (Version, error) {
	for _, candidate := range repository.versions[domain] {
		if candidate.Version == version {
			return candidate, nil
		}
	}

	return Version{}, ErrNotFound
}

func (repository *memoryRepository) Versions(_ context.Context, _ tenant.Scope, domain string) ([]Version, error) {
	versions, ok := repository.versions[domain]
	if !ok {
		return nil, ErrNotFound
	}

	return slices.Clone(versions), nil
}

func (repository *memoryRepository) Material(_ context.Context, _ tenant.Scope, domain string, version int64) ([]byte, error) {
	material, ok := repository.material[domain][version]
	if !ok {
		return nil, ErrNotFound
	}

	return slices.Clone(material), nil
}

func mustScope(t *testing.T) tenant.Scope {
	t.Helper()
	tenantID, err := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}

	return scope
}

func TestTokenDomainSeparation(t *testing.T) {
	t.Parallel()

	material := make([]byte, KeySize)
	for index := range material {
		material[index] = 0x2a
	}
	first, err := Token(material, "identity.lookup.v1", "ten_1", "region-1", "reference")
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	second, err := Token(material, "identity.identifier.v1", "ten_1", "region-1", "reference")
	if err != nil {
		t.Fatalf("Token() error = %v", err)
	}
	if first == second || !EqualToken(first, first) || EqualToken(first, second) {
		t.Fatalf("domain separation failed: %q %q", first, second)
	}
	if _, err := Token(material[:16], "identity.lookup.v1", "parts"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Token(short material) error = %v, want ErrInvalid", err)
	}
}

func TestProviderRotationKeepsOldIdentifiersVerifiable(t *testing.T) {
	t.Parallel()

	repository := newMemoryRepository()
	provider, err := NewProvider(repository)
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	scope := mustScope(t)
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("NewSystemGenerator() error = %v", err)
	}
	service, err := NewService(repository, generator, time.Now)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	actor := testAPIKey(t)
	if _, err := service.ExecuteDirect(context.Background(), scope, actor, "create-key", Command{
		Operation: "create", Domain: "identity.identifier.v1", Reason: "create identifier key domain",
	}); err != nil {
		t.Fatalf("create error = %v", err)
	}
	first, version, err := provider.Issue(context.Background(), scope, "identity.identifier.v1", "namespace", "issuer", "value")
	if err != nil || version != 1 {
		t.Fatalf("Issue() version=%d error=%v", version, err)
	}
	if _, err := service.ExecuteDirect(context.Background(), scope, actor, "rotate-key", Command{
		Operation: "rotate", Domain: "identity.identifier.v1", ExpectedVersion: 1, Reason: "rotate identifier keys",
	}); err != nil {
		t.Fatalf("rotate error = %v", err)
	}
	second, version, err := provider.Issue(context.Background(), scope, "identity.identifier.v1", "namespace", "issuer", "value")
	if err != nil || version != 2 || second == first {
		t.Fatalf("Issue(rotated) version=%d error=%v", version, err)
	}
	// Backward verification: identifiers created under version 1 still resolve.
	matched, err := provider.Verify(context.Background(), scope, "identity.identifier.v1", first, "namespace", "issuer", "value")
	if err != nil || matched != 1 {
		t.Fatalf("Verify(old) version=%d error=%v", matched, err)
	}
	matched, err = provider.Verify(context.Background(), scope, "identity.identifier.v1", second, "namespace", "issuer", "value")
	if err != nil || matched != 2 {
		t.Fatalf("Verify(new) version=%d error=%v", matched, err)
	}
	if _, err := provider.Verify(context.Background(), scope, "identity.lookup.v1", first, "namespace", "issuer", "value"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Verify(other domain) error = %v, want ErrNotFound", err)
	}

	// Disabling the active version fails new issuance closed but keeps older
	// active versions verifiable.
	if _, err := service.ExecuteDirect(context.Background(), scope, actor, "disable-key", Command{
		Operation: "disable", Domain: "identity.identifier.v1", ExpectedVersion: 2, Reason: "disable compromised key version",
	}); err != nil {
		t.Fatalf("disable error = %v", err)
	}
	if _, _, err := provider.Issue(context.Background(), scope, "identity.identifier.v1", "parts"); !errors.Is(err, ErrNoActiveKey) {
		t.Fatalf("Issue(disabled) error = %v, want ErrNoActiveKey", err)
	}
	matched, err = provider.Verify(context.Background(), scope, "identity.identifier.v1", first, "namespace", "issuer", "value")
	if err != nil || matched != 1 {
		t.Fatalf("Verify(older active) version=%d error=%v", matched, err)
	}
	if _, err := provider.Verify(context.Background(), scope, "identity.identifier.v1", second, "namespace", "issuer", "value"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Verify(disabled) error = %v, want ErrNotFound", err)
	}
}

func TestServiceRejectsInvalidCommands(t *testing.T) {
	t.Parallel()

	repository := newMemoryRepository()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("NewSystemGenerator() error = %v", err)
	}
	service, err := NewService(repository, generator, time.Now)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	scope := mustScope(t)
	actor := testAPIKey(t)
	tests := []struct {
		name    string
		command Command
		wantErr error
	}{
		{name: "invalid domain", command: Command{Operation: "create", Domain: "Invalid", Reason: "reason is long enough"}, wantErr: ErrInvalid},
		{name: "missing reason", command: Command{Operation: "create", Domain: "identity.lookup.v1"}, wantErr: ErrInvalid},
		{name: "unknown operation", command: Command{Operation: "destroy", Domain: "identity.lookup.v1", Reason: "reason is long enough"}, wantErr: ErrInvalid},
		{name: "rotate without version", command: Command{Operation: "rotate", Domain: "identity.lookup.v1", Reason: "reason is long enough"}, wantErr: ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := service.ExecuteDirect(context.Background(), scope, actor, "key", test.command); !errors.Is(err, test.wantErr) {
				t.Fatalf("ExecuteDirect() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func testAPIKey(t *testing.T) id.APIKey {
	t.Helper()
	key, err := id.ParseAPIKey("key_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("ParseAPIKey() error = %v", err)
	}

	return key
}
