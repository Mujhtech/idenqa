package idenqa

import (
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/spf13/cobra"
)

type migrationOptions struct {
	envFile   string
	confirmed bool
}

func newMigrationCommand() *cobra.Command {
	options := &migrationOptions{}
	command := &cobra.Command{
		Use:   "migrate",
		Short: "Inspect and update the PostgreSQL schema",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown migrate operation %q", args[0]))
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("migrate requires an operation"))
		},
	}
	command.PersistentFlags().StringVar(&options.envFile, "env-file", "", "load local configuration from this dotenv file")
	command.AddCommand(
		newMigrationOperationCommand("preflight", options),
		newMigrationOperationCommand("up", options),
		newMigrationOperationCommand("version", options),
		newMigrationDownCommand(options),
		newHeadgateMigrationCommand(options),
	)

	return command
}

func newMigrationOperationCommand(operation string, options *migrationOptions) *cobra.Command {
	return &cobra.Command{
		Use:   operation,
		Short: migrationOperationDescription(operation),
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return executeMigration(command, operation, options)
		},
	}
}

func newMigrationDownCommand(options *migrationOptions) *cobra.Command {
	command := newMigrationOperationCommand("down", options)
	command.Flags().BoolVar(&options.confirmed, "confirm", false, "confirm one development or test rollback step")
	command.PreRunE = func(_ *cobra.Command, _ []string) error {
		if !options.confirmed {
			return cli.UsageError(errors.New("migrate down requires --confirm"))
		}

		return nil
	}

	return command
}

func migrationOperationDescription(operation string) string {
	switch operation {
	case "preflight":
		return "Check schema compatibility without changing it"
	case "up":
		return "Apply all pending migrations"
	case "version":
		return "Print the current and latest schema versions"
	case "down":
		return "Roll back one development or test migration"
	default:
		return "Operate on the PostgreSQL schema"
	}
}

func executeMigration(command *cobra.Command, operation string, options *migrationOptions) error {
	ctx := command.Context()
	configuration, err := config.LoadAPI(options.envFile)
	if err != nil {
		return cli.RuntimeError("load migration configuration", err)
	}
	migrator, err := postgres.OpenMigrator(ctx, postgres.MigrationConfig{
		URL:              configuration.OperationalDatabaseURL(),
		ConnectTimeout:   configuration.DatabaseConnectTimeout,
		StatementTimeout: configuration.DatabaseMigrationTimeout,
	})
	if err != nil {
		return cli.RuntimeError("open migration engine", err)
	}

	var report postgres.MigrationReport
	switch operation {
	case "preflight", "version":
		report, err = migrator.Preflight(ctx)
	case "up":
		report, err = migrator.Up(ctx)
	case "down":
		report, err = migrator.DownOne(ctx, postgres.RollbackGuard{
			Environment: configuration.Environment,
			Confirmed:   options.confirmed,
		})
	}
	closeErr := migrator.Close()
	if err != nil || closeErr != nil {
		return cli.RuntimeError("execute migration", errors.Join(err, closeErr))
	}
	_, err = fmt.Fprintf(
		command.OutOrStdout(),
		"schema current=%d latest=%d dirty=%t pending=%t\n",
		report.Current,
		report.Latest,
		report.Dirty,
		report.Pending,
	)
	if err != nil {
		return cli.RuntimeError("write migration result", err)
	}

	return nil
}
