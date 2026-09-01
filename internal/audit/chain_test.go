package audit

import (
	"crypto/ed25519"
	"crypto/rand"
	"strings"
	"testing"
	"time"
)

func TestVerifyDetectsTamperingAndWrongKey(t *testing.T) {
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	first, err := Append(nil, "evt_one", "verification.created.v1", "ver_one", "key_one", strings.Repeat("1", 64), now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Append(&first, "evt_two", "verification.decision.v1", "ver_one", "worker_one", strings.Repeat("2", 64), now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	publicKey, privateKey, _ := ed25519.GenerateKey(rand.Reader)
	checkpoint, err := SignCheckpoint("ten_one", "audit_key_one", second, now.Add(2*time.Second), privateKey)
	if err != nil {
		t.Fatal(err)
	}
	export := Export{SchemaVersion: 1, TenantID: "ten_one", Records: []Record{first, second}, Checkpoints: []Checkpoint{checkpoint}}
	if _, err := Verify(export, map[string]ed25519.PublicKey{"audit_key_one": publicKey}); err != nil {
		t.Fatal(err)
	}
	mutated := export
	mutated.Records = append([]Record(nil), export.Records...)
	mutated.Records[0].ActorID = "attacker"
	if _, err := Verify(mutated, map[string]ed25519.PublicKey{"audit_key_one": publicKey}); err == nil {
		t.Fatal("mutation verified")
	}
	wrong, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := Verify(export, map[string]ed25519.PublicKey{"audit_key_one": wrong}); err == nil {
		t.Fatal("wrong key verified")
	}
	deleted := export
	deleted.Records = []Record{second}
	if _, err := Verify(deleted, map[string]ed25519.PublicKey{"audit_key_one": publicKey}); err == nil {
		t.Fatal("deleted record verified")
	}
	reordered := export
	reordered.Records = []Record{second, first}
	if _, err := Verify(reordered, map[string]ed25519.PublicKey{"audit_key_one": publicKey}); err == nil {
		t.Fatal("reordered records verified")
	}
	broken := export
	broken.Checkpoints = append([]Checkpoint(nil), export.Checkpoints...)
	broken.Checkpoints[0].Signature = strings.Repeat("0", ed25519.SignatureSize*2)
	if _, err := Verify(broken, map[string]ed25519.PublicKey{"audit_key_one": publicKey}); err == nil {
		t.Fatal("broken checkpoint verified")
	}
}
