//go:build integration

package integration_test

import (
	"errors"
	"net/http"
	"os"
	"os/exec"
	"testing"

	sdk "github.com/Mujhtech/idenqa/sdk/go"
)

// This proof runs inside the existing isolated, restricted-role public capture
// journey. SDK calls therefore exercise actual HTTP routes and PostgreSQL, not mocks.
func assertPublicSDKAdministration(t *testing.T, transport *http.Client, base, credential, evidenceID, consentID, verificationID string) {
	t.Helper()
	client, err := sdk.NewClient(base, credential, transport)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	asset, err := client.GetEvidence(ctx, evidenceID)
	if err != nil {
		t.Fatalf("SDK evidence: %v", err)
	}
	if asset.Data.ID != evidenceID || asset.Data.VerificationID != verificationID || asset.RequestID == "" {
		t.Fatal("evidence projection lost identity")
	}
	history, err := client.ListEvidenceLifecycle(ctx, evidenceID, sdk.HistoryOptions{Limit: 10})
	if err != nil || len(history.Data.Data) == 0 {
		t.Fatalf("SDK evidence lifecycle: %v", err)
	}
	consent, err := client.GetConsentReceipt(ctx, consentID)
	if err != nil || consent.Data.Action != "consent" {
		t.Fatalf("SDK consent: %v", err)
	}
	input := sdk.EvidenceAccessGrantCreate{
		CheckReference: "sdk.check", RunnerIdentity: "sdk.runner", WorkloadVersion: "v1",
		Purpose: "idenqa.purpose.identity_verification", PermittedVariants: []string{"evidence.variant.original"},
		RecipientReference: "tenant.recipient.primary", OutputDestination: "workflow.result.normalized",
		MaximumUses: 1, TTLSeconds: 60, Reason: "sdk proof",
	}
	key := sdk.MutationOptions{IdempotencyKey: "sdk-grant-create"}
	grant, err := client.CreateEvidenceAccessGrant(ctx, evidenceID, input, key)
	if err != nil {
		t.Fatalf("SDK grant create: %v", err)
	}
	replay, err := client.CreateEvidenceAccessGrant(ctx, evidenceID, input, key)
	if err != nil || replay.Data.ID != grant.Data.ID {
		t.Fatalf("SDK grant replay: %v", err)
	}
	input.Reason = "different meaning"
	_, err = client.CreateEvidenceAccessGrant(ctx, evidenceID, input, key)
	assertSDKStatus(t, err, 409)
	if _, err = client.GetEvidenceAccessGrant(ctx, grant.Data.ID); err != nil {
		t.Fatal(err)
	}
	revoked, err := client.RevokeEvidenceAccessGrant(ctx, grant.Data.ID, sdk.EvidenceAccessGrantRevoke{Reason: "finished"}, sdk.MutationOptions{IdempotencyKey: "sdk-grant-revoke"})
	if err != nil || revoked.Data.RevokedAt == nil {
		t.Fatalf("SDK grant revoke: %v", err)
	}
	revokedAgain, err := client.RevokeEvidenceAccessGrant(ctx, grant.Data.ID, sdk.EvidenceAccessGrantRevoke{Reason: "finished"}, sdk.MutationOptions{IdempotencyKey: "sdk-grant-revoke"})
	if err != nil || !revokedAgain.Data.RevokedAt.Equal(*revoked.Data.RevokedAt) {
		t.Fatalf("SDK grant revoke replay: %v", err)
	}

	assessment, err := client.CreateProposalImpactAssessment(ctx, sdk.ProposalImpactAssessmentCreate{Kind: "review.copilot.summarize", Assessment: "Synthetic SDK proof; human review required", RiskLevel: "high"})
	if err != nil {
		t.Fatalf("SDK impact create: %v", err)
	}
	found, err := client.GetProposalImpactAssessment(ctx, assessment.Data.ID)
	if err != nil || found.Data.Assessment != assessment.Data.Assessment {
		t.Fatalf("SDK impact read: %v", err)
	}
	impacts, err := client.ListProposalImpactAssessments(ctx, sdk.ImpactAssessmentListOptions{Limit: 1})
	if err != nil || len(impacts.Data.Data) != 1 {
		t.Fatalf("SDK impact list: %v", err)
	}

	processor := sdk.PrivacyProcessorPut{Name: "SDK fixture", Role: "processor", Purpose: "identity.verification", DataClasses: []sdk.PrivacyProcessorPutDataClasses{"raw_evidence"}, Regions: []string{"tenant.region.ng"}, TransferMechanism: "tenant.contract", ExpectedVersion: 0}
	created, err := client.CreatePrivacyProcessor(ctx, processor)
	if err != nil {
		t.Fatalf("SDK processor create: %v", err)
	}
	processor.ExpectedVersion = created.Data.Version
	processor.Name = "Updated SDK fixture"
	updated, err := client.UpdatePrivacyProcessor(ctx, created.Data.ID, processor)
	if err != nil || updated.Data.Version != 2 {
		t.Fatalf("SDK processor update: %v", err)
	}
	_, err = client.UpdatePrivacyProcessor(ctx, created.Data.ID, processor)
	assertSDKStatus(t, err, 409)
	if _, err = client.GetPrivacyProcessor(ctx, created.Data.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = client.ListPrivacyProcessors(ctx, sdk.PaginationOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}

	subjectID := asset.Data.SubjectID
	request, err := client.CreatePrivacyRequest(ctx, sdk.PrivacyRequestCreate{Type: "restriction", Region: "ng", SubjectID: &subjectID})
	if err != nil {
		t.Fatalf("SDK privacy request create: %v", err)
	}
	approved, err := client.ApprovePrivacyRequest(ctx, request.Data.ID, sdk.PrivacyRequestDecision{ExpectedVersion: request.Data.Version, ReasonCode: "restriction_approved"})
	if err != nil {
		t.Fatalf("SDK privacy approve: %v", err)
	}
	executed, err := client.ExecutePrivacyRequest(ctx, request.Data.ID, sdk.PrivacyRequestExpectedVersion{ExpectedVersion: approved.Data.Version})
	if err != nil || executed.Data.State != "completed" {
		t.Fatalf("SDK privacy execute: %v", err)
	}
	restrictions, err := client.ListPrivacyRestrictions(ctx, sdk.PrivacyRestrictionListOptions{SubjectID: subjectID})
	if err != nil || len(restrictions.Data.Data) != 1 {
		t.Fatalf("SDK privacy restrictions: %v", err)
	}
	restriction := restrictions.Data.Data[0]
	if _, err = client.LiftPrivacyRestriction(ctx, restriction.ID, sdk.PrivacyRestrictionLift{ExpectedVersion: restriction.Version, ReasonCode: "lifted_by_tenant"}); err != nil {
		t.Fatalf("SDK lift restriction: %v", err)
	}
	if _, err = client.GetPrivacyRequest(ctx, request.Data.ID); err != nil {
		t.Fatalf("SDK get privacy request: %v", err)
	}
	if _, err = client.ListPrivacyRequests(ctx, sdk.PrivacyRequestListOptions{SubjectID: subjectID}); err != nil {
		t.Fatalf("SDK list privacy requests: %v", err)
	}
	for _, action := range []string{"deny", "withdraw"} {
		next, err := client.CreatePrivacyRequest(ctx, sdk.PrivacyRequestCreate{Type: "access", Region: "ng", SubjectID: &subjectID})
		if err != nil {
			t.Fatal(err)
		}
		if action == "deny" {
			_, err = client.DenyPrivacyRequest(ctx, next.Data.ID, sdk.PrivacyRequestDecision{ExpectedVersion: next.Data.Version, ReasonCode: "insufficient_proof"})
		} else {
			_, err = client.WithdrawPrivacyRequest(ctx, next.Data.ID, sdk.PrivacyRequestExpectedVersion{ExpectedVersion: next.Data.Version})
		}
		if err != nil {
			t.Fatalf("SDK privacy %s: %v", action, err)
		}
	}
	if _, err = client.ListPrivacyDisclosures(ctx, sdk.PrivacyDisclosureListOptions{RequestID: request.Data.ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = client.CreatePrivacyDisclosure(ctx, sdk.PrivacyDisclosureCreate{RequestID: request.Data.ID, Recipient: "tenant.backend", Purpose: "sdk.proof", DataClass: "restriction", LegalBasis: "tenant.contract", Region: "ng", Reference: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}); err != nil {
		t.Fatalf("SDK create disclosure: %v", err)
	}
	if _, err = client.ListDeletions(ctx, sdk.DeletionListOptions{AggregateID: verificationID}); err != nil {
		t.Fatalf("SDK list deletions: %v", err)
	}
	if _, err = client.GetRetentionResolution(ctx, sdk.RetentionOptions{AggregateID: verificationID}); err != nil {
		t.Fatalf("SDK retention: %v", err)
	}

	// Exercise the published TypeScript bundle against these same durable resources.
	build := exec.CommandContext(ctx, "pnpm", "--filter", "@idenqa/sdk", "build")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build TypeScript SDK proof: %v\n%s", err, output)
	}
	command := exec.CommandContext(ctx, "node", "../conformance/sdk-administration.mjs")
	command.Env = append(os.Environ(), "IDENQA_SDK_BASE="+base, "IDENQA_SDK_KEY="+credential, "IDENQA_SDK_EVIDENCE="+evidenceID, "IDENQA_SDK_CONSENT="+consentID, "IDENQA_SDK_VERIFICATION="+verificationID)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("TypeScript public resource proof: %v\n%s", err, output)
	}
}

func assertPublicSDKDecisionAndConsent(t *testing.T, transport *http.Client, base, credential, verificationID, decisionID, consentID string) {
	t.Helper()
	client, err := sdk.NewClient(base, credential, transport)
	if err != nil {
		t.Fatal(err)
	}
	history, err := client.ListVerificationDecisions(t.Context(), verificationID, sdk.DecisionHistoryOptions{Limit: 1})
	if err != nil || len(history.Data.Data) != 1 || history.Data.Data[0].DecisionID != decisionID {
		t.Fatalf("SDK decision history: %v", err)
	}
	next, err := client.ListVerificationDecisions(t.Context(), verificationID, sdk.DecisionHistoryOptions{Before: decisionID, Limit: 1})
	if err != nil || len(next.Data.Data) != 0 {
		t.Fatalf("SDK decision history cursor: %v", err)
	}
	// Full tenant scopes do not substitute for independent certified operator authority.
	_, err = client.CreateVerificationReconsideration(t.Context(), verificationID, sdk.VerificationReconsiderationCreate{DecisionID: decisionID}, sdk.MutationOptions{IdempotencyKey: "sdk-reconsider"})
	assertSDKStatus(t, err, http.StatusForbidden)
	withdrawn, err := client.RevokeConsentReceipt(t.Context(), consentID, sdk.ConsentReceiptRevoke{Reason: "subject withdrawal"}, sdk.MutationOptions{IdempotencyKey: "sdk-consent-withdraw"})
	if err != nil || withdrawn.Data.Action != "refuse" || withdrawn.Data.ID == consentID {
		t.Fatalf("SDK consent withdrawal: %v", err)
	}
	replay, err := client.RevokeConsentReceipt(t.Context(), consentID, sdk.ConsentReceiptRevoke{Reason: "subject withdrawal"}, sdk.MutationOptions{IdempotencyKey: "sdk-consent-withdraw"})
	if err != nil || replay.Data.ID != withdrawn.Data.ID {
		t.Fatalf("SDK consent replay: %v", err)
	}
	original, err := client.GetConsentReceipt(t.Context(), consentID)
	if err != nil || original.Data.Action != "consent" {
		t.Fatalf("SDK changed original consent: %v", err)
	}
}

func assertSDKStatus(t *testing.T, err error, status int) {
	t.Helper()
	var problem *sdk.APIError
	if !errors.As(err, &problem) || problem.StatusCode != status {
		t.Fatalf("SDK error = %v; want status %d", err, status)
	}
}
