// Package idenqa composes the public operational command-line interface.
package idenqa

import (
	"context"
	"io"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/spf13/cobra"
)

// Run executes the CLI and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer, info buildinfo.Info) int {
	return cli.ExecuteArgs(context.Background(), newRootCommand(info), args, stdout, stderr)
}

// RunContext executes the CLI with process arguments and cancellation for
// operational commands.
func RunContext(
	ctx context.Context,
	stdout io.Writer,
	stderr io.Writer,
	info buildinfo.Info,
) int {
	return cli.Execute(ctx, newRootCommand(info), stdout, stderr)
}

func newRootCommand(info buildinfo.Info) *cobra.Command {
	root := cli.NewRoot(cli.RootOptions{
		Use:     "idenqa",
		Short:   "Operate an Idenqa Core installation",
		Version: info.String(),
	})
	root.AddCommand(
		newMigrationCommand(), newTenantCommand(), newAPIKeyCommand(),
		newEvidenceKeyCommand(), newPolicyCommand(),
		newAuditCommand(), newRecoveryCommand(), newWorkCommand(),
	)

	return root
}
