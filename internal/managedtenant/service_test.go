package managedtenant

import (
	"bytes"
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type tenantStub struct {
	item     tenant.Tenant
	commands []tenant.ProvisionCommand
}

func (stub *tenantStub) Provision(
	_ context.Context,
	_ tenant.AdminAction,
	command tenant.ProvisionCommand,
	displayName string,
) (tenant.Tenant, bool, error) {
	stub.commands = append(stub.commands, command)
	if stub.item.DisplayName() != displayName {
		return tenant.Tenant{}, false, tenant.ErrProvisionConflict
	}
	return stub.item, len(stub.commands) == 1, nil
}

type issuerCall struct {
	kind     string
	command  access.BridgeCommand
	patterns []string
	label    string
	overlap  time.Duration
}

type issuerStub struct {
	calls   []issuerCall
	results map[string]access.BridgeIssueResult
	expired map[string]bool
}

func (stub *issuerStub) Find(_ context.Context, scope tenant.Scope, keyID id.APIKey) (access.Key, error) {
	grant, err := access.TenantRegistry().Resolve(access.Pattern("tenant:read"))
	if err != nil {
		return access.Key{}, err
	}
	digestBytes := make([]byte, 32)
	digestBytes[0] = 42
	digest, _ := access.ParseDigest(digestBytes)
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	expiresAt := now.Add(time.Hour)
	if stub.expired[keyID.String()] {
		expiresAt = now
	}
	return access.RestoreKey(access.KeyRecord{ID: keyID, TenantID: scope.ID(), Label: "managed", Digest: digest, PepperVersion: 1, Grant: grant, Version: 1, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Hour), ExpiresAt: &expiresAt})
}

func (stub *issuerStub) Issue(_ context.Context, command access.BridgeCommand, _ access.AdminAction, _ tenant.Scope, input access.IssueInput, sealer access.CredentialSealer) (access.BridgeIssueResult, error) {
	patterns := make([]string, 0, len(input.Patterns))
	for _, pattern := range input.Patterns {
		patterns = append(patterns, string(pattern))
	}
	stub.calls = append(stub.calls, issuerCall{kind: "issue", command: command, patterns: patterns, label: input.Label})
	if _, err := sealer.Seal("idq_synthetic_secret"); err != nil {
		return access.BridgeIssueResult{}, err
	}
	return stub.result(command.ID, input.Patterns)
}

func (stub *issuerStub) Rotate(_ context.Context, command access.BridgeCommand, _ access.AdminAction, _ tenant.Scope, input access.RotateInput, _ access.CredentialSealer) (access.BridgeIssueResult, error) {
	patterns := make([]string, 0, len(input.Patterns))
	for _, pattern := range input.Patterns {
		patterns = append(patterns, string(pattern))
	}
	stub.calls = append(stub.calls, issuerCall{kind: "rotate", command: command, patterns: patterns, overlap: input.Overlap})
	return stub.result(command.ID, input.Patterns)
}

func (stub *issuerStub) result(commandID string, patterns []access.Pattern) (access.BridgeIssueResult, error) {
	if result, ok := stub.results[commandID]; ok {
		result.Created = false
		return result, nil
	}
	grant, err := access.TenantRegistry().Resolve(patterns...)
	if err != nil {
		return access.BridgeIssueResult{}, err
	}
	keyID, _ := id.ParseAPIKey([]string{"key_01K6C3F6M7Z8W9X0Y1A2B3C4D5", "key_01K6C3F6M7Z8W9X0Y1A2B3C4D6", "key_01K6C3F6M7Z8W9X0Y1A2B3C4D7", "key_01K6C3F6M7Z8W9X0Y1A2B3C4D8"}[len(stub.results)])
	tenantID, _ := id.ParseTenant("ten_01K6C3F6M7Z8W9X0Y1A2B3C4D5")
	digestBytes := make([]byte, 32)
	digestBytes[0] = []byte{1, 2, 3, 4}[len(stub.results)]
	digest, _ := access.ParseDigest(digestBytes)
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	expiresAt := now.Add(24 * time.Hour)
	key, err := access.RestoreKey(access.KeyRecord{ID: keyID, TenantID: tenantID, Label: "managed", Digest: digest, PepperVersion: 1, Grant: grant, Version: 1, CreatedAt: now, UpdatedAt: now, ExpiresAt: &expiresAt})
	if err != nil {
		return access.BridgeIssueResult{}, err
	}
	result := access.BridgeIssueResult{Key: key, Envelope: access.CredentialEnvelope{Algorithm: "test", Ciphertext: commandID}, Created: true}
	stub.results[commandID] = result
	return result, nil
}

type fixedClock struct{ now time.Time }

func (source fixedClock) Now() time.Time { return source.now }

func TestProvisionUsesFixedTenantAndAuthorityContract(t *testing.T) {
	t.Parallel()
	tenantID, err := id.ParseTenant("ten_01K6C3F6M7Z8W9X0Y1A2B3C4D5")
	if err != nil {
		t.Fatalf("ParseTenant() error = %v", err)
	}
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	item, err := tenant.Restore(tenantID, "Example Organisation", tenant.StateActive, 1, now, now, nil)
	if err != nil {
		t.Fatalf("Restore() error = %v", err)
	}
	tenants := &tenantStub{item: item}
	issuer := &issuerStub{results: make(map[string]access.BridgeIssueResult)}
	service, err := New(tenants, issuer, func(_, _ string) (access.CredentialSealer, error) { return testSealer{}, nil }, fixedClock{now}, 24*time.Hour)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := Request{
		CommandID:         "onboarding-command-0001",
		DisplayName:       "Example Organisation",
		DeliveryPublicKey: "delivery-key",
	}
	first, err := service.Provision(t.Context(), request)
	if err != nil {
		t.Fatalf("Provision(first) error = %v", err)
	}
	second, err := service.Provision(t.Context(), request)
	if err != nil {
		t.Fatalf("Provision(replay) error = %v", err)
	}
	if first.TenantID != tenantID.String() || second.TenantID != tenantID.String() || !first.Created || second.Created {
		t.Fatalf("provision results = %#v, %#v", first, second)
	}
	if first.DisplayName != "Example Organisation" || second.DisplayName != "Example Organisation" {
		t.Fatalf("display names = %q, %q", first.DisplayName, second.DisplayName)
	}
	if len(tenants.commands) != 2 || tenants.commands[0] != tenants.commands[1] {
		t.Fatalf("tenant commands = %#v", tenants.commands)
	}
	want := [][]string{consolePatterns, reviewerPatterns, consolePatterns, reviewerPatterns}
	if len(issuer.calls) != len(want) {
		t.Fatalf("issuer calls = %d, want %d", len(issuer.calls), len(want))
	}
	for index, call := range issuer.calls {
		if !slices.Equal(call.patterns, want[index]) {
			t.Fatalf("issuer call %d patterns = %v, want %v", index, call.patterns, want[index])
		}
	}
	if issuer.calls[0].command.ID != request.CommandID+":console" || issuer.calls[1].command.ID != request.CommandID+":reviewer" {
		t.Fatalf("credential command ids = %q, %q", issuer.calls[0].command.ID, issuer.calls[1].command.ID)
	}
}

func TestConsoleAuthorityCanReadVerificationInspection(t *testing.T) {
	t.Parallel()

	for _, required := range []string{"verification_sessions:read", "evidence:read", "webhooks:read"} {
		if !slices.Contains(consolePatterns, required) {
			t.Errorf("consolePatterns is missing verification inspection permission %q", required)
		}
	}
}

func TestRenewRotatesBothAuthorityClassesUnderOneFixedRequest(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	issuer := &issuerStub{results: make(map[string]access.BridgeIssueResult)}
	tenantID, _ := id.ParseTenant("ten_01K6C3F6M7Z8W9X0Y1A2B3C4D5")
	item, _ := tenant.Restore(tenantID, "Example Organisation", tenant.StateActive, 1, now, now, nil)
	service, err := New(&tenantStub{item: item}, issuer, func(_, _ string) (access.CredentialSealer, error) { return testSealer{}, nil }, fixedClock{now}, 24*time.Hour)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := RenewRequest{
		CommandID: "binding-renewal-command-0001", DeliveryPublicKey: "delivery-key",
		TenantID:     "ten_01K6C3F6M7Z8W9X0Y1A2B3C4D5",
		ConsoleKeyID: "key_01K6C3F6M7Z8W9X0Y1A2B3C4D9", ReviewerKeyID: "key_01K6C3F6M7Z8W9X0Y1A2B3C4DA",
	}
	first, err := service.Renew(t.Context(), request)
	if err != nil {
		t.Fatalf("Renew(first) error = %v", err)
	}
	second, err := service.Renew(t.Context(), request)
	if err != nil {
		t.Fatalf("Renew(replay) error = %v", err)
	}
	if first.Console.KeyID != second.Console.KeyID || first.Reviewer.KeyID != second.Reviewer.KeyID || first.Console.KeyID == request.ConsoleKeyID || first.Reviewer.KeyID == request.ReviewerKeyID {
		t.Fatalf("renewal results = %#v, %#v", first, second)
	}
	want := [][]string{consolePatterns, reviewerPatterns, consolePatterns, reviewerPatterns}
	if len(issuer.calls) != len(want) {
		t.Fatalf("issuer calls = %d, want %d", len(issuer.calls), len(want))
	}
	for index, call := range issuer.calls {
		if call.kind != "rotate" || !slices.Equal(call.patterns, want[index]) {
			t.Fatalf("issuer call %d = %#v", index, call)
		}
	}
}

func TestRenewRecoversExpiredAuthorityWithoutOperatorCredential(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	consoleKey := "key_01K6C3F6M7Z8W9X0Y1A2B3C4D9"
	reviewerKey := "key_01K6C3F6M7Z8W9X0Y1A2B3C4DA"
	issuer := &issuerStub{results: make(map[string]access.BridgeIssueResult), expired: map[string]bool{consoleKey: true, reviewerKey: true}}
	tenantID, _ := id.ParseTenant("ten_01K6C3F6M7Z8W9X0Y1A2B3C4D5")
	item, _ := tenant.Restore(tenantID, "Example Organisation", tenant.StateActive, 1, now, now, nil)
	service, err := New(&tenantStub{item: item}, issuer, func(_, _ string) (access.CredentialSealer, error) { return testSealer{}, nil }, fixedClock{now}, 24*time.Hour)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := service.Renew(t.Context(), RenewRequest{
		CommandID: "binding-recovery-command-0001", DeliveryPublicKey: "delivery-key",
		TenantID: "ten_01K6C3F6M7Z8W9X0Y1A2B3C4D5", ConsoleKeyID: consoleKey, ReviewerKeyID: reviewerKey,
	})
	if err != nil {
		t.Fatalf("Renew() error = %v", err)
	}
	if result.Console.KeyID == consoleKey || result.Reviewer.KeyID == reviewerKey || len(issuer.calls) != 2 {
		t.Fatalf("recovery result = %#v, calls = %#v", result, issuer.calls)
	}
	for _, call := range issuer.calls {
		if call.kind != "issue" {
			t.Fatalf("expired authority call = %#v, want issue", call)
		}
	}
}

type testSealer struct{}

func (testSealer) Seal(string) (access.CredentialEnvelope, error) {
	return access.CredentialEnvelope{}, nil
}

var _ clock.Clock = fixedClock{}

func TestManagedBindingAcceptsShorterCoreMaximumAndCapsLifetime(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	issuer := &issuerStub{results: make(map[string]access.BridgeIssueResult)}
	sealers := func(_, _ string) (access.CredentialSealer, error) { return testSealer{}, nil }
	for _, lifetime := range []time.Duration{0, -time.Second, 25 * time.Hour} {
		if _, err := New(&tenantStub{}, issuer, sealers, fixedClock{now}, lifetime); err == nil {
			t.Fatalf("invalid lifetime accepted: %v", lifetime)
		}
	}
	tenantID, _ := id.ParseTenant("ten_01K6C3F6M7Z8W9X0Y1A2B3C4D5")
	item, _ := tenant.Restore(tenantID, "Example Organisation", tenant.StateActive, 1, now, now, nil)
	service, err := New(&tenantStub{item: item}, issuer, sealers, fixedClock{now}, 15*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Renew(t.Context(), RenewRequest{
		CommandID: "short-core-renewal-command", DeliveryPublicKey: "delivery-key",
		TenantID: "ten_01K6C3F6M7Z8W9X0Y1A2B3C4D5", ConsoleKeyID: "key_01K6C3F6M7Z8W9X0Y1A2B3C4D9", ReviewerKeyID: "key_01K6C3F6M7Z8W9X0Y1A2B3C4DA",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(issuer.calls) != 2 {
		t.Fatalf("rotations=%d", len(issuer.calls))
	}
	for _, call := range issuer.calls {
		if call.overlap != 15*time.Minute/4 {
			t.Fatalf("short lifetime overlap=%v", call.overlap)
		}
	}
}

func TestCredentialProjectionIsStableAcrossPostgresTimestampPrecision(t *testing.T) {
	t.Parallel()
	stub := &issuerStub{results: make(map[string]access.BridgeIssueResult)}
	pattern, err := access.ParsePattern("tenant:read")
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := stub.result("precision-command", []access.Pattern{pattern})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.October, 1, 9, 0, 0, 456789123, time.UTC)
	expires := at.Add(time.Hour)
	record := access.KeyRecord{ID: fixture.Key.ID(), TenantID: fixture.Key.TenantID(), Label: fixture.Key.Label(), Digest: fixture.Key.Digest(), PepperVersion: fixture.Key.PepperVersion(), Grant: fixture.Key.Grant(), Version: fixture.Key.Version(), CreatedAt: at, UpdatedAt: at, ExpiresAt: &expires}
	fixture.Key, err = access.RestoreKey(record)
	if err != nil {
		t.Fatal(err)
	}
	first, err := credential(fixture)
	if err != nil {
		t.Fatal(err)
	}
	record.CreatedAt, record.UpdatedAt = at.Truncate(time.Microsecond), at.Truncate(time.Microsecond)
	expires = expires.Truncate(time.Microsecond)
	fixture.Key, err = access.RestoreKey(record)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := credential(fixture)
	if err != nil {
		t.Fatal(err)
	}
	a, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(replay)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("regional immutable delivery metadata changed on PostgreSQL replay")
	}
}

type runnerStub struct {
	credential string
	request    SyntheticRunRequest
}

func (stub *runnerStub) Run(_ context.Context, request SyntheticRunRequest, credential string) (string, error) {
	stub.request, stub.credential = request, credential
	return "verified", nil
}

func TestSyntheticJourneyKeepsCredentialInsideCore(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC)
	tenantID, _ := id.ParseTenant("ten_01K6C3F6M7Z8W9X0Y1A2B3C4D5")
	item, _ := tenant.Restore(tenantID, "Idenqa synthetic dep_12345678", tenant.StateActive, 1, now, now, nil)
	issuer := &issuerStub{results: make(map[string]access.BridgeIssueResult)}
	runner := &runnerStub{}
	service, err := New(&tenantStub{item: item}, issuer, func(_, _ string) (access.CredentialSealer, error) { return testSealer{}, nil }, fixedClock{now}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	service, err = service.WithSyntheticRunner(runner)
	if err != nil {
		t.Fatal(err)
	}
	request := SyntheticRunRequest{RunID: "sjr_12345678", DeploymentID: "dep_12345678", CoreEndpoint: "https://core.example.test", ProfileDocument: []byte(`{}`), PolicyDocument: []byte(`{}`), FixtureVersion: "builtin.synthetic.selfie.v1", Region: "ng-lagos-1", ExpectedOutcome: "verified", TimeoutSeconds: 60}
	result, err := service.RunSynthetic(t.Context(), request)
	if err != nil {
		t.Fatalf("RunSynthetic() error = %v", err)
	}
	if result.TenantID != tenantID.String() || result.Outcome != "verified" || runner.credential != "idq_synthetic_secret" {
		t.Fatalf("result=%#v credential=%q", result, runner.credential)
	}
	if len(issuer.calls) != 1 || !slices.Equal(issuer.calls[0].patterns, syntheticPatterns) {
		t.Fatalf("issuer calls = %#v", issuer.calls)
	}
}
