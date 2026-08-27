// Package cli owns the shared Cobra process boundary for Idenqa binaries.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

const (
	exitRuntime = 1
	exitUsage   = 2
)

// RootOptions configures behavior shared by public Idenqa process commands.
type RootOptions struct {
	Use     string
	Short   string
	Version string
	RunE    func(command *cobra.Command, args []string) error
}

type commandError struct {
	code     int
	err      error
	reported bool
	usage    bool
}

func (err *commandError) Error() string { return err.err.Error() }
func (err *commandError) Unwrap() error { return err.err }

// NewRoot constructs a root command with shared help, version, completion,
// argument validation, and error-output conventions.
func NewRoot(options RootOptions) *cobra.Command {
	var showVersion bool
	root := &cobra.Command{
		Use:           options.Use,
		Short:         options.Short,
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, args []string) error {
			if showVersion {
				return writeVersion(command, options.Version)
			}
			if options.RunE != nil {
				return options.RunE(command, args)
			}

			return command.Help()
		},
	}
	root.Flags().BoolVar(&showVersion, "version", false, "print version information")
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return writeVersion(command, options.Version)
		},
	})

	return root
}

// Execute runs a command with Cobra-owned process arguments.
func Execute(ctx context.Context, root *cobra.Command, stdout, stderr io.Writer) int {
	return execute(ctx, root, nil, false, stdout, stderr)
}

// ExecuteArgs runs a command with explicitly supplied arguments for tests and
// other embedded callers.
func ExecuteArgs(
	ctx context.Context,
	root *cobra.Command,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	return execute(ctx, root, args, true, stdout, stderr)
}

// UsageArgs classifies Cobra positional-argument validation failures as usage
// errors.
func UsageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(command *cobra.Command, args []string) error {
		if err := validate(command, args); err != nil {
			return UsageError(err)
		}

		return nil
	}
}

// UsageError marks invalid command syntax or arguments.
func UsageError(err error) error {
	return &commandError{code: exitUsage, err: err, usage: true}
}

// RuntimeError marks an operational error that still needs to be presented.
func RuntimeError(operation string, err error) error {
	return &commandError{code: exitRuntime, err: fmt.Errorf("%s: %w", operation, err)}
}

// ReportedRuntimeError marks an operational error already presented through
// the process logger, preventing duplicate plain-text output.
func ReportedRuntimeError(err error) error {
	return &commandError{code: exitRuntime, err: err, reported: true}
}

func execute(
	ctx context.Context,
	root *cobra.Command,
	args []string,
	setArgs bool,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if setArgs {
		root.SetArgs(args)
	}
	root.SetOut(stdout)
	root.SetErr(stderr)

	err := root.ExecuteContext(ctx)
	if err == nil {
		return 0
	}

	code := exitUsage
	reported := false
	showUsage := true
	var exitErr *commandError
	if errors.As(err, &exitErr) {
		code = exitErr.code
		reported = exitErr.reported
		showUsage = exitErr.usage
	}
	if !reported {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
	}
	if showUsage {
		_, _ = fmt.Fprintln(stderr)
		_, _ = fmt.Fprint(stderr, root.UsageString())
	}

	return code
}

func writeVersion(command *cobra.Command, version string) error {
	if _, err := fmt.Fprintln(command.OutOrStdout(), version); err != nil {
		return RuntimeError("write version information", err)
	}

	return nil
}
