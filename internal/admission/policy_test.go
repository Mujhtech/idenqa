package admission

import (
	"bytes"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"

	admissionv1 "github.com/Mujhtech/idenqa/contracts/admission/v1"
)

func TestPolicyRequiresExactSignedCapabilityWithoutDefaultGrace(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{3}, ed25519.SeedSize))
	operation, err := admissionv1.VerificationOperation("profile", 1, "policy")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := NewPolicy(Config{DeploymentID: "deployment", Region: "eu-west-1", MaximumLifetime: time.Hour, Issuers: map[string]ed25519.PublicKey{"issuer": key.Public().(ed25519.PublicKey)}})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		change func(*admissionv1.Snapshot)
		at     time.Time
		want   error
	}{
		{"active", func(*admissionv1.Snapshot) {}, now, nil},
		{"explicit grace", func(s *admissionv1.Snapshot) { s.Status = "grace" }, now, nil},
		{"expired active", func(*admissionv1.Snapshot) {}, now.Add(time.Hour), ErrUnavailable},
		{"expired grace", func(s *admissionv1.Snapshot) { s.Status = "grace" }, now.Add(time.Hour), ErrUnavailable},
		{"scheduled", func(s *admissionv1.Snapshot) { s.ValidFrom = s.ValidFrom.Add(time.Minute) }, now, ErrUnavailable},
		{"suspended", func(s *admissionv1.Snapshot) { s.Status = "suspended" }, now, ErrEntitlement},
		{"revoked", func(s *admissionv1.Snapshot) { s.Status = "revoked" }, now, ErrEntitlement},
		{"untrusted issuer", func(s *admissionv1.Snapshot) { s.Issuer = "other" }, now, ErrInvalid},
		{"wrong region", func(s *admissionv1.Snapshot) { s.Scope.Region = "other" }, now, ErrInvalid},
		{"wrong deployment", func(s *admissionv1.Snapshot) { s.Scope.DeploymentID = "other" }, now, ErrInvalid},
		{"excessive lifetime", func(s *admissionv1.Snapshot) { s.ValidUntil = s.ValidUntil.Add(time.Second) }, now, ErrInvalid},
		{"future issuance", func(s *admissionv1.Snapshot) { s.IssuedAt = s.IssuedAt.Add(time.Second); s.ValidFrom = s.IssuedAt }, now, ErrInvalid},
		{"capability absent", func(s *admissionv1.Snapshot) { s.Capabilities = map[string]int64{"different": 1} }, now, ErrEntitlement},
		{"capability version mismatch", func(s *admissionv1.Snapshot) { s.Capabilities["workflow"] = 2 }, now, ErrEntitlement},
		{"another profile revision", func(s *admissionv1.Snapshot) { s.Rules[0].Operation = "verifications.create/profile/2/policy" }, now, ErrEntitlement},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := admissionv1.Snapshot{Schema: admissionv1.Schema, ID: "snapshot", Scope: admissionv1.Scope{TenantID: "tenant", DeploymentID: "deployment", Region: "eu-west-1"}, Revision: 1, Issuer: "issuer", IssuedAt: now, ValidFrom: now, ValidUntil: now.Add(time.Hour), Status: "active", Capabilities: map[string]int64{"workflow": 1}, Rules: []admissionv1.Rule{{Operation: operation, Capabilities: map[string]int64{"workflow": 1}}}}
			test.change(&s)
			s.Proof = ed25519.Sign(key, s.SigningBytes())
			_, err := policy.Decide(s, operation, test.at)
			if test.want == nil {
				if err != nil {
					t.Fatal(err)
				}
			} else if !errors.Is(err, test.want) {
				t.Fatalf("decision %v want %v", err, test.want)
			}
		})
	}
}
