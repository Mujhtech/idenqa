package idenqa

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/operations"
)

func TestRecoveryVerifyCommand(t *testing.T) {
	root := t.TempDir()
	artifacts := make([]operations.Artifact, 0, 4)
	for _, kind := range []string{"postgres", "evidence", "keyring", "audit"} {
		path := kind + ".data"
		body := []byte(kind)
		if err := os.WriteFile(filepath.Join(root, path), body, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(body)
		artifacts = append(artifacts, operations.Artifact{Kind: kind, Path: path, SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(body))})
	}
	now := time.Now().UTC()
	manifest := operations.BackupManifest{SchemaVersion: 1, Region: "ng-1", DatabaseSchema: 26, CreatedAt: now, ExpiresAt: now.Add(35 * 24 * time.Hour), Artifacts: artifacts}
	encoded, _ := json.Marshal(manifest)
	manifestPath := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(manifestPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr := new(bytes.Buffer), new(bytes.Buffer)
	exit := Run([]string{"recovery", "verify", "--manifest-file", manifestPath, "--backup-root", root}, stdout, stderr, buildinfo.Info{})
	if exit != 0 || !strings.Contains(stdout.String(), `"artifacts":4`) || stderr.Len() != 0 {
		t.Fatalf("Run() exit=%d stdout=%q stderr=%q", exit, stdout, stderr)
	}
}
