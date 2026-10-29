package v1

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestPublicSnapshotSigningFixture(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile("testdata/snapshot.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		PublicKey     string   `json:"publicKey"`
		SigningSHA256 string   `json:"signingSha256"`
		Snapshot      Snapshot `json:"snapshot"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	key, err := base64.StdEncoding.Strict().DecodeString(fixture.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.Snapshot.Verify(key); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(fixture.Snapshot.SigningBytes())
	if hex.EncodeToString(sum[:]) != fixture.SigningSHA256 {
		t.Fatal("public signing bytes changed without protocol conformance update")
	}
}

func TestPublicDependentResourceFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/resource.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Kind                                    string
		Selectors                               []string
		Workflow, Operation, DependentOperation string
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	resource, err := ResourceOperation(fixture.Kind, fixture.Selectors...)
	if err != nil || resource != fixture.Operation {
		t.Fatalf("resource selection contract: %s: %v", resource, err)
	}
	dependent, err := DependentOperation(fixture.Workflow, resource)
	if err != nil || dependent != fixture.DependentOperation {
		t.Fatalf("dependent workflow contract: %s: %v", dependent, err)
	}
	other, err := DependentOperation("verifications.create/other/3/policy", resource)
	if err != nil || other == dependent {
		t.Fatal("another workflow borrowed the dependent grant")
	}
}
