package provider

import (
	"context"
	"encoding/json"
	"regexp"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// RegistrationRepository owns atomic registration state, append-only history
// and bounded operational reads.
type RegistrationRepository interface {
	Apply(context.Context, tenant.Scope, idempotency.Request, id.Event, RegistrationCommand) (RegistrationReceipt, error)
	Get(context.Context, tenant.Scope, string) (Registration, error)
	List(context.Context, tenant.Scope, *RegistrationPosition, int) (RegistrationPage, error)
	Health(context.Context, tenant.Scope, Registration) (RegistrationHealth, error)
}

type registrationIDs interface {
	NewEvent() (id.Event, error)
	NewProviderRegistration() (id.ProviderRegistration, error)
}

// HealthView derives the bounded readiness snapshot for one registration. It is
// owned by the consuming administration boundary and cached by its
// implementation.
type HealthView interface {
	RegistrationHealth(context.Context, tenant.Scope, Registration) (HealthSnapshot, error)
}

// RegistrationManagement authorizes every registration operation, including
// idempotent replay. Manifests are the configured deployment adapter
// advertisements; a registration may only target one of them.
type RegistrationManagement struct {
	repository RegistrationRepository
	ids        registrationIDs
	now        func() time.Time
	retention  time.Duration
	manifests  map[string]providerv1.Manifest
	health     HealthView
}

var registrationReason = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

// NewRegistrationManagement composes tenant provider administration without
// infrastructure types. Manifests are the configured deployment adapter
// advertisements; an empty catalogue makes writes fail closed while reads
// remain available.
func NewRegistrationManagement(repository RegistrationRepository, ids registrationIDs, now func() time.Time, retention time.Duration, manifests map[string]providerv1.Manifest) (*RegistrationManagement, error) {
	if repository == nil || ids == nil || now == nil || retention <= 0 {
		return nil, ErrRegistrationInvalid
	}
	validated := make(map[string]providerv1.Manifest, len(manifests))
	for adapter, manifest := range manifests {
		if adapter == "" || manifest.Validate() != nil || manifest.Package.AdapterID != adapter {
			return nil, ErrRegistrationInvalid
		}
		validated[adapter] = manifest
	}
	return &RegistrationManagement{repository: repository, ids: ids, now: now, retention: retention, manifests: validated}, nil
}

// Execute commits one version-checked registration command. Create is
// idempotent; mutations require the exact expected version.
func (service *RegistrationManagement) Execute(ctx context.Context, actor access.Context, key string, command RegistrationCommand) (RegistrationReceipt, error) {
	if err := actor.Require(access.PermissionProvidersWrite); err != nil {
		return RegistrationReceipt{}, err
	}
	if err := service.validateCommand(command); err != nil {
		return RegistrationReceipt{}, err
	}
	canonical := struct {
		Operation       string              `json:"operation"`
		RegistrationID  string              `json:"registration_id,omitempty"`
		ExpectedVersion int64               `json:"expected_version"`
		Write           *RegistrationWrite  `json:"write,omitempty"`
		Credential      *CredentialRotation `json:"credential,omitempty"`
		Reason          string              `json:"reason"`
	}{command.Operation, command.RegistrationID, command.ExpectedVersion, command.Write, command.Credential, command.Reason}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return RegistrationReceipt{}, ErrRegistrationInvalid
	}
	event, err := service.ids.NewEvent()
	if err != nil {
		return RegistrationReceipt{}, err
	}
	if key == "" {
		// Version-checked mutations carry their own concurrency proof; the
		// deterministic per-event key keeps the reservation path uniform
		// without claiming cross-request replay protection that the caller
		// did not request.
		key = "provider.command." + event.String()
	}
	request, err := idempotency.NewRequest(actor.TenantScope().ID(), actor.Principal().KeyID(), "providers."+command.Operation, key, raw, service.now().UTC().Truncate(time.Microsecond), service.retention)
	if err != nil {
		return RegistrationReceipt{}, err
	}
	if command.Operation == "create" {
		generated, err := service.ids.NewProviderRegistration()
		if err != nil {
			return RegistrationReceipt{}, err
		}
		command.RegistrationID = generated.String()
	}
	return service.repository.Apply(ctx, actor.TenantScope(), request, event, command)
}

// WithHealth attaches the bounded derived health view. Without it reads remain
// available with an explicit unknown state and no invented probe.
func (service *RegistrationManagement) WithHealth(health HealthView) *RegistrationManagement {
	if service != nil && health != nil {
		service.health = health
	}
	return service
}

// Get reads one authorized tenant registration.
func (service *RegistrationManagement) Get(ctx context.Context, actor access.Context, registrationID string) (Registration, error) {
	if err := actor.Require(access.PermissionProvidersRead); err != nil {
		return Registration{}, err
	}
	return service.repository.Get(ctx, actor.TenantScope(), registrationID)
}

// List reads one bounded newest-first page for an authorized tenant.
func (service *RegistrationManagement) List(ctx context.Context, actor access.Context, after *RegistrationPosition, limit int) (RegistrationPage, error) {
	if err := actor.Require(access.PermissionProvidersRead); err != nil {
		return RegistrationPage{}, err
	}
	if limit < 1 || limit > 100 {
		return RegistrationPage{}, ErrRegistrationInvalid
	}
	return service.repository.List(ctx, actor.TenantScope(), after, limit)
}

// Validate checks one closed write document against the configured deployment
// manifest without persistence.
func (service *RegistrationManagement) Validate(ctx context.Context, actor access.Context, write RegistrationWrite) (RegistrationValidationReport, error) {
	if err := actor.Require(access.PermissionProvidersWrite); err != nil {
		return RegistrationValidationReport{}, err
	}
	if err := ctx.Err(); err != nil {
		return RegistrationValidationReport{}, err
	}
	manifest, ok := service.manifests[write.AdapterID]
	if !ok {
		return RegistrationValidationReport{Accepted: false, ReasonCodes: []string{RegistrationAdapterUnknown}}, nil
	}
	return write.ValidateReport(manifest), nil
}

// Health reads a bounded snapshot of the tenant's own dispatch records for one
// registration plus the cached derived readiness snapshot. It never performs an
// external probe beyond the existing bounded runner health surface.
func (service *RegistrationManagement) Health(ctx context.Context, actor access.Context, registrationID string) (RegistrationHealth, error) {
	if err := actor.Require(access.PermissionProvidersRead); err != nil {
		return RegistrationHealth{}, err
	}
	registration, err := service.repository.Get(ctx, actor.TenantScope(), registrationID)
	if err != nil {
		return RegistrationHealth{}, err
	}
	health, err := service.repository.Health(ctx, actor.TenantScope(), registration)
	if err != nil {
		return RegistrationHealth{}, err
	}
	if service.health == nil {
		health.State = HealthUnknown
		health.ReasonCode = HealthReasonNoEvidence
		observed := service.now().UTC()
		health.ObservedAt = &observed
		return health, nil
	}
	snapshot, err := service.health.RegistrationHealth(ctx, actor.TenantScope(), registration)
	if err != nil {
		return RegistrationHealth{}, err
	}
	health.ApplySnapshot(snapshot)
	return health, nil
}

// SimulateFailure returns the pure owned operational classification preview for
// one bounded provider failure class and code. Nothing is dispatched or
// persisted and no identity outcome is ever produced.
func (service *RegistrationManagement) SimulateFailure(ctx context.Context, actor access.Context, registrationID string, class providerv1.FailureClass, code string) (FailureSimulation, error) {
	if err := actor.Require(access.PermissionProvidersRead); err != nil {
		return FailureSimulation{}, err
	}
	registration, err := service.repository.Get(ctx, actor.TenantScope(), registrationID)
	if err != nil {
		return FailureSimulation{}, err
	}
	if len(class) > 64 || registration.AdapterID == "" {
		return FailureSimulation{}, ErrRegistrationInvalid
	}
	return SimulateFailurePreview(class, code)
}

func (service *RegistrationManagement) validateCommand(command RegistrationCommand) error {
	if !registrationReason.MatchString(command.Reason) || command.ExpectedVersion < 0 {
		return ErrRegistrationInvalid
	}
	switch command.Operation {
	case "create":
		if command.ExpectedVersion != 0 || command.Write == nil || command.RegistrationID != "" {
			return ErrRegistrationInvalid
		}
		manifest, ok := service.manifests[command.Write.AdapterID]
		if !ok {
			return ErrRegistrationInvalid
		}
		return command.Write.Validate(manifest)
	case "update":
		if command.ExpectedVersion < 1 || command.Write == nil {
			return ErrRegistrationInvalid
		}
		if _, err := id.ParseProviderRegistration(command.RegistrationID); err != nil {
			return ErrRegistrationInvalid
		}
		manifest, ok := service.manifests[command.Write.AdapterID]
		if !ok {
			return ErrRegistrationInvalid
		}
		return command.Write.Validate(manifest)
	case "enable", "disable":
		if command.ExpectedVersion < 1 || command.Write != nil || command.Credential != nil {
			return ErrRegistrationInvalid
		}
		_, err := id.ParseProviderRegistration(command.RegistrationID)
		return err
	case "rotate-credential":
		if command.ExpectedVersion < 1 || command.Write != nil || command.Credential == nil {
			return ErrRegistrationInvalid
		}
		if _, err := id.ParseProviderRegistration(command.RegistrationID); err != nil {
			return ErrRegistrationInvalid
		}
		// The current version is re-checked under the registration lock; this
		// pass only proves the replacement reference is closed and bounded.
		return command.Credential.Validate()
	default:
		return ErrRegistrationInvalid
	}
}
