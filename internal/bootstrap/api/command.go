package api

import (
	"context"
	"io"
	"log/slog"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/Mujhtech/idenqa/internal/platform/health"
	"github.com/Mujhtech/idenqa/internal/platform/logging"
	"github.com/spf13/cobra"
)

// Run executes the API command with explicit arguments for tests and embedded
// callers.
func Run(args []string, stdout, stderr io.Writer, info buildinfo.Info) int {
	return cli.ExecuteArgs(context.Background(), newCommand(info), args, stdout, stderr)
}

// RunContext executes the API command with Cobra-owned process arguments and
// signal cancellation.
func RunContext(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	info buildinfo.Info,
) int {
	return cli.Execute(ctx, newCommand(info), stdout, stderr)
}

func newCommand(info buildinfo.Info) *cobra.Command {
	var envFile string
	command := cli.NewRoot(cli.RootOptions{
		Use:     "api",
		Short:   "Run the Idenqa Core HTTP API",
		Version: info.String(),
		RunE: func(command *cobra.Command, _ []string) error {
			return runProcess(command, envFile, info)
		},
	})
	command.Flags().StringVar(&envFile, "env-file", "", "load local configuration from this dotenv file")

	return command
}

func runProcess(command *cobra.Command, envFile string, info buildinfo.Info) error {
	ctx := command.Context()
	stderr := command.ErrOrStderr()
	fallbackLogger := slog.New(slog.NewJSONHandler(stderr, nil))

	configuration, err := config.LoadAPI(envFile)
	if err != nil {
		fallbackLogger.ErrorContext(ctx, "api configuration failed", "error", err)

		return cli.ReportedRuntimeError(err)
	}

	logger, err := logging.New(stderr, configuration.LogLevel, configuration.LogFormat)
	if err != nil {
		fallbackLogger.ErrorContext(ctx, "api logging configuration failed", "error", err)

		return cli.ReportedRuntimeError(err)
	}

	state := &health.State{}
	process, err := NewProcess(ctx, configuration, logger, state, info)
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
