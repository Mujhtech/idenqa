//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	accesspostgres "github.com/Mujhtech/idenqa/internal/access/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/sqlgen"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

// providerRegistrationFixture seeds one full-scope actor and the tenant-scoped
// registration service over the fixture runtime role.
const providerRegistrationRegion = "tenant.region.ng"

func providerRegistrationFixture(t *testing.T, f captureAcceptanceFixture) (*provider.RegistrationManagement, *providerpostgres.RegistrationStore, access.Context, map[string]providerv1.Manifest) {
	t.Helper()
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x63}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	accessStore, err := accesspostgres.New(f.runtime)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := access.NewAuthenticator(accessStore, peppers, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	key, presented := newFullScopeIntegrationCredential(t, f.ids, f.scope.ID(), f.now, peppers)
	if err := accessStore.Create(t.Context(), f.scope, key); err != nil {
		t.Fatal(err)
	}
	actor, err := authenticator.Authenticate(t.Context(), presented.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerpostgres.NewRegistrationStore(f.runtime, fixedIntegrationClock{now: f.now})
	if err != nil {
		t.Fatal(err)
	}
	manifest := dojah.Description()
	service, err := provider.NewRegistrationManagement(store, f.ids, func() time.Time { return f.now }, time.Hour, map[string]providerv1.Manifest{manifest.Package.AdapterID: manifest})
	if err != nil {
		t.Fatal(err)
	}
	return service, store, actor, map[string]providerv1.Manifest{manifest.Package.AdapterID: manifest}
}

func providerRegistrationWrite(t *testing.T, manifest providerv1.Manifest, providerID, secret string) provider.RegistrationWrite {
	t.Helper()
	reference := providerv1.ConfigurationReference{ProviderID: providerID, SchemaDigest: manifest.Configuration.Digest, SecretReference: secret, CredentialVersion: "v1"}
	if reference.Validate() != nil {
		t.Fatal("fixture configuration is invalid")
	}
	return provider.RegistrationWrite{AdapterID: manifest.Package.AdapterID, Region: providerRegistrationRegion, Configuration: reference}
}

func providerRegistrationBinding(f captureAcceptanceFixture, configuration providerv1.ConfigurationReference) provider.Binding {
	region := providerRegistrationRegion
	if regions := f.declaration.Record().Regions; len(regions) > 0 {
		region = regions[0]
	}
	return provider.Binding{TenantID: f.scope.ID().String(), PolicyID: f.creation.Session.PolicyID().String(), ProfileDigest: f.creation.Session.ProfileDigest(),
		Requirement: f.creation.Session.Requirements().Requirements[0].Key, Region: region, Purpose: "idenqa.purpose.identity_verification",
		Recipient: "tenant.recipient.primary", Configuration: configuration}
}

// persistSelectedProviderRequest persists one prepared-style provider request
// for the exact selected plan and returns the stored envelope. It deliberately
// exercises the same request store, digest pin and attempt binding that
// preparation uses; evidence redemption itself is covered by the provider
// runtime suite.
func persistSelectedProviderRequest(t *testing.T, f captureAcceptanceFixture, plan *provider.Plan, key string) providerv1.Request {
	t.Helper()
	checkID, err := f.ids.NewCheck()
	if err != nil {
		t.Fatal(err)
	}
	check, err := verification.NewCheck(checkID, f.scope.ID(), f.creation.Session.ID(), plan.Capability.Check, f.now)
	if err != nil {
		t.Fatal(err)
	}
	checkStore, err := verificationpostgres.NewCheckStore(f.runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	event, err := f.ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkStore.CreateCheck(t.Context(), f.scope, check, event); err != nil {
		t.Fatal(err)
	}
	attemptID, err := f.ids.NewAttempt()
	if err != nil {
		t.Fatal(err)
	}
	grantID, err := f.ids.NewGrant()
	if err != nil {
		t.Fatal(err)
	}
	redemptionID, err := f.ids.NewRedemption()
	if err != nil {
		t.Fatal(err)
	}
	evidenceID, err := f.ids.NewEvidence()
	if err != nil {
		t.Fatal(err)
	}
	deadline := f.now.Add(time.Minute)
	request := providerv1.Request{Contract: plan.Manifest.Package.Contract, AttemptID: attemptID.String(), ProviderID: plan.Binding.Configuration.ProviderID,
		TenantID: f.scope.ID().String(), VerificationID: f.creation.Session.ID().String(), Check: plan.Capability.Check, IdempotencyKey: key,
		Adapter: plan.Manifest.Package, Capability: plan.Capability, Restrictions: plan.Manifest.Restrictions, Configuration: plan.Binding.Configuration,
		Inputs: plan.Binding.Inputs, Deadline: deadline,
		Evidence: []providerv1.EvidenceGrantReference{{GrantID: grantID.String(), RedemptionID: redemptionID.String(), EvidenceID: evidenceID.String(), Purpose: plan.Binding.Purpose, Variant: "document.front", ExpiresAt: deadline}}}
	digest, err := provider.RequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	attempt := verification.Attempt{ID: attemptID, Number: 1, Fence: 1, RunnerKind: verification.RunnerProvider, State: verification.AttemptRunning,
		Provenance: verification.Provenance{RunnerID: plan.Manifest.Package.AdapterID, RunnerVersion: plan.Manifest.Package.AdapterVersion,
			PackageDigest: digest, ContractMajor: plan.Manifest.Package.Contract.Major, ContractMinor: plan.Manifest.Package.Contract.Minor,
			RequestDigest: digest, Configuration: plan.ConfigurationDigest()}, StartedAt: f.now, Deadline: deadline}
	if err := check.BeginAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	event, err = f.ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkStore.SaveCheck(t.Context(), f.scope, verification.CheckCommit{Check: check, ExpectedVersion: 1, EventID: event}); err != nil {
		t.Fatal(err)
	}
	requests, err := providerpostgres.NewRequestStore(f.runtime, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.runtime.WithinTransaction(t.Context(), pg.TransactionOptions{}, func(ctx context.Context, tx pg.Transaction) error {
		if _, err := sqlgen.New(tx).SetTenantScope(ctx, f.scope.ID().String()); err != nil {
			return err
		}
		return requests.SaveWithin(ctx, tx, check, request)
	}); err != nil {
		t.Fatal(err)
	}
	return request
}

// TestProviderRegistrationSelectionPinsPersistedRequest proves the enabled
// tenant registration selects its own configuration and that the persisted
// provider_requests row pins the registered adapter, configuration and digest.
func TestProviderRegistrationSelectionPinsPersistedRequest(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		service, store, actor, manifests := providerRegistrationFixture(t, f)
		manifest := manifests["dojah"]
		deployedID, err := f.ids.NewProvider()
		if err != nil {
			t.Fatal(err)
		}
		deploymentConfiguration := providerRegistrationWrite(t, manifest, deployedID.String(), "secret://provider/dojah/deployment").Configuration
		registeredID, err := f.ids.NewProvider()
		if err != nil {
			t.Fatal(err)
		}
		write := providerRegistrationWrite(t, manifest, registeredID.String(), "secret://provider/dojah/tenant")
		created, err := service.Execute(t.Context(), actor, "register-key", provider.RegistrationCommand{Operation: "create", Reason: "onboarding", Write: &write})
		if err != nil {
			t.Fatal(err)
		}
		if created.Registration.Enabled || created.Registration.Version != 1 {
			t.Fatalf("created registration = %+v", created.Registration)
		}
		replayed, err := service.Execute(t.Context(), actor, "register-key", provider.RegistrationCommand{Operation: "create", Reason: "onboarding", Write: &write})
		if err != nil || !replayed.Replayed || replayed.Registration.ID != created.Registration.ID {
			t.Fatalf("replay = %+v, %v", replayed, err)
		}
		bad := write
		bad.Configuration.CredentialVersion = "my_api_key"
		if _, err := service.Execute(t.Context(), actor, "bad-key", provider.RegistrationCommand{Operation: "create", Reason: "onboarding", Write: &bad}); !errors.Is(err, provider.ErrRegistrationInvalid) {
			t.Fatalf("credential-like material accepted: %v", err)
		}
		if _, err := service.Execute(t.Context(), actor, "", provider.RegistrationCommand{Operation: "enable", RegistrationID: created.Registration.ID, ExpectedVersion: 1, Reason: "enable"}); err != nil {
			t.Fatal(err)
		}
		binding := providerRegistrationBinding(f, deploymentConfiguration)
		deploymentPlan, err := provider.NewPlan(binding, manifest)
		if err != nil {
			t.Fatal(err)
		}
		registrations, err := store.Enabled(t.Context(), f.scope.ID(), "dojah", binding.Region)
		if err != nil || len(registrations) != 1 {
			t.Fatalf("enabled registrations = %d, %v", len(registrations), err)
		}
		selected, ok := provider.Selected(registrations, "dojah", binding.Region)
		if !ok || selected.Configuration.ProviderID != registeredID.String() {
			t.Fatalf("selection = %+v, %v", selected, ok)
		}
		registeredPlan, err := provider.NewRegisteredPlan(selected, binding, manifest)
		if err != nil {
			t.Fatal(err)
		}
		if registeredPlan.ConfigurationDigest() == deploymentPlan.ConfigurationDigest() {
			t.Fatal("registered plan did not change the pinned digest")
		}
		request := persistSelectedProviderRequest(t, f, registeredPlan, "registered-attempt")
		var body []byte
		var digest, configuration string
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT requests.request_body, requests.request_digest, attempts.configuration_digest FROM idenqa.provider_requests AS requests JOIN idenqa.verification_attempts AS attempts ON attempts.tenant_id=requests.tenant_id AND attempts.id=requests.attempt_id WHERE requests.tenant_id=$1 AND requests.attempt_id=$2`, f.scope.ID().String(), request.AttemptID).Scan(&body, &digest, &configuration); err != nil {
			t.Fatal(err)
		}
		var stored providerv1.Request
		if json.Unmarshal(body, &stored) != nil || stored.Validate() != nil {
			t.Fatal("persisted request envelope is invalid")
		}
		if stored.Configuration.ProviderID != registeredID.String() || stored.Adapter.AdapterID != "dojah" {
			t.Fatalf("persisted request did not pin the registered configuration: %+v", stored.Configuration)
		}
		if configuration != registeredPlan.ConfigurationDigest() || digest != requestDigestOf(t, stored) {
			t.Fatalf("persisted pins = %s/%s", configuration, digest)
		}
		// Cross-tenant isolation.
		other, err := f.ids.NewTenant()
		if err != nil {
			t.Fatal(err)
		}
		otherScope, err := tenant.NewScope(other)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.Get(t.Context(), otherScope, created.Registration.ID); !errors.Is(err, provider.ErrRegistrationNotFound) {
			t.Fatalf("cross-tenant read = %v", err)
		}
		if visible, err := store.Enabled(t.Context(), other, "dojah", binding.Region); err != nil || len(visible) != 0 {
			t.Fatalf("cross-tenant selection = %d, %v", len(visible), err)
		}
	})
}

// TestProviderRegistrationDisableFallsBackToDeployment proves that disabling
// the tenant registration restores the deployment-configured provider route.
func TestProviderRegistrationDisableFallsBackToDeployment(t *testing.T) {
	runAuthorityPersistence(t, func(f captureAcceptanceFixture) {
		service, store, actor, manifests := providerRegistrationFixture(t, f)
		manifest := manifests["dojah"]
		deployedID, err := f.ids.NewProvider()
		if err != nil {
			t.Fatal(err)
		}
		deploymentConfiguration := providerRegistrationWrite(t, manifest, deployedID.String(), "secret://provider/dojah/deployment").Configuration
		registeredID, err := f.ids.NewProvider()
		if err != nil {
			t.Fatal(err)
		}
		write := providerRegistrationWrite(t, manifest, registeredID.String(), "secret://provider/dojah/tenant")
		created, err := service.Execute(t.Context(), actor, "register-key", provider.RegistrationCommand{Operation: "create", Reason: "onboarding", Write: &write})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.Execute(t.Context(), actor, "", provider.RegistrationCommand{Operation: "enable", RegistrationID: created.Registration.ID, ExpectedVersion: 1, Reason: "enable"}); err != nil {
			t.Fatal(err)
		}
		if _, err := service.Execute(t.Context(), actor, "", provider.RegistrationCommand{Operation: "disable", RegistrationID: created.Registration.ID, ExpectedVersion: 2, Reason: "disable"}); err != nil {
			t.Fatal(err)
		}
		binding := providerRegistrationBinding(f, deploymentConfiguration)
		deploymentPlan, err := provider.NewPlan(binding, manifest)
		if err != nil {
			t.Fatal(err)
		}
		registrations, err := store.Enabled(t.Context(), f.scope.ID(), "dojah", binding.Region)
		if err != nil || len(registrations) != 0 {
			t.Fatalf("disabled registration still selected: %d, %v", len(registrations), err)
		}
		if _, ok := provider.Selected(registrations, "dojah", binding.Region); ok {
			t.Fatal("disabled registration selected")
		}
		request := persistSelectedProviderRequest(t, f, deploymentPlan, "fallback-attempt")
		var body []byte
		if err := f.admin.Native().QueryRow(t.Context(), `SELECT request_body FROM idenqa.provider_requests WHERE tenant_id=$1 AND attempt_id=$2`, f.scope.ID().String(), request.AttemptID).Scan(&body); err != nil {
			t.Fatal(err)
		}
		var stored providerv1.Request
		if json.Unmarshal(body, &stored) != nil {
			t.Fatal("persisted fallback request is invalid")
		}
		if stored.Configuration.ProviderID != deployedID.String() {
			t.Fatalf("fallback request = %+v", stored.Configuration)
		}
	})
}

// TestProviderRegistrationStoreLifecycleIsolationAndHealth proves the tenant
// store lifecycle, unique enabled scope, append-only history, RLS, versioning
// and the bounded health read over the tenant's own dispatch records.
func TestProviderRegistrationStoreLifecycleIsolationAndHealth(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := pg.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := pg.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	tenantID, verificationID := seedExecutionVerification(t, admin, ids, now)
	otherID, _ := seedExecutionVerification(t, admin, ids, now)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	otherScope, err := tenant.NewScope(otherID)
	if err != nil {
		t.Fatal(err)
	}
	cfg := poolConfig(database.url)
	cfg.Role = database.createRuntimeRole(t)
	runtime, err := pg.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x65}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	accessStore, err := accesspostgres.New(runtime)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := access.NewAuthenticator(accessStore, peppers, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	key, presented := newFullScopeIntegrationCredential(t, ids, tenantID, now, peppers)
	if err := accessStore.Create(ctx, scope, key); err != nil {
		t.Fatal(err)
	}
	actor, err := authenticator.Authenticate(ctx, presented.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	store, err := providerpostgres.NewRegistrationStore(runtime, clock.System{})
	if err != nil {
		t.Fatal(err)
	}
	manifest := dojah.Description()
	service, err := provider.NewRegistrationManagement(store, ids, func() time.Time { return now }, time.Hour, map[string]providerv1.Manifest{"dojah": manifest})
	if err != nil {
		t.Fatal(err)
	}
	write := providerRegistrationWrite(t, manifest, "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", "secret://provider/dojah/tenant")
	created, err := service.Execute(ctx, actor, "register-key", provider.RegistrationCommand{Operation: "create", Reason: "onboarding", Write: &write})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Execute(ctx, actor, "", provider.RegistrationCommand{Operation: "enable", RegistrationID: created.Registration.ID, ExpectedVersion: 1, Reason: "enable"}); err != nil {
		t.Fatal(err)
	}
	// A second enabled registration in the same tenant/adapter/region violates
	// the unique enabled scope; the deployment route remains the fallback.
	second, err := service.Execute(ctx, actor, "second-key", provider.RegistrationCommand{Operation: "create", Reason: "onboarding", Write: &write})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Execute(ctx, actor, "", provider.RegistrationCommand{Operation: "enable", RegistrationID: second.Registration.ID, ExpectedVersion: 1, Reason: "enable"}); !errors.Is(err, provider.ErrRegistrationConflict) {
		t.Fatalf("duplicate enabled scope = %v", err)
	}
	// Version-checked update and stale rejection.
	updatedWrite := providerRegistrationWrite(t, manifest, "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWK", "secret://provider/dojah/rotated")
	updated, err := service.Execute(ctx, actor, "", provider.RegistrationCommand{Operation: "update", RegistrationID: created.Registration.ID, ExpectedVersion: 2, Reason: "rotation", Write: &updatedWrite})
	if err != nil || updated.Registration.Version != 3 {
		t.Fatalf("update = %+v, %v", updated.Registration, err)
	}
	if _, err := service.Execute(ctx, actor, "", provider.RegistrationCommand{Operation: "update", RegistrationID: created.Registration.ID, ExpectedVersion: 2, Reason: "rotation", Write: &updatedWrite}); !errors.Is(err, provider.ErrRegistrationConflict) {
		t.Fatalf("stale update = %v", err)
	}
	// Reads are tenant-scoped.
	if _, err := service.Get(ctx, actor, created.Registration.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, otherScope, created.Registration.ID); !errors.Is(err, provider.ErrRegistrationNotFound) {
		t.Fatalf("cross-tenant get = %v", err)
	}
	page, err := service.List(ctx, actor, nil, 10)
	if err != nil || len(page.Registrations) != 2 {
		t.Fatalf("list = %d, %v", len(page.Registrations), err)
	}
	// Health is bounded to the tenant's own request records.
	checkID, err := ids.NewCheck()
	if err != nil {
		t.Fatal(err)
	}
	check, err := verification.NewCheck(checkID, tenantID, verificationID, "idenqa.check.document_analysis", now)
	if err != nil {
		t.Fatal(err)
	}
	checkStore, err := verificationpostgres.NewCheckStore(runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	event, err := ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if err := checkStore.CreateCheck(ctx, scope, check, event); err != nil {
		t.Fatal(err)
	}
	attemptID, err := ids.NewAttempt()
	if err != nil {
		t.Fatal(err)
	}
	digest := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	attempt := verification.Attempt{ID: attemptID, Number: 1, Fence: 1, RunnerKind: verification.RunnerProvider, State: verification.AttemptRunning,
		Provenance: verification.Provenance{RunnerID: "dojah", RunnerVersion: "0.1.1", PackageDigest: digest, ContractMajor: 1, RequestDigest: digest, Configuration: digest},
		StartedAt:  now, Deadline: now.Add(time.Minute)}
	if err := check.BeginAttempt(attempt); err != nil {
		t.Fatal(err)
	}
	event, err = ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := checkStore.SaveCheck(ctx, scope, verification.CheckCommit{Check: check, ExpectedVersion: 1, EventID: event}); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{"configuration": map[string]any{"provider_id": updated.Registration.Configuration.ProviderID, "credential_version": updated.Registration.Configuration.CredentialVersion}, "adapter": map[string]any{"adapter_id": "dojah"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Native().Exec(ctx, `INSERT INTO idenqa.provider_requests(tenant_id,attempt_id,verification_id,check_id,request_digest,request_body) VALUES($1,$2,$3,$4,$5,$6)`, tenantID.String(), attemptID.String(), verificationID.String(), checkID.String(), digest, body); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Native().Exec(ctx, `INSERT INTO idenqa.provider_dispatches(tenant_id,attempt_id,request_digest,claimed_at) VALUES($1,$2,$3,$4)`, tenantID.String(), attemptID.String(), digest, now); err != nil {
		t.Fatal(err)
	}
	// Credential rotation advances only the secret reference version, is
	// version-checked, attributed and replayed exactly. In-flight requests keep
	// the persisted version pin: the stored request body is immutable while the
	// registration advances.
	rotation := provider.CredentialRotation{SecretReference: updated.Registration.Configuration.SecretReference, CredentialVersion: "v2"}
	rotated, err := service.Execute(ctx, actor, "rotate-key", provider.RegistrationCommand{Operation: "rotate-credential", RegistrationID: created.Registration.ID, ExpectedVersion: 3, Reason: "credential_rotation", Credential: &rotation})
	if err != nil || rotated.Registration.Version != 4 || rotated.Registration.Configuration.CredentialVersion != "v2" {
		t.Fatalf("rotate-credential = %+v, %v", rotated.Registration, err)
	}
	replayed, err := service.Execute(ctx, actor, "rotate-key", provider.RegistrationCommand{Operation: "rotate-credential", RegistrationID: created.Registration.ID, ExpectedVersion: 3, Reason: "credential_rotation", Credential: &rotation})
	if err != nil || !replayed.Replayed || replayed.Registration.Version != 4 {
		t.Fatalf("rotate-credential replay = %+v, %v", replayed, err)
	}
	if _, err := service.Execute(ctx, actor, "", provider.RegistrationCommand{Operation: "rotate-credential", RegistrationID: created.Registration.ID, ExpectedVersion: 3, Reason: "credential_rotation", Credential: &rotation}); !errors.Is(err, provider.ErrRegistrationConflict) {
		t.Fatalf("stale rotate-credential = %v", err)
	}
	same := provider.CredentialRotation{SecretReference: rotation.SecretReference, CredentialVersion: "v2"}
	if _, err := service.Execute(ctx, actor, "", provider.RegistrationCommand{Operation: "rotate-credential", RegistrationID: created.Registration.ID, ExpectedVersion: 4, Reason: "credential_rotation", Credential: &same}); !errors.Is(err, provider.ErrRegistrationConflict) {
		t.Fatalf("no-op rotate-credential = %v", err)
	}
	var persistedVersion string
	if err := admin.Native().QueryRow(ctx, `SELECT request_body->'configuration'->>'credential_version' FROM idenqa.provider_requests WHERE tenant_id=$1 AND attempt_id=$2`, tenantID.String(), attemptID.String()).Scan(&persistedVersion); err != nil {
		t.Fatal(err)
	}
	if persistedVersion != "v1" {
		t.Fatalf("persisted request credential version = %q, want the in-flight v1 pin", persistedVersion)
	}
	health, err := service.Health(ctx, actor, created.Registration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if health.Requests != 1 || health.Pending != 1 || health.Completed != 0 || health.Failed != 0 {
		t.Fatalf("health = %+v", health)
	}
	// RLS hides rows even when a query omits its tenant predicate.
	if err := runtime.WithinTransaction(ctx, pg.TransactionOptions{ReadOnly: true}, func(ctx context.Context, tx pg.Transaction) error {
		var count int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.provider_registrations`).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return errors.New("unscoped registration rows visible")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Append-only history rejects mutation.
	if _, err := admin.Native().Exec(ctx, `UPDATE idenqa.provider_registration_history SET reason=reason WHERE tenant_id=$1`, tenantID.String()); err == nil {
		t.Fatal("registration history was mutable")
	}
}

func requestDigestOf(t *testing.T, request providerv1.Request) string {
	t.Helper()
	digest, err := provider.RequestDigest(request)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
