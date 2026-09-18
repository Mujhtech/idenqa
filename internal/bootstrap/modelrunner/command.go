package modelrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/Mujhtech/idenqa/adapters/models/onnx"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/spf13/cobra"
)

// RunContext executes the standalone isolated model workload.
func RunContext(ctx context.Context, stdout, stderr io.Writer, info buildinfo.Info) int {
	return cli.Execute(ctx, newCommand(info), stdout, stderr)
}

func newCommand(info buildinfo.Info) *cobra.Command {
	var path, python, dataset, inventory, candidate, thresholdRevision string
	var inspect, inspectConfiguration bool
	command := cli.NewRoot(cli.RootOptions{Use: "model-runner", Short: "Run an isolated tenant model adapter", Version: info.String(), RunE: func(command *cobra.Command, _ []string) error {
		if inventory != "" {
			value, err := ImportDataset(command.Context(), inventory)
			if err != nil {
				return cli.RuntimeError("import PAD dataset", err)
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(value)
		}
		if inspect {
			value, err := onnx.RuntimeDigest(command.Context(), python)
			if err != nil {
				return cli.RuntimeError("inspect model runtime", err)
			}
			_, err = fmt.Fprintln(command.OutOrStdout(), value)
			return err
		}
		var settings Settings
		if err := config.ReadClosedFile(path, &settings, 64<<10); err != nil {
			return cli.RuntimeError("model runner configuration", err)
		}
		if inspectConfiguration {
			_, err := fmt.Fprintln(command.OutOrStdout(), onnx.ConfigurationDigest(settings.Model))
			return err
		}
		if dataset != "" {
			if thresholdRevision != "" {
				if err := CheckThresholdRevision(settings, dataset, thresholdRevision); err != nil {
					return cli.RuntimeError("validate threshold revision", err)
				}
			}
			if candidate != "" {
				var other Settings
				if err := config.ReadClosedFile(candidate, &other, 64<<10); err != nil {
					return cli.RuntimeError("candidate configuration", err)
				}
				if thresholdRevision != "" {
					if err := CheckThresholdRevision(other, dataset, thresholdRevision); err != nil {
						return cli.RuntimeError("validate candidate threshold revision", err)
					}
				}
				report, err := Compare(command.Context(), settings, other, dataset)
				if err != nil {
					return cli.RuntimeError("compare PAD configurations", err)
				}
				return json.NewEncoder(command.OutOrStdout()).Encode(report)
			}
			report, err := Evaluate(command.Context(), settings, dataset)
			if err != nil {
				return cli.RuntimeError("evaluate PAD dataset", err)
			}
			return json.NewEncoder(command.OutOrStdout()).Encode(report)
		}
		process, err := NewProcess(command.Context(), settings)
		if err != nil {
			return cli.RuntimeError("model runner startup", err)
		}
		defer process.Close()
		if err := process.Run(command.Context()); err != nil {
			return cli.RuntimeError("model runner stopped", err)
		}
		return nil
	}})
	command.Flags().StringVar(&thresholdRevision, "threshold-revision", "", "exported immutable registry threshold revision for this exact model configuration")
	command.Flags().StringVar(&inventory, "import-dataset", "", "path to a labelled local inventory; print a hash-pinned manifest")
	command.Flags().StringVar(&candidate, "compare-config", "", "candidate configuration to compare with --config on --evaluate-dataset")
	command.Flags().StringVar(&dataset, "evaluate-dataset", "", "path to an approved local PAD dataset manifest; print aggregate report")
	command.Flags().BoolVar(&inspectConfiguration, "configuration-digest", false, "print the model configuration digest without starting the runner")
	command.Flags().BoolVar(&inspect, "runtime-digest", false, "print the installed native runtime digest")
	command.Flags().StringVar(&python, "python", "", "absolute path to the isolated Python executable")
	command.Flags().StringVar(&path, "config", "", "path to the mounted runner configuration")
	command.MarkFlagsMutuallyExclusive("import-dataset", "evaluate-dataset", "runtime-digest", "configuration-digest")
	command.MarkFlagsMutuallyExclusive("import-dataset", "config")
	command.MarkFlagsMutuallyExclusive("compare-config", "runtime-digest", "configuration-digest", "import-dataset")
	command.PreRunE = func(command *cobra.Command, _ []string) error {
		if command.Flags().Changed("threshold-revision") && (thresholdRevision == "" || dataset == "" || path == "") {
			return fmt.Errorf("--threshold-revision requires --config and --evaluate-dataset")
		}
		if command.Flags().Changed("compare-config") && (candidate == "" || dataset == "" || path == "") {
			return fmt.Errorf("--compare-config requires --config and --evaluate-dataset")
		}
		if command.Flags().Changed("import-dataset") && inventory == "" {
			return fmt.Errorf("--import-dataset requires a path")
		}
		if command.Flags().Changed("evaluate-dataset") && dataset == "" {
			return fmt.Errorf("--evaluate-dataset requires a path")
		}
		return nil
	}
	command.AddCommand(newGateCommand(), newDriftCommand())
	return command
}
