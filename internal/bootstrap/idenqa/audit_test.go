package idenqa

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/audit"
)

func TestAuditVerifyCommandWorksOfflineAndRejectsTrailingData(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	record, err := audit.Append(nil, "evt_one", "verification.decision.v1", "dec_one", "worker.policy", strings.Repeat("a", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	checkpoint, err := audit.SignCheckpoint("ten_one", "audit_key_v1", record, now.Add(time.Second), private)
	if err != nil {
		t.Fatal(err)
	}
	exported, _ := json.Marshal(audit.Export{SchemaVersion: 1, TenantID: "ten_one", Records: []audit.Record{record}, Checkpoints: []audit.Checkpoint{checkpoint}})
	keys, _ := json.Marshal(map[string]string{"audit_key_v1": hex.EncodeToString(public)})
	directory := t.TempDir()
	exportPath, keyPath := filepath.Join(directory, "audit.json"), filepath.Join(directory, "keys.json")
	if err := os.WriteFile(exportPath, exported, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, keys, 0o600); err != nil {
		t.Fatal(err)
	}
	command := newAuditCommand()
	output := &bytes.Buffer{}
	command.SetOut(output)
	command.SetErr(output)
	command.SetArgs([]string{"verify", "--export-file", exportPath, "--keys-file", keyPath})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), `"records":1`) {
		t.Fatalf("output=%q", output.String())
	}
	if err := os.WriteFile(exportPath, append(exported, []byte(` {}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	command = newAuditCommand()
	command.SetArgs([]string{"verify", "--export-file", exportPath, "--keys-file", keyPath})
	if err := command.Execute(); err == nil {
		t.Fatal("trailing export data verified")
	}
}
