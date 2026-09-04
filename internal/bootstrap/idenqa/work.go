package idenqa

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"
)

type workOptions struct {
	envFile, state, taskID string
	limit                  uint32
	confirmed              bool
}

func newWorkCommand() *cobra.Command {
	options := &workOptions{}
	command := &cobra.Command{Use: "work", Short: "Inspect and recover durable background work", Args: cli.UsageArgs(cobra.NoArgs), RunE: func(*cobra.Command, []string) error { return cli.UsageError(errors.New("work requires an operation")) }}
	command.PersistentFlags().StringVar(&options.envFile, "env-file", "", "load local configuration from this dotenv file")
	list := &cobra.Command{Use: "list", Short: "List payload-free work metadata in one state", Args: cli.UsageArgs(cobra.NoArgs), RunE: func(command *cobra.Command, _ []string) error { return executeWorkList(command, options) }}
	list.Flags().StringVar(&options.state, "state", "quarantined", "exact Headgate state to inspect")
	list.Flags().Uint32Var(&options.limit, "limit", 100, "maximum records (1-1000)")
	retry := &cobra.Command{Use: "retry", Short: "Retry one exact terminal task", Args: cli.UsageArgs(cobra.NoArgs), PreRunE: func(*cobra.Command, []string) error {
		if options.taskID == "" || !options.confirmed {
			return cli.UsageError(errors.New("work retry requires --task-id and --confirm"))
		}
		return nil
	}, RunE: func(command *cobra.Command, _ []string) error { return executeWorkRetry(command, options) }}
	retry.Flags().StringVar(&options.taskID, "task-id", "", "exact durable task identifier")
	retry.Flags().BoolVar(&options.confirmed, "confirm", false, "confirm retry of the exact task")
	command.AddCommand(list, retry)
	return command
}

func openWorkAdapter(ctx context.Context, options *workOptions) (*taskheadgate.Adapter, *pgxpool.Pool, error) {
	configuration, err := config.LoadAPI(options.envFile)
	if err != nil {
		return nil, nil, err
	}
	poolConfig, err := pgxpool.ParseConfig(configuration.OperationalDatabaseURL())
	if err != nil {
		return nil, nil, errors.New("invalid operational database configuration")
	}
	connectContext, cancel := context.WithTimeout(ctx, configuration.DatabaseConnectTimeout)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(connectContext, poolConfig)
	if err != nil {
		return nil, nil, errors.New("open operational database")
	}
	adapterConfig := taskheadgate.DefaultConfig(configuration.HeadgateInstallationID)
	adapterConfig.Schema = configuration.HeadgateSchema
	adapter, err := taskheadgate.NewPostgres(pool, adapterConfig)
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	return adapter, pool, nil
}

func executeWorkList(command *cobra.Command, options *workOptions) error {
	if options.limit == 0 || options.limit > 1000 || options.state == "" {
		return cli.UsageError(errors.New("work list requires a state and limit from 1 to 1000"))
	}
	adapter, pool, err := openWorkAdapter(command.Context(), options)
	if err != nil {
		return cli.RuntimeError("open work inspector", err)
	}
	defer pool.Close()
	jobs, cursor, err := adapter.ListJobs(command.Context(), options.state, options.limit)
	if err != nil {
		return cli.RuntimeError("list work", err)
	}
	result := struct {
		Jobs       []taskheadgate.JobSummary `json:"jobs"`
		NextCursor string                    `json:"next_cursor,omitempty"`
	}{jobs, cursor}
	encoded, err := json.Marshal(result)
	if err != nil {
		return cli.RuntimeError("encode work report", err)
	}
	if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\n", encoded); err != nil {
		return cli.RuntimeError("write work report", err)
	}
	return nil
}

func executeWorkRetry(command *cobra.Command, options *workOptions) error {
	adapter, pool, err := openWorkAdapter(command.Context(), options)
	if err != nil {
		return cli.RuntimeError("open work operator", err)
	}
	defer pool.Close()
	if err := adapter.RetryJob(command.Context(), options.taskID); err != nil {
		return cli.RuntimeError("retry work", err)
	}
	_, err = fmt.Fprintf(command.OutOrStdout(), "task_id=%s state=scheduled\n", options.taskID)
	if err != nil {
		return cli.RuntimeError("write work retry result", err)
	}
	return nil
}
