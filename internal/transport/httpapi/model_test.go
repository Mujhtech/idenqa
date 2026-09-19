package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type registryProbe struct {
	model.RegistryRepository
	calls     int
	state     model.RegistryState
	stateErr  error
	revisions map[string]model.RegistryRevision
	eligible  bool
}

func (probe *registryProbe) Apply(_ context.Context, _ tenant.Scope, _ idempotency.Request, _ id.Event, command model.RegistryCommand) (model.RegistryReceipt, error) {
	probe.calls++
	return model.RegistryReceipt{State: model.RegistryState{Name: command.Name, Version: command.ExpectedVersion + 1}, Operation: command.Operation}, nil
}

func (probe *registryProbe) Get(context.Context, tenant.Scope, string) (model.RegistryState, error) {
	return probe.state, probe.stateErr
}

func (probe *registryProbe) Revision(_ context.Context, _ tenant.Scope, _, kind string, revision int64) (model.RegistryRevision, error) {
	found, ok := probe.revisions[kind+"-"+strconv.FormatInt(revision, 10)]
	if !ok {
		return model.RegistryRevision{}, model.ErrRegistryNotFound
	}
	return found, nil
}

func (probe *registryProbe) RollbackEligible(context.Context, tenant.Scope, string, model.Deployment) (bool, error) {
	return probe.eligible, nil
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

func handlerRegistryFixture(t *testing.T) (model.Registration, model.ThresholdSet) {
	t.Helper()
	hash := "sha256:" + strings.Repeat("a", 64)
	ref := modelv1.ConfigurationReference{ModelID: "mdl_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ConfigurationRef: "configuration://model/test", ConfigurationDigest: hash}
	provenance := modelv1.Provenance{ModelID: ref.ModelID, ModelVersion: "0.1.0", ModelDigest: hash, RuntimeDigest: hash, PreprocessingDigest: hash, OutputSchemaDigest: hash, Contract: modelv1.CurrentVersion}
	registration := model.Registration{Manifest: modelv1.Manifest{Provenance: provenance, Capabilities: []modelv1.Capability{{Evaluation: "idenqa.check.passive_pad", AcceptedEvidence: []string{"idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.passive_pad"}}}, Restrictions: modelv1.Restrictions{MaximumGrants: 1, MaximumInputBytes: 1024, MaximumResultSize: 4096, MaximumDuration: 30 * time.Second}}, Configuration: ref, Owner: "fixture", License: "synthetic-only", TrainingProvenance: "synthetic", IntendedUse: "evaluation", ProhibitedUse: "production", Regions: []string{"ng"}, HardwareClass: "cpu", EvaluationOnly: true}
	threshold := model.ThresholdSet{Configuration: ref, Provenance: provenance, ScoreName: "real_score", Minimum: 0, Maximum: 1, Cutoff: 0.5, HigherIsGenuine: true, EvaluationReportDigest: hash, EvaluationOnly: true}
	return registration, threshold
}

func TestModelValidationReportsAndRejectsWithoutPersistence(t *testing.T) {
	registration, threshold := handlerRegistryFixture(t)
	deployment := model.Deployment{ModelRevision: 1, ThresholdRevision: 1, Region: "ng"}
	production := registration
	production.EvaluationOnly = false
	validRegister, err := json.Marshal(model.ValidationRequest{Operation: "register", Reason: "evaluation", Registration: &registration})
	if err != nil {
		t.Fatal(err)
	}
	productionRegister, err := json.Marshal(model.ValidationRequest{Operation: "register", Reason: "evaluation", Registration: &production})
	if err != nil {
		t.Fatal(err)
	}
	validThreshold, err := json.Marshal(model.ValidationRequest{Operation: "threshold", Reason: "evaluation", ExpectedVersion: 1, Thresholds: &threshold})
	if err != nil {
		t.Fatal(err)
	}
	validActivate, err := json.Marshal(model.ValidationRequest{Operation: "activate", Reason: "evaluation", ExpectedVersion: 2, Deployment: &deployment})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name          string
		scope         access.Pattern
		path          string
		body          []byte
		state         model.RegistryState
		stateErr      error
		status        int
		accepted      bool
		reasonCodes   []string
		applyExpected int
	}{
		{
			name: "read scope cannot validate", scope: "models:read", path: "/v1/models/pad/validate", body: validRegister,
			status: 403,
		},
		{
			name: "register accepted without persistence", scope: "models:write", path: "/v1/models/pad/validate", body: validRegister,
			stateErr: model.ErrRegistryNotFound, status: 200, accepted: true, reasonCodes: []string{},
		},
		{
			name: "production registration rejected", scope: "models:write", path: "/v1/models/pad/validate", body: productionRegister,
			stateErr: model.ErrRegistryNotFound, status: 200, reasonCodes: []string{"registration_invalid"},
		},
		{
			name: "threshold accepted", scope: "models:write", path: "/v1/models/pad/validate", body: validThreshold,
			state: model.RegistryState{Name: "pad", Version: 1}, status: 200, accepted: true, reasonCodes: []string{},
		},
		{
			name: "unknown model rejected", scope: "models:write", path: "/v1/models/pad/validate", body: validActivate,
			stateErr: model.ErrRegistryNotFound, status: 200, reasonCodes: []string{"registry_not_found"},
		},
		{
			name: "malformed body", scope: "models:write", path: "/v1/models/pad/validate", body: []byte("{"),
			status: 400,
		},
		{
			name: "duplicate operation key", scope: "models:write", path: "/v1/models/pad/validate",
			body:   []byte(`{"operation":"register","operation":"register","reason":"evaluation"}`),
			status: 400,
		},
		{
			name: "unknown field", scope: "models:write", path: "/v1/models/pad/validate",
			body:   []byte(`{"operation":"register","reason":"evaluation","registration":null,"actor_id":"other"}`),
			status: 400,
		},
		{
			name: "unknown operation", scope: "models:write", path: "/v1/models/pad/validate",
			body:   []byte(`{"operation":"promote","reason":"evaluation","registration":null}`),
			status: 400,
		},
		{
			name: "invalid name", scope: "models:write", path: "/v1/models/Pad/validate", body: validRegister,
			status: 400,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newHTTPAccessFixture(t, nil, test.scope)
			probe := &registryProbe{
				state: test.state, stateErr: test.stateErr,
				revisions: map[string]model.RegistryRevision{"model-1": {Kind: "model", Revision: 1, Registration: &registration}, "threshold-1": {Kind: "threshold", Revision: 1, Thresholds: &threshold}},
			}
			service := handlerModelService(t, probe)
			routes, err := NewModelRoutes(fixture.middleware, service, fixture.logger)
			if err != nil {
				t.Fatal(err)
			}
			router := versionedRouter(t, routes)
			request := httptest.NewRequestWithContext(t.Context(), "POST", test.path, bytes.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer "+fixture.encoded)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if probe.calls != test.applyExpected {
				t.Fatalf("persisted commands=%d, want %d", probe.calls, test.applyExpected)
			}
			if test.status != 200 {
				return
			}
			var report struct {
				Accepted    bool     `json:"accepted"`
				Operation   string   `json:"operation"`
				ReasonCodes []string `json:"reason_codes"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &report); err != nil {
				t.Fatal(err)
			}
			if report.Accepted != test.accepted || !slices.Equal(report.ReasonCodes, test.reasonCodes) {
				t.Fatalf("report = %+v, want accepted=%t codes=%v", report, test.accepted, test.reasonCodes)
			}
		})
	}
}

func TestModelValidationRejectsOversizedBody(t *testing.T) {
	fixture := newHTTPAccessFixture(t, nil, "models:write")
	probe := &registryProbe{}
	service := handlerModelService(t, probe)
	routes, err := NewModelRoutes(fixture.middleware, service, fixture.logger)
	if err != nil {
		t.Fatal(err)
	}
	router := versionedRouter(t, routes)
	// 2 * policy MaximumDocumentBytes is the accepted ceiling; one extra byte fails closed.
	oversized := bytes.Repeat([]byte{'x'}, 2*128*1024+1)
	request := httptest.NewRequestWithContext(t.Context(), "POST", "/v1/models/pad/validate", bytes.NewReader(oversized))
	request.Header.Set("Authorization", "Bearer "+fixture.encoded)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != 400 || probe.calls != 0 {
		t.Fatalf("status=%d body=%s", response.Code, strings.TrimSpace(response.Body.String()))
	}
}

func handlerModelService(t *testing.T, probe model.RegistryRepository) *model.Management {
	t.Helper()
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	service, err := model.NewManagement(probe, ids, time.Now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
