package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/spf13/cobra"
)

func TestExecuteArgsClassifiesErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		runError   error
		wantCode   int
		wantStderr string
		wantUsage  bool
	}{
		{name: "success", wantCode: 0},
		{name: "usage", runError: cli.UsageError(errors.New("invalid input")), wantCode: 2, wantStderr: "invalid input", wantUsage: true},
		{name: "runtime", runError: cli.RuntimeError("run command", errors.New("failed")), wantCode: 1, wantStderr: "run command: failed"},
		{name: "reported runtime", runError: cli.ReportedRuntimeError(errors.New("already logged")), wantCode: 1},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := cli.NewRoot(cli.RootOptions{
				Use:     "test-command",
				Short:   "test command",
				Version: "test-command v1",
				RunE: func(_ *cobra.Command, _ []string) error {
					return test.runError
				},
			})
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := cli.ExecuteArgs(context.Background(), root, nil, &stdout, &stderr)

			if code != test.wantCode {
				t.Errorf("exit code = %d, want %d", code, test.wantCode)
			}
			if !strings.Contains(stderr.String(), test.wantStderr) {
				t.Errorf("stderr = %q, want it to contain %q", stderr.String(), test.wantStderr)
			}
			if gotUsage := strings.Contains(stderr.String(), "Usage:"); gotUsage != test.wantUsage {
				t.Errorf("stderr usage presence = %t, want %t; stderr = %q", gotUsage, test.wantUsage, stderr.String())
			}
		})
	}
}

func TestNewRootProvidesVersionAndCompletion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		args       []string
		wantStdout string
	}{
		{name: "version command", args: []string{"version"}, wantStdout: "test-command v1\n"},
		{name: "version flag", args: []string{"--version"}, wantStdout: "test-command v1\n"},
		{name: "completion", args: []string{"completion", "zsh"}, wantStdout: "compdef _test-command test-command"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			root := cli.NewRoot(cli.RootOptions{Use: "test-command", Short: "test command", Version: "test-command v1"})
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := cli.ExecuteArgs(context.Background(), root, test.args, &stdout, &stderr)

			if code != 0 {
				t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.wantStdout) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), test.wantStdout)
			}
		})
	}
}
