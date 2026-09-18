package review

import (
	"github.com/Mujhtech/idenqa/internal/policy"
)

// RoutingRule pins initial case requirements to one exact tenant policy revision.
// It does not authorize findings or resolve the remaining review-policy schema.
type RoutingRule struct {
	PermittedFindings   []PermittedFinding `json:"permitted_findings,omitempty"`
	TenantID            string             `json:"tenant_id"`
	PolicyID            string             `json:"policy_id"`
	Revision            uint32             `json:"revision"`
	PolicyDigest        string             `json:"policy_digest"`
	RequiredCertificate string             `json:"required_certificate"`
	Oversight           Oversight          `json:"oversight"`
}

// Matches requires the full immutable policy identity, not an active-policy alias.
func (rule RoutingRule) Matches(snapshot policy.Snapshot) bool {
	reference := snapshot.Policy()
	return rule.TenantID == snapshot.TenantID().String() && rule.PolicyID == reference.ID.String() && rule.Revision == reference.Revision && rule.PolicyDigest == reference.Digest
}
