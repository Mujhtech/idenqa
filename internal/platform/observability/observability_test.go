package observability_test

import (
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/observability"
)

func TestBoundedEnumValuesCollapseForbiddenIdentifiers(t *testing.T) {
	t.Parallel()
	if got := observability.State("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV").Safe(); got != "other" {
		t.Fatalf("state = %q, want other", got)
	}
	if got := observability.Outcome("sub_01ARZ3NDEKTSV4RRFFQ69G5FAV").Safe(); got != "other" {
		t.Fatalf("outcome = %q, want other", got)
	}
	if got := observability.FailureClass("evd_01ARZ3NDEKTSV4RRFFQ69G5FAV").Safe(); got != "other" {
		t.Fatalf("failure class = %q, want other", got)
	}
	if got := observability.ReviewOutcome("tsk_01ARZ3NDEKTSV4RRFFQ69G5FAV").Safe(); got != "other" {
		t.Fatalf("review outcome = %q, want other", got)
	}
	if got := observability.DataClass("sub_01ARZ3NDEKTSV4RRFFQ69G5FAV").Safe(); got != "other" {
		t.Fatalf("data class = %q, want other", got)
	}
}

func TestBoundedIdentitiesRejectMixedCaseIdentifiers(t *testing.T) {
	t.Parallel()
	if got := observability.Provider("pvd_01ARZ3NDEKTSV4RRFFQ69G5FAV").Safe(); got != "pvd_01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("provider = %q, want unchanged contract identifier", got)
	}
	if got := observability.Provider("smileid").Safe(); got != "smileid" {
		t.Fatalf("provider = %q, want deployment adapter", got)
	}
	subjectID := "sub_01ARZ3NDEKTSV4RRFFQ69G5FAV"
	if got := observability.Provider(subjectID).Safe(); got != "other" {
		t.Fatalf("provider = %q, want other for subject identifier", got)
	}
	if got := observability.Model(subjectID).Safe(); got != "other" {
		t.Fatalf("model = %q, want other for subject identifier", got)
	}
	if got := observability.Model("idenqa.model.liveness").Safe(); got != "idenqa.model.liveness" {
		t.Fatalf("model = %q, want deployment model name", got)
	}
	if got := observability.Provider("").Safe(); got != "other" {
		t.Fatalf("empty provider = %q, want other", got)
	}
}

func TestBoundedRegionsRejectUnboundedValues(t *testing.T) {
	t.Parallel()
	if got := observability.Region("sa-riyadh-1").Safe(); got != "sa-riyadh-1" {
		t.Fatalf("region = %q, want unchanged", got)
	}
	if got := observability.Region("SA-RIYADH").Safe(); got != "unknown" {
		t.Fatalf("region = %q, want unknown", got)
	}
	if got := observability.Region("evd_01ARZ3NDEKTSV4RRFFQ69G5FAV").Safe(); got != "unknown" {
		t.Fatalf("region = %q, want unknown", got)
	}
	if got := observability.Region("").Safe(); got != "unknown" {
		t.Fatalf("empty region = %q, want unknown", got)
	}
}

func TestCaptureStepMappingUsesBoundedCatalogueLabels(t *testing.T) {
	t.Parallel()
	if got := observability.NewCaptureStep("idenqa.artefact.document_front"); got != observability.StepDocumentFront {
		t.Fatalf("step = %q, want document_front", got)
	}
	if got := observability.NewCaptureStep("tenant.artefact.custom"); got != observability.StepOther {
		t.Fatalf("step = %q, want other", got)
	}
	if got := observability.NewCaptureStep("idenqa.artefact.document_front").Safe(); got != "document_front" {
		t.Fatalf("safe step = %q, want document_front", got)
	}
}

func TestPrivacyRequestLabelsCollapseUnknownValues(t *testing.T) {
	t.Parallel()
	for _, requestType := range []observability.RequestType{
		observability.RequestAccess, observability.RequestPortability, observability.RequestCorrection,
		observability.RequestRestriction, observability.RequestObjection, observability.RequestErasure,
	} {
		if got := requestType.Safe(); got != string(requestType) {
			t.Fatalf("request type = %q, want %q", got, requestType)
		}
	}
	if got := observability.RequestType("sub_01ARZ3NDEKTSV4RRFFQ69G5FAV").Safe(); got != "other" {
		t.Fatalf("unbounded request type = %q, want other", got)
	}
	if got := observability.RequestType("").Safe(); got != "other" {
		t.Fatalf("empty request type = %q, want other", got)
	}
	for _, state := range []observability.RequestState{
		observability.RequestStateRequested, observability.RequestStateInReview, observability.RequestStateApproved,
		observability.RequestStatePartiallyApproved, observability.RequestStateDenied, observability.RequestStateExecuting,
		observability.RequestStateCompleted, observability.RequestStateFailed, observability.RequestStateWithdrawn,
		observability.RequestStateExpired,
	} {
		if got := state.Safe(); got != string(state) {
			t.Fatalf("request state = %q, want %q", got, state)
		}
	}
	if got := observability.RequestState("prq_01ARZ3NDEKTSV4RRFFQ69G5FAV").Safe(); got != "other" {
		t.Fatalf("unbounded request state = %q, want other", got)
	}
}
