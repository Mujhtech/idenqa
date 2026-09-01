//go:build integration

package integration_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestPolicyCatalogDurabilityActivationConcurrencyAndIsolation(t *testing.T) {
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

	adminPool, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer adminPool.Close()
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstTenant, _ := seedExecutionVerification(t, adminPool, generator, now)
	secondTenant, _ := seedExecutionVerification(t, adminPool, generator, now)

	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtimePool, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimePool.Close()
	firstScope, err := tenant.NewScope(firstTenant)
	if err != nil {
		t.Fatal(err)
	}
	secondScope, err := tenant.NewScope(secondTenant)
	if err != nil {
		t.Fatal(err)
	}
	accessStore, err := accesspostgres.New(runtimePool)
	if err != nil {
		t.Fatal(err)
	}
	firstActor := newIntegrationKey(t, generator, firstTenant, now, id.APIKey{})
	if err := accessStore.Create(ctx, firstScope, firstActor); err != nil {
		t.Fatal(err)
	}
	secondActor := newIntegrationKey(t, generator, secondTenant, now, id.APIKey{})
	if err := accessStore.Create(ctx, secondScope, secondActor); err != nil {
		t.Fatal(err)
	}
	store, err := policypostgres.New(runtimePool)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := policy.NewCatalog(store, policycel.Compiler{})
	if err != nil {
		t.Fatal(err)
	}
	policyID, err := generator.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}

	registered := make([]policy.Revision, 3)
	for index := range registered {
		canonical := integrationPolicyCanonical(t, policyID, uint32(index+1))
		registered[index], err = catalog.RegisterCanonical(
			ctx, firstScope, canonical, now.Add(time.Duration(index)*time.Second),
		)
		if err != nil {
			t.Fatalf("register revision %d: %v", index+1, err)
		}
		replayed, replayErr := catalog.RegisterCanonical(
			ctx, firstScope, canonical, now.Add(time.Duration(index)*time.Second),
		)
		if replayErr != nil || replayed.Reference() != registered[index].Reference() {
			t.Fatalf("replay revision %d: %+v error=%v", index+1, replayed, replayErr)
		}
	}
	if _, err := store.FindRevision(ctx, secondScope, policyID, 1); !errors.Is(err, policy.ErrRevisionNotFound) {
		t.Fatalf("cross-tenant revision error = %v", err)
	}
	if _, err := catalog.FindActive(ctx, firstScope, policyID); !errors.Is(err, policy.ErrActivationNotFound) {
		t.Fatalf("inactive lookup error = %v", err)
	}

	firstActivation, err := catalog.Activate(
		ctx, firstScope, policyID, 1, 0, firstActor.ID(), now.Add(3*time.Second),
	)
	if err != nil || firstActivation.Version() != 1 {
		t.Fatalf("first activation=%+v error=%v", firstActivation, err)
	}
	if _, err := catalog.Activate(
		ctx, firstScope, policyID, 1, 1, firstActor.ID(), now.Add(4*time.Second),
	); !errors.Is(err, policy.ErrActivationConflict) {
		t.Fatalf("no-op activation error = %v", err)
	}
	if _, err := catalog.Activate(
		ctx, firstScope, policyID, 2, 1, secondActor.ID(), now.Add(4*time.Second),
	); !errors.Is(err, policy.ErrActivationConflict) {
		t.Fatalf("cross-tenant actor activation error = %v", err)
	}
	afterFailedAudit, err := catalog.FindActive(ctx, firstScope, policyID)
	if err != nil || afterFailedAudit.Version() != 1 ||
		afterFailedAudit.Revision().Reference().Revision != 1 {
		t.Fatalf("failed audit changed active state: %+v error=%v", afterFailedAudit, err)
	}

	activationErrors := make([]error, 2)
	var wait sync.WaitGroup
	for index, revision := range []uint32{2, 3} {
		wait.Add(1)
		go func(index int, revision uint32) {
			defer wait.Done()
			_, activationErrors[index] = catalog.Activate(
				ctx, firstScope, policyID, revision, 1, firstActor.ID(), now.Add(5*time.Second),
			)
		}(index, revision)
	}
	wait.Wait()
	successes, conflicts := 0, 0
	for _, activationErr := range activationErrors {
		if activationErr == nil {
			successes++
		} else if errors.Is(activationErr, policy.ErrActivationConflict) {
			conflicts++
		} else {
			t.Fatalf("unexpected activation error = %v", activationErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent activation successes=%d conflicts=%d", successes, conflicts)
	}
	winner, err := catalog.FindActive(ctx, firstScope, policyID)
	if err != nil || winner.Version() != 2 || winner.PreviousRevision() != 1 ||
		(winner.Revision().Reference().Revision != 2 && winner.Revision().Reference().Revision != 3) {
		t.Fatalf("winner=%+v error=%v", winner, err)
	}
	if _, err := catalog.FindActive(ctx, secondScope, policyID); !errors.Is(err, policy.ErrActivationNotFound) {
		t.Fatalf("cross-tenant activation error = %v", err)
	}
	inspector, err := policy.NewCatalogInspector(store)
	if err != nil {
		t.Fatal(err)
	}
	revisionPage, err := inspector.ListRevisions(ctx, firstScope, policyID, 0, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !revisionPage.HasMore() || revisionPage.NextBefore() != 2 ||
		len(revisionPage.Items()) != 2 ||
		revisionPage.Items()[0].Reference() != registered[2].Reference() ||
		revisionPage.Items()[1].Reference() != registered[1].Reference() {
		t.Fatalf("revision inspection page = %+v", revisionPage.Items())
	}
	oldestRevisions, err := inspector.ListRevisions(
		ctx, firstScope, policyID, revisionPage.NextBefore(), 2,
	)
	if err != nil || oldestRevisions.HasMore() || oldestRevisions.NextBefore() != 0 ||
		len(oldestRevisions.Items()) != 1 ||
		oldestRevisions.Items()[0].Reference() != registered[0].Reference() {
		t.Fatalf("oldest revision page = %+v error=%v", oldestRevisions.Items(), err)
	}
	activationPage, err := inspector.ListActivations(ctx, firstScope, policyID, 0, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !activationPage.HasMore() || activationPage.NextBefore() != 2 ||
		len(activationPage.Items()) != 1 || activationPage.Items()[0].Version() != 2 ||
		activationPage.Items()[0].Revision().Reference() != winner.Revision().Reference() ||
		activationPage.Items()[0].Actor() != firstActor.ID() {
		t.Fatalf("activation inspection page = %+v", activationPage.Items())
	}
	oldestActivations, err := inspector.ListActivations(
		ctx, firstScope, policyID, activationPage.NextBefore(), 1,
	)
	if err != nil || oldestActivations.HasMore() || oldestActivations.NextBefore() != 0 ||
		len(oldestActivations.Items()) != 1 || oldestActivations.Items()[0].Version() != 1 ||
		oldestActivations.Items()[0].Revision().Reference() != registered[0].Reference() {
		t.Fatalf("oldest activation page = %+v error=%v", oldestActivations.Items(), err)
	}
	crossTenantRevisions, err := inspector.ListRevisions(ctx, secondScope, policyID, 0, 10)
	if err != nil || len(crossTenantRevisions.Items()) != 0 || crossTenantRevisions.HasMore() {
		t.Fatalf("cross-tenant revisions = %+v error=%v", crossTenantRevisions.Items(), err)
	}
	crossTenantActivations, err := inspector.ListActivations(ctx, secondScope, policyID, 0, 10)
	if err != nil || len(crossTenantActivations.Items()) != 0 || crossTenantActivations.HasMore() {
		t.Fatalf("cross-tenant activations = %+v error=%v", crossTenantActivations.Items(), err)
	}

	if err := adminPool.WithinTransaction(
		ctx,
		idenqapostgres.TransactionOptions{},
		func(ctx context.Context, transaction idenqapostgres.Transaction) error {
			_, err := transaction.Exec(ctx,
				"UPDATE idenqa.policy_revisions SET canonical = canonical WHERE tenant_id = $1 AND policy_id = $2 AND revision = 1",
				firstTenant.String(), policyID.String(),
			)
			return err
		},
	); err == nil {
		t.Fatal("database allowed immutable revision update")
	}
	if err := adminPool.WithinTransaction(
		ctx,
		idenqapostgres.TransactionOptions{},
		func(ctx context.Context, transaction idenqapostgres.Transaction) error {
			_, err := transaction.Exec(ctx,
				"DELETE FROM idenqa.policy_activations WHERE tenant_id = $1 AND policy_id = $2",
				firstTenant.String(), policyID.String(),
			)
			return err
		},
	); err == nil {
		t.Fatal("database allowed activation history deletion")
	}
	var activationCount int64
	if err := runtimePool.WithinTransaction(
		ctx,
		idenqapostgres.TransactionOptions{ReadOnly: true},
		func(ctx context.Context, transaction idenqapostgres.Transaction) error {
			queries := sqlgen.New(transaction)
			if _, err := queries.SetTenantScope(ctx, firstTenant.String()); err != nil {
				return err
			}
			return transaction.QueryRow(ctx,
				"SELECT count(*) FROM idenqa.policy_activations WHERE tenant_id = $1 AND policy_id = $2",
				firstTenant.String(), policyID.String(),
			).Scan(&activationCount)
		},
	); err != nil {
		t.Fatal(err)
	}
	if activationCount != 2 {
		t.Fatalf("activation history count = %d, want 2", activationCount)
	}
}

func integrationPolicyCanonical(t *testing.T, policyID id.Policy, revision uint32) []byte {
	t.Helper()
	canonical, err := policyv1.Canonical(policyv1.Document{
		SchemaMajor: 1, SchemaMinor: 0, PolicyID: policyID.String(), Revision: revision,
		Rules: []policyv1.Rule{{
			Name: "terminal", When: `facts["document.authenticity"] == "satisfied"`,
			Result: policyv1.Result{
				State:     policyv1.RequirementNotSatisfied,
				Directive: policyv1.DirectiveCompleteNotVerified,
				Priority:  1, ContributingFacts: []string{"document.authenticity"},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}
