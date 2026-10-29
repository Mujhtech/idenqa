package idenqa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/db/migrations"
	admissionpostgres "github.com/Mujhtech/idenqa/internal/admission/postgres"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/spf13/cobra"
)

func newAdmissionCounterCommand(options *migrationOptions) *cobra.Command {
	var tenantID, kind, key, actor, reason string
	var window int64
	command := &cobra.Command{Use: "admission-counter-rebuild", Short: "Repair one operational counter from durable reservations", Args: cli.UsageArgs(cobra.NoArgs), RunE: func(command *cobra.Command, _ []string) error {
		configuration, err := config.LoadAPI(options.envFile)
		if err != nil {
			return cli.RuntimeError("load admission repair configuration", err)
		}
		policy, err := config.LoadAdmissionPolicy(configuration.AdmissionRuntimeFile)
		if err != nil {
			return cli.RuntimeError("load admission repair policy", err)
		}
		if policy == nil {
			return cli.RuntimeError("load admission repair policy", errors.New("operational admission is not configured"))
		}
		ctx, cancel := context.WithTimeout(command.Context(), 30*time.Second)
		defer cancel()
		pool, err := postgres.Open(ctx, postgres.Config{URL: configuration.DatabaseConnectionString(), Role: configuration.DatabaseRole, MaxConnections: 1, MaxConnectionAge: configuration.DatabaseMaxLifetime, MaxConnectionIdle: configuration.DatabaseMaxIdleTime, HealthCheckInterval: configuration.DatabaseHealthInterval, ConnectTimeout: configuration.DatabaseConnectTimeout})
		if err != nil {
			return cli.RuntimeError("open admission repair database", err)
		}
		defer pool.Close()
		if err := pool.Check(ctx, migrations.LatestVersion); err != nil {
			return cli.RuntimeError("check admission repair schema", err)
		}
		store, err := admissionpostgres.New(pool, policy)
		if err != nil {
			return cli.RuntimeError("compose admission repair", err)
		}
		if err := store.RebuildCounter(ctx, tenantID, kind, key, window, actor, reason); err != nil {
			return cli.RuntimeError("rebuild admission counter", err)
		}
		_, err = fmt.Fprintln(command.OutOrStdout(), "admission counter rebuilt")
		return err
	}}
	f := command.Flags()
	f.StringVar(&tenantID, "tenant", "", "exact Core tenant")
	f.StringVar(&kind, "kind", "", "exact configured quota kind")
	f.StringVar(&key, "key", "", "exact configured quota key")
	f.Int64Var(&window, "window", 0, "persisted UTC window start in Unix nanoseconds, zero for a resource lifetime")
	f.StringVar(&actor, "actor", "", "opaque operator identity")
	f.StringVar(&reason, "reason-reference", "", "opaque approved repair reference")
	for _, name := range []string{"tenant", "kind", "key", "window", "actor", "reason-reference"} {
		_ = command.MarkFlagRequired(name)
	}
	return command
}
