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

func TestCaptureTokenKeysDecodeAndRedaction(t *testing.T) {
	t.Parallel()

	one := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	two := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	var keys config.CaptureTokenKeys
	if err := keys.Decode("1=" + one + ",2=" + two); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	values := keys.Values()
	values[1][0] = 99
	delete(values, 2)
	if got := keys.Values(); got[1][0] != 1 || len(got) != 2 {
		t.Fatal("Values() exposed internal capture-token key configuration")
	}
	if got := fmt.Sprintf("%s|%#v", keys, keys); got != "[REDACTED]|[REDACTED]" {
		t.Fatalf("formatted capture-token keys = %q", got)
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), one) || string(encoded) != `"[REDACTED]"` {
		t.Fatalf("JSON capture-token keys = %s", encoded)
	}
}

func TestLoadAPICaptureTokenConfiguration(t *testing.T) {
	clearIDENQAEnvironment(t)
	setRequiredAPIEnvironment(t)
	key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	t.Setenv("IDENQA_CAPTURE_TOKEN_ACTIVE_KEY_VERSION", "2")
	t.Setenv("IDENQA_CAPTURE_TOKEN_KEYS", "1="+key+",2="+key)

	configuration, err := config.LoadAPI("")
	if err != nil {
		t.Fatalf("LoadAPI() error = %v", err)
	}
	if configuration.CaptureTokenActiveVersion != 2 || len(configuration.CaptureTokenKeys.Values()) != 2 {
		t.Fatal("capture-token key configuration was not loaded")
	}
}

func TestOutcomeTokenKeysDecodeAndRedaction(t *testing.T) {
	t.Parallel()

	one := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{8}, 32))
	var keys config.OutcomeTokenKeys
	if err := keys.Decode("1=" + one); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	values := keys.Values()
	values[1][0] = 99
	if got := keys.Values(); got[1][0] != 8 {
		t.Fatal("Values() exposed internal outcome-token key configuration")
	}
	if got := fmt.Sprintf("%s|%#v", keys, keys); got != "[REDACTED]|[REDACTED]" {
		t.Fatalf("formatted outcome-token keys = %q", got)
	}
	encoded, err := json.Marshal(keys)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), one) || string(encoded) != `"[REDACTED]"` {
		t.Fatalf("JSON outcome-token keys = %s", encoded)
	}
}

func TestLoadAPIRejectsInvalidCaptureTokenConfiguration(t *testing.T) {
	key := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	tests := []map[string]string{
		{"IDENQA_CAPTURE_TOKEN_ACTIVE_KEY_VERSION": "1"},
		{"IDENQA_CAPTURE_TOKEN_KEYS": "1=" + key},
		{"IDENQA_CAPTURE_TOKEN_ACTIVE_KEY_VERSION": "2", "IDENQA_CAPTURE_TOKEN_KEYS": "1=" + key},
		{"IDENQA_VERIFICATION_DEFAULT_TTL": "0s"},
		{"IDENQA_VERIFICATION_DEFAULT_TTL": "2h", "IDENQA_VERIFICATION_MAXIMUM_TTL": "1h"},
		{"IDENQA_CAPTURE_TOKEN_DEFAULT_TTL": "0s"},
		{"IDENQA_CAPTURE_TOKEN_DEFAULT_TTL": "2h", "IDENQA_CAPTURE_TOKEN_MAXIMUM_TTL": "1h"},
		{"IDENQA_VERIFICATION_MAXIMUM_TTL": "1h", "IDENQA_CAPTURE_TOKEN_MAXIMUM_TTL": "2h"},
		{"IDENQA_OUTCOME_TOKEN_DEFAULT_POST_EXPIRY_TTL": "0s"},
		{"IDENQA_OUTCOME_TOKEN_DEFAULT_POST_EXPIRY_TTL": "2h", "IDENQA_OUTCOME_TOKEN_MAXIMUM_POST_EXPIRY_TTL": "1h"},
		{"IDENQA_OUTCOME_TOKEN_MAXIMUM_POST_EXPIRY_TTL": "721h"},
		{"IDENQA_VERIFICATION_IDEMPOTENCY_RETENTION": "0s"},
	}
	for index, environment := range tests {
		t.Run(fmt.Sprintf("case-%d", index), func(t *testing.T) {
			clearIDENQAEnvironment(t)
			setRequiredAPIEnvironment(t)
			for name, value := range environment {
				t.Setenv(name, value)
			}
			if _, err := config.LoadAPI(""); err == nil {
				t.Fatal("LoadAPI() error = nil")
			}
		})
	}
}
