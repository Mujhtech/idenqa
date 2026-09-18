//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	bootstrapidenqa "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
	"github.com/go-chi/chi/v5"
)

func TestPolicyDecisionDurabilityIsolationLineageAndImmutability(t *testing.T) {
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
	now := time.Now().UTC().Truncate(time.Second)
	firstTenant, firstVerification := seedExecutionVerification(t, adminPool, generator, now)
	secondTenant, _ := seedExecutionVerification(t, adminPool, generator, now)
	authorTenant, authorVerification := seedExecutionVerification(t, adminPool, generator, now)
	transactionTenant, transactionVerification := seedExecutionVerification(t, adminPool, generator, now)

	runtimeConfig := poolConfig(database.url)
	runtimeRole := database.createRuntimeRole(t)
	runtimeConfig.Role = runtimeRole
	runtimePool, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimePool.Close()
	store, err := policypostgres.New(runtimePool, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	firstScope, _ := tenant.NewScope(firstTenant)
	secondScope, _ := tenant.NewScope(secondTenant)
	authorScope, _ := tenant.NewScope(authorTenant)
	transactionScope, _ := tenant.NewScope(transactionTenant)
	authorRequest, authorInput, authorEvaluator := integrationAuthorFixture(
		t,
		generator,
		authorVerification,
		now,
	)
	authorLoader := &integrationAuthorLoader{input: authorInput}
	author, err := policy.NewAuthor(store, authorLoader, authorEvaluator)
	if err != nil {
		t.Fatal(err)
	}
	authored, err := author.Author(ctx, authorScope, authorRequest)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := author.Author(ctx, authorScope, authorRequest)
	if err != nil || replayed.Digest() != authored.Digest() || authorLoader.calls != 1 || authorEvaluator.calls != 1 {
		t.Fatalf("author replay digest=%q loader=%d evaluator=%d error=%v",
			replayed.Digest(), authorLoader.calls, authorEvaluator.calls, err)
	}
	if _, err := store.Find(ctx, firstScope, authored.ID()); !errors.Is(err, policy.ErrDecisionNotFound) {
		t.Fatalf("authored decision crossed tenant scope: %v", err)
	}
	transactionDecision := newIntegrationDecision(
		t, generator, transactionTenant, transactionVerification, id.Decision{}, now,
	)
	rollback := errors.New("simulate lost task fence")
	err = runtimePool.WithinTransaction(
		ctx,
		idenqapostgres.TransactionOptions{},
		func(ctx context.Context, transaction idenqapostgres.Transaction) error {
			if err := store.AppendWithin(ctx, transactionScope, transaction, transactionDecision); err != nil {
				return err
			}
			found, err := store.FindWithin(
				ctx, transactionScope, transaction, transactionDecision.ID(),
			)
			if err != nil || found.Digest() != transactionDecision.Digest() {
				return errors.New("transactional decision was not visible inside its effect")
			}
			return rollback
		},
	)
	if !errors.Is(err, rollback) {
		t.Fatalf("transaction rollback error = %v", err)
	}
	if _, err := store.Find(ctx, transactionScope, transactionDecision.ID()); !errors.Is(err, policy.ErrDecisionNotFound) {
		t.Fatalf("rolled-back decision remained visible: %v", err)
	}
	err = runtimePool.WithinTransaction(
		ctx,
		idenqapostgres.TransactionOptions{},
		func(ctx context.Context, transaction idenqapostgres.Transaction) error {
			return store.AppendWithin(ctx, transactionScope, transaction, transactionDecision)
		},
	)
	if err != nil {
		t.Fatalf("transactional decision append: %v", err)
	}
	if found, err := store.Find(ctx, transactionScope, transactionDecision.ID()); err != nil || found.Digest() != transactionDecision.Digest() {
		t.Fatalf("committed transactional decision digest = %q, error = %v", found.Digest(), err)
	}
	accessStore, err := accesspostgres.New(runtimePool)
	if err != nil {
		t.Fatal(err)
	}
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{
		1: bytes.Repeat([]byte{0x71}, 32),
	})
	if err != nil {
		t.Fatal(err)
	}
	decisionPattern, err := access.ParsePattern("decisions:*")
	if err != nil {
		t.Fatal(err)
	}
	firstKey, firstCredential := newIntegrationCredentialKey(
		t, generator, firstTenant, now, peppers, decisionPattern,
	)
	if err := accessStore.Create(ctx, firstScope, firstKey); err != nil {
		t.Fatal(err)
	}
	authenticator, err := access.NewAuthenticator(accessStore, peppers, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	accessMiddleware, err := httpapi.NewAccessMiddleware(authenticator, logger)
	if err != nil {
		t.Fatal(err)
	}
	decisionReader, err := policy.NewReader(store)
	if err != nil {
		t.Fatal(err)
	}
	decisionRoutes, err := httpapi.NewDecisionRoutes(accessMiddleware, decisionReader, logger)
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	router.Route(httpapi.VersionPrefix, func(versioned chi.Router) {
		decisionRoutes.Register(versioned)
	})
	serveDecision := func(credential, path, etag string) *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+credential)
		if etag != "" {
			request.Header.Set("If-None-Match", etag)
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		return response
	}

	root := newIntegrationDecision(t, generator, firstTenant, firstVerification, id.Decision{}, now)
	if err := store.Append(ctx, firstScope, root); err != nil {
		t.Fatal(err)
	}
	if err := store.Append(ctx, firstScope, root); err != nil {
		t.Fatalf("exact replay error = %v", err)
	}
	found, err := store.Find(ctx, firstScope, root.ID())
	if err != nil || found.Digest() != root.Digest() {
		t.Fatalf("Find() digest = %q, error = %v", found.Digest(), err)
	}
	if _, err := store.Find(ctx, secondScope, root.ID()); !errors.Is(err, policy.ErrDecisionNotFound) {
		t.Fatalf("cross-tenant Find() error = %v", err)
	}
	exactResponse := serveDecision(
		firstCredential.Reveal(), "/v1/decisions/"+root.ID().String(), "",
	)
	if exactResponse.Code != http.StatusOK {
		t.Fatalf("decision HTTP status = %d body=%s", exactResponse.Code, exactResponse.Body)
	}
	var exactReport policy.ReproductionReport
	if err := json.Unmarshal(exactResponse.Body.Bytes(), &exactReport); err != nil ||
		exactReport.DecisionDigest != root.Digest() || !exactReport.Reproduced {
		t.Fatalf("decision HTTP report = %+v error=%v", exactReport, err)
	}
	exportResponse := serveDecision(
		firstCredential.Reveal(), "/v1/decisions/"+root.ID().String()+"/bundle", "",
	)
	expectedBundle, _, err := policy.NewDecisionBundle(root)
	if err != nil {
		t.Fatal(err)
	}
	if exportResponse.Code != http.StatusOK || !bytes.Equal(exportResponse.Body.Bytes(), expectedBundle.Canonical()) {
		t.Fatalf("decision export status=%d body changed=%t", exportResponse.Code,
			!bytes.Equal(exportResponse.Body.Bytes(), expectedBundle.Canonical()))
	}
	secondKey, secondCredential := newIntegrationCredentialKey(
		t, generator, secondTenant, now, peppers, decisionPattern,
	)
	if err := accessStore.Create(ctx, secondScope, secondKey); err != nil {
		t.Fatal(err)
	}
	crossTenantResponse := serveDecision(
		secondCredential.Reveal(), "/v1/decisions/"+root.ID().String(), "",
	)
	if crossTenantResponse.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant decision HTTP status = %d body=%s", crossTenantResponse.Code, crossTenantResponse.Body)
	}

	t.Setenv("IDENQA_ENVIRONMENT", "test")
	t.Setenv("IDENQA_DATABASE_URL", database.url)
	t.Setenv("IDENQA_DATABASE_ROLE", runtimeRole)
	var cliOutput, cliError bytes.Buffer
	code := bootstrapidenqa.Run([]string{
		"policy", "decision", "reproduce", "--env-file", "", "--tenant", firstTenant.String(),
		"--id", root.ID().String(), "--output", "bundle",
	}, &cliOutput, &cliError, buildinfo.Info{})
	if code != 0 {
		t.Fatalf("reproduction CLI exit code = %d, stderr = %q", code, cliError.String())
	}
	bundle, report, err := policy.RestoreDecisionBundle(cliOutput.Bytes())
	if err != nil || bundle.Decision().Digest() != root.Digest() || !report.Reproduced {
		t.Fatalf("reproduction bundle decision digest = %q, report = %+v, error = %v",
			bundle.Decision().Digest(), report, err)
	}
	bundleFile := filepath.Join(t.TempDir(), "decision.bundle.json")
	if err := os.WriteFile(bundleFile, cliOutput.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	cliOutput.Reset()
	cliError.Reset()
	code = bootstrapidenqa.Run([]string{
		"policy", "decision", "verify", "--bundle-file", bundleFile, "--output", "json",
	}, &cliOutput, &cliError, buildinfo.Info{})
	if code != 0 {
		t.Fatalf("offline verification CLI exit code = %d, stderr = %q", code, cliError.String())
	}
	var verified policy.ReproductionReport
	if err := json.Unmarshal(cliOutput.Bytes(), &verified); err != nil || verified != report {
		t.Fatalf("offline report = %+v, error = %v", verified, err)
	}

	changed := rebuildIntegrationDecision(t, generator, root, policy.ActorHuman, now.Add(time.Second))
	if err := store.Append(ctx, firstScope, changed); !errors.Is(err, policy.ErrDecisionConflict) {
		t.Fatalf("changed replay error = %v", err)
	}
	conflictingRoot := newIntegrationDecision(t, generator, firstTenant, firstVerification, id.Decision{}, now.Add(time.Second))
	if err := store.Append(ctx, firstScope, conflictingRoot); !errors.Is(err, policy.ErrDecisionConflict) {
		t.Fatalf("second root error = %v", err)
	}

	successors := []policy.Decision{
		newIntegrationDecision(t, generator, firstTenant, firstVerification, root.ID(), now.Add(2*time.Second)),
		newIntegrationDecision(t, generator, firstTenant, firstVerification, root.ID(), now.Add(3*time.Second)),
	}
	errorsBySuccessor := make([]error, len(successors))
	var wait sync.WaitGroup
	for index := range successors {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			errorsBySuccessor[index] = store.Append(ctx, firstScope, successors[index])
		}(index)
	}
	wait.Wait()
	successes, conflicts := 0, 0
	var winner policy.Decision
	for index, appendErr := range errorsBySuccessor {
		switch {
		case appendErr == nil:
			successes++
			winner = successors[index]
		case errors.Is(appendErr, policy.ErrDecisionConflict):
			conflicts++
		default:
			t.Fatalf("concurrent successor error = %v", appendErr)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("concurrent successors successes=%d conflicts=%d", successes, conflicts)
	}
	latest, err := store.FindLatest(ctx, firstScope, firstVerification)
	if err != nil || latest.ID().String() != winner.ID().String() || latest.Supersedes().String() != root.ID().String() {
		t.Fatalf("FindLatest() id=%s supersedes=%s error=%v", latest.ID(), latest.Supersedes(), err)
	}
	latestResponse := serveDecision(
		firstCredential.Reveal(), "/v1/verifications/"+firstVerification.String()+"/decision", "",
	)
	if latestResponse.Code != http.StatusOK {
		t.Fatalf("latest decision HTTP status = %d body=%s", latestResponse.Code, latestResponse.Body)
	}
	latestETag := latestResponse.Header().Get("ETag")
	notModified := serveDecision(
		firstCredential.Reveal(), "/v1/verifications/"+firstVerification.String()+"/decision", latestETag,
	)
	if notModified.Code != http.StatusNotModified || notModified.Body.Len() != 0 {
		t.Fatalf("latest conditional HTTP status=%d body=%q", notModified.Code, notModified.Body)
	}

	transaction, err := runtimePool.Native().Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback(t.Context()) }()
	if _, err := transaction.Exec(ctx, "SELECT set_config('idenqa.tenant_id', $1, true)", firstTenant.String()); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"UPDATE idenqa.policy_snapshots SET region = 'tampered' WHERE tenant_id = $1",
		"DELETE FROM idenqa.policy_evaluations WHERE tenant_id = $1",
		"UPDATE idenqa.verification_decisions SET actor = 'human' WHERE tenant_id = $1",
	} {
		if _, err := transaction.Exec(ctx, statement, firstTenant.String()); err == nil {
			t.Fatalf("append-only mutation succeeded: %s", statement)
		}
		if err := transaction.Rollback(ctx); err != nil {
			t.Fatal(err)
		}
		transaction, err = runtimePool.Native().Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := transaction.Exec(ctx, "SELECT set_config('idenqa.tenant_id', $1, true)", firstTenant.String()); err != nil {
			t.Fatal(err)
		}
	}
	if err := transaction.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var visibleWithoutScope int
	if err := runtimePool.Native().QueryRow(ctx, "SELECT count(*) FROM idenqa.verification_decisions").Scan(&visibleWithoutScope); err != nil {
		t.Fatal(err)
	}
	if visibleWithoutScope != 0 {
		t.Fatalf("decisions visible without tenant scope = %d", visibleWithoutScope)
	}
}

type integrationAuthorLoader struct {
	input policy.AuthorInput
	calls int
}

func (loader *integrationAuthorLoader) LoadPolicyInput(
	_ context.Context,
	_ tenant.Scope,
	_ id.Verification,
	_ time.Time,
) (policy.AuthorInput, error) {
	loader.calls++
	return loader.input, nil
}

type integrationAuthorEvaluator struct {
	reference policy.EvaluatorReference
	output    policy.EvaluatorOutput
	calls     int
}

func (evaluator *integrationAuthorEvaluator) Reference() policy.EvaluatorReference {
	return evaluator.reference
}

func (evaluator *integrationAuthorEvaluator) Evaluate(
	_ context.Context,
	_ policy.Snapshot,
) (policy.EvaluatorOutput, error) {
	evaluator.calls++
	return evaluator.output, nil
}

func integrationAuthorFixture(
	t testing.TB,
	generator *id.Generator,
	verificationID id.Verification,
	now time.Time,
) (policy.AuthorRequest, policy.AuthorInput, *integrationAuthorEvaluator) {
	t.Helper()
	decisionID, _ := generator.NewDecision()
	authorityID, _ := generator.NewAuthority()
	acknowledgementID, _ := generator.NewAcknowledgement()
	policyID, _ := generator.NewPolicy()
	checkID, _ := generator.NewCheck()
	attemptID, _ := generator.NewAttempt()
	observationID, _ := generator.NewObservation()
	factKey, _ := policy.NewFactKey("check.document_authenticity")
	evaluatorReference := policy.EvaluatorReference{
		Major: 1, Minor: 0, Digest: strings.Repeat("b", 64),
	}
	return policy.AuthorRequest{
		DecisionID: decisionID, VerificationID: verificationID,
		EvaluatedAt: now, DecidedAt: now.Add(time.Second),
	}, policy.AuthorInput{
		AuthorityID: authorityID, AcknowledgementID: acknowledgementID, Region: "tenant_home",
		Policy: policy.Reference{
			ID: policyID, Revision: 1, SchemaMajor: 1, SchemaMinor: 0,
			Digest: strings.Repeat("a", 64),
		},
		Facts: []policy.Fact{{
			Key: factKey, State: policy.RequirementSatisfied, ObservedAt: now.Add(-time.Second),
			ReasonCodes: []string{"document_authenticity_satisfied"},
			Source: policy.FactSource{Kind: policy.FactSourceCheck, Check: &policy.CheckSource{
				CheckID: checkID, CheckVersion: 1, AttemptID: attemptID,
				ObservationIDs: []id.Observation{observationID},
				ContractDigest: strings.Repeat("b", 64), ImplementationDigest: strings.Repeat("c", 64),
			}},
		}},
	}, &integrationAuthorEvaluator{
		reference: evaluatorReference,
		output: policy.EvaluatorOutput{
			Results: []policy.RequirementResult{{
				Name: "document_authenticity", State: policy.RequirementSatisfied,
				ContributingFacts: []policy.FactKey{factKey},
				Candidate:         policy.DirectiveCompleteVerified, Priority: 1,
				ReasonCodes: []string{"document_authenticity_satisfied"},
			}},
			Assurance: "global_individual_substantial.1",
		},
	}
}

func newIntegrationDecision(
	t testing.TB,
	generator *id.Generator,
	tenantID id.Tenant,
	verificationID id.Verification,
	supersedes id.Decision,
	decidedAt time.Time,
) policy.Decision {
	t.Helper()
	decisionID, _ := generator.NewDecision()
	return integrationDecision(t, generator, tenantID, verificationID, decisionID, supersedes, policy.ActorMachine, decidedAt)
}

func rebuildIntegrationDecision(
	t testing.TB,
	generator *id.Generator,
	base policy.Decision,
	actor policy.ActorClass,
	decidedAt time.Time,
) policy.Decision {
	t.Helper()
	return integrationDecision(
		t,
		generator,
		base.Snapshot().TenantID(),
		base.Snapshot().VerificationID(),
		base.ID(),
		base.Supersedes(),
		actor,
		decidedAt,
	)
}

func integrationDecision(
	t testing.TB,
	generator *id.Generator,
	tenantID id.Tenant,
	verificationID id.Verification,
	decisionID id.Decision,
	supersedes id.Decision,
	actor policy.ActorClass,
	decidedAt time.Time,
) policy.Decision {
	t.Helper()
	authorityID, _ := generator.NewAuthority()
	acknowledgementID, _ := generator.NewAcknowledgement()
	policyID, _ := generator.NewPolicy()
	checkID, _ := generator.NewCheck()
	attemptID, _ := generator.NewAttempt()
	observationID, _ := generator.NewObservation()
	factKey, _ := policy.NewFactKey("check.document_authenticity")
	snapshot, err := policy.NewSnapshot(policy.SnapshotInput{
		TenantID: tenantID, VerificationID: verificationID, AuthorityID: authorityID,
		AcknowledgementID: acknowledgementID, Region: "tenant_home",
		Policy: policy.Reference{ID: policyID, Revision: 1, SchemaMajor: 1, SchemaMinor: 0,
			Digest: strings.Repeat("a", 64)},
		Evaluator:   policy.EvaluatorReference{Major: 1, Minor: 0, Digest: strings.Repeat("b", 64)},
		EvaluatedAt: decidedAt.Add(-time.Second),
		Facts: []policy.Fact{{Key: factKey, State: policy.RequirementSatisfied,
			ObservedAt: decidedAt.Add(-2 * time.Second), ReasonCodes: []string{"document_authenticity_satisfied"},
			Source: policy.FactSource{Kind: policy.FactSourceCheck, Check: &policy.CheckSource{
				CheckID: checkID, CheckVersion: 1, AttemptID: attemptID,
				ObservationIDs: []id.Observation{observationID}, ContractDigest: strings.Repeat("b", 64),
				ImplementationDigest: strings.Repeat("c", 64),
			}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluation, err := policy.Resolve(snapshot, []policy.RequirementResult{{
		Name: "document_authenticity", State: policy.RequirementSatisfied,
		ContributingFacts: []policy.FactKey{factKey}, Candidate: policy.DirectiveCompleteVerified,
		Priority: 1, ReasonCodes: []string{"document_authenticity_satisfied"},
	}}, "global_individual_substantial.1")
	if err != nil {
		t.Fatal(err)
	}
	decision, err := policy.NewDecision(policy.DecisionInput{
		ID: decisionID, Snapshot: snapshot, Evaluation: evaluation,
		Actor: actor, Supersedes: supersedes, DecidedAt: decidedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return decision
}
