// Package experience owns the portable capture-experience aggregate, its
// immutable signed revisions, fail-closed targeting resolution, safe-default
// fallback, and session pinning. It depends on the public contract and owned
// ports only — never HTTP, SQL, task, telemetry, or cloud SDK types.
package experience

import "errors"

// Stable public errors — callers must use errors.Is.
var (
	// ErrInvalid means the command or document violates the domain contract.
	ErrInvalid = errors.New("experience: invalid")
	// ErrNotFound deliberately covers absent and cross-tenant aggregates.
	ErrNotFound = errors.New("experience: not found")
	// ErrConflict identifies a stale expected version or invalid transition.
	ErrConflict = errors.New("experience: lifecycle conflict")
	// ErrRevoked means the aggregate is under kill-switch and refuses the command.
	ErrRevoked = errors.New("experience: revoked")
	// ErrAssetUnavailable means referenced assets cannot be verified now.
	ErrAssetUnavailable = errors.New("experience: asset verification unavailable")
	// ErrUnknownMandatoryCopy means the document references an unknown Core catalogue.
	ErrUnknownMandatoryCopy = errors.New("experience: unknown mandatory copy version")
	// ErrResolutionConflict means equally specific published matches disagree.
	ErrResolutionConflict = errors.New("experience: ambiguous targeting resolution")
	// ErrSignature means a revision or imported manifest failed verification.
	ErrSignature = errors.New("experience: signature verification failed")
)
