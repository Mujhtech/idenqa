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

type subjectOptions struct {
	reviewOptions
	reveal  bool
	confirm bool
}

func newSubjectCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "subject",
		Short: "Administer persistent tenant subjects and identity records through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newSubjectCreateCommand())
	root.AddCommand(newSubjectGetCommand())
	root.AddCommand(newSubjectUpdateCommand())
	root.AddCommand(newSubjectDeleteCommand())
	root.AddCommand(newSubjectLookupCommand())
	root.AddCommand(newSubjectRecordsCommand())
	root.AddCommand(newSubjectAttachVerificationCommand())
	root.AddCommand(newSubjectProjectionRebuildCommand())
	root.AddCommand(newSubjectIdentifierLookupCommand())
	root.AddCommand(newSubjectConfigurationCommand())
	root.AddCommand(newSubjectConfigurationGetCommand())
	root.AddCommand(newSubjectConfigurationRevisionCommand())
	return root
}

func newSubjectCreateCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "create",
		Short: "Subject create",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := readReviewBody(command, &options.reviewOptions, "subject")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation:   "subject",
				method:      http.MethodPost,
				path:        "/v1/subjects",
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

func newSubjectGetCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "get <subjectID>",
		Short: "Subject get",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			subject, err := id.ParseSubject(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid subject id is required"))
			}
			request := reviewRequest{operation: "subject", method: http.MethodGet, path: "/v1/subjects/" + subject.String()}
			if options.reveal {
				request.query = url.Values{"reveal": {"true"}}
			}
			return runReviewHTTP(command, &options.reviewOptions, request)
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().BoolVar(&options.reveal, "reveal", false, "request explicit value reveal under identity:reveal")
	return command
}

func newSubjectUpdateCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "update <subjectID>",
		Short: "Subject update",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			subject, err := id.ParseSubject(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid subject id is required"))
			}
			body, err := readReviewBody(command, &options.reviewOptions, "subject")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation:   "subject",
				method:      http.MethodPut,
				path:        "/v1/subjects/" + subject.String(),
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

func newSubjectDeleteCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "delete <subjectID>",
		Short: "Subject delete",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			subject, err := id.ParseSubject(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid subject id is required"))
			}
			if !options.confirm {
				return cli.UsageError(errors.New("this operation requires --confirm"))
			}
			if options.expectedVersion < 1 {
				return cli.UsageError(errors.New("expected-version must be positive"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation:   "subject",
				method:      http.MethodDelete,
				path:        "/v1/subjects/" + subject.String(),
				query:       url.Values{"expected_version": {strconv.FormatInt(options.expectedVersion, 10)}},
				idempotency: true,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "current subject version")
	command.Flags().BoolVar(&options.confirm, "confirm", false, "confirm this consequential operation")
	command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	return command
}

func newSubjectLookupCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "lookup",
		Short: "Subject external-reference lookup",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := readReviewBody(command, &options.reviewOptions, "subject")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "subject",
				method:    http.MethodPost,
				path:      "/v1/subjects/lookup",
				body:      body,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON request body file, or - for stdin")
	return command
}

func newSubjectRecordsCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "records <subjectID>",
		Short: "Subject record append",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			subject, err := id.ParseSubject(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid subject id is required"))
			}
			body, err := readReviewBody(command, &options.reviewOptions, "subject")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation:   "subject",
				method:      http.MethodPost,
				path:        "/v1/subjects/" + subject.String() + "/records",
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

func newSubjectAttachVerificationCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "attach-verification <subjectID> <verificationID>",
		Short: "Subject verification link",
		Args:  cli.UsageArgs(cobra.ExactArgs(2)),
		RunE: func(command *cobra.Command, args []string) error {
			return runSubjectVersionCommand(command, options, "attach-verification", args[0], args[1])
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "current subject version")
	command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	return command
}

func newSubjectProjectionRebuildCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "projection-rebuild <subjectID>",
		Short: "Subject projection rebuild",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			return runSubjectVersionCommand(command, options, "projection-rebuild", args[0], "")
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "current subject version")
	command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	return command
}

func runSubjectVersionCommand(command *cobra.Command, options *subjectOptions, operation, encodedSubject, encodedVerification string) error {
	subject, err := id.ParseSubject(encodedSubject)
	if err != nil {
		return cli.UsageError(errors.New("a valid subject id is required"))
	}
	if options.expectedVersion < 1 {
		return cli.UsageError(errors.New("expected-version must be positive"))
	}
	request := reviewRequest{
		operation:   "subject",
		method:      http.MethodPut,
		path:        "/v1/subjects/" + subject.String(),
		body:        map[string]any{"expected_version": options.expectedVersion},
		idempotency: true,
	}
	switch operation {
	case "attach-verification":
		verification, err := id.ParseVerification(encodedVerification)
		if err != nil {
			return cli.UsageError(errors.New("a valid verification id is required"))
		}
		request.path += "/verifications/" + verification.String()
	case "projection-rebuild":
		request.method = http.MethodPost
		request.path += "/projection/rebuild"
	}
	return runReviewHTTP(command, &options.reviewOptions, request)
}

func newSubjectIdentifierLookupCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "identifier-lookup",
		Short: "Subject identifier lookup",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := readReviewBody(command, &options.reviewOptions, "subject")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "subject",
				method:    http.MethodPost,
				path:      "/v1/identity/identifiers/lookup",
				body:      body,
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON request body file, or - for stdin")
	return command
}

func newSubjectConfigurationCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "configuration",
		Short: "Subject identity configuration activate",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			body, err := readReviewBody(command, &options.reviewOptions, "subject")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation:   "subject",
				method:      http.MethodPut,
				path:        "/v1/identity/configuration",
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

func newSubjectConfigurationGetCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "configuration-get",
		Short: "Subject identity configuration get",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{operation: "subject", method: http.MethodGet, path: "/v1/identity/configuration"})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}

func newSubjectConfigurationRevisionCommand() *cobra.Command {
	options := &subjectOptions{}
	command := &cobra.Command{
		Use:   "configuration-revision <version>",
		Short: "Subject identity configuration revision get",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			version, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil || version < 1 {
				return cli.UsageError(errors.New("a positive configuration version is required"))
			}
			return runReviewHTTP(command, &options.reviewOptions, reviewRequest{
				operation: "subject",
				method:    http.MethodGet,
				path:      "/v1/identity/configuration/revisions/" + strconv.FormatInt(version, 10),
			})
		},
	}
	addReviewAPICommonFlags(command, &options.reviewOptions)
	return command
}
