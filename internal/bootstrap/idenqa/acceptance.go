package idenqa

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/Mujhtech/idenqa/internal/acceptance"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/spf13/cobra"
)

type acceptanceOptions struct {
	file          string
	requirePassed bool
}

func newAcceptanceCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "acceptance",
		Short: "Validate content-free production-acceptance records",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newAcceptanceValidateCommand())
	return root
}

func newAcceptanceValidateCommand() *cobra.Command {
	options := &acceptanceOptions{}
	command := &cobra.Command{
		Use:   "validate",
		Short: "Validate and digest a provider or model acceptance record",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runAcceptanceValidate(command, *options)
		},
	}
	command.Flags().StringVar(&options.file, "file", "", "path to the JSON acceptance record")
	command.Flags().BoolVar(&options.requirePassed, "require-passed", false, "fail unless the record is fully accepted")
	_ = command.MarkFlagRequired("file")
	return command
}

func runAcceptanceValidate(command *cobra.Command, options acceptanceOptions) error {
	encoded, err := readBoundedAcceptanceFile(options.file)
	if err != nil {
		return err
	}
	record, err := acceptance.DecodeProvider(encoded)
	if err != nil {
		return fmt.Errorf("validate provider acceptance record: %w", err)
	}
	report, err := record.Receipt()
	if err != nil {
		return fmt.Errorf("digest provider acceptance record: %w", err)
	}
	if options.requirePassed && !record.Passed() {
		return acceptance.ErrIncomplete
	}
	encoder := json.NewEncoder(command.OutOrStdout())
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(report); err != nil {
		return fmt.Errorf("write acceptance report: %w", err)
	}
	return nil
}

func readBoundedAcceptanceFile(path string) ([]byte, error) {
	if path == "" {
		return nil, cli.UsageError(errors.New("acceptance record file is required"))
	}
	// #nosec G304 -- the operator explicitly supplies the bounded record path.
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open acceptance record: %w", err)
	}
	defer func() { _ = file.Close() }()
	encoded, err := io.ReadAll(io.LimitReader(file, acceptance.MaximumRecordBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read acceptance record: %w", err)
	}
	if len(encoded) > acceptance.MaximumRecordBytes {
		return nil, acceptance.ErrInvalid
	}
	return encoded, nil
}
