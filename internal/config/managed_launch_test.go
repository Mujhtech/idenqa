package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/config"
)

func TestLoadAPIManagedLaunchAuthorityConfiguration(t *testing.T) {
	clearIDENQAEnvironment(t)
	setRequiredAPIEnvironment(t)
	t.Setenv("IDENQA_MANAGED_LAUNCH_ORGANISATION_ID", "org_example")
	t.Setenv("IDENQA_MANAGED_LAUNCH_ENVIRONMENT_ID", "env_sandbox")
	t.Setenv("IDENQA_MANAGED_LAUNCH_DEPLOYMENT_ID", "dep_example")
	t.Setenv("IDENQA_MANAGED_LAUNCH_ISSUER", "https://cloud.example.test")
	t.Setenv("IDENQA_MANAGED_LAUNCH_PUBLIC_KEYS", "managed-launch-1=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
	t.Setenv("IDENQA_MANAGED_LAUNCH_AUTHORITY_MAX_TTL", "1m")

	configuration, err := config.LoadAPI("")
	if err != nil {
		t.Fatal(err)
	}
	if configuration.ManagedLaunchOrganisationID != "org_example" || configuration.ManagedLaunchEnvironmentID != "env_sandbox" || configuration.ManagedLaunchDeploymentID != "dep_example" || configuration.ManagedLaunchIssuer != "https://cloud.example.test" || configuration.ManagedLaunchAuthorityTTL != time.Minute {
		t.Fatalf("managed launch configuration = %#v", configuration)
	}
	if configuration.ManagedLaunchPublicKeys.Values()["managed-launch-1"] == "" {
		t.Fatal("managed launch public key was not decoded")
	}
}

func TestLoadAPIRejectsPartialManagedLaunchAuthorityConfiguration(t *testing.T) {
	clearIDENQAEnvironment(t)
	setRequiredAPIEnvironment(t)
	t.Setenv("IDENQA_MANAGED_LAUNCH_ISSUER", "https://cloud.example.test")

	_, err := config.LoadAPI("")
	if err == nil || !strings.Contains(err.Error(), "managed launch binding") {
		t.Fatalf("LoadAPI() error = %v", err)
	}
}
