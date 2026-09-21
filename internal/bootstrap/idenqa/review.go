package idenqa

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

const reviewBodyMaximum = 1 << 20

type reviewOptions struct {
	apiURL, keyFile, decisionID, bodyFile, idempotencyKey, cursor string
	expectedVersion                                               int64
	limit                                                         int
}

type reviewRequest struct {
	operation   string
	method      string
	path        string
	query       url.Values
	body        any
	idempotency bool
	binary      bool
	transform   func([]byte) ([]byte, error)
}

func newReviewCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "review",
		Short: "Administer manual review cases and appeals through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newReviewCaseCommand())
	root.AddCommand(newReviewAppealCommand())
	return root
}

func newReviewCaseCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "case",
		Short: "Administer review cases through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newReviewCaseOpenCommand())
	root.AddCommand(newReviewCaseListCommand())
	for _, operation := range []string{"get", "claim", "findings", "evidence", "grant", "recaptures", "recapture", "recapture-renew", "recapture-ack", "recapture-reevaluate", "correction", "correction-evaluate", "arbitration"} {
		root.AddCommand(newReviewCaseOperation(operation))
	}
	root.AddCommand(newReviewCaseGrantContentCommand())
	return root
}

func newReviewCaseOpenCommand() *cobra.Command {
	options := &reviewOptions{}
	command := &cobra.Command{
		Use:   "open",
		Short: "Review case open",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			decision, err := id.ParseDecision(options.decisionID)
			if err != nil {
				return cli.UsageError(errors.New("a valid decision id is required"))
			}
			return runReviewHTTP(command, options, reviewRequest{
				method:      http.MethodPost,
				path:        "/v1/decisions/" + decision.String() + "/review-cases",
				idempotency: true,
			})
		},
	}
	addReviewAPICommonFlags(command, options)
	command.Flags().StringVar(&options.decisionID, "decision-id", "", "challenged decision id")
	command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	return command
}

func newReviewCaseListCommand() *cobra.Command {
	options := &reviewOptions{limit: 25}
	command := &cobra.Command{
		Use:   "list",
		Short: "Review case list",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			if options.limit < 1 || options.limit > 100 {
				return cli.UsageError(errors.New("limit must be from 1 to 100"))
			}
			query := url.Values{"limit": {strconv.Itoa(options.limit)}}
			if options.cursor != "" {
				query.Set("cursor", options.cursor)
			}
			return runReviewHTTP(command, options, reviewRequest{method: http.MethodGet, path: "/v1/review-cases", query: query})
		},
	}
	addReviewAPICommonFlags(command, options)
	command.Flags().IntVar(&options.limit, "limit", 25, "page size from 1 to 100")
	command.Flags().StringVar(&options.cursor, "cursor", "", "opaque continuation cursor")
	return command
}

func newReviewCaseOperation(operation string) *cobra.Command {
	options := &reviewOptions{}
	command := &cobra.Command{
		Use:   operation + " <caseID>",
		Short: "Review case " + operation,
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			return runReviewCaseOperation(command, operation, options, args[0])
		},
	}
	addReviewAPICommonFlags(command, options)
	flags := command.Flags()
	switch operation {
	case "claim", "evidence":
		flags.Int64Var(&options.expectedVersion, "expected-version", 0, "current case version")
	case "findings", "grant", "recapture", "recapture-renew", "recapture-ack", "recapture-reevaluate", "correction", "correction-evaluate", "arbitration":
		flags.StringVar(&options.bodyFile, "body-file", "", "bounded JSON review body file, or - for stdin")
	}
	if reviewCaseIdempotent(operation) {
		flags.StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	}
	return command
}

func reviewCaseIdempotent(operation string) bool {
	switch operation {
	case "grant", "recapture", "recapture-renew", "recapture-ack", "recapture-reevaluate", "correction-evaluate", "arbitration":
		return true
	}
	return false
}

func newReviewCaseGrantContentCommand() *cobra.Command {
	options := &reviewOptions{}
	command := &cobra.Command{
		Use:   "grant-content <grantID>",
		Short: "Review case grant-content",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			grant, err := id.ParseGrant(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid evidence grant id is required"))
			}
			return runReviewHTTP(command, options, reviewRequest{method: http.MethodPost, path: "/v1/review-evidence-grants/" + grant.String() + "/content", binary: true})
		},
	}
	addReviewAPICommonFlags(command, options)
	return command
}

func newReviewAppealCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "appeal",
		Short: "Administer review appeals through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newReviewAppealOpenCommand())
	for _, operation := range []string{"get", "assign", "resolve", "withdraw"} {
		root.AddCommand(newReviewAppealOperation(operation))
	}
	return root
}

func newReviewAppealOpenCommand() *cobra.Command {
	options := &reviewOptions{}
	command := &cobra.Command{
		Use:   "open <caseID>",
		Short: "Appeal open",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			reviewCase, err := id.ParseReviewCase(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid review case id is required"))
			}
			body, err := readReviewBody(command, options, "review")
			if err != nil {
				return err
			}
			return runReviewHTTP(command, options, reviewRequest{
				method:      http.MethodPost,
				path:        "/v1/review-cases/" + reviewCase.String() + "/appeals",
				body:        body,
				idempotency: true,
			})
		},
	}
	addReviewAPICommonFlags(command, options)
	command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON review body file, or - for stdin")
	command.Flags().StringVar(&options.idempotencyKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	return command
}

func newReviewAppealOperation(operation string) *cobra.Command {
	options := &reviewOptions{}
	command := &cobra.Command{
		Use:   operation + " <appealID>",
		Short: "Appeal " + operation,
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			return runReviewAppealOperation(command, operation, options, args[0])
		},
	}
	addReviewAPICommonFlags(command, options)
	if operation != "get" {
		command.Flags().StringVar(&options.bodyFile, "body-file", "", "bounded JSON review body file, or - for stdin")
	}
	return command
}

func addReviewAPICommonFlags(command *cobra.Command, options *reviewOptions) {
	command.Flags().StringVar(&options.apiURL, "api-url", "", "Core API base URL (HTTPS, or local loopback HTTP)")
	command.Flags().StringVar(&options.keyFile, "api-key-file", "", "read API credential from this file (otherwise IDENQA_API_KEY)")
}

func runReviewCaseOperation(command *cobra.Command, operation string, options *reviewOptions, encoded string) error {
	reviewCase, err := id.ParseReviewCase(encoded)
	if err != nil {
		return cli.UsageError(errors.New("a valid review case id is required"))
	}
	request := reviewRequest{method: http.MethodGet, path: "/v1/review-cases/" + reviewCase.String()}
	switch operation {
	case "get":
	case "claim":
		if options.expectedVersion < 1 {
			return cli.UsageError(errors.New("expected-version must be positive"))
		}
		request.method = http.MethodPost
		request.path += "/claim"
		request.body = map[string]any{"expected_version": options.expectedVersion}
	case "findings":
		request.method = http.MethodPost
		request.path += "/findings"
		if request.body, err = readReviewBody(command, options, "review"); err != nil {
			return err
		}
	case "evidence":
		if options.expectedVersion < 1 {
			return cli.UsageError(errors.New("expected-version must be positive"))
		}
		request.path += "/evidence"
		request.query = url.Values{"expected_version": {strconv.FormatInt(options.expectedVersion, 10)}}
	case "grant":
		request.method = http.MethodPost
		request.path += "/evidence-grants"
		request.idempotency = true
		if request.body, err = readReviewBody(command, options, "review"); err != nil {
			return err
		}
	case "recaptures":
		request.path += "/recaptures"
	case "recapture":
		request.method = http.MethodPost
		request.path += "/recaptures"
		request.idempotency = true
		if request.body, err = readReviewBody(command, options, "review"); err != nil {
			return err
		}
	case "recapture-renew":
		request.method = http.MethodPost
		request.path += "/recaptures/renew"
		request.idempotency = true
		if request.body, err = readReviewBody(command, options, "review"); err != nil {
			return err
		}
	case "recapture-ack":
		request.method = http.MethodPost
		request.path += "/recaptures/acknowledgements"
		request.idempotency = true
		if request.body, err = readReviewBody(command, options, "review"); err != nil {
			return err
		}
	case "recapture-reevaluate":
		request.method = http.MethodPost
		request.path += "/recaptures/reevaluations"
		request.idempotency = true
		if request.body, err = readReviewBody(command, options, "review"); err != nil {
			return err
		}
	case "correction":
		request.method = http.MethodPost
		request.path += "/corrections"
		if request.body, err = readReviewBody(command, options, "review"); err != nil {
			return err
		}
	case "correction-evaluate":
		request.method = http.MethodPost
		request.path += "/corrections/evaluate"
		request.idempotency = true
		if request.body, err = readReviewBody(command, options, "review"); err != nil {
			return err
		}
	case "arbitration":
		request.method = http.MethodPost
		request.path += "/arbitrations"
		request.idempotency = true
		if request.body, err = readReviewBody(command, options, "review"); err != nil {
			return err
		}
	}
	return runReviewHTTP(command, options, request)
}

func runReviewAppealOperation(command *cobra.Command, operation string, options *reviewOptions, encoded string) error {
	appeal, err := id.ParseAppeal(encoded)
	if err != nil {
		return cli.UsageError(errors.New("a valid appeal id is required"))
	}
	request := reviewRequest{method: http.MethodGet, path: "/v1/appeals/" + appeal.String()}
	if operation != "get" {
		request.method = http.MethodPost
		request.path += "/" + operation
		if request.body, err = readReviewBody(command, options, "review"); err != nil {
			return err
		}
	}
	return runReviewHTTP(command, options, request)
}

func readReviewBody(command *cobra.Command, options *reviewOptions, operation string) (json.RawMessage, error) {
	return readBoundedJSONBody(command, options.bodyFile, operation)
}

func readBoundedJSONBody(command *cobra.Command, bodyFile, operation string) (json.RawMessage, error) {
	if bodyFile == "" {
		return nil, cli.UsageError(errors.New("body-file is required"))
	}
	input := command.InOrStdin()
	if bodyFile != "-" {
		file, err := os.Open(bodyFile) //nolint:gosec // The operator-selected local body path is the documented CLI contract.
		if err != nil {
			return nil, cli.RuntimeError(operation, errors.New("open "+operation+" body file"))
		}
		defer func() { _ = file.Close() }()
		input = file
	}
	material, err := io.ReadAll(io.LimitReader(input, reviewBodyMaximum+1))
	if err != nil || len(material) > reviewBodyMaximum || !json.Valid(material) {
		clear(material)
		return nil, cli.UsageError(errors.New("body-file must contain one bounded JSON request body"))
	}
	return json.RawMessage(material), nil
}

func runReviewHTTP(command *cobra.Command, options *reviewOptions, request reviewRequest) error {
	operation := request.operation
	if operation == "" {
		operation = "review"
	}
	base, err := url.Parse(options.apiURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "https" && (base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost" && base.Hostname() != "::1"))) {
		return cli.UsageError(errors.New("a valid HTTPS API URL or local loopback HTTP URL is required"))
	}
	if request.idempotency && (options.idempotencyKey == "" || len(options.idempotencyKey) > 128 || strings.ContainsAny(options.idempotencyKey, "\r\n")) {
		return cli.UsageError(errors.New("a stable idempotency-key of at most 128 characters is required"))
	}
	key := os.Getenv("IDENQA_API_KEY")
	if options.keyFile != "" {
		file, err := os.Open(options.keyFile)
		if err != nil {
			return cli.RuntimeError(operation, errors.New("read API credential file"))
		}
		material, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(material) > 4096 {
			clear(material)
			return cli.RuntimeError(operation, errors.New("read bounded API credential file"))
		}
		key = strings.TrimSpace(string(material))
		clear(material)
	}
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return cli.UsageError(errors.New("an API credential file or IDENQA_API_KEY is required"))
	}
	var encoded []byte
	if request.body != nil {
		encoded, err = json.Marshal(request.body)
		if err != nil {
			return cli.RuntimeError(operation, errors.New("encode "+operation+" request"))
		}
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + request.path
	if len(request.query) > 0 {
		target.RawQuery = request.query.Encode()
	}
	apiRequest, err := http.NewRequestWithContext(command.Context(), request.method, target.String(), bytes.NewReader(encoded))
	if err != nil {
		return cli.RuntimeError(operation, errors.New("construct "+operation+" API request"))
	}
	apiRequest.Header.Set("Authorization", "Bearer "+key)
	if request.body != nil {
		apiRequest.Header.Set("Content-Type", "application/json")
	}
	if request.idempotency {
		apiRequest.Header.Set("Idempotency-Key", strconv.Quote(options.idempotencyKey))
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(apiRequest)
	if err != nil {
		return cli.RuntimeError(operation, errors.New(operation+" API request failed"))
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return cli.RuntimeError(operation, errors.New("read bounded "+operation+" API response"))
	}
	defer clear(raw)
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusNoContent {
		return cli.RuntimeError(operation, fmt.Errorf("%s API returned status %d", operation, response.StatusCode))
	}
	if request.binary {
		if _, err := command.OutOrStdout().Write(raw); err != nil {
			return cli.RuntimeError(operation, err)
		}
		return nil
	}
	if request.transform != nil {
		transformed, transformErr := request.transform(raw)
		if transformErr != nil {
			return cli.RuntimeError(operation, transformErr)
		}
		raw = transformed
	}
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		return cli.RuntimeError(operation, errors.New("decode "+operation+" API response"))
	}
	if err := json.NewEncoder(command.OutOrStdout()).Encode(result); err != nil {
		return cli.RuntimeError(operation, err)
	}
	return nil
}
