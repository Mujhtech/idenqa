package idenqa

import (
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/postgres/runtimepermissions"
	"github.com/spf13/cobra"
)

type runtimePermissionOptions struct{ role string }

func newRuntimePermissionsCommand(migration *migrationOptions) *cobra.Command {
	options := &runtimePermissionOptions{}
	command := &cobra.Command{
		Use:   "permissions",
		Short: "Reconcile the versioned least-privilege runtime database role",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if options.role == "" {
				return cli.UsageError(errors.New("migrate permissions requires --runtime-role"))
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			configuration, err := config.LoadAPI(migration.envFile)
			if err != nil {
				return cli.RuntimeError("load runtime permission configuration", err)
			}
			report, err := runtimepermissions.Apply(command.Context(), configuration.OperationalDatabaseURL(), options.role, configuration.DatabaseMigrationTimeout)
			if err != nil {
				return cli.RuntimeError("reconcile runtime database permissions", err)
			}
			if _, err := fmt.Fprintf(command.OutOrStdout(), "runtime permissions version=%s role=%s\n", report.Version, report.Role); err != nil {
				return cli.RuntimeError("write runtime permission result", err)
			}
			return nil
		},
	}
	command.Flags().StringVar(&options.role, "runtime-role", "", "NOLOGIN PostgreSQL role used by API and worker connections")
	return command
}
