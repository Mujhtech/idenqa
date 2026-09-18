package worker

import (
	"context"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
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
func (r *routeFixture) Plan(verification.PlanInput) ([]verification.PlannedCheck, error) {
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
			if _, err := composeRoutes([]executionRoute{{a, a, []string{a.signal}}, {b, b, []string{b.signal}}}); err == nil {
				t.Fatal("ambiguous composition accepted")
			}
		})
	}
}
func TestCompositionDispatchesPreparationExactly(t *testing.T) {
	t.Parallel()
	a := &routeFixture{name: "pad", policy: "pol_01K4AR9V8FQ2G7ZXCPNM5T6JWH", signal: "pad"}
	b := &routeFixture{name: "match", policy: a.policy, signal: "match"}
	routes, err := composeRoutes([]executionRoute{{a, a, []string{a.signal}}, {b, b, []string{b.signal}}})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := routes.Plan(verification.PlanInput{})
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
