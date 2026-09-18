package httpapi

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type registryProbe struct {
	model.RegistryRepository
	calls int
}

func (probe *registryProbe) Apply(_ context.Context, _ tenant.Scope, _ idempotency.Request, _ id.Event, command model.RegistryCommand) (model.RegistryReceipt, error) {
	probe.calls++
	return model.RegistryReceipt{State: model.RegistryState{Name: command.Name, Version: command.ExpectedVersion + 1}, Operation: command.Operation}, nil
}
func TestModelActivationRequiresDedicatedScopeAndClosedBody(t *testing.T) {
	for _, test := range []struct {
		name          string
		scope         access.Pattern
		body          string
		status, calls int
	}{
		{"write cannot activate", "models:write", `{"expected_version":0,"reason":"evaluation","deployment":{"model_revision":1,"threshold_revision":1,"region":"ng"}}`, 403, 0},
		{"authorized", "models:activate", `{"expected_version":0,"reason":"evaluation","deployment":{"model_revision":1,"threshold_revision":1,"region":"ng"}}`, 200, 1},
		{"missing version", "models:activate", `{"reason":"evaluation","deployment":{"model_revision":1,"threshold_revision":1,"region":"ng"}}`, 400, 0},
		{"duplicate version", "models:activate", `{"expected_version":0,"expected_version":1,"reason":"evaluation","deployment":{"model_revision":1,"threshold_revision":1,"region":"ng"}}`, 400, 0},
		{"actor injection", "models:activate", `{"expected_version":0,"reason":"evaluation","actor_id":"other","deployment":{"model_revision":1,"threshold_revision":1,"region":"ng"}}`, 400, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHTTPAccessFixture(t, nil, test.scope)
			probe := &registryProbe{}
			ids, err := id.NewSystemGenerator()
			if err != nil {
				t.Fatal(err)
			}
			service, err := model.NewManagement(probe, ids, time.Now, time.Hour)
			if err != nil {
				t.Fatal(err)
			}
			routes, err := NewModelRoutes(fixture.middleware, service, fixture.logger)
			if err != nil {
				t.Fatal(err)
			}
			router := versionedRouter(t, routes)
			request := httptest.NewRequestWithContext(t.Context(), "POST", "/v1/models/pad/activate", strings.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+fixture.encoded)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", `"activation"`)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status || probe.calls != test.calls {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, probe.calls, response.Body.String())
			}
		})
	}
}
