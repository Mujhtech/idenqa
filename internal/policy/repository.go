package policy

import (
	"context"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// Repository owns durable decision lineage at the policy consuming boundary.
type Repository interface {
	Append(context.Context, tenant.Scope, Decision) error
	Find(context.Context, tenant.Scope, id.Decision) (Decision, error)
	FindLatest(context.Context, tenant.Scope, id.Verification) (Decision, error)
}

// HistoryRepository is the additive decision-lineage read capability. Keeping
// it separate preserves narrow repositories used by authoring-only consumers.
type HistoryRepository interface {
	List(context.Context, tenant.Scope, id.Verification, id.Decision, int) ([]Decision, error)
}
