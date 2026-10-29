package idenqa

import (
	"context"
	"encoding/json"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	usagepostgres "github.com/Mujhtech/idenqa/internal/usage/postgres"
	"github.com/spf13/cobra"
)

func newUsageHandoffCommand(migration *migrationOptions) *cobra.Command {
	var input usagepostgres.Handoff
	command := &cobra.Command{Use: "usage-handoff", Short: "Start a new usage coverage epoch after draining the previous consumer", Args: cli.UsageArgs(cobra.NoArgs), RunE: func(command *cobra.Command, _ []string) error {
		configuration, err := config.LoadAPI(migration.envFile)
		if err != nil {
			return cli.RuntimeError("load usage handoff configuration", err)
		}
		ctx, cancel := context.WithTimeout(command.Context(), 30*time.Second)
		defer cancel()
		pool, err := postgres.Open(ctx, postgres.Config{URL: configuration.OperationalDatabaseURL(), MaxConnections: 1, MaxConnectionAge: configuration.DatabaseMaxLifetime, MaxConnectionIdle: configuration.DatabaseMaxIdleTime, HealthCheckInterval: configuration.DatabaseHealthInterval, ConnectTimeout: configuration.DatabaseConnectTimeout})
		if err != nil {
			return cli.RuntimeError("open usage handoff database", err)
		}
		defer pool.Close()
		store, err := usagepostgres.New(pool)
		if err != nil {
			return cli.RuntimeError("compose usage handoff", err)
		}
		coverage, err := store.Handoff(ctx, input)
		if err != nil {
			return cli.RuntimeError("handoff usage coverage", err)
		}
		if err = json.NewEncoder(command.OutOrStdout()).Encode(coverage); err != nil {
			return cli.RuntimeError("write usage coverage boundary", err)
		}
		return nil
	}}
	f := command.Flags()
	f.StringVar(&input.Tenant, "tenant", "", "exact Core tenant")
	f.StringVar(&input.ExpectedEpoch, "expected-epoch", "", "current coverage epoch")
	f.StringVar(&input.ExpectedConsumer, "expected-consumer", "", "current consumer digest, or empty for unowned legacy source")
	f.StringVar(&input.ExpectedRouting, "expected-routing", "", "current routing digest, or empty for unowned legacy source")
	f.StringVar(&input.Epoch, "epoch", "", "new unique coverage epoch")
	f.StringVar(&input.Consumer, "consumer", "", "new exact consumer digest")
	f.StringVar(&input.Routing, "routing", "", "new exact route configuration digest")
	f.StringVar(&input.Actor, "actor", "", "opaque operator identity")
	f.StringVar(&input.ReasonReference, "reason-reference", "", "opaque approved change reference")
	f.BoolVar(&input.CarryPending, "carry-pending", false, "preserve pending receipts under the same consumer for a validated route expansion; claims no previous coverage")
	for _, name := range []string{"tenant", "expected-epoch", "epoch", "consumer", "routing", "actor", "reason-reference"} {
		_ = command.MarkFlagRequired(name)
	}
	return command
}
