package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"

	"github.com/Mujhtech/idenqa/internal/provider"
)

// ProviderRuntime is an explicitly mounted first-provider route and private transport.
type ProviderRuntime struct {
	Adapter               string           `json:"adapter,omitempty"`
	Binding               provider.Binding `json:"binding"`
	RunnerAddress         string           `json:"runner_address"`
	RunnerCAFile          string           `json:"runner_ca_file"`
	RunnerServerName      string           `json:"runner_server_name"`
	RunnerCredentialFile  string           `json:"runner_credential_file"`
	GatewayCredentialFile string           `json:"gateway_credential_file"`
}

// LoadProviderRuntime reads closed bounded reference-only operator configuration.
func LoadProviderRuntime(path string) (ProviderRuntime, error) {
	var value ProviderRuntime
	if err := ReadClosedFile(path, &value, 64<<10); err != nil {
		return value, err
	}
	if value.Adapter != "" && value.Adapter != "dojah" && value.Adapter != "smileid" {
		return value, errors.New("unsupported provider runtime adapter")
	}
	if value.RunnerAddress == "" || value.RunnerCAFile == "" || value.RunnerServerName == "" || value.RunnerCredentialFile == "" || value.GatewayCredentialFile == "" {
		return value, errors.New("provider runtime configuration is incomplete")
	}
	return value, nil
}

// ReadClosedFile reads bounded JSON without disclosing its contents in errors.
func ReadClosedFile(path string, target any, limit int64) error {
	file, err := os.Open(path) // #nosec G304 -- operator-mounted configuration or credential path, never request input.
	if err != nil {
		return errors.New("open runtime configuration")
	}
	defer func() { _ = file.Close() }()
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return errors.New("read bounded runtime configuration")
	}
	defer clear(raw)
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil {
		return errors.New("invalid runtime configuration")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing runtime configuration")
	}
	return nil
}

// ReadCredentialFile reads a bounded owner-only mounted secret, without environment exposure.
func ReadCredentialFile(path string) (string, error) {
	file, err := os.Open(path) // #nosec G304 -- operator-mounted configuration or credential path, never request input.
	if err != nil {
		return "", errors.New("open mounted credential")
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("mounted credential must be an owner-only regular file")
	}
	raw, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(raw) > 4096 {
		return "", errors.New("read mounted credential")
	}
	defer clear(raw)
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", errors.New("invalid mounted credential")
	}
	return value, nil
}
