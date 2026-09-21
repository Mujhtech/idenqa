package worker

import (
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	"github.com/Mujhtech/idenqa/internal/model"
	"github.com/Mujhtech/idenqa/internal/platform/telemetry"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/provider"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

// Compile-time proof that one bounded adapter satisfies every worker-owned
// boundary metric receiver. A missing or renamed method fails the build.
var (
	_ verificationpostgres.Metrics = (*telemetry.DomainMetrics)(nil)
	_ deliverytask.Metrics         = (*telemetry.DomainMetrics)(nil)
	_ provider.Metrics             = (*telemetry.DomainMetrics)(nil)
	_ model.Metrics                = (*telemetry.DomainMetrics)(nil)
	_ privacy.Metrics              = (*telemetry.DomainMetrics)(nil)
)
