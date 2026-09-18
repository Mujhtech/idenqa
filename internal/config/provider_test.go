package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/config"
)

func TestMountedCredentialsAreBoundedAndPrivate(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, value string
		mode        os.FileMode
		valid       bool
	}{{"private", "mounted-test-value\n", 0600, true}, {"public", "mounted-test-value", 0644, false}, {"empty", "", 0600, false}, {"multiple lines", "one\ntwo", 0600, false}, {"oversized", strings.Repeat("x", 4097), 0600, false}} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "credential")
			if err := os.WriteFile(path, []byte(test.value), test.mode); err != nil {
				t.Fatal(err)
			}
			value, err := config.ReadCredentialFile(path)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v err=%v", test.valid, err)
			}
			if err != nil && (value != "" || strings.Contains(err.Error(), test.value) && test.value != "") {
				t.Fatal("credential exposed in error")
			}
		})
	}
}
func TestRuntimeConfigurationRejectsUnknownAndTrailingData(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{"unknown":"private-secret"}`, `{"known":"value"} {}`, strings.Repeat(" ", 129)} {
		path := filepath.Join(t.TempDir(), "runtime.json")
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		var target struct {
			Known string `json:"known"`
		}
		if err := config.ReadClosedFile(path, &target, 128); err == nil || strings.Contains(err.Error(), "private-secret") {
			t.Fatalf("unsafe configuration accepted or disclosed: %v", err)
		}
	}
}
