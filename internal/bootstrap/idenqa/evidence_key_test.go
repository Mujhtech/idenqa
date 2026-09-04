package idenqa

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/cli"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
)

func TestEvidenceKeyInitCreatesOwnerOnlyKeyringWithoutReplacement(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "evidence-keyring.json")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := cli.ExecuteArgs(
		t.Context(),
		newRootCommand(buildinfo.Info{}),
		[]string{"evidence-key", "init", "--keyring-file", path},
		&stdout,
		&stderr,
	)
	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got, want := stdout.String(), "evidence_key_initialized active_key_version=v1\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat initialized keyring: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("keyring mode = %o, want 600", got)
	}
	keyring, err := localkms.Open(path)
	if err != nil {
		t.Fatalf("open initialized keyring: %v", err)
	}
	if err := keyring.Close(); err != nil {
		t.Fatalf("close initialized keyring: %v", err)
	}
	original, err := os.ReadFile(path) //nolint:gosec // test-owned temporary secret file
	if err != nil {
		t.Fatalf("read initialized keyring: %v", err)
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.ExecuteArgs(
		t.Context(),
		newRootCommand(buildinfo.Info{}),
		[]string{"evidence-key", "init", "--keyring-file", path},
		&stdout,
		&stderr,
	)
	if code != 1 {
		t.Fatalf("duplicate exit code = %d, want 1", code)
	}
	if stdout.Len() != 0 {
		t.Fatalf("duplicate stdout = %q, want empty", stdout.String())
	}
	if !strings.Contains(stderr.String(), "initialize evidence keyring: local kms: invalid keyring") {
		t.Fatalf("duplicate stderr = %q, want redacted initialization failure", stderr.String())
	}
	if strings.Contains(stderr.String(), path) {
		t.Fatalf("duplicate stderr disclosed keyring path: %q", stderr.String())
	}
	after, err := os.ReadFile(path) //nolint:gosec // test-owned temporary secret file
	if err != nil {
		t.Fatalf("read keyring after duplicate: %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatal("duplicate initialization replaced the existing keyring")
	}
}

func TestEvidenceKeyInitHonoursCancellationBeforeCreatingKeyring(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "evidence-keyring.json")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	command := newRootCommand(buildinfo.Info{})
	command.SetArgs([]string{"evidence-key", "init", "--keyring-file", path})
	command.SetOut(new(bytes.Buffer))
	command.SetErr(new(bytes.Buffer))
	err := command.ExecuteContext(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteContext() error = %v, want context cancellation", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(cancelled keyring) error = %v, want not exist", err)
	}
}
