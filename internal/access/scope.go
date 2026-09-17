// Package access owns authentication and application-authorisation concepts.
package access

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

const maxScopeSegmentLength = 64

// Permission is one exact tenant-assignable resource action.
type Permission string

const (
	// PermissionSubjectsRead permits tenant subject metadata and exact external-reference lookup.
	PermissionSubjectsRead Permission = "subjects:read"
	// PermissionSubjectsWrite permits subject lifecycle updates and explicit verification links.
	PermissionSubjectsWrite Permission = "subjects:write"
	// PermissionSubjectsDelete requests deletion of identity data and all linked verification evidence.
	PermissionSubjectsDelete Permission = "subjects:delete"
	// PermissionIdentityRead permits identity record metadata and exact identifier lookup.
	PermissionIdentityRead Permission = "identity:read"
	// PermissionIdentityWrite permits attributed observations and immutable derived records.
	PermissionIdentityWrite Permission = "identity:write"
	// PermissionIdentityReveal permits audited decryption of structured identity values.
	PermissionIdentityReveal Permission = "identity:reveal"
	// PermissionIdentityConfigure activates versioned corroboration and provider-lineage rules.
	PermissionIdentityConfigure Permission = "identity:configure"

	// PermissionFraudRead inspects safe fraud configuration and receipts.
	PermissionFraudRead Permission = "fraud:read"
	// PermissionFraudWrite ingests configured tenant sources and proposes hypotheses.
	PermissionFraudWrite Permission = "fraud:write"
	// PermissionFraudConfigure activates versioned tenant fraud rules.
	PermissionFraudConfigure Permission = "fraud:configure"
	// PermissionModelsRead permits model registry inspection.
	PermissionModelsRead Permission = "models:read"
	// PermissionModelsWrite permits immutable evaluation model and threshold registration.
	PermissionModelsWrite Permission = "models:write"
	// PermissionModelsActivate permits audited evaluation deployment changes.
	PermissionModelsActivate Permission = "models:activate"
	// PermissionPoliciesRead permits policy catalog and source inspection.
	PermissionPoliciesRead Permission = "policies:read"
	// PermissionPoliciesWrite permits immutable revision creation and validation.
	PermissionPoliciesWrite Permission = "policies:write"
	// PermissionPoliciesActivate permits version-checked activation and rollback.
	PermissionPoliciesActivate Permission = "policies:activate"
	// PermissionWebhooksRead permits safe webhook configuration and delivery inspection.
	PermissionWebhooksRead Permission = "webhooks:read"
	// PermissionWebhooksConfigure permits endpoint creation, disablement and rotation.
	PermissionWebhooksConfigure Permission = "webhooks:configure"
	// PermissionWebhooksReplay permits deliberate failed delivery replay.
	PermissionWebhooksReplay Permission = "webhooks:replay"
	// PermissionTenantRead permits reading the authenticated tenant's safe metadata.
	PermissionTenantRead Permission = "tenant:read"
	// PermissionCaptureProfilesRead permits reading capture profiles.
	PermissionCaptureProfilesRead Permission = "capture_profiles:read"
	// PermissionCaptureProfilesWrite permits changing capture profiles.
	PermissionCaptureProfilesWrite Permission = "capture_profiles:write"
	// PermissionVerificationSessionsCreate permits creating verification sessions.
	PermissionVerificationSessionsCreate Permission = "verification_sessions:create"
	// PermissionVerificationSessionsCancel permits stopping an active verification.
	PermissionVerificationSessionsCancel Permission = "verification_sessions:cancel"
	// PermissionVerificationSessionsRead permits reading verification sessions.
	PermissionVerificationSessionsRead Permission = "verification_sessions:read"
	// PermissionNoticesRead permits reading immutable notice versions.
	PermissionNoticesRead Permission = "notices:read"
	// PermissionNoticesWrite permits creating immutable notice versions.
	PermissionNoticesWrite Permission = "notices:write"
	// PermissionAuthoritiesRead permits reading processing-authority state.
	PermissionAuthoritiesRead Permission = "authorities:read"
	// PermissionAuthoritiesWrite permits declaring and transitioning processing authority.
	PermissionAuthoritiesWrite Permission = "authorities:write"
	// PermissionDecisionsRead permits reading safe immutable decision summaries.
	PermissionDecisionsRead Permission = "decisions:read"
	// PermissionDecisionsExport permits exporting portable decision bundles.
	PermissionDecisionsExport Permission = "decisions:export"
	// PermissionReviewsRead permits reading non-evidence review metadata.
	PermissionReviewsRead Permission = "reviews:read"
	// PermissionReviewsWrite permits attributed review and correction actions.
	PermissionReviewsWrite Permission = "reviews:write"
	// PermissionReviewsAdmin manages tenant review operations and operator assignments.
	PermissionReviewsAdmin Permission = "reviews:admin"
	// PermissionAppealsWrite permits attributed appeal actions.
	PermissionAppealsWrite Permission = "appeals:write"
	// PermissionDeletionsWrite permits observable tenant deletion workflows.
	PermissionDeletionsWrite Permission = "deletions:write"
	// PermissionLegalHoldsWrite permits legal-hold creation and release.
	PermissionLegalHoldsWrite Permission = "legal_holds:write"
	// PermissionProposalsRead permits reading proposal and accepted-command records.
	PermissionProposalsRead Permission = "proposals:read"
	// PermissionProposalsWrite permits creating bounded proposals.
	PermissionProposalsWrite Permission = "proposals:write"
	// PermissionProposalsApprove permits guardrail-checked approval and execution of proposals.
	PermissionProposalsApprove Permission = "proposals:approve"
	// PermissionProposalsConfigure permits versioned automation-mode configuration.
	PermissionProposalsConfigure Permission = "proposals:configure"
	// PermissionPromptsRead permits prompt registry inspection.
	PermissionPromptsRead Permission = "prompts:read"
	// PermissionPromptsWrite permits immutable prompt registration.
	PermissionPromptsWrite Permission = "prompts:write"
)

// ParsePermission validates an exact resource-action permission.
func ParsePermission(value string) (Permission, error) {
	resource, action, err := parseSegments(value, false)
	if err != nil {
		return "", err
	}

	return Permission(resource + ":" + action), nil
}

// Pattern is an exact permission or a complete-segment wildcard grant.
type Pattern string

// ParsePattern validates an exact, resource-wildcard, action-wildcard, or
// full-wildcard scope pattern. Partial globs are rejected.
func ParsePattern(value string) (Pattern, error) {
	resource, action, err := parseSegments(value, true)
	if err != nil {
		return "", err
	}

	return Pattern(resource + ":" + action), nil
}

// Matches reports whether the pattern includes an exact permission.
func (pattern Pattern) Matches(permission Permission) bool {
	patternResource, patternAction, patternErr := parseSegments(string(pattern), true)
	permissionResource, permissionAction, permissionErr := parseSegments(string(permission), false)
	if patternErr != nil || permissionErr != nil {
		return false
	}

	return (patternResource == "*" || patternResource == permissionResource) &&
		(patternAction == "*" || patternAction == permissionAction)
}

// Registry is the immutable set of permissions tenant API keys may receive.
// Platform-administration and internal permissions must never enter this set.
type Registry struct {
	permissions []Permission
}

// NewRegistry validates and snapshots tenant-assignable permissions.
func NewRegistry(permissions ...Permission) (Registry, error) {
	if len(permissions) == 0 {
		return Registry{}, errors.New("access scope registry requires permissions")
	}

	values := slices.Clone(permissions)
	for _, permission := range values {
		parsed, err := ParsePermission(string(permission))
		if err != nil {
			return Registry{}, fmt.Errorf("validate registered permission: %w", err)
		}
		if parsed != permission {
			return Registry{}, errors.New("registered permission is not canonical")
		}
	}
	slices.Sort(values)
	if hasAdjacentDuplicate(values) {
		return Registry{}, errors.New("access scope registry contains a duplicate permission")
	}

	return Registry{permissions: values}, nil
}

// TenantRegistry returns the initial tenant-assignable permission registry.
func TenantRegistry() Registry {
	return Registry{permissions: []Permission{PermissionSubjectsRead, PermissionSubjectsWrite, PermissionSubjectsDelete, PermissionIdentityRead, PermissionIdentityWrite, PermissionIdentityReveal, PermissionIdentityConfigure, PermissionFraudRead, PermissionFraudWrite, PermissionFraudConfigure,
		PermissionModelsRead, PermissionModelsWrite, PermissionModelsActivate,
		PermissionAuthoritiesRead,
		PermissionAuthoritiesWrite,
		PermissionCaptureProfilesRead,
		PermissionCaptureProfilesWrite,
		PermissionDecisionsExport,
		PermissionDecisionsRead,
		PermissionDeletionsWrite,
		PermissionLegalHoldsWrite,
		PermissionAppealsWrite,
		PermissionNoticesRead,
		PermissionNoticesWrite,
		PermissionReviewsRead,
		PermissionReviewsWrite,
		PermissionReviewsAdmin,
		PermissionTenantRead,
		PermissionVerificationSessionsCreate,
		PermissionVerificationSessionsCancel,
		PermissionVerificationSessionsRead,
		PermissionPoliciesRead,
		PermissionPoliciesWrite,
		PermissionPoliciesActivate,
		PermissionWebhooksConfigure,
		PermissionWebhooksRead,
		PermissionWebhooksReplay,
		PermissionProposalsRead,
		PermissionProposalsWrite,
		PermissionProposalsApprove,
		PermissionProposalsConfigure,
		PermissionPromptsRead,
		PermissionPromptsWrite,
	}}
}

// Resolve expands requested patterns into an immutable exact-permission
// snapshot. A pattern matching no registered permission is rejected.
func (registry Registry) Resolve(patterns ...Pattern) (Grant, error) {
	if len(registry.permissions) == 0 {
		return Grant{}, errors.New("access scope registry is empty")
	}
	if len(patterns) == 0 {
		return Grant{}, errors.New("access grant requires scope patterns")
	}

	requested := slices.Clone(patterns)
	for _, pattern := range requested {
		parsed, err := ParsePattern(string(pattern))
		if err != nil {
			return Grant{}, fmt.Errorf("validate scope pattern: %w", err)
		}
		if parsed != pattern {
			return Grant{}, errors.New("scope pattern is not canonical")
		}
	}
	slices.Sort(requested)
	if hasAdjacentDuplicate(requested) {
		return Grant{}, errors.New("access grant contains a duplicate scope pattern")
	}

	resolved := make([]Permission, 0, len(registry.permissions))
	for _, pattern := range requested {
		matched := false
		for _, permission := range registry.permissions {
			if pattern.Matches(permission) {
				resolved = append(resolved, permission)
				matched = true
			}
		}
		if !matched {
			return Grant{}, fmt.Errorf("scope pattern %q matches no tenant-assignable permission", pattern)
		}
	}
	slices.Sort(resolved)
	resolved = slices.Compact(resolved)

	return Grant{patterns: requested, permissions: resolved}, nil
}

// Grant is the immutable requested-pattern and resolved-permission snapshot
// attached to one credential.
type Grant struct {
	patterns    []Pattern
	permissions []Permission
}

// RestoreGrant validates an immutable grant snapshot loaded from durable state.
// It intentionally does not consult the current registry: later registry
// changes must not expand or invalidate an already-issued credential.
func RestoreGrant(patterns []Pattern, permissions []Permission) (Grant, error) {
	if len(patterns) == 0 || len(permissions) == 0 {
		return Grant{}, errors.New("access grant snapshot must contain patterns and permissions")
	}

	validatedPatterns := slices.Clone(patterns)
	for _, pattern := range validatedPatterns {
		parsed, err := ParsePattern(string(pattern))
		if err != nil || parsed != pattern {
			return Grant{}, errors.New("access grant snapshot contains an invalid pattern")
		}
	}
	slices.Sort(validatedPatterns)
	if hasAdjacentDuplicate(validatedPatterns) {
		return Grant{}, errors.New("access grant snapshot contains a duplicate pattern")
	}

	validatedPermissions := slices.Clone(permissions)
	for _, permission := range validatedPermissions {
		parsed, err := ParsePermission(string(permission))
		if err != nil || parsed != permission {
			return Grant{}, errors.New("access grant snapshot contains an invalid permission")
		}
		matched := false
		for _, pattern := range validatedPatterns {
			if pattern.Matches(permission) {
				matched = true
				break
			}
		}
		if !matched {
			return Grant{}, errors.New("access grant snapshot permission is not covered by its patterns")
		}
	}
	slices.Sort(validatedPermissions)
	if hasAdjacentDuplicate(validatedPermissions) {
		return Grant{}, errors.New("access grant snapshot contains a duplicate permission")
	}
	for _, pattern := range validatedPatterns {
		matched := false
		for _, permission := range validatedPermissions {
			if pattern.Matches(permission) {
				matched = true
				break
			}
		}
		if !matched {
			return Grant{}, errors.New("access grant snapshot pattern covers no stored permission")
		}
	}

	return Grant{patterns: validatedPatterns, permissions: validatedPermissions}, nil
}

// Patterns returns a copy of the requested patterns.
func (grant Grant) Patterns() []Pattern {
	return slices.Clone(grant.patterns)
}

// Permissions returns a copy of the resolved exact permissions.
func (grant Grant) Permissions() []Permission {
	return slices.Clone(grant.permissions)
}

// Allows reports whether the immutable snapshot contains permission.
func (grant Grant) Allows(permission Permission) bool {
	if _, err := ParsePermission(string(permission)); err != nil {
		return false
	}

	_, exists := slices.BinarySearch(grant.permissions, permission)

	return exists
}

func parseSegments(value string, allowWildcard bool) (string, string, error) {
	if strings.TrimSpace(value) != value {
		return "", "", errors.New("scope must not contain surrounding whitespace")
	}
	segments := strings.Split(value, ":")
	if len(segments) != 2 {
		return "", "", errors.New("scope must contain one resource and one action")
	}
	if err := validateSegment("resource", segments[0], allowWildcard); err != nil {
		return "", "", err
	}
	if err := validateSegment("action", segments[1], allowWildcard); err != nil {
		return "", "", err
	}

	return segments[0], segments[1], nil
}

func validateSegment(name, value string, allowWildcard bool) error {
	if allowWildcard && value == "*" {
		return nil
	}
	if value == "" || len(value) > maxScopeSegmentLength {
		return fmt.Errorf("scope %s length is invalid", name)
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' {
			continue
		}
		if index > 0 && ((character >= '0' && character <= '9') || character == '_') {
			continue
		}

		return fmt.Errorf("scope %s contains an invalid character", name)
	}

	return nil
}

func hasAdjacentDuplicate[T comparable](values []T) bool {
	for index := 1; index < len(values); index++ {
		if values[index] == values[index-1] {
			return true
		}
	}

	return false
}
