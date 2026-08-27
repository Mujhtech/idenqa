// Package clock provides explicit time sources for process and domain wiring.
package clock

import "time"

// Clock supplies the current time to code whose behaviour depends on it.
type Clock interface {
	Now() time.Time
}

// System reads the process wall clock.
type System struct{}

// Now returns the current UTC time.
func (System) Now() time.Time {
	return time.Now().UTC()
}
