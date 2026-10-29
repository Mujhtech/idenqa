//go:build integration

package postgres

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/Mujhtech/idenqa/internal/admission"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const resourceTenant = "ten_01ARZ3NDEKTSV4RRFFQ69G5FAV"
const resourceVerification = "ver_01ARZ3NDEKTSV4RRFFQ69G5FAV"

type resourceTX struct {
	pgx.Tx
	schema string
}

func (t resourceTX) rewrite(query string) string {
	return strings.ReplaceAll(strings.ReplaceAll(query, "idenqa.", t.schema+"."), "'"+t.schema+".tenant_id'", "'idenqa.tenant_id'")
}
func (t resourceTX) Exec(ctx context.Context, q string, a ...any) (pgconn.CommandTag, error) {
	return t.Tx.Exec(ctx, t.rewrite(q), a...)
}
func (t resourceTX) Query(ctx context.Context, q string, a ...any) (pgx.Rows, error) {
	return t.Tx.Query(ctx, t.rewrite(q), a...)
}
func (t resourceTX) QueryRow(ctx context.Context, q string, a ...any) pgx.Row {
	return t.Tx.QueryRow(ctx, t.rewrite(q), a...)
}

type resourceRunner struct {
	pool   *pgxpool.Pool
	schema string
}

func (r resourceRunner) WithinTransaction(ctx context.Context, _ pg.TransactionOptions, work func(context.Context, pg.Transaction) error) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = work(ctx, resourceTX{tx, r.schema}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type resourceFixture struct {
	owner, runtime               *pgxpool.Pool
	schema, role, kind, workflow string
	store                        *Store
	snapshot                     admissionv1.Snapshot
}

func newResourceFixture(t *testing.T, kind string, maximum int64) resourceFixture {
	t.Helper()
	url := os.Getenv("DATABASE_TEST_URL")
	if url == "" {
		t.Fatal("DATABASE_TEST_URL required")
	}
	ctx := t.Context()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	schema, role := "dependent_admission_"+suffix, "dependent_admission_role_"+suffix
	qs, qr := pgx.Identifier{schema}.Sanitize(), pgx.Identifier{role}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+qs+";CREATE ROLE "+qr+" NOLOGIN NOSUPERUSER NOBYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.Exec(ctx, "DROP SCHEMA "+qs+" CASCADE;DROP ROLE "+qr); err != nil {
			t.Error(err)
		}
	})
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	config.MaxConns = 6
	owner, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	rewrite := func(q string) string { return resourceTX{schema: qs}.rewrite(q) }
	exec := func(q string, a ...any) {
		t.Helper()
		if _, err := owner.Exec(ctx, rewrite(q), a...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE idenqa.tenants(id text PRIMARY KEY);CREATE TABLE idenqa.verification_sessions(tenant_id text,id text,state text,PRIMARY KEY(tenant_id,id));
 CREATE TABLE idenqa.verification_attempts(tenant_id text,id text,verification_id text,runner_kind text,state text,PRIMARY KEY(tenant_id,id));
 CREATE TABLE idenqa.provider_requests(tenant_id text,attempt_id text,verification_id text,request_body jsonb,PRIMARY KEY(tenant_id,attempt_id));
 CREATE TABLE idenqa.model_requests(tenant_id text,attempt_id text,verification_id text,request_body jsonb,PRIMARY KEY(tenant_id,attempt_id));
 CREATE TABLE idenqa.evidence_upload_intents(tenant_id text,id text,verification_id text,evidence_id text,state text,attempt int NOT NULL DEFAULT 0,evidence_type text,artefact text,acquisition_method text,media_type text,retention_class text,expected_bytes bigint,PRIMARY KEY(tenant_id,id));
 CREATE TABLE idenqa.evidence_assets(tenant_id text,id text,state text,PRIMARY KEY(tenant_id,id));
 CREATE TABLE idenqa.evidence_object_reconciliations(tenant_id text,upload_id text,upload_attempt int,evidence_id text,state text,PRIMARY KEY(tenant_id,upload_id,upload_attempt));
 CREATE TABLE idenqa.review_cases(tenant_id text,id text,verification_id text,required_certification text,oversight text,state text,PRIMARY KEY(tenant_id,id));`)
	for _, file := range []string{"000093_operational_admission.up.sql", "000094_dependent_resource_admission.up.sql"} {
		raw, err := migrations.Files.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		exec(string(raw))
	}
	for _, table := range []string{"verification_sessions", "verification_attempts", "provider_requests", "model_requests", "evidence_upload_intents", "evidence_assets", "evidence_object_reconciliations", "review_cases"} {
		exec("ALTER TABLE idenqa." + table + " ENABLE ROW LEVEL SECURITY;ALTER TABLE idenqa." + table + " FORCE ROW LEVEL SECURITY;CREATE POLICY tenant_scope ON idenqa." + table + " USING(tenant_id=current_setting('idenqa.tenant_id',true)) WITH CHECK(tenant_id=current_setting('idenqa.tenant_id',true))")
	}
	exec("GRANT USAGE ON SCHEMA " + qs + " TO " + qr)
	exec("GRANT SELECT,INSERT,UPDATE ON ALL TABLES IN SCHEMA " + qs + " TO " + qr)
	exec("GRANT EXECUTE ON FUNCTION idenqa.admission_resource_release(text,text,text,boolean),idenqa.admission_evidence_cleanup_proven(text,text) TO " + qr)
	config.AfterConnect = func(ctx context.Context, c *pgx.Conn) error { _, err := c.Exec(ctx, "SET ROLE "+qr); return err }
	runtime, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	public, private, _ := ed25519.GenerateKey(bytes.NewReader(bytes.Repeat([]byte{4}, 64)))
	policy, err := admission.NewPolicy(admission.Config{DeploymentID: "deployment", Region: "eu-west-1", MaximumLifetime: 2 * time.Hour, Issuers: map[string]ed25519.PublicKey{"issuer": public}})
	if err != nil {
		t.Fatal(err)
	}
	store, err := New(resourceRunner{runtime, qs}, policy)
	if err != nil {
		t.Fatal(err)
	}
	workflow, err := admissionv1.VerificationOperation("profile", 1, "policy")
	if err != nil {
		t.Fatal(err)
	}
	selection, err := admissionv1.ResourceOperation(kind, resourceSelectors(kind)...)
	if err != nil {
		t.Fatal(err)
	}
	dependent, err := admissionv1.DependentOperation(workflow, selection)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	limits := []admissionv1.Limit{{Kind: "capacity", Key: "slot", Maximum: maximum}, {Kind: "count", Key: "record", Maximum: 10}, {Kind: "usage", Key: "units", Maximum: 100, Window: time.Minute}}
	budgets := []admissionv1.Budget{{Kind: "capacity", Key: "slot", Quantity: 1, Lifetime: "terminal"}, {Kind: "count", Key: "record", Quantity: 1, Lifetime: "delete"}, {Kind: "usage", Key: "units", Quantity: 1, Lifetime: "window"}}
	if kind == "evidence" {
		limits = append(limits, admissionv1.Limit{Kind: "storage", Key: "bytes", Maximum: 256})
		budgets = append(budgets, admissionv1.Budget{Kind: "storage", Key: "bytes", Quantity: 128, Lifetime: "delete"})
	}
	slices.SortFunc(limits, func(a, b admissionv1.Limit) int { return strings.Compare(a.Kind+":"+a.Key, b.Kind+":"+b.Key) })
	slices.SortFunc(budgets, func(a, b admissionv1.Budget) int { return strings.Compare(a.Kind+":"+a.Key, b.Kind+":"+b.Key) })
	snapshot := admissionv1.Snapshot{Schema: admissionv1.Schema, ID: "snapshot", Scope: admissionv1.Scope{TenantID: resourceTenant, DeploymentID: "deployment", Region: "eu-west-1"}, Revision: 1, Issuer: "issuer", IssuedAt: now.Add(-time.Minute), ValidFrom: now.Add(-time.Minute), ValidUntil: now.Add(time.Hour), Status: "active", Capabilities: map[string]int64{"work": 1}, Limits: limits, Rules: []admissionv1.Rule{{Operation: dependent, Capabilities: map[string]int64{"work": 1}, Budgets: budgets}}}
	snapshot.Proof = ed25519.Sign(private, snapshot.SigningBytes())
	if err = snapshot.Verify(public); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO idenqa.tenants VALUES($1)`, resourceTenant)
	exec(`INSERT INTO idenqa.verification_sessions VALUES($1,$2,'collecting')`, resourceTenant, resourceVerification)
	exec(`INSERT INTO idenqa.admission_snapshots(tenant_id,revision,id,deployment_id,region,valid_from,valid_until,body,digest) VALUES($1,1,'snapshot','deployment','eu-west-1',$2,$3,$4,repeat('0',64))`, resourceTenant, snapshot.ValidFrom, snapshot.ValidUntil, raw)
	exec(`INSERT INTO idenqa.admission_operations(tenant_id,id,rule,digest,snapshot_revision,state,accepted_at) VALUES($1,$2,$3,repeat('0',64),1,'active',$4)`, resourceTenant, resourceVerification, workflow, now.Add(-time.Second))
	return resourceFixture{owner, runtime, qs, qr, kind, workflow, store, snapshot}
}
func resourceSelectors(kind string) []string {
	switch kind {
	case "provider":
		return []string{"provider", "version", "package_digest", "check"}
	case "model":
		return []string{"registration", "version", "model_digest", "evaluation"}
	case "review":
		return []string{"reviewer_certificate", "single"}
	default:
		return []string{"document", "front", "file_upload", "image/jpeg", "temporary"}
	}
}
func (f resourceFixture) source(ctx context.Context, tx resourceTX, id string) error {
	switch f.kind {
	case "provider", "model":
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.verification_attempts VALUES($1,$2,$3,$4,'running')`, resourceTenant, id, resourceVerification, f.kind); err != nil {
			return err
		}
		body := `{"provider_id":"provider","adapter":{"adapter_version":"version","package_digest":"package_digest"},"check":"check"}`
		if f.kind == "model" {
			body = `{"model_registration_id":"registration","provenance":{"model_version":"version","model_digest":"model_digest"},"evaluation":"evaluation"}`
		}
		_, err := tx.Exec(ctx, "INSERT INTO idenqa."+f.kind+"_requests VALUES($1,$2,$3,$4)", resourceTenant, id, resourceVerification, []byte(body))
		return err
	case "review":
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.review_cases VALUES($1,$2,$3,'reviewer_certificate','single','open')`, resourceTenant, id, resourceVerification)
		return err
	default:
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.evidence_upload_intents VALUES($1,$2,$3,$4,'issued',0,'document','front','file_upload','image/jpeg','temporary',8)`, resourceTenant, id, resourceVerification, "evidence_"+id)
		return err
	}
}
func (f resourceFixture) within(ctx context.Context, work func(resourceTX) error) error {
	tx, err := f.runtime.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	wrapped := resourceTX{tx, f.schema}
	if _, err = wrapped.Exec(ctx, `SELECT set_config('idenqa.tenant_id',$1,true)`, resourceTenant); err != nil {
		return err
	}
	if err = work(wrapped); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
func (f resourceFixture) admit(ctx context.Context, id string) error {
	return f.within(ctx, func(tx resourceTX) error {
		if err := f.source(ctx, tx, id); err != nil {
			return err
		}
		return f.store.AdmitResourceWithin(ctx, tx, admission.Resource{TenantID: resourceTenant, VerificationID: resourceVerification, Kind: f.kind, ID: id})
	})
}
func (f resourceFixture) used(t *testing.T, kind string) int64 {
	t.Helper()
	var used int64
	if err := f.owner.QueryRow(t.Context(), `SELECT coalesce(sum(used),0)::bigint FROM `+f.schema+`.admission_counters WHERE tenant_id=$1 AND kind=$2`, resourceTenant, kind).Scan(&used); err != nil {
		t.Fatal(err)
	}
	return used
}

func TestDependentResourcesReserveRollbackReplayAndRelease(t *testing.T) {
	for _, kind := range []string{"provider", "model", "review", "evidence"} {
		t.Run(kind, func(t *testing.T) {
			f := newResourceFixture(t, kind, 1)
			ctx := t.Context()
			if err := f.admit(ctx, "first"); err != nil {
				t.Fatal(err)
			}
			if f.used(t, "capacity") != 1 || f.used(t, "count") != 1 || f.used(t, "usage") != 1 {
				t.Fatal("resource budgets were not atomic")
			}
			replay := admission.Resource{TenantID: resourceTenant, VerificationID: resourceVerification, Kind: kind, ID: "first"}
			if err := f.within(ctx, func(tx resourceTX) error { return f.store.AdmitResourceWithin(ctx, tx, replay) }); err != nil {
				t.Fatal(err)
			}
			if f.used(t, "usage") != 1 {
				t.Fatal("replay reserved another usage unit")
			}
			if err := f.admit(ctx, "second"); !errors.Is(err, admission.ErrQuota) {
				t.Fatalf("capacity was not enforced: %v", err)
			}
			var count int
			sourceTable, sourceColumn := "verification_attempts", "id"
			if kind == "review" {
				sourceTable = "review_cases"
			}
			if kind == "evidence" {
				sourceTable = "evidence_upload_intents"
			}
			if err := f.owner.QueryRow(ctx, "SELECT count(*) FROM "+f.schema+"."+sourceTable+" WHERE "+sourceColumn+"='second'").Scan(&count); err != nil || count != 0 {
				t.Fatalf("denied source survived transaction %d %v", count, err)
			}
			if err := f.within(ctx, func(tx resourceTX) error {
				table := sourceTable
				state := "succeeded"
				if kind == "review" {
					state = "resolved"
				}
				if kind == "evidence" {
					state = "expired"
				}
				_, err := tx.Exec(ctx, "UPDATE idenqa."+table+" SET state=$1 WHERE tenant_id=$2 AND id='first'", state, resourceTenant)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if f.used(t, "capacity") != 0 || f.used(t, "usage") != 1 {
				t.Fatal("terminal release refunded usage or retained concurrency")
			}
			// Metadata count/storage remain until an actual delete lifetime is proved.
			expectedCount := int64(1)
			if kind == "evidence" {
				expectedCount = 0
			}
			if f.used(t, "count") != expectedCount {
				t.Fatalf("count lifetime=%d", f.used(t, "count"))
			}
			if err := f.within(ctx, func(tx resourceTX) error { return f.store.AdmitResourceWithin(ctx, tx, replay) }); err != nil {
				t.Fatalf("terminal replay lost accepted meaning: %v", err)
			}
			if f.used(t, "capacity") != 0 {
				t.Fatal("terminal replay reopened reservation")
			}
			if err := f.admit(ctx, "third"); err != nil {
				t.Fatalf("released slot unavailable: %v", err)
			}
			if f.used(t, "usage") != 2 {
				t.Fatal("new resource did not consume distinct usage")
			}
			if err := f.runtime.QueryRow(ctx, `SELECT count(*) FROM `+f.schema+`.admission_resource_sources`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("unscoped RLS leaked %d %v", count, err)
			}
		})
	}
}
func TestDependentResourceZeroCapacityDeniesWithoutPartialSource(t *testing.T) {
	for _, kind := range []string{"provider", "model", "review", "evidence"} {
		t.Run(kind, func(t *testing.T) {
			f := newResourceFixture(t, kind, 0)
			err := f.admit(t.Context(), "denied")
			if !errors.Is(err, admission.ErrCeiling) {
				t.Fatalf("zero ceiling accepted: %v", err)
			}
			if f.used(t, "capacity") != 0 || f.used(t, "count") != 0 || f.used(t, "usage") != 0 {
				t.Fatal("hard refusal left a partial reservation")
			}
		})
	}
}
func TestDependentResourceConcurrentAdmissionCannotOverdraw(t *testing.T) {
	for _, kind := range []string{"provider", "model", "review", "evidence"} {
		t.Run(kind, func(t *testing.T) {
			f := newResourceFixture(t, kind, 1)
			start := make(chan struct{})
			results := make(chan error, 2)
			var group sync.WaitGroup
			for i := range 2 {
				group.Go(func() {
					<-start
					results <- f.admit(t.Context(), fmt.Sprint("contender_", i))
				})
			}
			close(start)
			group.Wait()
			close(results)
			admitted, denied := 0, 0
			for err := range results {
				switch {
				case err == nil:
					admitted++
				case errors.Is(err, admission.ErrQuota):
					denied++
				default:
					t.Fatal(err)
				}
			}
			if admitted != 1 || denied != 1 || f.used(t, "capacity") != 1 || f.used(t, "usage") != 1 {
				t.Fatalf("overdraw admitted=%d denied=%d", admitted, denied)
			}
		})
	}
}
func TestStartedEvidenceExpiryHoldsStorageUntilEveryAttemptIsProvenDeleted(t *testing.T) {
	f := newResourceFixture(t, "evidence", 1)
	ctx := t.Context()
	if err := f.admit(ctx, "upload"); err != nil {
		t.Fatal(err)
	}
	if err := f.within(ctx, func(tx resourceTX) error {
		_, err := tx.Exec(ctx, `UPDATE idenqa.evidence_upload_intents SET state='uploading',attempt=2 WHERE tenant_id=$1 AND id='upload'`, resourceTenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.within(ctx, func(tx resourceTX) error {
		_, err := tx.Exec(ctx, `UPDATE idenqa.evidence_upload_intents SET state='expired' WHERE tenant_id=$1 AND id='upload'`, resourceTenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if f.used(t, "capacity") != 0 || f.used(t, "storage") != 128 || f.used(t, "count") != 1 {
		t.Fatal("unknown in-flight storage was released on expiry")
	}
	if err := f.within(ctx, func(tx resourceTX) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.evidence_object_reconciliations VALUES($1,'upload',1,'evidence_upload','pending')`, resourceTenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.within(ctx, func(tx resourceTX) error {
		_, err := tx.Exec(ctx, `UPDATE idenqa.evidence_object_reconciliations SET state='deleted' WHERE tenant_id=$1 AND upload_id='upload' AND upload_attempt=1`, resourceTenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if f.used(t, "storage") != 128 {
		t.Fatal("one deleted attempt proved all attempts absent")
	}
	if err := f.within(ctx, func(tx resourceTX) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.evidence_object_reconciliations VALUES($1,'upload',2,'evidence_upload','pending')`, resourceTenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.within(ctx, func(tx resourceTX) error {
		_, err := tx.Exec(ctx, `UPDATE idenqa.evidence_object_reconciliations SET state='deleted' WHERE tenant_id=$1 AND upload_id='upload' AND upload_attempt=2`, resourceTenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if f.used(t, "storage") != 0 || f.used(t, "count") != 0 || f.used(t, "usage") != 1 {
		t.Fatal("exact physical deletion did not release held lifetime")
	}
}
func TestAcceptedEvidenceRetainsStorageThroughPendingPhysicalCleanup(t *testing.T) {
	f := newResourceFixture(t, "evidence", 1)
	ctx := t.Context()
	if err := f.admit(ctx, "accepted"); err != nil {
		t.Fatal(err)
	}
	if err := f.within(ctx, func(tx resourceTX) error {
		if _, err := tx.Exec(ctx, `UPDATE idenqa.evidence_upload_intents SET state='uploading',attempt=1 WHERE tenant_id=$1 AND id='accepted'`, resourceTenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.evidence_assets VALUES($1,'evidence_accepted','available')`, resourceTenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.evidence_object_reconciliations VALUES($1,'accepted',1,'evidence_accepted','retained')`, resourceTenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE idenqa.evidence_upload_intents SET state='accepted' WHERE tenant_id=$1 AND id='accepted'`, resourceTenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if f.used(t, "storage") != 128 || f.used(t, "capacity") != 0 {
		t.Fatal("capture acceptance released live evidence storage")
	}
	if err := f.within(ctx, func(tx resourceTX) error {
		_, err := tx.Exec(ctx, `UPDATE idenqa.evidence_assets SET state='deleted' WHERE tenant_id=$1 AND id='evidence_accepted'`, resourceTenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if f.used(t, "storage") != 0 || f.used(t, "count") != 0 {
		t.Fatal("deleted accepted asset retained physical budget")
	}
}

func TestManagedUploadRetryWaitsForExactPriorObjectDeletion(t *testing.T) {
	f := newResourceFixture(t, "evidence", 1)
	ctx := t.Context()
	if err := f.admit(ctx, "retry"); err != nil {
		t.Fatal(err)
	}
	claim := func(attempt int) error {
		return f.within(ctx, func(tx resourceTX) error {
			_, err := tx.Exec(ctx, `UPDATE idenqa.evidence_upload_intents SET state='uploading',attempt=$2 WHERE tenant_id=$1 AND id='retry'`, resourceTenant, attempt)
			return err
		})
	}
	if err := claim(1); err != nil {
		t.Fatalf("first claim rejected: %v", err)
	}
	if err := claim(1); err != nil {
		t.Fatalf("same-claim replay rejected: %v", err)
	}
	assertBlocked := func() {
		t.Helper()
		err := claim(2)
		var databaseError *pgconn.PgError
		if !errors.As(err, &databaseError) || databaseError.Code != "PQA01" {
			t.Fatalf("uncertain retry accepted: %v", err)
		}
	}
	assertBlocked()
	if err := f.within(ctx, func(tx resourceTX) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.evidence_object_reconciliations VALUES($1,'retry',1,'evidence_retry','pending')`, resourceTenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	if err := f.within(ctx, func(tx resourceTX) error {
		_, err := tx.Exec(ctx, `UPDATE idenqa.evidence_object_reconciliations SET state='deleted' WHERE tenant_id=$1 AND upload_id='retry'`, resourceTenant)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := claim(2); err != nil {
		t.Fatalf("exact cleanup did not permit next attempt: %v", err)
	}
	if f.used(t, "storage") != 128 || f.used(t, "usage") != 1 {
		t.Fatal("retry changed the fixed physical reservation or created billing units")
	}
}
func TestStandaloneEvidenceTransitionsNeedNoAdmissionCleanupAuthority(t *testing.T) {
	f := newResourceFixture(t, "evidence", 1)
	ctx := t.Context()
	if _, err := f.owner.Exec(ctx, `REVOKE EXECUTE ON FUNCTION `+f.schema+`.admission_evidence_cleanup_proven(text,text) FROM `+f.role); err != nil {
		t.Fatal(err)
	}
	if err := f.within(ctx, func(tx resourceTX) error {
		if err := f.source(ctx, tx, "standalone"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE idenqa.evidence_upload_intents SET state='uploading',attempt=1 WHERE tenant_id=$1 AND id='standalone'`, resourceTenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE idenqa.evidence_upload_intents SET attempt=2 WHERE tenant_id=$1 AND id='standalone'`, resourceTenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.evidence_assets VALUES($1,'evidence_standalone','available')`, resourceTenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.evidence_object_reconciliations VALUES($1,'standalone',2,'evidence_standalone','pending')`, resourceTenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE idenqa.evidence_upload_intents SET state='accepted' WHERE tenant_id=$1 AND id='standalone'`, resourceTenant); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE idenqa.evidence_object_reconciliations SET state='retained' WHERE tenant_id=$1 AND upload_id='standalone'`, resourceTenant); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE idenqa.evidence_assets SET state='deleted' WHERE tenant_id=$1 AND id='evidence_standalone'`, resourceTenant)
		return err
	}); err != nil {
		t.Fatalf("standalone evidence was coupled to admission: %v", err)
	}
}
func TestEvidencePhysicalCiphertextMustFitTheSignedStorageBudget(t *testing.T) {
	f := newResourceFixture(t, "evidence", 1)
	ctx := t.Context()
	err := f.within(ctx, func(tx resourceTX) error {
		if err := f.source(ctx, tx, "too_large"); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE idenqa.evidence_upload_intents SET expected_bytes=128 WHERE tenant_id=$1 AND id='too_large'`, resourceTenant); err != nil {
			return err
		}
		return f.store.AdmitResourceWithin(ctx, tx, admission.Resource{TenantID: resourceTenant, VerificationID: resourceVerification, Kind: "evidence", ID: "too_large"})
	})
	if !errors.Is(err, admission.ErrCeiling) || f.used(t, "storage") != 0 {
		t.Fatalf("plaintext128 fits128 but physical184 must not: %v", err)
	}
}
