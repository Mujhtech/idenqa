package config_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/config"
)

func TestPeppersDecodeAndRedaction(t *testing.T) {
	t.Parallel()

	one := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	two := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	var peppers config.Peppers
	if err := peppers.Decode("1=" + one + ",2=" + two); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	values := peppers.Values()
	values[1][0] = 99
	delete(values, 2)
	if got := peppers.Values(); got[1][0] != 1 || len(got) != 2 {
		t.Fatal("Values() exposed internal pepper configuration")
	}
	if got := fmt.Sprintf("%s|%#v", peppers, peppers); got != "[REDACTED]|[REDACTED]" {
		t.Fatalf("formatted peppers = %q", got)
	}
	encoded, err := json.Marshal(peppers)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), one) || string(encoded) != `"[REDACTED]"` {
		t.Fatalf("JSON peppers = %s", encoded)
	}
}

func TestPeppersDecodeRejectsInvalidAndDoesNotPartiallyReplace(t *testing.T) {
	t.Parallel()

	valid := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	tests := []string{
		"",
		" 1=" + valid,
		"1",
		"0=" + valid,
		"65536=" + valid,
		"one=" + valid,
		"1=" + valid + ",1=" + valid,
		"1=AQ",
		"1=" + valid + "=",
	}
	for _, value := range tests {
		value := value
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			var peppers config.Peppers
			if err := peppers.Decode("1=" + valid); err != nil {
				t.Fatalf("initial Decode() error = %v", err)
			}
			if err := peppers.Decode(value); err == nil {
				t.Fatalf("Decode(%q) error = nil", value)
			}
			if got := peppers.Values(); len(got) != 1 || got[1][0] != 1 {
				t.Fatal("failed Decode() partially replaced configuration")
			}
		})
	}
}

func TestLoadAPIPepperConfiguration(t *testing.T) {
	clearIDENQAEnvironment(t)
	setRequiredAPIEnvironment(t)
	pepper := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	t.Setenv("IDENQA_API_KEY_ACTIVE_PEPPER_VERSION", "2")
	t.Setenv("IDENQA_API_KEY_PEPPERS", "1="+pepper+",2="+pepper)

	configuration, err := config.LoadAPI("")
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if configuration.APIKeyActivePepperVersion != 2 || len(configuration.APIKeyPeppers.Values()) != 2 {
		t.Fatalf("API-key pepper configuration was not loaded")
	}
}

func TestLoadAPIRejectsIncompletePepperConfiguration(t *testing.T) {
	pepper := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	tests := []map[string]string{
		{"IDENQA_API_KEY_ACTIVE_PEPPER_VERSION": "1"},
		{"IDENQA_API_KEY_PEPPERS": "1=" + pepper},
		{"IDENQA_API_KEY_ACTIVE_PEPPER_VERSION": "2", "IDENQA_API_KEY_PEPPERS": "1=" + pepper},
	}
	for index, environment := range tests {
		index, environment := index, environment
		t.Run(fmt.Sprintf("case-%d", index), func(t *testing.T) {
			clearIDENQAEnvironment(t)
			setRequiredAPIEnvironment(t)
			for key, value := range environment {
				t.Setenv(key, value)
			}
			if _, err := config.LoadAPI(""); err == nil {
				t.Fatal("LoadAPI() error = nil")
			}
		})
	}
}
