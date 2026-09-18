package proposal

import "errors"

// Stable public errors — callers must use errors.Is.
var (
	ErrInvalid      = errors.New("proposal v1: invalid")
	ErrVersion      = errors.New("proposal v1: unsupported version")
	ErrNonCanonical = errors.New("proposal v1: non-canonical")
	ErrTooLarge     = errors.New("proposal v1: too large")
	ErrUnknownKind  = errors.New("proposal v1: unknown action kind")
	ErrNotAllowed   = errors.New("proposal v1: not allowed")
	ErrExpired      = errors.New("proposal v1: expired")
	ErrConflict     = errors.New("proposal v1: conflict")
	ErrNotFound     = errors.New("proposal v1: not found")
)
