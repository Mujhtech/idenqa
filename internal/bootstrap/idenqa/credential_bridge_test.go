package idenqa

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialBridgeSocketIsPrivateAndOwnedCleanupRemovesIt(t *testing.T) {
	t.Parallel()

	directory, err := os.MkdirTemp("", "idq-bridge-")
	if err != nil {
		t.Fatalf("MkdirTemp() error = %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	path := filepath.Join(directory, "bridge.sock")
	listener, cleanup, err := listenCredentialBridge(path)
	if err != nil {
		t.Fatalf("listenCredentialBridge() error = %v", err)
	}
	if listener.Addr().Network() != "unix" {
		t.Fatalf("listener network = %q, want unix", listener.Addr().Network())
	}
	information, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat(socket) error = %v", err)
	}
	if information.Mode().Perm() != 0o600 || information.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket mode = %v, want private Unix socket", information.Mode())
	}

	cleanup()
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("socket remains after cleanup: %v", err)
	}
}
