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
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

var proposalWorkflowPattern = regexp.MustCompile(`^[a-z0-9._-]{1,64}$`)
var proposalModelPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{0,63}$`)

type proposalOptions struct {
	baseURL, keyFile, identifier, workflow, mode, modelID, modelVersion, promptVersion string
	contextDigest, retryKey, content                                                   string
	modelRegistryID, promptRegistryID, digest, reason, from, to                        string
	assessment, riskLevel, before                                                      string
	verificationID                                                                     string
	kinds                                                                              []string
	evidenceRefs                                                                       []string
	expiresInMinutes                                                                   int
	version                                                                            int64
	modelRegistryVersion, promptRegistryVersion, activationRevision, targetRevision    int64
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
	root.AddCommand(newProposalExecuteCommand())
	root.AddCommand(newProposalModeCommand())
	root.AddCommand(newProposalPromptCommand())
	root.AddCommand(newProposalModelCommand())
	root.AddCommand(newProposalActivationCommand())
	root.AddCommand(newProposalUsageCommand())
	root.AddCommand(newProposalImpactCommand())
	return root
}

func newProposalImpactCommand() *cobra.Command {
	root := &cobra.Command{Use: "impact", Short: "Create and inspect AI impact assessments", Args: cli.UsageArgs(cobra.NoArgs)}
	for _, operation := range []string{"create", "get", "list"} {
		op := operation
		options := &proposalOptions{}
		command := &cobra.Command{Use: op, Short: "Proposal impact assessment " + op, Args: cli.UsageArgs(cobra.NoArgs), RunE: func(command *cobra.Command, _ []string) error {
			return runProposalImpactOperation(command, op, options)
		}}
		addProposalAPICommonFlags(command, options)
		switch op {
		case "create":
			command.Flags().StringVar(&options.mode, "kind", "", "bounded proposal action kind")
			command.Flags().StringVar(&options.assessment, "assessment", "", "bounded impact assessment text")
			command.Flags().StringVar(&options.riskLevel, "risk-level", "", "risk level: low, medium, high, or critical")
		case "get":
			command.Flags().StringVar(&options.identifier, "id", "", "impact assessment identifier")
		case "list":
			command.Flags().StringVar(&options.before, "before", "", "exclusive RFC3339 timestamp cursor")
			command.Flags().IntVar(&options.expiresInMinutes, "limit", 25, "page size from 1 to 100")
		}
		root.AddCommand(command)
	}
	return root
}

func runProposalImpactOperation(command *cobra.Command, operation string, options *proposalOptions) error {
	path := "/v1/proposal-impact-assessments"
	method := http.MethodGet
	var body any
	switch operation {
	case "create":
		if options.mode == "" || strings.TrimSpace(options.assessment) == "" || options.riskLevel == "" {
			return cli.UsageError(errors.New("kind, assessment, and risk-level are required"))
		}
		method = http.MethodPost
		body = map[string]any{"kind": options.mode, "assessment": options.assessment, "risk_level": options.riskLevel}
	case "get":
		if !strings.HasPrefix(options.identifier, "imp_") {
			return cli.UsageError(errors.New("a valid impact assessment id is required"))
		}
		path += "/" + url.PathEscape(options.identifier)
	case "list":
		if options.expiresInMinutes < 1 || options.expiresInMinutes > 100 {
			return cli.UsageError(errors.New("limit must be from 1 to 100"))
		}
		query := url.Values{"limit": {strconv.Itoa(options.expiresInMinutes)}}
		if options.before != "" {
			if _, err := time.Parse(time.RFC3339Nano, options.before); err != nil {
				return cli.UsageError(errors.New("before must be RFC3339"))
			}
			query.Set("before", options.before)
		}
		return runProposalHTTP(command, options, method, path+"?"+query.Encode(), nil)
	}
	return runProposalHTTP(command, options, method, path, body)
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
		flags.StringVar(&options.modelRegistryID, "model-registry-id", "", "pinned model registry id")
		flags.Int64Var(&options.modelRegistryVersion, "model-registry-version", 0, "pinned model registry version")
		flags.StringVar(&options.promptRegistryID, "prompt-id", "", "pinned prompt registry id")
		flags.Int64Var(&options.promptRegistryVersion, "prompt-registry-version", 0, "pinned prompt registry version")
		flags.Int64Var(&options.activationRevision, "activation-revision", 0, "pinned activation revision")
	}
	return command
}

func newProposalModelCommand() *cobra.Command {
	root := &cobra.Command{Use: "model", Short: "Create and inspect generative-model registry records", Args: cli.UsageArgs(cobra.NoArgs)}
	for _, operation := range []string{"create", "get"} {
		op := operation
		options := &proposalOptions{}
		command := &cobra.Command{Use: op, Short: "Proposal model " + op, Args: cli.UsageArgs(cobra.NoArgs), RunE: func(command *cobra.Command, _ []string) error { return runProposalModelOperation(command, op, options) }}
		addProposalAPICommonFlags(command, options)
		if op == "create" {
			command.Flags().StringVar(&options.modelID, "model-id", "", "provider-neutral logical model id")
			command.Flags().StringVar(&options.digest, "digest", "", "model revision SHA-256 digest")
		} else {
			command.Flags().StringVar(&options.modelRegistryID, "id", "", "model registry id")
			command.Flags().Int64Var(&options.modelRegistryVersion, "version", 0, "model registry version")
		}
		root.AddCommand(command)
	}
	return root
}

func newProposalActivationCommand() *cobra.Command {
	root := &cobra.Command{Use: "activation", Short: "Administer audited proposal model-route activation", Args: cli.UsageArgs(cobra.NoArgs)}
	for _, operation := range []string{"put", "get", "history", "retire", "rollback"} {
		op := operation
		options := &proposalOptions{}
		command := &cobra.Command{Use: op, Short: "Proposal activation " + op, Args: cli.UsageArgs(cobra.NoArgs), RunE: func(command *cobra.Command, _ []string) error {
			return runProposalActivationOperation(command, op, options)
		}}
		addProposalAPICommonFlags(command, options)
		flags := command.Flags()
		flags.StringVar(&options.workflow, "workflow", "default", "workflow name")
		if op == "put" {
			flags.StringVar(&options.modelRegistryID, "model-registry-id", "", "model registry id")
			flags.Int64Var(&options.modelRegistryVersion, "model-registry-version", 0, "model registry version")
			flags.StringVar(&options.promptRegistryID, "prompt-id", "", "prompt registry id")
			flags.Int64Var(&options.promptRegistryVersion, "prompt-version", 0, "prompt registry version")
			flags.StringVar(&options.modelVersion, "upstream-model-version", "", "exact upstream model version")
			flags.StringVar(&options.promptVersion, "route-prompt-version", "", "reviewed route prompt version")
		}
		if op == "put" || op == "retire" || op == "rollback" {
			flags.Int64Var(&options.activationRevision, "expected-revision", 0, "current activation revision")
			flags.StringVar(&options.reason, "reason", "", "bounded lifecycle reason")
		}
		if op == "rollback" {
			flags.Int64Var(&options.targetRevision, "target-revision", 0, "prior active revision to republish")
		}
		root.AddCommand(command)
	}
	return root
}

func newProposalUsageCommand() *cobra.Command {
	options := &proposalOptions{}
	command := &cobra.Command{Use: "usage", Short: "Report content-free proposal generation usage", Args: cli.UsageArgs(cobra.NoArgs), RunE: func(command *cobra.Command, _ []string) error { return runProposalUsage(command, options) }}
	addProposalAPICommonFlags(command, options)
	command.Flags().StringVar(&options.from, "from", "", "inclusive RFC3339 timestamp")
	command.Flags().StringVar(&options.to, "to", "", "exclusive RFC3339 timestamp")
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

func newProposalExecuteCommand() *cobra.Command {
	options := &proposalOptions{}
	command := &cobra.Command{
		Use:   "execute <commandID>",
		Short: "Proposal accepted-command execute",
		Args:  cli.UsageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, args []string) error {
			accepted, err := id.ParseAcceptedCommand(args[0])
			if err != nil {
				return cli.UsageError(errors.New("a valid accepted-command id is required"))
			}
			return runProposalHTTP(command, options, http.MethodPost, "/v1/accepted-commands/"+accepted.String()+"/execute", nil)
		},
	}
	command.Flags().StringVar(&options.baseURL, "api-url", "", "Core API base URL (HTTPS, or local loopback HTTP)")
	command.Flags().StringVar(&options.keyFile, "api-key-file", "", "read API credential from this file (otherwise IDENQA_API_KEY)")
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
		if options.modelRegistryID != "" || options.promptRegistryID != "" || options.activationRevision != 0 {
			if _, err := id.ParseModel(options.modelRegistryID); err != nil {
				return cli.UsageError(errors.New("a valid model-registry-id is required"))
			}
			if _, err := id.ParsePrompt(options.promptRegistryID); err != nil {
				return cli.UsageError(errors.New("a valid prompt-id is required"))
			}
			if options.modelRegistryVersion < 1 || options.promptRegistryVersion < 1 || options.activationRevision < 1 {
				return cli.UsageError(errors.New("positive registry and activation versions are required"))
			}
			body.(map[string]any)["model_registry_id"] = options.modelRegistryID
			body.(map[string]any)["model_registry_version"] = options.modelRegistryVersion
			body.(map[string]any)["prompt_id"] = options.promptRegistryID
			body.(map[string]any)["prompt_registry_version"] = options.promptRegistryVersion
			body.(map[string]any)["activation_revision"] = options.activationRevision
		}
	}
	return runProposalHTTP(command, options, method, path, body)
}

func runProposalModelOperation(command *cobra.Command, operation string, options *proposalOptions) error {
	method, path := http.MethodGet, "/v1/proposal-models"
	var body any
	if operation == "create" {
		if !proposalModelPattern.MatchString(options.modelID) || !isHexDigest(options.digest) {
			return cli.UsageError(errors.New("a logical model-id and SHA-256 digest are required"))
		}
		method, body = http.MethodPost, map[string]any{"model_id": options.modelID, "digest": options.digest}
	} else {
		if _, err := id.ParseModel(options.modelRegistryID); err != nil || options.modelRegistryVersion < 1 {
			return cli.UsageError(errors.New("a model registry id and positive version are required"))
		}
		path = fmt.Sprintf("%s/%s/%d", path, options.modelRegistryID, options.modelRegistryVersion)
	}
	return runProposalHTTP(command, options, method, path, body)
}

func runProposalActivationOperation(command *cobra.Command, operation string, options *proposalOptions) error {
	if !proposalWorkflowPattern.MatchString(options.workflow) {
		return cli.UsageError(errors.New("a bounded workflow name is required"))
	}
	method, path := http.MethodGet, "/v1/proposal-activations/"+options.workflow
	var body any
	switch operation {
	case "put":
		if _, err := id.ParseModel(options.modelRegistryID); err != nil {
			return cli.UsageError(errors.New("a model registry id is required"))
		}
		if _, err := id.ParsePrompt(options.promptRegistryID); err != nil {
			return cli.UsageError(errors.New("a prompt registry id is required"))
		}
		if options.modelRegistryVersion < 1 || options.promptRegistryVersion < 1 || options.modelVersion == "" || options.promptVersion == "" || options.activationRevision < 0 {
			return cli.UsageError(errors.New("exact route versions and a non-negative expected revision are required"))
		}
		method = http.MethodPut
		body = map[string]any{"model_registry_id": options.modelRegistryID, "model_registry_version": options.modelRegistryVersion, "prompt_registry_id": options.promptRegistryID, "prompt_registry_version": options.promptRegistryVersion, "model_version": options.modelVersion, "prompt_version": options.promptVersion, "expected_revision": options.activationRevision, "reason": options.reason}
	case "history":
		path += "/history"
	case "retire":
		if options.activationRevision < 1 {
			return cli.UsageError(errors.New("a positive expected revision is required"))
		}
		method, path, body = http.MethodPost, path+"/retire", map[string]any{"expected_revision": options.activationRevision, "reason": options.reason}
	case "rollback":
		if options.activationRevision < 1 || options.targetRevision < 1 {
			return cli.UsageError(errors.New("positive expected and target revisions are required"))
		}
		method, path, body = http.MethodPost, path+"/rollback", map[string]any{"expected_revision": options.activationRevision, "target_revision": options.targetRevision, "reason": options.reason}
	}
	return runProposalHTTP(command, options, method, path, body)
}

func runProposalUsage(command *cobra.Command, options *proposalOptions) error {
	from, err := time.Parse(time.RFC3339Nano, options.from)
	if err != nil {
		return cli.UsageError(errors.New("a valid --from timestamp is required"))
	}
	to, err := time.Parse(time.RFC3339Nano, options.to)
	if err != nil || !to.After(from) || to.Sub(from) > 366*24*time.Hour {
		return cli.UsageError(errors.New("--to must be after --from by at most 366 days"))
	}
	path := "/v1/proposal-usage?from=" + url.QueryEscape(from.UTC().Format(time.RFC3339Nano)) + "&to=" + url.QueryEscape(to.UTC().Format(time.RFC3339Nano))
	return runProposalHTTP(command, options, http.MethodGet, path, nil)
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
	requestPath, rawQuery, _ := strings.Cut(path, "?")
	target.Path = strings.TrimRight(base.Path, "/") + requestPath
	target.RawQuery = rawQuery
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
