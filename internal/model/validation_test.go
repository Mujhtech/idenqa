package model_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type validationClock struct{ now time.Time }

func (source validationClock) Now() time.Time { return source.now }

type validationVerificationRepository struct{ record access.VerificationRecord }

func (repository validationVerificationRepository) FindForVerification(
	context.Context,
	id.Tenant,
	id.APIKey,
) (access.VerificationRecord, error) {
	return repository.record, nil
}

func validationAuthority(t testing.TB, pattern access.Pattern) (access.Context, id.Tenant) {
	t.Helper()
	now := time.Date(2026, time.September, 20, 10, 0, 0, 0, time.UTC)
	identifiers, err := id.NewGenerator(validationClock{now: now}, bytes.NewReader(bytes.Repeat([]byte{0x31}, 256)))
	if err != nil {
		t.Fatal(err)
	}
	tenantID, err := identifiers.NewTenant()
	if err != nil {
		t.Fatal(err)
	}
	keyID, err := identifiers.NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	generator, err := access.NewKeyGenerator(bytes.NewReader(bytes.Repeat([]byte{0x32}, 32)))
	if err != nil {
		t.Fatal(err)
	}
	presented, err := generator.Generate(tenantID, keyID)
	if err != nil {
		t.Fatal(err)
	}
	peppers, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{0x33}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	digest, version, err := peppers.Digest(presented)
	if err != nil {
		t.Fatal(err)
	}
	grant, err := access.TenantRegistry().Resolve(pattern)
	if err != nil {
		t.Fatal(err)
	}
	key, err := access.RestoreKey(access.KeyRecord{
		ID: keyID, TenantID: tenantID, Label: "model validation", Digest: digest,
		PepperVersion: version, Grant: grant, Version: 1, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	record, err := access.NewVerificationRecord(key, tenant.StateActive)
	if err != nil {
		t.Fatal(err)
	}
	authenticator, err := access.NewAuthenticator(validationVerificationRepository{record: record}, peppers, validationClock{now: now})
	if err != nil {
		t.Fatal(err)
	}
	authority, err := authenticator.Authenticate(t.Context(), presented.Reveal())
	if err != nil {
		t.Fatal(err)
	}
	return authority, tenantID
}

type validationRepository struct {
	model.RegistryRepository
	state       model.RegistryState
	stateErr    error
	revisions   map[string]model.RegistryRevision
	revisionErr error
	eligible    bool
}

func (repository *validationRepository) Get(context.Context, tenant.Scope, string) (model.RegistryState, error) {
	return repository.state, repository.stateErr
}

func (repository *validationRepository) Revision(_ context.Context, _ tenant.Scope, _, kind string, revision int64) (model.RegistryRevision, error) {
	if repository.revisionErr != nil {
		return model.RegistryRevision{}, repository.revisionErr
	}
	found, ok := repository.revisions[kind+"-"+strconv.FormatInt(revision, 10)]
	if !ok {
		return model.RegistryRevision{}, model.ErrRegistryNotFound
	}
	return found, nil
}

func (repository *validationRepository) RollbackEligible(context.Context, tenant.Scope, string, model.Deployment) (bool, error) {
	return repository.eligible, nil
}

func TestRegistryValidationReportsDocumentsAndCurrentState(t *testing.T) {
	t.Parallel()
	registration, threshold := registryFixture(t)
	deployment := model.Deployment{ModelRevision: 1, ThresholdRevision: 1, Region: "ng"}
	stored := map[string]model.RegistryRevision{
		"model-1":     {Kind: "model", Revision: 1, Registration: &registration},
		"threshold-1": {Kind: "threshold", Revision: 1, Thresholds: &threshold},
	}
	invalidThreshold := threshold
	invalidThreshold.Cutoff = 2
	mismatched := threshold
	mismatched.Provenance.RuntimeDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	mismatchedStored := map[string]model.RegistryRevision{
		"model-1":     stored["model-1"],
		"threshold-1": {Kind: "threshold", Revision: 1, Thresholds: &mismatched},
	}
	for _, test := range []struct {
		name      string
		request   model.ValidationRequest
		state     model.RegistryState
		stateErr  error
		revisions map[string]model.RegistryRevision
		eligible  bool
		accepted  bool
		codes     []model.ValidationCode
	}{
		{
			name:     "register new evaluation model",
			request:  model.ValidationRequest{Operation: "register", Reason: "evaluation", Registration: &registration},
			stateErr: model.ErrRegistryNotFound, accepted: true,
		},
		{
			name: "register production model",
			request: model.ValidationRequest{Operation: "register", Reason: "evaluation", Registration: func() *model.Registration {
				production := registration
				production.EvaluationOnly = false
				return &production
			}()},
			stateErr: model.ErrRegistryNotFound, codes: []model.ValidationCode{model.ValidationRegistrationInvalid},
		},
		{
			name:    "register missing document",
			request: model.ValidationRequest{Operation: "register", Reason: "evaluation"},
			codes:   []model.ValidationCode{model.ValidationCommandInvalid},
		},
		{
			name:    "register with foreign document",
			request: model.ValidationRequest{Operation: "register", Reason: "evaluation", Thresholds: &threshold},
			codes:   []model.ValidationCode{model.ValidationCommandInvalid},
		},
		{
			name:     "register stale version",
			request:  model.ValidationRequest{Operation: "register", Reason: "evaluation", ExpectedVersion: 4, Registration: &registration},
			stateErr: model.ErrRegistryNotFound, codes: []model.ValidationCode{model.ValidationVersionConflict},
		},
		{
			name:    "threshold out of range",
			request: model.ValidationRequest{Operation: "threshold", Reason: "evaluation", ExpectedVersion: 1, Thresholds: &invalidThreshold},
			state:   model.RegistryState{Name: "pad", Version: 1}, codes: []model.ValidationCode{model.ValidationThresholdInvalid},
		},
		{
			name:    "threshold valid",
			request: model.ValidationRequest{Operation: "threshold", Reason: "evaluation", ExpectedVersion: 1, Thresholds: &threshold},
			state:   model.RegistryState{Name: "pad", Version: 1}, accepted: true,
		},
		{
			name:     "activate valid",
			request:  model.ValidationRequest{Operation: "activate", Reason: "evaluation", ExpectedVersion: 2, Deployment: &deployment},
			state:    model.RegistryState{Name: "pad", Version: 2},
			accepted: true,
		},
		{
			name:    "activate missing revision",
			request: model.ValidationRequest{Operation: "activate", Reason: "evaluation", ExpectedVersion: 2, Deployment: &model.Deployment{ModelRevision: 9, ThresholdRevision: 1, Region: "ng"}},
			state:   model.RegistryState{Name: "pad", Version: 2}, codes: []model.ValidationCode{model.ValidationRevisionNotFound},
		},
		{
			name:    "activate unapproved region",
			request: model.ValidationRequest{Operation: "activate", Reason: "evaluation", ExpectedVersion: 2, Deployment: &model.Deployment{ModelRevision: 1, ThresholdRevision: 1, Region: "us"}},
			state:   model.RegistryState{Name: "pad", Version: 2}, codes: []model.ValidationCode{model.ValidationDeploymentInvalid},
		},
		{
			name:      "activate mismatched stored provenance",
			request:   model.ValidationRequest{Operation: "activate", Reason: "evaluation", ExpectedVersion: 2, Deployment: &deployment},
			state:     model.RegistryState{Name: "pad", Version: 2},
			revisions: mismatchedStored, codes: []model.ValidationCode{model.ValidationDeploymentInvalid},
		},
		{
			name:    "activate stale version",
			request: model.ValidationRequest{Operation: "activate", Reason: "evaluation", ExpectedVersion: 1, Deployment: &deployment},
			state:   model.RegistryState{Name: "pad", Version: 2}, codes: []model.ValidationCode{model.ValidationVersionConflict},
		},
		{
			name:     "activate unknown registry",
			request:  model.ValidationRequest{Operation: "activate", Reason: "evaluation", Deployment: &deployment},
			stateErr: model.ErrRegistryNotFound, codes: []model.ValidationCode{model.ValidationRegistryNotFound},
		},
		{
			name:    "rollback never active",
			request: model.ValidationRequest{Operation: "rollback", Reason: "evaluation", ExpectedVersion: 4, Deployment: &deployment},
			state:   model.RegistryState{Name: "pad", Version: 4}, codes: []model.ValidationCode{model.ValidationStateConflict},
		},
		{
			name:    "rollback previously active",
			request: model.ValidationRequest{Operation: "rollback", Reason: "evaluation", ExpectedVersion: 4, Deployment: &deployment},
			state:   model.RegistryState{Name: "pad", Version: 4}, eligible: true, accepted: true,
		},
		{
			name:    "retire inactive registry",
			request: model.ValidationRequest{Operation: "retire", Reason: "evaluation", ExpectedVersion: 3},
			state:   model.RegistryState{Name: "pad", Version: 3}, codes: []model.ValidationCode{model.ValidationStateConflict},
		},
		{
			name:    "retire active deployment",
			request: model.ValidationRequest{Operation: "retire", Reason: "evaluation", ExpectedVersion: 3},
			state:   model.RegistryState{Name: "pad", Version: 3, Active: &deployment}, accepted: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			authority, _ := validationAuthority(t, "models:write")
			revisions := test.revisions
			if revisions == nil {
				revisions = stored
			}
			repository := &validationRepository{state: test.state, stateErr: test.stateErr, eligible: test.eligible, revisions: revisions}
			report, err := testService(t, repository).Validate(t.Context(), authority, "pad", test.request)
			if err != nil {
				t.Fatal(err)
			}
			if report.Accepted != test.accepted || report.Operation != test.request.Operation {
				t.Fatalf("report = %+v, want accepted %t", report, test.accepted)
			}
			if !slices.Equal(report.ReasonCodes, test.codes) {
				t.Fatalf("reason codes = %v, want %v", report.ReasonCodes, test.codes)
			}
		})
	}
}

func TestRegistryValidationRequiresWriteScope(t *testing.T) {
	t.Parallel()
	authority, _ := validationAuthority(t, "models:read")
	registration, _ := registryFixture(t)
	if _, err := testService(t, &validationRepository{}).Validate(t.Context(), authority, "pad", model.ValidationRequest{Operation: "register", Reason: "evaluation", Registration: &registration}); !errors.Is(err, access.ErrInsufficientScope) {
		t.Fatalf("Validate() denial = %v", err)
	}
}

func TestRegistryValidationRejectsMalformedCommands(t *testing.T) {
	t.Parallel()
	authority, _ := validationAuthority(t, "models:write")
	registration, _ := registryFixture(t)
	service := testService(t, &validationRepository{stateErr: model.ErrRegistryNotFound})
	for _, test := range []struct {
		name    string
		request model.ValidationRequest
		code    model.ValidationCode
	}{
		{"invalid reason", model.ValidationRequest{Operation: "register", Reason: "Not A Code", Registration: &registration}, model.ValidationCommandInvalid},
		{"negative version", model.ValidationRequest{Operation: "retire", Reason: "evaluation", ExpectedVersion: -1}, model.ValidationCommandInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			report, err := service.Validate(t.Context(), authority, "pad", test.request)
			if err != nil {
				t.Fatal(err)
			}
			if report.Accepted || !slices.Equal(report.ReasonCodes, []model.ValidationCode{test.code}) {
				t.Fatalf("report = %+v", report)
			}
		})
	}
	for _, test := range []struct {
		name    string
		request model.ValidationRequest
	}{
		{"unknown operation", model.ValidationRequest{Operation: "promote", Reason: "evaluation", Registration: &registration}},
		{"missing operation", model.ValidationRequest{Reason: "evaluation", Registration: &registration}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := service.Validate(t.Context(), authority, "pad", test.request); !errors.Is(err, model.ErrRegistryInvalid) {
				t.Fatalf("Validate() error = %v", err)
			}
		})
	}
	if _, err := service.Validate(t.Context(), authority, "Pad", model.ValidationRequest{Operation: "retire", Reason: "evaluation"}); !errors.Is(err, model.ErrRegistryInvalid) {
		t.Fatalf("invalid name error = %v", err)
	}
}

func testService(t *testing.T, repository model.RegistryRepository) *model.Management {
	t.Helper()
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	service, err := model.NewManagement(repository, ids, time.Now, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return service
}
