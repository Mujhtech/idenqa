package provider

import (
	"errors"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// Tenant provider registration errors deliberately conceal absent and
// cross-tenant records.
var (
	ErrRegistrationInvalid  = errors.New("provider: invalid tenant provider registration")
	ErrRegistrationConflict = errors.New("provider: registration version conflict")
	ErrRegistrationNotFound = errors.New("provider: registration not found")
)

// Stable registration-validation reason codes.
const (
	RegistrationAccepted             = "accepted"
	RegistrationSecretMaterial       = "secret_material"
	RegistrationAdapterUnknown       = "adapter_unknown"
	RegistrationAdapterMismatch      = "adapter_mismatch"
	RegistrationRegionInvalid        = "region_invalid"
	RegistrationConfigurationInvalid = "configuration_invalid"
	RegistrationSchemaMismatch       = "schema_mismatch"
	RegistrationInputsInvalid        = "inputs_invalid"
	RegistrationSelfieInvalid        = "selfie_invalid"
	RegistrationRestrictionsInvalid  = "restrictions_invalid"
)

var registrationToken = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

// RegistrationWrite is the closed secret-free write document for one tenant
// provider registration. It never carries a credential value: configuration
// and structured inputs are external references only.
type RegistrationWrite struct {
	AdapterID         string                            `json:"adapter_id"`
	Region            string                            `json:"region"`
	Configuration     providerv1.ConfigurationReference `json:"configuration"`
	Inputs            []providerv1.InputReference       `json:"inputs,omitempty"`
	SelfieRequirement string                            `json:"selfie_requirement,omitempty"`
	Restrictions      *providerv1.Restrictions          `json:"restrictions,omitempty"`
}

// Registration is one tenant-owned, secret-free provider route registration.
// The configuration names externally resolved credentials; Core never stores a
// credential value. Version is the optimistic-concurrency pointer.
type Registration struct {
	ID                string                            `json:"id"`
	TenantID          string                            `json:"-"`
	AdapterID         string                            `json:"adapter_id"`
	Region            string                            `json:"region"`
	Configuration     providerv1.ConfigurationReference `json:"configuration"`
	Inputs            []providerv1.InputReference       `json:"inputs,omitempty"`
	SelfieRequirement string                            `json:"selfie_requirement,omitempty"`
	Restrictions      *providerv1.Restrictions          `json:"restrictions,omitempty"`
	Enabled           bool                              `json:"enabled"`
	Version           int64                             `json:"version"`
	ActorID           string                            `json:"actor_id"`
	CreatedAt         time.Time                         `json:"created_at"`
	UpdatedAt         time.Time                         `json:"updated_at"`
}

// CredentialRotation is one closed credential-reference rotation for a tenant
// provider registration. It carries a new versioned secret reference and never
// credential material. In-flight requests keep their persisted pin; subsequent
// dispatches use the rotated version.
type CredentialRotation struct {
	SecretReference   string `json:"secret_reference"`
	CredentialVersion string `json:"credential_version"`
}

// RegistrationCommand is one closed action on a tenant provider registration.
type RegistrationCommand struct {
	Operation       string              `json:"operation"`
	RegistrationID  string              `json:"registration_id,omitempty"`
	ExpectedVersion int64               `json:"expected_version"`
	Write           *RegistrationWrite  `json:"write,omitempty"`
	Credential      *CredentialRotation `json:"credential,omitempty"`
	Reason          string              `json:"reason"`
}

// Validate checks one rotation and rejects credential-bearing or malformed
// replacement references. The caller compares the replacement against the
// locked current version.
func (rotation CredentialRotation) Validate() error {
	if !validReference(rotation.SecretReference) || !validCredentialVersion(rotation.CredentialVersion) {
		return ErrRegistrationInvalid
	}
	return nil
}

// ChangedFrom reports whether the rotation advances the current reference or
// version. A rotation to the identical pair is a no-op conflict.
func (rotation CredentialRotation) ChangedFrom(current CredentialRotation) bool {
	return rotation != current
}

func validReference(value string) bool {
	if !strings.HasPrefix(value, "secret://") || len(value) < 10 || len(value) > 512 || strings.TrimSpace(value) != value {
		return false
	}
	return !strings.ContainsFunc(value, unicode.IsControl)
}

func validCredentialVersion(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

// RegistrationReceipt preserves the exact safe result and original actor.
type RegistrationReceipt struct {
	Registration Registration `json:"registration"`
	Operation    string       `json:"operation"`
	Reason       string       `json:"reason"`
	Replayed     bool         `json:"replayed"`
}

// RegistrationPosition is one opaque stable list position.
type RegistrationPosition struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

// RegistrationPage is one bounded tenant registration page.
type RegistrationPage struct {
	Registrations []Registration
	Next          *RegistrationPosition
}

// RegistrationValidationReport is a bounded side-effect-free validation result.
type RegistrationValidationReport struct {
	Accepted    bool     `json:"accepted"`
	ReasonCodes []string `json:"reason_codes"`
}

// RegistrationHealthFailure is the last normalized operational failure.
type RegistrationHealthFailure struct {
	Class      string    `json:"class"`
	Code       string    `json:"code,omitempty"`
	RecordedAt time.Time `json:"recorded_at"`
}

// RegistrationHealth is a bounded read over the tenant's own provider request
// and dispatch records for one registration plus the derived rolling-window
// readiness snapshot. It is not an invented probe.
type RegistrationHealth struct {
	RegistrationID   string                     `json:"registration_id"`
	AdapterID        string                     `json:"adapter_id"`
	Requests         int64                      `json:"requests"`
	Pending          int64                      `json:"pending_dispatches"`
	Completed        int64                      `json:"completed_dispatches"`
	Failed           int64                      `json:"failed_dispatches"`
	LastOutcome      string                     `json:"last_outcome,omitempty"`
	LastFailure      *RegistrationHealthFailure `json:"last_failure,omitempty"`
	LastActivityAt   *time.Time                 `json:"last_activity_at,omitempty"`
	State            HealthState                `json:"state"`
	ReasonCode       string                     `json:"reason_code"`
	ObservedAt       *time.Time                 `json:"observed_at,omitempty"`
	WindowSeconds    int64                      `json:"window_seconds,omitempty"`
	WindowCompleted  int64                      `json:"window_completed_dispatches,omitempty"`
	WindowFailed     int64                      `json:"window_failed_dispatches,omitempty"`
	FailureRatio     float64                    `json:"failure_ratio,omitempty"`
	FailureClasses   []FailureClassCount        `json:"failure_classes,omitempty"`
	AsyncUnresolved  int64                      `json:"async_unresolved_dispatches,omitempty"`
	AsyncExpired     int64                      `json:"async_expired_dispatches,omitempty"`
	CallbacksAdopted int64                      `json:"callbacks_adopted,omitempty"`
	BreakerState     BreakerState               `json:"breaker_state,omitempty"`
	BreakerSince     *time.Time                 `json:"breaker_since,omitempty"`
	Stale            bool                       `json:"stale,omitempty"`
	Continuity       bool                       `json:"continuity,omitempty"`
}

// ApplySnapshot merges one derived bounded health snapshot into the legacy
// bounded read without changing its evidence meaning.
func (health *RegistrationHealth) ApplySnapshot(snapshot HealthSnapshot) {
	if health == nil {
		return
	}
	health.State = HealthState(snapshot.State.Safe())
	health.ReasonCode = snapshot.ReasonCode
	observed := snapshot.ObservedAt.UTC()
	health.ObservedAt = &observed
	health.WindowSeconds = int64(snapshot.Window / time.Second)
	health.WindowCompleted = snapshot.Completed
	health.WindowFailed = snapshot.Failed
	health.FailureRatio = snapshot.FailureRatio
	health.FailureClasses = slices.Clone(snapshot.FailureClasses)
	health.AsyncUnresolved = snapshot.AsyncUnresolved
	health.AsyncExpired = snapshot.AsyncExpired
	health.CallbacksAdopted = snapshot.CallbacksAdopted
	health.BreakerState = snapshot.Breaker
	health.BreakerSince = snapshot.BreakerSince
	health.Stale = snapshot.Stale
	health.Continuity = snapshot.Continuity
}

// FailureSimulation is the pure operational classification preview for one
// bounded provider failure. It never produces an identity outcome.
type FailureSimulation struct {
	Class                   string `json:"class"`
	Code                    string `json:"code"`
	Retry                   string `json:"retry"`
	RetryAfterSeconds       int64  `json:"retry_after_seconds"`
	AttemptState            string `json:"attempt_state"`
	CheckState              string `json:"check_state"`
	ProducesIdentityOutcome bool   `json:"produces_identity_outcome"`
}

// Validate checks the write document against one configured deployment adapter
// manifest and rejects any credential-bearing or secret material.
func (write RegistrationWrite) Validate(manifest providerv1.Manifest) error {
	if !write.ValidateReport(manifest).Accepted {
		return ErrRegistrationInvalid
	}
	return nil
}

// ValidateReport returns the stable bounded reason codes for one write
// document. It is side-effect-free and never echoes supplied values.
func (write RegistrationWrite) ValidateReport(manifest providerv1.Manifest) RegistrationValidationReport {
	report := RegistrationValidationReport{Accepted: false, ReasonCodes: []string{}}
	reject := func(code string) RegistrationValidationReport {
		report.ReasonCodes = append(report.ReasonCodes, code)
		return report
	}
	if containsSecretMaterial(write) {
		return reject(RegistrationSecretMaterial)
	}
	if manifest.Validate() != nil {
		return reject(RegistrationAdapterUnknown)
	}
	if write.AdapterID == "" || write.Region == "" || write.Configuration.ProviderID == "" {
		return reject(RegistrationConfigurationInvalid)
	}
	if write.AdapterID != manifest.Package.AdapterID {
		return reject(RegistrationAdapterMismatch)
	}
	if !registrationToken.MatchString(write.Region) {
		return reject(RegistrationRegionInvalid)
	}
	if write.Configuration.Validate() != nil {
		return reject(RegistrationConfigurationInvalid)
	}
	if write.Configuration.SchemaDigest != manifest.Configuration.Digest {
		return reject(RegistrationSchemaMismatch)
	}
	if !validRegistrationInputs(write.Inputs) {
		return reject(RegistrationInputsInvalid)
	}
	if manifest.Package.AdapterID == providerv1.AdapterSmileID {
		if len(write.Inputs) != 2 || write.SelfieRequirement == "" || !registrationToken.MatchString(write.SelfieRequirement) {
			return reject(RegistrationSelfieInvalid)
		}
		names := map[string]bool{}
		for _, input := range write.Inputs {
			if input.Name != "idenqa.input.country" && input.Name != "idenqa.input.id_type" {
				return reject(RegistrationInputsInvalid)
			}
			if names[input.Name] {
				return reject(RegistrationInputsInvalid)
			}
			names[input.Name] = true
		}
	} else if len(write.Inputs) != 0 || write.SelfieRequirement != "" {
		return reject(RegistrationInputsInvalid)
	}
	if write.Restrictions != nil && !validRegistrationRestrictions(*write.Restrictions, manifest.Restrictions) {
		return reject(RegistrationRestrictionsInvalid)
	}
	report.Accepted = true
	report.ReasonCodes = append(report.ReasonCodes, RegistrationAccepted)
	return report
}

// Validate re-checks a persisted registration against the configured manifest.
func (registration Registration) Validate(manifest providerv1.Manifest) error {
	if _, err := id.ParseProviderRegistration(registration.ID); err != nil {
		return ErrRegistrationInvalid
	}
	if _, err := id.ParseTenant(registration.TenantID); err != nil {
		return ErrRegistrationInvalid
	}
	write := registration.Write()
	return write.Validate(manifest)
}

// Write projects the mutable secret-free fields of a persisted registration.
func (registration Registration) Write() RegistrationWrite {
	return RegistrationWrite{AdapterID: registration.AdapterID, Region: registration.Region, Configuration: registration.Configuration,
		Inputs: registration.Inputs, SelfieRequirement: registration.SelfieRequirement, Restrictions: registration.Restrictions}
}

// Apply overlays one registration onto an exact deployment route binding.
// Tenant, policy, profile, document requirement, purpose and recipient remain
// the deployment route's meaning; only the registered adapter configuration,
// region, inputs and selfie requirement are replaced. The result is validated
// by NewPlan, which pins the adapter configuration digest.
func (registration Registration) Apply(template Binding) (Binding, bool) {
	if registration.AdapterID == "" || registration.Region != template.Region || registration.Configuration.Validate() != nil || len(registration.Inputs) > 2 {
		return Binding{}, false
	}
	for _, input := range registration.Inputs {
		if input.Validate() != nil {
			return Binding{}, false
		}
	}
	if registration.SelfieRequirement != "" && !registrationToken.MatchString(registration.SelfieRequirement) {
		return Binding{}, false
	}
	binding := template
	binding.Configuration = registration.Configuration
	binding.Inputs = slices.Clone(registration.Inputs)
	binding.SelfieRequirement = registration.SelfieRequirement
	return binding, true
}

// NewRegisteredPlan composes one deployment manifest with an enabled tenant
// registration using the same validation and configuration digest pin as the
// deployment route. Registration restrictions only ever tighten the
// deployment manifest restrictions.
func NewRegisteredPlan(registration Registration, template Binding, manifest providerv1.Manifest) (*Plan, error) {
	if registration.Restrictions != nil && !validRegistrationRestrictions(*registration.Restrictions, manifest.Restrictions) {
		return nil, ErrRegistrationInvalid
	}
	binding, ok := registration.Apply(template)
	if !ok {
		return nil, ErrRegistrationInvalid
	}
	plan, err := NewPlan(binding, manifest)
	if err != nil {
		return nil, ErrRegistrationInvalid
	}
	if registration.Restrictions != nil {
		plan.Manifest.Restrictions = tightenRestrictions(manifest.Restrictions, *registration.Restrictions)
	}
	return plan, nil
}

// Selected reports the enabled registration for one tenant/adapter/region.
// When region is empty it returns the tenant's single enabled registration for
// the adapter; more than one enabled registration is ambiguous and returns
// false rather than silently broadening the route. Callers must not infer
// capability or assurance beyond the manifest the registration was validated
// against.
func Selected(registrations []Registration, adapter, region string) (Registration, bool) {
	if adapter == "" {
		return Registration{}, false
	}
	var matched Registration
	count := 0
	for _, registration := range registrations {
		if !registration.Enabled || registration.AdapterID != adapter {
			continue
		}
		if region != "" && registration.Region != region {
			continue
		}
		matched = registration
		count++
	}
	if count != 1 {
		return Registration{}, false
	}
	return matched, true
}

// HealthLookup returns the bounded readiness state for one candidate
// registration. Unknown states are permitted and are never silently excluded.
type HealthLookup func(Registration) HealthState

// SelectHealthy selects one enabled registration for the exact adapter and
// region, excluding not_ready registrations and preferring ready over degraded
// ones. An empty region defers to Selected so health never switches a route
// across regions. Ambiguity still returns false instead of silently broadening
// the route.
func SelectHealthy(registrations []Registration, adapter, region string, health HealthLookup) (Registration, bool) {
	if health == nil || region == "" {
		return Selected(registrations, adapter, region)
	}
	candidates := make([]Registration, 0, len(registrations))
	for _, registration := range registrations {
		if !registration.Enabled || registration.AdapterID != adapter || registration.Region != region {
			continue
		}
		if health(registration).Safe() == string(HealthNotReady) {
			continue
		}
		candidates = append(candidates, registration)
	}
	if len(candidates) == 0 {
		return Registration{}, false
	}
	ready := make([]Registration, 0, len(candidates))
	for _, registration := range candidates {
		if health(registration).Safe() == string(HealthReady) {
			ready = append(ready, registration)
		}
	}
	if len(ready) > 0 {
		candidates = ready
	}
	if len(candidates) != 1 {
		return Registration{}, false
	}
	return candidates[0], true
}

// SimulateFailurePreview maps one bounded provider failure class to the owned
// operational attempt/check state. Retry dispositions mirror the adapter
// normalisation defaults; the result never carries an identity conclusion.
func SimulateFailurePreview(class providerv1.FailureClass, code string) (FailureSimulation, error) {
	if !registrationToken.MatchString(code) {
		return FailureSimulation{}, ErrRegistrationInvalid
	}
	result := FailureSimulation{Class: string(class), Code: code, Retry: string(providerv1.RetryNever), AttemptState: "failed", CheckState: "failed", ProducesIdentityOutcome: false}
	switch class {
	case providerv1.FailureInvalidRequest, providerv1.FailureUnauthenticated, providerv1.FailureUnauthorized,
		providerv1.FailureUnsupported, providerv1.FailureProviderRejected, providerv1.FailureInternal:
	case providerv1.FailureRateLimited, providerv1.FailureUnavailable:
		result.Retry = string(providerv1.RetryBackoff)
		result.RetryAfterSeconds = 1
	case providerv1.FailureDeadline:
		result.Retry = string(providerv1.RetryReconcile)
		result.AttemptState, result.CheckState = "timed_out", "timed_out"
	case providerv1.FailureCancelled:
		result.Retry = string(providerv1.RetryReconcile)
		result.AttemptState, result.CheckState = "cancelled", "cancelled"
	default:
		return FailureSimulation{}, ErrRegistrationInvalid
	}
	return result, nil
}

func validRegistrationInputs(inputs []providerv1.InputReference) bool {
	if len(inputs) > 2 {
		return false
	}
	for _, input := range inputs {
		if input.Validate() != nil {
			return false
		}
	}
	return true
}

func validRegistrationRestrictions(restrictions, manifest providerv1.Restrictions) bool {
	return restrictions.MaximumGrants > 0 && restrictions.MaximumGrants <= manifest.MaximumGrants &&
		restrictions.MaximumResultSize > 0 && restrictions.MaximumResultSize <= manifest.MaximumResultSize &&
		restrictions.MaximumDuration > 0 && restrictions.MaximumDuration <= manifest.MaximumDuration
}

func tightenRestrictions(manifest providerv1.Restrictions, registration providerv1.Restrictions) providerv1.Restrictions {
	return providerv1.Restrictions{NetworkRequired: manifest.NetworkRequired || registration.NetworkRequired,
		MaximumGrants:     min(manifest.MaximumGrants, registration.MaximumGrants),
		MaximumResultSize: min(manifest.MaximumResultSize, registration.MaximumResultSize),
		MaximumDuration:   min(manifest.MaximumDuration, registration.MaximumDuration)}
}

// containsSecretMaterial rejects obvious credential shapes in text fields as
// defence in depth. The closed reference shapes already make credentials
// unrepresentable; this check keeps operator mistakes explicit.
func containsSecretMaterial(write RegistrationWrite) bool {
	values := []string{write.AdapterID, write.Region, write.SelfieRequirement, write.Configuration.ProviderID, write.Configuration.SecretReference, write.Configuration.CredentialVersion}
	for _, input := range write.Inputs {
		values = append(values, input.Name, input.Reference)
	}
	for _, value := range values {
		lower := strings.ToLower(value)
		if strings.HasPrefix(lower, "bearer ") || strings.HasPrefix(lower, "basic ") || strings.Contains(lower, "-----begin") ||
			strings.Contains(lower, "password") || strings.Contains(lower, "api_key") || strings.Contains(lower, "apikey") ||
			strings.Contains(lower, "secret_key") || strings.Contains(lower, "private_key") || strings.Contains(lower, "client_secret") {
			return true
		}
	}
	return false
}
