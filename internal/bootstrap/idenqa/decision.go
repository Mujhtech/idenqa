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

type decisionOptions struct {
	reviewOptions
	before         string
	decisionID     string
	idempotencyKey string
	limit          int
}

func newDecisionCommand() *cobra.Command {
	root := &cobra.Command{Use: "decision", Short: "Inspect immutable decisions through the public API", Args: cli.UsageArgs(cobra.NoArgs)}
	root.AddCommand(newDecisionGetCommand())
	root.AddCommand(newDecisionHistoryCommand())
	root.AddCommand(newDecisionReconsiderCommand())
	return root
}

func newDecisionReconsiderCommand() *cobra.Command {
	options := &decisionOptions{}
	command := &cobra.Command{Use: "reconsider <verificationID>", Short: "Open an independent correction workflow for a decision", Args: cli.UsageArgs(cobra.ExactArgs(1)), RunE: func(command *cobra.Command, args []string) error {
		verificationID, err := id.ParseVerification(args[0])
		if err != nil {
			return cli.UsageError(errors.New("a valid verification id is required"))
		}
		decisionID, err := id.ParseDecision(options.decisionID)
		if err != nil {
			return cli.UsageError(errors.New("a valid decision-id is required"))
		}
		options.reviewOptions.idempotencyKey = options.idempotencyKey
		return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "decision reconsideration", method: http.MethodPost, path: "/v1/verifications/" + verificationID.String() + "/reconsiderations", body: map[string]string{"decision_id": decisionID.String()}, idempotency: true})
	}}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.decisionID, "decision-id", "", "decision to reconsider")
	command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key")
	return command
}

func newDecisionGetCommand() *cobra.Command {
	options := &decisionOptions{}
	command := &cobra.Command{
		Use: "get <decisionID>", Short: "Get one immutable decision", Args: cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			identifier, err := id.ParseDecision(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid decision id is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "decision", method: http.MethodGet, path: "/v1/decisions/" + identifier.String()})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newDecisionHistoryCommand() *cobra.Command {
	options := &decisionOptions{limit: 25}
	command := &cobra.Command{
		Use: "history <verificationID>", Short: "List one verification's immutable decision lineage", Args: cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			verificationID, err := id.ParseVerification(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid verification id is required"))
			}
			if options.limit < 1 || options.limit > 100 {
				return cli.UsageError(errors.New("limit must be from 1 to 100"))
			}
			query := url.Values{"limit": {strconv.Itoa(options.limit)}}
			if options.before != "" {
				before, parseErr := id.ParseDecision(options.before)
				if parseErr != nil {
					return cli.UsageError(errors.New("before must be a valid decision id"))
				}
				query.Set("before", before.String())
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "decision", method: http.MethodGet, path: "/v1/verifications/" + verificationID.String() + "/decisions", query: query})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.before, "before", "", "exclusive decision cursor")
	command.Flags().IntVar(&options.limit, "limit", 25, "page size from 1 to 100")
	return command
}
