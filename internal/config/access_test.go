package config_test

import (
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/config"
)

func TestLoadAPIAccessPolicyConfiguration(t *testing.T) {
	clearIDENQAEnvironment(t)
	setRequiredAPIEnvironment(t)
	t.Setenv("IDENQA_API_KEY_ALLOW_NO_EXPIRY", "true")
	t.Setenv("IDENQA_API_KEY_MAXIMUM_LIFETIME", "2160h")
	t.Setenv("IDENQA_API_KEY_MAXIMUM_ROTATION_OVERLAP", "24h")

	configuration, err := config.LoadAPI("")
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if !configuration.APIKeyAllowNoExpiry || configuration.APIKeyMaximumLifetime != 2160*time.Hour ||
		configuration.APIKeyMaximumOverlap != 24*time.Hour {
		t.Fatalf("API-key policy configuration = %+v", configuration)
	}
}

func TestLoadAPIRejectsNegativeAccessPolicyDuration(t *testing.T) {
	tests := []string{"IDENQA_API_KEY_MAXIMUM_LIFETIME", "IDENQA_API_KEY_MAXIMUM_ROTATION_OVERLAP"}
	for _, variable := range tests {
		variable := variable
		t.Run(variable, func(t *testing.T) {
			clearIDENQAEnvironment(t)
			setRequiredAPIEnvironment(t)
			t.Setenv(variable, "-1s")
			if _, err := config.LoadAPI(""); err == nil || !strings.Contains(err.Error(), "must not be negative") {
				t.Fatalf("LoadAPI() error = %v", err)
			}
		})
	}
}
