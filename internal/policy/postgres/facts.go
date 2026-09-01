package postgres

import (
	"context"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/policy"
)

// DirectFactProjector implements the selected v1 public fact mapping. A
// validated namespaced signal name is its public key and its normalized state
// is preserved exactly. Operational or authority-only states are not guessed.
type DirectFactProjector struct{}

// ProjectFacts returns reference-only facts and rejects duplicate public keys.
func (DirectFactProjector) ProjectFacts(ctx context.Context, projection Projection) ([]policy.Fact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	facts := make([]policy.Fact, 0)
	seen := make(map[policy.FactKey]struct{})
	for _, check := range projection.Checks {
		for _, observation := range check.Observations {
			key, err := policy.NewFactKey(observation.SignalName)
			if err != nil {
				return nil, fmt.Errorf("%w: public signal fact key", policy.ErrInvalid)
			}
			if _, exists := seen[key]; exists {
				return nil, fmt.Errorf("%w: duplicate public signal fact", policy.ErrConflict)
			}
			state, err := directRequirementState(observation.SignalOutcome)
			if err != nil {
				return nil, err
			}
			facts = append(facts, policy.Fact{
				Key: key, State: state,
				Source: policy.FactSource{Kind: policy.FactSourceCheck, Check: &policy.CheckSource{
					CheckID: check.CheckID, CheckVersion: check.Version,
					AttemptID:            check.Attempt.AttemptID,
					ObservationIDs:       []id.Observation{observation.ObservationID},
					ContractDigest:       check.Attempt.RequestDigest,
					ImplementationDigest: check.Attempt.PackageDigest,
				}},
				ObservedAt: observation.RecordedAt, ReasonCodes: append([]string(nil), observation.ReasonCodes...),
			})
			seen[key] = struct{}{}
		}
	}
	return facts, nil
}

func directRequirementState(outcome string) (policy.RequirementState, error) {
	switch outcome {
	case "satisfied":
		return policy.RequirementSatisfied, nil
	case "not_satisfied":
		return policy.RequirementNotSatisfied, nil
	case "inconclusive":
		return policy.RequirementInconclusive, nil
	default:
		return "", fmt.Errorf("%w: public signal outcome", policy.ErrInvalid)
	}
}

var _ FactProjector = DirectFactProjector{}
