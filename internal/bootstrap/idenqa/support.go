package idenqa

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/support"
	supportpostgres "github.com/Mujhtech/idenqa/internal/support/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/spf13/cobra"
)

type supportOptions struct {
	envFile         string
	tenantID        string
	actorKey        string
	identifier      string
	grantee         string
	patterns        []string
	permissions     []string
	duration        time.Duration
	reason          string
	target          string
	expectedVersion int64
	confirmation    bool
}

func newSupportCommand() *cobra.Command {
	options := &supportOptions{}
	command := &cobra.Command{
		Use:   "support",
		Short: "Operate delegated and emergency support access",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown support operation %q", args[0]))
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("support requires an operation"))
		},
	}
	command.PersistentFlags().StringVar(&options.envFile, "env-file", ".env", "load local configuration from this dotenv file")
	command.PersistentFlags().StringVar(&options.tenantID, "tenant", "", "owning tenant identifier")
	command.PersistentFlags().StringVar(&options.actorKey, "actor-key", "", "authenticated operator API-key identifier")

	command.AddCommand(
		newSupportGrantCommand(options),
		newSupportReadCommand(options, "list", "List delegated support grants", "grants", ""),
		newSupportReadCommand(options, "show", "Show one delegated support grant", "grant", "id"),
		newSupportRevokeCommand(options),
		newBreakGlassCommand(options),
	)

	return command
}

func newSupportGrantCommand(options *supportOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "grant",
		Short: "Grant time-bounded support access",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(*cobra.Command, []string) error {
			if err := validateSupportActor(options); err != nil {
				return err
			}
			if options.grantee == "" || len(options.patterns) == 0 || options.duration < support.MinimumDuration ||
				options.duration > support.MaximumGrantDuration || len(options.reason) < 8 || !options.confirmation {
				return cli.UsageError(errors.New("support grant requires --grantee, --pattern, a bounded --duration, --reason, and --confirm"))
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return runSupportMutation(command, options, support.Command{
				Operation: "grant", Grantee: options.grantee, Patterns: options.patterns,
				Duration: options.duration, Reason: options.reason,
			})
		},
	}
	command.Flags().StringVar(&options.grantee, "grantee", "", "support principal identifier")
	command.Flags().StringArrayVar(&options.patterns, "pattern", nil, "permission pattern to grant; repeatable")
	command.Flags().DurationVar(&options.duration, "duration", 0, "bounded access duration, for example 24h")
	command.Flags().StringVar(&options.reason, "reason", "", "reason recorded in the immutable audit trail")
	command.Flags().BoolVar(&options.confirmation, "confirm", false, "confirm this support grant")

	return command
}

func newSupportReadCommand(options *supportOptions, name, short, kind, argument string) *cobra.Command {
	command := &cobra.Command{
		Use:   name,
		Short: short,
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(*cobra.Command, []string) error {
			if _, err := id.ParseTenant(options.tenantID); err != nil {
				return cli.UsageError(errors.New("support requires a valid --tenant"))
			}
			if argument == "id" && strings.TrimSpace(options.identifier) == "" {
				return cli.UsageError(errors.New("support show requires --id"))
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return runSupportRead(command, options, kind)
		},
	}
	if argument == "id" {
		command.Flags().StringVar(&options.identifier, "id", "", "support record identifier")
	}

	return command
}

func newSupportRevokeCommand(options *supportOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "revoke",
		Short: "Revoke one delegated support grant",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(*cobra.Command, []string) error {
			return validateSupportVersioned(options)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return runSupportMutation(command, options, support.Command{
				Operation: "grant_revoke", Identifier: options.identifier,
				ExpectedVersion: options.expectedVersion, Reason: options.reason,
			})
		},
	}
	command.Flags().StringVar(&options.identifier, "id", "", "support grant identifier")
	command.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "expected grant version")
	command.Flags().StringVar(&options.reason, "reason", "", "reason recorded in the immutable audit trail")
	command.Flags().BoolVar(&options.confirmation, "confirm", false, "confirm this revocation")

	return command
}

func newBreakGlassCommand(options *supportOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "break-glass",
		Short: "Operate emergency break-glass access",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown break-glass operation %q", args[0]))
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("break-glass requires an operation"))
		},
	}
	request := &cobra.Command{
		Use:   "request",
		Short: "Request approval for bounded emergency access",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(*cobra.Command, []string) error {
			if err := validateSupportActor(options); err != nil {
				return err
			}
			if len(options.permissions) == 0 || options.duration < support.MinimumDuration ||
				options.duration > support.MaximumEmergencyDuration || len(options.reason) < 8 || !options.confirmation {
				return cli.UsageError(errors.New("break-glass request requires --permission, a bounded --duration, --reason, and --confirm"))
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return runSupportMutation(command, options, support.Command{
				Operation: "break_glass_request", Permissions: options.permissions,
				Duration: options.duration, Reason: options.reason,
			})
		},
	}
	request.Flags().StringArrayVar(&options.permissions, "permission", nil, "bounded emergency permission; repeatable")
	request.Flags().DurationVar(&options.duration, "duration", 0, "bounded emergency access duration")
	request.Flags().StringVar(&options.reason, "reason", "", "reason recorded in the immutable audit trail")
	request.Flags().BoolVar(&options.confirmation, "confirm", false, "confirm this request")
	command.AddCommand(request)

	for _, operation := range []struct {
		name  string
		short string
	}{
		{name: "approve", short: "Approve a pending break-glass request"},
		{name: "deny", short: "Deny a pending break-glass request"},
		{name: "revoke", short: "Revoke an approved break-glass request"},
	} {
		operation := operation
		entry := &cobra.Command{
			Use:   operation.name,
			Short: operation.short,
			Args:  cli.UsageArgs(cobra.NoArgs),
			PreRunE: func(*cobra.Command, []string) error {
				return validateSupportVersioned(options)
			},
			RunE: func(command *cobra.Command, _ []string) error {
				return runSupportMutation(command, options, support.Command{
					Operation: "break_glass_" + operation.name, Identifier: options.identifier,
					ExpectedVersion: options.expectedVersion, Reason: options.reason,
				})
			},
		}
		entry.Flags().StringVar(&options.identifier, "id", "", "break-glass request identifier")
		entry.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "expected request version")
		entry.Flags().StringVar(&options.reason, "reason", "", "reason recorded in the immutable audit trail")
		entry.Flags().BoolVar(&options.confirmation, "confirm", false, "confirm this transition")
		command.AddCommand(entry)
	}

	use := &cobra.Command{
		Use:   "use",
		Short: "Record one approved break-glass use",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(*cobra.Command, []string) error {
			if err := validateSupportActor(options); err != nil {
				return err
			}
			if strings.TrimSpace(options.identifier) == "" || options.expectedVersion < 1 || !options.confirmation {
				return cli.UsageError(errors.New("break-glass use requires --id, positive --expected-version, and --confirm"))
			}
			if len(options.permissions) != 1 || options.target == "" {
				return cli.UsageError(errors.New("break-glass use requires exactly one --permission and a --target"))
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return runSupportMutation(command, options, support.Command{
				Operation: "break_glass_use", Identifier: options.identifier,
				ExpectedVersion: options.expectedVersion, Permissions: options.permissions,
				Target: options.target, Reason: "break-glass use recorded",
			})
		},
	}
	use.Flags().StringVar(&options.identifier, "id", "", "break-glass request identifier")
	use.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "expected request version")
	use.Flags().StringArrayVar(&options.permissions, "permission", nil, "permission exercised")
	use.Flags().StringVar(&options.target, "target", "", "non-secret target reference")
	use.Flags().BoolVar(&options.confirmation, "confirm", false, "confirm this use")
	command.AddCommand(use)

	command.AddCommand(newBreakGlassReadCommand(options))

	return command
}

func newBreakGlassReadCommand(options *supportOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "show",
		Short: "Show one break-glass request and its uses",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(*cobra.Command, []string) error {
			if _, err := id.ParseTenant(options.tenantID); err != nil {
				return cli.UsageError(errors.New("support requires a valid --tenant"))
			}
			if strings.TrimSpace(options.identifier) == "" {
				return cli.UsageError(errors.New("break-glass show requires --id"))
			}

			return nil
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return runSupportRead(command, options, "emergency")
		},
	}
	command.Flags().StringVar(&options.identifier, "id", "", "break-glass request identifier")

	return command
}

func validateSupportActor(options *supportOptions) error {
	if _, err := id.ParseTenant(options.tenantID); err != nil {
		return cli.UsageError(errors.New("support requires a valid --tenant"))
	}
	if _, err := id.ParseAPIKey(options.actorKey); err != nil {
		return cli.UsageError(errors.New("support requires a valid --actor-key"))
	}

	return nil
}

func validateSupportVersioned(options *supportOptions) error {
	if err := validateSupportActor(options); err != nil {
		return err
	}
	if strings.TrimSpace(options.identifier) == "" || options.expectedVersion < 1 || len(options.reason) < 8 || !options.confirmation {
		return cli.UsageError(errors.New("support transition requires --id, positive --expected-version, --reason, and --confirm"))
	}

	return nil
}

func openSupportService(ctx context.Context, options *supportOptions) (*operationalDependencies, *support.Service, *supportpostgres.Store, error) {
	dependencies, err := openOperationalDependencies(ctx, options.envFile)
	if err != nil {
		return nil, nil, nil, err
	}
	store, err := supportpostgres.New(dependencies.pool, dependencies.identifiers)
	if err != nil {
		dependencies.Close()

		return nil, nil, nil, err
	}
	service, err := support.NewService(store, access.TenantRegistry(), time.Now)
	if err != nil {
		dependencies.Close()

		return nil, nil, nil, err
	}

	return dependencies, service, store, nil
}

func runSupportMutation(command *cobra.Command, options *supportOptions, request support.Command) error {
	ctx := command.Context()
	dependencies, service, _, err := openSupportService(ctx, options)
	if err != nil {
		return cli.RuntimeError("compose support access", err)
	}
	defer dependencies.Close()
	scope, err := tenant.NewScope(mustTenant(options.tenantID))
	if err != nil {
		return cli.UsageError(errors.New("support tenant identifier is invalid"))
	}
	actor, _ := id.ParseAPIKey(options.actorKey)
	result, err := service.ExecuteDirect(ctx, scope, actor, idempotencyKey(command), request)
	if err != nil {
		return cli.RuntimeError("execute support transition", err)
	}
	switch {
	case result.Grant != nil:
		if _, err := fmt.Fprintf(command.OutOrStdout(), "support_grant id=%s state=%s version=%d expires_at=%s\n",
			result.Grant.ID, result.Grant.State, result.Grant.Version, result.Grant.ExpiresAt.Format(time.RFC3339)); err != nil {
			return cli.RuntimeError("write support result", err)
		}
	case result.Emergency != nil:
		if _, err := fmt.Fprintf(command.OutOrStdout(), "break_glass id=%s state=%s version=%d permissions=%s\n",
			result.Emergency.ID, result.Emergency.State, result.Emergency.Version, strings.Join(result.Emergency.Permissions, ",")); err != nil {
			return cli.RuntimeError("write support result", err)
		}
	}

	return nil
}

func runSupportRead(command *cobra.Command, options *supportOptions, kind string) error {
	ctx := command.Context()
	dependencies, _, store, err := openSupportService(ctx, options)
	if err != nil {
		return cli.RuntimeError("compose support access", err)
	}
	defer dependencies.Close()
	scope, err := tenant.NewScope(mustTenant(options.tenantID))
	if err != nil {
		return cli.UsageError(errors.New("support tenant identifier is invalid"))
	}
	result, err := store.Read(ctx, scope, kind, options.identifier)
	if err != nil {
		return cli.RuntimeError("read support state", err)
	}
	switch {
	case result.Grant != nil:
		if _, err := fmt.Fprintf(command.OutOrStdout(), "support_grant id=%s grantee=%s state=%s version=%d permissions=%s\n",
			result.Grant.ID, result.Grant.Grantee, result.Grant.State, result.Grant.Version, strings.Join(result.Grant.Permissions, ",")); err != nil {
			return cli.RuntimeError("write support result", err)
		}
	case len(result.Grants) > 0:
		for _, grant := range result.Grants {
			if _, err := fmt.Fprintf(command.OutOrStdout(), "support_grant id=%s grantee=%s state=%s expires_at=%s\n",
				grant.ID, grant.Grantee, grant.State, grant.ExpiresAt.Format(time.RFC3339)); err != nil {
				return cli.RuntimeError("write support result", err)
			}
		}
	case result.Emergency != nil:
		if _, err := fmt.Fprintf(command.OutOrStdout(), "break_glass id=%s state=%s version=%d uses=%d\n",
			result.Emergency.ID, result.Emergency.State, result.Emergency.Version, len(result.Uses)); err != nil {
			return cli.RuntimeError("write support result", err)
		}
	}

	return nil
}
