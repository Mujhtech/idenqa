package idenqa

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

type assuranceOptions struct {
	reviewOptions
	after string
}

func newAssuranceCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "assurance",
		Short: "Inspect and publish assurance capabilities and profiles through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newAssuranceCapabilitiesCommand())
	root.AddCommand(newAssuranceProfilesCommand())
	root.AddCommand(newAssuranceProfileCommand())
	root.AddCommand(newAssurancePublishCommand())
	root.AddCommand(newAssuranceValidateCommand())
	root.AddCommand(newAssurancePolicyGetCommand())
	root.AddCommand(newAssurancePolicyAssignCommand())
	root.AddCommand(newAssuranceVerificationCommand())
	return root
}

func newAssuranceCapabilitiesCommand() *cobra.Command {
	options := &assuranceOptions{}
	command := &cobra.Command{
		Use:   "capabilities",
		Short: "Assurance capabilities",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "assurance", method: http.MethodGet, path: "/v1/assurance-capabilities"})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newAssuranceProfilesCommand() *cobra.Command {
	options := &assuranceOptions{reviewOptions: reviewOptions{limit: 50}}
	command := &cobra.Command{
		Use:   "profiles",
		Short: "Assurance profiles list",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			if options.limit < 1 || options.limit > 100 {
				return cli.UsageError(errors.New("limit must be from 1 to 100"))
			}
			query := url.Values{"limit": {strconv.Itoa(options.limit)}}
			if options.after != "" {
				query.Set("after", options.after)
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "assurance", method: http.MethodGet, path: "/v1/assurance-profiles", query: query})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.after, "after", "", "opaque continuation cursor")
	command.Flags().IntVar(&options.limit, "limit", 50, "page size from 1 to 100")
	return command
}

func newAssuranceProfileCommand() *cobra.Command {
	options := &assuranceOptions{}
	command := &cobra.Command{
		Use:   "profile <name> <revision>",
		Short: "Assurance profile revision get",
		Args:  cli.UsageArgs(cobra.ExactArgs(2)),
		RunE: func(command *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			if name == "" || len(name) > 64 {
				return cli.UsageError(errors.New("a bounded assurance profile name is required"))
			}
			revision, err := strconv.ParseUint(args[1], 10, 32)
			if err != nil || revision < 1 {
				return cli.UsageError(errors.New("a positive assurance profile revision is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "assurance",
				method:    http.MethodGet,
				path:      "/v1/assurance-profiles/" + name + "/revisions/" + strconv.FormatUint(revision, 10),
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newAssurancePublishCommand() *cobra.Command {
	options := &assuranceOptions{}
	command := &cobra.Command{
		Use:   "publish",
		Short: "Assurance profile publish",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := readReviewBody(command, &options.reviewOptions, "assurance")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation:   "assurance",
				method:      http.MethodPost,
				path:        "/v1/assurance-profiles",
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

func newAssuranceValidateCommand() *cobra.Command {
	options := &assuranceOptions{}
	command := &cobra.Command{
		Use:   "validate",
		Short: "Assurance profile validate",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := readReviewBody(command, &options.reviewOptions, "assurance")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "assurance",
				method:    http.MethodPost,
				path:      "/v1/assurance-profiles/validate",
				body:      body,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON request body file, or - for stdin")
	return command
}

func newAssurancePolicyGetCommand() *cobra.Command {
	options := &assuranceOptions{}
	command := &cobra.Command{
		Use:   "policy-get <policyID>",
		Short: "Policy assurance selection get",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			policyID, err := id.ParsePolicy(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid policy id is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "assurance", method: http.MethodGet, path: "/v1/policies/" + policyID.String() + "/assurance"})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newAssurancePolicyAssignCommand() *cobra.Command {
	options := &assuranceOptions{}
	command := &cobra.Command{
		Use:   "policy-assign <policyID>",
		Short: "Policy assurance selection assign",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			policyID, err := id.ParsePolicy(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid policy id is required"))
			}
			body, err := readReviewBody(command, &options.reviewOptions, "assurance")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation:   "assurance",
				method:      http.MethodPut,
				path:        "/v1/policies/" + policyID.String() + "/assurance",
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

func newAssuranceVerificationCommand() *cobra.Command {
	options := &assuranceOptions{}
	command := &cobra.Command{
		Use:   "verification <verificationID>",
		Short: "Verification assurance get",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			verification, err := id.ParseVerification(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid verification id is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "assurance", method: http.MethodGet, path: "/v1/verifications/" + verification.String() + "/assurance"})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}
