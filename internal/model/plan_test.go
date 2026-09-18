package model_test

import (
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/model"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

func TestMatchingPlanRequiresDocumentAndSelfieContract(t *testing.T) {
	t.Parallel()
	request := runtimeRequest(t)
	binding := model.Binding{TenantID: request.TenantID, PolicyID: "pol_01K4AR9V8FQ2G7ZXCPNM5T6JWH", ProfileDigest: "sha256:" + strings.Repeat("a", 64), Requirement: "selfie", DocumentRequirement: "document", Region: "tenant.region.ng", Purpose: "idenqa.purpose.identity_verification", Recipient: "tenant.recipient.primary", Configuration: request.Configuration}
	manifest := modelv1.Manifest{Provenance: request.Provenance, Restrictions: request.Restrictions, Capabilities: []modelv1.Capability{{Evaluation: "idenqa.check.face_match_1to1", AcceptedEvidence: []string{"idenqa.evidence.document_image", "idenqa.evidence.selfie_image"}, OutputSignals: []string{"idenqa.signal.face_match_1to1"}}}}
	manifest.Restrictions.MaximumGrants = 2
	plan, err := model.NewPlan(binding, manifest)
	if err != nil || plan.OutputDestination() != "model.face_match_1to1" {
		t.Fatal("pair route unavailable", err)
	}
	binding.DocumentRequirement = "selfie"
	if _, err := model.NewPlan(binding, manifest); err == nil {
		t.Fatal("same requirement accepted for both roles")
	}
	binding.DocumentRequirement = "document"
	manifest.Restrictions.MaximumGrants = 1
	if _, err := model.NewPlan(binding, manifest); err == nil {
		t.Fatal("one-grant pair route accepted")
	}
}
