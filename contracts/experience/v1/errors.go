package experience

import "errors"

// Stable public errors — callers must use errors.Is.
var (
	// ErrInvalid means the document or envelope violates the closed contract.
	ErrInvalid = errors.New("experience v1: invalid")
	// ErrUnsupportedVersion means the schema version is not implemented.
	ErrUnsupportedVersion = errors.New("experience v1: unsupported version")
	// ErrTooLarge means a bounded field or the document exceeded its limit.
	ErrTooLarge = errors.New("experience v1: too large")
	// ErrReservedCopy means tenant copy used a Core-owned mandatory namespace.
	ErrReservedCopy = errors.New("experience v1: reserved mandatory copy key")
	// ErrSignature means manifest digest or signature verification failed.
	ErrSignature = errors.New("experience v1: signature verification failed")
	// ErrUnknownKey means the manifest key id is not trusted.
	ErrUnknownKey = errors.New("experience v1: unknown signing key")
)
