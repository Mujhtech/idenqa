// Package bootstrap composes and runs the S3-backed Idenqa API distribution.
package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"

	s3objects "github.com/Mujhtech/idenqa/adapters/objectstore/s3"
	distributionconfig "github.com/Mujhtech/idenqa/distributions/s3/internal/config"
	bootstrapapi "github.com/Mujhtech/idenqa/internal/bootstrap/api"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/health"
	"github.com/Mujhtech/idenqa/internal/platform/kms/keys"
	"github.com/Mujhtech/idenqa/internal/platform/logging"
	"github.com/spf13/cobra"
)

// Run executes the distribution command with explicit arguments.
func Run(args []string, stdout, stderr io.Writer, info buildinfo.Info) int {
	return cli.ExecuteArgs(context.Background(), newCommand(info), args, stdout, stderr)
}

// RunContext executes the distribution command with Cobra-owned arguments and
// process cancellation supplied by the entry point.
func RunContext(ctx context.Context, stdout, stderr io.Writer, info buildinfo.Info) int {
	return cli.Execute(ctx, newCommand(info), stdout, stderr)
}

func newCommand(info buildinfo.Info) *cobra.Command {
	var envFile string
	command := cli.NewRoot(cli.RootOptions{
		Use:     "api",
		Short:   "Run the S3-backed Idenqa Core HTTP API",
		Version: info.String(),
		RunE: func(command *cobra.Command, _ []string) error {
			return runProcess(command, envFile, info)
		},
	})
	command.Flags().StringVar(&envFile, "env-file", config.DefaultEnvFile, "load local configuration from this dotenv file")

	return command
}

func runProcess(command *cobra.Command, envFile string, info buildinfo.Info) error {
	ctx := command.Context()
	stderr := command.ErrOrStderr()
	fallbackLogger := slog.New(slog.NewJSONHandler(stderr, nil))

	configuration, err := distributionconfig.Load(envFile)
	if err != nil {
		fallbackLogger.ErrorContext(ctx, "api configuration failed", "error", err)

		return cli.ReportedRuntimeError(err)
	}
	logger, err := logging.New(stderr, configuration.LogLevel, configuration.LogFormat)
	if err != nil {
		fallbackLogger.ErrorContext(ctx, "api logging configuration failed", "error", err)

		return cli.ReportedRuntimeError(err)
	}
	infrastructure, err := composeEvidence(ctx, configuration)
	if err != nil {
		logger.ErrorContext(ctx, "api evidence composition failed", "error", err)

		return cli.ReportedRuntimeError(err)
	}
	state := &health.State{}
	process, err := bootstrapapi.NewProcessWithEvidence(
		ctx, configuration.API, logger, state, info, infrastructure,
	)
	if err != nil {
		logger.ErrorContext(ctx, "api startup composition failed", "error", err)

		return cli.ReportedRuntimeError(err)
	}
	if err := process.Run(ctx); err != nil {
		logger.ErrorContext(context.WithoutCancel(ctx), "api stopped with error", "error", err)

		return cli.ReportedRuntimeError(err)
	}

	return nil
}

func composeEvidence(
	ctx context.Context,
	configuration distributionconfig.Configuration,
) (bootstrapapi.EvidenceInfrastructure, error) {
	policy, err := configuration.EvidenceUploadPolicy()
	if err != nil {
		return bootstrapapi.EvidenceInfrastructure{}, err
	}
	keyring, err := keys.Open(ctx, keys.Options{
		Provider:             configuration.KMSProvider,
		LocalKeyringFile:     configuration.EvidenceLocalKeyringFile,
		AWSKeyID:             configuration.KMSAWSKeyID,
		AWSRegion:            configuration.KMSAWSRegion,
		AWSMaxPlaintextBytes: configuration.KMSAWSMaxPlaintextBytes,
	})
	if err != nil {
		return bootstrapapi.EvidenceInfrastructure{}, err
	}
	objects, err := s3objects.Open(ctx, configuration.ObjectStoreConfig(policy.MaximumBytes()))
	if err != nil {
		_ = keyring.Close()

		return bootstrapapi.EvidenceInfrastructure{}, errors.New("open S3 evidence object store")
	}
	lifecycle := &evidenceLifecycle{keys: keyring}
	infrastructure, err := bootstrapapi.NewEvidenceInfrastructure(objects, keyring, lifecycle)
	if err != nil {
		_ = lifecycle.Shutdown(context.Background())

		return bootstrapapi.EvidenceInfrastructure{}, errors.New("compose S3 evidence infrastructure")
	}

	return infrastructure, nil
}

type evidenceLifecycle struct {
	keys interface{ Close() error }
}

func (lifecycle *evidenceLifecycle) Shutdown(ctx context.Context) error {
	_ = ctx
	return lifecycle.keys.Close()
}
