package idenqa

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/spf13/cobra"
)

func newHeadgateMigrationCommand(options *migrationOptions) *cobra.Command {
	command := &cobra.Command{
		Use: "headgate", Short: "Inspect and update the pinned Headgate schema",
		Args: cli.UsageArgs(cobra.NoArgs),
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("migrate headgate requires an operation"))
		},
	}
	for _, operation := range []string{"preflight", "up", "version"} {
		operation := operation
		command.AddCommand(&cobra.Command{
			Use: operation, Short: migrationOperationDescription(operation),
			Args: cli.UsageArgs(cobra.NoArgs),
			RunE: func(command *cobra.Command, _ []string) error {
				return executeHeadgateMigration(command, operation, options)
			},
		})
	}
	down := &cobra.Command{
		Use: "down", Short: migrationOperationDescription("down"), Args: cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			if !options.confirmed {
				return cli.UsageError(errors.New("migrate headgate down requires --confirm"))
			}
			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executeHeadgateMigration(command, "down", options)
		},
	}
	down.Flags().BoolVar(&options.confirmed, "confirm", false, "confirm one development or test rollback step")
	command.AddCommand(down)
	return command
}

func executeHeadgateMigration(
	command *cobra.Command,
	operation string,
	options *migrationOptions,
) error {
	ctx := command.Context()
	configuration, err := config.LoadAPI(options.envFile)
	if err != nil {
		return cli.RuntimeError("load Headgate migration configuration", err)
	}
	if operation == "down" && configuration.Environment == "production" {
		return cli.RuntimeError("execute Headgate migration", errors.New("rollback is disabled in production"))
	}
	migrator, err := taskheadgate.OpenMigrator(
		ctx, configuration.OperationalDatabaseURL(), configuration.HeadgateSchema,
		configuration.DatabaseConnectTimeout, configuration.DatabaseMigrationTimeout,
	)
	if err != nil {
		return cli.RuntimeError("open Headgate migration engine", err)
	}
	var report taskheadgate.MigrationReport
	switch operation {
	case "preflight", "version":
		report, err = migrator.Preflight(ctx)
	case "up":
		report, err = migrator.Up(ctx)
	case "down":
		report, err = migrator.DownOne(ctx)
	}
	closeContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	closeErr := migrator.Close(closeContext)
	if err != nil || closeErr != nil {
		return cli.RuntimeError("execute Headgate migration", errors.Join(err, closeErr))
	}
	if _, err := fmt.Fprintf(
		command.OutOrStdout(),
		"headgate schema=%s state=%s current=%d latest=%d healthy=%t pending=%t steps=%d\n",
		configuration.HeadgateSchema, report.State, report.Current, report.Latest,
		report.Healthy, report.Pending, report.Steps,
	); err != nil {
		return cli.RuntimeError("write Headgate migration result", err)
	}
	return nil
}
