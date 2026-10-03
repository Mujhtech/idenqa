//go:build integration

package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/provider"
	usagepostgres "github.com/Mujhtech/idenqa/internal/usage/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Only the schema name is rewritten. Transactions, triggers, RLS and adapter
// SQL execute on PostgreSQL under a non-bypass role in a disposable schema.
type usageTransaction struct {
	pgx.Tx
	schema string
}

func (t usageTransaction) sql(query string) string {
	return strings.ReplaceAll(query, "idenqa.", t.schema+".")
}
func (t usageTransaction) Exec(ctx context.Context, query string, args ...any) (pgconn.CommandTag, error) {
	return t.Tx.Exec(ctx, t.sql(query), args...)
}
func (t usageTransaction) Query(ctx context.Context, query string, args ...any) (pgx.Rows, error) {
	return t.Tx.Query(ctx, t.sql(query), args...)
}
func (t usageTransaction) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	return t.Tx.QueryRow(ctx, t.sql(query), args...)
}

type usageRunner struct {
	pool         *pgxpool.Pool
	schema, role string
}

func (r usageRunner) WithinTransaction(ctx context.Context, _ pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+r.role); err != nil {
		return err
	}
	if err := work(ctx, usageTransaction{tx, r.schema}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type usageClock struct{ at time.Time }

func (c usageClock) Now() time.Time { return c.at }

func TestDispatchReceiptAtomicDeliveryAndReplay(t *testing.T) {
	url := os.Getenv("DATABASE_TEST_URL")
	if url == "" {
		t.Skip("DATABASE_TEST_URL required")
	}
	ctx := t.Context()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	suffix := strings.ReplaceAll(time.Now().UTC().Format("150405.000000000"), ".", "")
	schema, role := "usage_test_"+suffix, "usage_role_"+suffix
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE SCHEMA " + schema)
	exec("CREATE ROLE " + role + " NOLOGIN NOSUPERUSER NOBYPASSRLS")
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_, _ = pool.Exec(context.Background(), "DROP ROLE "+role)
	})
	exec("CREATE TABLE " + schema + ".tenants(id text PRIMARY KEY)")
	exec("CREATE TABLE " + schema + ".provider_requests(tenant_id text,attempt_id text,request_digest text,PRIMARY KEY(tenant_id,attempt_id))")
	exec("CREATE TABLE " + schema + ".provider_dispatches(tenant_id text,attempt_id text,request_digest text,claimed_at timestamptz,result_body jsonb,PRIMARY KEY(tenant_id,attempt_id))")
	exec("CREATE TABLE " + schema + ".provider_callback_receipts(tenant_id text,attempt_id text,result_digest text,progress_body jsonb,received_at timestamptz,provider_replay_id text)")
	up, err := os.ReadFile(filepath.Join("../../../db/migrations", "000088_regional_usage_receipts.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(strings.ReplaceAll(string(up), "idenqa.", schema+"."))
	exec("GRANT USAGE ON SCHEMA " + schema + " TO " + role)
	exec("GRANT SELECT,INSERT,UPDATE ON ALL TABLES IN SCHEMA " + schema + " TO " + role)
	m := dojah.Description()
	capability := m.Capabilities[2]
	at := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	request := providerv1.Request{Contract: m.Package.Contract, AttemptID: "atm_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", VerificationID: "ver_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Check: capability.Check, IdempotencyKey: "usage-fixture-idempotency", Adapter: m.Package, Capability: capability, Restrictions: m.Restrictions, Configuration: providerv1.ConfigurationReference{ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", SchemaDigest: m.Configuration.Digest, SecretReference: "secret://provider/fixture", CredentialVersion: "v1"}, Deadline: at.Add(time.Minute), Evidence: []providerv1.EvidenceGrantReference{{GrantID: "grt_01K4AR9V8FQ2G7ZXCPNM5T6JWH", RedemptionID: "rdm_01K4AR9V8FQ2G7ZXCPNM5T6JWH", EvidenceID: "evd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", Purpose: "idenqa.purpose.identity_verification", Variant: "document.front", ExpiresAt: at.Add(time.Minute)}}}
	digest, err := provider.RequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO "+schema+".tenants VALUES($1)", request.TenantID)
	exec("INSERT INTO "+schema+".provider_requests VALUES($1,$2,$3)", request.TenantID, request.AttemptID, digest)
	runner := usageRunner{pool, schema, role}
	store, err := NewRequestStore(runner, usageClock{at})
	if err != nil {
		t.Fatal(err)
	}
	// An unavailable outbox must roll back dispatch acceptance, never leave a
	// provider claim committed without its source receipt.
	exec("REVOKE INSERT ON " + schema + ".usage_receipts FROM " + role)
	if _, _, err := store.Claim(ctx, request); err == nil {
		t.Fatal("claim succeeded without receipt permission")
	}
	var count int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".provider_dispatches").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rollback count=%d error=%v", count, err)
	}
	exec("GRANT INSERT ON " + schema + ".usage_receipts TO " + role)
	claimed, _, err := store.Claim(ctx, request)
	if err != nil || !claimed {
		t.Fatalf("claim=%v error=%v", claimed, err)
	}
	claimed, _, err = store.Claim(ctx, request)
	if err != nil || claimed {
		t.Fatalf("replay claim=%v error=%v", claimed, err)
	}
	usage, err := usagepostgres.New(runner)
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := usage.Read(ctx, request.TenantID, 10)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("pending=%d error=%v", len(receipts), err)
	}
	if receipts[0].Quantity != 1 || !receipts[0].OccurredAt.Equal(at) || receipts[0].ProviderID != request.ProviderID {
		t.Fatal("receipt meaning changed")
	}
	encoded, err := json.Marshal(receipts[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, prohibited := range []string{"attemptId", "verificationId", "evidence", "secret", "result"} {
		if strings.Contains(string(encoded), prohibited) {
			t.Fatalf("receipt contains %s", prohibited)
		}
	}
	// Use a valid, different tenant ID to exercise scope rather than validation.
	wrongTenant := "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWK"
	other, err := usage.Read(ctx, wrongTenant, 10)
	if err != nil || len(other) != 0 {
		t.Fatalf("cross-scope read=%d error=%v", len(other), err)
	}
	err = runner.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		return tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.usage_receipts").Scan(&count)
	})
	if err != nil || count != 0 {
		t.Fatalf("unscoped RLS count=%d error=%v", count, err)
	}
	if err := usage.Acknowledge(ctx, request.TenantID, receipts[0].ID, strings.Repeat("0", 64), at); err == nil {
		t.Fatal("wrong digest accepted")
	}
	hash := sha256.Sum256(encoded)
	for range 2 {
		if err := usage.Acknowledge(ctx, request.TenantID, receipts[0].ID, hex.EncodeToString(hash[:]), at); err != nil {
			t.Fatal(err)
		}
	}
	receipts, err = usage.Read(ctx, request.TenantID, 10)
	if err != nil || len(receipts) != 0 {
		t.Fatal("acknowledged receipt remains pending")
	}
	err = runner.WithinTransaction(ctx, pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := tx.Exec(ctx, "SELECT set_config('idenqa.tenant_id',$1,true)", request.TenantID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "UPDATE idenqa.usage_receipts SET receipt='{}'::jsonb WHERE tenant_id=$1", request.TenantID)
		return err
	})
	if err == nil {
		t.Fatal("receipt mutation accepted")
	}
	down, err := os.ReadFile(filepath.Join("../../../db/migrations", "000088_regional_usage_receipts.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(strings.ReplaceAll(string(down), "idenqa.", schema+"."))
	exec(strings.ReplaceAll(string(up), "idenqa.", schema+"."))
}
