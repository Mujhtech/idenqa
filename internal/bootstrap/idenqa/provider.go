package idenqa

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

var providerReason = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

type providerOptions struct {
	apiURL, keyFile, bodyFile, idempotencyKey, reason, cursor string
	secretReference, credentialVersion                        string
	expectedVersion                                           int64
	limit                                                     int
}

func newProviderCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "provider",
		Short: "Administer tenant provider registrations through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newProviderListCommand())
	root.AddCommand(newProviderBodyCommand("validate"))
	root.AddCommand(newProviderBodyCommand("register"))
	root.AddCommand(newProviderUpdateCommand())
	for _, operation := range []string{"get", "health"} {
		root.AddCommand(newProviderGetCommand(operation))
	}
	for _, operation := range []string{"enable", "disable"} {
		root.AddCommand(newProviderToggleCommand(operation))
	}
	root.AddCommand(newProviderRotateCredentialCommand())
	root.AddCommand(newProviderSimulateCommand())
	return root
}

func newProviderListCommand() *cobra.Command {
	options := &providerOptions{limit: 25}
	command := &cobra.Command{
		Use:   "list",
		Short: "List tenant provider registrations",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			if options.limit < 1 || options.limit > 100 {
				return cli.UsageError(errors.New("limit must be from 1 to 100"))
			}
			query := url.Values{"limit": {strconv.Itoa(options.limit)}}
			if options.cursor != "" {
				query.Set("cursor", options.cursor)
			}
			return runProviderHTTP(command, options, reviewRequest{method: http.MethodGet, path: "/v1/providers", query: query})
		},
	}
	addProviderCommonFlags(command, options)
	command.Flags().IntVar(&options.limit, "limit", 25, "page size from 1 to 100")
	command.Flags().StringVar(&options.cursor, "cursor", "", "opaque continuation cursor")
	return command
}

func newProviderGetCommand(operation string) *cobra.Command {
	options := &providerOptions{}
	command := &cobra.Command{
		Use:   operation + " <providerID>",
		Short: "Provider " + operation,
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if _, err := id.ParseProviderRegistration(args[0]); err != nil {
				return cli.UsageError(errors.New("a valid provider registration id is required"))
			}
			path := "/v1/providers/" + args[0]
			if operation == "health" {
				path += "/health"
			}
			return runProviderHTTP(command, options, reviewRequest{method: http.MethodGet, path: path})
		},
	}
	addProviderCommonFlags(command, options)
	return command
}

func newProviderBodyCommand(operation string) *cobra.Command {
	options := &providerOptions{}
	command := &cobra.Command{
		Use:   operation + " <providerID>",
		Short: "Provider " + operation,
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if _, err := id.ParseProviderRegistration(args[0]); err != nil {
				return cli.UsageError(errors.New("a valid provider registration id is required"))
			}
			body, err := readBoundedJSONBody(command, options.bodyFile, "provider")
			if err != nil {
				return err
			}
			if operation == "validate" {
				return runProviderHTTP(command, options, reviewRequest{method: http.MethodPost, path: "/v1/providers/" + args[0] + "/validate", body: body})
			}
			if !providerReason.MatchString(options.reason) {
				return cli.UsageError(errors.New("reason must be a bounded lowercase token"))
			}
			canonical, err := providerEnvelope(options.reason, 0, false, body)
			if err != nil {
				return err
			}
			return runProviderHTTP(command, options, reviewRequest{method: http.MethodPost, path: "/v1/providers", body: canonical, idempotency: true})
		},
	}
	addProviderCommonFlags(command, options)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded secret-free registration body file, or - for stdin")
	command.Flags().StringVar(&options.reason, "reason", "", "bounded attribution reason token")
	if operation == "register" {
		command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	}
	return command
}

func newProviderUpdateCommand() *cobra.Command {
	options := &providerOptions{}
	command := &cobra.Command{
		Use:   "update <providerID>",
		Short: "Provider update",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if _, err := id.ParseProviderRegistration(args[0]); err != nil {
				return cli.UsageError(errors.New("a valid provider registration id is required"))
			}
			if options.expectedVersion < 1 {
				return cli.UsageError(errors.New("expected-version must be positive"))
			}
			if !providerReason.MatchString(options.reason) {
				return cli.UsageError(errors.New("reason must be a bounded lowercase token"))
			}
			body, err := readBoundedJSONBody(command, options.bodyFile, "provider")
			if err != nil {
				return err
			}
			canonical, err := providerEnvelope(options.reason, options.expectedVersion, true, body)
			if err != nil {
				return err
			}
			return runProviderHTTP(command, options, reviewRequest{method: http.MethodPut, path: "/v1/providers/" + args[0], body: canonical})
		},
	}
	addProviderCommonFlags(command, options)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded secret-free registration body file, or - for stdin")
	command.Flags().StringVar(&options.reason, "reason", "", "bounded attribution reason token")
	command.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "current registration version")
	return command
}

func newProviderRotateCredentialCommand() *cobra.Command {
	options := &providerOptions{}
	command := &cobra.Command{
		Use:   "rotate-credential <providerID>",
		Short: "Rotate a provider registration credential reference version",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if _, err := id.ParseProviderRegistration(args[0]); err != nil {
				return cli.UsageError(errors.New("a valid provider registration id is required"))
			}
			if options.expectedVersion < 1 {
				return cli.UsageError(errors.New("expected-version must be positive"))
			}
			if !providerReason.MatchString(options.reason) {
				return cli.UsageError(errors.New("reason must be a bounded lowercase token"))
			}
			if options.secretReference == "" || options.credentialVersion == "" {
				return cli.UsageError(errors.New("secret-reference and credential-version are required"))
			}
			body := struct {
				ExpectedVersion int64  `json:"expected_version"`
				Reason          string `json:"reason"`
				Credential      struct {
					SecretReference   string `json:"secret_reference"`
					CredentialVersion string `json:"credential_version"`
				} `json:"credential"`
			}{options.expectedVersion, options.reason, struct {
				SecretReference   string `json:"secret_reference"`
				CredentialVersion string `json:"credential_version"`
			}{options.secretReference, options.credentialVersion}}
			return runProviderHTTP(command, options, reviewRequest{method: http.MethodPost, path: "/v1/providers/" + args[0] + "/rotate-credential", body: body})
		},
	}
	addProviderCommonFlags(command, options)
	command.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "current registration version")
	command.Flags().StringVar(&options.reason, "reason", "", "bounded attribution reason token")
	command.Flags().StringVar(&options.secretReference, "secret-reference", "", "new secret:// credential reference")
	command.Flags().StringVar(&options.credentialVersion, "credential-version", "", "new opaque credential version")
	return command
}

func newProviderToggleCommand(operation string) *cobra.Command {
	options := &providerOptions{}
	command := &cobra.Command{
		Use:   operation + " <providerID>",
		Short: "Provider " + operation,
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if _, err := id.ParseProviderRegistration(args[0]); err != nil {
				return cli.UsageError(errors.New("a valid provider registration id is required"))
			}
			if options.expectedVersion < 1 {
				return cli.UsageError(errors.New("expected-version must be positive"))
			}
			if !providerReason.MatchString(options.reason) {
				return cli.UsageError(errors.New("reason must be a bounded lowercase token"))
			}
			body := struct {
				ExpectedVersion int64  `json:"expected_version"`
				Reason          string `json:"reason"`
			}{options.expectedVersion, options.reason}
			return runProviderHTTP(command, options, reviewRequest{method: http.MethodPost, path: "/v1/providers/" + args[0] + "/" + operation, body: body})
		},
	}
	addProviderCommonFlags(command, options)
	command.Flags().Int64Var(&options.expectedVersion, "expected-version", 0, "current registration version")
	command.Flags().StringVar(&options.reason, "reason", "", "bounded attribution reason token")
	return command
}

func newProviderSimulateCommand() *cobra.Command {
	options := &providerOptions{}
	command := &cobra.Command{
		Use:   "simulate-failure <providerID>",
		Short: "Preview provider failure classification without dispatch",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			if _, err := id.ParseProviderRegistration(args[0]); err != nil {
				return cli.UsageError(errors.New("a valid provider registration id is required"))
			}
			body, err := readBoundedJSONBody(command, options.bodyFile, "provider")
			if err != nil {
				return err
			}
			return runProviderHTTP(command, options, reviewRequest{method: http.MethodPost, path: "/v1/providers/" + args[0] + "/failure-simulations", body: body})
		},
	}
	addProviderCommonFlags(command, options)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded failure classification body file, or - for stdin")
	return command
}

func addProviderCommonFlags(command *cobra.Command, options *providerOptions) {
	command.Flags().StringVar(&options.apiURL, "api-url", "", "Core API base URL (HTTPS, or local loopback HTTP)")
	command.Flags().StringVar(&options.keyFile, "api-key-file", "", "read API credential from this file (otherwise IDENQA_API_KEY)")
}

func runProviderHTTP(command *cobra.Command, options *providerOptions, request reviewRequest) error {
	if request.operation == "" {
		request.operation = "provider"
	}
	return runReviewHTTP(command, &reviewOptions{apiURL: options.apiURL, keyFile: options.keyFile, idempotencyKey: options.idempotencyKey}, request)
}

// providerEnvelope wraps one bounded secret-free registration document into
// the closed command envelope without interpreting or rewriting its fields.
func providerEnvelope(reason string, expectedVersion int64, includeVersion bool, document json.RawMessage) (map[string]json.RawMessage, error) {
	encodedReason, err := json.Marshal(reason)
	if err != nil {
		return nil, cli.UsageError(errors.New("encode provider command attribute"))
	}
	result := map[string]json.RawMessage{"reason": encodedReason, "registration": document}
	if includeVersion {
		result["expected_version"] = json.RawMessage(strconv.FormatInt(expectedVersion, 10))
	}
	return result, nil
}
