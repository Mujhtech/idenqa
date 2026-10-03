//go:build integration

package integration_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
	"github.com/jackc/pgx/v5"
)

func TestTenantIsolationAndAdministrativeBypass(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("OpenMigrator() error = %v", err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}

	adminPool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()
	adminStore, err := tenantpostgres.New(adminPool)
	if err != nil {
		t.Fatalf("new admin store: %v", err)
	}
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("new id generator: %v", err)
	}
	admin, err := tenant.NewAdmin(adminStore, generator, clock.System{})
	if err != nil {
		t.Fatalf("new tenant admin: %v", err)
	}
	action := tenant.AdminAction{Actor: "integration-operator", Reason: "verify tenant isolation"}
	first, err := admin.Create(ctx, action)
	if err != nil {
		t.Fatalf("create first tenant: %v", err)
	}
	second, err := admin.Create(ctx, action)
	if err != nil {
		t.Fatalf("create second tenant: %v", err)
	}
	thirdID, err := generator.NewTenant()
	if err != nil {
		t.Fatalf("generate third tenant id: %v", err)
	}

	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtimePool, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer runtimePool.Close()
	runtimeStore, err := tenantpostgres.New(runtimePool)
	if err != nil {
		t.Fatalf("new runtime store: %v", err)
	}
	firstScope, err := tenant.NewScope(first.ID())
	if err != nil {
		t.Fatalf("new first scope: %v", err)
	}
	if _, err := runtimeStore.Find(ctx, firstScope, first.ID()); err != nil {
		t.Fatalf("find own tenant: %v", err)
	}
	if _, err := runtimeStore.Find(ctx, firstScope, second.ID()); !errors.Is(err, tenant.ErrNotFound) {
		t.Fatalf("cross-tenant probe error = %v, want ErrNotFound", err)
	}
	// Worker-style access uses the same explicit scope and RLS-enforced store;
	// no transport or process identity receives a database bypass.
	workerScope, err := tenant.NewScope(second.ID())
	if err != nil {
		t.Fatalf("new worker scope: %v", err)
	}
	if _, err := runtimeStore.Find(ctx, workerScope, second.ID()); err != nil {
		t.Fatalf("worker-style own-tenant read: %v", err)
	}
	if _, err := runtimeStore.Find(ctx, workerScope, first.ID()); !errors.Is(err, tenant.ErrNotFound) {
		t.Fatalf("worker-style cross-tenant probe error = %v, want ErrNotFound", err)
	}

	err = runtimePool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.tenants").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatalf("missing-scope tenant count = %d, want 0", count)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("missing-scope read: %v", err)
	}

	err = runtimePool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('idenqa.tenant_id', $1, true)", first.ID().String()); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.tenants (id, state, version, created_at, updated_at) VALUES ($1, 'active', 1, $2, $2)`, thirdID.String(), time.Now().UTC())
		return err
	})
	if err == nil {
		t.Fatal("cross-tenant write error = nil")
	}
	err = runtimePool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		var count int
		return tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.tenant_admin_audit").Scan(&count)
	})
	if err == nil {
		t.Fatal("runtime audit-table access error = nil")
	}

	if _, err := admin.Inspect(ctx, action, first.ID()); err != nil {
		t.Fatalf("admin inspect: %v", err)
	}
	if _, err := admin.Disable(ctx, action, first.ID(), first.Version()); err != nil {
		t.Fatalf("admin disable: %v", err)
	}
	connection, err := pgx.Connect(ctx, database.url)
	if err != nil {
		t.Fatalf("connect for audit assertion: %v", err)
	}
	defer func() {
		if err := connection.Close(context.Background()); err != nil {
			t.Errorf("close audit assertion connection: %v", err)
		}
	}()
	var auditCount int
	if err := connection.QueryRow(ctx, "SELECT count(*) FROM idenqa.tenant_admin_audit").Scan(&auditCount); err != nil {
		t.Fatalf("count admin audit: %v", err)
	}
	if auditCount != 4 {
		t.Fatalf("admin audit count = %d, want 4", auditCount)
	}
}

func TestManagedTenantProvisionRecordsAuditAndReplays(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatalf("OpenMigrator() error = %v", err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}

	adminPool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatalf("open admin pool: %v", err)
	}
	defer adminPool.Close()
	adminStore, err := tenantpostgres.New(adminPool)
	if err != nil {
		t.Fatalf("new admin store: %v", err)
	}
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("new id generator: %v", err)
	}
	admin, err := tenant.NewAdmin(adminStore, generator, clock.System{})
	if err != nil {
		t.Fatalf("new tenant admin: %v", err)
	}
	command := tenant.ProvisionCommand{
		ID:            "synthetic-tenant:dep_integration_01",
		RequestDigest: sha256.Sum256([]byte("synthetic deployment integration fixture")),
	}
	action := tenant.AdminAction{Actor: "idenqa-cloud", Reason: "managed deployment synthetic readiness"}
	provisioned, created, err := admin.Provision(ctx, action, command)
	if err != nil {
		t.Fatalf("provision managed tenant: %v", err)
	}
	if !created {
		t.Fatal("first managed tenant provision was not created")
	}
	replayed, created, err := admin.Provision(ctx, action, command)
	if err != nil {
		t.Fatalf("replay managed tenant provision: %v", err)
	}
	if created || replayed.ID() != provisioned.ID() {
		t.Fatalf("provision replay = %s, created %t; want %s, false", replayed.ID(), created, provisioned.ID())
	}

	var auditCount int
	if err := adminPool.Native().QueryRow(ctx, "SELECT count(*) FROM idenqa.tenant_admin_audit WHERE action='provision'").Scan(&auditCount); err != nil {
		t.Fatalf("count provision audit: %v", err)
	}
	if auditCount != 1 {
		t.Fatalf("provision audit count = %d, want 1", auditCount)
	}
}
