package verification

import "regexp"

var (
	failureClassPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	failureCodePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
)

// SessionFailure is the bounded operational reason a verification workflow
// reached the terminal failed state. It explains why the workflow could not
// proceed; it is never an identity outcome and never carries decision,
// evidence, or provider meaning. A failed lifecycle always carries exactly one
// SessionFailure and every other lifecycle carries none. The name
// distinguishes it from the attempt-level Failure used by check execution.
type SessionFailure struct {
	Class string
	Code  string
}

// Validate rejects an unset, unbounded, or non-token failure. Class is limited
// to 32 lowercase token characters and code to 64.
func (failure SessionFailure) Validate() error {
	if !failureClassPattern.MatchString(failure.Class) || !failureCodePattern.MatchString(failure.Code) {
		return ErrSessionConflict
	}
	return nil
}
