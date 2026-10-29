package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/Mujhtech/idenqa/adapters/providers/dojah"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

type currentCredentialFixture struct {
	version string
	err     error
	calls   int
}

func (f *currentCredentialFixture) CurrentVersion(context.Context, string) (string, error) {
	f.calls++
	return f.version, f.err
}
func TestPreparationPinsCurrentRegionalVersionBeforeRequestPersistence(t *testing.T) {
	source := &currentCredentialFixture{version: "pvc_first"}
	preparation := &Preparation{CurrentCredential: source}
	base := providerv1.ConfigurationReference{ProviderID: "pvd_01K4AR9V8FQ2G7ZXCPNM5T6JWH", SchemaDigest: dojah.Description().Configuration.Digest, SecretReference: "secret://broker/pva_fixture", CredentialVersion: "regional-current"}
	one, err := preparation.resolveConfiguration(t.Context(), base)
	if err != nil || one.CredentialVersion != "pvc_first" {
		t.Fatal("initial version not pinned", err)
	}
	source.version = "pvc_second"
	two, err := preparation.resolveConfiguration(t.Context(), base)
	if err != nil || two.CredentialVersion != "pvc_second" || one.CredentialVersion != "pvc_first" {
		t.Fatal("rotation rewrote prepared configuration", err)
	}
	if base.CredentialVersion != "regional-current" {
		t.Fatal("shared route mutated")
	}
	if _, err = preparation.resolveConfiguration(t.Context(), one); err != nil || source.calls != 2 {
		t.Fatal("persisted pin resolved as current again", err)
	}
	source.err = errors.New("broker unavailable")
	if _, err = preparation.resolveConfiguration(t.Context(), base); err == nil {
		t.Fatal("missing current authority did not fail closed")
	}
}
