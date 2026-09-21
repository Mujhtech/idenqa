package api

import (
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/telemetry"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/review"
	verificationpostgres "github.com/Mujhtech/idenqa/internal/verification/postgres"
)

// Compile-time proof that one bounded adapter satisfies every API-owned
// boundary metric receiver. A missing or renamed method fails the build.
var (
	_ verificationpostgres.Metrics = (*telemetry.DomainMetrics)(nil)
	_ evidencepostgres.Metrics     = (*telemetry.DomainMetrics)(nil)
	_ privacy.Metrics              = (*telemetry.DomainMetrics)(nil)
	_ review.Metrics               = (*telemetry.DomainMetrics)(nil)
)
