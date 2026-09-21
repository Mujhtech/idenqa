package runnersecret

import "sync/atomic"

// Holder is an atomically replaceable string value for a reloadable runner
// setting. A zero Holder loads as the empty string.
type Holder struct{ value atomic.Pointer[string] }

// Store publishes value for subsequent loads.
func (holder *Holder) Store(value string) {
	if holder == nil {
		return
	}
	holder.value.Store(&value)
}

// Load returns the current value.
func (holder *Holder) Load() string {
	if holder == nil {
		return ""
	}
	if value := holder.value.Load(); value != nil {
		return *value
	}

	return ""
}
