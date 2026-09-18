//go:build integration

package integration_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func publicSyntheticPolicyDefinition() policy.Definition {
	return policy.Definition{SchemaMajor: 1, VerifiedAssurance: "synthetic.fixture", Rules: []policyv1.Rule{{Name: "synthetic_success", When: `facts["synthetic.document"] == "satisfied" && facts["synthetic.liveness"] == "satisfied"`, Result: policyv1.Result{State: policyv1.RequirementSatisfied, Directive: policyv1.DirectiveCompleteVerified, Priority: 1, ContributingFacts: []string{"synthetic.document", "synthetic.liveness"}, ReasonCodes: []string{}}}}}
}

func assertPublicPolicyAdministration(t *testing.T, client *http.Client, base, credential string, admin, runtime *pg.Pool, scope tenant.Scope, peppers *access.PepperSet) id.Policy {
	t.Helper()
	call := func(method, path, key string, body, result any, status int) {
		t.Helper()
		performPublicJSONRequest(t, client, publicJSONRequest{Method: method, URL: base + path, Bearer: credential, IdempotencyKey: key, Body: body, Result: result, WantStatus: status})
	}
	definition := publicSyntheticPolicyDefinition()
	body := map[string]any{"definition": definition}
	var validation policy.Validation
	call("POST", "/v1/policies/validate", "", body, &validation, 200)
	if !validation.Valid || validation.RuleCount != 1 || validation.EvaluatorDigest == "" {
		t.Fatal("validation did not compile")
	}
	invalid := publicSyntheticPolicyDefinition()
	invalid.Rules[0].When = `facts["undeclared.key"] == "satisfied"`
	call("POST", "/v1/policies/validate", "", map[string]any{"definition": invalid}, nil, 400)
	var first, retry policy.ManagementResult
	call("POST", "/v1/policies", "public-policy-create", body, &first, 200)
	if first.Policy.ID == "" || first.Policy.ActiveRevision != nil || first.Policy.ActivationVersion != 0 || first.Policy.LatestRevision != 1 || first.Revision == nil {
		t.Fatal("new policy must contain inactive revision one")
	}
	call("POST", "/v1/policies", "public-policy-create", body, &retry, 200)
	if !retry.Replayed || retry.Policy.ID != first.Policy.ID || !retry.Revision.CreatedAt.Equal(first.Revision.CreatedAt) {
		t.Fatal("creation retry changed identity")
	}
	reordered := publicSyntheticPolicyDefinition()
	reordered.Rules[0].Result.ContributingFacts = []string{"synthetic.liveness", "synthetic.document"}
	call("POST", "/v1/policies", "public-policy-create", map[string]any{"definition": reordered}, &retry, 200)
	if !retry.Replayed {
		t.Fatal("canonical policy meaning did not replay")
	}
	call("POST", "/v1/policies", " leading-space", body, nil, 400)
	call("POST", "/v1/policies", "public-policy-create", map[string]any{"definition": invalid}, nil, 409)
	identifier, err := id.ParsePolicy(first.Policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := "/v1/policies/" + identifier.String()
	var source policy.RevisionDocument
	call("GET", path+"/revisions/1", "", nil, &source, 200)
	document, err := policyv1.ParseCanonical(source.Document)
	if err != nil || document.PolicyID != identifier.String() || document.Revision != 1 {
		t.Fatal("source did not preserve canonical policy identity")
	}
	call("POST", path+"/revisions", "public-policy-r2", map[string]any{"definition": definition, "expected_revision": 1}, &retry, 200)
	if retry.Policy.LatestRevision != 2 || retry.Policy.ActivationVersion != 0 {
		t.Fatal("registering revision implicitly activated it")
	}
	call("POST", path+"/revisions", "public-policy-stale", map[string]any{"definition": definition, "expected_revision": 1}, nil, 409)
	activate := func(operation, key string, revision uint32, version int64, result any, status int) {
		t.Helper()
		call("POST", path+"/"+operation, key, map[string]any{"revision": revision, "expected_version": version, "reason": "tenant_requested"}, result, status)
	}
	activate("activate", "public-policy-activate1", 1, 0, &first, 200)
	activate("activate", "public-policy-activate1", 1, 0, &retry, 200)
	if retry.Activation == nil || !retry.Replayed || retry.Activation.Version != 1 || retry.Activation.ActorID == "" {
		t.Fatal("activation did not replay the original audit identity")
	}
	activate("activate", "public-policy-stale-activation", 2, 0, nil, 409)
	activate("activate", "public-policy-activate2", 2, 1, &first, 200)
	call("POST", path+"/revisions", "public-policy-r3", map[string]any{"definition": definition, "expected_revision": 2}, nil, 200)
	activate("rollback", "public-policy-never-active", 3, 2, nil, 409)
	activate("rollback", "public-policy-rollback1", 1, 2, &retry, 200)
	if retry.Activation.PreviousRevision != 2 || retry.Activation.Revision != 1 || retry.Activation.Version != 3 {
		t.Fatal("rollback did not append a new activation")
	}
	var revisions struct {
		Data []policy.RevisionInfo `json:"data"`
		Page struct {
			HasMore bool   `json:"has_more"`
			Next    string `json:"next_cursor"`
		} `json:"page"`
	}
	call("GET", path+"/revisions?limit=1", "", nil, &revisions, 200)
	if len(revisions.Data) != 1 || revisions.Data[0].Revision != 3 || !revisions.Page.HasMore {
		t.Fatal("missing descending revision page")
	}
	cursor := revisions.Page.Next
	call("GET", path+"/revisions?limit=1&cursor="+cursor, "", nil, &revisions, 200)
	if len(revisions.Data) != 1 || revisions.Data[0].Revision != 2 {
		t.Fatal("revision cursor skipped metadata")
	}
	call("GET", path+"/activations?limit=1&cursor="+cursor, "", nil, nil, 400)
	call("GET", path+"/revisions?limit=2&cursor="+cursor, "", nil, nil, 400)
	var history struct {
		Data []policy.ActivationInfo `json:"data"`
	}
	call("GET", path+"/activations", "", nil, &history, 200)
	if len(history.Data) != 3 || history.Data[0].Version != 3 || history.Data[2].Version != 1 {
		t.Fatal("activation retry duplicated or rewrote history")
	}
	var list struct {
		Data []policy.Summary `json:"data"`
	}
	call("GET", "/v1/policies", "", nil, &list, 200)
	if len(list.Data) != 1 || list.Data[0].ID != identifier.String() {
		t.Fatal("policy listing omitted new root")
	}
	// Rejecting the common outbox write must roll back revision and receipt as well.
	count := func() string {
		t.Helper()
		var result string
		err := admin.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
			return tx.QueryRow(ctx, `SELECT (SELECT count(*) FROM idenqa.policy_revisions WHERE tenant_id=$1)::text||':'||(SELECT count(*) FROM idenqa.audit_records WHERE tenant_id=$1)::text||':'||(SELECT count(*) FROM idenqa.outbox_events WHERE tenant_id=$1)::text||':'||(SELECT count(*) FROM idenqa.idempotency_records WHERE tenant_id=$1)::text`, scope.ID().String()).Scan(&result)
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := count()
	ddl := func(query string) {
		t.Helper()
		err := admin.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error { _, err := tx.Exec(ctx, query); return err })
		if err != nil {
			t.Fatal(err)
		}
	}
	ddl(`ALTER TABLE idenqa.outbox_events ADD CONSTRAINT test_reject_policy_revision CHECK (event_type <> 'policy.revision.v1') NOT VALID`)
	call("POST", path+"/revisions", "public-policy-r4", map[string]any{"definition": definition, "expected_revision": 3}, nil, 500)
	if count() != before {
		t.Fatal("failed common audit/outbox left partial revision effects")
	}
	ddl(`ALTER TABLE idenqa.outbox_events DROP CONSTRAINT test_reject_policy_revision`)
	call("POST", path+"/revisions", "public-policy-r4", map[string]any{"definition": definition, "expected_revision": 3}, &retry, 200)
	if retry.Policy.LatestRevision != 4 || retry.Policy.ActivationVersion != 3 {
		t.Fatal("retry after transaction failure lost state")
	}
	// Inspectors may not write or activate. Revision authors may not activate either.
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	accessStore, err := accesspostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	for _, permission := range []string{"policies:read", "policies:write"} {
		key, presented := newFullScopeIntegrationCredential(t, ids, scope.ID(), time.Now().UTC(), peppers)
		pattern, err := access.ParsePattern(permission)
		if err != nil {
			t.Fatal(err)
		}
		grant, err := access.TenantRegistry().Resolve(pattern)
		if err != nil {
			t.Fatal(err)
		}
		key, err = access.RestoreKey(access.KeyRecord{ID: key.ID(), TenantID: key.TenantID(), Label: key.Label(), Digest: key.Digest(), PepperVersion: key.PepperVersion(), Grant: grant, Version: 1, CreatedAt: key.CreatedAt(), UpdatedAt: key.UpdatedAt()})
		if err != nil {
			t.Fatal(err)
		}
		if err := accessStore.Create(t.Context(), scope, key); err != nil {
			t.Fatal(err)
		}
		performPublicJSONRequest(t, client, publicJSONRequest{Method: "POST", URL: base + path + "/activate", Bearer: presented.Reveal(), IdempotencyKey: "denied-activation", Body: map[string]any{"revision": 2, "expected_version": 3, "reason": "tenant_requested"}, WantStatus: 403})
		if permission == "policies:read" {
			performPublicJSONRequest(t, client, publicJSONRequest{Method: "POST", URL: base + "/v1/policies", Bearer: presented.Reveal(), IdempotencyKey: "denied-create", Body: body, WantStatus: 403})
		}
	}
	otherID, err := ids.NewTenant()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	err = admin.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		_, err := tx.Exec(ctx, `INSERT INTO idenqa.tenants(id,state,version,created_at,updated_at) VALUES($1,'active',1,$2,$2)`, otherID.String(), now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	otherScope, err := tenant.NewScope(otherID)
	if err != nil {
		t.Fatal(err)
	}
	key, presented := newFullScopeIntegrationCredential(t, ids, otherID, now, peppers)
	if err := accessStore.Create(t.Context(), otherScope, key); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"", "/revisions", "/revisions/1", "/activations"} {
		performPublicJSONRequest(t, client, publicJSONRequest{Method: "GET", URL: base + path + suffix, Bearer: presented.Reveal(), WantStatus: 404})
	}
	performPublicJSONRequest(t, client, publicJSONRequest{Method: "POST", URL: base + path + "/rollback", Bearer: presented.Reveal(), IdempotencyKey: "cross-tenant-policy", Body: map[string]any{"revision": 1, "expected_version": 3, "reason": "tenant_requested"}, WantStatus: 404})
	performPublicJSONRequest(t, client, publicJSONRequest{Method: "GET", URL: base + path + "/revisions?limit=1&cursor=" + cursor, Bearer: presented.Reveal(), WantStatus: 400})
	return identifier
}
