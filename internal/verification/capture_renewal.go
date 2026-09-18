package verification

import (
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
)

// CaptureRenewal replaces one expected capture credential without extending a session.
type CaptureRenewal struct {
	ExpectedToken id.CaptureToken
	Token         id.CaptureToken
	Actor         id.APIKey
	KeyVersion    access.CaptureTokenKeyVersion
	At            time.Time
	ExpiresAt     time.Time
	Idempotency   idempotency.Request
}
