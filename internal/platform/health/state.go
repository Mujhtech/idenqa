// Package health owns process health and drain state.
package health

import "sync/atomic"

// State tracks startup and readiness independently. Its zero value is safe and
// represents a process that has not completed startup.
type State struct {
	started atomic.Bool
	ready   atomic.Bool
}

// MarkStarted records that process startup has completed.
func (state *State) MarkStarted() {
	state.started.Store(true)
}

// MarkReady records that the process may receive traffic.
func (state *State) MarkReady() {
	state.ready.Store(true)
}

// MarkNotReady records that a runtime dependency currently prevents traffic.
func (state *State) MarkNotReady() {
	state.ready.Store(false)
}

// BeginDrain makes the process unready while preserving its startup state.
func (state *State) BeginDrain() {
	state.ready.Store(false)
}

// Started reports whether startup completed successfully.
func (state *State) Started() bool {
	return state.started.Load()
}

// Ready reports whether the process may receive traffic.
func (state *State) Ready() bool {
	return state.ready.Load()
}
