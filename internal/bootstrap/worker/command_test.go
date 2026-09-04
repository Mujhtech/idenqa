package worker_test

import (
	"bytes"
	"strings"
	"testing"

	bootstrapworker "github.com/Mujhtech/idenqa/internal/bootstrap/worker"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func TestRunVersionAndConfigurationFailure(t *testing.T) {
	t.Parallel()
	info := buildinfo.Info{Version: "v0.1.0"}
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{name: "version", args: []string{"version"}, code: 0, want: "idenqa v0.1.0"},
		{name: "missing configuration", args: []string{"--env-file", "/definitely/missing/idenqa.env"}, code: 1, want: "worker configuration failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := bootstrapworker.Run(test.args, &stdout, &stderr, info)
			if code != test.code || !strings.Contains(stdout.String()+stderr.String(), test.want) {
				t.Fatalf("Run() = code %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
		})
	}
}
