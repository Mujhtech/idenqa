// Package syntheticplan defines the opt-in synthetic workflow fixture.
package syntheticplan

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/verification"
)

// Plan is the explicit synthetic v1 success fixture, never a real identity check plan.
// Enabling it is restricted to an operator's synthetic-data test installation.
type Plan struct{}

// Plan pins the two synthetic success fixtures to one immutable session input.
func (Plan) Plan(ctx context.Context, input verification.PlanInput) ([]verification.PlannedCheck, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if input.TenantID.IsZero() || input.VerificationID.IsZero() || input.PolicyID.IsZero() || input.ProfileDigest == "" {
		return nil, verification.ErrInvalidCheck
	}
	request := digest(fmt.Sprintf("%s|%s|%s|%s", input.TenantID, input.VerificationID, input.ProfileDigest, input.PolicyID))
	checks := make([]verification.PlannedCheck, 0, 2)
	for _, runner := range []struct {
		name string
		kind verification.RunnerKind
	}{
		{"synthetic.document", verification.RunnerProvider}, {"synthetic.liveness", verification.RunnerModel},
	} {
		checks = append(checks, verification.PlannedCheck{Name: runner.name, RunnerKind: runner.kind,
			Provenance: verification.Provenance{RunnerID: runner.name, RunnerVersion: "1.0.0",
				PackageDigest: digest("idenqa.synthetic.v1:" + runner.name), ContractMajor: 1,
				RequestDigest: request, Configuration: digest("idenqa.synthetic.plan.v1:success")}})
	}
	return checks, nil
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
