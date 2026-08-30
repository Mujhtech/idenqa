package realtime

import (
	"errors"
	"time"
)

// ErrSessionUnavailable deliberately covers inactive, expired, or mismatched
// capture-session authority at realtime admission.
var ErrSessionUnavailable = errors.New("realtime: session unavailable")

// SessionAuthority is the minimum current session state needed to keep a
// redeemed realtime connection active.
type SessionAuthority struct {
	version   int64
	expiresAt time.Time
}

// NewSessionAuthority validates a current session version and absolute expiry.
func NewSessionAuthority(version int64, expiresAt time.Time) (SessionAuthority, error) {
	if version < 1 || expiresAt.IsZero() || expiresAt.Location() != time.UTC {
		return SessionAuthority{}, errors.New("realtime: session authority is invalid")
	}

	return SessionAuthority{version: version, expiresAt: expiresAt}, nil
}

// Version returns the authoritative optimistic session version.
func (authority SessionAuthority) Version() int64 { return authority.version }

// ExpiresAt returns the session's absolute UTC expiry.
func (authority SessionAuthority) ExpiresAt() time.Time { return authority.expiresAt }
