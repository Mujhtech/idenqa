package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"

	s3objects "github.com/Mujhtech/idenqa/adapters/objectstore/s3"
	distributionconfig "github.com/Mujhtech/idenqa/distributions/s3/internal/config"
	bootstrapworker "github.com/Mujhtech/idenqa/internal/bootstrap/worker"
	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/logging"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/spf13/cobra"
)

// RunWorker executes the S3-backed worker command with explicit arguments.
func RunWorker(args []string, stdout, stderr io.Writer, info buildinfo.Info) int {
	return cli.ExecuteArgs(context.Background(), newWorkerCommand(info), args, stdout, stderr)
}

// RunWorkerContext executes the S3-backed worker command with process
// cancellation supplied by the entry point.
func RunWorkerContext(ctx context.Context, stdout, stderr io.Writer, info buildinfo.Info) int {
	return cli.Execute(ctx, newWorkerCommand(info), stdout, stderr)
}

func newWorkerCommand(info buildinfo.Info) *cobra.Command {
	var envFile string
	command := cli.NewRoot(cli.RootOptions{
		Use:     "worker",
		Short:   "Run the S3-backed Idenqa Core background worker",
		Version: info.String(),
		RunE: func(command *cobra.Command, _ []string) error {
			return runWorkerProcess(command, envFile, info)
		},
	})
	command.Flags().StringVar(&envFile, "env-file", config.DefaultEnvFile, "load local configuration from this dotenv file")

	return command
}

func runWorkerProcess(command *cobra.Command, envFile string, info buildinfo.Info) error {
	ctx := command.Context()
	stderr := command.ErrOrStderr()
	fallback := slog.New(slog.NewJSONHandler(stderr, nil))
	configuration, err := distributionconfig.LoadWorker(envFile)
	if err != nil {
		fallback.ErrorContext(ctx, "worker configuration failed", "error", err)

		return cli.ReportedRuntimeError(err)
	}
	logger, err := logging.New(stderr, configuration.LogLevel, configuration.LogFormat)
	if err != nil {
		fallback.ErrorContext(ctx, "worker logging configuration failed", "error", err)

		return cli.ReportedRuntimeError(err)
	}
	slog.SetDefault(logger)
	infrastructure, err := composeWorkerEvidence(ctx, configuration)
	if err != nil {
		logger.ErrorContext(ctx, "worker evidence composition failed", "error", err)

		return cli.ReportedRuntimeError(err)
	}
	process, err := bootstrapworker.NewProcessWithEvidence(
		ctx, configuration.Worker, logger, info, task.NewRegistry(), infrastructure,
	)
	if err != nil {
		logger.ErrorContext(ctx, "worker startup failed", "error", err)

		return cli.ReportedRuntimeError(err)
	}
	logger.InfoContext(ctx, "worker started", "version", info.Version, "object_store", "s3")
	runErr := process.Run(ctx)
	closeErr := process.Close(context.WithoutCancel(ctx))
	if runErr != nil || closeErr != nil {
		err := errors.Join(runErr, closeErr)
		logger.ErrorContext(context.WithoutCancel(ctx), "worker stopped with error", "error", err)

		return cli.ReportedRuntimeError(err)
	}
	logger.InfoContext(context.WithoutCancel(ctx), "worker stopped")

	return nil
}

func composeWorkerEvidence(
	ctx context.Context,
	configuration distributionconfig.WorkerConfiguration,
) (bootstrapworker.EvidenceInfrastructure, error) {
	policy, err := configuration.EvidenceUploadPolicy()
	if err != nil {
		return bootstrapworker.EvidenceInfrastructure{}, err
	}
	objects, err := s3objects.Open(ctx, configuration.ObjectStoreConfig(policy.MaximumBytes()))
	if err != nil {
		return bootstrapworker.EvidenceInfrastructure{}, errors.New("open S3 worker evidence object store")
	}
	infrastructure, err := bootstrapworker.NewEvidenceInfrastructure(objects, workerEvidenceLifecycle{})
	if err != nil {
		return bootstrapworker.EvidenceInfrastructure{}, errors.New("compose S3 worker evidence infrastructure")
	}

	return infrastructure, nil
}

// The AWS S3 client owns no closeable resource; its HTTP transport is shared
// and managed by the SDK. The lifecycle value still makes process ownership
// explicit at the provider-neutral worker boundary.
type workerEvidenceLifecycle struct{}

func (workerEvidenceLifecycle) Shutdown(context.Context) error { return nil }
