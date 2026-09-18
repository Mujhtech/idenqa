package adapterrunner

import (
	"context"
	"io"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/spf13/cobra"
)

// RunContext executes the standalone isolated provider workload.
func RunContext(ctx context.Context, stdout, stderr io.Writer, info buildinfo.Info) int {
	var path string
	command := cli.NewRoot(cli.RootOptions{Use: "adapter-runner", Short: "Run an isolated tenant provider adapter", Version: info.String(), RunE: func(command *cobra.Command, _ []string) error {
		var settings Settings
		if err := config.ReadClosedFile(path, &settings, 64<<10); err != nil {
			return cli.RuntimeError("provider runner configuration", err)
		}
		process, err := NewProcess(command.Context(), settings)
		if err != nil {
			return cli.RuntimeError("provider runner startup", err)
		}
		defer process.Close()
		if err := process.Run(command.Context()); err != nil {
			return cli.RuntimeError("provider runner stopped", err)
		}
		return nil
	}})
	command.Flags().StringVar(&path, "config", "", "path to the mounted runner configuration")
	return cli.Execute(ctx, command, stdout, stderr)
}
