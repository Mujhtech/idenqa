package realtime

import (
	"context"
	"errors"
	"sync"
)

// ErrServerDraining identifies an intentional connection stop during process drain.
var ErrServerDraining = errors.New("realtime transport: server draining")

// ErrConnectionIdle identifies a retryable established transport inactivity expiry.
var ErrConnectionIdle = errors.New("realtime transport: connection idle")

// ConnectionLifecycle rejects new socket admissions during drain and tracks
// every admission that can become a hijacked connection.
type ConnectionLifecycle struct {
	mu       sync.Mutex
	draining bool
	drain    chan struct{}
	active   int
	empty    chan struct{}
}

// NewConnectionLifecycle constructs an accepting connection lifecycle.
func NewConnectionLifecycle() *ConnectionLifecycle {
	empty := make(chan struct{})
	close(empty)

	return &ConnectionLifecycle{drain: make(chan struct{}), empty: empty}
}

type connectionAdmission struct {
	once      sync.Once
	lifecycle *ConnectionLifecycle
	drain     <-chan struct{}
}

func (lifecycle *ConnectionLifecycle) admit() (*connectionAdmission, bool) {
	if lifecycle == nil {
		return nil, false
	}
	lifecycle.mu.Lock()
	defer lifecycle.mu.Unlock()
	if lifecycle.draining {
		return nil, false
	}
	if lifecycle.active == 0 {
		lifecycle.empty = make(chan struct{})
	}
	lifecycle.active++

	return &connectionAdmission{lifecycle: lifecycle, drain: lifecycle.drain}, true
}

func (admission *connectionAdmission) done() {
	if admission == nil || admission.lifecycle == nil {
		return
	}
	admission.once.Do(func() {
		admission.lifecycle.mu.Lock()
		defer admission.lifecycle.mu.Unlock()
		admission.lifecycle.active--
		if admission.lifecycle.active == 0 {
			close(admission.lifecycle.empty)
		}
	})
}

// Drain rejects new admissions, signals active connections, and waits for all
// admitted handlers to return within ctx.
func (lifecycle *ConnectionLifecycle) Drain(ctx context.Context) error {
	if lifecycle == nil {
		return nil
	}
	lifecycle.mu.Lock()
	if !lifecycle.draining {
		lifecycle.draining = true
		close(lifecycle.drain)
	}
	empty := lifecycle.empty
	lifecycle.mu.Unlock()

	select {
	case <-empty:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
