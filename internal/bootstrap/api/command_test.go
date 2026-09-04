package api_test

import (
	"bytes"
	"strings"
	"testing"

	bootstrapapi "github.com/Mujhtech/idenqa/internal/bootstrap/api"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func TestCommandSurface(t *testing.T) {
	t.Parallel()

	info := buildinfo.Info{Version: "v0.1.0", Commit: "abc1234", Date: "2026-08-27"}
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{name: "help", args: []string{"--help"}, wantCode: 0, wantStdout: "Run the Idenqa Core HTTP API"},
		{name: "version command", args: []string{"version"}, wantCode: 0, wantStdout: "idenqa v0.1.0"},
		{name: "version flag", args: []string{"--version"}, wantCode: 0, wantStdout: "idenqa v0.1.0"},
		{name: "unknown argument", args: []string{"unexpected"}, wantCode: 2, wantStderr: "unknown command"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := bootstrapapi.Run(test.args, &stdout, &stderr, info)
			if code != test.wantCode {
				t.Errorf("exit code = %d, want %d", code, test.wantCode)
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

func TestCommandDoesNotDuplicateReportedStartupErrors(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := bootstrapapi.Run(
		[]string{"--env-file", "/synthetic/missing.env"},
		&stdout,
		&stderr,
		buildinfo.Info{},
	)

	if code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), `"msg":"api configuration failed"`) {
		t.Errorf("stderr = %q, want structured configuration failure", stderr.String())
	}
	if strings.Contains(stderr.String(), "error: read dotenv file") {
		t.Errorf("stderr duplicated the reported error: %q", stderr.String())
	}
}
