package idenqa

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

type privacyOptions struct {
	reviewOptions
	aggregateID string
}

func newPrivacyCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "privacy",
		Short: "Inspect deletion workflows and retention resolutions through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newPrivacyDeletionStatusCommand())
	root.AddCommand(newPrivacyDeletionCommand())
	root.AddCommand(newPrivacyDeletionRetryCommand())
	root.AddCommand(newPrivacyRetentionCommand())
	return root
}

func newPrivacyDeletionStatusCommand() *cobra.Command {
	options := &privacyOptions{}
	command := &cobra.Command{
		Use:   "deletion-status",
		Short: "List tenant deletion workflows",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			if options.limit < 1 || options.limit > 100 {
				return cli.UsageError(errors.New("limit must be from 1 to 100"))
			}
			query := url.Values{"limit": {strconv.Itoa(options.limit)}}
			if options.cursor != "" {
				query.Set("cursor", options.cursor)
			}
			if options.aggregateID != "" {
				query.Set("aggregate_id", options.aggregateID)
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy",
				method:    http.MethodGet,
				path:      "/v1/deletions",
				query:     query,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().IntVar(&options.limit, "limit", 25, "page size from 1 to 100")
	command.Flags().StringVar(&options.cursor, "cursor", "", "opaque continuation cursor")
	command.Flags().StringVar(&options.aggregateID, "aggregate-id", "", "restrict the page to one exact aggregate")
	return command
}

func newPrivacyDeletionCommand() *cobra.Command {
	options := &privacyOptions{}
	command := &cobra.Command{
		Use:   "deletion <deletionID>",
		Short: "Get one deletion workflow status",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			deletion, err := id.ParseDeletion(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid deletion id is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy",
				method:    http.MethodGet,
				path:      "/v1/deletions/" + deletion.String(),
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newPrivacyDeletionRetryCommand() *cobra.Command {
	options := &privacyOptions{}
	command := &cobra.Command{
		Use:   "deletion-retry <deletionID>",
		Short: "Retry one deletion workflow idempotently",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			deletion, err := id.ParseDeletion(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid deletion id is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy",
				method:    http.MethodPost,
				path:      "/v1/deletions/" + deletion.String() + "/run",
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newPrivacyRetentionCommand() *cobra.Command {
	options := &privacyOptions{}
	command := &cobra.Command{
		Use:   "retention",
		Short: "Resolve typed retention for one aggregate",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			if options.aggregateID == "" {
				return cli.UsageError(errors.New("aggregate-id is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "privacy",
				method:    http.MethodGet,
				path:      "/v1/retention/resolutions",
				query:     url.Values{"aggregate_id": {options.aggregateID}},
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.aggregateID, "aggregate-id", "", "exact aggregate whose retention is resolved")
	return command
}
