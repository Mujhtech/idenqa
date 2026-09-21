package worker

import (
	"context"
	"errors"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/provider"
	providerpostgres "github.com/Mujhtech/idenqa/internal/provider/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

// RegistrationSource reads the enabled tenant provider registrations for one
// adapter and region. It is owned by the consuming composition boundary and
// implemented by the tenant-scoped registration store.
type RegistrationSource interface {
	Enabled(context.Context, id.Tenant, string, string) ([]provider.Registration, error)
}

// RegistrationHealthSource derives the bounded readiness snapshot for one
// registration. It is owned by the consuming composition boundary and
// implemented by the cached provider health service.
type RegistrationHealthSource interface {
	RegistrationHealth(context.Context, tenant.Scope, provider.Registration) (provider.HealthSnapshot, error)
}

// registrationRoute carries everything needed to re-derive the exact
// tenant-selected provider plan without mutating shared composition state.
// The registration list yields zero or one enabled row per (tenant, adapter,
// region); no evidence or credential value ever enters this decision.
type registrationRoute struct {
	source      RegistrationSource
	health      RegistrationHealthSource
	manifest    providerv1.Manifest
	template    provider.Binding
	deployment  *provider.Plan
	preparation *providerpostgres.Preparation
}

// plan selects the tenant registration for the exact adapter and deployment
// region. PlanInput carries tenant, policy and immutable profile only; the
// adapter and region come from the candidate deployment route, which is the
// strongest context the input proves. A not_ready registration is excluded so
// the deployment route remains the fallback; a registration that cannot compose
// an exact plan still fails closed instead of being silently bypassed.
func (route *registrationRoute) plan(ctx context.Context, input verification.PlanInput) (*provider.Plan, error) {
	registrations, err := route.source.Enabled(ctx, input.TenantID, route.manifest.Package.AdapterID, route.template.Region)
	if err != nil {
		return nil, err
	}
	registration, ok, err := route.selectRegistration(ctx, input, registrations)
	if err != nil || !ok {
		return nil, err
	}
	plan, err := provider.NewRegisteredPlan(registration, route.template, route.manifest)
	if err != nil {
		// An enabled registration that cannot compose an exact plan must not be
		// silently bypassed by a deployment fallback.
		return nil, verification.ErrPlanUnavailable
	}
	return plan, nil
}

// selectRegistration applies the bounded readiness states for the exact adapter
// and region. Without a health source it preserves the single-enabled
// registration rule; with one it excludes not_ready candidates and prefers
// ready over degraded ones. Health never broadens the route to another region.
func (route *registrationRoute) selectRegistration(ctx context.Context, input verification.PlanInput, registrations []provider.Registration) (provider.Registration, bool, error) {
	if route.health == nil {
		registration, ok := provider.Selected(registrations, route.manifest.Package.AdapterID, route.template.Region)
		return registration, ok, nil
	}
	scope, err := tenant.NewScope(input.TenantID)
	if err != nil {
		return provider.Registration{}, false, err
	}
	states := map[string]provider.HealthState{}
	for _, registration := range registrations {
		if !registration.Enabled || registration.AdapterID != route.manifest.Package.AdapterID || registration.Region != route.template.Region {
			continue
		}
		snapshot, err := route.health.RegistrationHealth(ctx, scope, registration)
		if err != nil {
			return provider.Registration{}, false, err
		}
		states[registration.ID] = snapshot.State
	}
	registration, ok := provider.SelectHealthy(registrations, route.manifest.Package.AdapterID, route.template.Region, func(registration provider.Registration) provider.HealthState {
		return states[registration.ID]
	})
	return registration, ok, nil
}

// selectPlan re-derives the plan that produced one already-pinned check
// definition. The pinned configuration digest decides between the deployment
// plan and the current enabled registration. A digest that matches neither
// fails closed instead of switching provider meaning after planning.
func (route *registrationRoute) selectPlan(ctx context.Context, scope tenant.Scope, pinned string) (*provider.Plan, bool, error) {
	if pinned == route.deployment.ConfigurationDigest() {
		return route.deployment, false, nil
	}
	plan, err := route.plan(ctx, verification.PlanInput{TenantID: scope.ID()})
	if err != nil {
		return nil, false, err
	}
	if plan == nil || plan.ConfigurationDigest() != pinned {
		return nil, false, verification.ErrPlanUnavailable
	}
	return plan, true, nil
}

type executionRoute struct {
	planner      verification.CheckPlanner
	preparation  verificationpostgres.PlannedCheckPreparation
	signals      []string
	registration *registrationRoute
}
type composedRoutes struct {
	routes        []executionRoute
	tenant        string
	policy        string
	profile       string
	preparations  map[string]verificationpostgres.PlannedCheckPreparation
	registrations map[string]*registrationRoute
	kinds         map[string]verification.RunnerKind
	signals       []string
}

func composeRoutes(ctx context.Context, routes []executionRoute) (*composedRoutes, error) {
	result := &composedRoutes{routes: routes, preparations: map[string]verificationpostgres.PlannedCheckPreparation{}, registrations: map[string]*registrationRoute{}, kinds: map[string]verification.RunnerKind{}}
	if len(routes) == 0 || len(routes) > 9 {
		return nil, verification.ErrPlanUnavailable
	}
	seen := map[string]bool{}
	for i, route := range routes {
		filter, ok := route.planner.(verification.CaptureRoute)
		if !ok || route.preparation == nil {
			return nil, verification.ErrPlanUnavailable
		}
		tenantValue, policyValue, profile := filter.CaptureRoute()
		if i == 0 {
			result.tenant, result.policy, result.profile = tenantValue, policyValue, profile
		}
		if tenantValue != result.tenant || policyValue != result.policy || profile != result.profile {
			return nil, verification.ErrPlanUnavailable
		}
		tenantID, err := id.ParseTenant(tenantValue)
		if err != nil {
			return nil, err
		}
		policyID, err := id.ParsePolicy(policyValue)
		if err != nil {
			return nil, err
		}
		checks, err := route.planner.Plan(ctx, verification.PlanInput{TenantID: tenantID, PolicyID: policyID, ProfileDigest: profile})
		if err != nil || len(checks) == 0 {
			return nil, verification.ErrPlanUnavailable
		}
		for _, check := range checks {
			if result.preparations[check.Name] != nil {
				return nil, verification.ErrPlanUnavailable
			}
			result.preparations[check.Name], result.kinds[check.Name] = route.preparation, check.RunnerKind
			if route.registration != nil {
				result.registrations[check.Name] = route.registration
			}
		}
		for _, signal := range route.signals {
			if seen[signal] {
				return nil, verification.ErrPlanUnavailable
			}
			seen[signal] = true
			result.signals = append(result.signals, signal)
		}
	}
	return result, nil
}
func (routes *composedRoutes) CaptureRoute() (string, string, string) {
	return routes.tenant, routes.policy, routes.profile
}
func (routes *composedRoutes) Plan(ctx context.Context, input verification.PlanInput) ([]verification.PlannedCheck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var result []verification.PlannedCheck
	for _, route := range routes.routes {
		if route.registration != nil {
			selected, err := route.registration.plan(ctx, input)
			if err != nil {
				return nil, err
			}
			if selected != nil {
				checks, err := selected.Plan(ctx, input)
				if err != nil {
					return nil, err
				}
				result = append(result, checks...)
				continue
			}
		}
		checks, err := route.planner.Plan(ctx, input)
		if err != nil {
			return nil, err
		}
		result = append(result, checks...)
	}
	return result, nil
}
func (routes *composedRoutes) Prepare(ctx context.Context, tx pg.Transaction, scope tenant.Scope, verificationID id.Verification, checkID id.Check, attemptID id.Attempt, definition verification.PlannedCheck, now, deadline time.Time) (verification.Provenance, func(context.Context, verification.Check) error, error) {
	preparation := routes.preparations[definition.Name]
	if preparation == nil || routes.kinds[definition.Name] != definition.RunnerKind {
		return verification.Provenance{}, nil, verification.ErrPlanUnavailable
	}
	if registration := routes.registrations[definition.Name]; registration != nil {
		selected, chosen, err := registration.selectPlan(ctx, scope, definition.Provenance.Configuration)
		if err != nil {
			return verification.Provenance{}, nil, err
		}
		if chosen {
			bound := *registration.preparation
			bound.Plan = selected
			return bound.Prepare(ctx, tx, scope, verificationID, checkID, attemptID, definition, now, deadline)
		}
	}
	return preparation.Prepare(ctx, tx, scope, verificationID, checkID, attemptID, definition, now, deadline)
}

type modelExecutors map[modelv1.ConfigurationReference]modelv1.Executor

func (executors modelExecutors) Execute(ctx context.Context, request modelv1.Request) (modelv1.Result, error) {
	executor := executors[request.Configuration]
	if executor == nil {
		return modelv1.Result{}, model.ErrRequestUnavailable
	}
	return executor.Execute(ctx, request)
}

type runtimeConnections []interface{ Close() error }

func (connections runtimeConnections) Close() error {
	var result error
	for _, connection := range connections {
		result = errors.Join(result, connection.Close())
	}
	return result
}
