package delivery

import (
	"fmt"

	"github.com/Mujhtech/idenqa/internal/platform/kms"
)

// BodyPurpose separates catalogue event and delivery body wrapping from other
// delivery key uses such as endpoint signing-secret wrapping.
func BodyPurpose() kms.Purpose {
	purpose, _ := kms.NewPurpose("delivery.webhook-body")
	return purpose
}

// BodyContext returns the canonical authenticated context that binds one
// wrapped body to its tenant and catalogue event.
func BodyContext(tenantID, eventID string) []byte {
	return []byte(fmt.Sprintf("v1\n%s\n%s", tenantID, eventID))
}
