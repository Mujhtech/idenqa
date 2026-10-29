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
	usagev1 "github.com/Mujhtech/idenqa/contracts/usage/v1"
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
	if r.role != "" {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+r.role); err != nil {
			return err
		}
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
	coverage, err := os.ReadFile(filepath.Join("../../../db/migrations", "000092_regional_usage_coverage.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	exec(strings.ReplaceAll(string(coverage), "idenqa.", schema+"."))
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
	sourceStart := time.Now().UTC()
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
	usage, err = usage.ForSubscription(strings.Repeat("a", 64), strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	receipts, err := usage.Read(ctx, request.TenantID, 10)
	if err != nil || len(receipts) != 1 {
		t.Fatalf("pending=%d error=%v", len(receipts), err)
	}
	if receipts[0].Quantity != 1 || receipts[0].OccurredAt.Before(sourceStart) || receipts[0].OccurredAt.After(time.Now().UTC()) || receipts[0].ProviderID != request.ProviderID {
		t.Fatal("receipt meaning changed")
	}
	if through, err := usage.Coverage(ctx, request.TenantID); err != nil || !through.Through.IsZero() {
		t.Fatalf("pending source claimed complete coverage: %v %v", through, err)
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
	exec("INSERT INTO "+schema+".tenants VALUES($1)", wrongTenant)
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
	if through, err := usage.Coverage(ctx, request.TenantID); err != nil || through.Through.Before(sourceStart) {
		t.Fatalf("acknowledged source lacks coverage: %v %v", through, err)
	}
	for _, change := range []struct{ consumer, routing string }{{strings.Repeat("c", 64), strings.Repeat("b", 64)}, {strings.Repeat("a", 64), strings.Repeat("c", 64)}} {
		other, err := usage.ForSubscription(change.consumer, change.routing)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = other.Coverage(ctx, request.TenantID); err == nil {
			t.Fatal("changed consumer/routing claimed prior acknowledgements")
		}
	}
	legacy, err := usagepostgres.New(runner)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = legacy.Read(ctx, request.TenantID, 10); err == nil {
		t.Fatal("legacy consumer bypassed owned subscription")
	}
	result := providerv1.Result{Contract: request.Contract, AttemptID: request.AttemptID, Outcome: providerv1.ResultOutcomeCompleted, Signals: []providerv1.Signal{{Name: "idenqa.signal.document_quality", Outcome: providerv1.SignalOutcomeSatisfied}}, CompletedAt: request.Deadline.Add(-time.Second)}
	exec("REVOKE INSERT ON " + schema + ".usage_receipts FROM " + role)
	if err := store.Complete(ctx, request, result); err == nil {
		t.Fatal("terminal effect committed without usage receipt")
	}
	var terminal bool
	if err := pool.QueryRow(ctx, "SELECT result_body IS NOT NULL FROM "+schema+".provider_dispatches WHERE tenant_id=$1 AND attempt_id=$2", request.TenantID, request.AttemptID).Scan(&terminal); err != nil || terminal {
		t.Fatalf("terminal rollback: %v %v", terminal, err)
	}
	exec("GRANT INSERT ON " + schema + ".usage_receipts TO " + role)
	if err := store.Complete(ctx, request, result); err != nil {
		t.Fatal(err)
	}
	if _, cached, err := store.Claim(ctx, request); err != nil || cached == nil {
		t.Fatalf("terminal replay: %v %v", cached, err)
	}
	terminalReceipts, err := usage.Read(ctx, request.TenantID, 10)
	if err != nil || len(terminalReceipts) != 1 || terminalReceipts[0].Event != "provider_terminal" {
		t.Fatalf("terminal receipts: %+v %v", terminalReceipts, err)
	}
	privileged, _ := usagepostgres.New(usageRunner{pool, schema, ""})
	handoff := usagepostgres.Handoff{Tenant: request.TenantID, ExpectedEpoch: "core_operational_v2", ExpectedConsumer: strings.Repeat("a", 64), ExpectedRouting: strings.Repeat("b", 64), Epoch: "replacement_v3", Consumer: strings.Repeat("c", 64), Routing: strings.Repeat("d", 64), Actor: "operator", ReasonReference: "change_1"}
	if _, err = usage.Handoff(ctx, handoff); err == nil {
		t.Fatal("runtime performed privileged handoff")
	}
	if _, err = privileged.Handoff(ctx, handoff); err == nil {
		t.Fatal("handoff discarded pending source receipt")
	}
	rotation := handoff
	rotation.CarryPending = true
	if _, err = privileged.Handoff(ctx, rotation); err == nil {
		t.Fatal("pending transfer changed signing consumer")
	}
	expansion := handoff
	expansion.Consumer = expansion.ExpectedConsumer
	expansion.Routing = strings.Repeat("f", 64)
	expansion.Epoch = "expanded_v3"
	expansion.CarryPending = true
	// A handoff audit failure must roll back both ownership and the new floor.
	exec("CREATE FUNCTION " + schema + ".reject_usage_handoff() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture audit unavailable'; END $$")
	exec("CREATE TRIGGER reject_usage_handoff BEFORE INSERT ON " + schema + ".usage_source_handoffs FOR EACH ROW EXECUTE FUNCTION " + schema + ".reject_usage_handoff()")
	if _, err = privileged.Handoff(ctx, expansion); err == nil {
		t.Fatal("handoff committed without audit")
	}
	if pending, e := usage.Read(ctx, request.TenantID, 10); e != nil || len(pending) != 1 || pending[0].ID != terminalReceipts[0].ID {
		t.Fatalf("audit rollback lost ownership/receipt: %+v %v", pending, e)
	}
	exec("DROP TRIGGER reject_usage_handoff ON " + schema + ".usage_source_handoffs")
	exec("DROP FUNCTION " + schema + ".reject_usage_handoff()")
	cutover, err := privileged.Handoff(ctx, expansion)
	if err != nil || !cutover.Previous.Through.IsZero() {
		t.Fatalf("pending transfer claimed old completeness: %+v %v", cutover, err)
	}
	if _, err = usage.Read(ctx, request.TenantID, 10); err == nil {
		t.Fatal("old routing survived transfer")
	}
	usage, err = usage.ForSubscription(expansion.Consumer, expansion.Routing)
	if err != nil {
		t.Fatal(err)
	}
	if pending, e := usage.Read(ctx, request.TenantID, 10); e != nil || len(pending) != 1 || pending[0].ID != terminalReceipts[0].ID {
		t.Fatalf("transfer lost pending meaning: %+v %v", pending, e)
	}
	handoff.ExpectedEpoch = expansion.Epoch
	handoff.ExpectedRouting = expansion.Routing
	ack := func(values []usagev1.Receipt) {
		t.Helper()
		for _, receipt := range values {
			raw, e := json.Marshal(receipt)
			if e != nil {
				t.Fatal(e)
			}
			digest := sha256.Sum256(raw)
			if e = usage.Acknowledge(ctx, request.TenantID, receipt.ID, hex.EncodeToString(digest[:]), time.Now()); e != nil {
				t.Fatal(e)
			}
		}
	}
	ack(terminalReceipts)
	// A writer holds the same source lock until its state and receipt commit.
	// The coverage reader must wait, then observe the newly pending receipt.
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wtx := usageTransaction{writer, schema}
	if _, err = wtx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('idenqa.usage:'||$1,0))`, request.TenantID); err != nil {
		t.Fatal(err)
	}
	type coverageResult struct {
		value usagev1.Coverage
		err   error
	}
	completed := make(chan coverageResult, 1)
	go func() { value, e := usage.Coverage(ctx, request.TenantID); completed <- coverageResult{value, e} }()
	waitCtx, cancelWait := context.WithTimeout(ctx, 2*time.Second)
	ticker := time.NewTicker(5 * time.Millisecond)
	for {
		var blocked bool
		if e := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_locks WHERE locktype='advisory' AND NOT granted)`).Scan(&blocked); e != nil {
			t.Fatal(e)
		}
		if blocked {
			break
		}
		select {
		case result := <-completed:
			t.Fatalf("coverage crossed in-flight barrier: %+v", result)
		case <-waitCtx.Done():
			t.Fatal("coverage did not wait on writer")
		case <-ticker.C:
		}
	}
	ticker.Stop()
	cancelWait()
	if err = usagepostgres.AppendWithin(ctx, wtx, usagev1.Receipt{Event: "model_dispatch", CoreTenantID: request.TenantID, ModelID: "model"}, "inflight"); err != nil {
		t.Fatal(err)
	}
	if err = writer.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	barrier := <-completed
	if barrier.err != nil || !barrier.value.Through.IsZero() {
		t.Fatalf("in-flight coverage: %+v", barrier)
	}
	latest, err := usage.Read(ctx, request.TenantID, 10)
	if err != nil {
		t.Fatal(err)
	}
	ack(latest)
	// A backwards wall clock cannot reuse an already covered occurrence time.
	future := time.Now().UTC().Add(time.Second).Truncate(time.Microsecond)
	exec("UPDATE "+schema+".usage_source_clocks SET through=$2 WHERE tenant_id=$1", request.TenantID, future)
	mark, err := usage.Coverage(ctx, request.TenantID)
	if err != nil || !mark.Through.After(future) || !mark.From.Equal(cutover.From) || mark.Epoch != expansion.Epoch {
		t.Fatalf("nonmonotonic source clock: %+v %v", mark, err)
	}
	wrong := handoff
	wrong.ExpectedRouting = strings.Repeat("e", 64)
	if _, err = privileged.Handoff(ctx, wrong); err == nil {
		t.Fatal("changed handoff expectation accepted")
	}
	next, err := privileged.Handoff(ctx, handoff)
	if err != nil || !next.From.After(mark.Through) || next.Epoch != handoff.Epoch {
		t.Fatalf("drained handoff %+v %v", next, err)
	}
	if _, err = usage.Read(ctx, request.TenantID, 10); err == nil {
		t.Fatal("old owner continued after handoff")
	}
	newOwner, err := usage.ForSubscription(handoff.Consumer, handoff.Routing)
	if err != nil {
		t.Fatal(err)
	}
	if current, e := newOwner.Coverage(ctx, request.TenantID); e != nil || !current.From.Equal(next.From) {
		t.Fatalf("new epoch coverage %+v %v", current, e)
	}
	if _, err = privileged.Handoff(ctx, handoff); err == nil {
		t.Fatal("stale handoff replay rewrote boundary")
	}
	var handoffs int
	if err = pool.QueryRow(ctx, "SELECT count(*) FROM "+schema+".usage_source_handoffs WHERE tenant_id=$1", request.TenantID).Scan(&handoffs); err != nil || handoffs != 2 {
		t.Fatalf("handoff audit %d %v", handoffs, err)
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
