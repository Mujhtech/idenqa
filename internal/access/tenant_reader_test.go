package access

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestTenantReaderAuthorisesAndUsesVerifiedScope(t *testing.T) {
	t.Parallel()

	generator, err := id.NewGenerator(tenantReaderClock{}, bytes.NewReader(bytes.Repeat([]byte{3}, 64)))
	if err != nil {
		t.Fatalf("NewGenerator() error = %v", err)
	}
	tenantID, err := generator.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	keyID, err := generator.NewAPIKey()
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	pattern, err := ParsePattern("tenant:read")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	grant, err := TenantRegistry().Resolve(pattern)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	now := time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
	want, err := tenant.Restore(tenantID, tenant.StateActive, 1, now, now, nil)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	repository := &tenantReadRepositoryStub{value: want}
	reader, err := NewTenantReader(repository)
	if err != nil {
		t.Fatalf("NewTenantReader() error = %v", err)
	}
	authority := Context{principal: APIKeyPrincipal{keyID: keyID}, scope: scope, grant: grant}

	got, err := reader.Current(context.Background(), authority)
	if err != nil {
		t.Fatalf("Current() error = %v", err)
	}
	if got.ID().String() != want.ID().String() {
		t.Fatalf("Current() tenant = %s, want %s", got.ID(), want.ID())
	}
	if repository.scope.ID().String() != tenantID.String() || repository.identifier.String() != tenantID.String() {
		t.Fatal("repository did not receive the verified tenant as both scope and target")
	}
}

type tenantReaderClock struct{}

func (tenantReaderClock) Now() time.Time {
	return time.Date(2026, time.August, 27, 12, 0, 0, 0, time.UTC)
}

func TestTenantReaderRejectsInsufficientScopeBeforePersistence(t *testing.T) {
	t.Parallel()

	repository := &tenantReadRepositoryStub{}
	reader, err := NewTenantReader(repository)
	if err != nil {
		t.Fatalf("NewTenantReader() error = %v", err)
	}

	_, err = reader.Current(context.Background(), Context{})
	if !errors.Is(err, ErrInsufficientScope) {
		t.Fatalf("Current() error = %v, want ErrInsufficientScope", err)
	}
	if repository.called {
		t.Fatal("repository called before application authorisation")
	}
}

type tenantReadRepositoryStub struct {
	value      tenant.Tenant
	err        error
	scope      tenant.Scope
	identifier id.Tenant
	called     bool
}

func (repository *tenantReadRepositoryStub) Find(
	_ context.Context,
	scope tenant.Scope,
	identifier id.Tenant,
) (tenant.Tenant, error) {
	repository.called = true
	repository.scope = scope
	repository.identifier = identifier

	return repository.value, repository.err
}
