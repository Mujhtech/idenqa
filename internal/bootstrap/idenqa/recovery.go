package idenqa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/operations"
	operationspostgres "github.com/Mujhtech/idenqa/internal/operations/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

type recoveryVerifyOptions struct{ manifestFile, backupRoot, envFile string }

func newRecoveryCommand() *cobra.Command {
	command := &cobra.Command{Use: "recovery", Short: "Verify open-source backup and restore evidence", Args: cli.UsageArgs(cobra.NoArgs), RunE: func(*cobra.Command, []string) error {
		return cli.UsageError(errors.New("recovery requires an operation"))
	}}
	options := &recoveryVerifyOptions{}
	verify := &cobra.Command{Use: "verify", Short: "Verify a restored backup directory", Args: cli.UsageArgs(cobra.NoArgs), RunE: func(command *cobra.Command, _ []string) error { return executeRecoveryVerify(command, options) }}
	verify.Flags().StringVar(&options.manifestFile, "manifest-file", "", "bounded backup manifest JSON file")
	verify.Flags().StringVar(&options.backupRoot, "backup-root", "", "root containing restored backup artifacts")
	_ = verify.MarkFlagFilename("manifest-file", "json")
	_ = verify.MarkFlagDirname("backup-root")
	command.AddCommand(verify)
	reconcile := &cobra.Command{Use: "reconcile", Short: "Inspect restored durable state without payloads", Args: cli.UsageArgs(cobra.NoArgs), RunE: func(command *cobra.Command, _ []string) error { return executeRecoveryReconcile(command, options) }}
	reconcile.Flags().StringVar(&options.envFile, "env-file", config.DefaultEnvFile, "load local configuration from this dotenv file")
	command.AddCommand(reconcile)
	return command
}

func executeRecoveryReconcile(command *cobra.Command, options *recoveryVerifyOptions) error {
	configuration, err := config.LoadAPI(options.envFile)
	if err != nil {
		return cli.RuntimeError("load recovery configuration", err)
	}
	poolConfig, err := pgxpool.ParseConfig(configuration.OperationalDatabaseURL())
	if err != nil {
		return cli.RuntimeError("parse recovery database configuration", errors.New("invalid database configuration"))
	}
	ctx, cancel := context.WithTimeout(command.Context(), configuration.DatabaseConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return cli.RuntimeError("open recovery database", errors.New("database unavailable"))
	}
	defer pool.Close()
	report, err := operationspostgres.Reconcile(command.Context(), pool, time.Now().UTC())
	if err != nil {
		return cli.RuntimeError("reconcile restored state", err)
	}
	encoded, err := json.Marshal(struct {
		operations.ReconciliationReport
		Ready bool `json:"ready"`
	}{report, report.Ready()})
	if err != nil {
		return cli.RuntimeError("encode reconciliation report", err)
	}
	if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\n", encoded); err != nil {
		return cli.RuntimeError("write reconciliation report", err)
	}
	return nil
}

func executeRecoveryVerify(command *cobra.Command, options *recoveryVerifyOptions) error {
	if options.manifestFile == "" || options.backupRoot == "" {
		return cli.UsageError(errors.New("recovery verify requires --manifest-file and --backup-root"))
	}
	info, err := os.Stat(options.manifestFile)
	if err != nil || info.Size() <= 0 || info.Size() > operations.MaximumManifestBytes {
		return cli.RuntimeError("read recovery manifest", operations.ErrInvalid)
	}
	encoded, err := os.ReadFile(options.manifestFile)
	if err != nil {
		return cli.RuntimeError("read recovery manifest", err)
	}
	manifest, err := operations.DecodeManifest(encoded)
	if err != nil {
		return cli.RuntimeError("decode recovery manifest", err)
	}
	report, err := operations.VerifyDirectory(command.Context(), options.backupRoot, manifest, time.Now().UTC())
	if err != nil {
		return cli.RuntimeError("verify restored backup", err)
	}
	result, err := json.Marshal(report)
	if err != nil {
		return cli.RuntimeError("encode recovery report", err)
	}
	if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\n", result); err != nil {
		return cli.RuntimeError("write recovery report", err)
	}
	return nil
}
