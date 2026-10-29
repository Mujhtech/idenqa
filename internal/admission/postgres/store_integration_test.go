//go:build integration

package postgres

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This component fixture exercises the actual migration's release triggers on
// PostgreSQL. The separate runnable API journey proves real Core completion;
// these minimal source rows do not claim a production metadata/archive policy.
func TestSourceClosureReleasesExactLifetimes(t *testing.T) {
	connection := os.Getenv("DATABASE_TEST_URL")
	if connection == "" {
		t.Skip("DATABASE_TEST_URL required")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, connection)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	suffix := fmt.Sprint(time.Now().UnixNano())
	schema, role := "admission_"+suffix, "admission_role_"+suffix
	qs, qr := pgx.Identifier{schema}.Sanitize(), pgx.Identifier{role}.Sanitize()
	rewrite := func(query string) string {
		query = strings.ReplaceAll(query, "idenqa.", qs+".")
		// Namespace SQL objects without changing the public tenant-scope GUC.
		return strings.ReplaceAll(query, "'"+qs+".tenant_id'", "'idenqa.tenant_id'")
	}
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, rewrite(query), args...); err != nil {
			t.Fatal(err)
		}
	}
	exec("CREATE SCHEMA " + qs + "; CREATE ROLE " + qr + " NOLOGIN NOSUPERUSER NOBYPASSRLS")
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := pool.Exec(cleanup, "DROP SCHEMA "+qs+" CASCADE; DROP ROLE "+qr); err != nil {
			t.Error(err)
		}
	})
	exec(`CREATE TABLE idenqa.tenants(id text PRIMARY KEY); CREATE TABLE idenqa.verification_sessions(tenant_id text,id text,state text,PRIMARY KEY(tenant_id,id)); INSERT INTO idenqa.tenants VALUES('tenant')`)
	raw, err := migrations.Files.ReadFile("000093_operational_admission.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	exec(string(raw))
	exec(`INSERT INTO idenqa.admission_snapshots(tenant_id,revision,id,deployment_id,region,valid_from,valid_until,body,digest) VALUES('tenant',1,'snapshot','deployment','region',clock_timestamp(),clock_timestamp()+interval '1 hour','{"scope":{"tenantId":"tenant","deploymentId":"deployment","region":"region"},"revision":1,"id":"snapshot"}',repeat('0',64))`)
	exec("GRANT USAGE ON SCHEMA " + qs + " TO " + qr)
	exec("GRANT SELECT,INSERT,UPDATE ON idenqa.admission_operations,idenqa.admission_counters,idenqa.admission_reservations TO " + qr)
	exec("GRANT SELECT,INSERT ON idenqa.admission_events TO " + qr)
	exec("GRANT SELECT,UPDATE,DELETE ON idenqa.verification_sessions TO " + qr)
	for _, state := range []string{"completed", "cancelled", "expired", "failed", "deleted"} {
		t.Run(state, func(t *testing.T) {
			exec(`INSERT INTO idenqa.verification_sessions VALUES('tenant',$1,'collecting')`, state)
			exec(`INSERT INTO idenqa.admission_operations(tenant_id,id,rule,digest,snapshot_revision,state,accepted_at) VALUES('tenant',$1,'rule',repeat('0',64),1,'active',clock_timestamp())`, state)
			for _, kind := range []string{"rate", "usage", "concurrency", "capacity", "count", "storage"} {
				lifetime := "delete"
				switch kind {
				case "rate", "usage":
					lifetime = "window"
				case "concurrency", "capacity":
					lifetime = "terminal"
				}
				exec(`INSERT INTO idenqa.admission_counters VALUES('tenant',$1,$2,0,7,false)`, kind, state)
				exec(`INSERT INTO idenqa.admission_reservations(tenant_id,operation_id,kind,key,window_key,quantity,lifetime) VALUES('tenant',$1,$2,$1,0,7,$3)`, state, kind, lifetime)
			}
			mutate := func(deleted bool) {
				t.Helper()
				tx, err := pool.Begin(ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = tx.Rollback(ctx) }()
				if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+qr); err != nil {
					t.Fatal(err)
				}
				if _, err := tx.Exec(ctx, `SELECT set_config('idenqa.tenant_id','tenant',true)`); err != nil {
					t.Fatal(err)
				}
				query := `UPDATE idenqa.verification_sessions SET state=$1 WHERE tenant_id='tenant' AND id=$1`
				if deleted {
					query = `DELETE FROM idenqa.verification_sessions WHERE tenant_id='tenant' AND id=$1`
				}
				if _, err := tx.Exec(ctx, strings.ReplaceAll(query, "idenqa.", qs+"."), state); err != nil {
					t.Fatal(err)
				}
				if err := tx.Commit(ctx); err != nil {
					t.Fatal(err)
				}
			}
			assert := func(deleted bool) {
				t.Helper()
				rows, err := pool.Query(ctx, `SELECT kind,used FROM `+qs+`.admission_counters WHERE tenant_id='tenant' AND key=$1`, state)
				if err != nil {
					t.Fatal(err)
				}
				defer rows.Close()
				count := 0
				for rows.Next() {
					var kind string
					var used int64
					if err := rows.Scan(&kind, &used); err != nil {
						t.Fatal(err)
					}
					want := int64(7)
					if kind == "capacity" || kind == "concurrency" || (deleted && kind != "rate" && kind != "usage") {
						want = 0
					}
					if used != want {
						t.Fatalf("%s after delete=%v used=%d want=%d", kind, deleted, used, want)
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if count != 6 {
					t.Fatalf("counter count=%d", count)
				}
			}
			if state != "deleted" {
				mutate(false)
				assert(false)
				mutate(false)
				assert(false)
			}
			mutate(true)
			assert(true)
			var got string
			var events int
			if err := pool.QueryRow(ctx, `SELECT state,(SELECT count(*) FROM `+qs+`.admission_events WHERE tenant_id='tenant' AND reference=$1) FROM `+qs+`.admission_operations WHERE tenant_id='tenant' AND id=$1`, state).Scan(&got, &events); err != nil {
				t.Fatal(err)
			}
			want := 2
			if state == "deleted" {
				want = 1
			}
			if got != "deleted" || events != want {
				t.Fatalf("closure state=%s events=%d want=%d", got, events, want)
			}
		})
	}
}
