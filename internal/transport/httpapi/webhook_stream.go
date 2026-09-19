package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/delivery"
	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
)

const (
	streamPollInterval    = 500 * time.Millisecond
	streamNotification    = time.Second
	streamHeartbeat       = 15 * time.Second
	streamWriteWindow     = 30 * time.Second
	streamEventTypesName  = "event_types"
	streamDeliveredWindow = 4096
)

// events returns one bounded page of decrypted canonical catalogue events.
func (routes *WebhookRoutes) events(writer http.ResponseWriter, request *http.Request) {
	actor, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	eventTypes, err := streamEventTypes(request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	after, limit, query, err := routes.streamCursor(request, eventTypes)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	events, err := routes.stream.Events(request.Context(), actor, after, eventTypes, limit+1)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	more := len(events) > limit
	if more {
		events = events[:limit]
	}
	data := make([]json.RawMessage, 0, len(events))
	for _, event := range events {
		data = append(data, event.Body)
	}
	page := openapiv1.Page{HasMore: more}
	if more {
		encoded, err := json.Marshal(events[len(events)-1].Sequence)
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		next, err := routes.cursors.Encode(actor.TenantScope().ID(), query, encoded)
		if err != nil {
			routes.problem(writer, request, err)
			return
		}
		page.NextCursor = &next
	}
	routes.json(writer, request, struct {
		Data []json.RawMessage `json:"data"`
		Page openapiv1.Page    `json:"page"`
	}{data, page})
}

// eventStream serves the live read-only tenant event feed as Server-Sent
// Events with Last-Event-ID resume and a bounded polling fallback.
func (routes *WebhookRoutes) eventStream(writer http.ResponseWriter, request *http.Request) {
	actor, ok := AccessContext(request.Context())
	if !ok {
		routes.problem(writer, request, access.ErrInvalidCredential)
		return
	}
	eventTypes, err := streamEventTypes(request)
	if err != nil {
		routes.problem(writer, request, invalidRequest(err))
		return
	}
	position, err := routes.resumePosition(request, actor)
	if err != nil {
		if errors.Is(err, delivery.ErrNotFound) {
			routes.problem(writer, request, err)
		} else {
			routes.problem(writer, request, invalidRequest(err))
		}
		return
	}
	flusher, ok := writer.(http.Flusher)
	if !ok {
		routes.problem(writer, request, delivery.ErrInvalid)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Accel-Buffering", "no")
	writer.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(writer, "retry: 1000\n\n"); err != nil {
		return
	}
	flusher.Flush()
	controller := http.NewResponseController(writer)
	wakeups := make(chan struct{}, 1)
	if routes.wakeups != nil {
		go func() {
			for {
				if err := routes.wakeups.Wait(request.Context(), actor.TenantScope().ID()); err != nil {
					return
				}
				select {
				case wakeups <- struct{}{}:
				default:
				}
			}
		}()
	}
	poll := time.NewTicker(streamPollInterval)
	if routes.wakeups != nil {
		poll = time.NewTicker(streamNotification)
	}
	defer poll.Stop()
	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	cursor := position
	delivered := newStreamDelivered(streamDeliveredWindow)
	for {
		events, err := routes.stream.Events(request.Context(), actor, cursor, eventTypes, delivery.MaximumStreamBatch)
		if err != nil {
			routes.logger.WarnContext(request.Context(), "webhook event stream stopped", "error", err)
			return
		}
		highest := cursor
		for _, event := range events {
			if event.Sequence > highest {
				highest = event.Sequence
			}
			if !delivered.add(event.ID) {
				continue
			}
			if err := writeEventFrame(writer, event); err != nil {
				return
			}
			_ = controller.SetWriteDeadline(time.Now().Add(streamWriteWindow))
		}
		if advanced := highest - delivery.StreamCommitLag; advanced > cursor {
			cursor = advanced
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		select {
		case <-request.Context().Done():
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprint(writer, ": heartbeat\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-wakeups:
		case <-poll.C:
		}
	}
}

func writeEventFrame(writer http.ResponseWriter, event delivery.StreamEvent) error {
	if _, err := fmt.Fprintf(writer, "id: %d\nevent: webhook.event\n", int64(event.Sequence)); err != nil {
		return err
	}
	if _, err := fmt.Fprint(writer, "data: "); err != nil {
		return err
	}
	if _, err := writer.Write(event.Body); err != nil {
		return err
	}
	_, err := fmt.Fprint(writer, "\n\n")
	return err
}

func (routes *WebhookRoutes) resumePosition(request *http.Request, actor access.Context) (delivery.StreamPosition, error) {
	last := strings.TrimSpace(request.Header.Get("Last-Event-ID"))
	if last == "" {
		return routes.stream.LatestPosition(request.Context(), actor)
	}
	parsed, err := strconv.ParseInt(last, 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New("last-event-id is invalid")
	}
	return delivery.StreamPosition(parsed), nil
}

// streamDelivered bounds in-memory duplicate suppression for the commit-lag
// reread window. Event-ID deduplication remains the receiver's contract.
type streamDelivered struct {
	seen  map[string]struct{}
	order []string
	next  int
}

func newStreamDelivered(capacity int) *streamDelivered {
	return &streamDelivered{seen: make(map[string]struct{}, capacity), order: make([]string, 0, capacity)}
}

func (set *streamDelivered) add(identifier string) bool {
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

func streamEventTypes(request *http.Request) ([]string, error) {
	values, exists := request.URL.Query()[streamEventTypesName]
	if !exists {
		return nil, nil
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return nil, errors.New("event_types must appear once with at least one value")
	}
	entries := strings.Split(values[0], ",")
	if len(entries) > 64 {
		return nil, errors.New("event_types is bounded to 64 entries")
	}
	result := make([]string, 0, len(entries))
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, errors.New("event_types contains an empty entry")
		}
		result = append(result, entry)
	}
	return result, nil
}

func (routes *WebhookRoutes) streamCursor(request *http.Request, eventTypes []string) (delivery.StreamPosition, int, string, error) {
	limit, token, err := parseStreamQuery(request)
	if err != nil {
		return 0, 0, "", err
	}
	query := "webhooks:events;types=" + strings.Join(eventTypes, ",") + ";limit=" + strconv.Itoa(limit)
	var after delivery.StreamPosition
	if token != "" {
		actor, _ := AccessContext(request.Context())
		claims, err := routes.cursors.Decode(token, actor.TenantScope().ID(), query)
		if err != nil {
			return 0, 0, "", err
		}
		if err := json.Unmarshal(claims.Position, &after); err != nil {
			return 0, 0, "", err
		}
	}
	return after, limit, query, nil
}

func parseStreamQuery(request *http.Request) (int, string, error) {
	query := request.URL.Query()
	for name, values := range query {
		if name != "limit" && name != "cursor" && name != streamEventTypesName {
			return 0, "", fmt.Errorf("unknown query parameter %q", name)
		}
		if len(values) != 1 {
			return 0, "", fmt.Errorf("query parameter %q must appear once", name)
		}
	}
	limit := 25
	if encoded := query.Get("limit"); encoded != "" {
		parsed, err := strconv.Atoi(encoded)
		if err != nil || parsed < 1 || parsed > 100 {
			return 0, "", errors.New("limit must be from 1 to 100")
		}
		limit = parsed
	}
	return limit, query.Get("cursor"), nil
}

func isEventStreamRequest(request *http.Request) bool {
	if request.Method != http.MethodGet {
		return false
	}
	for _, value := range strings.Split(request.Header.Get("Accept"), ",") {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "text/event-stream") {
			return true
		}
	}
	return false
}
