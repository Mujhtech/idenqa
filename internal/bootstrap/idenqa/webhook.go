package idenqa

import (
	"bytes"
	"encoding/base64"
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

type webhookOptions struct {
	baseURL, keyFile, identifier, targetURL, retryKey, reason, cursor, secretOut string
	version, overlap                                                             int64
	limit                                                                        int
	confirm                                                                      bool
}

func newWebhookCommand() *cobra.Command {
	root := &cobra.Command{
		Use:   "webhook",
		Short: "Configure and inspect webhooks through the public API",
		Args:  cli.UsageArgs(cobra.NoArgs),
	}
	for _, operation := range []string{"create", "list", "get", "rotate", "disable", "deliveries", "delivery", "attempts", "replay"} {
		root.AddCommand(newWebhookOperation(operation))
	}
	return root
}
func newWebhookOperation(operation string) *cobra.Command {
	options := &webhookOptions{limit: 25, overlap: 3600}
	command := &cobra.Command{
		Use:   operation,
		Short: "Webhook " + operation,
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runWebhookOperation(command, operation, options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.baseURL, "api-url", "", "Core API base URL (HTTPS, or local loopback HTTP)")
	flags.StringVar(&options.keyFile, "api-key-file", "", "read API credential from this file (otherwise IDENQA_API_KEY)")
	if operation != "create" && operation != "list" {
		flags.StringVar(&options.identifier, "id", "", "endpoint or delivery identifier")
	}
	if operation == "create" {
		flags.StringVar(&options.targetURL, "url", "", "HTTPS receiver URL")
	}
	if operation == "list" || operation == "deliveries" {
		flags.IntVar(&options.limit, "limit", 25, "page size from 1 to 100")
		flags.StringVar(&options.cursor, "cursor", "", "opaque continuation cursor")
	}
	if operation == "create" || operation == "rotate" || operation == "disable" || operation == "replay" {
		flags.StringVar(&options.retryKey, "idempotency-key", "", "stable retry key; reuse with identical input")
	}
	if operation == "rotate" || operation == "disable" {
		flags.Int64Var(&options.version, "expected-version", 0, "current endpoint version")
	}
	if operation == "rotate" {
		flags.Int64Var(&options.overlap, "overlap-seconds", 3600, "previous-key overlap from 1 to 86400 seconds")
	}
	if operation == "create" || operation == "rotate" {
		flags.StringVar(&options.secretOut, "secret-out", "", "create a new owner-only file for the display-once Base64URL secret")
	}
	if operation == "disable" || operation == "replay" {
		flags.StringVar(&options.reason, "reason", "", "bounded non-sensitive reason code")
	}
	if operation == "rotate" || operation == "disable" || operation == "replay" {
		flags.BoolVar(&options.confirm, "confirm", false, "confirm this consequential operation")
	}
	return command
}

func runWebhookOperation(command *cobra.Command, operation string, options *webhookOptions) error {
	base, err := url.Parse(options.baseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "https" && (base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost" && base.Hostname() != "::1"))) {
		return cli.UsageError(errors.New("a valid HTTPS API URL or local loopback HTTP URL is required"))
	}
	if (operation == "rotate" || operation == "disable" || operation == "replay") && !options.confirm {
		return cli.UsageError(errors.New("this webhook operation requires --confirm"))
	}
	if (operation == "rotate" || operation == "disable") && options.version < 1 {
		return cli.UsageError(errors.New("expected-version must be positive"))
	}
	if (operation == "list" || operation == "deliveries") && (options.limit < 1 || options.limit > 100) {
		return cli.UsageError(errors.New("limit must be from 1 to 100"))
	}
	path := "/v1/webhook-endpoints"
	method := http.MethodGet
	var body any
	if operation != "create" && operation != "list" {
		if operation == "delivery" || operation == "attempts" || operation == "replay" {
			if _, err := id.ParseDelivery(options.identifier); err != nil {
				return cli.UsageError(errors.New("a delivery id is required"))
			}
			path = "/v1/webhook-deliveries/" + options.identifier
		} else {
			if _, err := id.ParseWebhookEndpoint(options.identifier); err != nil {
				return cli.UsageError(errors.New("an endpoint id is required"))
			}
			path += "/" + options.identifier
		}
	}
	switch operation {
	case "create":
		method = http.MethodPost
		body = map[string]any{"url": options.targetURL}
	case "rotate":
		method = http.MethodPost
		path += "/rotate"
		body = map[string]any{"expected_version": options.version, "overlap_seconds": options.overlap}
	case "disable":
		method = http.MethodPost
		path += "/disable"
		body = map[string]any{"expected_version": options.version, "reason": options.reason}
	case "replay":
		method = http.MethodPost
		path += "/replay"
		body = map[string]any{"reason": options.reason}
	case "deliveries":
		path += "/deliveries"
	case "attempts":
		path += "/attempts"
	}
	if method == http.MethodPost && (options.retryKey == "" || len(options.retryKey) > 128 || strings.ContainsAny(options.retryKey, "\r\n")) {
		return cli.UsageError(errors.New("a stable idempotency-key of at most 128 characters is required"))
	}
	key := os.Getenv("IDENQA_API_KEY")
	if options.keyFile != "" {
		file, err := os.Open(options.keyFile)
		if err != nil {
			return cli.RuntimeError("webhook", errors.New("read API credential file"))
		}
		material, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(material) > 4096 {
			clear(material)
			return cli.RuntimeError("webhook", errors.New("read bounded API credential file"))
		}
		key = strings.TrimSpace(string(material))
		clear(material)
	}
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return cli.UsageError(errors.New("an API credential file or IDENQA_API_KEY is required"))
	}
	var secretFile *os.File
	keepSecret := false
	if operation == "create" || operation == "rotate" {
		if options.secretOut == "" {
			return cli.UsageError(errors.New("secret-out is required for display-once secret delivery"))
		}
		secretFile, err = os.OpenFile(options.secretOut, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return cli.RuntimeError("webhook", errors.New("create new owner-only webhook secret file"))
		}
		defer func() {
			if secretFile != nil {
				_ = secretFile.Close()
			}
			if !keepSecret {
				_ = os.Remove(options.secretOut)
			}
		}()
	}
	var encoded []byte
	if body != nil {
		encoded, err = json.Marshal(body)
		if err != nil {
			return err
		}
	}
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + path
	if operation == "list" || operation == "deliveries" {
		query := url.Values{"limit": {strconv.Itoa(options.limit)}}
		if options.cursor != "" {
			query.Set("cursor", options.cursor)
		}
		target.RawQuery = query.Encode()
	}
	request, err := http.NewRequestWithContext(command.Context(), method, target.String(), bytes.NewReader(encoded))
	if err != nil {
		return cli.RuntimeError("webhook", errors.New("construct webhook API request"))
	}
	request.Header.Set("Authorization", "Bearer "+key)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", strconv.Quote(options.retryKey))
	}
	client := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return cli.RuntimeError("webhook", errors.New("webhook API request failed"))
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20+1))
	if err != nil || len(raw) > 1<<20 {
		return cli.RuntimeError("webhook", errors.New("read bounded webhook API response"))
	}
	defer clear(raw)
	if response.StatusCode != http.StatusOK {
		return cli.RuntimeError("webhook", fmt.Errorf("webhook API returned status %d", response.StatusCode))
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return cli.RuntimeError("webhook", errors.New("decode webhook API response"))
	}
	if material, ok := result["signing_secret"]; ok {
		delete(result, "signing_secret")
		var secret string
		if err := json.Unmarshal(material, &secret); err != nil || secretFile == nil {
			return cli.RuntimeError("webhook", errors.New("invalid signing-secret response"))
		}
		decoded, decodeErr := base64.RawURLEncoding.Strict().DecodeString(secret)
		valid := decodeErr == nil && len(decoded) == 32
		clear(decoded)
		clear(material)
		if !valid {
			return cli.RuntimeError("webhook", errors.New("invalid signing-secret response"))
		}
		if _, err := io.WriteString(secretFile, secret+"\n"); err != nil {
			return cli.RuntimeError("webhook", errors.New("write webhook signing-secret file"))
		}
		if err := secretFile.Close(); err != nil {
			return cli.RuntimeError("webhook", errors.New("close webhook signing-secret file"))
		}
		secretFile = nil
		keepSecret = true
	}
	if err := json.NewEncoder(command.OutOrStdout()).Encode(result); err != nil {
		return cli.RuntimeError("webhook", err)
	}
	return nil
}
