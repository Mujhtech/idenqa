package support

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type recordingRepository struct {
	commands []Command
	result   Result
}

func (repository *recordingRepository) Execute(_ context.Context, _ tenant.Scope, command Command) (Result, error) {
	repository.commands = append(repository.commands, command)

	return repository.result, nil
}

func (repository *recordingRepository) Read(context.Context, tenant.Scope, string, string) (Result, error) {
	return repository.result, nil
}

func testScope(t *testing.T) tenant.Scope {
	t.Helper()
	tenantID, err := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}

	return scope
}

func testActor(t *testing.T) id.APIKey {
	t.Helper()
	actor, err := id.ParseAPIKey("key_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatalf("ParseAPIKey() error = %v", err)
	}

	return actor
}

func newTestService(t *testing.T, repository Repository) *Service {
	t.Helper()
	service, err := NewService(repository, access.TenantRegistry(), time.Now)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	return service
}

func TestServiceValidatesDelegatedGrants(t *testing.T) {
	t.Parallel()

	scope, actor := testScope(t), testActor(t)
	base := Command{Operation: "grant", Grantee: "support_engineer", Duration: time.Hour, Reason: "incident investigation"}

	tests := []struct {
		name    string
		command Command
		wantErr error
	}{
		{name: "valid", command: base, wantErr: nil},
		{name: "support surface escalation", command: Command{Operation: "grant", Grantee: "support_engineer", Patterns: []string{"support_access:*"}, Duration: time.Hour, Reason: "incident investigation"}, wantErr: ErrForbidden},
		{name: "break-glass escalation", command: Command{Operation: "grant", Grantee: "support_engineer", Patterns: []string{"break_glass:approve"}, Duration: time.Hour, Reason: "incident investigation"}, wantErr: ErrForbidden},
		{name: "unknown pattern", command: Command{Operation: "grant", Grantee: "support_engineer", Patterns: []string{"nonsense:read"}, Duration: time.Hour, Reason: "incident investigation"}, wantErr: ErrForbidden},
		{name: "too many patterns", command: Command{Operation: "grant", Grantee: "support_engineer", Patterns: []string{"subjects:*", "identity:*", "fraud:*", "models:*", "policies:*", "webhooks:*", "reviews:*", "privacy_requests:*", "kms:*"}, Duration: time.Hour, Reason: "incident investigation"}, wantErr: ErrInvalid},
		{name: "duration above maximum", command: Command{Operation: "grant", Grantee: "support_engineer", Patterns: []string{"subjects:read"}, Duration: MaximumGrantDuration + time.Hour, Reason: "incident investigation"}, wantErr: ErrInvalid},
		{name: "invalid grantee", command: Command{Operation: "grant", Grantee: "Support Engineer", Patterns: []string{"subjects:read"}, Duration: time.Hour, Reason: "incident investigation"}, wantErr: ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := &recordingRepository{}
			service := newTestService(t, repository)
			command := test.command
			if command.Patterns == nil {
				command.Patterns = []string{"subjects:read"}
			}
			_, err := service.ExecuteDirect(context.Background(), scope, actor, "key", command)
			if !errors.Is(err, test.wantErr) {
				t.Fatalf("ExecuteDirect() error = %v, want %v", err, test.wantErr)
			}
			if test.wantErr == nil {
				if len(repository.commands) != 1 {
					t.Fatal("valid grant was not persisted")
				}
				expanded := repository.commands[0].Permissions
				if len(expanded) == 0 || expanded[0] != "subjects:read" {
					t.Fatalf("expanded permissions = %v", expanded)
				}
			}
		})
	}
}

func TestServiceValidatesBreakGlass(t *testing.T) {
	t.Parallel()

	repository := &recordingRepository{}
	service := newTestService(t, repository)
	scope, actor := testScope(t), testActor(t)
	request := Command{
		Operation: "break_glass_request", Permissions: []string{"identity:reveal"},
		Duration: time.Hour, Reason: "production incident triage",
	}
	if _, err := service.ExecuteDirect(context.Background(), scope, actor, "request-key", request); err != nil {
		t.Fatalf("ExecuteDirect(request) error = %v", err)
	}

	tests := []struct {
		name    string
		command Command
		wantErr error
	}{
		{name: "write permission", command: Command{Operation: "break_glass_request", Permissions: []string{"webhooks:write"}, Duration: time.Hour, Reason: "production incident triage"}, wantErr: ErrForbidden},
		{name: "too many permissions", command: Command{Operation: "break_glass_request", Permissions: []string{"subjects:read", "identity:read", "reviews:read", "kms:read", "tenant:read"}, Duration: time.Hour, Reason: "production incident triage"}, wantErr: ErrInvalid},
		{name: "duration above maximum", command: Command{Operation: "break_glass_request", Permissions: []string{"subjects:read"}, Duration: MaximumEmergencyDuration + time.Minute, Reason: "production incident triage"}, wantErr: ErrInvalid},
		{name: "short duration", command: Command{Operation: "break_glass_request", Permissions: []string{"subjects:read"}, Duration: time.Second, Reason: "production incident triage"}, wantErr: ErrInvalid},
		{name: "use without version", command: Command{Operation: "break_glass_use", Permissions: []string{"identity:reveal"}, Target: "subject_1", Reason: "break-glass use recorded"}, wantErr: ErrInvalid},
		{name: "use with two permissions", command: Command{Operation: "break_glass_use", ExpectedVersion: 2, Permissions: []string{"identity:reveal", "subjects:read"}, Target: "subject_1", Reason: "break-glass use recorded"}, wantErr: ErrInvalid},
		{name: "revoke without version", command: Command{Operation: "break_glass_revoke", Reason: "closing the incident window"}, wantErr: ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := service.ExecuteDirect(context.Background(), scope, actor, "key", test.command); !errors.Is(err, test.wantErr) {
				t.Fatalf("ExecuteDirect() error = %v, want %v", err, test.wantErr)
			}
		})
	}
}

func TestGrantAndEmergencyWindows(t *testing.T) {
	t.Parallel()

	now := time.Unix(1_700_000_000, 0).UTC()
	grant := Grant{
		State: StateActive, StartsAt: now, ExpiresAt: now.Add(time.Hour),
		Permissions: []string{"subjects:read"},
	}
	if !grant.ActiveAt(now) || !grant.Allows(access.PermissionSubjectsRead, now) {
		t.Fatal("active grant must authorise at its start instant")
	}
	if grant.ActiveAt(now.Add(time.Hour)) || grant.Allows(access.PermissionSubjectsWrite, now) {
		t.Fatal("expired or out-of-scope grant must not authorise")
	}
	usableUntil := now.Add(30 * time.Minute)
	emergency := Emergency{State: StateApproved, UsableUntil: &usableUntil, Permissions: []string{"identity:reveal"}}
	if !emergency.Allows(access.PermissionIdentityReveal, now) {
		t.Fatal("approved emergency must authorise inside its window")
	}
	if emergency.Allows(access.PermissionIdentityReveal, usableUntil) ||
		emergency.Allows(access.PermissionIdentityWrite, now) {
		t.Fatal("expired or out-of-scope emergency must not authorise")
	}
}
