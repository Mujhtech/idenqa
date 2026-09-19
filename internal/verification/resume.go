package verification

import (
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/idempotency"
)

// OperationResumeVerification separates subject-input recovery from creation.
const OperationResumeVerification = "verifications.resume"

// ResumeMutation is one authorised, idempotent awaiting-input recovery.
type ResumeMutation struct {
	VerificationID  id.Verification
	ExpectedVersion int64
	ReplacementID   id.CaptureToken
	EventID         id.Event
	Actor           id.APIKey
	KeyVersion      access.CaptureTokenKeyVersion
	At              time.Time
	TokenExpiresAt  time.Time
	Idempotency     idempotency.Request
}

// ResumeResult is reference-only on replay. A bearer is signed only for a
// freshly committed replacement and is never reconstructed for exact replay.
type ResumeResult struct {
	Session    Session
	Credential access.CaptureCredential
	Replaced   bool
	Replayed   bool
}
