package access_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestAdminActionValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		action access.AdminAction
		valid  bool
	}{
		{name: "valid", action: access.AdminAction{Actor: "operator", Reason: "rotate deployment key"}, valid: true},
		{name: "missing actor", action: access.AdminAction{Reason: "rotate deployment key"}},
		{name: "actor whitespace", action: access.AdminAction{Actor: " operator ", Reason: "rotate deployment key"}},
		{name: "reason control", action: access.AdminAction{Actor: "operator", Reason: "rotate\nkey"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := test.action.Validate(); (err == nil) != test.valid {
				t.Fatalf("Validate() error = %v, valid = %t", err, test.valid)
			}
		})
	}
}

func TestAdministrativeIssuerUsesAuditedPersistence(t *testing.T) {
	t.Parallel()

	repository := &adminRepositoryStub{}
	issuer, _ := newTestIssuer(t, repository, false, 24*time.Hour, time.Hour)
	administrator, err := access.NewAdministrativeIssuer(issuer, repository)
	if err != nil {
		t.Fatalf("NewAdministrativeIssuer() error = %v", err)
	}
	tenantID, _ := testKeyIDs(t)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	pattern, err := access.ParsePattern("tenant:read")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	issued, err := administrator.Issue(t.Context(), access.AdminAction{
		Actor: "operator", Reason: "create backend credential",
	}, scope, access.IssueInput{
		Label: "backend", Patterns: []access.Pattern{pattern},
		Expiry: access.ExpiringAt(keyClock{}.Now().Add(time.Hour)),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if repository.regularCreates != 0 || repository.adminCreates != 1 {
		t.Fatalf("regular/admin creates = %d/%d, want 0/1", repository.regularCreates, repository.adminCreates)
	}
	if repository.action.OccurredAt().IsZero() || issued.Credential().Reveal() == "" {
		t.Fatal("audited issuance omitted occurrence time or display-once credential")
	}
}

func TestAdministratorListsAndRevokesWithAudit(t *testing.T) {
	t.Parallel()

	now := keyClock{}.Now()
	repository := &adminRepositoryStub{}
	record := testKeyRecord(t, now.Add(-time.Hour))
	key, err := access.RestoreKey(record)
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}
	repository.found = key
	repository.listed = []access.Key{key}
	administrator, err := access.NewAdministrator(repository, keyClock{})
	if err != nil {
		t.Fatalf("NewAdministrator() error = %v", err)
	}
	scope, err := tenant.NewScope(key.TenantID())
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	action := access.AdminAction{Actor: "operator", Reason: "credential maintenance"}
	listed, err := administrator.List(t.Context(), action, scope)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 1 || repository.adminLists != 1 || repository.action.OccurredAt().IsZero() {
		t.Fatal("List() did not use atomically audited persistence")
	}
	revoked, err := administrator.Revoke(t.Context(), action, scope, key.ID(), key.Version())
	if err != nil {
		t.Fatalf("Revoke() error = %v", err)
	}
	if revoked.StateAt(now) != access.KeyStateRevoked || repository.adminSaves != 1 || repository.regularSaves != 0 {
		t.Fatal("Revoke() did not use the aggregate and audited lifecycle persistence")
	}
}

func TestAdministratorRejectsStaleRevocationBeforeWrite(t *testing.T) {
	t.Parallel()

	now := keyClock{}.Now()
	repository := &adminRepositoryStub{}
	key, err := access.RestoreKey(testKeyRecord(t, now.Add(-time.Hour)))
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}
	repository.found = key
	administrator, err := access.NewAdministrator(repository, keyClock{})
	if err != nil {
		t.Fatalf("NewAdministrator() error = %v", err)
	}
	scope, err := tenant.NewScope(key.TenantID())
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}

	_, err = administrator.Revoke(t.Context(), access.AdminAction{
		Actor: "operator", Reason: "revoke stale key",
	}, scope, key.ID(), key.Version()+1)
	if !errors.Is(err, access.ErrKeyConflict) {
		t.Fatalf("Revoke() error = %v, want ErrKeyConflict", err)
	}
	if repository.adminSaves != 0 {
		t.Fatal("stale revocation reached persistence")
	}
}

type adminRepositoryStub struct {
	found          access.Key
	listed         []access.Key
	action         access.AdminAction
	regularCreates int
	regularSaves   int
	adminCreates   int
	adminLists     int
	adminSaves     int
	adminRotations int
}

func (repository *adminRepositoryStub) Create(context.Context, tenant.Scope, access.Key) error {
	repository.regularCreates++

	return nil
}

func (repository *adminRepositoryStub) Find(context.Context, tenant.Scope, id.APIKey) (access.Key, error) {
	return repository.found, nil
}

func (repository *adminRepositoryStub) FindAdministrative(
	context.Context,
	tenant.Scope,
	id.APIKey,
) (access.Key, error) {
	return repository.found, nil
}

func (repository *adminRepositoryStub) SaveLifecycle(context.Context, tenant.Scope, access.Key, int64) error {
	repository.regularSaves++

	return nil
}

func (*adminRepositoryStub) Rotate(context.Context, tenant.Scope, access.Key, int64, access.Key) error {
	return nil
}

func (repository *adminRepositoryStub) CreateAdministrative(
	_ context.Context,
	action access.AdminAction,
	_ tenant.Scope,
	_ access.Key,
) error {
	repository.action = action
	repository.adminCreates++

	return nil
}

func (repository *adminRepositoryStub) ListAdministrative(
	_ context.Context,
	action access.AdminAction,
	_ tenant.Scope,
) ([]access.Key, error) {
	repository.action = action
	repository.adminLists++

	return repository.listed, nil
}

func (repository *adminRepositoryStub) SaveLifecycleAdministrative(
	_ context.Context,
	action access.AdminAction,
	_ tenant.Scope,
	_ access.Key,
	_ int64,
) error {
	repository.action = action
	repository.adminSaves++

	return nil
}

func (repository *adminRepositoryStub) RotateAdministrative(
	_ context.Context,
	action access.AdminAction,
	_ tenant.Scope,
	_ access.Key,
	_ int64,
	_ access.Key,
) error {
	repository.action = action
	repository.adminRotations++

	return nil
}
