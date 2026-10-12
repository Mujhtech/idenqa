package managedlaunch

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/verification"
)

type recordingCreator struct {
	scope          tenant.Scope
	principal      verification.SessionPrincipal
	delegatedActor string
	idempotencyKey string
	input          verification.SessionCreateInput
}

func (creator *recordingCreator) CreateManaged(_ context.Context, scope tenant.Scope, principal verification.SessionPrincipal, delegatedActor, idempotencyKey string, input verification.SessionCreateInput) (verification.CreatedSession, error) {
	creator.scope = scope
	creator.principal = principal
	creator.delegatedActor = delegatedActor
	creator.idempotencyKey = idempotencyKey
	creator.input = input
	return verification.CreatedSession{}, nil
}

func TestServiceTranslatesVerifiedAuthorityToPinnedManagedCreation(t *testing.T) {
	t.Parallel()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	binding := Binding{OrganisationID: "org_1", EnvironmentID: "env_sandbox", DeploymentID: "dep_1", Region: "eu-west-1"}
	verifier, err := NewVerifier(map[string]string{"preview-key-1": base64.StdEncoding.EncodeToString(public)}, binding, "https://cloud.example.test", time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	creator := &recordingCreator{}
	service, err := NewService(verifier, creator)
	if err != nil {
		t.Fatal(err)
	}
	claims := validClaims(now, binding)
	if _, err := service.Create(context.Background(), signedEnvelope(t, private, claims)); err != nil {
		t.Fatal(err)
	}
	expectedTenant, _ := id.ParseTenant(claims.CoreTenantID)
	if creator.scope.ID() != expectedTenant || creator.principal.String() != claims.WorkloadID || !creator.principal.IsWorkload() || creator.delegatedActor != claims.ActorID || creator.idempotencyKey != claims.CommandID {
		t.Fatalf("unexpected managed creation binding: scope=%s principal=%s actor=%s key=%s", creator.scope.ID(), creator.principal.String(), creator.delegatedActor, creator.idempotencyKey)
	}
	if creator.input.ExpectedProfileRevision == nil || *creator.input.ExpectedProfileRevision != claims.ProfileRevision || creator.input.ProfileID.String() != claims.ProfileID || creator.input.PolicyID.String() != claims.PolicyID {
		t.Fatalf("unexpected pinned input: %#v", creator.input)
	}
	if creator.input.Experience == nil || creator.input.Experience.Origin != claims.Origin || creator.input.Experience.Workflow != claims.ProfileID {
		t.Fatalf("unexpected experience pin request: %#v", creator.input.Experience)
	}
}
