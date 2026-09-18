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

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

type policyAdminOptions struct {
	apiURL, keyFile, identifier, file, key, reason, cursor string
	revision, expectedRevision                             uint32
	version                                                int64
	limit                                                  int
	confirm                                                bool
}

func addPolicyAdminCommands(root *cobra.Command) {
	for _, operation := range []string{"create", "validate", "list", "get", "revisions", "add-revision", "get-revision", "activations", "activate", "rollback"} {
		root.AddCommand(newPolicyAdminCommand(operation))
	}
}
func newPolicyAdminCommand(operation string) *cobra.Command {
	options := &policyAdminOptions{version: -1, limit: 25}
	command := &cobra.Command{Use: operation, Short: "Policy " + operation + " through the public API", Args: cli.UsageArgs(cobra.NoArgs), RunE: func(command *cobra.Command, _ []string) error { return runPolicyAdmin(command, operation, options) }}
	flags := command.Flags()
	flags.StringVar(&options.apiURL, "api-url", "", "Core API base URL (HTTPS or loopback HTTP)")
	flags.StringVar(&options.keyFile, "api-key-file", "", "API credential file; otherwise IDENQA_API_KEY")
	if operation != "create" && operation != "validate" && operation != "list" {
		flags.StringVar(&options.identifier, "id", "", "policy identifier")
	}
	if operation == "create" || operation == "validate" || operation == "add-revision" {
		flags.StringVar(&options.file, "file", "", "bounded JSON policy definition file, or - for stdin")
	}
	if operation == "create" || operation == "add-revision" || operation == "activate" || operation == "rollback" {
		flags.StringVar(&options.key, "idempotency-key", "", "stable command retry key")
	}
	if operation == "add-revision" {
		flags.Uint32Var(&options.expectedRevision, "expected-revision", 0, "latest registered revision")
	}
	if operation == "get-revision" || operation == "activate" || operation == "rollback" {
		flags.Uint32Var(&options.revision, "revision", 0, "explicit target revision")
	}
	if operation == "activate" || operation == "rollback" {
		flags.Int64Var(&options.version, "expected-version", -1, "current activation version, zero for initial activation")
		flags.StringVar(&options.reason, "reason", "", "non-sensitive reason code")
		flags.BoolVar(&options.confirm, "confirm", false, "confirm the active policy change")
	}
	if operation == "list" || operation == "revisions" || operation == "activations" {
		flags.IntVar(&options.limit, "limit", 25, "page size from 1 to 100")
		flags.StringVar(&options.cursor, "cursor", "", "opaque continuation cursor")
	}
	return command
}
func runPolicyAdmin(command *cobra.Command, operation string, options *policyAdminOptions) error {
	target, err := url.Parse(options.apiURL)
	if err != nil || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || (target.Scheme != "https" && (target.Scheme != "http" || (target.Hostname() != "localhost" && target.Hostname() != "127.0.0.1" && target.Hostname() != "::1"))) {
		return cli.UsageError(errors.New("a valid HTTPS API URL or loopback HTTP URL is required"))
	}
	if (operation == "activate" || operation == "rollback") && (!options.confirm || options.version < 0 || options.revision == 0 || options.reason == "") {
		return cli.UsageError(errors.New("activation and rollback require --confirm, --expected-version, --revision and --reason"))
	}
	if operation == "get-revision" && options.revision == 0 {
		return cli.UsageError(errors.New("revision must be positive"))
	}
	if operation == "add-revision" && options.expectedRevision == 0 {
		return cli.UsageError(errors.New("expected-revision must be positive"))
	}
	path := "/v1/policies"
	method := "GET"
	var body any
	if operation != "create" && operation != "validate" && operation != "list" {
		if _, err := id.ParsePolicy(options.identifier); err != nil {
			return cli.UsageError(errors.New("a valid policy id is required"))
		}
		path += "/" + options.identifier
	}
	switch operation {
	case "create", "validate", "add-revision":
		if options.file == "" {
			return cli.UsageError(errors.New("file is required"))
		}
		input := command.InOrStdin()
		var file *os.File
		if options.file != "-" {
			file, err = os.Open(options.file)
			if err != nil {
				return cli.RuntimeError("policy", errors.New("open policy definition file"))
			}
			defer func() { _ = file.Close() }()
			input = file
		}
		definition, err := io.ReadAll(io.LimitReader(input, policyv1.MaximumDocumentBytes+1))
		if err != nil || len(definition) > policyv1.MaximumDocumentBytes || !json.Valid(definition) {
			return cli.UsageError(errors.New("file must contain one bounded JSON policy definition"))
		}
		body = struct {
			Definition       json.RawMessage `json:"definition"`
			ExpectedRevision uint32          `json:"expected_revision,omitempty"`
		}{definition, options.expectedRevision}
		method = "POST"
		if operation == "validate" {
			path += "/validate"
		}
		if operation == "add-revision" {
			path += "/revisions"
		}
	case "revisions", "activations":
		path += "/" + operation
	case "get-revision":
		path += "/revisions/" + strconv.FormatUint(uint64(options.revision), 10)
	case "activate", "rollback":
		path += "/" + operation
		method = "POST"
		body = struct {
			Revision        uint32 `json:"revision"`
			ExpectedVersion int64  `json:"expected_version"`
			Reason          string `json:"reason"`
		}{options.revision, options.version, options.reason}
	}
	if method == "POST" && operation != "validate" && (options.key == "" || len(options.key) > 128 || strings.ContainsAny(options.key, "\r\n")) {
		return cli.UsageError(errors.New("a stable idempotency-key is required"))
	}
	target.Path = strings.TrimRight(target.Path, "/") + path
	if operation == "list" || operation == "revisions" || operation == "activations" {
		if options.limit < 1 || options.limit > 100 {
			return cli.UsageError(errors.New("limit must be from 1 to 100"))
		}
		query := url.Values{"limit": {strconv.Itoa(options.limit)}}
		if options.cursor != "" {
			query.Set("cursor", options.cursor)
		}
		target.RawQuery = query.Encode()
	}
	credential := os.Getenv("IDENQA_API_KEY")
	if options.keyFile != "" {
		file, err := os.Open(options.keyFile)
		if err != nil {
			return cli.RuntimeError("policy", errors.New("open API credential file"))
		}
		material, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(material) > 4096 {
			clear(material)
			return cli.RuntimeError("policy", errors.New("read bounded API credential file"))
		}
		credential = strings.TrimSpace(string(material))
		clear(material)
	}
	if credential == "" || strings.ContainsAny(credential, "\r\n") {
		return cli.UsageError(errors.New("API credential file or IDENQA_API_KEY is required"))
	}
	var encoded []byte
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return cli.RuntimeError("policy", errors.New("encode policy request"))
		}
	}
	request, err := http.NewRequestWithContext(command.Context(), method, target.String(), bytes.NewReader(encoded))
	if err != nil {
		return cli.RuntimeError("policy", errors.New("construct API request"))
	}
	request.Header.Set("Authorization", "Bearer "+credential)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method == "POST" && operation != "validate" {
		request.Header.Set("Idempotency-Key", strconv.Quote(options.key))
	}
	client := http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return cli.RuntimeError("policy", errors.New("API request failed"))
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, (1<<20)+1))
	if err != nil || len(raw) > 1<<20 {
		return cli.RuntimeError("policy", errors.New("read bounded API response"))
	}
	if response.StatusCode != 200 {
		return cli.RuntimeError("policy", fmt.Errorf("API returned status %d", response.StatusCode))
	}
	if !json.Valid(raw) {
		return cli.RuntimeError("policy", errors.New("invalid API response"))
	}
	if _, err := fmt.Fprintln(command.OutOrStdout(), string(raw)); err != nil {
		return cli.RuntimeError("policy", err)
	}
	return nil
}
