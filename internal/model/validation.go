package model

import (
	"context"
	"errors"
	"slices"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ValidationCode is one stable, bounded reason for a registry validation result.
// Codes never carry declared model metadata, scores, or tenant data.
type ValidationCode string

// Stable registry validation reason codes.
const (
	// ValidationCommandInvalid rejects an unknown operation, an invalid reason or
	// version, or fields that do not belong to the selected operation.
	ValidationCommandInvalid ValidationCode = "command_invalid"
	// ValidationRegistrationInvalid rejects a registration document the domain refuses.
	ValidationRegistrationInvalid ValidationCode = "registration_invalid"
	// ValidationThresholdInvalid rejects a threshold document the domain refuses.
	ValidationThresholdInvalid ValidationCode = "threshold_invalid"
	// ValidationRevisionNotFound reports a missing stored model or threshold revision.
	ValidationRevisionNotFound ValidationCode = "revision_not_found"
	// ValidationDeploymentInvalid reports a pair the domain refuses to deploy together.
	ValidationDeploymentInvalid ValidationCode = "deployment_invalid"
	// ValidationVersionConflict reports an expected version that does not match the current registry.
	ValidationVersionConflict ValidationCode = "version_conflict"
	// ValidationRegistryNotFound reports a command that needs a registry that does not exist.
	ValidationRegistryNotFound ValidationCode = "registry_not_found"
	// ValidationStateConflict reports an operation that current registry state refuses.
	ValidationStateConflict ValidationCode = "state_conflict"
)

var registryValidationOperations = []string{"register", "threshold", "activate", "rollback", "retire"}

// ValidationRequest is a closed, side-effect-free evaluation registry command.
type ValidationRequest struct {
	Operation       string        `json:"operation"`
	ExpectedVersion int64         `json:"expected_version"`
	Reason          string        `json:"reason"`
	Registration    *Registration `json:"registration,omitempty"`
	Thresholds      *ThresholdSet `json:"thresholds,omitempty"`
	Deployment      *Deployment   `json:"deployment,omitempty"`
}

// ValidationReport states whether a supplied evaluation-only command would be
// accepted. Nothing is persisted and no idempotency record is created.
type ValidationReport struct {
	Accepted    bool             `json:"accepted"`
	Operation   string           `json:"operation"`
	ReasonCodes []ValidationCode `json:"reason_codes"`
}

func (report *ValidationReport) reject(code ValidationCode) {
	report.Accepted = false
	report.ReasonCodes = append(report.ReasonCodes, code)
}

// Validate reports whether the supplied command would be accepted, checking the
// domain document and the current registry state without persisting anything.
func (service *Management) Validate(ctx context.Context, actor access.Context, name string, request ValidationRequest) (ValidationReport, error) {
	if err := actor.Require(access.PermissionModelsWrite); err != nil {
		return ValidationReport{}, err
	}
	if !registryName.MatchString(name) || !slices.Contains(registryValidationOperations, request.Operation) {
		return ValidationReport{}, ErrRegistryInvalid
	}
	report := ValidationReport{Accepted: true, Operation: request.Operation, ReasonCodes: []ValidationCode{}}
	command := RegistryCommand{
		Name: name, Operation: request.Operation, ExpectedVersion: request.ExpectedVersion, Reason: request.Reason,
		Registration: request.Registration, Thresholds: request.Thresholds, Deployment: request.Deployment,
	}
	if err := command.validateEnvelope(); err != nil {
		report.reject(ValidationCommandInvalid)
		return report, nil //nolint:nilerr // A rejected document is a bounded validation report, not an operational error.
	}
	switch command.Operation {
	case "register":
		if command.Registration.Validate() != nil {
			report.reject(ValidationRegistrationInvalid)
		}
	case "threshold":
		if command.Thresholds.Validate() != nil {
			report.reject(ValidationThresholdInvalid)
		}
	}
	if !report.Accepted {
		return report, nil
	}
	state, err := service.repository.Get(ctx, actor.TenantScope(), name)
	if err != nil && !errors.Is(err, ErrRegistryNotFound) {
		return ValidationReport{}, err
	}
	if errors.Is(err, ErrRegistryNotFound) {
		switch {
		case command.Operation != "register":
			report.reject(ValidationRegistryNotFound)
		case command.ExpectedVersion != 0:
			report.reject(ValidationVersionConflict)
		}
		return report, nil
	}
	if state.Version != command.ExpectedVersion {
		report.reject(ValidationVersionConflict)
		return report, nil
	}
	switch command.Operation {
	case "activate", "rollback":
		if state.Active != nil && *state.Active == *command.Deployment {
			report.reject(ValidationStateConflict)
			return report, nil
		}
		valid, err := service.validateDeployment(ctx, actor.TenantScope(), name, *command.Deployment, &report)
		if err != nil || !valid {
			return report, err
		}
		if command.Operation == "rollback" {
			eligible, err := service.repository.RollbackEligible(ctx, actor.TenantScope(), name, *command.Deployment)
			if err != nil {
				return ValidationReport{}, err
			}
			if !eligible {
				report.reject(ValidationStateConflict)
			}
		}
	case "retire":
		if state.Active == nil {
			report.reject(ValidationStateConflict)
		}
	}
	return report, nil
}

func (service *Management) validateDeployment(ctx context.Context, scope tenant.Scope, name string, deployment Deployment, report *ValidationReport) (bool, error) {
	registration, err := service.repository.Revision(ctx, scope, name, "model", deployment.ModelRevision)
	if err != nil && !errors.Is(err, ErrRegistryNotFound) {
		return false, err
	}
	if errors.Is(err, ErrRegistryNotFound) {
		report.reject(ValidationRevisionNotFound)
		return false, nil
	}
	thresholds, err := service.repository.Revision(ctx, scope, name, "threshold", deployment.ThresholdRevision)
	if err != nil && !errors.Is(err, ErrRegistryNotFound) {
		return false, err
	}
	if errors.Is(err, ErrRegistryNotFound) {
		report.reject(ValidationRevisionNotFound)
		return false, nil
	}
	if registration.Registration == nil || thresholds.Thresholds == nil {
		report.reject(ValidationDeploymentInvalid)
		return false, nil
	}
	if ValidateDeployment(deployment, *registration.Registration, *thresholds.Thresholds) != nil {
		report.reject(ValidationDeploymentInvalid)
		return false, nil //nolint:nilerr // A refused pair is a bounded validation report, not an operational error.
	}
	return true, nil
}
