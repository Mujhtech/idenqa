package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

var (
	digestPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	identifierPattern  = regexp.MustCompile(`^[a-z]{3}_[0-9A-HJKMNP-TV-Z]{26}$`)
	traceparentPattern = regexp.MustCompile(`^[0-9a-f]{2}-[0-9a-f]{32}-[0-9a-f]{16}-[0-9a-f]{2}$`)
)

// ValidationError identifies one rejected public-contract field.
type ValidationError struct {
	Field  string
	Reason string
}

func (failure *ValidationError) Error() string {
	return fmt.Sprintf("provider contract: %s %s", failure.Field, failure.Reason)
}

func invalid(field, reason string) error { return &ValidationError{Field: field, Reason: reason} }

// Validate checks an immutable adapter manifest and rejects ambiguous entries.
func (manifest Manifest) Validate() error {
	if err := manifest.Package.validate(); err != nil {
		return err
	}
	if !validName(manifest.Configuration.ID, 160) || !digestPattern.MatchString(manifest.Configuration.Digest) {
		return invalid("configuration", "schema is invalid")
	}
	if len(manifest.Capabilities) == 0 || len(manifest.Capabilities) > 64 {
		return invalid("capabilities", "count is outside 1..64")
	}
	checks := make([]string, 0, len(manifest.Capabilities))
	for index, capability := range manifest.Capabilities {
		if err := capability.validate(); err != nil {
			return fmt.Errorf("provider contract: capabilities[%d]: %w", index, err)
		}
		checks = append(checks, capability.Check)
	}
	if !unique(checks) {
		return invalid("capabilities", "contain duplicate checks")
	}
	return manifest.Restrictions.validate()
}

func (provenance PackageProvenance) validate() error {
	if !CurrentVersion.Accepts(provenance.Contract) || !validName(provenance.AdapterID, 160) ||
		!validVersion(provenance.AdapterVersion) || !digestPattern.MatchString(provenance.PackageDigest) {
		return invalid("package", "provenance is invalid")
	}
	return nil
}

func (restrictions Restrictions) validate() error {
	if restrictions.MaximumGrants == 0 || restrictions.MaximumGrants > 32 ||
		restrictions.MaximumResultSize == 0 || restrictions.MaximumResultSize > MaxResultBytes ||
		restrictions.MaximumDuration <= 0 || restrictions.MaximumDuration > 10*time.Minute {
		return invalid("restrictions", "bounds are invalid")
	}
	return nil
}

func (capability Capability) validate() error {
	if !validName(capability.Check, 160) ||
		len(capability.AcceptedEvidence)+len(capability.AcceptedInputs) == 0 ||
		len(capability.AcceptedEvidence) > 32 || !validNames(capability.AcceptedEvidence, 160) ||
		len(capability.AcceptedInputs) > 32 || !validNames(capability.AcceptedInputs, 160) ||
		len(capability.AcceptedAssurances) > 32 || !validNames(capability.AcceptedAssurances, 160) ||
		len(capability.ProcessingRegions) == 0 || len(capability.ProcessingRegions) > 32 ||
		!validNames(capability.ProcessingRegions, 64) {
		return errors.New("capability is invalid")
	}
	if !unique(capability.AcceptedEvidence) || !unique(capability.AcceptedInputs) || !unique(capability.AcceptedAssurances) ||
		!unique(capability.ProcessingRegions) {
		return errors.New("capability sets contain duplicates")
	}
	return nil
}

// Validate rejects missing, malformed, or secret-bearing configuration data.
func (configuration ConfigurationReference) Validate() error {
	if !validID(configuration.ProviderID, "pvd") || !digestPattern.MatchString(configuration.SchemaDigest) ||
		!validReference(configuration.SecretReference) || !validName(configuration.CredentialVersion, 128) {
		return invalid("configuration", "reference is invalid")
	}
	return nil
}

// Validate checks one scoped evidence reference without resolving its secret grant.
func (grant EvidenceGrantReference) Validate() error {
	if !validID(grant.GrantID, "grt") || !validID(grant.RedemptionID, "rdm") ||
		!validID(grant.EvidenceID, "evd") || !validName(grant.Purpose, 160) ||
		!validName(grant.Variant, 160) || !validUTC(grant.ExpiresAt) {
		return invalid("evidence", "grant reference is invalid")
	}
	return nil
}

// Validate rejects inline, malformed, or ambiguous structured inputs.
func (input InputReference) Validate() error {
	if !validName(input.Name, 160) || !validReference(input.Reference) {
		return invalid("input", "reference is invalid")
	}
	return nil
}

// Validate checks W3C propagation fields. An absent context is permitted.
func (trace TraceContext) Validate() error {
	if trace.Traceparent == "" && trace.Tracestate == "" {
		return nil
	}
	if !traceparentPattern.MatchString(trace.Traceparent) || len(trace.Tracestate) > 512 ||
		containsControl(trace.Tracestate) {
		return invalid("trace", "context is invalid")
	}
	return nil
}

// Validate checks a complete immutable provider attempt envelope.
func (request Request) Validate() error {
	if !CurrentVersion.Accepts(request.Contract) || !validID(request.AttemptID, "atm") ||
		!validID(request.ProviderID, "pvd") || !validID(request.TenantID, "ten") ||
		!validID(request.VerificationID, "ver") || !validName(request.Check, 160) ||
		!validOpaque(request.IdempotencyKey, 16, 200) || !validUTC(request.Deadline) {
		return invalid("request", "identity, contract, or deadline is invalid")
	}
	if request.Configuration.ProviderID != request.ProviderID {
		return invalid("configuration.provider_id", "does not match request")
	}
	if err := request.Configuration.Validate(); err != nil {
		return err
	}
	if err := request.Adapter.validate(); err != nil {
		return err
	}
	if request.Adapter.Contract != request.Contract {
		return invalid("adapter.contract", "does not match request")
	}
	if err := request.Capability.validate(); err != nil {
		return err
	}
	if request.Capability.Check != request.Check {
		return invalid("capability.check", "does not match request")
	}
	if err := request.Restrictions.validate(); err != nil {
		return err
	}
	if len(request.Evidence) > int(request.Restrictions.MaximumGrants) {
		return invalid("evidence", "exceeds pinned restriction")
	}
	if len(request.Inputs) > 32 {
		return invalid("inputs", "count exceeds 32")
	}
	inputNames := make([]string, 0, len(request.Inputs))
	for _, input := range request.Inputs {
		if err := input.Validate(); err != nil {
			return err
		}
		inputNames = append(inputNames, input.Name)
		if !slices.Contains(request.Capability.AcceptedInputs, input.Name) {
			return invalid("inputs", "contains a name outside the pinned capability")
		}
	}
	if !unique(inputNames) {
		return invalid("inputs", "contains duplicate names")
	}
	if request.Contract.Minor == 0 && len(request.Inputs) > 0 {
		return invalid("inputs", "require provider contract v1.1")
	}
	if len(request.Evidence) > 32 || (len(request.Evidence) == 0 && len(request.Inputs) == 0) {
		return invalid("request", "requires evidence or structured input")
	}
	grantIDs := make([]string, 0, len(request.Evidence))
	for _, grant := range request.Evidence {
		if err := grant.Validate(); err != nil {
			return err
		}
		grantIDs = append(grantIDs, grant.GrantID)
	}
	if !unique(grantIDs) {
		return invalid("evidence", "contains duplicate grants")
	}
	return request.Trace.Validate()
}

// Validate checks result binding, stable classifications, and encoded size.
func (result Result) Validate() error {
	if !CurrentVersion.Accepts(result.Contract) || !validID(result.AttemptID, "atm") ||
		!validUTC(result.CompletedAt) || len(result.Signals) > 64 {
		return invalid("result", "identity, contract, time, or signal count is invalid")
	}
	switch result.Outcome {
	case ResultOutcomeCompleted:
		if result.Failure != nil || len(result.Signals) == 0 {
			return invalid("result", "completed outcome requires signals and no failure")
		}
	case ResultOutcomeFailed:
		if result.Failure == nil || len(result.Signals) != 0 {
			return invalid("result", "failed outcome requires one failure and no signals")
		}
		if err := result.Failure.validate(); err != nil {
			return err
		}
	default:
		return invalid("result.outcome", "is invalid")
	}
	for _, signal := range result.Signals {
		if err := signal.validate(); err != nil {
			return err
		}
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("provider contract: encode result: %w", err)
	}
	if len(encoded) > MaxResultBytes {
		return invalid("result", "exceeds maximum encoded size")
	}
	return nil
}

// ValidateForRequest additionally proves exact attempt and version binding.
func (result Result) ValidateForRequest(request Request) error {
	if err := request.Validate(); err != nil {
		return err
	}
	if err := result.Validate(); err != nil {
		return err
	}
	if result.AttemptID != request.AttemptID || result.Contract != request.Contract {
		return invalid("result", "does not bind the exact request")
	}
	return nil
}

func (signal Signal) validate() error {
	if !validName(signal.Name, 160) || len(signal.ReasonCodes) > 32 ||
		!validNames(signal.ReasonCodes, 160) || !unique(signal.ReasonCodes) {
		return invalid("signal", "is invalid")
	}
	switch signal.Outcome {
	case SignalOutcomeSatisfied, SignalOutcomeNotSatisfied, SignalOutcomeInconclusive:
		return nil
	default:
		return invalid("signal.outcome", "is invalid")
	}
}

func (failure Failure) validate() error {
	if !validName(failure.Code, 160) {
		return invalid("failure.code", "is invalid")
	}
	switch failure.Class {
	case FailureInvalidRequest, FailureUnauthenticated, FailureUnauthorized, FailureUnsupported,
		FailureUnavailable, FailureRateLimited, FailureDeadline, FailureCancelled,
		FailureProviderRejected, FailureInternal:
	default:
		return invalid("failure.class", "is invalid")
	}
	switch failure.Retry {
	case RetryNever:
		if failure.RetryAfter != 0 {
			return invalid("failure.retry_after", "must be absent for a non-retryable failure")
		}
	case RetryBackoff, RetryReconcile:
		if failure.RetryAfter < 0 || failure.RetryAfter > time.Hour {
			return invalid("failure.retry_after", "is invalid")
		}
	default:
		return invalid("failure.retry", "is invalid")
	}
	return nil
}

// Validate checks a safe health response.
func (health Health) Validate() error {
	if !validName(health.Code, 160) || !validUTC(health.CheckedAt) {
		return invalid("health", "is invalid")
	}
	switch health.State {
	case HealthReady, HealthDegraded, HealthNotReady:
		return nil
	default:
		return invalid("health.state", "is invalid")
	}
}

func validID(value, prefix string) bool {
	return identifierPattern.MatchString(value) && strings.HasPrefix(value, prefix+"_")
}

func validName(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.TrimSpace(value) != value || containsControl(value) {
		return false
	}
	for _, character := range value {
		if unicode.IsSpace(character) {
			return false
		}
	}
	return true
}

func validNames(values []string, maximum int) bool {
	return slices.IndexFunc(values, func(value string) bool { return !validName(value, maximum) }) == -1
}

func validVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func validReference(value string) bool {
	return strings.HasPrefix(value, "secret://") && validOpaque(value, 10, 512)
}

func validOpaque(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum && strings.TrimSpace(value) == value && !containsControl(value)
}

func validUTC(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}

func unique(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}
