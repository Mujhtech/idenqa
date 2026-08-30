package model

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
type ValidationError struct{ Field, Reason string }

func (failure *ValidationError) Error() string {
	return fmt.Sprintf("model contract: %s %s", failure.Field, failure.Reason)
}

func invalid(field, reason string) error { return &ValidationError{Field: field, Reason: reason} }

// Validate checks an immutable model manifest.
func (manifest Manifest) Validate() error {
	if err := manifest.Provenance.validate(); err != nil {
		return err
	}
	if len(manifest.Capabilities) == 0 || len(manifest.Capabilities) > 64 {
		return invalid("capabilities", "count is outside 1..64")
	}
	evaluations := make([]string, 0, len(manifest.Capabilities))
	for index, capability := range manifest.Capabilities {
		if err := capability.validate(); err != nil {
			return fmt.Errorf("model contract: capabilities[%d]: %w", index, err)
		}
		evaluations = append(evaluations, capability.Evaluation)
	}
	if !unique(evaluations) {
		return invalid("capabilities", "contain duplicate evaluations")
	}
	return manifest.Restrictions.validate()
}

func (provenance Provenance) validate() error {
	if !CurrentVersion.Accepts(provenance.Contract) || !validName(provenance.ModelID) ||
		!validVersion(provenance.ModelVersion) || !digestPattern.MatchString(provenance.ModelDigest) ||
		!digestPattern.MatchString(provenance.RuntimeDigest) ||
		!digestPattern.MatchString(provenance.PreprocessingDigest) ||
		!digestPattern.MatchString(provenance.OutputSchemaDigest) {
		return invalid("provenance", "is invalid")
	}
	return nil
}

func (capability Capability) validate() error {
	if !validName(capability.Evaluation) || len(capability.AcceptedEvidence) == 0 ||
		len(capability.AcceptedEvidence) > 32 || !validNames(capability.AcceptedEvidence) ||
		len(capability.RequiredAssurances) > 32 || !validNames(capability.RequiredAssurances) ||
		len(capability.OutputSignals) == 0 || len(capability.OutputSignals) > 64 ||
		!validNames(capability.OutputSignals) || !unique(capability.AcceptedEvidence) ||
		!unique(capability.RequiredAssurances) || !unique(capability.OutputSignals) {
		return errors.New("capability is invalid")
	}
	return nil
}

func (restrictions Restrictions) validate() error {
	if restrictions.MaximumGrants == 0 || restrictions.MaximumGrants > 32 ||
		restrictions.MaximumInputBytes == 0 || restrictions.MaximumInputBytes > 64*1024*1024 ||
		restrictions.MaximumResultSize == 0 || restrictions.MaximumResultSize > MaxResultBytes ||
		restrictions.MaximumDuration <= 0 || restrictions.MaximumDuration > 10*time.Minute {
		return invalid("restrictions", "bounds are invalid")
	}
	return nil
}

// Validate checks a reference-only model configuration.
func (configuration ConfigurationReference) Validate() error {
	if !validID(configuration.ModelID, "mdl") ||
		!digestPattern.MatchString(configuration.ConfigurationDigest) ||
		!strings.HasPrefix(configuration.ConfigurationRef, "configuration://") ||
		!validOpaque(configuration.ConfigurationRef, 17, 512) {
		return invalid("configuration", "reference is invalid")
	}
	return nil
}

func (grant EvidenceGrantReference) validate() error {
	if !validID(grant.GrantID, "grt") || !validID(grant.RedemptionID, "rdm") ||
		!validID(grant.EvidenceID, "evd") || !validName(grant.Purpose) ||
		!validName(grant.Variant) || !validUTC(grant.ExpiresAt) {
		return invalid("evidence", "grant reference is invalid")
	}
	return nil
}

func (trace TraceContext) validate() error {
	if trace.Traceparent == "" && trace.Tracestate == "" {
		return nil
	}
	if !traceparentPattern.MatchString(trace.Traceparent) || len(trace.Tracestate) > 512 ||
		containsControl(trace.Tracestate) {
		return invalid("trace", "context is invalid")
	}
	return nil
}

// Validate checks a complete immutable model execution envelope.
func (request Request) Validate() error {
	if !CurrentVersion.Accepts(request.Contract) || !validID(request.AttemptID, "atm") ||
		!validID(request.ModelID, "mdl") || !validID(request.TenantID, "ten") ||
		!validID(request.VerificationID, "ver") || !validName(request.Evaluation) ||
		!validOpaque(request.IdempotencyKey, 16, 200) || !validUTC(request.Deadline) {
		return invalid("request", "identity, contract, or deadline is invalid")
	}
	if err := request.Provenance.validate(); err != nil {
		return err
	}
	if request.Provenance.Contract != request.Contract {
		return invalid("provenance.contract", "does not match request")
	}
	if err := request.Capability.validate(); err != nil {
		return err
	}
	if request.Capability.Evaluation != request.Evaluation {
		return invalid("capability.evaluation", "does not match request")
	}
	if err := request.Restrictions.validate(); err != nil {
		return err
	}
	if err := request.Configuration.Validate(); err != nil {
		return err
	}
	if request.Configuration.ModelID != request.ModelID || len(request.Evidence) == 0 ||
		len(request.Evidence) > int(request.Restrictions.MaximumGrants) {
		return invalid("request", "configuration or evidence binding is invalid")
	}
	grants := make([]string, 0, len(request.Evidence))
	for _, grant := range request.Evidence {
		if err := grant.validate(); err != nil {
			return err
		}
		grants = append(grants, grant.GrantID)
	}
	if !unique(grants) {
		return invalid("evidence", "contains duplicate grants")
	}
	return request.Trace.validate()
}

// Validate checks result binding, classifications, and encoded size.
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
		return fmt.Errorf("model contract: encode result: %w", err)
	}
	if len(encoded) > MaxResultBytes {
		return invalid("result", "exceeds maximum encoded size")
	}
	return nil
}

// ValidateForRequest proves exact attempt and version binding.
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
	if !validName(signal.Name) || len(signal.ReasonCodes) > 32 ||
		!validNames(signal.ReasonCodes) || !unique(signal.ReasonCodes) {
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
	if !validName(failure.Code) {
		return invalid("failure.code", "is invalid")
	}
	switch failure.Class {
	case FailureInvalidInput, FailureUnauthenticated, FailureUnauthorized, FailureUnsupported,
		FailureUnavailable, FailureResourceExhausted, FailureDeadline, FailureCancelled, FailureInternal:
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

// Validate checks a safe model health response.
func (health Health) Validate() error {
	if !validName(health.Code) || !validUTC(health.CheckedAt) {
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

func validName(value string) bool {
	if value == "" || len(value) > 160 || strings.TrimSpace(value) != value || containsControl(value) {
		return false
	}
	return strings.IndexFunc(value, unicode.IsSpace) == -1
}

func validNames(values []string) bool {
	return slices.IndexFunc(values, func(value string) bool { return !validName(value) }) == -1
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

func validOpaque(value string, minimum, maximum int) bool {
	return len(value) >= minimum && len(value) <= maximum && strings.TrimSpace(value) == value && !containsControl(value)
}

func validUTC(value time.Time) bool     { return !value.IsZero() && value.Location() == time.UTC }
func containsControl(value string) bool { return strings.IndexFunc(value, unicode.IsControl) >= 0 }

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
