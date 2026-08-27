//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

func TestAPIKeyPersistenceIsolationAndLifecycle(t *testing.T) {
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
	tenantStore, err := tenantpostgres.New(adminPool)
	if err != nil {
		t.Fatalf("new tenant store: %v", err)
	}
	identifierGenerator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("new identifier generator: %v", err)
	}
	tenantAdmin, err := tenant.NewAdmin(tenantStore, identifierGenerator, clock.System{})
	if err != nil {
		t.Fatalf("new tenant admin: %v", err)
	}
	action := tenant.AdminAction{Actor: "integration-operator", Reason: "verify API key isolation"}
	firstTenant, err := tenantAdmin.Create(ctx, action)
	if err != nil {
		t.Fatalf("create first tenant: %v", err)
	}
	secondTenant, err := tenantAdmin.Create(ctx, action)
	if err != nil {
		t.Fatalf("create second tenant: %v", err)
	}

	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtimePool, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatalf("open runtime pool: %v", err)
	}
	defer runtimePool.Close()
	store, err := accesspostgres.New(runtimePool)
	if err != nil {
		t.Fatalf("new access store: %v", err)
	}
	runtimeTenantStore, err := tenantpostgres.New(runtimePool)
	if err != nil {
		t.Fatalf("new runtime tenant store: %v", err)
	}
	firstScope, err := tenant.NewScope(firstTenant.ID())
	if err != nil {
		t.Fatalf("new first scope: %v", err)
	}
	secondScope, err := tenant.NewScope(secondTenant.ID())
	if err != nil {
		t.Fatalf("new second scope: %v", err)
	}

	created := time.Now().UTC().Truncate(time.Microsecond)
	firstKey := newIntegrationKey(t, identifierGenerator, firstTenant.ID(), created, id.APIKey{})
	if err := store.Create(ctx, firstScope, firstKey); err != nil {
		t.Fatalf("create first key: %v", err)
	}
	found, err := store.Find(ctx, firstScope, firstKey.ID())
	if err != nil {
		t.Fatalf("find own key: %v", err)
	}
	if found.ID().String() != firstKey.ID().String() || !found.Grant().Allows(access.PermissionTenantRead) {
		t.Fatal("found key does not preserve identity and grant")
	}
	if _, err := store.FindAdministrative(ctx, firstScope, firstKey.ID()); err == nil {
		t.Fatal("runtime role performed an administrative key read")
	}
	if _, err := store.Find(ctx, secondScope, firstKey.ID()); !errors.Is(err, access.ErrKeyNotFound) {
		t.Fatalf("cross-tenant Find() error = %v, want ErrKeyNotFound", err)
	}
	if _, err := store.FindForVerification(ctx, firstTenant.ID(), firstKey.ID()); err != nil {
		t.Fatalf("verification lookup: %v", err)
	}
	if _, err := store.FindForVerification(ctx, secondTenant.ID(), firstKey.ID()); !errors.Is(err, access.ErrKeyNotFound) {
		t.Fatalf("wrong-hint verification error = %v, want ErrKeyNotFound", err)
	}

	err = runtimePool.WithinTransaction(ctx, idenqapostgres.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		var count int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM idenqa.api_keys").Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatalf("missing-scope API-key count = %d, want 0", count)
		}

		return nil
	})
	if err != nil {
		t.Fatalf("missing-scope API-key read: %v", err)
	}

	expectedVersion := found.Version()
	scheduledAt := created.Add(time.Minute)
	retirementAt := scheduledAt.Add(10 * time.Minute)
	successor := newIntegrationKey(t, identifierGenerator, firstTenant.ID(), scheduledAt, firstKey.ID())
	if err := found.ScheduleRetirement(scheduledAt, retirementAt); err != nil {
		t.Fatalf("schedule predecessor retirement: %v", err)
	}
	if err := store.Rotate(ctx, firstScope, found, expectedVersion, successor); err != nil {
		t.Fatalf("persist rotation: %v", err)
	}
	scheduled, err := store.Find(ctx, firstScope, firstKey.ID())
	if err != nil {
		t.Fatalf("find scheduled predecessor: %v", err)
	}
	if !scheduled.UsableAt(retirementAt.Add(-time.Nanosecond)) || scheduled.StateAt(retirementAt) != access.KeyStateRetired {
		t.Fatalf("scheduled lifecycle before/at deadline = %q/%q", scheduled.StateAt(retirementAt.Add(-time.Nanosecond)), scheduled.StateAt(retirementAt))
	}
	rotated, err := store.Find(ctx, firstScope, successor.ID())
	if err != nil {
		t.Fatalf("find rotation successor: %v", err)
	}
	if rotated.ReplacesID().String() != firstKey.ID().String() {
		t.Fatalf("rotation predecessor = %q, want %q", rotated.ReplacesID(), firstKey.ID())
	}

	expectedVersion = scheduled.Version()
	revokedAt := scheduledAt.Add(time.Minute)
	if err := scheduled.Revoke(revokedAt); err != nil {
		t.Fatalf("revoke aggregate: %v", err)
	}
	if err := store.SaveLifecycle(ctx, firstScope, scheduled, expectedVersion); err != nil {
		t.Fatalf("save revocation: %v", err)
	}
	if err := store.SaveLifecycle(ctx, firstScope, scheduled, expectedVersion); !errors.Is(err, access.ErrKeyConflict) {
		t.Fatalf("stale lifecycle save error = %v, want ErrKeyConflict", err)
	}
	revoked, err := store.Find(ctx, firstScope, firstKey.ID())
	if err != nil {
		t.Fatalf("find revoked key: %v", err)
	}
	if revoked.StateAt(revokedAt) != access.KeyStateRevoked || revoked.Version() != expectedVersion+1 {
		t.Fatalf("persisted lifecycle = state %q version %d", revoked.StateAt(revokedAt), revoked.Version())
	}

	secondKey := newIntegrationKey(t, identifierGenerator, secondTenant.ID(), created, id.APIKey{})
	if err := store.Create(ctx, firstScope, secondKey); err == nil {
		t.Fatal("cross-tenant Create() error = nil")
	}

	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{
		1: bytes.Repeat([]byte{0x31}, 32),
	})
	if err != nil {
		t.Fatalf("new pepper set: %v", err)
	}
	authenticationKey, credential := newIntegrationCredentialKey(
		t, identifierGenerator, firstTenant.ID(), time.Now().UTC(), peppers,
	)
	if err := store.Create(ctx, firstScope, authenticationKey); err != nil {
		t.Fatalf("create authentication key: %v", err)
	}
	authenticator, err := access.NewAuthenticator(store, peppers, clock.System{})
	if err != nil {
		t.Fatalf("new authenticator: %v", err)
	}
	authenticated, err := authenticator.Authenticate(ctx, credential.Reveal())
	if err != nil {
		t.Fatalf("authenticate active tenant key: %v", err)
	}
	if authenticated.TenantScope().ID().String() != firstTenant.ID().String() ||
		authenticated.Principal().KeyID().String() != authenticationKey.ID().String() {
		t.Fatal("authentication produced incorrect authority")
	}
	accessMiddleware, err := httpapi.NewAccessMiddleware(
		authenticator,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("new HTTP access middleware: %v", err)
	}
	tenantReader, err := access.NewTenantReader(runtimeTenantStore)
	if err != nil {
		t.Fatalf("new tenant reader: %v", err)
	}
	tenantRoutes, err := httpapi.NewTenantRoutes(
		accessMiddleware,
		tenantReader,
		slog.New(slog.NewJSONHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("new tenant routes: %v", err)
	}
	router := chi.NewRouter()
	tenantRoutes.Register(router)
	router.With(accessMiddleware.Authenticate, accessMiddleware.Require(access.PermissionTenantRead)).Get(
		"/tenants/{tenantID}",
		func(writer http.ResponseWriter, request *http.Request) {
			accessContext, ok := httpapi.AccessContext(request.Context())
			if !ok {
				t.Fatal("protected HTTP handler has no access context")
			}
			if err := accessContext.Require(access.PermissionTenantRead); err != nil {
				writeIntegrationProblem(t, writer, request, err)

				return
			}
			target, err := id.ParseTenant(chi.URLParam(request, "tenantID"))
			if err != nil || target.String() != accessContext.TenantScope().ID().String() {
				writeIntegrationProblem(t, writer, request, tenant.ErrNotFound)

				return
			}
			respond.NoContent(writer)
		},
	)
	serveAuthenticatedRequest := func(target id.Tenant) *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(
			ctx,
			http.MethodGet,
			"/tenants/"+target.String(),
			nil,
		)
		request.Header.Set("Authorization", "Bearer "+credential.Reveal())
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		return response
	}
	serveCurrentTenantRequest := func() *httptest.ResponseRecorder {
		request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/v1/tenant", nil)
		request.Header.Set("Authorization", "Bearer "+credential.Reveal())
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)

		return response
	}
	if response := serveCurrentTenantRequest(); response.Code != http.StatusOK {
		t.Fatalf("real tenant-read HTTP status = %d, want %d; body=%s", response.Code, http.StatusOK, response.Body)
	}
	if response := serveAuthenticatedRequest(firstTenant.ID()); response.Code != http.StatusNoContent {
		t.Fatalf("active-tenant HTTP status = %d, want %d; body=%s", response.Code, http.StatusNoContent, response.Body)
	}
	if response := serveAuthenticatedRequest(secondTenant.ID()); response.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant HTTP status = %d, want %d; body=%s", response.Code, http.StatusNotFound, response.Body)
	}
	if _, err := tenantAdmin.Disable(ctx, action, firstTenant.ID(), firstTenant.Version()); err != nil {
		t.Fatalf("disable authenticated tenant: %v", err)
	}
	if _, err := authenticator.Authenticate(ctx, credential.Reveal()); !errors.Is(err, access.ErrInvalidCredential) {
		t.Fatalf("disabled-tenant authentication error = %v, want ErrInvalidCredential", err)
	}
	if response := serveAuthenticatedRequest(firstTenant.ID()); response.Code != http.StatusUnauthorized {
		t.Fatalf("disabled-tenant HTTP status = %d, want %d; body=%s", response.Code, http.StatusUnauthorized, response.Body)
	}
}

func newIntegrationKey(
	t *testing.T,
	generator *id.Generator,
	tenantID id.Tenant,
	created time.Time,
	replacesID id.APIKey,
) access.Key {
	t.Helper()

	identifier, err := generator.NewAPIKey()
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}
	digest, err := access.ParseDigest(bytes.Repeat([]byte{9}, 32))
	if err != nil {
		t.Fatalf("ParseDigest() error = %v", err)
	}
	pattern, err := access.ParsePattern("tenant:read")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	grant, err := access.TenantRegistry().Resolve(pattern)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	key, err := access.RestoreKey(access.KeyRecord{
		ID: identifier, TenantID: tenantID, Label: "integration backend",
		Digest: digest, PepperVersion: 1, Grant: grant, Version: 1,
		CreatedAt: created, UpdatedAt: created, ReplacesID: replacesID,
	})
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}

	return key
}

func newIntegrationCredentialKey(
	t *testing.T,
	generator *id.Generator,
	tenantID id.Tenant,
	created time.Time,
	peppers *access.PepperSet,
	patterns ...access.Pattern,
) (access.Key, access.PresentedKey) {
	t.Helper()

	identifier, err := generator.NewAPIKey()
	if err != nil {
		t.Fatalf("NewAPIKey() error = %v", err)
	}
	secretGenerator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0x41}, 32)))
	if err != nil {
		t.Fatalf("NewKeyGenerator() error = %v", err)
	}
	presented, err := secretGenerator.Generate(tenantID, identifier)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	digest, version, err := peppers.Digest(presented)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	if len(patterns) == 0 {
		pattern, err := access.ParsePattern("tenant:read")
		if err != nil {
			t.Fatalf("ParsePattern() error = %v", err)
		}
		patterns = []access.Pattern{pattern}
	}
	grant, err := access.TenantRegistry().Resolve(patterns...)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	key, err := access.RestoreKey(access.KeyRecord{
		ID: identifier, TenantID: tenantID, Label: "authenticated integration backend",
		Digest: digest, PepperVersion: version, Grant: grant, Version: 1,
		CreatedAt: created, UpdatedAt: created,
	})
	if err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}

	return key, presented
}

func writeIntegrationProblem(t *testing.T, writer http.ResponseWriter, request *http.Request, err error) {
	t.Helper()

	if writeErr := respond.WriteProblem(writer, request, err, ""); writeErr != nil {
		t.Fatalf("write integration problem: %v", writeErr)
	}
}
