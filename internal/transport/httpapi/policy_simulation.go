package httpapi

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

// policyRevisionDiffer is the narrow authorised revision-diff boundary.
type policyRevisionDiffer interface {
	Diff(context.Context, access.Context, id.Policy, uint32, uint32) (policy.RevisionDiff, error)
}

// PolicySimulationRoutes exposes read-only policy simulation, regression, and
// revision diff. None of these operations creates policy meaning, activation,
// decision, task, or audit effects.
type PolicySimulationRoutes struct {
	access    *AccessMiddleware
	differ    policyRevisionDiffer
	simulator *policy.Simulator
	suite     *policy.ScenarioSuite
	logger    *slog.Logger
}

// NewPolicySimulationRoutes constructs read-only policy computation routes.
func NewPolicySimulationRoutes(
	middleware *AccessMiddleware,
	differ policyRevisionDiffer,
	simulator *policy.Simulator,
	suite *policy.ScenarioSuite,
	logger *slog.Logger,
) (*PolicySimulationRoutes, error) {
	if middleware == nil || differ == nil || simulator == nil || suite == nil || logger == nil {
		return nil, policy.ErrInvalid
	}
	return &PolicySimulationRoutes{middleware, differ, simulator, suite, logger}, nil
}

// Register adds policy simulation, regression, and revision diff. All three
// require only the existing policies:read permission.
func (routes *PolicySimulationRoutes) Register(router chi.Router) {
	for _, route := range []struct {
		method, path string
		handler      http.HandlerFunc
	}{
		{"POST", "/policy-simulations", routes.simulate},
		{"POST", "/policy-regressions", routes.regress},
		{"GET", "/policies/{policyID}/diff", routes.diff},
	} {
		router.With(routes.access.Authenticate, routes.access.Require(access.PermissionPoliciesRead)).MethodFunc(route.method, route.path, route.handler)
	}
}

func (routes *PolicySimulationRoutes) simulate(w http.ResponseWriter, r *http.Request) {
	input, err := routes.simulationInput(w, r)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	simulation, err := routes.simulator.Run(r.Context(), input)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	_, report, err := policy.NewSimulationBundle(simulation)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	routes.json(w, r, report)
}

func (routes *PolicySimulationRoutes) regress(w http.ResponseWriter, r *http.Request) {
	scenarios, err := routes.scenarioSuite(w, r)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	_, report, err := routes.suite.Run(r.Context(), scenarios)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	routes.json(w, r, report)
}

func (routes *PolicySimulationRoutes) diff(w http.ResponseWriter, r *http.Request) {
	identifier, err := policyIdentifier(r)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	from, to, err := policyDiffRevisions(r)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	actor, _ := AccessContext(r.Context())
	result, err := routes.differ.Diff(r.Context(), actor, identifier, from, to)
	if err != nil {
		routes.problem(w, r, err)
		return
	}
	routes.json(w, r, result)
}

func policyDiffRevisions(r *http.Request) (uint32, uint32, error) {
	from, err := strconv.ParseUint(r.URL.Query().Get("from_revision"), 10, 32)
	if err != nil || from == 0 {
		return 0, 0, policy.ErrInvalid
	}
	to, err := strconv.ParseUint(r.URL.Query().Get("to_revision"), 10, 32)
	if err != nil || to == 0 {
		return 0, 0, policy.ErrInvalid
	}
	return uint32(from), uint32(to), nil
}

func (routes *PolicySimulationRoutes) simulationInput(w http.ResponseWriter, r *http.Request) (policy.SimulationInput, error) {
	body, err := boundedPolicyBody(w, r, policy.MaximumSimulationInputBytes)
	if err != nil {
		return policy.SimulationInput{}, err
	}
	input, err := policy.ParseSimulationInput(body)
	if err != nil {
		return policy.SimulationInput{}, invalidRequest(err)
	}
	return input, nil
}

func (routes *PolicySimulationRoutes) scenarioSuite(w http.ResponseWriter, r *http.Request) ([]policy.Scenario, error) {
	body, err := boundedPolicyBody(w, r, policy.MaximumScenarioSuiteBytes)
	if err != nil {
		return nil, err
	}
	scenarios, err := policy.ParseScenarioSuite(body)
	if err != nil {
		return nil, invalidRequest(err)
	}
	return scenarios, nil
}

func boundedPolicyBody(w http.ResponseWriter, r *http.Request, maximum int) ([]byte, error) {
	if r.ContentLength > int64(maximum) {
		return nil, requestTooLarge()
	}
	body, err := io.ReadAll(io.LimitReader(http.MaxBytesReader(w, r.Body, int64(maximum)), int64(maximum)+1))
	if err != nil || len(body) > maximum {
		return nil, requestTooLarge()
	}
	return body, nil
}

func (routes *PolicySimulationRoutes) json(w http.ResponseWriter, r *http.Request, value any) {
	w.Header().Set("Cache-Control", "no-store")
	if err := respond.JSON(w, r, 200, value); err != nil {
		routes.logger.ErrorContext(r.Context(), "write policy computation response")
	}
}

func (routes *PolicySimulationRoutes) problem(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, policy.ErrRevisionNotFound), errors.Is(err, policy.ErrActivationNotFound):
		err = apierror.New(404, apierror.CodeNotFound, "Not found", "The policy resource was not found.", err)
	case errors.Is(err, policy.ErrConflict), errors.Is(err, policy.ErrRevisionConflict):
		err = apierror.New(409, apierror.CodeConflict, "Conflict", "The policy operation conflicts with current state.", err)
	case errors.Is(err, policy.ErrInvalid):
		err = invalidRequest(err)
	}
	if writeErr := respond.WriteProblem(w, r, err, requestIDString(r.Context())); writeErr != nil {
		routes.logger.ErrorContext(r.Context(), "write policy computation problem")
	}
}
