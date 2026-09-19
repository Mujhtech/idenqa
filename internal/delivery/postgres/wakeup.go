package postgres

import (
	"context"
	"errors"
	"sync"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// WebhookEventChannel is the fixed routing-only PostgreSQL wake-up channel for
// newly emitted catalogue events.
const WebhookEventChannel = "idenqa_webhook_events_v1"

var errStreamWakeupsUnavailable = errors.New("delivery postgres: stream wakeups unavailable")

type notificationListener interface {
	Wait(context.Context) (string, error)
	Close()
}

// WakeupHub multiplexes one PostgreSQL LISTEN connection across active tenant
// event streams. Payloads contain routing identifiers only; consumers always
// reread the durable event table after a wake-up.
type WakeupHub struct {
	mu          sync.Mutex
	listener    notificationListener
	cancel      context.CancelFunc
	done        chan struct{}
	subscribers map[string]map[chan struct{}]struct{}
	terminal    error
	closeOnce   sync.Once
}

// NewWakeupHub starts one process-local LISTEN multiplexer.
func NewWakeupHub(listener notificationListener) (*WakeupHub, error) {
	if listener == nil {
		return nil, errStreamWakeupsUnavailable
	}
	ctx, cancel := context.WithCancel(context.Background())
	hub := &WakeupHub{
		listener: listener, cancel: cancel, done: make(chan struct{}),
		subscribers: make(map[string]map[chan struct{}]struct{}),
	}
	go hub.run(ctx)
	return hub, nil
}

// Wait blocks for a possibly duplicated or lossy wake-up for this tenant.
// Cancellation unregisters the waiter.
func (hub *WakeupHub) Wait(ctx context.Context, tenantID id.Tenant) error {
	if hub == nil || tenantID.IsZero() {
		return errStreamWakeupsUnavailable
	}
	key := tenantID.String()
	wakeup := make(chan struct{}, 1)
	hub.mu.Lock()
	if hub.terminal != nil {
		hub.mu.Unlock()
		return hub.terminal
	}
	if hub.subscribers[key] == nil {
		hub.subscribers[key] = make(map[chan struct{}]struct{})
	}
	hub.subscribers[key][wakeup] = struct{}{}
	hub.mu.Unlock()
	defer hub.unregister(key, wakeup)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wakeup:
		return nil
	}
}

// Close stops the listener and releases its dedicated pool connection.
func (hub *WakeupHub) Close() {
	if hub == nil {
		return
	}
	hub.closeOnce.Do(func() {
		hub.cancel()
		<-hub.done
		hub.listener.Close()
	})
}

func (hub *WakeupHub) run(ctx context.Context) {
	defer close(hub.done)
	for {
		payload, err := hub.listener.Wait(ctx)
		if err != nil {
			hub.fail(err)
			return
		}
		hub.notify(payload)
	}
}

func (hub *WakeupHub) notify(key string) {
	if len(key) > 64 {
		return
	}
	hub.mu.Lock()
	defer hub.mu.Unlock()
	for subscriber := range hub.subscribers[key] {
		select {
		case subscriber <- struct{}{}:
		default:
		}
	}
}

func (hub *WakeupHub) fail(err error) {
	if errors.Is(err, context.Canceled) {
		err = errStreamWakeupsUnavailable
	}
	hub.mu.Lock()
	hub.terminal = errors.Join(errStreamWakeupsUnavailable, err)
	for _, subscribers := range hub.subscribers {
		for subscriber := range subscribers {
			select {
			case subscriber <- struct{}{}:
			default:
			}
		}
	}
	hub.mu.Unlock()
}

func (hub *WakeupHub) unregister(key string, subscriber chan struct{}) {
	hub.mu.Lock()
	defer hub.mu.Unlock()
	delete(hub.subscribers[key], subscriber)
	if len(hub.subscribers[key]) == 0 {
		delete(hub.subscribers, key)
	}
}
