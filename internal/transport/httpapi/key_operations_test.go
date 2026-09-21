package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	"github.com/Mujhtech/idenqa/internal/keyrewrap"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/go-chi/chi/v5"
)

type stubRewrapRepository struct {
	states []keyrewrap.State
}

func (repository *stubRewrapRepository) Load(_ context.Context, class keyrewrap.Class) (keyrewrap.State, bool, error) {
	for _, state := range repository.states {
		if state.Class == class {
			return state, true, nil
		}
	}
	return keyrewrap.State{}, false, nil
}

func (repository *stubRewrapRepository) States(context.Context) ([]keyrewrap.State, error) {
	return repository.states, nil
}

func (repository *stubRewrapRepository) ListTenants(context.Context, string, int) ([]id.Tenant, error) {
	return nil, nil
}

func (repository *stubRewrapRepository) BeginGeneration(_ context.Context, class keyrewrap.Class, epoch keyrewrap.Epoch, at time.Time) (keyrewrap.State, error) {
	state := keyrewrap.State{Class: class, Epoch: epoch, Generation: 1, Status: keyrewrap.StatusRunning, StartedAt: at, UpdatedAt: at}
	repository.states = append(repository.states, state)
	return state, nil
}

func (repository *stubRewrapRepository) CommitBatch(context.Context, keyrewrap.State, keyrewrap.Batch) error {
	return nil
}

type stubRewrapAdapter struct{ class keyrewrap.Class }

func (adapter stubRewrapAdapter) Class() keyrewrap.Class { return adapter.class }
func (adapter stubRewrapAdapter) List(context.Context, tenant.Scope, string, int) ([]keyrewrap.Target, error) {
	return nil, nil
}
func (adapter stubRewrapAdapter) Rewrap(context.Context, keyrewrap.Target) (keyrewrap.Outcome, error) {
	return keyrewrap.Outcome{Changed: false}, nil
}

type stubWrapper struct{}

func (stubWrapper) Wrap(context.Context, kms.Purpose, []byte, []byte) (kms.WrappedKey, error) {
	return kms.NewWrappedKey(kms.WrappedKeyRecord{Provider: "test", Reference: "ref", Version: "v1", Algorithm: "TEST", Ciphertext: []byte{1}})
}

func (stubWrapper) Unwrap(context.Context, kms.Purpose, kms.WrappedKey, []byte) ([]byte, error) {
	return []byte{1}, nil
}

type rewrapTestRepository interface {
	keyrewrap.Repository
	keyrewrap.TenantSource
}

func mustKeyRewrapService(t *testing.T, repository rewrapTestRepository) *keyrewrap.Service {
	t.Helper()
	service, err := keyrewrap.NewService(repository, repository, []keyrewrap.Adapter{
		stubRewrapAdapter{class: keyrewrap.ClassHMACKey},
	}, stubWrapper{}, nil, time.Now)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	return service
}

type stubDestructionScanner struct {
	counts map[keycustody.ReferenceClass]int64
}

func (scanner stubDestructionScanner) Scan(context.Context, keycustody.DestructionTarget) (map[keycustody.ReferenceClass]int64, error) {
	return scanner.counts, nil
}

type stubDestructionRepository struct {
	verifications map[string]keycustody.VerificationReceipt
}

func (repository *stubDestructionRepository) RecordVerification(_ context.Context, receipt keycustody.VerificationReceipt) error {
	repository.verifications[receipt.ID] = receipt
	return nil
}

func (repository *stubDestructionRepository) Verification(_ context.Context, identifier string) (keycustody.VerificationReceipt, error) {
	receipt, ok := repository.verifications[identifier]
	if !ok {
		return keycustody.VerificationReceipt{}, keycustody.ErrNotFound
	}
	return receipt, nil
}

func (repository *stubDestructionRepository) RecordSchedule(context.Context, keycustody.DestructionSchedule) error {
	return nil
}

type stubRecoveryRepository struct {
	ceremonies map[string]keycustody.RecoveryCeremony
}

func (repository *stubRecoveryRepository) Create(_ context.Context, ceremony keycustody.RecoveryCeremony) (keycustody.RecoveryCeremony, error) {
	repository.ceremonies[ceremony.ID] = ceremony
	return ceremony, nil
}

func (repository *stubRecoveryRepository) Load(_ context.Context, identifier string) (keycustody.RecoveryCeremony, error) {
	ceremony, ok := repository.ceremonies[identifier]
	if !ok {
		return keycustody.RecoveryCeremony{}, keycustody.ErrNotFound
	}
	return ceremony, nil
}

func (repository *stubRecoveryRepository) Advance(_ context.Context, expected, next keycustody.RecoveryCeremony) (keycustody.RecoveryCeremony, error) {
	current, ok := repository.ceremonies[expected.ID]
	if !ok || current.Version != expected.Version {
		return keycustody.RecoveryCeremony{}, keycustody.ErrConflict
	}
	repository.ceremonies[next.ID] = next
	return next, nil
}

func mustKeyOperationRoutes(t *testing.T, fixture interface {
	Register(chi.Router)
}) chi.Router {
	t.Helper()
	router := chi.NewRouter()
	fixture.Register(router)

	return router
}

func TestKeyOperationRoutesRewrapStatusAndRun(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("kms:read"), access.Pattern("kms:write"))
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	destructionRepository := &stubDestructionRepository{verifications: map[string]keycustody.VerificationReceipt{}}
	destructionService, err := keycustody.NewDestructionService(stubDestructionScanner{}, destructionRepository, nil, generator, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	recoveryService, err := keycustody.NewRecoveryService(&stubRecoveryRepository{ceremonies: map[string]keycustody.RecoveryCeremony{}}, nil, nil, generator, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	repository := &stubRewrapRepository{states: []keyrewrap.State{{
		Class: keyrewrap.ClassHMACKey, Generation: 1, Status: keyrewrap.StatusRunning,
		Epoch: keyrewrap.Epoch{Key: "8fa2e84effb4128181f3f71db0c12fad609e704021422272eb96fb99637ccaa4"},
	}}}
	routes, err := NewKeyOperationsRoutes(fixture.middleware, mustKeyRewrapService(t, repository), destructionService, recoveryService, fixture.logger)
	if err != nil {
		t.Fatalf("NewKeyOperationsRoutes() error = %v", err)
	}
	router := mustKeyOperationRoutes(t, routes)

	status := supportRequest(t, router, fixture.encoded, http.MethodGet, "/kms/rewrap", "", "")
	if status.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status.Code, status.Body.String())
	}
	var statusBody struct {
		Classes []struct {
			Class string `json:"class"`
		} `json:"classes"`
	}
	if err := json.Unmarshal(status.Body.Bytes(), &statusBody); err != nil || len(statusBody.Classes) != 1 {
		t.Fatalf("status body = %s, error = %v", status.Body.String(), err)
	}
	run := supportRequest(t, router, fixture.encoded, http.MethodPost, "/kms/rewrap/run", `{"class":"keycustody.hmac","batch":2}`, "")
	if run.Code != http.StatusOK {
		t.Fatalf("run = %d, body = %s", run.Code, run.Body.String())
	}

	readOnly := newHTTPAccessFixture(t, nil, access.Pattern("kms:read"))
	readRoutes, err := NewKeyOperationsRoutes(readOnly.middleware, mustKeyRewrapService(t, repository), destructionService, recoveryService, readOnly.logger)
	if err != nil {
		t.Fatal(err)
	}
	readRouter := mustKeyOperationRoutes(t, readRoutes)
	denied := supportRequest(t, readRouter, readOnly.encoded, http.MethodPost, "/kms/rewrap/run", `{}`, "")
	if denied.Code != http.StatusForbidden {
		t.Fatalf("read-only run status = %d, want 403", denied.Code)
	}
}

func TestKeyOperationRoutesDestructionAndRecovery(t *testing.T) {
	t.Parallel()

	fixture := newHTTPAccessFixture(t, nil, access.Pattern("kms:read"), access.Pattern("kms:write"))
	generator, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	destructionRepository := &stubDestructionRepository{verifications: map[string]keycustody.VerificationReceipt{}}
	destructionService, err := keycustody.NewDestructionService(stubDestructionScanner{}, destructionRepository, nil, generator, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	recoveryService, err := keycustody.NewRecoveryService(&stubRecoveryRepository{ceremonies: map[string]keycustody.RecoveryCeremony{}}, nil, nil, generator, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	repository := &stubRewrapRepository{}
	routes, err := NewKeyOperationsRoutes(fixture.middleware, mustKeyRewrapService(t, repository), destructionService, recoveryService, fixture.logger)
	if err != nil {
		t.Fatal(err)
	}
	router := mustKeyOperationRoutes(t, routes)

	verify := supportRequest(t, router, fixture.encoded, http.MethodPost, "/kms/destruction-verifications",
		`{"provider":"test","reference":"ref","version":"v1","algorithm":"TEST","reason":"integration destruction"}`, "")
	if verify.Code != http.StatusOK {
		t.Fatalf("verify = %d, body = %s", verify.Code, verify.Body.String())
	}
	var receipt struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(verify.Body.Bytes(), &receipt); err != nil || receipt.State != "verified" {
		t.Fatalf("verify body = %s, error = %v", verify.Body.String(), err)
	}
	read := supportRequest(t, router, fixture.encoded, http.MethodGet, "/kms/destruction-verifications/"+receipt.ID, "", "")
	if read.Code != http.StatusOK {
		t.Fatalf("read receipt = %d, body = %s", read.Code, read.Body.String())
	}
	schedule := supportRequest(t, router, fixture.encoded, http.MethodPost,
		"/kms/destruction-verifications/"+receipt.ID+"/schedule",
		`{"window":"168h","record_only":true,"reason":"integration record only"}`, "")
	if schedule.Code != http.StatusOK {
		t.Fatalf("schedule = %d, body = %s", schedule.Code, schedule.Body.String())
	}

	start := supportRequest(t, router, fixture.encoded, http.MethodPost, "/kms/recovery-ceremonies",
		`{"kind":"migrate_epoch","class":"keycustody.hmac","target":{"provider":"test","reference":"ref","version":"v1","algorithm":"TEST"},"reason":"integration epoch migration"}`, "")
	if start.Code != http.StatusOK {
		t.Fatalf("recovery start = %d, body = %s", start.Code, start.Body.String())
	}
	var ceremony struct {
		ID      string `json:"id"`
		Version int64  `json:"version"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &ceremony); err != nil || ceremony.Version != 1 {
		t.Fatalf("recovery body = %s, error = %v", start.Body.String(), err)
	}
	// The authenticated principal cannot approve its own ceremony.
	self := supportRequest(t, router, fixture.encoded, http.MethodPost, "/kms/recovery-ceremonies/"+ceremony.ID+"/approve",
		`{"expected_version":1,"reason":"integration self approval"}`, "")
	if self.Code != http.StatusForbidden {
		t.Fatalf("self approval = %d, want 403; body = %s", self.Code, self.Body.String())
	}
}
