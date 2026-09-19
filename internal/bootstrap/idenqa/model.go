package idenqa

import (
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/spf13/cobra"
)

var modelRegistryName = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

type modelOptions struct {
	apiURL, keyFile, bodyFile, idempotencyKey string
	before                                    int64
	limit                                     int
	confirm                                   bool
}

func newModelCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "model",
		Short: "Administer the evaluation-only model registry through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newModelGetCommand())
	root.AddCommand(newModelRevisionCommand())
	root.AddCommand(newModelHistoryCommand())
	for _, operation := range []string{"register", "threshold", "activate", "rollback", "retire"} {
		root.AddCommand(newModelMutationCommand(operation))
	}
	root.AddCommand(newModelValidateCommand())
	return root
}

func newModelGetCommand() *cobra.Command {
	options := &modelOptions{}
	command := &cobra.Command{
		Use:   "get <name>",
		Short: "Get evaluation-model registry state",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if !modelRegistryName.MatchString(args[0]) {
				return cli.UsageError(errors.New("a valid model name is required"))
			}
			return runModelHTTP(command, options, reviewRequest{method: http.MethodGet, path: "/v1/models/" + args[0]})
		},
	}
	addModelCommonFlags(command, options)
	return command
}

func newModelRevisionCommand() *cobra.Command {
	options := &modelOptions{}
	command := &cobra.Command{
		Use:   "revision <name> <kind> <revision>",
		Short: "Get one immutable evaluation-model revision",
		Args:  cli.UsageArgs(cobra.ExactArgs(3)),
		RunE: func(command *cobra.Command, args []string) error {
			if !modelRegistryName.MatchString(args[0]) {
				return cli.UsageError(errors.New("a valid model name is required"))
			}
			if args[1] != "model" && args[1] != "threshold" {
				return cli.UsageError(errors.New("kind must be model or threshold"))
			}
			revision, err := strconv.ParseInt(args[2], 10, 64)
			if err != nil || revision < 1 {
				return cli.UsageError(errors.New("revision must be a positive integer"))
			}
			path := "/v1/models/" + args[0] + "/revisions/" + args[1] + "/" + strconv.FormatInt(revision, 10)
			return runModelHTTP(command, options, reviewRequest{method: http.MethodGet, path: path})
		},
	}
	addModelCommonFlags(command, options)
	return command
}

func newModelHistoryCommand() *cobra.Command {
	options := &modelOptions{limit: 50}
	command := &cobra.Command{
		Use:   "history <name>",
		Short: "List immutable evaluation-registry command history",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if !modelRegistryName.MatchString(args[0]) {
				return cli.UsageError(errors.New("a valid model name is required"))
			}
			if options.before < 0 {
				return cli.UsageError(errors.New("before must not be negative"))
			}
			if options.limit < 1 || options.limit > 100 {
				return cli.UsageError(errors.New("limit must be from 1 to 100"))
			}
			query := url.Values{
				"before": {strconv.FormatInt(options.before, 10)},
				"limit":  {strconv.Itoa(options.limit)},
			}
			return runModelHTTP(command, options, reviewRequest{method: http.MethodGet, path: "/v1/models/" + args[0] + "/history", query: query})
		},
	}
	addModelCommonFlags(command, options)
	command.Flags().Int64Var(&options.before, "before", 0, "return receipts below this registry version")
	command.Flags().IntVar(&options.limit, "limit", 50, "page size from 1 to 100")
	return command
}

func newModelMutationCommand(operation string) *cobra.Command {
	options := &modelOptions{}
	command := &cobra.Command{
		Use:   operation + " <name>",
		Short: "Model " + operation,
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if !modelRegistryName.MatchString(args[0]) {
				return cli.UsageError(errors.New("a valid model name is required"))
			}
			if operation == "retire" && !options.confirm {
				return cli.UsageError(errors.New("model retire requires --confirm"))
			}
			body, err := readBoundedJSONBody(command, options.bodyFile, "model")
			if err != nil {
				return err
			}
			return runModelHTTP(command, options, reviewRequest{
				method:      http.MethodPost,
				path:        "/v1/models/" + args[0] + "/" + operation,
				body:        body,
				idempotency: true,
			})
		},
	}
	addModelCommonFlags(command, options)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON model command body file, or - for stdin")
	command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	if operation == "retire" {
		command.Flags().BoolVar(&options.confirm, "confirm", false, "confirm retirement of the active evaluation deployment")
	}
	return command
}

func newModelValidateCommand() *cobra.Command {
	options := &modelOptions{}
	command := &cobra.Command{
		Use:   "validate <name>",
		Short: "Validate a model registry command without persistence",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if !modelRegistryName.MatchString(args[0]) {
				return cli.UsageError(errors.New("a valid model name is required"))
			}
			body, err := readBoundedJSONBody(command, options.bodyFile, "model")
			if err != nil {
				return err
			}
			return runModelHTTP(command, options, reviewRequest{method: http.MethodPost, path: "/v1/models/" + args[0] + "/validate", body: body})
		},
	}
	addModelCommonFlags(command, options)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON model command body file, or - for stdin")
	return command
}

func addModelCommonFlags(command *cobra.Command, options *modelOptions) {
	command.Flags().StringVar(&options.apiURL, "api-url", "", "Core API base URL (HTTPS, or local loopback HTTP)")
	command.Flags().StringVar(&options.keyFile, "api-key-file", "", "read API credential from this file (otherwise IDENQA_API_KEY)")
}

func runModelHTTP(command *cobra.Command, options *modelOptions, request reviewRequest) error {
	if request.operation == "" {
		request.operation = "model"
	}
	return runReviewHTTP(command, &reviewOptions{apiURL: options.apiURL, keyFile: options.keyFile, idempotencyKey: options.idempotencyKey}, request)
}
