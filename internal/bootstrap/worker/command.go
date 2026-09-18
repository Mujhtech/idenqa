// Package worker composes the open-source background worker process.
package worker

import (
	"context"
	"errors"
	"io"
	"log/slog"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/logging"
	"github.com/Mujhtech/idenqa/internal/platform/task"
	"github.com/spf13/cobra"
)

// Run executes with explicit arguments for tests and embedded callers.
func Run(args []string, stdout, stderr io.Writer, info buildinfo.Info) int {
	return cli.ExecuteArgs(context.Background(), newCommand(info), args, stdout, stderr)
}

// RunContext executes with Cobra-owned process arguments and signal cancellation.
func RunContext(ctx context.Context, stdout, stderr io.Writer, info buildinfo.Info) int {
	return cli.Execute(ctx, newCommand(info), stdout, stderr)
}

func newCommand(info buildinfo.Info) *cobra.Command {
	var envFile string
	command := cli.NewRoot(cli.RootOptions{
		Use: "worker", Short: "Run the Idenqa Core background worker", Version: info.String(),
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
	fallback := slog.New(slog.NewJSONHandler(stderr, nil))
	configuration, err := config.LoadWorker(envFile)
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
	process, err := NewProcess(ctx, configuration, logger, info, task.NewRegistry())
	if err != nil {
		logger.ErrorContext(ctx, "worker startup failed", "error", err)
		return cli.ReportedRuntimeError(err)
	}
	logger.InfoContext(ctx, "worker started", "version", info.Version)
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
