package evidence_test

import (
	"context"
	"testing"

	"github.com/Mujhtech/idenqa/internal/evidence"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

type acceptedUploadsStub struct{ uploads []evidence.Upload }

func (stub acceptedUploadsStub) ListAcceptedUploads(
	_ context.Context,
	_ tenant.Scope,
	_ id.CaptureToken,
	_ id.Verification,
	_ int32,
) ([]evidence.Upload, error) {
	return stub.uploads, nil
}

func TestProgressReaderReturnsOnlyAcceptedCaptureBindings(t *testing.T) {
	t.Parallel()

	fixture := newUploadFixture(t)
	issued := fixture.upload(t)
	claimed, err := issued.ClaimAttempt(issued.Version(), fixture.now.Add(1))
	if err != nil {
		t.Fatal(err)
	}
	accepted, err := claimed.Accept(
		claimed.Version(), claimed.Attempt(), fixture.asset,
		fixture.input.ExpectedBytes, fixture.now.Add(2),
	)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := evidence.NewProgressReader(acceptedUploadsStub{uploads: []evidence.Upload{accepted}})
	if err != nil {
		t.Fatal(err)
	}
	scope, err := tenant.NewScope(fixture.input.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	progress, err := reader.Find(t.Context(), evidence.UploadPrincipal{
		Scope: scope, CaptureTokenID: fixture.input.CaptureTokenID,
		VerificationID: fixture.input.VerificationID,
	})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if progress.VerificationID != fixture.input.VerificationID || len(progress.Completions) != 1 {
		t.Fatalf("progress = %+v", progress)
	}
	completion := progress.Completions[0]
	if completion.UploadID != fixture.input.ID || completion.EvidenceID != fixture.input.EvidenceID ||
		completion.RequirementKey != fixture.input.RequirementKey ||
		completion.AcquisitionMethod != fixture.input.AcquisitionMethod {
		t.Fatalf("completion = %+v", completion)
	}
}
