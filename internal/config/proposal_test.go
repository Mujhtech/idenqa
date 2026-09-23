package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/config"
)

func TestLoadProposalRuntimes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "proposal.json")
	raw := []byte(`{
  "models": [
    {"model_id":"ai.review","model_version":"meta-llama/Llama-3.1","prompt_version":"review-p1","instructions":"Return only bounded review actions.","model_registry_id":"mdl_01ARZ3NDEKTSV4RRFFQ69G5FAV","model_registry_version":1,"model_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","prompt_registry_id":"prm_01ARZ3NDEKTSV4RRFFQ69G5FAV","prompt_registry_version":1,"prompt_digest":"fa59d1615ec3bb19f0b649680c8f65ef00dbb24b9b41926cee37066adbd50ca2","adapter":"openai_compatible","origin":"https://api.openai.com","credential_reference":"secret://file/run/secrets/openai"},
    {"model_id":"ai.policy","model_version":"claude-snapshot","prompt_version":"policy-p1","instructions":"Return only bounded policy actions.","model_registry_id":"mdl_01ARZ3NDEKTSV4RRFFQ69G5FAW","model_registry_version":2,"model_digest":"1123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","prompt_registry_id":"prm_01ARZ3NDEKTSV4RRFFQ69G5FAW","prompt_registry_version":3,"prompt_digest":"5552dc249a7c5f83fa9128a0939d5854c078745f80d448a83f75c7d51e4cdad2","adapter":"anthropic","origin":"https://api.anthropic.com","credential_reference":"secret://aws/prod/anthropic","max_output_tokens":4096}
  ]
}`)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runtimes, err := config.LoadProposalRuntimes(path)
	if err != nil {
		t.Fatalf("LoadProposalRuntimes() error = %v", err)
	}
	if len(runtimes.Models) != 2 || runtimes.Models[0].ModelVersion != "meta-llama/Llama-3.1" || runtimes.Models[0].MaxOutputTokens != 2048 || runtimes.Models[1].MaxOutputTokens != 4096 {
		t.Fatalf("LoadProposalRuntimes() = %+v", runtimes)
	}
}

func TestLoadProposalRuntimesRejectsInvalidRoutes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{name: "unknown adapter", raw: validRuntimeRoute(`"adapter":"openai_compatible"`, `"adapter":"other"`)},
		{name: "duplicate route", raw: `{"models":[` + validRuntimeRouteObject() + `,` + validRuntimeRouteObject() + `]}`},
		{name: "inline credential", raw: validRuntimeRoute(`"credential_reference":"secret://file/run/key"`, `"credential_reference":"plaintext"`)},
		{name: "missing registry binding", raw: `{"models":[{"model_id":"ai.review","model_version":"v1","prompt_version":"p1","adapter":"openai_compatible","origin":"https://example.com","credential_reference":"secret://file/run/key"}]}`},
		{name: "prompt digest mismatch", raw: validRuntimeRoute(`"prompt_digest":"73060ad79f2c28d9235e910e8040ca93e7e6a611a3df4f0aa3d0cf3616b37ba5"`, `"prompt_digest":"abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "proposal.json")
			if err := os.WriteFile(path, []byte(test.raw), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			if _, err := config.LoadProposalRuntimes(path); err == nil {
				t.Fatal("LoadProposalRuntimes() error = nil")
			}
		})
	}
}

func validRuntimeRoute(oldValue, newValue string) string {
	return `{"models":[` + strings.Replace(validRuntimeRouteObject(), oldValue, newValue, 1) + `]}`
}

func validRuntimeRouteObject() string {
	base := `"model_id":"ai.review","model_version":"v1","prompt_version":"p1","instructions":"bounded","model_registry_id":"mdl_01ARZ3NDEKTSV4RRFFQ69G5FAV","model_registry_version":1,"model_digest":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","prompt_registry_id":"prm_01ARZ3NDEKTSV4RRFFQ69G5FAV","prompt_registry_version":1,"prompt_digest":"73060ad79f2c28d9235e910e8040ca93e7e6a611a3df4f0aa3d0cf3616b37ba5","adapter":"openai_compatible","origin":"https://example.com","credential_reference":"secret://file/run/key"`
	return `{` + base + `}`
}
