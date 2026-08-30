package realtime_test

import (
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/realtime"
)

func TestDefaultLimits(t *testing.T) {
	t.Parallel()

	limits := realtime.DefaultLimits()
	if limits.TicketLifetime() != 30*time.Second || limits.MaximumMessageBytes() != 16*1024 ||
		limits.OutboundQueueDepth() != 64 || limits.UnacknowledgedLimit() != 16 ||
		limits.HelloTimeout() != 5*time.Second || limits.WriteTimeout() != 5*time.Second ||
		limits.PingInterval() != 15*time.Second || limits.PongTimeout() != 10*time.Second ||
		limits.IdleTimeout() != 45*time.Second || limits.ConnectionLifetime() != 30*time.Minute {
		t.Fatalf("DefaultLimits() = %#v", limits)
	}
}

func TestNewLimitsRejectsUnsafeValues(t *testing.T) {
	t.Parallel()

	valid := realtime.LimitConfig{
		TicketLifetime: 30 * time.Second, MaximumMessageBytes: 16 * 1024,
		OutboundQueueDepth: 64, UnacknowledgedLimit: 16,
		HelloTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second,
		PingInterval: 15 * time.Second, PongTimeout: 10 * time.Second,
		IdleTimeout: 45 * time.Second, ConnectionLifetime: 30 * time.Minute,
	}
	tests := []struct {
		name   string
		mutate func(*realtime.LimitConfig)
	}{
		{"short ticket", func(config *realtime.LimitConfig) { config.TicketLifetime = 9 * time.Second }},
		{"large message", func(config *realtime.LimitConfig) { config.MaximumMessageBytes = 64*1024 + 1 }},
		{"small queue", func(config *realtime.LimitConfig) { config.OutboundQueueDepth = 15 }},
		{"unacknowledged exceeds queue", func(config *realtime.LimitConfig) { config.UnacknowledgedLimit = 65 }},
		{"short hello", func(config *realtime.LimitConfig) { config.HelloTimeout = time.Millisecond }},
		{"long write", func(config *realtime.LimitConfig) { config.WriteTimeout = 11 * time.Second }},
		{"short ping", func(config *realtime.LimitConfig) { config.PingInterval = 4 * time.Second }},
		{"long pong", func(config *realtime.LimitConfig) { config.PongTimeout = 16 * time.Second }},
		{"idle misses heartbeat", func(config *realtime.LimitConfig) { config.IdleTimeout = 20 * time.Second }},
		{"long lifetime", func(config *realtime.LimitConfig) { config.ConnectionLifetime = 61 * time.Minute }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := valid
			test.mutate(&config)
			if _, err := realtime.NewLimits(config); err == nil {
				t.Fatal("NewLimits() error = nil")
			}
		})
	}
}
