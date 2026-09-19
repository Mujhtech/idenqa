package idenqa

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/spf13/cobra"
)

type fraudOptions struct {
	reviewOptions
}

func newFraudCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "fraud",
		Short: "Configure tenant fraud controls and inspect risk receipts through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newFraudConfigGetCommand())
	root.AddCommand(newFraudConfigPutCommand())
	root.AddCommand(newFraudRevisionCommand())
	root.AddCommand(newFraudInputsCommand())
	root.AddCommand(newFraudProposalCommand())
	root.AddCommand(newFraudProposalGetCommand())
	root.AddCommand(newFraudReceiptCommand())
	return root
}

func newFraudConfigGetCommand() *cobra.Command {
	options := &fraudOptions{}
	command := &cobra.Command{
		Use:   "config-get",
		Short: "Fraud configuration get",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "fraud", method: http.MethodGet, path: "/v1/fraud/configuration"})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newFraudConfigPutCommand() *cobra.Command {
	options := &fraudOptions{}
	command := &cobra.Command{
		Use:   "config-put",
		Short: "Fraud configuration put",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := readReviewBody(command, &options.reviewOptions, "fraud")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation:   "fraud",
				method:      http.MethodPut,
				path:        "/v1/fraud/configuration",
				body:        body,
				idempotency: true,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON request body file, or - for stdin")
	command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	return command
}

func newFraudRevisionCommand() *cobra.Command {
	options := &fraudOptions{}
	command := &cobra.Command{
		Use:   "revision <version>",
		Short: "Fraud configuration revision get",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			version, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || version < 1 {
				return cli.UsageError(errors.New("a positive fraud configuration version is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "fraud",
				method:    http.MethodGet,
				path:      "/v1/fraud/configuration/revisions/" + strconv.FormatInt(version, 10),
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newFraudInputsCommand() *cobra.Command {
	options := &fraudOptions{}
	command := &cobra.Command{
		Use:   "inputs",
		Short: "Fraud input ingest",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := readReviewBody(command, &options.reviewOptions, "fraud")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation:   "fraud",
				method:      http.MethodPost,
				path:        "/v1/fraud/inputs",
				body:        body,
				idempotency: true,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON request body file, or - for stdin")
	command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	return command
}

func newFraudProposalCommand() *cobra.Command {
	options := &fraudOptions{}
	command := &cobra.Command{
		Use:   "proposal",
		Short: "Fraud proposal create",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := readReviewBody(command, &options.reviewOptions, "fraud")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation:   "fraud",
				method:      http.MethodPost,
				path:        "/v1/fraud/proposals",
				body:        body,
				idempotency: true,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON request body file, or - for stdin")
	command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	return command
}

func newFraudProposalGetCommand() *cobra.Command {
	options := &fraudOptions{}
	command := &cobra.Command{
		Use:   "proposal-get <digest>",
		Short: "Fraud proposal get",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if !isHexDigest(args[0]) {
				return cli.UsageError(errors.New("a 64-character lowercase hex digest is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "fraud", method: http.MethodGet, path: "/v1/fraud/proposals/" + args[0]})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newFraudReceiptCommand() *cobra.Command {
	options := &fraudOptions{}
	command := &cobra.Command{
		Use:   "receipt <digest>",
		Short: "Fraud receipt get",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if !isHexDigest(args[0]) {
				return cli.UsageError(errors.New("a 64-character lowercase hex digest is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "fraud", method: http.MethodGet, path: "/v1/fraud/receipts/" + args[0]})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}
