package config

import (
	"crypto/ed25519"
	"encoding/base64"
	"time"

	"github.com/Mujhtech/idenqa/internal/admission"
)

// LoadAdmissionPolicy loads only deployment identity, an explicit maximum policy
// lifetime and pinned public keys. An empty path leaves standalone Core enabled
// without managed operational limits. It never selects grace or quota quantities.
func LoadAdmissionPolicy(path string) (*admission.Policy, error) {
	if path == "" {
		return nil, nil
	}
	var file struct {
		DeploymentID    string            `json:"deploymentId"`
		Region          string            `json:"region"`
		MaximumLifetime string            `json:"maximumLifetime"`
		Issuers         map[string]string `json:"issuers"`
	}
	if err := ReadClosedFile(path, &file, 16<<10); err != nil {
		return nil, err
	}
	lifetime, err := time.ParseDuration(file.MaximumLifetime)
	if err != nil {
		return nil, admission.ErrInvalid
	}
	keys := make(map[string]ed25519.PublicKey, len(file.Issuers))
	for issuer, encoded := range file.Issuers {
		key, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if err != nil {
			return nil, admission.ErrInvalid
		}
		keys[issuer] = ed25519.PublicKey(key)
	}
	return admission.NewPolicy(admission.Config{DeploymentID: file.DeploymentID, Region: file.Region, MaximumLifetime: lifetime, Issuers: keys})
}
