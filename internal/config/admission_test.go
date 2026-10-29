package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Mujhtech/idenqa/internal/config"
)

func TestLoadAdmissionPolicyRequiresExplicitTrustAndLifetime(t *testing.T) {
	t.Parallel()
	if policy, err := config.LoadAdmissionPolicy(""); err != nil || policy != nil {
		t.Fatalf("standalone Core policy=%v: %v", policy, err)
	}
	for _, test := range []struct {
		name, body string
		valid      bool
	}{
		{name: "explicit", body: `{"deploymentId":"dep","region":"eu-west-1","maximumLifetime":"10m","issuers":{"operator":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}`, valid: true},
		{name: "missing lifetime", body: `{"deploymentId":"dep","region":"eu-west-1","issuers":{"operator":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}`},
		{name: "missing trust", body: `{"deploymentId":"dep","region":"eu-west-1","maximumLifetime":"10m"}`},
		{name: "negative lifetime", body: `{"deploymentId":"dep","region":"eu-west-1","maximumLifetime":"-1m","issuers":{"operator":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}`},
		{name: "invalid issuer", body: `{"deploymentId":"dep","region":"eu-west-1","maximumLifetime":"10m","issuers":{"not an identifier":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}`},
		{name: "unknown field", body: `{"deploymentId":"dep","region":"eu-west-1","maximumLifetime":"10m","quotas":{"free":true},"issuers":{"operator":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}}`},
		{name: "trailing body", body: `{} {}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "admission.json")
			if err := os.WriteFile(path, []byte(test.body), 0600); err != nil {
				t.Fatal(err)
			}
			policy, err := config.LoadAdmissionPolicy(path)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v: %v", test.valid, err)
			}
			if test.valid && policy.Region() != "eu-west-1" {
				t.Fatal("installation region changed")
			}
		})
	}
}
