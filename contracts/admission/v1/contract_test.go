package v1

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"testing"
	"time"
)

func snapshot(t *testing.T) Snapshot {
	t.Helper()
	operation, err := VerificationOperation("profile", 1, "policy")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	return Snapshot{Schema: Schema, ID: "snapshot", Scope: Scope{TenantID: "tenant", DeploymentID: "deployment", Region: "eu-west-1"}, Revision: 1, Issuer: "issuer", IssuedAt: now, ValidFrom: now, ValidUntil: now.Add(time.Hour), Status: "active", Capabilities: map[string]int64{"workflow": 1}, Limits: []Limit{{Kind: "concurrency", Key: "workflow", Maximum: 1}}, Rules: []Rule{{Operation: operation, Capabilities: map[string]int64{"workflow": 1}, Budgets: []Budget{{Kind: "concurrency", Key: "workflow", Quantity: 1, Lifetime: "terminal"}}}}}
}

func TestSnapshotSignatureBindsScopeAndOperationMeaning(t *testing.T) {
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{1}, ed25519.SeedSize))
	s := snapshot(t)
	s.Proof = ed25519.Sign(key, s.SigningBytes())
	if err := s.Verify(key.Public().(ed25519.PublicKey)); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*Snapshot)
	}{
		{"tenant", func(s *Snapshot) { s.Scope.TenantID = "other" }},
		{"deployment", func(s *Snapshot) { s.Scope.DeploymentID = "other" }},
		{"region", func(s *Snapshot) { s.Scope.Region = "other" }},
		{"capability version", func(s *Snapshot) { s.Capabilities["workflow"] = 2 }},
		{"quota maximum", func(s *Snapshot) { s.Limits[0].Maximum = 2 }},
		{"reservation amount", func(s *Snapshot) { s.Rules[0].Budgets[0].Quantity = 2 }},
		{"workflow", func(s *Snapshot) { s.Rules[0].Operation = "other" }},
		{"expiry", func(s *Snapshot) { s.ValidUntil = s.ValidUntil.Add(time.Hour) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			var changed Snapshot
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			test.change(&changed)
			if changed.Verify(key.Public().(ed25519.PublicKey)) == nil {
				t.Fatal("changed meaning retained authority")
			}
		})
	}
}

func TestSnapshotRejectsAmbiguousOrUnboundedPolicy(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Snapshot)
	}{
		{"empty scope", func(s *Snapshot) { s.Scope.TenantID = "" }},
		{"duplicate quota", func(s *Snapshot) { s.Limits = append(s.Limits, s.Limits[0]) }},
		{"duplicate workflow", func(s *Snapshot) { s.Rules = append(s.Rules, s.Rules[0]) }},
		{"duplicate budget", func(s *Snapshot) { s.Rules[0].Budgets = append(s.Rules[0].Budgets, s.Rules[0].Budgets[0]) }},
		{"missing quota", func(s *Snapshot) { s.Rules[0].Budgets[0].Key = "unknown" }},
		{"implicit lifetime", func(s *Snapshot) { s.Rules[0].Budgets[0].Lifetime = "" }},
		{"invalid count window", func(s *Snapshot) { s.Limits[0].Window = time.Minute }},
		{"negative maximum", func(s *Snapshot) { s.Limits[0].Maximum = -1 }},
		{"negative quantity", func(s *Snapshot) { s.Rules[0].Budgets[0].Quantity = -1 }},
		{"empty lifetime", func(s *Snapshot) { s.ValidUntil = s.ValidFrom }},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := snapshot(t)
			test.change(&s)
			if s.Validate() == nil {
				t.Fatal("invalid policy accepted")
			}
		})
	}
	s := snapshot(t)
	s.Limits[0].Maximum = 0
	if s.Validate() != nil {
		t.Fatal("an explicit zero ceiling must remain a valid deny policy")
	}
}

func TestVerificationOperationPinsPublishedWorkflow(t *testing.T) {
	one, err := VerificationOperation("profile", 1, "policy")
	if err != nil {
		t.Fatal(err)
	}
	two, err := VerificationOperation("profile", 2, "policy")
	if err != nil || one == two {
		t.Fatal("profile revision did not change operation")
	}
	for _, value := range []string{"", "*", "profile/other", "profile?token=bad"} {
		if _, err := VerificationOperation(value, 1, "policy"); err == nil {
			t.Fatal("unbounded operation context")
		}
	}
}
