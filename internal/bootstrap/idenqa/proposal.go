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
	"regexp"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

var proposalWorkflowPattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)

type proposalOptions struct {
	baseURL, keyFile, identifier, workflow, mode, modelID, modelVersion, promptVersion string
	contextDigest, retryKey, content                                                   string
	verificationID                                                                     string
	kinds                                                                              []string
	evidenceRefs                                                                       []string
	expiresInMinutes                                                                   int
	version                                                                            int64
	humanApproved                                                                      bool
	confirm                                                                            bool
}

func newProposalCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "proposal",
		Short: "Create and administer AI proposals, automation modes, and prompts through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newProposalOperation("create"))
	root.AddCommand(newProposalOperation("get"))
	root.AddCommand(newProposalOperation("approve"))
	root.AddCommand(newProposalOperation("reject"))
	root.AddCommand(newProposalOperation("cancel"))
	root.AddCommand(newProposalModeCommand())
	root.AddCommand(newProposalPromptCommand())
	return root
}

func newProposalOperation(operation string) *cobra.Command {
	options := &proposalOptions{expiresInMinutes: 30, modelVersion: "v1", promptVersion: "p1"}
	command := &cobra.Command{
		Use:   operation,
		Short: "Proposal " + operation,
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runProposalOperation(command, operation, options)
		},
	}
	addProposalAPICommonFlags(command, options)
	if operation == "create" {
		flags := command.Flags()
		flags.StringVar(&options.verificationID, "verification-id", "", "verification session id")
		flags.StringVar(&options.mode, "mode", "", "automation mode (assist, recommend, guardrailed_auto, human_required)")
		flags.StringSliceVar(&options.kinds, "kind", nil, "one or more allowed action kinds")
		flags.StringSliceVar(&options.evidenceRefs, "evidence-ref", nil, "session-owned evidence/observation references")
		flags.StringVar(&options.modelID, "model-id", "", "generative model id")
		flags.StringVar(&options.modelVersion, "model-version", "v1", "model version")
		flags.StringVar(&options.promptVersion, "prompt-version", "p1", "prompt version")
		flags.StringVar(&options.contextDigest, "context-digest", "", "redacted bounded-context digest (64 hex)")
		flags.IntVar(&options.expiresInMinutes, "expires-in-minutes", 30, "proposal lifetime in minutes")
	} else {
		flags := command.Flags()
		flags.StringVar(&options.identifier, "id", "", "proposal identifier")
	}
	if operation == "approve" || operation == "reject" || operation == "cancel" {
		command.Flags().Int64Var(&options.version, "expected-version", 0, "current proposal version")
	}
	if operation == "approve" {
		command.Flags().BoolVar(&options.humanApproved, "human-approved", false, "human approval granted")
	}
	if operation == "reject" || operation == "cancel" {
		command.Flags().BoolVar(&options.confirm, "confirm", false, "confirm this consequential operation")
	}
	return command
}

func newProposalModeCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "mode",
		Short: "Configure proposal automation modes through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newProposalModeOperation("get"))
	root.AddCommand(newProposalModeOperation("put"))
	return root
}

func newProposalModeOperation(operation string) *cobra.Command {
	options := &proposalOptions{}
	command := &cobra.Command{
		Use:   operation,
		Short: "Proposal automation mode " + operation,
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runProposalModeOperation(command, operation, options)
		},
	}
	addProposalAPICommonFlags(command, options)
	flags := command.Flags()
	flags.StringVar(&options.workflow, "workflow", "default", "workflow name")
	if operation == "put" {
		flags.StringVar(&options.mode, "mode", "", "automation mode (assist, recommend, guardrailed_auto, human_required)")
		flags.StringSliceVar(&options.kinds, "kind", nil, "allowed action kinds")
		flags.Int64Var(&options.version, "expected-version", 0, "current configuration version (0 for first)")
	}
	return command
}

func newProposalPromptCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "prompt",
		Short: "Create and inspect prompt versions through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	root.AddCommand(newProposalPromptOperation("create"))
	root.AddCommand(newProposalPromptOperation("get"))
	return root
}

func newProposalPromptOperation(operation string) *cobra.Command {
	options := &proposalOptions{}
	command := &cobra.Command{
		Use:   operation,
		Short: "Prompt " + operation,
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runProposalPromptOperation(command, operation, options)
		},
	}
	addProposalAPICommonFlags(command, options)
	flags := command.Flags()
	if operation == "create" {
		flags.StringVar(&options.content, "content", "", "prompt content")
		flags.StringVar(&options.modelID, "model-id", "", "generative model id")
	} else {
		flags.StringVar(&options.identifier, "id", "", "prompt identifier")
		flags.Int64Var(&options.version, "version", 0, "prompt version")
	}
	return command
}

func addProposalAPICommonFlags(command *cobra.Command, options *proposalOptions) {
	command.Flags().StringVar(&options.baseURL, "api-url", "", "Core API base URL (HTTPS, or local loopback HTTP)")
	command.Flags().StringVar(&options.keyFile, "api-key-file", "", "read API credential from this file (otherwise IDENQA_API_KEY)")
	command.Flags().StringVar(&options.retryKey, "idempotency-key", "", "stable retry key; reuse with identical input")
}

func runProposalOperation(command *cobra.Command, operation string, options *proposalOptions) error {
	method := http.MethodGet
	path := "/v1/proposals"
	var body any
	switch operation {
	case "create":
		method = http.MethodPost
		verification, err := id.ParseVerification(options.verificationID)
		if err != nil {
			return cli.UsageError(errors.New("a verification id is required"))
		}
		mode := strings.TrimSpace(options.mode)
		if mode == "" {
			return cli.UsageError(errors.New("a mode is required"))
		}
		if options.modelID == "" || !isHexDigest(options.contextDigest) {
			return cli.UsageError(errors.New("model-id and a 64-character context-digest are required"))
		}
		if options.expiresInMinutes < 1 || options.expiresInMinutes > 1440 {
			return cli.UsageError(errors.New("expires-in-minutes must be from 1 to 1440"))
		}
		actions := make([]map[string]any, 0, len(options.kinds))
		for _, kind := range options.kinds {
			actions = append(actions, map[string]any{"kind": kind, "args": map[string]any{}})
		}
		if len(actions) == 0 {
			return cli.UsageError(errors.New("at least one --kind is required"))
		}
		expiresAt := time.Now().UTC().Add(time.Duration(options.expiresInMinutes) * time.Minute).Format(time.RFC3339Nano)
		body = map[string]any{
			"verification_id": verification.String(),
			"mode":            mode,
			"actions":         actions,
			"evidence_refs":   options.evidenceRefs,
			"model_id":        options.modelID,
			"model_version":   options.modelVersion,
			"prompt_version":  options.promptVersion,
			"context_digest":  options.contextDigest,
			"expires_at":      expiresAt,
		}
	case "get":
		path += "/" + options.identifier
	default:
		if _, err := id.ParseProposal(options.identifier); err != nil {
			return cli.UsageError(errors.New("a proposal id is required"))
		}
		if options.version < 1 {
			return cli.UsageError(errors.New("expected-version must be positive"))
		}
		method = http.MethodPost
		path += "/" + options.identifier + "/" + operation
		switch operation {
		case "approve":
			body = map[string]any{"expected_version": options.version, "human_approved": options.humanApproved}
		case "reject", "cancel":
			if !options.confirm {
				return cli.UsageError(errors.New("this operation requires --confirm"))
			}
			body = map[string]any{"expected_version": options.version}
		}
	}
	return runProposalHTTP(command, options, method, path, body)
}

func runProposalModeOperation(command *cobra.Command, operation string, options *proposalOptions) error {
	if !proposalWorkflowPattern.MatchString(options.workflow) {
		return cli.UsageError(errors.New("a bounded workflow name is required"))
	}
	method := http.MethodGet
	path := "/v1/proposal-modes/" + options.workflow
	var body any
	if operation == "put" {
		method = http.MethodPut
		if options.mode == "" {
			return cli.UsageError(errors.New("a mode is required"))
		}
		if options.version < 0 {
			return cli.UsageError(errors.New("expected-version must be non-negative"))
		}
		body = map[string]any{"mode": options.mode, "allowed_kinds": options.kinds, "expected_version": options.version}
	}
	return runProposalHTTP(command, options, method, path, body)
}

func runProposalPromptOperation(command *cobra.Command, operation string, options *proposalOptions) error {
	method := http.MethodGet
	path := "/v1/prompts"
	var body any
	if operation == "create" {
		method = http.MethodPost
		if options.content == "" || options.modelID == "" {
			return cli.UsageError(errors.New("content and model-id are required"))
		}
		body = map[string]any{"content": options.content, "model_id": options.modelID}
	} else {
		if _, err := id.ParsePrompt(options.identifier); err != nil {
			return cli.UsageError(errors.New("a prompt id is required"))
		}
		if options.version < 1 {
			return cli.UsageError(errors.New("a positive prompt version is required"))
		}
		path = fmt.Sprintf("/v1/prompts/%s/%d", options.identifier, options.version)
	}
	return runProposalHTTP(command, options, method, path, body)
}

func runProposalHTTP(command *cobra.Command, options *proposalOptions, method, path string, body any) error {
	base, err := url.Parse(options.baseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "https" && (base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost" && base.Hostname() != "::1"))) {
		return cli.UsageError(errors.New("a valid HTTPS API URL or local loopback HTTP URL is required"))
	}
	key := os.Getenv("IDENQA_API_KEY")
	if options.keyFile != "" {
		file, err := os.Open(options.keyFile)
		if err != nil {
			return cli.RuntimeError("proposal", errors.New("read API credential file"))
		}
		material, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(material) > 4096 {
			clear(material)
			return cli.RuntimeError("proposal", errors.New("read bounded API credential file"))
		}
		key = strings.TrimSpace(string(material))
		clear(material)
	}
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return cli.UsageError(errors.New("an API credential file or IDENQA_API_KEY is required"))
	}
	var encoded []byte
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return cli.RuntimeError("proposal", err)
		}
		if options.retryKey == "" || len(options.retryKey) > 128 || strings.ContainsAny(options.retryKey, "\r\n") {
			return cli.UsageError(errors.New("a stable idempotency-key of at most 128 characters is required"))
		}
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + path
	request, err := http.NewRequestWithContext(command.Context(), method, target.String(), bytes.NewReader(encoded))
	if err != nil {
		return cli.RuntimeError("proposal", errors.New("construct proposal API request"))
	}
	request.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", fmt.Sprintf("%q", options.retryKey))
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return cli.RuntimeError("proposal", errors.New("proposal API request failed"))
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return cli.RuntimeError("proposal", errors.New("read bounded proposal API response"))
	}
	defer clear(raw)
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusNoContent {
		return cli.RuntimeError("proposal", fmt.Errorf("proposal API returned status %d", response.StatusCode))
	}
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	var result any
	if err := json.Unmarshal(raw, &result); err != nil {
		return cli.RuntimeError("proposal", errors.New("decode proposal API response"))
	}
	if err := json.NewEncoder(command.OutOrStdout()).Encode(result); err != nil {
		return cli.RuntimeError("proposal", err)
	}
	return nil
}

func isHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
