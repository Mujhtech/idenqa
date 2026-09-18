package modelrunner

import (
	"encoding/json"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/config"
	"github.com/spf13/cobra"
)

func newGateCommand() *cobra.Command {
	var reportPath, gatePath string
	command := &cobra.Command{Use: "check-evaluation", Short: "Check a pinned aggregate report against experimental limits", Args: cobra.NoArgs, RunE: func(command *cobra.Command, _ []string) error {
		if reportPath == "" || gatePath == "" {
			return fmt.Errorf("--report and --gate are required")
		}
		var report EvaluationReport
		var gate EvaluationGate
		if err := config.ReadClosedFile(reportPath, &report, 4<<20); err != nil {
			return err
		}
		if err := config.ReadClosedFile(gatePath, &gate, 64<<10); err != nil {
			return err
		}
		result, err := CheckGate(report, gate)
		if err != nil {
			return err
		}
		if err := json.NewEncoder(command.OutOrStdout()).Encode(result); err != nil {
			return err
		}
		if !result.Passed {
			return fmt.Errorf("experimental evaluation gate failed")
		}
		return nil
	}}
	command.Flags().StringVar(&reportPath, "report", "", "aggregate evaluation report path")
	command.Flags().StringVar(&gatePath, "gate", "", "pinned experimental gate path")
	return command
}
