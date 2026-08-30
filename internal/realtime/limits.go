// Package realtime owns the capture control-channel protocol independently of
// any WebSocket implementation.
package realtime

import (
	"errors"
	"time"
)

const (
	// DefaultTicketLifetime is the selected connection-ticket handshake window.
	DefaultTicketLifetime = 30 * time.Second
	// DefaultMaximumMessageBytes is the selected encoded-message ceiling.
	DefaultMaximumMessageBytes = 16 * 1024
	// DefaultOutboundQueueDepth is the selected per-connection queue capacity.
	DefaultOutboundQueueDepth = 64
	// DefaultUnacknowledgedLimit is the selected outstanding command ceiling.
	DefaultUnacknowledgedLimit = 16
	// DefaultHelloTimeout is the selected client hello deadline.
	DefaultHelloTimeout = 5 * time.Second
	// DefaultWriteTimeout is the selected per-message write deadline.
	DefaultWriteTimeout = 5 * time.Second
	// DefaultPingInterval is the selected native ping interval.
	DefaultPingInterval = 15 * time.Second
	// DefaultPongTimeout is the selected native pong deadline.
	DefaultPongTimeout = 10 * time.Second
	// DefaultIdleTimeout is the selected connection inactivity deadline.
	DefaultIdleTimeout = 45 * time.Second
	// DefaultConnectionLifetime is the selected deployment connection ceiling.
	DefaultConnectionLifetime = 30 * time.Minute
	// MinimumTicketLifetime is the shortest permitted ticket lifetime.
	MinimumTicketLifetime = 10 * time.Second
	// MaximumTicketLifetime is the longest permitted ticket lifetime.
	MaximumTicketLifetime = 60 * time.Second
	// MinimumMessageBytes is the smallest configurable message ceiling.
	MinimumMessageBytes int64 = 4 * 1024
	// MaximumMessageBytes is the largest configurable message ceiling.
	MaximumMessageBytes = 64 * 1024
)

// LimitConfig contains deployment-configurable values within protocol bounds.
type LimitConfig struct {
	TicketLifetime      time.Duration
	MaximumMessageBytes int64
	OutboundQueueDepth  int
	UnacknowledgedLimit int
	HelloTimeout        time.Duration
	WriteTimeout        time.Duration
	PingInterval        time.Duration
	PongTimeout         time.Duration
	IdleTimeout         time.Duration
	ConnectionLifetime  time.Duration
}

// Limits is an immutable validated control-channel resource policy.
type Limits struct{ config LimitConfig }

// DefaultLimits returns the selected v1 defaults.
func DefaultLimits() Limits {
	limits, err := NewLimits(DefaultLimitConfig())
	if err != nil {
		panic("realtime: default limits are invalid")
	}

	return limits
}

// DefaultLimitConfig returns a mutable copy of the selected v1 defaults for
// deployment configuration before validation with NewLimits.
func DefaultLimitConfig() LimitConfig {
	return LimitConfig{
		TicketLifetime: DefaultTicketLifetime, MaximumMessageBytes: DefaultMaximumMessageBytes,
		OutboundQueueDepth: DefaultOutboundQueueDepth, UnacknowledgedLimit: DefaultUnacknowledgedLimit,
		HelloTimeout: DefaultHelloTimeout, WriteTimeout: DefaultWriteTimeout,
		PingInterval: DefaultPingInterval, PongTimeout: DefaultPongTimeout,
		IdleTimeout: DefaultIdleTimeout, ConnectionLifetime: DefaultConnectionLifetime,
	}
}

// NewLimits validates deployment values against v1 safety bounds.
func NewLimits(config LimitConfig) (Limits, error) {
	if config.TicketLifetime < MinimumTicketLifetime || config.TicketLifetime > MaximumTicketLifetime {
		return Limits{}, errors.New("realtime: ticket lifetime must be between 10 and 60 seconds")
	}
	if config.MaximumMessageBytes < MinimumMessageBytes || config.MaximumMessageBytes > MaximumMessageBytes {
		return Limits{}, errors.New("realtime: maximum message bytes must be between 4 and 64 KiB")
	}
	if config.OutboundQueueDepth < 16 || config.OutboundQueueDepth > 256 {
		return Limits{}, errors.New("realtime: outbound queue depth must be between 16 and 256")
	}
	if config.UnacknowledgedLimit < 1 || config.UnacknowledgedLimit > 64 ||
		config.UnacknowledgedLimit > config.OutboundQueueDepth {
		return Limits{}, errors.New("realtime: unacknowledged command limit is invalid")
	}
	if config.HelloTimeout < time.Second || config.HelloTimeout > 10*time.Second ||
		config.WriteTimeout < time.Second || config.WriteTimeout > 10*time.Second {
		return Limits{}, errors.New("realtime: hello and write timeouts must be between 1 and 10 seconds")
	}
	if config.PingInterval < 5*time.Second || config.PingInterval > 30*time.Second ||
		config.PongTimeout < 5*time.Second || config.PongTimeout > 15*time.Second {
		return Limits{}, errors.New("realtime: ping and pong timing is invalid")
	}
	if config.IdleTimeout < config.PingInterval+config.PongTimeout ||
		config.IdleTimeout > 2*time.Minute {
		return Limits{}, errors.New("realtime: idle timeout must cover ping plus pong and not exceed two minutes")
	}
	if config.ConnectionLifetime < 5*time.Minute || config.ConnectionLifetime > time.Hour {
		return Limits{}, errors.New("realtime: connection lifetime must be between 5 and 60 minutes")
	}

	return Limits{config: config}, nil
}

// TicketLifetime returns the single-use ticket handshake window.
func (limits Limits) TicketLifetime() time.Duration { return limits.config.TicketLifetime }

// MaximumMessageBytes returns the maximum encoded control-message size.
func (limits Limits) MaximumMessageBytes() int64 { return limits.config.MaximumMessageBytes }

// OutboundQueueDepth returns the per-connection outbound queue capacity.
func (limits Limits) OutboundQueueDepth() int { return limits.config.OutboundQueueDepth }

// UnacknowledgedLimit returns the per-connection outstanding command limit.
func (limits Limits) UnacknowledgedLimit() int { return limits.config.UnacknowledgedLimit }

// HelloTimeout returns the deadline for receiving client.hello.
func (limits Limits) HelloTimeout() time.Duration { return limits.config.HelloTimeout }

// WriteTimeout returns the deadline for writing one protocol message.
func (limits Limits) WriteTimeout() time.Duration { return limits.config.WriteTimeout }

// PingInterval returns the native WebSocket ping interval.
func (limits Limits) PingInterval() time.Duration { return limits.config.PingInterval }

// PongTimeout returns the deadline for a native WebSocket pong.
func (limits Limits) PongTimeout() time.Duration { return limits.config.PongTimeout }

// IdleTimeout returns the maximum connection inactivity period.
func (limits Limits) IdleTimeout() time.Duration { return limits.config.IdleTimeout }

// ConnectionLifetime returns the deployment ceiling for one connection.
func (limits Limits) ConnectionLifetime() time.Duration { return limits.config.ConnectionLifetime }
