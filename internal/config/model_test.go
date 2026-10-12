package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	"github.com/Mujhtech/idenqa/internal/model"
)

func TestModelBundleIsClosedBoundedAndUnambiguous(t *testing.T) {
	t.Parallel()
	value := ModelRuntime{Binding: model.Binding{Registry: &model.RegistrySelection{Name: "fixture", ModelRevision: 1, ThresholdRevision: 1, ModelDigest: "sha256:" + strings.Repeat("a", 64), ThresholdDigest: "sha256:" + strings.Repeat("b", 64)}, Configuration: modelv1.ConfigurationReference{ModelID: "mdl_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ConfigurationRef: "configuration://fixture/model", ConfigurationDigest: "sha256:" + strings.Repeat("a", 64)}}, RunnerAddress: "fixture:443", RunnerCAFile: "ca", RunnerServerName: "fixture", RunnerCredentialFile: "runner", GatewayCredentialFile: "gateway"}
	unregistered := value
	unregistered.Binding.Registry = nil
	for _, tc := range []struct {
		name string
		body any
		ok   bool
	}{
		{"legacy", value, false},
		{"unregistered", map[string]any{"models": []ModelRuntime{unregistered}}, false},
		{"bundle", map[string]any{"models": []ModelRuntime{value}}, true},
		{"missing models", map[string]any{}, false},
		{"null models", map[string]any{"models": nil}, false},
		{"empty", map[string]any{"models": []ModelRuntime{}}, false},
		{"duplicate", map[string]any{"models": []ModelRuntime{value, value}}, false},
		{"mixed", map[string]any{"models": []ModelRuntime{value}, "runner_address": "other"}, false},
		{"unknown", map[string]any{"models": []ModelRuntime{value}, "fallback": true}, false},
		{"too many", map[string]any{"models": make([]ModelRuntime, 9)}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := json.Marshal(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "model.json")
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			got, err := LoadModelRuntimes(path)
			if (err == nil) != tc.ok || (tc.ok && len(got) != 1) {
				t.Fatalf("bundle outcome: %v %v", got, err)
			}
		})
	}
}
