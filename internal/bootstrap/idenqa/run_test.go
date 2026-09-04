package idenqa_test

import (
	"bytes"
	"strings"
	"testing"

	bootstrapidenqa "github.com/Mujhtech/idenqa/internal/bootstrap/idenqa"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func TestRun(t *testing.T) {
	t.Parallel()

	info := buildinfo.Info{Version: "v0.1.0", Commit: "abc1234", Date: "2026-08-27"}
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{
			name:       "version",
			args:       []string{"version"},
			wantCode:   0,
			wantStdout: "idenqa v0.1.0 (commit abc1234, built 2026-08-27)\n",
		},
		{
			name:       "version flag",
			args:       []string{"--version"},
			wantCode:   0,
			wantStdout: "idenqa v0.1.0 (commit abc1234, built 2026-08-27)\n",
		},
		{
			name:       "help",
			args:       []string{"help"},
			wantCode:   0,
			wantStdout: "Usage:",
		},
		{
			name:       "no arguments",
			wantCode:   0,
			wantStdout: "Usage:",
		},
		{
			name:       "unknown command",
			args:       []string{"unknown"},
			wantCode:   2,
			wantStderr: "unknown command",
		},
		{
			name:       "version arguments",
			args:       []string{"version", "extra"},
			wantCode:   2,
			wantStderr: "unknown command",
		},
		{
			name:       "migrate operation required",
			args:       []string{"migrate"},
			wantCode:   2,
			wantStderr: "requires an operation",
		},
		{
			name:       "unknown migrate operation",
			args:       []string{"migrate", "force"},
			wantCode:   2,
			wantStderr: "unknown migrate operation",
		},
		{
			name:       "down requires confirmation",
			args:       []string{"migrate", "down"},
			wantCode:   2,
			wantStderr: "requires --confirm",
		},
		{
			name:       "Headgate migration operation required",
			args:       []string{"migrate", "headgate"},
			wantCode:   2,
			wantStderr: "requires an operation",
		},
		{
			name:       "Headgate down requires confirmation",
			args:       []string{"migrate", "headgate", "down"},
			wantCode:   2,
			wantStderr: "requires --confirm",
		},
		{
			name:       "tenant operation required",
			args:       []string{"tenant"},
			wantCode:   2,
			wantStderr: "requires an operation",
		},
		{
			name:       "tenant create requires audit context",
			args:       []string{"tenant", "create"},
			wantCode:   2,
			wantStderr: "requires --actor and --reason",
		},
		{
			name:       "tenant disable requires version",
			args:       []string{"tenant", "disable", "--id", "ten_01K3P4NQF00000000000000000", "--actor", "test", "--reason", "test"},
			wantCode:   2,
			wantStderr: "positive --version",
		},
		{
			name:       "api-key operation required",
			args:       []string{"api-key"},
			wantCode:   2,
			wantStderr: "requires an operation",
		},
		{
			name:       "unknown api-key operation",
			args:       []string{"api-key", "inspect"},
			wantCode:   2,
			wantStderr: "unknown api-key operation",
		},
		{
			name:       "api-key create requires audit context",
			args:       []string{"api-key", "create"},
			wantCode:   2,
			wantStderr: "requires --actor and --reason",
		},
		{
			name: "api-key create requires metadata",
			args: []string{
				"api-key", "create", "--tenant", "ten_01K3P4NQF00000000000000000",
				"--actor", "test", "--reason", "test",
			},
			wantCode:   2,
			wantStderr: "requires --label and at least one --scope",
		},
		{
			name: "api-key create requires explicit expiry",
			args: []string{
				"api-key", "create", "--tenant", "ten_01K3P4NQF00000000000000000",
				"--actor", "test", "--reason", "test", "--label", "backend", "--scope", "tenant:read",
			},
			wantCode:   2,
			wantStderr: "exactly one of --expires-at or --no-expiry",
		},
		{
			name: "api-key rotate requires confirmation",
			args: []string{
				"api-key", "rotate", "--tenant", "ten_01K3P4NQF00000000000000000",
				"--actor", "test", "--reason", "test", "--id", "key_01K3P4NQF00000000000000000",
				"--overlap", "5m", "--no-expiry",
			},
			wantCode:   2,
			wantStderr: "requires --confirm",
		},
		{
			name: "api-key revoke requires confirmation",
			args: []string{
				"api-key", "revoke", "--tenant", "ten_01K3P4NQF00000000000000000",
				"--actor", "test", "--reason", "test", "--id", "key_01K3P4NQF00000000000000000",
				"--version", "1",
			},
			wantCode:   2,
			wantStderr: "requires --confirm",
		},
		{
			name:       "evidence-key operation required",
			args:       []string{"evidence-key"},
			wantCode:   2,
			wantStderr: "requires an operation",
		},
		{
			name:       "evidence-key init requires target",
			args:       []string{"evidence-key", "init"},
			wantCode:   2,
			wantStderr: "requires a valid --keyring-file",
		},
		{
			name:       "evidence-key rewrap requires exact target",
			args:       []string{"evidence-key", "rewrap"},
			wantCode:   2,
			wantStderr: "requires --keyring-file, --tenant, --id, and positive --version",
		},
		{
			name: "evidence-key rewrap requires confirmation",
			args: []string{
				"evidence-key", "rewrap", "--keyring-file", "/keyring.json",
				"--tenant", "ten_01K3P4NQF00000000000000000",
				"--id", "evd_01K3P4NQF00000000000000000", "--version", "1",
			},
			wantCode:   2,
			wantStderr: "requires --confirm",
		},
		{
			name:       "policy operation required",
			args:       []string{"policy"},
			wantCode:   2,
			wantStderr: "policy requires an operation",
		},
		{
			name:       "policy decision operation required",
			args:       []string{"policy", "decision"},
			wantCode:   2,
			wantStderr: "policy decision requires an operation",
		},
		{
			name:       "policy decision reproduce requires target",
			args:       []string{"policy", "decision", "reproduce"},
			wantCode:   2,
			wantStderr: "requires --tenant and --id",
		},
		{
			name: "policy decision reproduce rejects wrong identifier types",
			args: []string{
				"policy", "decision", "reproduce", "--tenant", "dec_01K3P4NQF00000000000000000",
				"--id", "ten_01K3P4NQF00000000000000000",
			},
			wantCode:   2,
			wantStderr: "tenant identifier is invalid",
		},
		{
			name:       "policy decision verify requires bundle",
			args:       []string{"policy", "decision", "verify"},
			wantCode:   2,
			wantStderr: "requires a valid --bundle-file",
		},
		{
			name: "policy decision output is closed",
			args: []string{
				"policy", "decision", "verify", "--bundle-file", "-", "--output", "yaml",
			},
			wantCode:   2,
			wantStderr: "must be summary, json, or bundle",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer

			gotCode := bootstrapidenqa.Run(test.args, &stdout, &stderr, info)
			if gotCode != test.wantCode {
				t.Errorf("exit code = %d, want %d", gotCode, test.wantCode)
			}
			if !strings.Contains(stdout.String(), test.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), test.wantStdout)
			}
			if !strings.Contains(stderr.String(), test.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), test.wantStderr)
			}
		})
	}
}

func TestRunGeneratesShellCompletion(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := bootstrapidenqa.Run(
		[]string{"completion", "zsh"},
		&stdout,
		&stderr,
		buildinfo.Info{},
	)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "compdef _idenqa idenqa") {
		t.Errorf("completion output does not contain the idenqa zsh registration")
	}
}
