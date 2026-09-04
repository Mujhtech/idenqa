package idenqa

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	tenantpostgres "github.com/Mujhtech/idenqa/internal/tenant/postgres"
	"github.com/spf13/cobra"
)

type tenantOptions struct {
	envFile   string
	actor     string
	reason    string
	encodedID string
	version   int64
}

func newTenantCommand() *cobra.Command {
	options := &tenantOptions{}
	command := &cobra.Command{
		Use:   "tenant",
		Short: "Administer tenant lifecycle records",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown tenant operation %q", args[0]))
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("tenant requires an operation"))
		},
	}
	command.PersistentFlags().StringVar(&options.envFile, "env-file", "", "load local configuration from this dotenv file")
	command.PersistentFlags().StringVar(&options.actor, "actor", "", "operator assertion recorded in the audit trail")
	command.PersistentFlags().StringVar(&options.reason, "reason", "", "reason recorded in the audit trail")
	command.AddCommand(
		newTenantOperationCommand("create", options),
		newTenantOperationCommand("inspect", options),
		newTenantOperationCommand("disable", options),
	)

	return command
}

func newTenantOperationCommand(operation string, options *tenantOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   operation,
		Short: tenantOperationDescription(operation),
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return validateTenantOptions(operation, options)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executeTenantOperation(command, operation, options)
		},
	}
	if operation != "create" {
		command.Flags().StringVar(&options.encodedID, "id", "", "tenant identifier")
	}
	if operation == "disable" {
		command.Flags().Int64Var(&options.version, "version", 0, "expected lifecycle version")
	}

	return command
}

func tenantOperationDescription(operation string) string {
	switch operation {
	case "create":
		return "Create a tenant"
	case "inspect":
		return "Inspect a tenant and record the access"
	case "disable":
		return "Disable a tenant with optimistic concurrency"
	default:
		return "Operate on a tenant"
	}
}

func validateTenantOptions(operation string, options *tenantOptions) error {
	if options.actor == "" || options.reason == "" {
		return cli.UsageError(errors.New("tenant operation requires --actor and --reason"))
	}
	if operation != "create" && options.encodedID == "" {
		return cli.UsageError(errors.New("tenant operation requires --id"))
	}
	if operation == "disable" && options.version < 1 {
		return cli.UsageError(errors.New("tenant disable requires a positive --version"))
	}

	return nil
}

func executeTenantOperation(command *cobra.Command, operation string, options *tenantOptions) error {
	ctx := command.Context()
	configuration, err := config.LoadAPI(options.envFile)
	if err != nil {
		return cli.RuntimeError("load tenant administration configuration", err)
	}
	if configuration.DatabaseAdminURL == "" {
		return cli.RuntimeError(
			"load tenant administration configuration",
			errors.New("administrative database url is required"),
		)
	}
	pool, err := postgres.Open(ctx, postgres.Config{
		URL: configuration.DatabaseAdminURL, MaxConnections: 2, MinConnections: 0,
		MaxConnectionAge: configuration.DatabaseMaxLifetime, MaxConnectionIdle: configuration.DatabaseMaxIdleTime,
		HealthCheckInterval: configuration.DatabaseHealthInterval, ConnectTimeout: configuration.DatabaseConnectTimeout,
	})
	if err != nil {
		return cli.RuntimeError("open tenant administration database", err)
	}
	defer pool.Close()
	store, err := tenantpostgres.New(pool)
	if err != nil {
		return cli.RuntimeError("compose tenant administration", err)
	}
	identifierGenerator, err := id.NewSystemGenerator()
	if err != nil {
		return cli.RuntimeError("compose tenant administration", err)
	}
	admin, err := tenant.NewAdmin(store, identifierGenerator, clock.System{})
	if err != nil {
		return cli.RuntimeError("compose tenant administration", err)
	}
	action := tenant.AdminAction{Actor: options.actor, Reason: options.reason}
	var value tenant.Tenant
	switch operation {
	case "create":
		value, err = admin.Create(ctx, action)
	case "inspect", "disable":
		identifier, parseErr := id.ParseTenant(options.encodedID)
		if parseErr != nil {
			return cli.RuntimeError("parse tenant id", parseErr)
		}
		if operation == "inspect" {
			value, err = admin.Inspect(ctx, action, identifier)
		} else {
			value, err = admin.Disable(ctx, action, identifier, options.version)
		}
	}
	if err != nil {
		return cli.RuntimeError("execute tenant operation", err)
	}
	disabledAt := "-"
	if value.DisabledAt() != nil {
		disabledAt = value.DisabledAt().Format(time.RFC3339Nano)
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "tenant id=%s state=%s version=%s created_at=%s updated_at=%s disabled_at=%s\n",
		value.ID(), value.State(), strconv.FormatInt(value.Version(), 10), value.CreatedAt().Format(time.RFC3339Nano),
		value.UpdatedAt().Format(time.RFC3339Nano), disabledAt)
	if err != nil {
		return cli.RuntimeError("write tenant result", err)
	}

	return nil
}
