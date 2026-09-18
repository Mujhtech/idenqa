// Package proposal owns the non-authoritative AI proposal lifecycle, guardrails,
// automation modes, and prompt/model registries described by gap-audit section 3.
package proposal

import "errors"

// Stable domain errors — public errors have codes via transport layer.
var (
	ErrNotFound       = errors.New("proposal: not found")
	ErrConflict       = errors.New("proposal: conflict")
	ErrInvalid        = errors.New("proposal: invalid")
	ErrNotAllowed     = errors.New("proposal: not allowed")
	ErrExpired        = errors.New("proposal: expired")
	ErrUnknownKind    = errors.New("proposal: unknown action kind")
	ErrModeDisabled   = errors.New("proposal: automation disabled")
	ErrApprovalNeeded = errors.New("proposal: human approval required")
	ErrRateLimited    = errors.New("proposal: rate limited")
)
