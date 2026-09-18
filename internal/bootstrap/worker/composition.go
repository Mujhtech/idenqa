package worker

import (
	"context"
	"errors"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

type executionRoute struct {
	planner     verification.CheckPlanner
	preparation verificationpostgres.PlannedCheckPreparation
	signals     []string
}
type composedRoutes struct {
	routes                  []executionRoute
	tenant, policy, profile string
	preparations            map[string]verificationpostgres.PlannedCheckPreparation
	kinds                   map[string]verification.RunnerKind
	signals                 []string
}

func composeRoutes(routes []executionRoute) (*composedRoutes, error) {
	result := &composedRoutes{routes: routes, preparations: map[string]verificationpostgres.PlannedCheckPreparation{}, kinds: map[string]verification.RunnerKind{}}
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
		checks, err := route.planner.Plan(verification.PlanInput{TenantID: tenantID, PolicyID: policyID, ProfileDigest: profile})
		if err != nil || len(checks) == 0 {
			return nil, verification.ErrPlanUnavailable
		}
		for _, check := range checks {
			if result.preparations[check.Name] != nil {
				return nil, verification.ErrPlanUnavailable
			}
			result.preparations[check.Name], result.kinds[check.Name] = route.preparation, check.RunnerKind
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
func (routes *composedRoutes) Plan(input verification.PlanInput) ([]verification.PlannedCheck, error) {
	var result []verification.PlannedCheck
	for _, route := range routes.routes {
		checks, err := route.planner.Plan(input)
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
