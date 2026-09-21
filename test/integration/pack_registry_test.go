//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/pack"
	packpostgres "github.com/Mujhtech/idenqa/internal/pack/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
	"github.com/go-chi/chi/v5"
)

// packAccessRepository authenticates one in-memory fixture key. Pack routes are
// global reads, so the lifecycle proof does not need durable API keys.
type packAccessRepository struct {
	record access.VerificationRecord
}

func (repository packAccessRepository) FindForVerification(
	context.Context,
	id.Tenant,
	id.APIKey,
) (access.VerificationRecord, error) {
	return repository.record, nil
}

func migratePackDatabase(t *testing.T) *idenqapostgres.Pool {
	t.Helper()
	database := createIsolatedDatabase(t)
	migrator, err := idenqapostgres.OpenMigrator(t.Context(), migrationConfig(database.url))
	if err != nil {
		t.Fatalf("OpenMigrator() error = %v", err)
	}
	if _, err := migrator.Up(t.Context()); err != nil {
		t.Fatalf("Up() error = %v", err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatalf("close migrator: %v", err)
	}
	pool, err := idenqapostgres.Open(t.Context(), poolConfig(database.url))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Check(t.Context(), migrations.LatestVersion); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	return pool
}

func draftRevision(t *testing.T, base pack.Pack) pack.Pack {
	t.Helper()
	draft := base.Clone()
	draft.Revision = base.Revision + 1
	draft.Lifecycle = pack.LifecycleDraft
	passport, ok := draft.Document(pack.DocumentTypePassport)
	if !ok {
		t.Fatal("seed pack has no passport document")
	}
	passport.KnownLimitations = append(passport.KnownLimitations, "authority_confirmation_pending")
	for index := range draft.Documents {
		if draft.Documents[index].Type == pack.DocumentTypePassport {
			draft.Documents[index] = passport
		}
	}
	canonical, err := pack.Canonicalize(draft)
	if err != nil {
		t.Fatalf("Canonicalize(draft) error = %v", err)
	}
	return canonical
}

func integrationPackRegistry(t *testing.T, pool *idenqapostgres.Pool, extra ...pack.Pack) *pack.Registry {
	t.Helper()
	store, err := packpostgres.New(pool)
	if err != nil {
		t.Fatalf("packpostgres.New() error = %v", err)
	}
	seeds, err := pack.Seeds()
	if err != nil {
		t.Fatalf("Seeds() error = %v", err)
	}
	registry, err := pack.NewRegistry(append(seeds, extra...), store, clock.System{}.Now)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	if err := registry.Load(t.Context()); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	return registry
}

// TestPackRegistryLifecyclePersistsAndProjectsSupport migrates a clean
// installation, persists one expected-version activation with immutable
// history, restores it in a replacement registry, rejects a mismatched digest,
// and proves the public support projection matches the active pack and reports
// legal review honestly.
func TestPackRegistryLifecyclePersistsAndProjectsSupport(t *testing.T) {
	pool := migratePackDatabase(t)
	seeds, err := pack.Seeds()
	if err != nil {
		t.Fatal(err)
	}
	var ng pack.Pack
	for _, seed := range seeds {
		if seed.Country == "NG" {
			ng = seed
		}
	}
	draft := draftRevision(t, ng)

	registry := integrationPackRegistry(t, pool, draft)
	if active, err := registry.Active("NG"); err != nil || active.Pack.Revision != 1 || active.Version != 0 {
		t.Fatalf("initial active = %+v, err = %v", active, err)
	}
	if _, err := registry.Activate(t.Context(), pack.Transition{
		Country: "NG", Revision: draft.Revision, ExpectedVersion: 0, Reason: "reviewed_revision", Actor: "integration",
	}); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if _, err := registry.Activate(t.Context(), pack.Transition{
		Country: "NG", Revision: draft.Revision, ExpectedVersion: 0, Reason: "stale", Actor: "integration",
	}); !errors.Is(err, pack.ErrConflict) {
		t.Fatalf("stale Activate() error = %v, want ErrConflict", err)
	}

	replacement := integrationPackRegistry(t, pool, draft)
	active, err := replacement.Active("NG")
	if err != nil || active.Pack.Revision != draft.Revision || active.State != pack.LifecycleActive || active.Version != 1 {
		t.Fatalf("restored active = %+v, err = %v", active, err)
	}
	history := replacement.History("NG")
	if len(history) != 2 || history[0].Operation != pack.OperationDeprecate || history[1].Operation != pack.OperationActivate {
		t.Fatalf("restored history = %+v", history)
	}
	if _, err := pool.Native().Exec(t.Context(),
		`UPDATE idenqa.pack_release_history SET reason = 'tampered' WHERE country = 'NG'`); err == nil {
		t.Fatal("append-only pack history accepted an update")
	}
	store, err := packpostgres.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Apply(t.Context(), []pack.Change{{
		Country: "NG", Revision: draft.Revision, TransitionVersion: 2,
		Operation: pack.OperationDeprecate, PreviousState: pack.LifecycleActive, State: pack.LifecycleDeprecated,
		PackDigest: strings.Repeat("f", 64), Reason: "mismatch", Actor: "integration", RecordedAt: time.Now().UTC(),
	}}); !errors.Is(err, packpostgres.ErrDigestMismatch) {
		t.Fatalf("mismatched digest error = %v, want ErrDigestMismatch", err)
	}

	projection, ok := replacement.Support("NG", "passport")
	if !ok {
		t.Fatal("Support() did not resolve the active passport")
	}
	if projection.PackRevision != draft.Revision || projection.LegalReview.State != pack.LegalReviewNotReviewed ||
		projection.SupportLevel != pack.SupportStructurallySupported {
		t.Fatalf("projection = %+v", projection)
	}

	fixture := newPackHTTPFixture(t, replacement)
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/document-support?country=NG&type=passport", nil)
	request.Header.Set("Authorization", "Bearer "+fixture.credential)
	response := httptest.NewRecorder()
	fixture.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("document-support status = %d, body = %s", response.Code, response.Body.String())
	}
	var served pack.SupportProjection
	if err := json.Unmarshal(response.Body.Bytes(), &served); err != nil {
		t.Fatalf("document-support body = %s: %v", response.Body.String(), err)
	}
	if !reflect.DeepEqual(served, projection) {
		t.Fatalf("document-support projection = %+v, want %+v", served, projection)
	}
	if !strings.Contains(response.Body.String(), `"legal_review":{"state":"not_reviewed"}`) {
		t.Fatalf("document-support did not mark unreviewed legal state honestly: %s", response.Body.String())
	}
}

type packHTTPFixture struct {
	handler    http.Handler
	credential string
}

func newPackHTTPFixture(t *testing.T, registry *pack.Registry) packHTTPFixture {
	t.Helper()
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x6c}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	generator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0x6d}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	tenantID, err := identifiers.NewTenant()
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := identifiers.NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	presented, err := generator.Generate(tenantID, keyID)
	if err != nil {
		t.Fatal(err)
	}
	digest, pepperVersion, err := peppers.Digest(presented)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := access.TenantRegistry().Resolve(access.Pattern("packs:read"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	key, err := access.RestoreKey(access.KeyRecord{
		ID: keyID, TenantID: tenantID, Label: "pack integration",
		Digest: digest, PepperVersion: pepperVersion, Grant: grant, Version: 1,
		CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := access.NewVerificationRecord(key, tenant.StateActive)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := access.NewAuthenticator(packAccessRepository{record: record}, peppers, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	middleware, err := httpapi.NewAccessMiddleware(authenticator, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	routes, err := httpapi.NewPackRoutes(middleware, registry, slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatal(err)
	}
	router := chi.NewRouter()
	routes.Register(router)
	return packHTTPFixture{handler: router, credential: presented.Reveal()}
}
