package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type routeFixture struct {
	name, policy, signal string
	calls                int
}

func (r *routeFixture) CaptureRoute() (string, string, string) {
	return "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", r.policy, "profile"
}
func (r *routeFixture) Plan(context.Context, verification.PlanInput) ([]verification.PlannedCheck, error) {
	return []verification.PlannedCheck{{Name: r.name, RunnerKind: verification.RunnerModel}}, nil
}
func (r *routeFixture) Prepare(context.Context, pg.Transaction, tenant.Scope, id.Verification, id.Check, id.Attempt, verification.PlannedCheck, time.Time, time.Time) (verification.Provenance, func(context.Context, verification.Check) error, error) {
	r.calls++
	return verification.Provenance{}, func(context.Context, verification.Check) error { return nil }, nil
}
func TestCompositionRejectsAmbiguousRoutes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*routeFixture)
	}{
		{"policy mismatch", func(r *routeFixture) { r.policy = "pol_01K4AR9V8FQ2G7ZXCPNM5T6JWX" }},
		{"duplicate check", func(r *routeFixture) { r.name = "pad" }},
		{"duplicate signal", func(r *routeFixture) { r.signal = "pad" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &routeFixture{name: "pad", policy: "pol_01K4AR9V8FQ2G7ZXCPNM5T6JWH", signal: "pad"}
			b := &routeFixture{name: "match", policy: a.policy, signal: "match"}
			tc.change(b)
			if _, err := composeRoutes(t.Context(), []executionRoute{{planner: a, preparation: a, signals: []string{a.signal}}, {planner: b, preparation: b, signals: []string{b.signal}}}); err == nil {
				t.Fatal("ambiguous composition accepted")
			}
		})
	}
}
func TestCompositionDispatchesPreparationExactly(t *testing.T) {
	t.Parallel()
	a := &routeFixture{name: "pad", policy: "pol_01K4AR9V8FQ2G7ZXCPNM5T6JWH", signal: "pad"}
	b := &routeFixture{name: "match", policy: a.policy, signal: "match"}
	routes, err := composeRoutes(t.Context(), []executionRoute{{planner: a, preparation: a, signals: []string{a.signal}}, {planner: b, preparation: b, signals: []string{b.signal}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := routes.Plan(t.Context(), verification.PlanInput{})
	if err != nil || len(plan) != 2 {
		t.Fatal("checks dropped", err)
	}
	if _, _, err := routes.Prepare(t.Context(), nil, tenant.Scope{}, id.Verification{}, id.Check{}, id.Attempt{}, plan[1], time.Time{}, time.Time{}); err != nil || a.calls != 0 || b.calls != 1 {
		t.Fatal("wrong preparer selected", err)
	}
	plan[1].RunnerKind = verification.RunnerProvider
	if _, _, err := routes.Prepare(t.Context(), nil, tenant.Scope{}, id.Verification{}, id.Check{}, id.Attempt{}, plan[1], time.Time{}, time.Time{}); err == nil {
		t.Fatal("runner kind substitution accepted")
	}
}

type modelExecutorFixture struct{ calls int }

func (e *modelExecutorFixture) Execute(context.Context, modelv1.Request) (modelv1.Result, error) {
	e.calls++
	return modelv1.Result{}, nil
}
func TestModelDispatchRequiresExactConfiguration(t *testing.T) {
	t.Parallel()
	a, b := &modelExecutorFixture{}, &modelExecutorFixture{}
	first := modelv1.ConfigurationReference{ModelID: "a", ConfigurationDigest: "a"}
	second := modelv1.ConfigurationReference{ModelID: "b", ConfigurationDigest: "b"}
	executors := modelExecutors{first: a, second: b}
	if _, err := executors.Execute(t.Context(), modelv1.Request{Configuration: second}); err != nil || a.calls != 0 || b.calls != 1 {
		t.Fatal("wrong model executed")
	}
	second.ConfigurationDigest = "changed"
	if _, err := executors.Execute(t.Context(), modelv1.Request{Configuration: second}); err == nil || b.calls != 1 {
		t.Fatal("unknown configuration fell back")
	}
}

type registrationSourceFixture struct {
	registrations []provider.Registration
	err           error
}

func (source registrationSourceFixture) Enabled(context.Context, id.Tenant, string, string) ([]provider.Registration, error) {
	return source.registrations, source.err
}

func registrationFixture(providerID string) provider.Binding {
	manifest := dojah.Description()
	return provider.Binding{TenantID: "ten_01K4AR9V8FQ2G7ZXCPNM5T6JWH", PolicyID: "pol_01K4AR9V8FQ2G7ZXCPNM5T6JWH",
		ProfileDigest: "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Requirement: "document",
		Region: "africa", Purpose: "idenqa.purpose.identity_verification", Recipient: "tenant.recipient.primary",
		Configuration: providerv1.ConfigurationReference{ProviderID: providerID, SchemaDigest: manifest.Configuration.Digest, SecretReference: "secret://provider/tenant", CredentialVersion: "v1"}}
}

func TestCompositionSelectsEnabledRegistrationThenFallsBack(t *testing.T) {
	t.Parallel()
	binding := registrationFixture("pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	manifest := dojah.Description()
	deployment, err := provider.NewPlan(binding, manifest)
	if err != nil {
		t.Fatal(err)
	}
	registration := provider.Registration{ID: "pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "dojah", Region: "africa", Enabled: true,
		Configuration: providerv1.ConfigurationReference{ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWK", SchemaDigest: manifest.Configuration.Digest, SecretReference: "secret://provider/tenant/other", CredentialVersion: "v1"}}
	source := registrationSourceFixture{registrations: []provider.Registration{registration}}
	route := &registrationRoute{source: source, manifest: manifest, template: binding, deployment: deployment, preparation: &providerpostgres.Preparation{Plan: deployment}}
	composed, err := composeRoutes(t.Context(), []executionRoute{{planner: deployment, preparation: preparationFixture{}, signals: deployment.OutputSignals(), registration: route}})
	if err != nil {
		t.Fatal(err)
	}
	input := verification.PlanInput{TenantID: mustTenant(t, binding.TenantID), PolicyID: mustPolicy(t, binding.PolicyID), ProfileDigest: binding.ProfileDigest}
	checks, err := composed.Plan(t.Context(), input)
	if err != nil || len(checks) != 1 {
		t.Fatalf("registered plan = %v, %v", checks, err)
	}
	registered, err := provider.NewRegisteredPlan(registration, binding, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if checks[0].Provenance.RequestDigest != registered.ConfigurationDigest() || checks[0].Provenance.RequestDigest == deployment.ConfigurationDigest() {
		t.Fatal("plan did not pin the registered configuration")
	}
	fallback, err := composeRoutes(t.Context(), []executionRoute{{planner: deployment, preparation: preparationFixture{}, signals: deployment.OutputSignals(),
		registration: &registrationRoute{source: registrationSourceFixture{}, manifest: manifest, template: binding, deployment: deployment, preparation: &providerpostgres.Preparation{Plan: deployment}}}})
	if err != nil {
		t.Fatal(err)
	}
	checks, err = fallback.Plan(t.Context(), input)
	if err != nil || len(checks) != 1 || checks[0].Provenance.RequestDigest != deployment.ConfigurationDigest() {
		t.Fatalf("deployment fallback = %v, %v", checks, err)
	}
}

type registrationHealthFixture struct {
	states map[string]provider.HealthState
	err    error
}

func (source registrationHealthFixture) RegistrationHealth(_ context.Context, _ tenant.Scope, registration provider.Registration) (provider.HealthSnapshot, error) {
	if source.err != nil {
		return provider.HealthSnapshot{}, source.err
	}
	return provider.HealthSnapshot{State: source.states[registration.ID]}, nil
}

func TestCompositionHealthRoutesAroundNotReadyRegistration(t *testing.T) {
	t.Parallel()
	binding := registrationFixture("pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	manifest := dojah.Description()
	deployment, err := provider.NewPlan(binding, manifest)
	if err != nil {
		t.Fatal(err)
	}
	registration := provider.Registration{ID: "pvr_01K4AR9V8FQ2G7ZXCPNM5T6JWH", AdapterID: "dojah", Region: "africa", Enabled: true,
		Configuration: providerv1.ConfigurationReference{ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWK", SchemaDigest: manifest.Configuration.Digest, SecretReference: "secret://provider/tenant/other", CredentialVersion: "v1"}}
	registered, err := provider.NewRegisteredPlan(registration, binding, manifest)
	if err != nil {
		t.Fatal(err)
	}
	input := verification.PlanInput{TenantID: mustTenant(t, binding.TenantID), PolicyID: mustPolicy(t, binding.PolicyID), ProfileDigest: binding.ProfileDigest}
	compose := func(health RegistrationHealthSource) (*composedRoutes, error) {
		route := &registrationRoute{source: registrationSourceFixture{registrations: []provider.Registration{registration}}, health: health,
			manifest: manifest, template: binding, deployment: deployment, preparation: &providerpostgres.Preparation{Plan: deployment}}
		return composeRoutes(t.Context(), []executionRoute{{planner: deployment, preparation: preparationFixture{}, signals: deployment.OutputSignals(), registration: route}})
	}
	for _, test := range []struct {
		name      string
		state     provider.HealthState
		want      string
		wantError bool
	}{
		{name: "ready selects the registration", state: provider.HealthReady, want: registered.ConfigurationDigest()},
		{name: "degraded keeps the registration", state: provider.HealthDegraded, want: registered.ConfigurationDigest()},
		{name: "not ready falls back to deployment", state: provider.HealthNotReady, want: deployment.ConfigurationDigest()},
		{name: "unknown keeps the registration", state: provider.HealthUnknown, want: registered.ConfigurationDigest()},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			composed, err := compose(registrationHealthFixture{states: map[string]provider.HealthState{registration.ID: test.state}})
			if err != nil {
				t.Fatal(err)
			}
			checks, err := composed.Plan(t.Context(), input)
			if err != nil || len(checks) != 1 || checks[0].Provenance.RequestDigest != test.want {
				t.Fatalf("plan = %v, %v", checks, err)
			}
		})
	}
	failing, err := compose(registrationHealthFixture{err: errors.New("health unavailable")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failing.Plan(t.Context(), input); err == nil {
		t.Fatal("health failure did not fail closed")
	}
}

func TestCompositionRegistrationPinnedDigestFailsClosed(t *testing.T) {
	t.Parallel()
	binding := registrationFixture("pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH")
	manifest := dojah.Description()
	deployment, err := provider.NewPlan(binding, manifest)
	if err != nil {
		t.Fatal(err)
	}
	route := &registrationRoute{source: registrationSourceFixture{}, manifest: manifest, template: binding, deployment: deployment}
	if _, _, err := route.selectPlan(t.Context(), mustScope(t, binding.TenantID), "deadbeef"); !errors.Is(err, verification.ErrPlanUnavailable) {
		t.Fatalf("pinned digest fallback = %v", err)
	}
	sourceErr := errors.New("registration read failed")
	failing := &registrationRoute{source: registrationSourceFixture{err: sourceErr}, manifest: manifest, template: binding, deployment: deployment}
	if _, _, err := failing.selectPlan(t.Context(), mustScope(t, binding.TenantID), "deadbeef"); !errors.Is(err, sourceErr) {
		t.Fatalf("source error = %v", err)
	}
}

type preparationFixture struct{}

func (preparationFixture) Prepare(context.Context, pg.Transaction, tenant.Scope, id.Verification, id.Check, id.Attempt, verification.PlannedCheck, time.Time, time.Time) (verification.Provenance, func(context.Context, verification.Check) error, error) {
	return verification.Provenance{}, func(context.Context, verification.Check) error { return nil }, nil
}

func mustTenant(t *testing.T, value string) id.Tenant {
	t.Helper()
	parsed, err := id.ParseTenant(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func mustPolicy(t *testing.T, value string) id.Policy {
	t.Helper()
	parsed, err := id.ParsePolicy(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func mustScope(t *testing.T, value string) tenant.Scope {
	t.Helper()
	scope, err := tenant.NewScope(mustTenant(t, value))
	if err != nil {
		t.Fatal(err)
	}
	return scope
}
