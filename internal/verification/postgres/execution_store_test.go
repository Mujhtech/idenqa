package postgres

import (
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/verification"
)

func TestObservationInsertParamsStoresNoReasonsAsEmptyArray(t *testing.T) {
	t.Parallel()
	tenantID, _ := id.ParseTenant("ten_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	verificationID, _ := id.ParseVerification("ver_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	checkID, _ := id.ParseCheck("chk_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	observationID, _ := id.ParseObservation("obs_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	parameters := observationInsertParams(
		verification.Check{TenantID: tenantID, VerificationID: verificationID, ID: checkID},
		verification.Observation{ID: observationID, Signal: verification.Signal{
			Name: "synthetic.document", Outcome: verification.SignalSatisfied,
		}},
	)
	if parameters.ReasonCodes == nil || len(parameters.ReasonCodes) != 0 {
		t.Fatalf("reason codes = %#v, want non-nil empty array", parameters.ReasonCodes)
	}
}
