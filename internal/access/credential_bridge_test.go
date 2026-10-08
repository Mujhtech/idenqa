package access_test

import (
	"context"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

func TestCredentialBridgeIssueReturnsCredentialOnlyForCreatedCommand(t *testing.T) {
	t.Parallel()

	repository := &bridgeRepositoryStub{}
	issuer, _ := newTestIssuer(t, repository, false, 24*time.Hour, time.Hour)
	bridge, err := access.NewCredentialBridge(issuer, repository)
	if err != nil {
		t.Fatalf("NewCredentialBridge() error = %v", err)
	}
	tenantID, _ := testKeyIDs(t)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	pattern, err := access.ParsePattern("tenant:read")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	digest := sha256.Sum256([]byte("canonical command"))
	command := access.BridgeCommand{ID: "command-1", RequestDigest: digest}
	input := access.IssueInput{
		Label:    "cloud application",
		Patterns: []access.Pattern{pattern},
		Expiry:   access.ExpiringAt(keyClock{}.Now().Add(time.Hour)),
	}
	action := access.AdminAction{Actor: "deployment-agent", Reason: "application credential issue"}

	sealer := bridgeSealerStub{}
	first, err := bridge.Issue(t.Context(), command, action, scope, input, sealer)
	if err != nil {
		t.Fatalf("Issue(first) error = %v", err)
	}
	if !first.Created || first.Envelope.Ciphertext == "" {
		t.Fatal("first issue did not return an encrypted credential envelope")
	}

	repository.replay = true
	second, err := bridge.Issue(t.Context(), command, action, scope, input, sealer)
	if err != nil {
		t.Fatalf("Issue(replay) error = %v", err)
	}
	if second.Created || second.Envelope != first.Envelope {
		t.Fatal("replayed issue did not return the original encrypted envelope")
	}
	if second.Key.ID().String() != first.Key.ID().String() {
		t.Fatal("replayed issue did not return original key metadata")
	}
	observed, observedEnvelope, err := bridge.Observe(t.Context(), command, scope)
	if err != nil {
		t.Fatalf("Observe() error = %v", err)
	}
	if observed.ID().String() != first.Key.ID().String() {
		t.Fatal("Observe() did not return committed key metadata")
	}
	if observedEnvelope != first.Envelope {
		t.Fatal("Observe() did not return the committed encrypted envelope")
	}
}

// This test exists because the Cloud bridge is useful only if the exact
// display-once credential it returns is accepted by Core's ordinary tenant
// authentication boundary with the requested immutable grant.
func TestCredentialBridgeIssuedCredentialAuthenticatesThroughCore(t *testing.T) {
	t.Parallel()

	repository := &bridgeRepositoryStub{}
	issuer, peppers := newTestIssuer(t, repository, false, 24*time.Hour, time.Hour)
	bridge, err := access.NewCredentialBridge(issuer, repository)
	if err != nil {
		t.Fatalf("NewCredentialBridge() error = %v", err)
	}
	tenantID, _ := testKeyIDs(t)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	pattern, err := access.ParsePattern("tenant:read")
	if err != nil {
		t.Fatalf("ParsePattern() error = %v", err)
	}
	command := access.BridgeCommand{ID: "command-authenticate", RequestDigest: sha256.Sum256([]byte("authenticate command"))}
	sealer := &capturingBridgeSealer{}
	result, err := bridge.Issue(t.Context(), command, access.AdminAction{
		Actor: "deployment-agent", Reason: "application credential issue",
	}, scope, access.IssueInput{
		Label: "cloud application", Patterns: []access.Pattern{pattern},
		Expiry: access.ExpiringAt(keyClock{}.Now().Add(time.Hour)),
	}, sealer)
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if !result.Created || sealer.credential == "" {
		t.Fatal("bridge did not deliver the newly issued credential")
	}

	authenticator, err := access.NewAuthenticator(repository, peppers, keyClock{})
	if err != nil {
		t.Fatalf("NewAuthenticator() error = %v", err)
	}
	accessContext, err := authenticator.Authenticate(t.Context(), sealer.credential)
	if err != nil {
		t.Fatalf("Authenticate(bridge credential) error = %v", err)
	}
	if accessContext.Principal().KeyID().String() != result.Key.ID().String() {
		t.Fatalf("authenticated key = %q, want %q", accessContext.Principal().KeyID(), result.Key.ID())
	}
	if err := accessContext.Require(access.PermissionTenantRead); err != nil {
		t.Fatalf("Require(tenant:read) error = %v", err)
	}
	if err := accessContext.Require(access.PermissionVerificationSessionsCreate); err == nil {
		t.Fatal("bridge credential received authority outside its requested grant")
	}
}

type bridgeSealerStub struct{}

func (bridgeSealerStub) Seal(credential string) (access.CredentialEnvelope, error) {
	return access.CredentialEnvelope{Algorithm: "test", Ciphertext: "sealed:" + credential}, nil
}

type capturingBridgeSealer struct{ credential string }

func (sealer *capturingBridgeSealer) Seal(credential string) (access.CredentialEnvelope, error) {
	sealer.credential = credential
	return access.CredentialEnvelope{Algorithm: "test", Ciphertext: "encrypted"}, nil
}

func (repository *bridgeRepositoryStub) ObserveBridgeCommand(
	context.Context,
	tenant.Scope,
	access.BridgeCommand,
) (access.Key, access.CredentialEnvelope, error) {
	return repository.created, repository.envelope, nil
}

func (repository *bridgeRepositoryStub) FindForVerification(
	_ context.Context,
	tenantHint id.Tenant,
	keyID id.APIKey,
) (access.VerificationRecord, error) {
	if repository.created.ID().String() != keyID.String() || repository.created.TenantID().String() != tenantHint.String() {
		return access.VerificationRecord{}, access.ErrKeyNotFound
	}
	return access.NewVerificationRecord(repository.created, tenant.StateActive)
}

type bridgeRepositoryStub struct {
	created  access.Key
	envelope access.CredentialEnvelope
	replay   bool
}

func (repository *bridgeRepositoryStub) Create(context.Context, tenant.Scope, access.Key) error {
	return nil
}

func (repository *bridgeRepositoryStub) Find(context.Context, tenant.Scope, id.APIKey) (access.Key, error) {
	panic("unreachable")
}

func (repository *bridgeRepositoryStub) Rotate(
	context.Context,
	tenant.Scope,
	access.Key,
	int64,
	access.Key,
) error {
	panic("unreachable")
}

func (repository *bridgeRepositoryStub) IssueBridgeCommand(
	_ context.Context,
	_ access.AdminAction,
	_ tenant.Scope,
	_ access.BridgeCommand,
	key access.Key,
	envelope access.CredentialEnvelope,
) (access.Key, access.CredentialEnvelope, bool, error) {
	if repository.replay {
		return repository.created, repository.envelope, false, nil
	}
	repository.created = key
	repository.envelope = envelope

	return key, envelope, true, nil
}

type expiryBridgeRepository struct {
	bridgeRepositoryStub
	retiring access.Key
}

func (*expiryBridgeRepository) ObserveBridgeCommand(context.Context, tenant.Scope, access.BridgeCommand) (access.Key, access.CredentialEnvelope, error) {
	return access.Key{}, access.CredentialEnvelope{}, access.ErrKeyNotFound
}
func (r *expiryBridgeRepository) FindAdministrative(context.Context, tenant.Scope, id.APIKey) (access.Key, error) {
	return r.created, nil
}
func (r *expiryBridgeRepository) RotateBridgeCommand(_ context.Context, _ access.AdminAction, _ tenant.Scope, _ access.BridgeCommand, predecessor access.Key, _ int64, successor access.Key, envelope access.CredentialEnvelope) (access.Key, access.CredentialEnvelope, bool, error) {
	r.retiring = predecessor
	return successor, envelope, true, nil
}
func (*expiryBridgeRepository) RevokeBridgeCommand(context.Context, access.AdminAction, tenant.Scope, access.BridgeCommand, access.Key, int64) (access.Key, bool, error) {
	panic("unused")
}

func TestCredentialBridgeOverlapCannotExtendPredecessorExpiry(t *testing.T) {
	t.Parallel()
	repository := &expiryBridgeRepository{}
	issuer, _ := newTestIssuer(t, repository, false, 24*time.Hour, time.Hour)
	bridge, err := access.NewCredentialBridge(issuer, repository)
	if err != nil {
		t.Fatal(err)
	}
	tenantID, _ := testKeyIDs(t)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatal(err)
	}
	pattern, err := access.ParsePattern("tenant:read")
	if err != nil {
		t.Fatal(err)
	}
	now := keyClock{}.Now()
	action := access.AdminAction{Actor: "regional-agent", Reason: "bounded credential rotation"}
	first, err := bridge.Issue(t.Context(), access.BridgeCommand{ID: "issue-expiry", RequestDigest: sha256.Sum256([]byte("issue"))}, action, scope, access.IssueInput{Label: "regional binding", Patterns: []access.Pattern{pattern}, Expiry: access.ExpiringAt(now.Add(time.Minute))}, bridgeSealerStub{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bridge.Rotate(t.Context(), access.BridgeCommand{ID: "rotate-expiry", RequestDigest: sha256.Sum256([]byte("rotate"))}, action, scope, access.RotateInput{KeyID: first.Key.ID(), Overlap: 10 * time.Minute, Expiry: access.ExpiringAt(now.Add(2 * time.Minute))}, bridgeSealerStub{})
	if err != nil {
		t.Fatal(err)
	}
	if repository.retiring.RetiredAt() == nil || !repository.retiring.RetiredAt().Equal(*first.Key.ExpiresAt()) {
		t.Fatal("overlap extended predecessor original expiry")
	}
}
