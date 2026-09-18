package api

import (
	"context"

	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/delivery"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
)

// webhookEnqueuer adapts public replay to the same installation's owned queue.
type webhookEnqueuer struct {
	database      database
	configuration config.API
}

func (queue webhookEnqueuer) EnqueueTx(ctx context.Context, tx pg.Transaction, intents ...task.Intent) error {
	pool, ok := queue.database.(*processDatabase)
	if !ok || queue.configuration.HeadgateInstallationID == "" {
		return delivery.ErrQueueUnavailable
	}
	configuration := taskheadgate.DefaultConfig(queue.configuration.HeadgateInstallationID)
	configuration.Schema = queue.configuration.HeadgateSchema
	adapter, err := taskheadgate.NewPostgres(pool.Native(), configuration)
	if err != nil {
		return delivery.ErrQueueUnavailable
	}
	return adapter.EnqueueTx(ctx, tx, intents...)
}
