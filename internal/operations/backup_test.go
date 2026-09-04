package operations_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/operations"
)

func TestBackupManifestRestoreVerificationAndFailureInjection(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	artifacts := make([]operations.Artifact, 0, 4)
	for _, item := range []struct{ kind, path, body string }{{"postgres", "database.dump", "postgres"}, {"evidence", "objects/inventory.json", "objects"}, {"keyring", "secrets/keyring.json", "keys"}, {"audit", "audit/export.json", "audit"}} {
		path := filepath.Join(root, item.path)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(item.body), 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(item.body))
		artifacts = append(artifacts, operations.Artifact{Kind: item.kind, Path: item.path, SHA256: hex.EncodeToString(digest[:]), Bytes: int64(len(item.body))})
	}
	manifest := operations.BackupManifest{SchemaVersion: 1, Region: "ng-1", DatabaseSchema: 26, CreatedAt: now, ExpiresAt: now.Add(35 * 24 * time.Hour), Artifacts: artifacts}
	encoded, _ := json.Marshal(manifest)
	restored, err := operations.DecodeManifest(encoded)
	if err != nil {
		t.Fatal(err)
	}
	report, err := operations.VerifyDirectory(context.Background(), root, restored, now.Add(time.Hour))
	if err != nil || report.Artifacts != 4 {
		t.Fatalf("VerifyDirectory() = %+v, %v", report, err)
	}
	if err := os.WriteFile(filepath.Join(root, "database.dump"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.VerifyDirectory(context.Background(), root, restored, now.Add(time.Hour)); !errors.Is(err, operations.ErrIntegrity) {
		t.Fatalf("tamper error = %v", err)
	}
	manifest.Artifacts[0].Path = "../escape"
	if manifest.Validate() == nil {
		t.Fatal("traversal manifest accepted")
	}
}
