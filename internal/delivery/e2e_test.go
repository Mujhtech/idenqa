package delivery_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"
	"time"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
	policycel "github.com/Mujhtech/idenqa/internal/policy/cel"
)

func TestSyntheticPolicyDecisionProducesVerifiableSignedWebhook(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	document := policyv1.Document{SchemaMajor: 1, PolicyID: "pol_01K3P4NQF00000000000000001", Revision: 1, VerifiedAssurance: "identity.basic", Rules: []policyv1.Rule{{Name: "verified", When: `facts["document.authenticity"] == "satisfied"`, Result: policyv1.Result{State: policyv1.RequirementSatisfied, Directive: policyv1.DirectiveCompleteVerified, Priority: 1, ContributingFacts: []string{"document.authenticity"}, ReasonCodes: []string{"document_authentic"}}}}}
	canonical, err := policyv1.Canonical(document)
	if err != nil {
		t.Fatal(err)
	}
	tenantID, _ := id.ParseTenant("ten_01K3P4NQF00000000000000001")
	verificationID, _ := id.ParseVerification("ver_01K3P4NQF00000000000000002")
	authorityID, _ := id.ParseAuthority("aut_01K3P4NQF00000000000000003")
	acknowledgementID, _ := id.ParseAcknowledgement("ack_01K3P4NQF00000000000000004")
	factKey, _ := policy.NewFactKey("document.authenticity")
	input := policy.SimulationInput{CanonicalPolicy: canonical, TenantID: tenantID, VerificationID: verificationID, AuthorityID: authorityID, AcknowledgementID: acknowledgementID, Region: "tenant_home", EvaluatedAt: now, Facts: []policy.Fact{{Key: factKey, State: policy.RequirementSatisfied, Source: policy.FactSource{Kind: policy.FactSourceProcessingAuthority, Authority: &policy.AuthoritySource{AuthorityID: authorityID}}, ObservedAt: now}}}
	simulator, err := policy.NewSimulator(policycel.Compiler{})
	if err != nil {
		t.Fatal(err)
	}
	first, err := simulator.Run(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := simulator.Run(context.Background(), input)
	if err != nil || first.Evaluation().Digest() != second.Evaluation().Digest() || !first.Evaluation().AuthorisesCompletion() {
		t.Fatalf("decision was not reproducible: %v", err)
	}
	body, err := json.Marshal(struct {
		Type           string `json:"type"`
		DecisionDigest string `json:"decision_digest"`
	}{Type: "verification.decision.v1", DecisionDigest: first.Evaluation().Digest()})
	if err != nil {
		t.Fatal(err)
	}
	eventID, _ := id.ParseEvent("evt_01K3P4NQF00000000000000005")
	secret := []byte("01234567890123456789012345678901")
	signature, err := delivery.Sign(secret, eventID, now, body)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(signature.Timestamp + "\n" + signature.EventID + "\n"))
	_, _ = mac.Write(body)
	if signature.Value != "v1="+hex.EncodeToString(mac.Sum(nil)) {
		t.Fatal("signed webhook did not bind the reproducible decision body")
	}
}
