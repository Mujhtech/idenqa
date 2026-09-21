package access_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/access"
)

func TestParsePermission(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		valid bool
	}{
		{name: "simple", value: "tenant:read", valid: true},
		{name: "underscores and digits", value: "verification_sessions:read_v2", valid: true},
		{name: "empty", value: "", valid: false},
		{name: "missing action", value: "tenant", valid: false},
		{name: "extra segment", value: "tenant:read:all", valid: false},
		{name: "wildcard", value: "tenant:*", valid: false},
		{name: "partial wildcard", value: "tenant:rea*", valid: false},
		{name: "uppercase", value: "Tenant:read", valid: false},
		{name: "leading underscore", value: "_tenant:read", valid: false},
		{name: "surrounding whitespace", value: " tenant:read", valid: false},
		{name: "long segment", value: strings.Repeat("a", 65) + ":read", valid: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			permission, err := access.ParsePermission(test.value)
			if (err == nil) != test.valid {
				t.Fatalf("ParsePermission(%q) error = %v, valid = %t", test.value, err, test.valid)
			}
			if test.valid && string(permission) != test.value {
				t.Errorf("ParsePermission(%q) = %q", test.value, permission)
			}
		})
	}
}

func TestParsePatternAndMatch(t *testing.T) {
	t.Parallel()

	permission := access.PermissionVerificationSessionsRead
	tests := []struct {
		name    string
		value   string
		valid   bool
		matches bool
	}{
		{name: "exact", value: "verification_sessions:read", valid: true, matches: true},
		{name: "resource wildcard", value: "verification_sessions:*", valid: true, matches: true},
		{name: "action wildcard", value: "*:read", valid: true, matches: true},
		{name: "full wildcard", value: "*:*", valid: true, matches: true},
		{name: "different resource", value: "capture_profiles:*", valid: true, matches: false},
		{name: "partial resource wildcard", value: "verification_*:read", valid: false},
		{name: "partial action wildcard", value: "verification_sessions:r*", valid: false},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			pattern, err := access.ParsePattern(test.value)
			if (err == nil) != test.valid {
				t.Fatalf("ParsePattern(%q) error = %v, valid = %t", test.value, err, test.valid)
			}
			if test.valid {
				got := pattern.Matches(permission)
				if got != test.matches {
					t.Errorf("Matches(%q, %q) = %t, want %t", pattern, permission, got, test.matches)
				}
			}
		})
	}
}

func TestRegistryResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		patterns []string
		want     []access.Permission
	}{
		{
			name:     "exact",
			patterns: []string{"tenant:read"},
			want: []access.Permission{
				access.PermissionTenantRead,
			},
		},
		{
			name:     "resource wildcard",
			patterns: []string{"capture_profiles:*"},
			want: []access.Permission{
				access.PermissionCaptureProfilesRead,
				access.PermissionCaptureProfilesWrite,
			},
		},
		{
			name:     "decision resource wildcard",
			patterns: []string{"decisions:*"},
			want: []access.Permission{
				access.PermissionDecisionsExport,
				access.PermissionDecisionsRead,
			},
		},
		{
			name:     "action wildcard",
			patterns: []string{"*:read"},
			want: []access.Permission{
				access.PermissionAuthoritiesRead,
				access.PermissionCaptureProfilesRead,
				access.PermissionDecisionsRead,
				access.PermissionDeletionsRead,
				access.PermissionExperiencesRead,
				access.PermissionFraudRead,
				access.PermissionIdentityRead,
				access.PermissionModelsRead,
				access.PermissionNoticesRead,
				access.PermissionPacksRead,
				access.PermissionPoliciesRead,
				access.PermissionPrivacyRequestsRead,
				access.PermissionPromptsRead,
				access.PermissionProposalsRead,
				access.PermissionProvidersRead,
				access.PermissionReviewsRead,
				access.PermissionSubjectsRead,
				access.PermissionTenantRead,
				access.PermissionVerificationSessionsRead,
				access.PermissionWebhooksRead,
			},
		},
		{
			name:     "overlapping patterns are deduplicated",
			patterns: []string{"*:*", "tenant:read"},
			want: []access.Permission{
				access.PermissionAppealsWrite,
				access.PermissionAuthoritiesRead,
				access.PermissionAuthoritiesWrite,
				access.PermissionCaptureProfilesRead,
				access.PermissionCaptureProfilesWrite,
				access.PermissionDecisionsExport,
				access.PermissionDecisionsRead,
				access.PermissionDeletionsRead,
				access.PermissionDeletionsWrite,
				access.PermissionExperiencesPublish,
				access.PermissionExperiencesRead,
				access.PermissionExperiencesWrite,
				access.PermissionFraudConfigure,
				access.PermissionFraudRead,
				access.PermissionFraudWrite,
				access.PermissionIdentityConfigure,
				access.PermissionIdentityRead,
				access.PermissionIdentityReveal,
				access.PermissionIdentityWrite,
				access.PermissionLegalHoldsWrite,
				access.PermissionModelsActivate,
				access.PermissionModelsRead,
				access.PermissionModelsWrite,
				access.PermissionNoticesRead,
				access.PermissionNoticesWrite,
				access.PermissionPacksRead,
				access.PermissionPoliciesActivate,
				access.PermissionPoliciesRead,
				access.PermissionPoliciesWrite,
				access.PermissionPrivacyRequestsApprove,
				access.PermissionPrivacyRequestsRead,
				access.PermissionPrivacyRequestsWrite,
				access.PermissionPromptsRead,
				access.PermissionPromptsWrite,
				access.PermissionProposalsApprove,
				access.PermissionProposalsConfigure,
				access.PermissionProposalsRead,
				access.PermissionProposalsWrite,
				access.PermissionProvidersRead,
				access.PermissionProvidersWrite,
				access.PermissionReviewsAdmin,
				access.PermissionReviewsRead,
				access.PermissionReviewsWrite,
				access.PermissionSubjectsDelete,
				access.PermissionSubjectsRead,
				access.PermissionSubjectsWrite,
				access.PermissionTenantExport,
				access.PermissionTenantRead,
				access.PermissionVerificationSessionsCancel,
				access.PermissionVerificationSessionsCreate,
				access.PermissionVerificationSessionsRead,
				access.PermissionVerificationSessionsResume,
				access.PermissionWebhooksConfigure,
				access.PermissionWebhooksRead,
				access.PermissionWebhooksReplay,
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			patterns := make([]access.Pattern, 0, len(test.patterns))
			for _, value := range test.patterns {
				pattern, err := access.ParsePattern(value)
				if err != nil {
					t.Fatalf("ParsePattern(%q) error = %v", value, err)
				}
				patterns = append(patterns, pattern)
			}
			grant, err := access.TenantRegistry().Resolve(patterns...)
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if !slices.Equal(grant.Permissions(), test.want) {
				t.Errorf("Permissions() = %v, want %v", grant.Permissions(), test.want)
			}
		})
	}
}

func TestRegistryRejectsInvalidGrants(t *testing.T) {
	t.Parallel()

	registry := access.TenantRegistry()
	tests := []struct {
		name     string
		patterns []access.Pattern
	}{
		{name: "no patterns"},
		{name: "invalid pattern", patterns: []access.Pattern{"tenant:rea*"}},
		{name: "duplicate pattern", patterns: []access.Pattern{"tenant:read", "tenant:read"}},
		{name: "unknown exact permission", patterns: []access.Pattern{"platform_admin:read"}},
		{name: "wildcard matches nothing", patterns: []access.Pattern{"unknown:*"}},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, err := registry.Resolve(test.patterns...); err == nil {
				t.Errorf("Resolve(%v) error = nil", test.patterns)
			}
		})
	}
}

func TestGrantSnapshotDoesNotExpandWithRegistry(t *testing.T) {
	t.Parallel()

	all, err := access.ParsePattern("*:*")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	initialRegistry, err := access.NewRegistry(access.PermissionTenantRead)
	if err != nil {
		t.Fatalf("NewRegistry() error = %v", err)
	}
	initialGrant, err := initialRegistry.Resolve(all)
	if err != nil {
		t.Fatalf("initial Resolve() error = %v", err)
	}
	expandedRegistry, err := access.NewRegistry(
		access.PermissionTenantRead,
		access.PermissionCaptureProfilesRead,
		access.PermissionDecisionsRead,
		access.PermissionDecisionsExport,
	)
	if err != nil {
		t.Fatalf("expanded NewRegistry() error = %v", err)
	}
	expandedGrant, err := expandedRegistry.Resolve(all)
	if err != nil {
		t.Fatalf("expanded Resolve() error = %v", err)
	}

	if initialGrant.Allows(access.PermissionCaptureProfilesRead) {
		t.Error("initial grant expanded to a later registered permission")
	}
	if initialGrant.Allows(access.PermissionDecisionsRead) ||
		initialGrant.Allows(access.PermissionDecisionsExport) {
		t.Error("initial grant expanded to later decision permissions")
	}
	if !expandedGrant.Allows(access.PermissionCaptureProfilesRead) ||
		!expandedGrant.Allows(access.PermissionDecisionsRead) ||
		!expandedGrant.Allows(access.PermissionDecisionsExport) {
		t.Error("new grant does not include the newly registered permission")
	}
}

func TestGrantReturnsDefensiveCopies(t *testing.T) {
	t.Parallel()

	pattern, err := access.ParsePattern("tenant:read")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	grant, err := access.TenantRegistry().Resolve(pattern)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	patterns := grant.Patterns()
	permissions := grant.Permissions()
	patterns[0] = "*:*"
	permissions[0] = access.PermissionCaptureProfilesWrite

	if grant.Patterns()[0] != pattern || grant.Permissions()[0] != access.PermissionTenantRead {
		t.Error("grant changed through returned slices")
	}
}

func TestRestoreGrantPreservesHistoricalSnapshot(t *testing.T) {
	t.Parallel()

	patterns := []access.Pattern{"verification_sessions:*"}
	permissions := []access.Permission{
		access.PermissionVerificationSessionsCreate,
		access.PermissionVerificationSessionsRead,
	}
	grant, err := access.RestoreGrant(patterns, permissions)
	if err != nil {
		t.Fatalf("RestoreGrant() error = %v", err)
	}
	patterns[0] = "*:*"
	permissions[0] = access.PermissionTenantRead
	if !grant.Allows(access.PermissionVerificationSessionsCreate) || grant.Allows(access.PermissionTenantRead) {
		t.Fatal("restored grant changed through caller-owned slices")
	}
}

func TestRestoreGrantRejectsInconsistentSnapshot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		patterns    []access.Pattern
		permissions []access.Permission
	}{
		{name: "empty"},
		{name: "invalid pattern", patterns: []access.Pattern{"tenant:rea*"}, permissions: []access.Permission{access.PermissionTenantRead}},
		{name: "duplicate pattern", patterns: []access.Pattern{"tenant:read", "tenant:read"}, permissions: []access.Permission{access.PermissionTenantRead}},
		{name: "invalid permission", patterns: []access.Pattern{"tenant:*"}, permissions: []access.Permission{"tenant:*"}},
		{name: "duplicate permission", patterns: []access.Pattern{"tenant:*"}, permissions: []access.Permission{access.PermissionTenantRead, access.PermissionTenantRead}},
		{name: "permission not covered", patterns: []access.Pattern{"tenant:*"}, permissions: []access.Permission{access.PermissionCaptureProfilesRead}},
		{name: "pattern matches nothing", patterns: []access.Pattern{"tenant:*", "capture_profiles:*"}, permissions: []access.Permission{access.PermissionTenantRead}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := access.RestoreGrant(test.patterns, test.permissions); err == nil {
				t.Error("RestoreGrant() error = nil")
			}
		})
	}
}

func FuzzParsePattern(f *testing.F) {
	f.Add("tenant:read")
	f.Add("*:read")
	f.Add("verification_*:read")

	f.Fuzz(func(t *testing.T, value string) {
		pattern, err := access.ParsePattern(value)
		if err == nil && string(pattern) != value {
			t.Errorf("ParsePattern(%q) = %q", value, pattern)
		}
	})
}
