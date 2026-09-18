package idenqa

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/evidence"
	evidencepostgres "github.com/Mujhtech/idenqa/internal/evidence/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/clock"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms/local"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/spf13/cobra"
)

type evidenceKeyOptions struct {
	envFile         string
	keyringFile     string
	tenantID        string
	evidenceID      string
	version         int64
	principalType   string
	principalID     string
	tenantActorType string
	tenantActorID   string
	reason          string
	confirmation    bool
}

func newEvidenceKeyCommand() *cobra.Command {
	options := &evidenceKeyOptions{}
	command := &cobra.Command{
		Use:   "evidence-key",
		Short: "Operate evidence envelope keys",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown evidence-key operation %q", args[0]))
			}

			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("evidence-key requires an operation"))
		},
	}
	command.PersistentFlags().StringVar(&options.envFile, "env-file", config.DefaultEnvFile, "load local configuration from this dotenv file")
	initialize := &cobra.Command{
		Use:   "init",
		Short: "Create a new local evidence KEK keyring",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return validateEvidenceKeyInit(options)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executeEvidenceKeyInit(command, options)
		},
	}
	initialize.Flags().StringVar(&options.keyringFile, "keyring-file", "", "new local KEK keyring file")
	command.AddCommand(initialize)
	rewrap := &cobra.Command{
		Use:   "rewrap",
		Short: "Rewrap one exact evidence content key under the active KEK",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return validateEvidenceKeyRewrap(options)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executeEvidenceKeyRewrap(command, options)
		},
	}
	rewrap.Flags().StringVar(&options.keyringFile, "keyring-file", "", "existing local KEK keyring file")
	rewrap.Flags().StringVar(&options.tenantID, "tenant", "", "owning tenant identifier")
	rewrap.Flags().StringVar(&options.evidenceID, "id", "", "evidence identifier")
	rewrap.Flags().Int64Var(&options.version, "version", 0, "expected evidence aggregate version")
	rewrap.Flags().StringVar(&options.principalType, "principal-type", "", "authenticated principal classification")
	rewrap.Flags().StringVar(&options.principalID, "principal-id", "", "authenticated principal identifier")
	rewrap.Flags().StringVar(&options.tenantActorType, "tenant-actor-type", "", "effective tenant-actor classification")
	rewrap.Flags().StringVar(&options.tenantActorID, "tenant-actor-id", "", "effective tenant-actor identifier")
	rewrap.Flags().StringVar(&options.reason, "reason", "", "reason recorded in the immutable audit trail")
	rewrap.Flags().BoolVar(&options.confirmation, "confirm", false, "confirm this optimistic key transition")
	command.AddCommand(rewrap)

	return command
}

func validateEvidenceKeyInit(options *evidenceKeyOptions) error {
	if options.keyringFile == "" || strings.TrimSpace(options.keyringFile) != options.keyringFile {
		return cli.UsageError(errors.New("evidence-key init requires a valid --keyring-file"))
	}

	return nil
}

func executeEvidenceKeyInit(command *cobra.Command, options *evidenceKeyOptions) error {
	if err := command.Context().Err(); err != nil {
		return cli.RuntimeError("initialize evidence keyring", err)
	}
	keyring, err := local.Create(options.keyringFile)
	if err != nil {
		return cli.RuntimeError("initialize evidence keyring", err)
	}
	if err := keyring.Close(); err != nil {
		return cli.RuntimeError("close initialized evidence keyring", err)
	}
	if _, err := fmt.Fprintln(command.OutOrStdout(), "evidence_key_initialized active_key_version=v1"); err != nil {
		return cli.RuntimeError("write evidence-key result", err)
	}

	return nil
}

func validateEvidenceKeyRewrap(options *evidenceKeyOptions) error {
	if options.keyringFile == "" || options.tenantID == "" || options.evidenceID == "" || options.version < 1 {
		return cli.UsageError(errors.New("evidence-key rewrap requires --keyring-file, --tenant, --id, and positive --version"))
	}
	if !options.confirmation {
		return cli.UsageError(errors.New("evidence-key rewrap requires --confirm"))
	}
	if _, err := id.ParseTenant(options.tenantID); err != nil {
		return cli.UsageError(errors.New("evidence-key tenant identifier is invalid"))
	}
	if _, err := id.ParseEvidence(options.evidenceID); err != nil {
		return cli.UsageError(errors.New("evidence-key evidence identifier is invalid"))
	}
	if !rewrapAttribution(options).Valid() {
		return cli.UsageError(errors.New("evidence-key rewrap attribution is invalid"))
	}

	return nil
}

func executeEvidenceKeyRewrap(command *cobra.Command, options *evidenceKeyOptions) error {
	ctx := command.Context()
	tenantID, _ := id.ParseTenant(options.tenantID)
	evidenceID, _ := id.ParseEvidence(options.evidenceID)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return cli.UsageError(errors.New("evidence-key tenant identifier is invalid"))
	}
	configuration, err := config.LoadAPI(options.envFile)
	if err != nil {
		return cli.RuntimeError("load evidence-key configuration", err)
	}
	if configuration.DatabaseURL == "" {
		return cli.RuntimeError("load evidence-key configuration", errors.New("runtime database url is required"))
	}
	pool, err := postgres.Open(ctx, postgres.Config{
		URL: configuration.DatabaseURL, Role: configuration.DatabaseRole,
		MaxConnections: 2, MinConnections: 0,
		MaxConnectionAge:    configuration.DatabaseMaxLifetime,
		MaxConnectionIdle:   configuration.DatabaseMaxIdleTime,
		HealthCheckInterval: configuration.DatabaseHealthInterval,
		ConnectTimeout:      configuration.DatabaseConnectTimeout,
	})
	if err != nil {
		return cli.RuntimeError("open evidence-key database", err)
	}
	defer pool.Close()
	if err := pool.Check(ctx, migrations.LatestVersion); err != nil {
		return cli.RuntimeError("check evidence-key database schema", err)
	}
	catalog, err := evidence.BuiltInCatalog()
	if err != nil {
		return cli.RuntimeError("compose evidence-key registry", err)
	}
	store, err := evidencepostgres.New(pool, catalog)
	if err != nil {
		return cli.RuntimeError("compose evidence-key persistence", err)
	}
	keyring, err := local.Open(options.keyringFile)
	if err != nil {
		return cli.RuntimeError("open evidence keyring", err)
	}
	defer func() { _ = keyring.Close() }()
	rewrapper, err := evidence.NewRewrapper(store, store, keyring, keyring)
	if err != nil {
		return cli.RuntimeError("compose evidence-key rewrap", err)
	}
	asset, err := rewrapper.Rewrap(
		ctx, scope, evidenceID, options.version, rewrapAttribution(options), clock.System{}.Now(),
	)
	if err != nil {
		return cli.RuntimeError("rewrap evidence key", err)
	}
	wrapped := asset.Record().Content.Envelope.WrappedKey
	if _, err := fmt.Fprintf(
		command.OutOrStdout(),
		"evidence_key_rewrapped evidence_id=%s aggregate_version=%d key_provider=%s key_reference=%s key_version=%s key_algorithm=%s\n",
		asset.ID(), asset.Version(), wrapped.Provider, wrapped.Reference, wrapped.Version, wrapped.Algorithm,
	); err != nil {
		return cli.RuntimeError("write evidence-key result", err)
	}

	return nil
}

func rewrapAttribution(options *evidenceKeyOptions) evidence.CommandAttribution {
	return evidence.CommandAttribution{
		Principal:   evidence.Actor{Type: options.principalType, ID: options.principalID},
		TenantActor: evidence.Actor{Type: options.tenantActorType, ID: options.tenantActorID},
		Reason:      options.reason,
	}
}
