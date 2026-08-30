package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
)

func TestRunProvidesSharedCommandSurface(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		contains string
	}{
		{name: "help", args: []string{"--help"}, contains: "Run the S3-backed Idenqa Core HTTP API"},
		{name: "version", args: []string{"version"}, contains: "idenqa test-version"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := Run(test.args, &stdout, &stderr, buildinfo.Info{
				Version: "test-version", Commit: "test-commit", Date: "test-date",
			})
			if code != 0 {
				t.Fatalf("Run() code = %d, stderr = %q", code, stderr.String())
			}
			if !strings.Contains(stdout.String(), test.contains) {
				t.Errorf("stdout = %q, want it to contain %q", stdout.String(), test.contains)
			}
		})
	}
}

func TestEvidenceLifecycleClosesMountedKeyring(t *testing.T) {
	closer := &recordingCloser{err: errors.New("close failed")}
	lifecycle := &evidenceLifecycle{keys: closer}

	err := lifecycle.Shutdown(context.Background())
	if !closer.closed || !errors.Is(err, closer.err) {
		t.Fatalf("Shutdown() closed = %t, error = %v", closer.closed, err)
	}
}

type recordingCloser struct {
	closed bool
	err    error
}

func (closer *recordingCloser) Close() error {
	closer.closed = true
	return closer.err
}
