package idenqa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/Mujhtech/idenqa/db/migrations"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	platformpostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/spf13/cobra"
)

type policyDecisionOptions struct {
	envFile    string
	tenantID   string
	decisionID string
	bundleFile string
	output     string
}

type policyRepositoryOpener func(context.Context, string) (policy.Repository, func(), error)

func newPolicyCommand() *cobra.Command { return newPolicyCommandWith(openPolicyRepository) }

func newPolicyCommandWith(openRepository policyRepositoryOpener) *cobra.Command {
	command := &cobra.Command{
		Use:   "policy",
		Short: "Inspect deterministic policy artifacts",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown policy operation %q", args[0]))
			}
			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("policy requires an operation"))
		},
	}
	decision := &cobra.Command{
		Use:   "decision",
		Short: "Reproduce immutable policy decisions",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) > 0 {
				return cli.UsageError(fmt.Errorf("unknown policy decision operation %q", args[0]))
			}
			return nil
		},
		RunE: func(_ *cobra.Command, _ []string) error {
			return cli.UsageError(errors.New("policy decision requires an operation"))
		},
	}
	decision.AddCommand(
		newPolicyDecisionReproduceCommand(openRepository),
		newPolicyDecisionVerifyCommand(),
	)
	command.AddCommand(decision)
	addPolicyAdminCommands(command)
	return command
}

func newPolicyDecisionReproduceCommand(openRepository policyRepositoryOpener) *cobra.Command {
	options := &policyDecisionOptions{output: "summary"}
	command := &cobra.Command{
		Use:   "reproduce",
		Short: "Reproduce one tenant-scoped durable decision",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return validatePolicyDecisionReproduce(options)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executePolicyDecisionReproduce(command, options, openRepository)
		},
	}
	command.Flags().StringVar(&options.envFile, "env-file", config.DefaultEnvFile, "load local configuration from this dotenv file")
	command.Flags().StringVar(&options.tenantID, "tenant", "", "owning tenant identifier")
	command.Flags().StringVar(&options.decisionID, "id", "", "immutable decision identifier")
	command.Flags().StringVar(&options.output, "output", "summary", "output format: summary, json, or bundle")
	return command
}

func newPolicyDecisionVerifyCommand() *cobra.Command {
	options := &policyDecisionOptions{output: "summary"}
	command := &cobra.Command{
		Use:   "verify",
		Short: "Verify and reproduce a portable decision bundle offline",
		Args:  cli.UsageArgs(cobra.NoArgs),
		PreRunE: func(_ *cobra.Command, _ []string) error {
			return validatePolicyDecisionVerify(options)
		},
		RunE: func(command *cobra.Command, _ []string) error {
			return executePolicyDecisionVerify(command, options)
		},
	}
	command.Flags().StringVar(&options.bundleFile, "bundle-file", "", "canonical bundle file, or - for stdin")
	command.Flags().StringVar(&options.output, "output", "summary", "output format: summary, json, or bundle")
	return command
}

func validatePolicyDecisionReproduce(options *policyDecisionOptions) error {
	if options.tenantID == "" || options.decisionID == "" {
		return cli.UsageError(errors.New("policy decision reproduce requires --tenant and --id"))
	}
	if _, err := id.ParseTenant(options.tenantID); err != nil {
		return cli.UsageError(errors.New("policy decision tenant identifier is invalid"))
	}
	if _, err := id.ParseDecision(options.decisionID); err != nil {
		return cli.UsageError(errors.New("policy decision identifier is invalid"))
	}
	return validatePolicyOutput(options.output)
}

func validatePolicyDecisionVerify(options *policyDecisionOptions) error {
	if options.bundleFile == "" || strings.TrimSpace(options.bundleFile) != options.bundleFile {
		return cli.UsageError(errors.New("policy decision verify requires a valid --bundle-file"))
	}
	return validatePolicyOutput(options.output)
}

func validatePolicyOutput(output string) error {
	switch output {
	case "summary", "json", "bundle":
		return nil
	default:
		return cli.UsageError(errors.New("policy decision --output must be summary, json, or bundle"))
	}
}

func executePolicyDecisionReproduce(
	command *cobra.Command,
	options *policyDecisionOptions,
	openRepository policyRepositoryOpener,
) error {
	ctx := command.Context()
	if err := ctx.Err(); err != nil {
		return cli.RuntimeError("reproduce policy decision", err)
	}
	tenantID, _ := id.ParseTenant(options.tenantID)
	decisionID, _ := id.ParseDecision(options.decisionID)
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		return cli.UsageError(errors.New("policy decision tenant identifier is invalid"))
	}
	repository, closeRepository, err := openRepository(ctx, options.envFile)
	if err != nil {
		return cli.RuntimeError("open policy decision repository", err)
	}
	defer closeRepository()
	decision, err := repository.Find(ctx, scope, decisionID)
	if err != nil {
		return cli.RuntimeError("find policy decision", err)
	}
	bundle, report, err := policy.NewDecisionBundle(decision)
	if err != nil {
		return cli.RuntimeError("reproduce policy decision", err)
	}
	return writePolicyReproduction(command.OutOrStdout(), options.output, bundle, report)
}

func executePolicyDecisionVerify(command *cobra.Command, options *policyDecisionOptions) error {
	reader := command.InOrStdin()
	var file *os.File
	if options.bundleFile != "-" {
		var err error
		file, err = os.Open(options.bundleFile)
		if err != nil {
			return cli.RuntimeError("open policy decision bundle", err)
		}
		defer func() { _ = file.Close() }()
		reader = file
	}
	encoded, err := readBounded(reader, policy.MaximumBundleBytes)
	if err != nil {
		return cli.RuntimeError("read policy decision bundle", err)
	}
	bundle, report, err := policy.RestoreDecisionBundle(encoded)
	if err != nil {
		return cli.RuntimeError("verify policy decision bundle", err)
	}
	return writePolicyReproduction(command.OutOrStdout(), options.output, bundle, report)
}

func openPolicyRepository(ctx context.Context, envFile string) (policy.Repository, func(), error) {
	configuration, err := config.LoadAPI(envFile)
	if err != nil {
		return nil, nil, fmt.Errorf("load configuration: %w", err)
	}
	if configuration.DatabaseURL == "" {
		return nil, nil, errors.New("runtime database url is required")
	}
	pool, err := platformpostgres.Open(ctx, platformpostgres.Config{
		URL: configuration.DatabaseURL, Role: configuration.DatabaseRole,
		MaxConnections: 2, MinConnections: 0,
		MaxConnectionAge:    configuration.DatabaseMaxLifetime,
		MaxConnectionIdle:   configuration.DatabaseMaxIdleTime,
		HealthCheckInterval: configuration.DatabaseHealthInterval,
		ConnectTimeout:      configuration.DatabaseConnectTimeout,
	})
	if err != nil {
		return nil, nil, err
	}
	if err := pool.Check(ctx, migrations.LatestVersion); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("check database schema: %w", err)
	}
	repository, err := policypostgres.New(pool, nil)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return repository, pool.Close, nil
}

func readBounded(reader io.Reader, maximum int) ([]byte, error) {
	encoded, err := io.ReadAll(io.LimitReader(reader, int64(maximum)+1))
	if err != nil {
		return nil, err
	}
	if len(encoded) == 0 || len(encoded) > maximum {
		return nil, policy.ErrReproduction
	}
	return encoded, nil
}

func writePolicyReproduction(
	writer io.Writer,
	output string,
	bundle policy.DecisionBundle,
	report policy.ReproductionReport,
) error {
	var encoded []byte
	var err error
	switch output {
	case "bundle":
		encoded = bundle.Canonical()
	case "json":
		encoded, err = json.Marshal(report)
	case "summary":
		_, err = fmt.Fprintf(
			writer,
			"policy_decision_reproduced decision_id=%s verification_id=%s directive=%s outcome=%s decision_digest=%s bundle_digest=%s\n",
			report.DecisionID, report.VerificationID, report.Directive, report.Outcome,
			report.DecisionDigest, report.BundleDigest,
		)
		return outputError(err)
	default:
		return cli.UsageError(errors.New("policy decision --output must be summary, json, or bundle"))
	}
	if err != nil {
		return cli.RuntimeError("encode policy reproduction", err)
	}
	if _, err := writer.Write(encoded); err != nil {
		return cli.RuntimeError("write policy reproduction", err)
	}
	if output == "json" {
		if _, err := writer.Write([]byte("\n")); err != nil {
			return cli.RuntimeError("write policy reproduction", err)
		}
	}
	return nil
}

func outputError(err error) error {
	if err != nil {
		return cli.RuntimeError("write policy reproduction", err)
	}
	return nil
}
