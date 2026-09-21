//go:build integration

package integration_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	keycustodypostgres "github.com/Mujhtech/idenqa/internal/keycustody/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/support"
	supportpostgres "github.com/Mujhtech/idenqa/internal/support/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type referenceOracle struct{ referenced bool }

func (oracle referenceOracle) ReferencedKeyVersion(context.Context, tenant.Scope, string, int64) (bool, error) {
	return oracle.referenced, nil
}

func TestKeyCustodyRotationRetirementRLSAndAudit(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenantID, _ := generator.NewTenant()
	otherID, _ := generator.NewTenant()
	if err := admin.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.tenants (id,state,version,created_at,updated_at) VALUES ($1,'active',1,$3,$3),($2,'active',1,$3,$3)`, tenantID.String(), otherID.String(), now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	otherScope, err := tenant.NewScope(otherID)
	if err != nil {
		t.Fatal(err)
	}
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	keys, err := accesspostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	actorKey := newIntegrationKey(t, generator, tenantID, now, id.APIKey{})
	if err := keys.Create(ctx, scope, actorKey); err != nil {
		t.Fatal(err)
	}
	keyring, err := localkms.Create(filepath.Join(t.TempDir(), "keyring.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = keyring.Close() }()
	generation := byte(0)
	store, err := keycustodypostgres.New(runtime, keyring, keyring, referenceOracle{referenced: true}, func() (string, []byte, error) {
		identifier, err := generator.New(keycustody.KeyIDPrefix)
		if err != nil {
			return "", nil, err
		}
		generation++
		material := make([]byte, keycustody.KeySize)
		for index := range material {
			material[index] = generation
		}
		return identifier.String(), material, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := keycustody.NewService(store, generator, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	actor := actorKey.ID()
	run := func(operation string, expected int64) keycustody.Result {
		t.Helper()
		event, err := generator.NewEvent()
		if err != nil {
			t.Fatal(err)
		}
		result, err := service.ExecuteDirect(ctx, scope, actor, "itest-"+operation+"-"+event.String(), keycustody.Command{
			Operation: operation, Domain: "identity.identifier.v1", ExpectedVersion: expected,
			Reason: "integration key lifecycle",
		})
		if err != nil {
			t.Fatalf("%s error = %v", operation, err)
		}
		return result
	}
	if result := run("create", 0); result.ActiveVersion != 1 || result.Generation != 1 {
		t.Fatalf("create result = %+v", result)
	}
	provider, err := keycustody.NewProvider(store)
	if err != nil {
		t.Fatal(err)
	}
	first, version, err := provider.Issue(ctx, scope, "identity.identifier.v1", "ns", "issuer", "value")
	if err != nil || version != 1 {
		t.Fatalf("Issue() version=%d error=%v", version, err)
	}
	run("rotate", 1)
	second, version, err := provider.Issue(ctx, scope, "identity.identifier.v1", "ns", "issuer", "value")
	if err != nil || version != 2 || second == first {
		t.Fatalf("Issue(rotated) version=%d error=%v", version, err)
	}
	if matched, err := provider.Verify(ctx, scope, "identity.identifier.v1", first, "ns", "issuer", "value"); err != nil || matched != 1 {
		t.Fatalf("Verify(old) version=%d error=%v", matched, err)
	}
	// Cross-tenant reads fail closed under forced RLS.
	if _, err := store.Read(ctx, otherScope, "identity.identifier.v1"); !errors.Is(err, keycustody.ErrNotFound) {
		t.Fatalf("cross-tenant read error = %v", err)
	}
	// A referenced version cannot be retired, and the active version cannot
	// be retired at all.
	run("disable", 1)
	if _, err := service.ExecuteDirect(ctx, scope, actor, "itest-retire-referenced", keycustody.Command{
		Operation: "retire", Domain: "identity.identifier.v1", ExpectedVersion: 1, Reason: "integration retire check",
	}); !errors.Is(err, keycustody.ErrReferenced) {
		t.Fatalf("retire(referenced) error = %v, want ErrReferenced", err)
	}
	if _, err := service.ExecuteDirect(ctx, scope, actor, "itest-retire-active", keycustody.Command{
		Operation: "retire", Domain: "identity.identifier.v1", ExpectedVersion: 2, Reason: "integration retire check",
	}); !errors.Is(err, keycustody.ErrReferenced) {
		t.Fatalf("retire(active) error = %v, want ErrReferenced", err)
	}
	// The immutable trigger refuses to rewrite wrapped material.
	if _, err := admin.Native().Exec(ctx, `UPDATE idenqa.hmac_keys SET wrapped_key='{"provider":"x"}' WHERE tenant_id=$1`, tenantID.String()); err == nil {
		t.Fatal("immutable hmac key material was rewritten")
	}
	// The lifecycle is chained into the tenant audit history.
	var events int
	if err := admin.Native().QueryRow(ctx, `SELECT count(*) FROM idenqa.audit_records WHERE tenant_id=$1 AND event_type LIKE 'keycustody.%'`, tenantID.String()).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events < 3 {
		t.Fatalf("audit events = %d, want at least three lifecycle events", events)
	}
}

func TestSupportGrantBreakGlassRLSAndAudit(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenantID, _ := generator.NewTenant()
	otherID, _ := generator.NewTenant()
	if err := admin.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.tenants (id,state,version,created_at,updated_at) VALUES ($1,'active',1,$3,$3),($2,'active',1,$3,$3)`, tenantID.String(), otherID.String(), now)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	scope, _ := tenant.NewScope(tenantID)
	otherScope, _ := tenant.NewScope(otherID)
	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	keys, err := accesspostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	requesterKey := newIntegrationKey(t, generator, tenantID, now, id.APIKey{})
	if err := keys.Create(ctx, scope, requesterKey); err != nil {
		t.Fatal(err)
	}
	approverKey := newIntegrationKey(t, generator, tenantID, now, id.APIKey{})
	if err := keys.Create(ctx, scope, approverKey); err != nil {
		t.Fatal(err)
	}
	store, err := supportpostgres.New(runtime, generator)
	if err != nil {
		t.Fatal(err)
	}
	service, err := support.NewService(store, access.TenantRegistry(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	grant, err := service.ExecuteDirect(ctx, scope, requesterKey.ID(), "itest-grant", support.Command{
		Operation: "grant", Grantee: "support_engineer", Patterns: []string{"subjects:read"},
		Duration: time.Hour, Reason: "integration delegated grant",
	})
	if err != nil || grant.Grant == nil || len(grant.Grant.Permissions) != 1 {
		t.Fatalf("grant = %+v, %v", grant, err)
	}
	revoked, err := service.ExecuteDirect(ctx, scope, requesterKey.ID(), "itest-grant-revoke", support.Command{
		Operation: "grant_revoke", Identifier: grant.Grant.ID, ExpectedVersion: 1, Reason: "integration revoke",
	})
	if err != nil || revoked.Grant == nil || revoked.Grant.State != support.StateRevoked {
		t.Fatalf("revoke = %+v, %v", revoked, err)
	}
	request, err := service.ExecuteDirect(ctx, scope, requesterKey.ID(), "itest-bg-request", support.Command{
		Operation: "break_glass_request", Permissions: []string{"identity:reveal"},
		Duration: time.Hour, Reason: "integration incident triage",
	})
	if err != nil || request.Emergency == nil || request.Emergency.State != support.StateRequested {
		t.Fatalf("request = %+v, %v", request, err)
	}
	// Self-approval is refused by the persistence layer.
	if _, err := service.ExecuteDirect(ctx, scope, requesterKey.ID(), "itest-bg-self", support.Command{
		Operation: "break_glass_approve", Identifier: request.Emergency.ID, ExpectedVersion: 1, Reason: "integration self approval",
	}); !errors.Is(err, support.ErrForbidden) {
		t.Fatalf("self approval error = %v, want ErrForbidden", err)
	}
	approved, err := service.ExecuteDirect(ctx, scope, approverKey.ID(), "itest-bg-approve", support.Command{
		Operation: "break_glass_approve", Identifier: request.Emergency.ID, ExpectedVersion: 1, Reason: "integration approval",
	})
	if err != nil || approved.Emergency == nil || approved.Emergency.State != support.StateApproved {
		t.Fatalf("approve = %+v, %v", approved, err)
	}
	used, err := service.ExecuteDirect(ctx, scope, approverKey.ID(), "itest-bg-use", support.Command{
		Operation: "break_glass_use", Identifier: request.Emergency.ID, ExpectedVersion: 2,
		Permissions: []string{"identity:reveal"}, Target: "subject_fixture", Reason: "break-glass use recorded",
	})
	if err != nil || len(used.Uses) != 1 || used.Uses[0].Permission != "identity:reveal" {
		t.Fatalf("use = %+v, %v", used, err)
	}
	// A use outside the approved permission set is refused.
	if _, err := service.ExecuteDirect(ctx, scope, approverKey.ID(), "itest-bg-use-other", support.Command{
		Operation: "break_glass_use", Identifier: request.Emergency.ID, ExpectedVersion: 2,
		Permissions: []string{"kms:read"}, Target: "subject_fixture", Reason: "break-glass use recorded",
	}); !errors.Is(err, support.ErrForbidden) {
		t.Fatalf("out-of-scope use error = %v, want ErrForbidden", err)
	}
	// Cross-tenant reads fail closed, and the append-only ledger rejects edits.
	if _, err := store.Read(ctx, otherScope, "grant", grant.Grant.ID); !errors.Is(err, support.ErrNotFound) {
		t.Fatalf("cross-tenant support read error = %v", err)
	}
	if _, err := admin.Native().Exec(ctx, `UPDATE idenqa.support_grants SET reason='rewritten' WHERE tenant_id=$1`, tenantID.String()); err == nil {
		t.Fatal("support grant identity was rewritten")
	}
	if _, err := admin.Native().Exec(ctx, `DELETE FROM idenqa.break_glass_uses WHERE tenant_id=$1`, tenantID.String()); err == nil {
		t.Fatal("break-glass use ledger was deleted")
	}
	var events int
	if err := admin.Native().QueryRow(ctx, `SELECT count(*) FROM idenqa.audit_records WHERE tenant_id=$1 AND event_type LIKE 'support.%'`, tenantID.String()).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events < 4 {
		t.Fatalf("support audit events = %d, want at least four", events)
	}
}
