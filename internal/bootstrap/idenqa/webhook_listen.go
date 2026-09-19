package idenqa

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/delivery"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/spf13/cobra"
)

const (
	listenSecretBytes       = 32
	listenMaximumFrame      = 1 << 20
	listenMaximumResponse   = 4096
	listenMaximumDiscovery  = 1 << 20
	listenDiscoveryPageSize = 100
	listenMaximumHeaders    = 32
	listenMaximumHeaderName = 64
	listenMaximumHeaderSize = 1024
	listenDeliveredWindow   = 1024
)

type webhookListenOptions struct {
	baseURL, keyFile, eventTypes, forwardTo, secretFile, secretOut string
	forwardHeaders                                                 []string
	printSecret, jsonOutput, loadFromWebhooksAPI, thin, backfill   bool
	skipVerify                                                     bool
}

type forwardHeader struct {
	name  string
	value string
}

func newWebhookListenCommand() *cobra.Command {
	options := &webhookListenOptions{}
	command := &cobra.Command{
		Use:   "listen",
		Short: "Stream live webhook events and optionally forward them to a local URL",
		Args:  cli.UsageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return runWebhookListen(command, options)
		},
	}
	flags := command.Flags()
	flags.StringVar(&options.baseURL, "api-url", "", "Core API base URL (HTTPS, or local loopback HTTP)")
	flags.StringVar(&options.keyFile, "api-key-file", "", "read API credential from this file (otherwise IDENQA_API_KEY)")
	flags.StringVar(&options.eventTypes, "event-types", "", "comma-separated catalogue event names, or * for all")
	flags.StringVar(&options.forwardTo, "forward-to", "", "forward each event to this local receiver URL")
	flags.StringArrayVar(&options.forwardHeaders, "forward-header", nil, "custom Name: Value header added to each forwarded request; repeatable")
	flags.StringVar(&options.secretFile, "secret-file", "", "existing Base64URL signing secret used to sign forwards")
	flags.StringVar(&options.secretOut, "secret-out", "", "create a new owner-only signing secret for forwards")
	flags.BoolVar(&options.printSecret, "print-secret", false, "print the configured signing secret and exit")
	flags.BoolVar(&options.jsonOutput, "json", false, "print each canonical event envelope as JSON")
	flags.BoolVar(&options.loadFromWebhooksAPI, "load-from-webhooks-api", false, "discover event types from enabled webhook endpoints")
	flags.BoolVar(&options.thin, "thin", false, "forward reference-only payloads containing only reference keys")
	flags.BoolVar(&options.backfill, "backfill", false, "start from retained history with Last-Event-ID: 0")
	flags.BoolVar(&options.skipVerify, "skip-verify", false, "allow a self-signed HTTPS forward target (development only)")
	return command
}

func runWebhookListen(command *cobra.Command, options *webhookListenOptions) error {
	if options.loadFromWebhooksAPI && options.eventTypes != "" {
		return cli.UsageError(errors.New("event-types and load-from-webhooks-api are mutually exclusive"))
	}
	if options.skipVerify && options.forwardTo == "" {
		return cli.UsageError(errors.New("skip-verify requires forward-to"))
	}
	var selection []string
	if !options.loadFromWebhooksAPI {
		parsed, err := parseEventTypes(options.eventTypes)
		if err != nil {
			return cli.UsageError(err)
		}
		selection = parsed
	}
	headers, err := parseForwardHeaders(options.forwardHeaders)
	if err != nil {
		return cli.UsageError(err)
	}
	stdout, stderr := command.OutOrStdout(), command.ErrOrStderr()
	needsSecret := options.forwardTo != "" || options.printSecret
	if needsSecret && (options.secretFile == "") == (options.secretOut == "") {
		return cli.UsageError(errors.New("exactly one of secret-file or secret-out is required"))
	}
	var secret []byte
	if needsSecret && options.secretFile != "" {
		secret, err = readSigningSecret(options.secretFile)
		if err != nil {
			return err
		}
		defer clear(secret)
	}
	if options.printSecret {
		if secret == nil {
			if secret, err = createListenSecret(options.secretOut, false, stdout); err != nil {
				return err
			}
			defer clear(secret)
		}
		if _, err := fmt.Fprintln(stdout, base64.RawURLEncoding.EncodeToString(secret)); err != nil {
			return cli.RuntimeError("webhook listen", err)
		}
		return nil
	}
	base, err := url.Parse(options.baseURL)
	if err != nil || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Scheme != "https" && (base.Scheme != "http" || (base.Hostname() != "127.0.0.1" && base.Hostname() != "localhost" && base.Hostname() != "::1"))) {
		return cli.UsageError(errors.New("a valid HTTPS API URL or local loopback HTTP URL is required"))
	}
	key, err := listenCredential(options.keyFile)
	if err != nil {
		return err
	}
	var target string
	if options.forwardTo != "" {
		if secret == nil {
			if secret, err = createListenSecret(options.secretOut, true, stdout); err != nil {
				return err
			}
			defer clear(secret)
		}
		if target, err = listenForwardTarget(options.forwardTo); err != nil {
			return cli.UsageError(err)
		}
	}
	client := &http.Client{Timeout: 0, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	forwardClient := client
	if options.skipVerify {
		// Development-only escape hatch for a self-signed local receiver. The
		// Core API stream and discovery calls always keep certificate validation.
		forwardClient = &http.Client{
			Timeout:       0,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
			Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, //nolint:gosec // explicit --skip-verify for a loopback development receiver
		}
	}
	if options.loadFromWebhooksAPI {
		if selection, err = discoverWebhookEventTypes(command.Context(), client, base, key); err != nil {
			return err
		}
	}
	backoff := time.Second
	lastEventID := ""
	if options.backfill {
		lastEventID = "0"
	}
	seen := newListenSeen(listenDeliveredWindow)
	for {
		connected, streamErr := streamWebhookEvents(command.Context(), client, base, key, selection, lastEventID, func(frameID string, body []byte) error {
			lastEventID = frameID
			var envelope struct {
				ID        string    `json:"id"`
				Type      string    `json:"type"`
				CreatedAt time.Time `json:"created_at"`
			}
			if err := json.Unmarshal(body, &envelope); err != nil {
				return fmt.Errorf("decode streamed event: %w", err)
			}
			if !seen.add(envelope.ID) {
				return nil
			}
			projected := body
			if options.thin {
				thinned, err := thinWebhookEvent(body)
				if err != nil {
					return fmt.Errorf("project streamed event: %w", err)
				}
				projected = thinned
			}
			if options.jsonOutput {
				if _, err := fmt.Fprintln(stdout, string(projected)); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintf(stdout, "%s  --> %s [%s]\n", envelope.CreatedAt.UTC().Format(time.RFC3339), envelope.Type, envelope.ID); err != nil {
				return err
			}
			if target != "" {
				if err := forwardWebhookEvent(command.Context(), forwardClient, target, secret, headers, envelope.ID, projected); err != nil {
					_, _ = fmt.Fprintf(stderr, "forward failed for %s: %v\n", envelope.ID, err)
				}
			}
			return nil
		})
		if command.Context().Err() != nil {
			return nil
		}
		var eventErr *listenEventError
		if errors.As(streamErr, &eventErr) {
			return cli.RuntimeError("webhook listen", eventErr.cause)
		}
		if streamErr != nil {
			var status *listenStatusError
			if errors.As(streamErr, &status) {
				switch status.Status {
				case http.StatusUnauthorized, http.StatusForbidden:
					return cli.RuntimeError("webhook listen", fmt.Errorf("event stream rejected the API credential (status %d)", status.Status))
				case http.StatusBadRequest, http.StatusNotFound:
					lastEventID = ""
					if options.backfill {
						lastEventID = "0"
					}
				}
			}
			_, _ = fmt.Fprintf(stderr, "event stream disconnected: %v\n", streamErr)
		}
		if connected {
			backoff = time.Second
		}
		select {
		case <-command.Context().Done():
			return nil
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

type listenEventError struct{ cause error }

func (failure *listenEventError) Error() string { return failure.cause.Error() }
func (failure *listenEventError) Unwrap() error { return failure.cause }

// listenSeen suppresses bounded duplicate event IDs across reconnects. The
// receiver remains the authoritative deduplicator.
type listenSeen struct {
	seen  map[string]struct{}
	order []string
	next  int
}

func newListenSeen(capacity int) *listenSeen {
	return &listenSeen{seen: make(map[string]struct{}, capacity), order: make([]string, 0, capacity)}
}

func (set *listenSeen) add(identifier string) bool {
	if _, exists := set.seen[identifier]; exists {
		return false
	}
	if len(set.order) < cap(set.order) {
		set.order = append(set.order, identifier)
	} else {
		delete(set.seen, set.order[set.next])
		set.order[set.next] = identifier
		set.next = (set.next + 1) % cap(set.order)
	}
	set.seen[identifier] = struct{}{}
	return true
}

type listenStatusError struct {
	Status  int
	Message string
}

func (failure *listenStatusError) Error() string {
	return fmt.Sprintf("event stream returned status %d: %s", failure.Status, failure.Message)
}

func streamWebhookEvents(ctx context.Context, client *http.Client, base *url.URL, credential string, selection []string, lastEventID string, handle func(string, []byte) error) (bool, error) {
	target := *base
	target.Path = strings.TrimRight(base.Path, "/") + "/v1/webhook-events/stream"
	if len(selection) > 0 {
		target.RawQuery = url.Values{"event_types": {strings.Join(selection, ",")}}.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return false, errors.New("construct event stream request")
	}
	request.Header.Set("Accept", "text/event-stream")
	request.Header.Set("Authorization", "Bearer "+credential)
	if lastEventID != "" {
		request.Header.Set("Last-Event-ID", lastEventID)
	}
	response, err := client.Do(request)
	if err != nil {
		return false, errors.New("event stream request failed")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(response.Body, listenMaximumResponse+1))
		message := strings.TrimSpace(string(raw))
		if len(message) > listenMaximumResponse {
			message = ""
		}
		return false, &listenStatusError{Status: response.StatusCode, Message: message}
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64*1024), listenMaximumFrame)
	var frameID string
	var data []byte
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) > 0 {
				if err := handle(frameID, data); err != nil {
					return true, &listenEventError{cause: err}
				}
			}
			frameID, data = "", nil
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "id":
			frameID = value
		case "data":
			if len(data) > 0 {
				data = append(data, '\n')
			}
			data = append(data, value...)
		}
	}
	if err := scanner.Err(); err != nil {
		return true, errors.New("read event stream")
	}
	return true, nil
}

func forwardWebhookEvent(ctx context.Context, client *http.Client, target string, secret []byte, headers []forwardHeader, encodedID string, body []byte) error {
	eventID, err := id.ParseEvent(encodedID)
	if err != nil {
		return errors.New("streamed event identifier is invalid")
	}
	signature, err := delivery.Sign(secret, eventID, time.Now().UTC(), body)
	if err != nil {
		return errors.New("sign forwarded event")
	}
	forwardContext, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(forwardContext, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return errors.New("construct forward request")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idenqa-Signature", signature.Value)
	request.Header.Set("Idenqa-Timestamp", signature.Timestamp)
	request.Header.Set("Idenqa-Event-ID", signature.EventID)
	for _, header := range headers {
		request.Header.Set(header.name, header.value)
	}
	response, err := client.Do(request)
	if err != nil {
		return errors.New("forward request failed")
	}
	defer func() { _ = response.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, listenMaximumResponse+1))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("receiver returned status %d", response.StatusCode)
	}
	return nil
}

var reservedForwardHeaders = map[string]struct{}{
	"connection":        {},
	"content-length":    {},
	"content-type":      {},
	"host":              {},
	"idenqa-event-id":   {},
	"idenqa-signature":  {},
	"idenqa-timestamp":  {},
	"transfer-encoding": {},
}

func parseForwardHeaders(entries []string) ([]forwardHeader, error) {
	if len(entries) > listenMaximumHeaders {
		return nil, fmt.Errorf("forward-header accepts at most %d entries", listenMaximumHeaders)
	}
	headers := make([]forwardHeader, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		name, value, found := strings.Cut(entry, ":")
		if !found || !validHeaderName(name) {
			return nil, fmt.Errorf("forward-header %q must use a valid HTTP header name", entry)
		}
		value = strings.TrimPrefix(value, " ")
		if value == "" || len(value) > listenMaximumHeaderSize || strings.ContainsAny(value, "\r\n") {
			return nil, fmt.Errorf("forward-header %q must have a non-empty single-line value of at most %d bytes", entry, listenMaximumHeaderSize)
		}
		lower := strings.ToLower(name)
		if _, reserved := reservedForwardHeaders[lower]; reserved {
			return nil, fmt.Errorf("forward-header %q overrides a reserved header", name)
		}
		if _, duplicate := seen[lower]; duplicate {
			return nil, fmt.Errorf("forward-header %q repeats a header name", name)
		}
		seen[lower] = struct{}{}
		headers = append(headers, forwardHeader{name: name, value: value})
	}
	return headers, nil
}

func validHeaderName(name string) bool {
	if name == "" || len(name) > listenMaximumHeaderName {
		return false
	}
	for index := 0; index < len(name); index++ {
		character := name[index]
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') {
			continue
		}
		if !strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			return false
		}
	}
	return true
}

func thinWebhookEvent(body []byte) ([]byte, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, errors.New("decode projected event envelope")
	}
	projected := make(map[string]json.RawMessage, 7)
	for _, key := range []string{"id", "type", "schema_version", "created_at", "tenant_id", "region"} {
		if value, ok := envelope[key]; ok {
			projected[key] = value
		}
	}
	references := map[string]json.RawMessage{}
	if raw, ok := envelope["data"]; ok {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err == nil {
			for key, value := range fields {
				if key == "id" || key == "type" || strings.HasSuffix(key, "_id") {
					references[key] = value
				}
			}
		}
	}
	encoded, err := json.Marshal(references)
	if err != nil {
		return nil, errors.New("encode projected event references")
	}
	projected["data"] = encoded
	result, err := json.Marshal(projected)
	if err != nil {
		return nil, errors.New("encode projected event envelope")
	}
	return result, nil
}

type webhookDiscoveryEndpoint struct {
	EventTypes []string `json:"event_types"`
	DisabledAt *string  `json:"disabled_at"`
}

type webhookDiscoveryPage struct {
	Data []webhookDiscoveryEndpoint `json:"data"`
	Page struct {
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	} `json:"page"`
}

func discoverWebhookEventTypes(ctx context.Context, client *http.Client, base *url.URL, credential string) ([]string, error) {
	selected := make(map[string]struct{})
	wildcard := false
	cursor := ""
	for {
		target := *base
		target.Path = strings.TrimRight(base.Path, "/") + "/v1/webhook-endpoints"
		query := url.Values{"limit": {strconv.Itoa(listenDiscoveryPageSize)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		target.RawQuery = query.Encode()
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, cli.RuntimeError("webhook listen", errors.New("construct webhook endpoint discovery request"))
		}
		request.Header.Set("Authorization", "Bearer "+credential)
		response, err := client.Do(request)
		if err != nil {
			return nil, cli.RuntimeError("webhook listen", errors.New("webhook endpoint discovery request failed"))
		}
		raw, readErr := io.ReadAll(io.LimitReader(response.Body, listenMaximumDiscovery+1))
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil || len(raw) > listenMaximumDiscovery {
			return nil, cli.RuntimeError("webhook listen", errors.New("read bounded webhook endpoint discovery response"))
		}
		if response.StatusCode != http.StatusOK {
			return nil, cli.RuntimeError("webhook listen", fmt.Errorf("webhook endpoint discovery returned status %d", response.StatusCode))
		}
		var page webhookDiscoveryPage
		if err := json.Unmarshal(raw, &page); err != nil {
			return nil, cli.RuntimeError("webhook listen", errors.New("decode webhook endpoint discovery response"))
		}
		for _, endpoint := range page.Data {
			if endpoint.DisabledAt != nil {
				continue
			}
			for _, eventType := range endpoint.EventTypes {
				if eventType == "" {
					continue
				}
				if eventType == "*" {
					wildcard = true
				}
				selected[eventType] = struct{}{}
			}
		}
		if !page.Page.HasMore {
			break
		}
		if page.Page.NextCursor == "" || page.Page.NextCursor == cursor {
			return nil, cli.RuntimeError("webhook listen", errors.New("webhook endpoint discovery returned an unusable cursor"))
		}
		cursor = page.Page.NextCursor
	}
	if wildcard {
		return []string{"*"}, nil
	}
	if len(selected) == 0 {
		return nil, cli.RuntimeError("webhook listen", errors.New("no enabled webhook endpoints define event types"))
	}
	result := make([]string, 0, len(selected))
	for eventType := range selected {
		result = append(result, eventType)
	}
	sort.Strings(result)
	return result, nil
}

func listenCredential(keyFile string) (string, error) {
	key := os.Getenv("IDENQA_API_KEY")
	if keyFile != "" {
		file, err := os.Open(keyFile) //nolint:gosec // operator-supplied credential path, not request input
		if err != nil {
			return "", cli.RuntimeError("webhook listen", errors.New("read API credential file"))
		}
		material, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil || closeErr != nil || len(material) > 4096 {
			clear(material)
			return "", cli.RuntimeError("webhook listen", errors.New("read bounded API credential file"))
		}
		key = strings.TrimSpace(string(material))
		clear(material)
	}
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return "", cli.UsageError(errors.New("an API credential file or IDENQA_API_KEY is required"))
	}
	return key, nil
}

func createListenSecret(path string, announce bool, stdout io.Writer) ([]byte, error) {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600) //nolint:gosec // operator-supplied secret path, not request input
	if err != nil {
		return nil, cli.RuntimeError("webhook listen", errors.New("create new owner-only signing secret file"))
	}
	keep := false
	defer func() {
		if file != nil {
			_ = file.Close()
		}
		if !keep {
			_ = os.Remove(path)
		}
	}()
	secret := make([]byte, listenSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return nil, cli.RuntimeError("webhook listen", errors.New("generate signing secret"))
	}
	encoded := base64.RawURLEncoding.EncodeToString(secret)
	if _, err := io.WriteString(file, encoded+"\n"); err != nil {
		clear(secret)
		return nil, cli.RuntimeError("webhook listen", errors.New("write signing secret file"))
	}
	if err := file.Close(); err != nil {
		clear(secret)
		return nil, cli.RuntimeError("webhook listen", errors.New("close signing secret file"))
	}
	file = nil
	keep = true
	if announce {
		if _, err := fmt.Fprintf(stdout, "webhook signing secret: %s\n", encoded); err != nil {
			clear(secret)
			return nil, cli.RuntimeError("webhook listen", err)
		}
	}
	return secret, nil
}

func readSigningSecret(path string) ([]byte, error) {
	file, err := os.Open(path) //nolint:gosec // operator-supplied secret path, not request input
	if err != nil {
		return nil, cli.RuntimeError("webhook listen", errors.New("read signing secret file"))
	}
	defer func() { _ = file.Close() }()
	material, err := io.ReadAll(io.LimitReader(file, 4097))
	if err != nil || len(material) > 4096 {
		clear(material)
		return nil, cli.RuntimeError("webhook listen", errors.New("read bounded signing secret file"))
	}
	defer clear(material)
	if len(material) == listenSecretBytes {
		return append([]byte(nil), material...), nil
	}
	trimmed := strings.TrimSpace(string(material))
	for _, encoding := range []*base64.Encoding{base64.RawURLEncoding.Strict(), base64.URLEncoding.Strict()} {
		if decoded, err := encoding.DecodeString(trimmed); err == nil && len(decoded) == listenSecretBytes {
			return decoded, nil
		}
	}
	return nil, cli.UsageError(errors.New("signing secret must be 32 bytes encoded as Base64URL"))
}

func listenForwardTarget(raw string) (string, error) {
	target, err := url.Parse(raw)
	if err != nil || target.Host == "" || target.User != nil || target.Fragment != "" || (target.Scheme != "https" && (target.Scheme != "http" || (target.Hostname() != "127.0.0.1" && target.Hostname() != "localhost" && target.Hostname() != "::1"))) {
		return "", errors.New("forward-to must be an HTTPS URL or a local loopback HTTP URL")
	}
	return target.String(), nil
}
