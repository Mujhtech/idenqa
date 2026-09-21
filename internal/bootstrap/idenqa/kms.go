package idenqa

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/keycustody"
	keycustodypostgres "github.com/Mujhtech/idenqa/internal/keycustody/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/spf13/cobra"
)

type kmsOptions struct {
	envFile         string
	tenantID        string
	domain          string
	version         int64
	expectedVersion int64
	actorKey        string
	reason          string
	confirmation    bool
}

func newKMSCommand() *cobra.Command {
	options := &kmsOptions{}
	command := &cobra.Command{
		Use:   "kms",
		Short: "Operate tenant HMAC key lifecycles",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown kms operation %q", args[0]))
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("kms requires an operation"))
		},
	}
	command.PersistentFlags().StringVar(&options.envFile, "env-file", ".env", "load local configuration from this dotenv file")
	command.PersistentFlags().StringVar(&options.tenantID, "tenant", "", "owning tenant identifier")

	for _, operation := range []struct {
		name  string
		short string
		run   func(*cobra.Command, *kmsOptions) error
	}{
		{name: "create", short: "Create a domain's first HMAC key version", run: executeKMSCreate},
		{name: "rotate", short: "Rotate a domain's active HMAC key version", run: executeKMSRotate},
		{name: "disable", short: "Disable one HMAC key version", run: executeKMSDisable},
		{name: "retire", short: "Retire one disabled HMAC key version", run: executeKMSRetire},
		{name: "show", short: "Show a domain's HMAC key versions", run: executeKMSShow},
	} {
		operation := operation
		entry := &cobra.Command{
			Use:   operation.name,
			Short: operation.short,
			Args:  cli.UsageArgs(cobra.NoArgs),
			PreRunE: func(_ *cobra.Command, _ []string) error {
				return validateKMSOperation(operation.name, options)
			},
			RunE: func(command *cobra.Command, _ []string) error {
				return operation.run(command, options)
			},
		}
		entry.Flags().StringVar(&options.domain, "domain", "", "namespaced identifier domain")
		entry.Flags().StringVar(&options.actorKey, "actor-key", "", "authenticated operator API-key identifier")
		entry.Flags().StringVar(&options.reason, "reason", "", "reason recorded in the immutable audit trail")
		entry.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "expected current key version")
		entry.Flags().BoolVar(&options.confirmation, "confirm", false, "confirm this key transition")
		if operation.name == "disable" || operation.name == "retire" {
			entry.Flags().Int64Var(&options.version, "version", 0, "target key version")
		}
		command.AddCommand(entry)
	}
	command.AddCommand(newKMSRewrapCommand(), newKMSDestroyCommand(), newKMSRecoveryCommand())

	return command
}

func validateKMSOperation(operation string, options *kmsOptions) error {
	if _, err := id.ParseTenant(options.tenantID); err != nil {
		return cli.UsageError(errors.New("kms requires a valid --tenant"))
	}
	if _, err := keycustody.ParseDomain(options.domain); err != nil {
		return cli.UsageError(errors.New("kms requires a valid --domain"))
	}
	if operation == "show" {
		return nil
	}
	if _, err := id.ParseAPIKey(options.actorKey); err != nil {
		return cli.UsageError(errors.New("kms requires a valid --actor-key"))
	}
	if len(strings.TrimSpace(options.reason)) < 8 {
		return cli.UsageError(errors.New("kms requires a --reason of at least eight characters"))
	}
	if !options.confirmation {
		return cli.UsageError(errors.New("kms mutation requires --confirm"))
	}
	switch operation {
	case "create":
		if options.expectedVersion != 0 {
			return cli.UsageError(errors.New("kms create requires expected version zero"))
		}
	case "rotate":
		if options.expectedVersion < 1 {
			return cli.UsageError(errors.New("kms rotate requires a positive --expected-version"))
		}
	case "disable", "retire":
		if options.version < 1 || options.expectedVersion != options.version {
			return cli.UsageError(errors.New("kms disable and retire require --version equal to --expected-version"))
		}
	}

	return nil
}

func executeKMSCreate(command *cobra.Command, options *kmsOptions) error {
	return runKMSMutation(command, options, "create")
}

func executeKMSRotate(command *cobra.Command, options *kmsOptions) error {
	return runKMSMutation(command, options, "rotate")
}

func executeKMSDisable(command *cobra.Command, options *kmsOptions) error {
	return runKMSMutation(command, options, "disable")
}

func executeKMSRetire(command *cobra.Command, options *kmsOptions) error {
	return runKMSMutation(command, options, "retire")
}

func runKMSMutation(command *cobra.Command, options *kmsOptions, operation string) error {
	ctx := command.Context()
	dependencies, err := openOperationalDependencies(ctx, options.envFile)
	if err != nil {
		return cli.RuntimeError("open kms database", err)
	}
	defer dependencies.Close()
	scope, err := tenant.NewScope(mustTenant(options.tenantID))
	if err != nil {
		return cli.UsageError(errors.New("kms tenant identifier is invalid"))
	}
	store, err := keycustodypostgres.New(dependencies.pool, dependencies.keys, dependencies.keys, dependencies.references, func() (string, []byte, error) {
		identifier, err := dependencies.identifiers.New(keycustody.KeyIDPrefix)
		if err != nil {
			return "", nil, err
		}
		material := make([]byte, keycustody.KeySize)
		if _, err := rand.Read(material); err != nil {
			clear(material)
			return "", nil, err
		}

		return identifier.String(), material, nil
	})
	if err != nil {
		return cli.RuntimeError("compose kms persistence", err)
	}
	service, err := keycustody.NewService(store, dependencies.identifiers, time.Now)
	if err != nil {
		return cli.RuntimeError("compose kms service", err)
	}
	actor, _ := id.ParseAPIKey(options.actorKey)
	result, err := service.ExecuteDirect(ctx, scope, actor, idempotencyKey(command), keycustody.Command{
		Operation: operation, Domain: options.domain, ExpectedVersion: options.expectedVersion,
		Reason: options.reason,
	})
	if err != nil {
		return cli.RuntimeError("execute kms transition", err)
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "kms_domain_%s domain=%s active_version=%d generation=%d\n",
		operation, result.Domain, result.ActiveVersion, result.Generation)
	if err != nil {
		return cli.RuntimeError("write kms result", err)
	}

	return nil
}

func executeKMSShow(command *cobra.Command, options *kmsOptions) error {
	ctx := command.Context()
	dependencies, err := openOperationalDependencies(ctx, options.envFile)
	if err != nil {
		return cli.RuntimeError("open kms database", err)
	}
	defer dependencies.Close()
	scope, err := tenant.NewScope(mustTenant(options.tenantID))
	if err != nil {
		return cli.UsageError(errors.New("kms tenant identifier is invalid"))
	}
	store, err := keycustodypostgres.New(dependencies.pool, nil, nil, nil, func() (string, []byte, error) {
		return "", nil, errors.New("read-only command")
	})
	if err != nil {
		return cli.RuntimeError("compose kms persistence", err)
	}
	result, err := store.Read(ctx, scope, options.domain)
	if err != nil {
		return cli.RuntimeError("read kms domain", err)
	}
	for _, version := range result.Versions {
		if _, err := fmt.Fprintf(command.OutOrStdout(), "kms_version domain=%s version=%d state=%s\n",
			result.Domain, version.Version, version.State); err != nil {
			return cli.RuntimeError("write kms result", err)
		}
	}
	if _, err := fmt.Fprintf(command.OutOrStdout(), "kms_domain domain=%s active_version=%d generation=%d\n",
		result.Domain, result.ActiveVersion, result.Generation); err != nil {
		return cli.RuntimeError("write kms result", err)
	}

	return nil
}

func mustTenant(value string) id.Tenant {
	tenantID, _ := id.ParseTenant(value)

	return tenantID
}

func idempotencyKey(command *cobra.Command) string {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return command.CommandPath()
	}

	return hex.EncodeToString(value)
}
