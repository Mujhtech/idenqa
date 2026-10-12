package config

import (
	"errors"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"

	"github.com/Mujhtech/idenqa/internal/model"
)

// ModelRuntime is an explicitly mounted model route and private transport.
type ModelRuntime struct {
	Manifest modelv1.Manifest `json:"manifest"`

	Binding               model.Binding `json:"binding"`
	RunnerAddress         string        `json:"runner_address"`
	RunnerCAFile          string        `json:"runner_ca_file"`
	RunnerServerName      string        `json:"runner_server_name"`
	RunnerCredentialFile  string        `json:"runner_credential_file"`
	GatewayCredentialFile string        `json:"gateway_credential_file"`
}

// LoadModelRuntimes reads a closed bundle of one to eight model routes.
func LoadModelRuntimes(path string) ([]ModelRuntime, error) {
	var bundle struct {
		Models []ModelRuntime `json:"models"`
	}
	if err := ReadClosedFile(path, &bundle, 512<<10); err != nil {
		return nil, err
	}
	values := bundle.Models
	if len(values) == 0 || len(values) > 8 {
		return nil, errors.New("model bundle requires one to eight routes")
	}
	seen := map[modelv1.ConfigurationReference]bool{}
	for _, value := range values {
		if value.RunnerAddress == "" || value.RunnerCAFile == "" || value.RunnerServerName == "" || value.RunnerCredentialFile == "" || value.GatewayCredentialFile == "" || value.Binding.Configuration.Validate() != nil || value.Binding.Registry == nil || value.Binding.Registry.Validate() != nil || seen[value.Binding.Configuration] {
			return nil, errors.New("model bundle contains incomplete or duplicate routes")
		}
		seen[value.Binding.Configuration] = true
	}
	return values, nil
}
